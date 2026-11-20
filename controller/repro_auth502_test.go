package controller

// Temporary local reproduction harness for req-6 (502/400 on free-source
// authorization start endpoints). This file is excluded from normal builds.
// It spins up an isolated sqlite-backed gateway with the REAL route table and
// exercises each start endpoint exactly the way the web client does, capturing
// status codes and bodies as evidence.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type reproStep struct {
	Name   string
	Method string
	Path   string
	Body   string
	Status int
	Resp   string
}

// registerReproSelfChannelRoutes mirrors the self-channel route group for the
// evidence harness only; identity is injected by the engine middleware above
// instead of the session-cookie middleware.
func registerReproSelfChannelRoutes(g *gin.RouterGroup) {
	self := g.Group("/channel/self")
	self.POST("/oauth/broker/:provider/start", SelfBrokerAccountStart)
	self.POST("/oauth/raccoon/url/start", SelfRaccoonURLStart)
	self.POST("/oauth/trae/url/start", SelfTraeURLStart)
	self.POST("/oauth/zcode/start", SelfZCodeOAuthStart)
}

func TestReproAuth502Harness(t *testing.T) {
	if os.Getenv("REPRO_502") == "" {
		t.Skip("evidence harness; run with REPRO_502=1")
	}
	outDir := os.Getenv("REPRO_OUT")
	if outDir == "" {
		wd, err := os.Getwd()
		require.NoError(t, err)
		outDir = filepath.Join(wd, "..", "_evidence", "gateway-next-20261120", "auth-502")
	}
	require.NoError(t, os.MkdirAll(outDir, 0o755))

	scenarios := []struct {
		name          string
		serverAddress string
		trustedOrigin string
	}{
		{"http-local", "http://localhost:3100", "http://localhost:3100"},
		{"https-deployed", "https://gateway.example", "https://gateway.example"},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			steps := runReproScenario(t, scenario.serverAddress, scenario.trustedOrigin)
			raw, err := json.MarshalIndent(steps, "", "  ")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(outDir, "repro-"+scenario.name+".json"), raw, 0o644))
		})
	}
}

func runReproScenario(t *testing.T, serverAddress, trustedOrigin string) []reproStep {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousCache, previousRedis, previousSQLite := common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath
	oldAddress, oldSecret, oldConfigured := system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured
	oldEnabled, oldLimit := operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser
	oldTrusted := common.SessionCookieTrustedURLs
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath = previousMaster, previousCache, previousRedis, previousSQLite
		system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = oldAddress, oldSecret, oldConfigured
		operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = oldEnabled, oldLimit
		common.SessionCookieTrustedURLs = oldTrusted
	})
	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = false, false, false
	common.SQLitePath = filepath.Join(t.TempDir(), "repro.db")
	require.NoError(t, model.InitDB())
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	model.LOG_DB = model.DB
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Log{}, &model.UserSession{}, &model.AuthFlow{}, &model.Option{}))
	system_setting.ServerAddress = serverAddress
	common.CryptoSecret, common.CryptoSecretConfigured = "repro-502-encryption-key", true
	operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = true, 50
	common.SessionCookieTrustedURLs = []string{trustedOrigin}
	if strings.HasPrefix(trustedOrigin, "http://") {
		// Simulate local dev: no trusted origin configured (CSRF off) but
		// ServerAddress still http. Production requires https trusted URLs.
		common.SessionCookieTrustedURLs = nil
	}

	user := &model.User{Username: "repro-user-" + serverAddress, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("auth_version", 1).Error)
	mac := hmac.New(sha256.New, []byte(common.SessionSecret))
	_, _ = mac.Write([]byte("new-api/auth/refresh/v1"))
	session := &model.UserSession{
		SID:             common.GetUUID(),
		UserID:          user.Id,
		Version:         1,
		UserAuthVersion: 1,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     common.GenerateHMACWithKey(mac.Sum(nil), "repro-refresh"),
		LoginMethod:     "password",
		ExpiresAt:       time.Now().Add(time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(session).Error)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if uid := c.GetHeader("X-Repro-Uid"); uid != "" {
			var id int
			_, _ = fmt.Sscanf(uid, "%d", &id)
			c.Set("id", id)
			c.Set("session_id", c.GetHeader("X-Repro-Sid"))
			c.Set("auth_version", int64(1))
			c.Set("session_version", int64(1))
			c.Set("role", common.RoleCommonUser)
		}
		c.Next()
	})
	apiRouter := engine.Group("/api")
	registerReproSelfChannelRoutes(apiRouter)
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)

	client := &http.Client{Timeout: 10 * time.Second}
	doStep := func(name, method, path, body string) reproStep {
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader([]byte(body)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", trustedOrigin)
		req.Header.Set("X-Account-Action", "authorize")
		req.Header.Set("X-Repro-Uid", fmt.Sprint(user.Id))
		req.Header.Set("X-Repro-Sid", session.SID)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		require.NoError(t, err)
		return reproStep{Name: name, Method: method, Path: path, Body: body, Status: resp.StatusCode, Resp: string(raw)}
	}

	steps := []reproStep{
		doStep("cline", "POST", "/api/channel/self/oauth/broker/cline/start", `{}`),
		doStep("qoder-cn", "POST", "/api/channel/self/oauth/broker/qoder/start", `{"realm":"cn"}`),
		doStep("qoder-global", "POST", "/api/channel/self/oauth/broker/qoder/start", `{"realm":"global"}`),
		doStep("nous", "POST", "/api/channel/self/oauth/broker/nous/start", `{}`),
		doStep("raccoon", "POST", "/api/channel/self/oauth/raccoon/url/start", `{}`),
		doStep("trae", "POST", "/api/channel/self/oauth/trae/url/start", `{"realm":"cn","confirmRotation":true}`),
		doStep("zcode-bigmodel", "POST", "/api/channel/self/oauth/zcode/start", `{"realm":"bigmodel"}`),
		doStep("zcode-zai", "POST", "/api/channel/self/oauth/zcode/start", `{"realm":"zai"}`),
	}
	return steps
}

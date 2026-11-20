package controller

// Scenario matrix for req-6: exact web-client payloads against the real start
// handlers with the provider transport intercepted. Evidence harness only;
// run with REPRO_502=1. Synthetic fixture hosts/credentials never leave the
// process: every provider client transport is replaced before any start runs.

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
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const reproProxyURL = "http://127.0.0.1:9"

// reproAccountSession builds an isolated sqlite gateway with one active
// dashboard session, mirroring the identity middleware contract.
func reproAccountSession(t *testing.T, serverAddress string) (int, *model.UserSession) {
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
	common.SQLitePath = filepath.Join(t.TempDir(), "repro-matrix.db")
	require.NoError(t, model.InitDB())
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	model.LOG_DB = model.DB
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Log{}, &model.UserSession{}, &model.AuthFlow{}, &model.Option{}))
	system_setting.ServerAddress = serverAddress
	common.CryptoSecret, common.CryptoSecretConfigured = "repro-502-matrix-key", true
	operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = true, 50
	common.SessionCookieTrustedURLs = []string{serverAddress}

	user := &model.User{Username: "repro-matrix-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
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
	return user.Id, session
}

// reproProxyEndpoint registers one usable user_pool endpoint so egress
// resolution succeeds and the provider stage is reached.
func reproProxyEndpoint(t *testing.T, uid int) {
	t.Helper()
	require.NoError(t, model.EnsureProxyPoolSchema())
	endpoint := model.ProxyEndpoint{UserId: uid, Name: "repro-egress", Kind: "http", Url: reproProxyURL, Enabled: true}
	require.NoError(t, model.DB.Create(&endpoint).Error)
}

// reproCall issues one start request the way the web client does: JSON body,
// Origin trusted, account-action header, and the initiating dashboard identity.
func reproCall(t *testing.T, uid int, session *model.UserSession, method, path, body string) reproStep {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", uid)
	c.Set("session_id", session.SID)
	c.Set("auth_version", int64(1))
	c.Set("session_version", int64(1))
	c.Set("role", common.RoleCommonUser)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Origin", system_setting.ServerAddress)
	c.Request.Header.Set("Sec-Fetch-Site", "same-origin")
	c.Request.Header.Set("X-Account-Action", "authorize")
	switch path {
	case "/api/channel/self/oauth/broker/cline/start":
		c.Params = gin.Params{{Key: "provider", Value: "cline"}}
		SelfBrokerAccountStart(c)
	case "/api/channel/self/oauth/broker/qoder/start":
		c.Params = gin.Params{{Key: "provider", Value: "qoder"}}
		SelfBrokerAccountStart(c)
	case "/api/channel/self/oauth/broker/nous/start":
		c.Params = gin.Params{{Key: "provider", Value: "nous"}}
		SelfBrokerAccountStart(c)
	case "/api/channel/self/oauth/zcode/start":
		SelfZCodeOAuthStart(c)
	case "/api/channel/self/oauth/raccoon/url/start":
		SelfRaccoonURLStart(c)
	case "/api/channel/self/oauth/trae/url/start":
		SelfTraeURLStart(c)
	default:
		t.Fatalf("unknown repro path %s", path)
	}
	raw, err := io.ReadAll(io.LimitReader(recorder.Result().Body, 1<<16))
	require.NoError(t, err)
	return reproStep{Name: path, Method: method, Path: path, Body: body, Status: recorder.Code, Resp: string(raw)}
}

func TestReproAuth502Matrix(t *testing.T) {
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

	type providerStub struct {
		name   string
		status int
		body   string
	}
	stubs := []providerStub{
		{"provider-down", 0, ""}, // connection error
		{"provider-400", 400, `{"error":"invalid_client"}`},
		{"provider-500", 500, `{"error":"server_error"}`},
	}
	starts := []struct {
		name string
		path string
		body string
	}{
		{"cline", "/api/channel/self/oauth/broker/cline/start", `{}`},
		{"qoder-cn", "/api/channel/self/oauth/broker/qoder/start", `{"realm":"cn"}`},
		{"nous", "/api/channel/self/oauth/broker/nous/start", `{}`},
		{"zcode-bigmodel", "/api/channel/self/oauth/zcode/start", `{"realm":"bigmodel"}`},
		{"raccoon", "/api/channel/self/oauth/raccoon/url/start", `{}`},
		{"trae", "/api/channel/self/oauth/trae/url/start", `{"realm":"cn","confirmRotation":true}`},
	}
	for _, stub := range stubs {
		t.Run(stub.name, func(t *testing.T) {
			uid, session := reproAccountSession(t, "https://gateway.example")
			reproProxyEndpoint(t, uid)
			transport := selfContractTransport(func(r *http.Request) (*http.Response, error) {
				if stub.status == 0 {
					return nil, fmt.Errorf("dial tcp: fixture provider unreachable")
				}
				return selfContractResponse(stub.status, stub.body)
			})
			setSelfContractProxyTransport(t, reproProxyURL, transport)
			setSelfContractTransport(t, transport)

			steps := make([]reproStep, 0, len(starts))
			for _, start := range starts {
				step := reproCall(t, uid, session, http.MethodPost, start.path, start.body)
				step.Name = start.name
				steps = append(steps, step)
			}
			raw, err := json.MarshalIndent(steps, "", "  ")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(outDir, "matrix-"+stub.name+".json"), raw, 0o644))
		})
	}
}

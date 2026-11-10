package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/nodemonitor"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestM5NodeManagementEndToEnd 覆盖 M5 后端验收链路：
// 建节点 → 巡检 PASS→FAIL→webhook 告警 → denylist 硬拒绝 → plan diff → apply 全链（mock GraphQL）→ 加密落库。
func TestM5NodeManagementEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// ---- 全局状态隔离 ----
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousSecret, previousConfigured := common.CryptoSecret, common.CryptoSecretConfigured
	previousMaster := common.IsMasterNode
	previousRedisEnabled := common.RedisEnabled
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.CryptoSecret, common.CryptoSecretConfigured = previousSecret, previousConfigured
		common.IsMasterNode = previousMaster
		common.RedisEnabled = previousRedisEnabled
	})
	common.RedisEnabled = false

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Option{}, &model.AuditLog{}, &model.Node{}, &model.NodeAudit{}))
	model.DB = database
	model.LOG_DB = database
	common.CryptoSecret = "m5-e2e-crypto-secret"
	common.CryptoSecretConfigured = true
	common.IsMasterNode = true
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()

	// ---- 平台配置 ----
	previousThreshold := operation_setting.NodeBalanceAlertThreshold
	previousWebhook := operation_setting.NodeAlertWebhookUrl
	previousEmail := operation_setting.NodeAlertEmail
	previousTemplate := operation_setting.NodeDeployEnvTemplate
	previousPrimary := operation_setting.NodePrimaryDomain
	previousAutoFailover := operation_setting.NodeAutoFailover
	previousMonitor := operation_setting.NodeMonitorEnabled
	t.Cleanup(func() {
		operation_setting.NodeBalanceAlertThreshold = previousThreshold
		operation_setting.NodeAlertWebhookUrl = previousWebhook
		operation_setting.NodeAlertEmail = previousEmail
		operation_setting.NodeDeployEnvTemplate = previousTemplate
		operation_setting.NodePrimaryDomain = previousPrimary
		operation_setting.NodeAutoFailover = previousAutoFailover
		operation_setting.NodeMonitorEnabled = previousMonitor
	})
	operation_setting.NodeBalanceAlertThreshold = 1
	operation_setting.NodeAlertEmail = ""
	operation_setting.NodeDeployEnvTemplate = "SESSION_SECRET=\nCRYPTO_SECRET=\nMISE_PYTHON_GITHUB_ATTESTATIONS=false\n"
	operation_setting.NodePrimaryDomain = "" // failover plan 会因缺主域名安全跳过
	operation_setting.NodeAutoFailover = false
	operation_setting.NodeMonitorEnabled = true
	nodemonitor.ResetForTest()
	require.NoError(t, operation_setting.SetNodeDenylistJSON("[]"))
	require.NoError(t, operation_setting.SetNodeAllowlistJSON("[]"))

	// ---- Mock /api/status ----
	var healthy atomic.Bool
	healthy.Store(true)
	statusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		if healthy.Load() {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{"status":"ok"}}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer statusServer.Close()

	// ---- Mock 告警 webhook ----
	var alertsMu sync.Mutex
	alerts := make([]map[string]any, 0)
	alertServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
			alertsMu.Lock()
			alerts = append(alerts, payload)
			alertsMu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer alertServer.Close()
	operation_setting.NodeAlertWebhookUrl = alertServer.URL

	// ---- Mock Railway GraphQL ----
	railwayMock := newM5RailwayMock(t)
	railwayServer := httptest.NewServer(http.HandlerFunc(railwayMock.handle))
	defer railwayServer.Close()
	t.Setenv("RAILWAY_GRAPHQL_ENDPOINT", railwayServer.URL)
	t.Setenv("NODE_DEPLOY_POLL_INTERVAL_MS", "1")

	// ---- 测试用 root API ----
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Set("username", "root")
		c.Set("role", common.RoleRootUser)
		c.Next()
	})
	node := engine.Group("/api/node")
	node.GET("", controller.GetNodes)
	node.POST("", controller.CreateNode)
	node.PUT("", controller.UpdateNode)
	node.DELETE("/:id", controller.DeleteNode)
	node.POST("/:id/check", controller.CheckNode)
	node.POST("/deploy/plan", controller.NodeDeployPlan)
	node.POST("/deploy/apply", controller.NodeDeployApply)
	node.GET("/settings", controller.GetNodeSettings)
	node.PUT("/settings", controller.UpdateNodeSettings)
	node.GET("/audit", controller.GetNodeAudit)

	doJSON := func(method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else {
			encoded, err := json.Marshal(body)
			require.NoError(t, err)
			reader = bytes.NewReader(encoded)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		var decoded map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
		return recorder, decoded
	}

	// ---- 1) root 建 mock 节点（指向本地 /api/status）----
	recorder, created := doJSON(http.MethodPost, "/api/node", map[string]any{
		"name": "e2e-railway-node", "provider": "railway",
		"api_token": "railway-token-e2e", "workspace_id": "ws_e2e",
		"project_id": "proj_e2e", "repo": "linlong5201314/newapi-public", "branch": "main",
		"service_url": statusServer.URL, "enabled": true,
	})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.True(t, created["success"].(bool))
	nodeID := int(created["data"].(map[string]any)["id"].(float64))
	require.True(t, created["data"].(map[string]any)["healthy"].(bool))

	// 加密落库：sqlite 原始列是密文，读取透明解密。
	var rawToken string
	require.NoError(t, database.Model(&model.Node{}).Where("id = ?", nodeID).Pluck("api_token_enc", &rawToken).Error)
	require.True(t, strings.HasPrefix(rawToken, common.EncryptedSecretPrefix), "token at rest must be encrypted, got %q", rawToken)
	require.NotContains(t, rawToken, "railway-token-e2e")
	loaded, err := model.GetNodeById(nodeID)
	require.NoError(t, err)
	require.Equal(t, "railway-token-e2e", loaded.DecryptedToken())

	// 列表脱敏：不回显 token。
	recorder, listed := doJSON(http.MethodGet, "/api/node", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "railway-token-e2e")
	nodeItems := listed["data"].([]any)
	require.Len(t, nodeItems, 1)
	item := nodeItems[0].(map[string]any)
	require.True(t, item["has_api_token"].(bool))
	require.NotEmpty(t, item["api_token_hint"])

	// ---- 2) 巡检 PASS → FAIL → webhook 告警 ----
	recorder, check := doJSON(http.MethodPost, "/api/node/"+itoa(nodeID)+"/check", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, check["data"].(map[string]any)["healthy"].(bool))

	healthy.Store(false)
	nodemonitor.RunOnce(context.Background())
	alertsMu.Lock()
	alertCount := len(alerts)
	var lastAlert map[string]any
	if alertCount > 0 {
		lastAlert = alerts[alertCount-1]
	}
	alertsMu.Unlock()
	require.Greater(t, alertCount, 0, "expected unhealthy webhook alert")
	require.Equal(t, "unhealthy", lastAlert["kind"])
	require.Equal(t, "e2e-railway-node", lastAlert["node_name"])

	// 恢复健康（后续 plan/apply 需要节点可用）。
	healthy.Store(true)
	_, check = doJSON(http.MethodPost, "/api/node/"+itoa(nodeID)+"/check", nil)
	require.True(t, check["data"].(map[string]any)["healthy"].(bool))

	// ---- 3) denylist 命中 → 部署 plan 硬拒绝 ----
	recorder, _ = doJSON(http.MethodPut, "/api/node/settings", map[string]any{"nodeDenylist": []string{"proj_e2e"}})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	recorder, denied := doJSON(http.MethodPost, "/api/node/deploy/plan", map[string]any{"nodeId": nodeID})
	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
	require.Equal(t, "denylisted", denied["code"])
	recorder, _ = doJSON(http.MethodPut, "/api/node/settings", map[string]any{"nodeDenylist": []string{}})
	require.Equal(t, http.StatusOK, recorder.Code)

	// ---- 4) plan diff 正确 ----
	recorder, planResp := doJSON(http.MethodPost, "/api/node/deploy/plan", map[string]any{
		"nodeId": nodeID,
		"envOverrides": map[string]string{
			"SESSION_SECRET": "new-session-secret",
			"CRYPTO_SECRET":  "new-crypto-secret",
		},
	})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	plan := planResp["data"].(map[string]any)
	planID := plan["planId"].(string)
	require.NotEmpty(t, planID)
	actions := plan["actions"].([]any)
	actionIndex := map[string]map[string]any{}
	for _, raw := range actions {
		action := raw.(map[string]any)
		if variable, ok := action["variable"].(string); ok {
			actionIndex[variable] = action
		}
	}
	require.Equal(t, "update", actionIndex["SESSION_SECRET"]["op"])
	// 密钥值在 API 面脱敏（内部保留明文执行）。
	require.Equal(t, "******", actionIndex["SESSION_SECRET"]["to"])
	require.Equal(t, "create", actionIndex["CRYPTO_SECRET"]["op"])
	require.Equal(t, "******", actionIndex["CRYPTO_SECRET"]["to"])
	require.Equal(t, "create", actionIndex["MISE_PYTHON_GITHUB_ATTESTATIONS"]["op"])
	require.Equal(t, "false", actionIndex["MISE_PYTHON_GITHUB_ATTESTATIONS"]["to"])
	require.Equal(t, "delete", actionIndex["EXTRA_VAR"]["op"])
	require.Equal(t, true, actionIndex["EXTRA_VAR"]["skipped"])
	warnings := plan["warnings"].([]any)
	require.Contains(t, strings.Join(toStrings(warnings), " | "), "EXTRA_VAR")
	require.NotContains(t, recorder.Body.String(), "new-session-secret")

	// ---- 5) apply 走通 mock GraphQL 全链 ----
	recorder, applied := doJSON(http.MethodPost, "/api/node/deploy/apply", map[string]any{
		"planId": planID, "confirm": true,
	})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	result := applied["data"].(map[string]any)
	require.Equal(t, "SUCCESS", result["deploymentStatus"])
	require.Equal(t, "e2e-node.up.railway.app", result["serviceDomain"])

	railwayMock.mu.Lock()
	upserts := append([]map[string]any{}, railwayMock.upserts...)
	deployCalls := railwayMock.deployCalls
	serviceDomainInput := railwayMock.serviceDomainInput
	pollCount := railwayMock.pollCount
	githubCalls := railwayMock.githubCalls
	railwayMock.mu.Unlock()
	require.GreaterOrEqual(t, len(upserts), 3)
	for _, upsert := range upserts {
		require.Equal(t, true, upsert["skipDeploys"], "every variableUpsert must carry skipDeploys")
	}
	require.Equal(t, 1, deployCalls, "variables must be batched into exactly one serviceInstanceDeploy")
	require.Equal(t, 1, githubCalls, "service should be created via githubRepoDeploy when ServiceId is empty")
	require.GreaterOrEqual(t, pollCount, 3, "deployment polling should observe QUEUED→BUILDING→SUCCESS")
	require.NotNil(t, serviceDomainInput)
	_, hasProjectID := serviceDomainInput["projectId"]
	require.False(t, hasProjectID, "serviceDomainCreate must not send projectId")
	require.Len(t, serviceDomainInput, 3, "serviceDomainCreate only accepts environmentId/serviceId/targetPort")

	updated, err := model.GetNodeById(nodeID)
	require.NoError(t, err)
	require.Equal(t, "svc_e2e", updated.ServiceId)
	require.Equal(t, "e2e-node.up.railway.app", updated.ServiceDomain)
	require.Equal(t, "env_e2e", updated.EnvironmentId)

	// ---- 6) 审计落库 ----
	recorder, auditResp := doJSON(http.MethodGet, "/api/node/audit?nodeId="+itoa(nodeID), nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	auditData := auditResp["data"].(map[string]any)
	require.GreaterOrEqual(t, int(auditData["total"].(float64)), 3)
	require.NotEmpty(t, auditData["items"])
}

// m5RailwayMock 有状态的 Railway GraphQL mock。
type m5RailwayMock struct {
	t *testing.T

	mu             sync.Mutex
	vars           map[string]string
	upserts        []map[string]any
	deployCalls    int
	pollCount      int
	deployed       bool
	serviceCreated bool
	githubCalls    int

	serviceDomainInput map[string]any
}

func newM5RailwayMock(t *testing.T) *m5RailwayMock {
	return &m5RailwayMock{
		t:    t,
		vars: map[string]string{"SESSION_SECRET": "old-session-secret", "EXTRA_VAR": "legacy"},
	}
}

func (m *m5RailwayMock) handle(w http.ResponseWriter, r *http.Request) {
	var call struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
		m.t.Errorf("decode railway request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	respond := func(data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case strings.Contains(call.Query, "projectCreate"):
		respond(map[string]any{"projectCreate": map[string]any{
			"id": "proj_created", "name": "created",
			"environments": map[string]any{"edges": []any{map[string]any{"node": map[string]any{"id": "env_created", "name": "production"}}}},
			"services":     map[string]any{"edges": []any{}},
		}})
	case strings.Contains(call.Query, "githubRepoDeploy"):
		m.githubCalls++
		m.serviceCreated = true
		respond(map[string]any{"githubRepoDeploy": "wf_e2e"})
	case strings.Contains(call.Query, "serviceInstanceDeploy"):
		m.deployCalls++
		m.deployed = true
		respond(map[string]any{"serviceInstanceDeploy": true})
	case strings.Contains(call.Query, "serviceDomainCreate"):
		input, _ := call.Variables["input"].(map[string]any)
		m.serviceDomainInput = input
		respond(map[string]any{"serviceDomainCreate": map[string]any{"id": "dom_e2e", "domain": "e2e-node.up.railway.app"}})
	case strings.Contains(call.Query, "variableUpsert"):
		input, _ := call.Variables["input"].(map[string]any)
		m.upserts = append(m.upserts, input)
		if name, ok := input["name"].(string); ok {
			if value, ok := input["value"].(string); ok {
				m.vars[name] = value
			}
		}
		respond(map[string]any{"variableUpsert": true})
	case strings.Contains(call.Query, "variableDelete"):
		input, _ := call.Variables["input"].(map[string]any)
		if name, ok := input["name"].(string); ok {
			delete(m.vars, name)
		}
		respond(map[string]any{"variableDelete": true})
	case strings.Contains(call.Query, "deployments("):
		if !m.deployed {
			respond(map[string]any{"deployments": map[string]any{"edges": []any{}}})
			return
		}
		statuses := []string{"QUEUED", "BUILDING", "SUCCESS"}
		status := statuses[min(m.pollCount, len(statuses)-1)]
		m.pollCount++
		respond(map[string]any{"deployments": map[string]any{
			"edges": []any{map[string]any{"node": map[string]any{"id": "dep_e2e_new", "status": status}}},
		}})
	case strings.Contains(call.Query, "customer"):
		respond(map[string]any{"workspace": map[string]any{"customer": map[string]any{
			"creditBalance": 5.0, "currentUsage": 1.0, "remainingUsageCreditBalance": 5.0,
			"hasExhaustedFreePlan": false, "trialDaysRemaining": 7,
			"usageLimit": map[string]any{"softLimit": 10.0, "hardLimit": 10.0, "isOverLimit": false},
		}}})
	case strings.Contains(call.Query, "estimatedUsage"):
		respond(map[string]any{"estimatedUsage": 1.5})
	case strings.Contains(call.Query, "variables("):
		vars := make(map[string]any, len(m.vars))
		for key, value := range m.vars {
			vars[key] = value
		}
		respond(map[string]any{"variables": vars})
	case strings.Contains(call.Query, "workspace(workspaceId"):
		services := []any{}
		if m.serviceCreated {
			services = append(services, map[string]any{"node": map[string]any{"id": "svc_e2e", "name": "newapi-public"}})
		}
		respond(map[string]any{"workspace": map[string]any{"projects": map[string]any{"edges": []any{
			map[string]any{"node": map[string]any{
				"id": "proj_e2e", "name": "e2e",
				"services": map[string]any{"edges": services},
			}},
		}}}})
	case strings.Contains(call.Query, "environments("):
		respond(map[string]any{"environments": map[string]any{"edges": []any{
			map[string]any{"node": map[string]any{"id": "env_e2e", "name": "production"}},
		}}})
	case strings.Contains(call.Query, "domains("):
		respond(map[string]any{"domains": map[string]any{"customDomains": []any{}, "serviceDomains": []any{}}})
	default:
		m.t.Errorf("unexpected railway GraphQL query: %s", call.Query)
		respond(map[string]any{})
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func toStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

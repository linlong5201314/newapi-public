// node_railway_services_test.go 覆盖节点管理页签「Railway 服务」的只读接口：
//   - 未配置 NodeRailwayServices 时返回 configured:false 且不报错（fail-soft）；
//   - 单服务失败降级为该服务 error，其余服务与余额不受影响；
//   - 同一 workspace 下多台服务只查询一次余额（不乘服务数）；
//   - token 密文绝不进入响应，响应只暴露 has_api_token 与 has 字段；
//   - 接口路由已挂在 RootAuth 组（router/node-router.go），无需在每个请求上重复鉴权。
package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/noderailway"
	"github.com/QuantumNous/new-api/service/railway"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// fakeRailwayClient 单测 mock：按 serviceId 返回不同行为，记录调用次数。
type fakeRailwayClient struct {
	serviceInstanceFunc func(serviceId, environmentId string) (*railway.ServiceInstance, error)
	workspaceBilling    *railway.WorkspaceBilling
	workspaceBillingErr error
	estimatedUsage      *float64
	estimatedUsageErr   error

	serviceInstanceCalls int
	workspaceBillingCall int
	estimatedUsageCalls  int
}

func (f *fakeRailwayClient) ServiceInstance(_ context.Context, serviceId, environmentId string) (*railway.ServiceInstance, error) {
	f.serviceInstanceCalls++
	if f.serviceInstanceFunc == nil {
		return nil, errors.New("not configured")
	}
	return f.serviceInstanceFunc(serviceId, environmentId)
}

func (f *fakeRailwayClient) WorkspaceBilling(_ context.Context, _ string) (*railway.WorkspaceBilling, error) {
	f.workspaceBillingCall++
	return f.workspaceBilling, f.workspaceBillingErr
}

func (f *fakeRailwayClient) EstimatedUsage(_ context.Context, _ string) (*float64, error) {
	f.estimatedUsageCalls++
	return f.estimatedUsage, f.estimatedUsageErr
}

// setupRailwayServicesTest 初始化隔离 DB + Option。
//
// 与 modelManagementDB 不同，这里直接打开 :memory: SQLite，避免 Windows 下
// t.TempDir() 与 SQLite WAL 文件锁冲突（同套 fixtures 在既有测试上也会偶发
// RemoveAll 失败，与本次改动无关）。结束通过 cleanup 还原 model.DB 与 OptionMap。
func setupRailwayServicesTest(t *testing.T) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.Node{}))

	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousOptions := common.OptionMap
	model.DB, model.LOG_DB = database, database
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		common.OptionMap = previousOptions
		model.DB, model.LOG_DB = previousDB, previousLogDB
		connection, err := database.DB()
		if err == nil {
			require.NoError(t, connection.Close())
		}
	})
}

// resetRailwayServicesOption 在每个用例前后清空 NodeRailwayServices 注册表，
// 避免 OptionMap 持久化串味（SetupTestDB 之间共享）。
func resetRailwayServicesOption(t *testing.T) {
	t.Helper()
	require.NoError(t, operation_setting.SetNodeRailwayServicesJSON(""))
	t.Cleanup(func() {
		require.NoError(t, operation_setting.SetNodeRailwayServicesJSON(""))
	})
}

func callGetNodeRailwayServices(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/node/railway-services", nil)
	context.Set("role", common.RoleRootUser)
	GetNodeRailwayServices(context)
	return recorder
}

func TestGetNodeRailwayServicesMissingConfigIsGraceful(t *testing.T) {
	setupRailwayServicesTest(t)
	resetRailwayServicesOption(t)

	recorder := callGetNodeRailwayServices(t)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, `"success":true`)
	assert.Contains(t, body, `"configured":false`)
	assert.Contains(t, body, `"services":[]`)
	assert.Contains(t, body, `"billing":[]`)
}

func TestGetNodeRailwayServicesFailsSoftPerServiceAndDoesNotLeakToken(t *testing.T) {
	setupRailwayServicesTest(t)
	resetRailwayServicesOption(t)

	// 注册 2 台服务共用同一 workspace，使用明文 plain: 前缀 token（绕过加密解密链路），
	// 显式触发 sanitizeError 路径以验证 token 绝不回流到响应。
	secret := "plain:super-secret-token-abcdef"
	entries := fmt.Sprintf(`[
		{"name":"svc-a","workspace_id":"ws-1","project_id":"p-1","environment_id":"env-1","service_id":"svc-a","api_token":%q},
		{"name":"svc-b","workspace_id":"ws-1","project_id":"p-1","environment_id":"env-1","service_id":"svc-b","api_token":%q}
	]`, secret, secret)
	require.NoError(t, operation_setting.SetNodeRailwayServicesJSON(entries))

	usage := 12.34
	client := &fakeRailwayClient{
		serviceInstanceFunc: func(serviceId, _ string) (*railway.ServiceInstance, error) {
			if serviceId == "svc-a" {
				return &railway.ServiceInstance{
					Id: "inst-a", ServiceId: "svc-a", ServiceName: "svc-a",
					LatestDeployment: &railway.Deployment{
						Id: "dep-1", Status: "SUCCESS", Url: "https://svc-a.up.railway.app",
						StaticUrl: "https://svc-a-static.up.railway.app",
						CreatedAt: "2026-11-19T10:00:00Z", UpdatedAt: "2026-11-19T10:05:00Z",
					},
				}, nil
			}
			// svc-b 模拟 Railway 报错；错误信息里故意带 token 字样，验证 sanitizeError 不会原样回显。
			return nil, &railway.APIError{StatusCode: 401, Errors: []railway.GraphQLError{{Message: "Not Authorized for token super-secret-token-abcdef"}}}
		},
		workspaceBilling: &railway.WorkspaceBilling{
			CreditBalance:               floatPtr(20.5),
			CurrentUsage:                &usage,
			RemainingUsageCreditBalance: floatPtr(8.16),
			HasExhaustedFreePlan:        false,
			TrialDaysRemaining:          intPtr(11),
			UsageLimit: &railway.UsageLimit{
				SoftLimit:   floatPtr(50),
				HardLimit:   floatPtr(100),
				IsOverLimit: false,
			},
		},
		estimatedUsage: floatPtr(13.7),
	}
	restore := noderailway.SetClientFactoryForTest(func(token string) noderailway.RailwayClient {
		// 验证服务端解密链路：mock 收到的必须是 plain 原文，而非 crypto: 密文。
		require.Equal(t, "super-secret-token-abcdef", token)
		return client
	})
	t.Cleanup(restore)

	recorder := callGetNodeRailwayServices(t)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()

	// 整体成功，configured:true，服务数组长度=2
	assert.Contains(t, body, `"success":true`)
	assert.Contains(t, body, `"configured":true`)
	assert.Contains(t, body, `"name":"svc-a"`)
	assert.Contains(t, body, `"name":"svc-b"`)

	// svc-a 成功并带 deployment 快照
	assert.Contains(t, body, `"status":"SUCCESS"`)
	assert.Contains(t, body, `"url":"https://svc-a.up.railway.app"`)

	// svc-b 降级为 error，但响应 200
	assert.Contains(t, body, `"status":"error"`)

	// workspace 余额只查询一次（workspace-not-multiplied），即便有两台服务
	assert.Equal(t, 1, client.workspaceBillingCall, "WorkspaceBilling must be deduplicated per workspace")
	assert.Equal(t, 2, client.serviceInstanceCalls, "ServiceInstance must be called once per service")
	assert.Equal(t, 1, client.estimatedUsageCalls, "EstimatedUsage must be deduplicated per workspace")

	// 余额字段：unit/currency/period + 估算标记
	assert.Contains(t, body, `"unit":"USD"`)
	assert.Contains(t, body, `"currency":"USD"`)
	assert.Contains(t, body, `"period":"monthly"`)
	assert.Contains(t, body, `"estimated_usage_is_estimate":true`)
	assert.Contains(t, body, `"usage_limit_soft":50`)
	assert.Contains(t, body, `"usage_limit_hard":100`)
	assert.Contains(t, body, `"trial_days_remaining":11`)

	// token 密文/原文绝不进入响应（api_token_enc 已剥离，错误消息已脱敏）
	assert.NotContains(t, body, "super-secret-token-abcdef")
	assert.NotContains(t, body, "plain:")
	assert.NotContains(t, body, "crypto:")
	// has_api_token 暴露布尔，不暴露值
	assert.Contains(t, body, `"has_api_token":true`)
}

func TestGetNodeRailwayServicesNoTokenIsUnavailableNotError(t *testing.T) {
	setupRailwayServicesTest(t)
	resetRailwayServicesOption(t)

	// 注册两台服务但都不配 token；服务应该返回 unavailable，而非 500。
	require.NoError(t, operation_setting.SetNodeRailwayServicesJSON(`[
		{"name":"svc-no-token","workspace_id":"ws-x","project_id":"p-1","environment_id":"env-1","service_id":"svc-x"}
	]`))

	called := false
	restore := noderailway.SetClientFactoryForTest(func(token string) noderailway.RailwayClient {
		called = true
		require.Empty(t, token, "no token should be passed to the client when none is configured")
		return &fakeRailwayClient{}
	})
	t.Cleanup(restore)

	recorder := callGetNodeRailwayServices(t)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, `"configured":true`)
	assert.Contains(t, body, `"status":"unavailable"`)
	assert.Contains(t, body, `"has_api_token":false`)
	assert.False(t, called, "railway client must not be invoked when no token is configured")
}

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }

// 防止敏感字面量泄漏到响应：如果未来字段重命名/加字段，下面这条用例必须同步失败。
func TestGetNodeRailwayServicesResponseHasNoApiTokenField(t *testing.T) {
	setupRailwayServicesTest(t)
	resetRailwayServicesOption(t)
	require.NoError(t, operation_setting.SetNodeRailwayServicesJSON(`[
		{"name":"svc","workspace_id":"ws","project_id":"p","environment_id":"e","service_id":"s","api_token":"plain:t"}
	]`))
	restore := noderailway.SetClientFactoryForTest(func(_ string) noderailway.RailwayClient {
		return &fakeRailwayClient{
			serviceInstanceFunc: func(_, _ string) (*railway.ServiceInstance, error) { return nil, nil },
			workspaceBilling:    &railway.WorkspaceBilling{},
		}
	})
	t.Cleanup(restore)

	recorder := callGetNodeRailwayServices(t)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	// JSON 字段名黑名单（防止意外回写）
	for _, field := range []string{`"api_token"`, `"apiToken"`, `"api_token_enc"`, `"token"`, `"secret"`} {
		assert.False(t, strings.Contains(body, field), "response must not contain sensitive field %s", field)
	}
}

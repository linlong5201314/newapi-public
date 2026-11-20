// Package noderailway 节点管理页签「Railway 服务」的只读摘要。
//
// 数据源：
//   - 服务清单来自 Option "NodeRailwayServices"（不落前端代码）；
//   - 实时状态通过 service/railway GraphQL 客户端读取，仅查询，不写；
//   - 余额按 workspace 只查一次（同 workspace 下 7 台服务共享一份账单）。
//
// 失败语义：单个服务查询失败不阻塞其它服务；Token 缺失时整项置为
// unavailable，原因写进 error，绝不抛 token 到响应。
package noderailway

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/railway"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// SummaryEntry 单台受管 Railway 服务的只读摘要。
type SummaryEntry struct {
	Name          string `json:"name"`
	WorkspaceId   string `json:"workspace_id"`
	ProjectId     string `json:"project_id"`
	EnvironmentId string `json:"environment_id"`
	ServiceId     string `json:"service_id"`
	ServiceDomain string `json:"service_domain,omitempty"`
	Domain        string `json:"domain,omitempty"`
	HasApiToken   bool   `json:"has_api_token"`

	// LatestDeployment 最新一次部署快照；查询失败时 nil。
	LatestDeployment *DeploymentSnapshot `json:"latest_deployment,omitempty"`

	// 状态：ok | unavailable | error。
	Status string `json:"status"`
	// Error 人类可读的错误原因（不含 token / 密文）。
	Error string `json:"error,omitempty"`
}

// DeploymentSnapshot 最新部署的展示字段。
type DeploymentSnapshot struct {
	Id        string `json:"id,omitempty"`
	Status    string `json:"status,omitempty"`
	Url       string `json:"url,omitempty"`
	StaticUrl string `json:"static_url,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	// CommitSha / CommitShortSha Railway GraphQL 不直接暴露 commit，
	// 本字段保留以便后续接 GitHub 集成时回填；当前恒为空。
	CommitSha      string `json:"commit_sha,omitempty"`
	CommitShortSha string `json:"commit_short_sha,omitempty"`
}

// WorkspaceBillingSummary 工作区级余额（每个 workspace 仅返回一份，不乘服务数）。
type WorkspaceBillingSummary struct {
	WorkspaceId string `json:"workspace_id"`
	// Unit / Currency 用于前端格式化；Railway 当前以美元计价。
	Unit     string `json:"unit"`     // "USD"
	Currency string `json:"currency"` // "USD"
	// Period 计费周期说明；当前月度。
	Period string `json:"period"` // "monthly"

	CreditBalance               *float64 `json:"credit_balance,omitempty"`
	CurrentUsage                *float64 `json:"current_usage,omitempty"`
	RemainingUsageCreditBalance *float64 `json:"remaining_usage_credit_balance,omitempty"`
	HasExhaustedFreePlan        bool     `json:"has_exhausted_free_plan"`
	TrialDaysRemaining          *int     `json:"trial_days_remaining,omitempty"`

	UsageLimitSoft   *float64 `json:"usage_limit_soft,omitempty"`
	UsageLimitHard   *float64 `json:"usage_limit_hard,omitempty"`
	UsageLimitIsOver bool     `json:"usage_limit_is_over"`

	// EstimatedUsage 实时估算（明显标注）；不可用时为 nil。
	EstimatedUsage        *float64 `json:"estimated_usage,omitempty"`
	EstimatedUsageIsGuess bool     `json:"estimated_usage_is_estimate"`

	SampledAt int64  `json:"sampled_at"` // 秒级时间戳
	Fresh     bool   `json:"fresh"`      // 数据是否仍在 5 分钟新鲜窗口内
	Status    string `json:"status"`     // ok | unavailable | error
	Error     string `json:"error,omitempty"`
}

// SummaryResponse 整页只读响应：服务数组 + 按 workspace 去重的余额数组。
type SummaryResponse struct {
	Services   []SummaryEntry            `json:"services"`
	Billing    []WorkspaceBillingSummary `json:"billing"`
	SampledAt  int64                     `json:"sampled_at"`
	Configured bool                      `json:"configured"` // 是否已配置 NodeRailwayServices
}

// RailwayClient 是 railway.Client 的最小可 mock 子集（导出供 controller 测试注入）。
type RailwayClient interface {
	ServiceInstance(ctx context.Context, serviceId, environmentId string) (*railway.ServiceInstance, error)
	WorkspaceBilling(ctx context.Context, workspaceId string) (*railway.WorkspaceBilling, error)
	EstimatedUsage(ctx context.Context, workspaceId string) (*float64, error)
}

// clientFactory 默认实现：每次按 token 现造客户端。
// 测试中可通过 SetClientFactoryForTest 注入 mock。
type clientFactory func(token string) RailwayClient

var (
	clientFactoryMu sync.RWMutex
	factory         clientFactory = func(token string) RailwayClient {
		return railway.NewClient(token)
	}
)

// SetClientFactoryForTest 单测替换 client 构造器；返回还原函数。
func SetClientFactoryForTest(f clientFactory) func() {
	clientFactoryMu.Lock()
	defer clientFactoryMu.Unlock()
	previous := factory
	factory = f
	return func() {
		clientFactoryMu.Lock()
		defer clientFactoryMu.Unlock()
		factory = previous
	}
}

func currentFactory() clientFactory {
	clientFactoryMu.RLock()
	defer clientFactoryMu.RUnlock()
	return factory
}

// FreshnessWindow 余额与部署状态的新鲜窗口（与 nodemonitor.balanceFresh 对齐）。
const FreshnessWindow = 5 * time.Minute

// CollectSummary 读取 Option 注册表并并发抓取每个服务的最新部署 + 每个 workspace 的余额。
//
// 失败语义：单服务失败只置该服务的 status/error；同 workspace 的余额失败
// 只置该 workspace 的 status/error。整体接口 200，绝不抛 5xx。
func CollectSummary(ctx context.Context) (*SummaryResponse, error) {
	entries := operation_setting.NodeRailwayServicesList()
	response := &SummaryResponse{
		Services:   make([]SummaryEntry, 0, len(entries)),
		Billing:    make([]WorkspaceBillingSummary, 0),
		SampledAt:  common.GetTimestamp(),
		Configured: len(entries) > 0,
	}
	if len(entries) == 0 {
		return response, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// 建立按 workspace 分组的 token 池：第一个带 token 的 entry / 本地节点为该 workspace 提供认证。
	tokens := tokensByWorkspace(entries)

	// 1) 并发抓取每个服务的最新部署
	services := make([]SummaryEntry, len(entries))
	var wg sync.WaitGroup
	for i, entry := range entries {
		services[i] = SummaryEntry{
			Name:          entry.Name,
			WorkspaceId:   entry.WorkspaceId,
			ProjectId:     entry.ProjectId,
			EnvironmentId: entry.EnvironmentId,
			ServiceId:     entry.ServiceId,
			ServiceDomain: entry.ServiceDomain,
			Domain:        entry.Domain,
			Status:        "ok",
		}
		token := tokenFor(entry, tokens)
		services[i].HasApiToken = token != ""
		if token == "" {
			services[i].Status = "unavailable"
			services[i].Error = "no railway token configured for this workspace"
			continue
		}
		wg.Add(1)
		go func(idx int, token string, serviceId, environmentId string) {
			defer wg.Done()
			client := currentFactory()(token)
			instance, err := client.ServiceInstance(ctx, serviceId, environmentId)
			if err != nil {
				services[idx].Status = "error"
				services[idx].Error = sanitizeError(err, token)
				return
			}
			if instance == nil || instance.LatestDeployment == nil {
				services[idx].Status = "ok"
				return
			}
			dep := instance.LatestDeployment
			services[idx].LatestDeployment = &DeploymentSnapshot{
				Id:        dep.Id,
				Status:    dep.Status,
				Url:       dep.Url,
				StaticUrl: dep.StaticUrl,
				CreatedAt: dep.CreatedAt,
				UpdatedAt: dep.UpdatedAt,
			}
		}(i, token, entry.ServiceId, entry.EnvironmentId)
	}
	wg.Wait()
	response.Services = services

	// 2) 按 workspace 去重抓余额（同 workspace 7 台服务共享一份）
	workspaceIds := make([]string, 0)
	seen := map[string]struct{}{}
	for _, entry := range entries {
		ws := strings.TrimSpace(entry.WorkspaceId)
		if ws == "" {
			continue
		}
		if _, ok := seen[ws]; ok {
			continue
		}
		seen[ws] = struct{}{}
		workspaceIds = append(workspaceIds, ws)
	}
	sort.Strings(workspaceIds)

	billing := make([]WorkspaceBillingSummary, 0, len(workspaceIds))
	for _, ws := range workspaceIds {
		token := strings.TrimSpace(tokens[ws])
		item := WorkspaceBillingSummary{
			WorkspaceId: ws,
			Unit:        "USD",
			Currency:    "USD",
			Period:      "monthly",
			SampledAt:   response.SampledAt,
			Status:      "ok",
		}
		if token == "" {
			item.Status = "unavailable"
			item.Error = "no railway token configured for this workspace"
			billing = append(billing, item)
			continue
		}
		client := currentFactory()(token)
		workspaceBilling, err := client.WorkspaceBilling(ctx, ws)
		if err != nil {
			item.Status = "error"
			item.Error = sanitizeError(err, token)
			billing = append(billing, item)
			continue
		}
		if workspaceBilling != nil {
			item.CreditBalance = workspaceBilling.CreditBalance
			item.CurrentUsage = workspaceBilling.CurrentUsage
			item.RemainingUsageCreditBalance = workspaceBilling.RemainingUsageCreditBalance
			item.HasExhaustedFreePlan = workspaceBilling.HasExhaustedFreePlan
			item.TrialDaysRemaining = workspaceBilling.TrialDaysRemaining
			if workspaceBilling.UsageLimit != nil {
				item.UsageLimitSoft = workspaceBilling.UsageLimit.SoftLimit
				item.UsageLimitHard = workspaceBilling.UsageLimit.HardLimit
				item.UsageLimitIsOver = workspaceBilling.UsageLimit.IsOverLimit
			}
		}
		estimated, estErr := client.EstimatedUsage(ctx, ws)
		if estErr == nil && estimated != nil {
			item.EstimatedUsage = estimated
			item.EstimatedUsageIsGuess = true
		}
		billing = append(billing, item)
	}
	response.Billing = billing

	// 3) 新鲜度（FreshnessWindow 内为 fresh）
	cutoff := time.Now().Add(-FreshnessWindow).Unix()
	for i := range response.Billing {
		response.Billing[i].Fresh = response.Billing[i].SampledAt >= cutoff
	}
	return response, nil
}

// tokensByWorkspace 归并每个 workspace 的可用 token：
// 1) 注册项自带 api_token；2) 本地 Node 表同 workspace 节点的 api_token。
// 返回值为明文（解密后），仅在本函数与 goroutine 闭包内使用，绝不写日志/响应。
func tokensByWorkspace(entries []operation_setting.NodeRailwayServiceEntry) map[string]string {
	out := map[string]string{}
	for _, entry := range entries {
		ws := strings.TrimSpace(entry.WorkspaceId)
		if ws == "" {
			continue
		}
		if token := strings.TrimSpace(common.DecryptSecretOrLegacy(entry.ApiTokenEnc)); token != "" {
			if _, exists := out[ws]; !exists {
				out[ws] = token
			}
		}
	}
	// 回落：从本地 Node 表继承同 workspace 的 token（仅当注册项未自带 token）。
	nodes, err := model.GetNodes()
	if err == nil {
		for i := range nodes {
			node := &nodes[i]
			if node.Provider != "railway" {
				continue
			}
			ws := strings.TrimSpace(node.WorkspaceId)
			if ws == "" {
				continue
			}
			if _, exists := out[ws]; exists {
				continue
			}
			if token := strings.TrimSpace(node.DecryptedToken()); token != "" {
				out[ws] = token
			}
		}
	}
	return out
}

func tokenFor(entry operation_setting.NodeRailwayServiceEntry, tokens map[string]string) string {
	if token := strings.TrimSpace(common.DecryptSecretOrLegacy(entry.ApiTokenEnc)); token != "" {
		return token
	}
	return strings.TrimSpace(tokens[strings.TrimSpace(entry.WorkspaceId)])
}

// sanitizeError 把下游错误裁成单行（最长 240 字符），并主动抹掉本次请求用过的 token，
// 防止上游 GraphQL 把请求头/变量回显进 message 时把 token 带回管理面板。
// GraphQL APIError 的 Body 可能含请求变量，这里只保留 status code 与首条 message。
func sanitizeError(err error, tokensUsed ...string) string {
	if err == nil {
		return ""
	}
	var raw string
	var apiErr *railway.APIError
	if errors.As(err, &apiErr) {
		if len(apiErr.Errors) > 0 {
			raw = fmt.Sprintf("railway http %d: %s", apiErr.StatusCode, apiErr.Errors[0].Message)
		} else {
			raw = fmt.Sprintf("railway http %d", apiErr.StatusCode)
		}
	} else {
		raw = err.Error()
	}
	return trimSingleLine(redactTokens(raw, tokensUsed), 240)
}

// redactTokens 把 msg 中出现过的任何 token 替换为 "***"。空 token 跳过。
func redactTokens(msg string, tokens []string) string {
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, token, "***")
	}
	return msg
}

func trimSingleLine(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

// Package railway 是 M5 的 Railway GraphQL v2 客户端（Go 版，精简自 panel/src/railway.js 实测结论）。
//
// 端点默认 https://backboard.railway.com/graphql/v2，可用 RAILWAY_GRAPHQL_ENDPOINT
// 覆盖（e2e mock）。Bearer token 由调用方提供（Node.ApiTokenEnc 解密后）。
//
// 实测要点（M5 蓝图 §6）：
//   - 批量 variableUpsert 必须带 skipDeploys:true，全部灌完后统一 serviceInstanceDeploy；
//   - serviceDomainCreate 的 input 只收 environmentId/serviceId/targetPort，多传 projectId 会被拒；
//   - customDomainIssueCertificate(id)；customDomains.status 是对象，必须选子字段；
//   - currentUsage 有分钟级滞后，余额监控用 workspaceBilling + estimatedUsage 双口径取 min。
package railway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultEndpoint Railway GraphQL v2 官方端点。
const DefaultEndpoint = "https://backboard.railway.com/graphql/v2"

// GraphQLError 单条 GraphQL 错误。
type GraphQLError struct {
	Message string `json:"message"`
	Path    []any  `json:"path,omitempty"`
}

// APIError Railway 返回的非 2xx / GraphQL errors 统一错误。
type APIError struct {
	StatusCode int
	Errors     []GraphQLError
	Body       string
}

func (e *APIError) Error() string {
	parts := make([]string, 0, len(e.Errors))
	for _, item := range e.Errors {
		parts = append(parts, item.Message)
	}
	if len(parts) > 0 {
		return fmt.Sprintf("railway graphql error (http %d): %s", e.StatusCode, strings.Join(parts, " | "))
	}
	return fmt.Sprintf("railway graphql error (http %d): %s", e.StatusCode, truncate(e.Body, 300))
}

// Client Railway GraphQL 客户端。
type Client struct {
	Endpoint   string
	Token      string
	HTTPClient *http.Client
}

// NewClient 使用 RAILWAY_GRAPHQL_ENDPOINT（缺省官方端点）创建客户端。
func NewClient(token string) *Client {
	endpoint := strings.TrimSpace(os.Getenv("RAILWAY_GRAPHQL_ENDPOINT"))
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return NewClientWithEndpoint(token, endpoint)
}

// NewClientWithEndpoint 显式指定端点（单测/e2e mock）。
func NewClientWithEndpoint(token, endpoint string) *Client {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{Endpoint: endpoint, Token: strings.TrimSpace(token), HTTPClient: &http.Client{Timeout: 60 * time.Second}}
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []GraphQLError  `json:"errors"`
}

// do 执行 GraphQL 请求并把 data 解码到 out。
func (c *Client) do(ctx context.Context, query string, variables map[string]any, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	body, err := json.Marshal(graphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var envelope graphQLResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return &APIError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	if resp.StatusCode >= 400 || len(envelope.Errors) > 0 {
		return &APIError{StatusCode: resp.StatusCode, Errors: envelope.Errors, Body: string(raw)}
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("railway graphql response has no data")
	}
	return json.Unmarshal(envelope.Data, out)
}

// ---------------------------------------------------------------------------
// 数据结构
// ---------------------------------------------------------------------------

type Environment struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

type Service struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

// Project 归一化后的项目树。
type Project struct {
	Id           string        `json:"id"`
	Name         string        `json:"name"`
	Environments []Environment `json:"environments"`
	Services     []Service     `json:"services"`
}

type Deployment struct {
	Id        string `json:"id"`
	Status    string `json:"status"`
	Url       string `json:"url"`
	StaticUrl string `json:"staticUrl"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type ServiceInstance struct {
	Id               string      `json:"id"`
	ServiceId        string      `json:"serviceId"`
	ServiceName      string      `json:"serviceName"`
	LatestDeployment *Deployment `json:"latestDeployment"`
}

type ServiceDomain struct {
	Id     string `json:"id"`
	Domain string `json:"domain"`
}

type CustomDomainStatus struct {
	Verified                bool   `json:"verified"`
	CertificateStatus       string `json:"certificateStatus"`
	CertificateErrorMessage string `json:"certificateErrorMessage"`
	VerificationDnsHost     string `json:"verificationDnsHost"`
	VerificationToken       string `json:"verificationToken"`
}

type CustomDomain struct {
	Id     string              `json:"id"`
	Domain string              `json:"domain"`
	Status *CustomDomainStatus `json:"status"`
}

type Domains struct {
	CustomDomains  []CustomDomain  `json:"customDomains"`
	ServiceDomains []ServiceDomain `json:"serviceDomains"`
}

type UsageLimit struct {
	SoftLimit   *float64 `json:"softLimit"`
	HardLimit   *float64 `json:"hardLimit"`
	IsOverLimit bool     `json:"isOverLimit"`
}

// WorkspaceBilling Railway 计费口径（§6）。
type WorkspaceBilling struct {
	CreditBalance               *float64    `json:"creditBalance"`
	CurrentUsage                *float64    `json:"currentUsage"`
	RemainingUsageCreditBalance *float64    `json:"remainingUsageCreditBalance"`
	HasExhaustedFreePlan        bool        `json:"hasExhaustedFreePlan"`
	TrialDaysRemaining          *int        `json:"trialDaysRemaining"`
	UsageLimit                  *UsageLimit `json:"usageLimit"`
}

// ---------------------------------------------------------------------------
// 项目 / 环境 / 服务
// ---------------------------------------------------------------------------

// ProjectCreate 创建项目并返回默认环境。
func (c *Client) ProjectCreate(ctx context.Context, name string) (*Project, error) {
	const query = `mutation ($input: ProjectCreateInput!) {
		projectCreate(input: $input) {
			id name
			environments { edges { node { id name } } }
			services { edges { node { id name } } }
		}
	}`
	var data struct {
		ProjectCreate struct {
			Id           string `json:"id"`
			Name         string `json:"name"`
			Environments struct {
				Edges []struct {
					Node Environment `json:"node"`
				} `json:"edges"`
			} `json:"environments"`
			Services struct {
				Edges []struct {
					Node Service `json:"node"`
				} `json:"edges"`
			} `json:"services"`
		} `json:"projectCreate"`
	}
	if err := c.do(ctx, query, map[string]any{"input": map[string]any{"name": name}}, &data); err != nil {
		return nil, err
	}
	project := &Project{Id: data.ProjectCreate.Id, Name: data.ProjectCreate.Name}
	for _, edge := range data.ProjectCreate.Environments.Edges {
		project.Environments = append(project.Environments, edge.Node)
	}
	for _, edge := range data.ProjectCreate.Services.Edges {
		project.Services = append(project.Services, edge.Node)
	}
	return project, nil
}

// GithubRepoDeploy 从 GitHub 仓库创建服务；返回 Railway workflow id。
func (c *Client) GithubRepoDeploy(ctx context.Context, projectId, repo, branch string) (string, error) {
	const query = `mutation ($input: GitHubRepoDeployInput!) { githubRepoDeploy(input: $input) }`
	input := map[string]any{"projectId": projectId, "repo": repo}
	if strings.TrimSpace(branch) != "" {
		input["branch"] = branch
	}
	var data struct {
		GithubRepoDeploy string `json:"githubRepoDeploy"`
	}
	if err := c.do(ctx, query, map[string]any{"input": input}, &data); err != nil {
		return "", err
	}
	return data.GithubRepoDeploy, nil
}

// Environments 查询项目环境列表。
func (c *Client) Environments(ctx context.Context, projectId string) ([]Environment, error) {
	const query = `query ($projectId: String!) {
		environments(projectId: $projectId) { edges { node { id name } } }
	}`
	var data struct {
		Environments struct {
			Edges []struct {
				Node Environment `json:"node"`
			} `json:"edges"`
		} `json:"environments"`
	}
	if err := c.do(ctx, query, map[string]any{"projectId": projectId}, &data); err != nil {
		return nil, err
	}
	out := make([]Environment, 0, len(data.Environments.Edges))
	for _, edge := range data.Environments.Edges {
		out = append(out, edge.Node)
	}
	return out, nil
}

// ServicesForProject 查询项目服务。workspaceId 非空走 workspace 直查（workspace
// API token 路线），否则走 externalWorkspaces（OAuth/account token 路线）。
func (c *Client) ServicesForProject(ctx context.Context, workspaceId, projectId string) ([]Service, error) {
	if strings.TrimSpace(workspaceId) != "" {
		const query = `query ($id: String!) {
			workspace(workspaceId: $id) {
				projects { edges { node {
					id name
					services { edges { node { id name } } }
				} } }
			}
		}`
		var data struct {
			Workspace struct {
				Projects struct {
					Edges []struct {
						Node struct {
							Id       string `json:"id"`
							Name     string `json:"name"`
							Services struct {
								Edges []struct {
									Node Service `json:"node"`
								} `json:"edges"`
							} `json:"services"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"projects"`
			} `json:"workspace"`
		}
		if err := c.do(ctx, query, map[string]any{"id": workspaceId}, &data); err != nil {
			return nil, err
		}
		for _, edge := range data.Workspace.Projects.Edges {
			if edge.Node.Id != projectId {
				continue
			}
			services := make([]Service, 0, len(edge.Node.Services.Edges))
			for _, serviceEdge := range edge.Node.Services.Edges {
				services = append(services, serviceEdge.Node)
			}
			return services, nil
		}
		return nil, nil
	}

	const query = `query {
		externalWorkspaces {
			projects {
				id name
				services { edges { node { id name } } }
			}
		}
	}`
	var data struct {
		ExternalWorkspaces []struct {
			Projects []struct {
				Id       string `json:"id"`
				Name     string `json:"name"`
				Services struct {
					Edges []struct {
						Node Service `json:"node"`
					} `json:"edges"`
				} `json:"services"`
			} `json:"projects"`
		} `json:"externalWorkspaces"`
	}
	if err := c.do(ctx, query, nil, &data); err != nil {
		return nil, err
	}
	for _, workspace := range data.ExternalWorkspaces {
		for _, project := range workspace.Projects {
			if project.Id != projectId {
				continue
			}
			services := make([]Service, 0, len(project.Services.Edges))
			for _, edge := range project.Services.Edges {
				services = append(services, edge.Node)
			}
			return services, nil
		}
	}
	return nil, nil
}

// ServiceInstance 查询服务实例与最新部署。
func (c *Client) ServiceInstance(ctx context.Context, serviceId, environmentId string) (*ServiceInstance, error) {
	const query = `query ($serviceId: String!, $environmentId: String!) {
		serviceInstance(serviceId: $serviceId, environmentId: $environmentId) {
			id serviceId serviceName
			latestDeployment { id status url staticUrl createdAt updatedAt }
		}
	}`
	var data struct {
		ServiceInstance *ServiceInstance `json:"serviceInstance"`
	}
	if err := c.do(ctx, query, map[string]any{"serviceId": serviceId, "environmentId": environmentId}, &data); err != nil {
		return nil, err
	}
	return data.ServiceInstance, nil
}

// ---------------------------------------------------------------------------
// 环境变量
// ---------------------------------------------------------------------------

// Variables 返回项目/环境/服务级变量（JSON 标量对象），统一转为 string。
func (c *Client) Variables(ctx context.Context, projectId, environmentId, serviceId string) (map[string]string, error) {
	const query = `query ($projectId: String!, $environmentId: String!, $serviceId: String) {
		variables(projectId: $projectId, environmentId: $environmentId, serviceId: $serviceId)
	}`
	var data struct {
		Variables map[string]any `json:"variables"`
	}
	vars := map[string]any{"projectId": projectId, "environmentId": environmentId}
	if strings.TrimSpace(serviceId) == "" {
		vars["serviceId"] = nil
	} else {
		vars["serviceId"] = serviceId
	}
	if err := c.do(ctx, query, vars, &data); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(data.Variables))
	for key, value := range data.Variables {
		switch typed := value.(type) {
		case nil:
			out[key] = ""
		case string:
			out[key] = typed
		default:
			out[key] = fmt.Sprintf("%v", typed)
		}
	}
	return out, nil
}

// VariableUpsertInput 变量写入参数。SkipDeploys 必须为 true（批量灌变量防部署风暴）。
type VariableUpsertInput struct {
	ProjectId     string `json:"projectId"`
	EnvironmentId string `json:"environmentId"`
	ServiceId     string `json:"serviceId,omitempty"`
	Name          string `json:"name"`
	Value         string `json:"value"`
	SkipDeploys   bool   `json:"skipDeploys"`
}

// VariableUpsert 写入单个变量（默认强制 skipDeploys）。
func (c *Client) VariableUpsert(ctx context.Context, input VariableUpsertInput) error {
	const query = `mutation ($input: VariableUpsertInput!) { variableUpsert(input: $input) }`
	input.SkipDeploys = true
	var data struct {
		VariableUpsert bool `json:"variableUpsert"`
	}
	return c.do(ctx, query, map[string]any{"input": input}, &data)
}

// VariableDeleteInput 变量删除参数。
type VariableDeleteInput struct {
	ProjectId     string `json:"projectId"`
	EnvironmentId string `json:"environmentId"`
	ServiceId     string `json:"serviceId,omitempty"`
	Name          string `json:"name"`
}

// VariableDelete 删除一个变量（仅 prune 开启时调用）。
func (c *Client) VariableDelete(ctx context.Context, input VariableDeleteInput) error {
	const query = `mutation ($input: VariableDeleteInput!) { variableDelete(input: $input) }`
	var data struct {
		VariableDelete bool `json:"variableDelete"`
	}
	return c.do(ctx, query, map[string]any{"input": input}, &data)
}

// ---------------------------------------------------------------------------
// 部署
// ---------------------------------------------------------------------------

// DeploymentListInput deployments 查询过滤。
type DeploymentListInput struct {
	ProjectId     string `json:"projectId"`
	EnvironmentId string `json:"environmentId"`
	ServiceId     string `json:"serviceId,omitempty"`
}

// Deployments 返回部署列表（新→旧）。
func (c *Client) Deployments(ctx context.Context, input DeploymentListInput, first int) ([]Deployment, error) {
	const query = `query ($input: DeploymentListInput!, $first: Int) {
		deployments(input: $input, first: $first) { edges { node {
			id status url staticUrl createdAt updatedAt
		} } }
	}`
	if first <= 0 {
		first = 20
	}
	var data struct {
		Deployments struct {
			Edges []struct {
				Node Deployment `json:"node"`
			} `json:"edges"`
		} `json:"deployments"`
	}
	if err := c.do(ctx, query, map[string]any{"input": input, "first": first}, &data); err != nil {
		return nil, err
	}
	out := make([]Deployment, 0, len(data.Deployments.Edges))
	for _, edge := range data.Deployments.Edges {
		out = append(out, edge.Node)
	}
	return out, nil
}

// ServiceInstanceDeploy 触发一次部署；返回 Railway 的布尔结果。
func (c *Client) ServiceInstanceDeploy(ctx context.Context, serviceId, environmentId string) (bool, error) {
	const query = `mutation ($serviceId: String!, $environmentId: String!) {
		serviceInstanceDeploy(serviceId: $serviceId, environmentId: $environmentId)
	}`
	var data struct {
		ServiceInstanceDeploy bool `json:"serviceInstanceDeploy"`
	}
	if err := c.do(ctx, query, map[string]any{"serviceId": serviceId, "environmentId": environmentId}, &data); err != nil {
		return false, err
	}
	return data.ServiceInstanceDeploy, nil
}

// DeploymentTerminalStatus 判断部署状态是否终态。
func DeploymentTerminalStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCESS", "FAILED", "CRASHED", "REMOVED", "SKIPPED":
		return true
	default:
		return false
	}
}

// DeploymentSuccessful 判断终态是否成功。
func DeploymentSuccessful(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "SUCCESS")
}

// DeploymentPollInterval 轮询间隔（env NODE_DEPLOY_POLL_INTERVAL_MS 可覆盖，便于 e2e）。
func DeploymentPollInterval() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("NODE_DEPLOY_POLL_INTERVAL_MS")); raw != "" {
		var ms int
		if _, err := fmt.Sscanf(raw, "%d", &ms); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 3 * time.Second
}

// WaitForDeployment 轮询直到出现基线之后的新部署并到达终态。
// baselineId 为触发部署前最新部署 ID（空表示此前无部署）。
func (c *Client) WaitForDeployment(ctx context.Context, input DeploymentListInput, baselineId string, timeout time.Duration) (*Deployment, error) {
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(timeout)
	interval := DeploymentPollInterval()
	seenNew := false
	var last *Deployment
	for {
		deployments, err := c.Deployments(ctx, input, 5)
		if err != nil {
			return nil, err
		}
		if len(deployments) > 0 {
			last = &deployments[0]
			if last.Id != baselineId {
				seenNew = true
			}
			if seenNew && DeploymentTerminalStatus(last.Status) {
				if DeploymentSuccessful(last.Status) {
					return last, nil
				}
				return last, fmt.Errorf("deployment %s ended in status %s", last.Id, last.Status)
			}
			// 无基线（此前从未部署）时，若首个可见部署已是终态成功，直接接受。
			if baselineId == "" && DeploymentTerminalStatus(last.Status) {
				if DeploymentSuccessful(last.Status) {
					return last, nil
				}
				return last, fmt.Errorf("deployment %s ended in status %s", last.Id, last.Status)
			}
		}
		if time.Now().After(deadline) {
			status := "unknown"
			if last != nil {
				status = last.Status
			}
			return last, fmt.Errorf("deployment polling timed out after %s (last status: %s)", timeout, status)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// ---------------------------------------------------------------------------
// 余额 / 用量
// ---------------------------------------------------------------------------

// WorkspaceBilling 查询工作区计费；workspaceId 为空时走 me.workspace。
func (c *Client) WorkspaceBilling(ctx context.Context, workspaceId string) (*WorkspaceBilling, error) {
	if strings.TrimSpace(workspaceId) != "" {
		const query = `query ($id: String!) {
			workspace(workspaceId: $id) {
				id name plan
				customer {
					isTrialing trialDaysRemaining creditBalance currentUsage
					remainingUsageCreditBalance hasExhaustedFreePlan
					usageLimit { softLimit hardLimit isOverLimit }
				}
			}
		}`
		var data struct {
			Workspace *struct {
				Customer *WorkspaceBilling `json:"customer"`
			} `json:"workspace"`
		}
		if err := c.do(ctx, query, map[string]any{"id": workspaceId}, &data); err != nil {
			return nil, err
		}
		if data.Workspace == nil || data.Workspace.Customer == nil {
			return nil, errors.New("railway workspace billing is unavailable for this token")
		}
		return data.Workspace.Customer, nil
	}
	const query = `query {
		me { workspace { customer {
			isTrialing trialDaysRemaining creditBalance currentUsage
			remainingUsageCreditBalance hasExhaustedFreePlan
			usageLimit { softLimit hardLimit isOverLimit }
		} } }
	}`
	var data struct {
		Me *struct {
			Workspace *struct {
				Customer *WorkspaceBilling `json:"customer"`
			} `json:"workspace"`
		} `json:"me"`
	}
	if err := c.do(ctx, query, nil, &data); err != nil {
		return nil, err
	}
	if data.Me == nil || data.Me.Workspace == nil || data.Me.Workspace.Customer == nil {
		return nil, errors.New("railway account billing is unavailable for this token")
	}
	return data.Me.Workspace.Customer, nil
}

// EstimatedUsage 实时用量估算（美元）；currentUsage 有分钟级滞后，监控取 min 双口径。
func (c *Client) EstimatedUsage(ctx context.Context, workspaceId string) (*float64, error) {
	if strings.TrimSpace(workspaceId) == "" {
		return nil, nil
	}
	const query = `query ($id: String!) { estimatedUsage(workspaceId: $id) }`
	var data struct {
		EstimatedUsage *float64 `json:"estimatedUsage"`
	}
	if err := c.do(ctx, query, map[string]any{"id": workspaceId}, &data); err != nil {
		return nil, err
	}
	return data.EstimatedUsage, nil
}

// ---------------------------------------------------------------------------
// 域名
// ---------------------------------------------------------------------------

// DomainsFull 查询服务域名 + 自定义域名验证/证书状态。
func (c *Client) DomainsFull(ctx context.Context, projectId, environmentId, serviceId string) (*Domains, error) {
	const query = `query ($projectId: String!, $environmentId: String!, $serviceId: String!) {
		domains(projectId: $projectId, environmentId: $environmentId, serviceId: $serviceId) {
			customDomains {
				id domain
				status { verified certificateStatus certificateErrorMessage verificationDnsHost verificationToken }
			}
			serviceDomains { id domain }
		}
	}`
	var data struct {
		Domains *Domains `json:"domains"`
	}
	if err := c.do(ctx, query, map[string]any{"projectId": projectId, "environmentId": environmentId, "serviceId": serviceId}, &data); err != nil {
		return nil, err
	}
	if data.Domains == nil {
		return &Domains{}, nil
	}
	return data.Domains, nil
}

// ServiceDomainCreateInput 只允许 environmentId/serviceId/targetPort（projectId 会被服务端拒绝）。
type ServiceDomainCreateInput struct {
	EnvironmentId string `json:"environmentId"`
	ServiceId     string `json:"serviceId"`
	TargetPort    *int   `json:"targetPort,omitempty"`
}

// ServiceDomainCreate 创建 Railway 生成的 service domain。
func (c *Client) ServiceDomainCreate(ctx context.Context, environmentId, serviceId string, targetPort *int) (*ServiceDomain, error) {
	const query = `mutation ($input: ServiceDomainCreateInput!) {
		serviceDomainCreate(input: $input) { id domain }
	}`
	// 显式结构体保证绝不携带 projectId。
	input := ServiceDomainCreateInput{EnvironmentId: environmentId, ServiceId: serviceId, TargetPort: targetPort}
	var data struct {
		ServiceDomainCreate *ServiceDomain `json:"serviceDomainCreate"`
	}
	if err := c.do(ctx, query, map[string]any{"input": input}, &data); err != nil {
		return nil, err
	}
	return data.ServiceDomainCreate, nil
}

// CustomDomainCreateInput 自定义域名创建参数。
type CustomDomainCreateInput struct {
	ProjectId     string `json:"projectId"`
	EnvironmentId string `json:"environmentId"`
	ServiceId     string `json:"serviceId"`
	Domain        string `json:"domain"`
}

// CustomDomainCreate 绑定自定义域名。
func (c *Client) CustomDomainCreate(ctx context.Context, input CustomDomainCreateInput) (*CustomDomain, error) {
	const query = `mutation ($input: CustomDomainCreateInput!) {
		customDomainCreate(input: $input) { id domain }
	}`
	var data struct {
		CustomDomainCreate *CustomDomain `json:"customDomainCreate"`
	}
	if err := c.do(ctx, query, map[string]any{"input": input}, &data); err != nil {
		return nil, err
	}
	return data.CustomDomainCreate, nil
}

// CustomDomainIssueCertificate 触发证书签发（customDomainIssueCertificate(id)）。
func (c *Client) CustomDomainIssueCertificate(ctx context.Context, id string) (bool, error) {
	const query = `mutation ($id: String!) { customDomainIssueCertificate(id: $id) }`
	var data struct {
		CustomDomainIssueCertificate bool `json:"customDomainIssueCertificate"`
	}
	if err := c.do(ctx, query, map[string]any{"id": id}, &data); err != nil {
		return false, err
	}
	return data.CustomDomainIssueCertificate, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

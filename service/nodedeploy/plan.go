// plan.go dry-run 差异计划生成：对比目标项目现有 variables 与模板，输出资源/变量变更清单。
package nodedeploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/railway"
)

// Action plan 中的一条变更。
type Action struct {
	Op        string `json:"op"`             // create|update|delete|deploy|noop
	Resource  string `json:"resource"`       // project|service|variable|deployment|service_domain|custom_domain|dns_txt|certificate
	Name      string `json:"name,omitempty"` // 资源名（项目/服务/域名）
	Variable  string `json:"variable,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Skipped   bool   `json:"skipped,omitempty"`   // prune 关闭时 delete 仅展示不执行
	Sensitive bool   `json:"sensitive,omitempty"` // 展示层脱敏（内部 plan 保留真实值）
}

// Plan 部署 dry-run 计划。
type Plan struct {
	PlanId        string   `json:"planId"`
	Kind          string   `json:"kind"` // deploy
	NodeId        int      `json:"nodeId"`
	NodeName      string   `json:"nodeName"`
	Provider      string   `json:"provider"`
	ProjectId     string   `json:"projectId"`
	EnvironmentId string   `json:"environmentId"`
	ServiceId     string   `json:"serviceId"`
	Repo          string   `json:"repo"`
	Branch        string   `json:"branch"`
	Domain        string   `json:"domain"`
	ServiceDomain string   `json:"serviceDomain"`
	Actions       []Action `json:"actions"`
	Warnings      []string `json:"warnings"`
	CreatedAt     int64    `json:"createdAt"`
	ExpiresAt     int64    `json:"expiresAt"`
	// NodeUpdatedTime plan 生成时的节点版本；apply 前比对，防止配置漂移。
	NodeUpdatedTime int64 `json:"nodeUpdatedTime"`
	// Variables 内部执行用（不序列化到 API；Store 在 Redis/内存内保存）。
	Variables map[string]string `json:"-"`
}

// Masked 返回 API 展示副本：密钥类变量的值替换为 ******。
func (p *Plan) Masked() *Plan {
	if p == nil {
		return nil
	}
	copyPlan := *p
	copyPlan.Actions = make([]Action, len(p.Actions))
	for i, action := range p.Actions {
		if action.Sensitive {
			if action.From != "" {
				action.From = "******"
			}
			if action.To != "" {
				action.To = "******"
			}
		}
		copyPlan.Actions[i] = action
	}
	copyPlan.Variables = nil
	return &copyPlan
}

// BuildVariableDiff 纯函数：比较期望变量与现有变量，生成 variable actions。
// prune=false 时多余变量的 delete 标记 Skipped 并给出 warning。
func BuildVariableDiff(desired map[string]string, existing map[string]string, prune bool) ([]Action, []string) {
	actions := make([]Action, 0)
	warnings := make([]string, 0)

	desiredKeys := make([]string, 0, len(desired))
	for key := range desired {
		desiredKeys = append(desiredKeys, key)
	}
	sort.Strings(desiredKeys)
	for _, key := range desiredKeys {
		sensitive := sensitiveVariable(key)
		newValue := desired[key]
		oldValue, exists := existing[key]
		switch {
		case !exists:
			actions = append(actions, Action{Op: "create", Resource: "variable", Variable: key, To: newValue, Sensitive: sensitive})
		case oldValue != newValue:
			actions = append(actions, Action{Op: "update", Resource: "variable", Variable: key, From: oldValue, To: newValue, Sensitive: sensitive})
		}
	}

	extraKeys := make([]string, 0)
	for key := range existing {
		if _, exists := desired[key]; !exists {
			extraKeys = append(extraKeys, key)
		}
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		actions = append(actions, Action{
			Op: "delete", Resource: "variable", Variable: key,
			From: existing[key], Skipped: !prune, Sensitive: sensitiveVariable(key),
		})
		if !prune {
			warnings = append(warnings, "variable "+key+" exists in the target but is not in the template; it will NOT be deleted (NodeDeployPruneVariables=false)")
		}
	}
	return actions, warnings
}

// BuildPlan 生成部署 dry-run 计划并写入 10 分钟缓存。
func BuildPlan(ctx context.Context, nodeId int, envOverrides map[string]string) (*Plan, error) {
	node, err := model.GetNodeById(nodeId)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNodeNotFound, err)
	}
	if node.Provider != "railway" {
		return nil, fmt.Errorf("one-click deploy currently supports provider=railway, got %q", node.Provider)
	}
	opts := LoadOptions()
	if err := ValidateTargets(node, opts); err != nil {
		return nil, err
	}
	token := node.DecryptedToken()
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("node %d has no API token configured", node.Id)
	}

	templateVars := ParseEnvTemplate(LoadTemplateText(opts))
	desired, warnings := ResolveVariables(templateVars, envOverrides)
	client := railway.NewClient(token)

	projectId := node.ProjectId
	environmentId := node.EnvironmentId
	serviceId := node.ServiceId
	repo := strings.TrimSpace(node.Repo)
	branch := strings.TrimSpace(node.Branch)
	if branch == "" {
		branch = "main"
	}

	actions := make([]Action, 0)
	if projectId == "" {
		actions = append(actions, Action{Op: "create", Resource: "project", Name: node.Name})
		warnings = append(warnings, "no ProjectId recorded; apply will create a new Railway project and cannot diff current variables")
	}

	// 解析环境/服务（已有项目时）。先查现有服务，确实不存在才计划创建。
	if projectId != "" && environmentId == "" {
		envs, err := client.Environments(ctx, projectId)
		if err != nil {
			return nil, fmt.Errorf("query railway environments: %w", err)
		}
		if len(envs) > 0 {
			environmentId = envs[0].Id
		} else {
			warnings = append(warnings, "target project has no environment; deploy cannot proceed until one exists")
		}
	}
	if projectId != "" && serviceId == "" {
		services, err := client.ServicesForProject(ctx, node.WorkspaceId, projectId)
		if err != nil {
			warnings = append(warnings, "could not list project services: "+err.Error())
		} else if len(services) > 0 {
			serviceId = services[0].Id
			if len(services) > 1 {
				warnings = append(warnings, fmt.Sprintf("project has %d services; planning against the first service %q", len(services), services[0].Name))
			}
		}
	}
	if serviceId == "" {
		actions = append(actions, Action{Op: "create", Resource: "service", Name: serviceNameFromRepo(repo)})
		if repo == "" {
			return nil, fmt.Errorf("node %d has no ServiceId and no repo configured; set repo (owner/name) for githubRepoDeploy", node.Id)
		}
	}

	existing := map[string]string{}
	if projectId != "" && environmentId != "" {
		current, err := client.Variables(ctx, projectId, environmentId, serviceId)
		if err != nil {
			warnings = append(warnings, "could not read current variables: "+err.Error())
		} else {
			existing = current
		}
	}
	variableActions, diffWarnings := BuildVariableDiff(desired, existing, opts.PruneVariables)
	actions = append(actions, variableActions...)
	warnings = append(warnings, diffWarnings...)

	hasChanges := false
	for _, action := range variableActions {
		if action.Skipped {
			continue
		}
		if action.Op == "create" || action.Op == "update" || action.Op == "delete" {
			hasChanges = true
			break
		}
	}
	for _, action := range actions {
		if action.Resource == "project" || action.Resource == "service" {
			hasChanges = true
		}
	}
	deployOp := "noop"
	if hasChanges {
		deployOp = "deploy"
	}
	actions = append(actions, Action{Op: deployOp, Resource: "deployment", Name: serviceNameFromRepo(repo)})

	// 域名：service domain（健康检查/公网入口）+ 自定义域名（CF TXT + 证书）。
	domainsKnown := false
	if projectId != "" && environmentId != "" && serviceId != "" {
		if domains, derr := client.DomainsFull(ctx, projectId, environmentId, serviceId); derr == nil {
			domainsKnown = true
			if len(domains.ServiceDomains) > 0 && node.ServiceDomain == "" {
				actions = append(actions, Action{Op: "noop", Resource: "service_domain", Name: domains.ServiceDomains[0].Domain})
			}
			if node.Domain != "" {
				for _, custom := range domains.CustomDomains {
					if strings.EqualFold(custom.Domain, node.Domain) {
						if custom.Status != nil {
							actions = append(actions, Action{Op: "noop", Resource: "custom_domain", Name: custom.Domain, From: custom.Status.CertificateStatus})
						}
					}
				}
			}
		} else {
			warnings = append(warnings, "could not query domains: "+derr.Error())
		}
	}
	if !domainsKnown && node.ServiceDomain == "" {
		actions = append(actions, Action{Op: "create", Resource: "service_domain", Name: "railway-generated"})
	}
	customDomainPlanned := false
	if node.Domain != "" {
		for _, action := range actions {
			if action.Resource == "custom_domain" {
				customDomainPlanned = true
				break
			}
		}
		if !customDomainPlanned {
			actions = append(actions, Action{Op: "create", Resource: "custom_domain", Name: node.Domain})
		}
		cfReady := strings.TrimSpace(opts.Cloudflare.ApiToken) != "" &&
			(strings.TrimSpace(opts.Cloudflare.ZoneId) != "" || strings.TrimSpace(opts.Cloudflare.ZoneName) != "")
		if cfReady {
			actions = append(actions,
				Action{Op: "create", Resource: "dns_txt", Name: "_railway-verify." + node.Domain},
				Action{Op: "create", Resource: "certificate", Name: node.Domain},
			)
		} else {
			warnings = append(warnings, "Cloudflare API token/zone is not configured; custom domain "+node.Domain+" will be created but its verification TXT record and certificate cannot be automated")
		}
	}
	if node.ServiceUrl == "" && node.ServiceDomain == "" && node.Domain == "" {
		warnings = append(warnings, "node has no ServiceUrl/ServiceDomain/Domain; health checks will fail until a domain is bound")
	}

	now := time.Now()
	plan := &Plan{
		PlanId:          newPlanId("deploy"),
		Kind:            "deploy",
		NodeId:          node.Id,
		NodeName:        node.Name,
		Provider:        node.Provider,
		ProjectId:       projectId,
		EnvironmentId:   environmentId,
		ServiceId:       serviceId,
		Repo:            repo,
		Branch:          branch,
		Domain:          node.Domain,
		ServiceDomain:   node.ServiceDomain,
		Actions:         actions,
		Warnings:        dedupeStrings(warnings),
		CreatedAt:       now.Unix(),
		ExpiresAt:       now.Add(PlanTTL).Unix(),
		NodeUpdatedTime: node.UpdatedTime,
		Variables:       desired,
	}
	if err := DefaultStore.PutDeploy(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func serviceNameFromRepo(repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return ""
	}
	if idx := strings.LastIndex(repo, "/"); idx >= 0 && idx+1 < len(repo) {
		return repo[idx+1:]
	}
	return repo
}

func newPlanId(prefix string) string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(buf)
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

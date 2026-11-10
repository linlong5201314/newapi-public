// apply.go 执行部署 plan：批量变量 upsert（skipDeploys）→ 统一部署 → 轮询终态 → 绑域 → 证书。
package nodedeploy

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/cloudflare"
	"github.com/QuantumNous/new-api/service/railway"
)

// ApplyResult 部署执行结果。
type ApplyResult struct {
	PlanId           string   `json:"planId"`
	NodeId           int      `json:"nodeId"`
	ProjectId        string   `json:"projectId"`
	EnvironmentId    string   `json:"environmentId"`
	ServiceId        string   `json:"serviceId"`
	ServiceDomain    string   `json:"serviceDomain"`
	Domain           string   `json:"domain"`
	DeploymentId     string   `json:"deploymentId"`
	DeploymentStatus string   `json:"deploymentStatus"`
	VariablesChanged int      `json:"variablesChanged"`
	Warnings         []string `json:"warnings"`
	DurationMs       int64    `json:"durationMs"`
}

// ApplyDeploy 执行（一次性消费）部署 plan。
func ApplyDeploy(ctx context.Context, planId string, confirm bool, operatorId int, operatorName string) (*ApplyResult, error) {
	if !confirm {
		return nil, ErrConfirmRequired
	}
	plan, err := DefaultStore.GetDeploy(planId)
	if err != nil {
		return nil, err
	}
	node, err := model.GetNodeById(plan.NodeId)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNodeNotFound, err)
	}
	opts := LoadOptions()
	if err := ValidateTargets(node, opts); err != nil {
		recordAudit(node.Id, "deploy_apply", planId, "rejected", operatorId, operatorName, err.Error())
		return nil, err
	}
	if plan.NodeUpdatedTime != 0 && node.UpdatedTime != plan.NodeUpdatedTime {
		return nil, ErrPlanNodeChanged
	}
	token := node.DecryptedToken()
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("node %d has no API token configured", node.Id)
	}
	started := time.Now()
	client := railway.NewClient(token)
	result := &ApplyResult{PlanId: plan.PlanId, Warnings: append([]string{}, plan.Warnings...)}
	warnings := func(format string, args ...any) {
		result.Warnings = append(result.Warnings, fmt.Sprintf(format, args...))
	}

	projectId := plan.ProjectId
	environmentId := plan.EnvironmentId
	serviceId := plan.ServiceId
	repo := plan.Repo
	branch := plan.Branch

	if projectId == "" {
		projectName := strings.TrimSpace(node.Name)
		if projectName == "" {
			projectName = "new-api-node"
		}
		created, cerr := client.ProjectCreate(ctx, projectName)
		if cerr != nil {
			return nil, failApply(node.Id, plan.PlanId, operatorId, operatorName, "create railway project: "+cerr.Error())
		}
		projectId = created.Id
		if len(created.Environments) > 0 {
			environmentId = created.Environments[0].Id
		}
	}
	if environmentId == "" {
		envs, cerr := client.Environments(ctx, projectId)
		if cerr != nil {
			return nil, failApply(node.Id, plan.PlanId, operatorId, operatorName, "query environments: "+cerr.Error())
		}
		if len(envs) == 0 {
			return nil, failApply(node.Id, plan.PlanId, operatorId, operatorName, "project has no environment")
		}
		environmentId = envs[0].Id
	}
	if serviceId == "" {
		if repo == "" {
			return nil, failApply(node.Id, plan.PlanId, operatorId, operatorName, "repo is required to create the service")
		}
		workflowId, derr := client.GithubRepoDeploy(ctx, projectId, repo, branch)
		if derr != nil {
			return nil, failApply(node.Id, plan.PlanId, operatorId, operatorName, "githubRepoDeploy: "+derr.Error())
		}
		if workflowId != "" {
			warnings("railway githubRepoDeploy workflow: %s", workflowId)
		}
		found, ferr := waitForService(ctx, client, node.WorkspaceId, projectId, serviceNameFromRepo(repo), 60*time.Second)
		if ferr != nil {
			return nil, failApply(node.Id, plan.PlanId, operatorId, operatorName, ferr.Error())
		}
		serviceId = found.Id
	}

	// 变量：按 plan action 执行，批量 upsert 统一带 skipDeploys。
	variableActions := make([]Action, 0)
	for _, action := range plan.Actions {
		if action.Resource != "variable" || action.Skipped {
			continue
		}
		switch action.Op {
		case "create", "update", "delete":
			variableActions = append(variableActions, action)
		}
	}
	sort.SliceStable(variableActions, func(i, j int) bool { return variableActions[i].Variable < variableActions[j].Variable })
	for _, action := range variableActions {
		switch action.Op {
		case "create", "update":
			value, ok := plan.Variables[action.Variable]
			if !ok {
				value = action.To
			}
			if err := client.VariableUpsert(ctx, railway.VariableUpsertInput{
				ProjectId:     projectId,
				EnvironmentId: environmentId,
				ServiceId:     serviceId,
				Name:          action.Variable,
				Value:         value,
				SkipDeploys:   true,
			}); err != nil {
				return nil, failApply(node.Id, plan.PlanId, operatorId, operatorName, "variableUpsert "+action.Variable+": "+err.Error())
			}
			result.VariablesChanged++
		case "delete":
			if err := client.VariableDelete(ctx, railway.VariableDeleteInput{
				ProjectId:     projectId,
				EnvironmentId: environmentId,
				ServiceId:     serviceId,
				Name:          action.Variable,
			}); err != nil {
				return nil, failApply(node.Id, plan.PlanId, operatorId, operatorName, "variableDelete "+action.Variable+": "+err.Error())
			}
			result.VariablesChanged++
		}
	}

	// 统一部署一次。
	baseline, berr := client.Deployments(ctx, railway.DeploymentListInput{ProjectId: projectId, EnvironmentId: environmentId, ServiceId: serviceId}, 1)
	if berr != nil {
		warnings("could not read baseline deployment: %s", berr.Error())
	}
	baselineId := ""
	if len(baseline) > 0 {
		baselineId = baseline[0].Id
	}
	if _, derr := client.ServiceInstanceDeploy(ctx, serviceId, environmentId); derr != nil {
		return nil, failApply(node.Id, plan.PlanId, operatorId, operatorName, "serviceInstanceDeploy: "+derr.Error())
	}
	deployment, werr := client.WaitForDeployment(ctx, railway.DeploymentListInput{
		ProjectId: projectId, EnvironmentId: environmentId, ServiceId: serviceId,
	}, baselineId, opts.DeployTimeout)
	if werr != nil {
		return nil, failApply(node.Id, plan.PlanId, operatorId, operatorName, "deployment wait: "+werr.Error())
	}
	result.DeploymentId = deployment.Id
	result.DeploymentStatus = deployment.Status

	// 域名：service domain + 自定义域名（CF TXT + 证书）。
	serviceDomain := node.ServiceDomain
	customDomain := node.Domain
	if domains, derr := client.DomainsFull(ctx, projectId, environmentId, serviceId); derr != nil {
		warnings("could not query domains: %s", derr.Error())
	} else {
		if len(domains.ServiceDomains) > 0 {
			serviceDomain = domains.ServiceDomains[0].Domain
		}
		customDomain = ensureCustomDomain(ctx, client, opts, node, projectId, environmentId, serviceId, domains, &result.Warnings)
	}
	if serviceDomain == "" {
		port := node.TargetPort
		if port <= 0 {
			port = 3000
		}
		created, derr := client.ServiceDomainCreate(ctx, environmentId, serviceId, &port)
		if derr != nil {
			warnings("serviceDomainCreate failed: %s", derr.Error())
		} else if created != nil {
			serviceDomain = created.Domain
		}
	}

	serviceURL := node.ServiceUrl
	if serviceURL == "" && serviceDomain != "" {
		serviceURL = "https://" + strings.TrimPrefix(serviceDomain, "https://")
	}
	if err := model.UpdateNodeDeployInfo(node.Id, projectId, serviceId, environmentId, serviceDomain, customDomain, serviceURL); err != nil {
		warnings("node record update failed: %s", err.Error())
	}

	result.ProjectId = projectId
	result.NodeId = node.Id
	result.EnvironmentId = environmentId
	result.ServiceId = serviceId
	result.ServiceDomain = serviceDomain
	result.Domain = customDomain
	result.DurationMs = time.Since(started).Milliseconds()
	result.Warnings = dedupeStrings(result.Warnings)
	DefaultStore.Delete(plan.PlanId)
	recordAudit(node.Id, "deploy_apply", fmt.Sprintf("project=%s service=%s deployment=%s", projectId, serviceId, deployment.Id), "success", operatorId, operatorName, fmt.Sprintf("variables=%d status=%s", result.VariablesChanged, deployment.Status))
	return result, nil
}

// ensureCustomDomain 确保自定义域名存在、CF TXT 已写、证书已触发。返回最终域名。
func ensureCustomDomain(ctx context.Context, client *railway.Client, opts Options, node *model.Node, projectId, environmentId, serviceId string, domains *railway.Domains, warnings *[]string) string {
	domain := strings.TrimSpace(node.Domain)
	if domain == "" {
		return ""
	}
	var existing *railway.CustomDomain
	for i := range domains.CustomDomains {
		if strings.EqualFold(domains.CustomDomains[i].Domain, domain) {
			existing = &domains.CustomDomains[i]
			break
		}
	}
	if existing == nil {
		created, err := client.CustomDomainCreate(ctx, railway.CustomDomainCreateInput{
			ProjectId:     projectId,
			EnvironmentId: environmentId,
			ServiceId:     serviceId,
			Domain:        domain,
		})
		if err != nil {
			*warnings = append(*warnings, "customDomainCreate failed: "+err.Error())
			return domain
		}
		existing = created
	}
	// 重新查询验证材料（domain 创建后 status 才有 verificationDnsHost/token）。
	latest, err := client.DomainsFull(ctx, projectId, environmentId, serviceId)
	if err != nil {
		*warnings = append(*warnings, "could not query custom domain status: "+err.Error())
		return domain
	}
	var status *railway.CustomDomainStatus
	var customId string
	for i := range latest.CustomDomains {
		if strings.EqualFold(latest.CustomDomains[i].Domain, domain) {
			customId = latest.CustomDomains[i].Id
			status = latest.CustomDomains[i].Status
			break
		}
	}
	if customId == "" && existing != nil {
		customId = existing.Id
	}
	if status == nil {
		*warnings = append(*warnings, "custom domain status is not yet available; certificate issuance skipped")
		return domain
	}
	txtHost := strings.TrimSpace(status.VerificationDnsHost)
	if txtHost == "" {
		txtHost = "_railway-verify." + domain
	}
	token := strings.TrimSpace(status.VerificationToken)
	if token != "" {
		cf := cloudflare.New(opts.Cloudflare)
		if zoneId, zerr := cf.ResolveZoneId(ctx); zerr != nil {
			*warnings = append(*warnings, "cloudflare zone unavailable: "+zerr.Error())
		} else if _, terr := cf.UpsertTXT(ctx, zoneId, txtHost, token); terr != nil {
			*warnings = append(*warnings, "cloudflare TXT upsert failed: "+terr.Error())
		} else if _, ierr := client.CustomDomainIssueCertificate(ctx, customId); ierr != nil {
			*warnings = append(*warnings, "customDomainIssueCertificate failed: "+ierr.Error())
		}
	} else {
		*warnings = append(*warnings, "railway did not return a verification token; verify the domain manually before issuing a certificate")
	}
	return domain
}

func waitForService(ctx context.Context, client *railway.Client, workspaceId, projectId, preferredName string, timeout time.Duration) (*railway.Service, error) {
	deadline := time.Now().Add(timeout)
	for {
		services, err := client.ServicesForProject(ctx, workspaceId, projectId)
		if err == nil && len(services) > 0 {
			for i := range services {
				if strings.EqualFold(services[i].Name, preferredName) {
					return &services[i], nil
				}
			}
			return &services[0], nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return nil, fmt.Errorf("service did not appear after githubRepoDeploy: %w", err)
			}
			return nil, fmt.Errorf("service did not appear after githubRepoDeploy within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func failApply(nodeId int, planId string, operatorId int, operatorName, detail string) error {
	recordAudit(nodeId, "deploy_apply", planId, "failed", operatorId, operatorName, detail)
	return fmt.Errorf("%s", detail)
}

func recordAudit(nodeId int, action, target, result string, operatorId int, operatorName, detail string) {
	_ = model.InsertNodeAudit(&model.NodeAudit{
		NodeId:       nodeId,
		Action:       action,
		Target:       truncateText(target, 255),
		Result:       result,
		OperatorId:   operatorId,
		OperatorName: operatorName,
		Detail:       truncateText(detail, 2000),
		CreatedTime:  common.GetTimestamp(),
	})
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// node.go M5 节点管理 API（/api/node，全部 RootAuth；写操作审计）。
package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/nodedeploy"
	"github.com/QuantumNous/new-api/service/nodemonitor"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

var validNodeProviders = map[string]bool{
	"railway": true, "koyeb": true, "render": true, "fly": true,
	"sealos": true, "clawcloud": true, "other": true,
}

// nodeRequest 节点新增/更新请求（更新时 nil 字段保持原值）。
type nodeRequest struct {
	Id            int     `json:"id"`
	Name          *string `json:"name"`
	Provider      *string `json:"provider"`
	ApiToken      *string `json:"api_token"`
	ClearApiToken bool    `json:"clear_api_token"`
	WorkspaceId   *string `json:"workspace_id"`
	ProjectId     *string `json:"project_id"`
	ServiceId     *string `json:"service_id"`
	EnvironmentId *string `json:"environment_id"`
	Repo          *string `json:"repo"`
	Branch        *string `json:"branch"`
	TargetPort    *int    `json:"target_port"`
	ServiceUrl    *string `json:"service_url"`
	ServiceDomain *string `json:"service_domain"`
	Domain        *string `json:"domain"`
	Enabled       *bool   `json:"enabled"`
}

func applyNodeRequest(node *model.Node, req *nodeRequest) {
	if req.Name != nil {
		node.Name = strings.TrimSpace(*req.Name)
	}
	if req.Provider != nil {
		node.Provider = strings.ToLower(strings.TrimSpace(*req.Provider))
	}
	if req.ApiToken != nil && strings.TrimSpace(*req.ApiToken) != "" {
		node.ApiTokenEnc = strings.TrimSpace(*req.ApiToken)
	}
	if req.ClearApiToken {
		node.ApiTokenEnc = ""
	}
	if req.WorkspaceId != nil {
		node.WorkspaceId = strings.TrimSpace(*req.WorkspaceId)
	}
	if req.ProjectId != nil {
		node.ProjectId = strings.TrimSpace(*req.ProjectId)
	}
	if req.ServiceId != nil {
		node.ServiceId = strings.TrimSpace(*req.ServiceId)
	}
	if req.EnvironmentId != nil {
		node.EnvironmentId = strings.TrimSpace(*req.EnvironmentId)
	}
	if req.Repo != nil {
		node.Repo = strings.TrimSpace(*req.Repo)
	}
	if req.Branch != nil {
		node.Branch = strings.TrimSpace(*req.Branch)
	}
	if req.TargetPort != nil {
		node.TargetPort = *req.TargetPort
	}
	if req.ServiceUrl != nil {
		node.ServiceUrl = strings.TrimRight(strings.TrimSpace(*req.ServiceUrl), "/")
	}
	if req.ServiceDomain != nil {
		node.ServiceDomain = strings.TrimSpace(*req.ServiceDomain)
	}
	if req.Domain != nil {
		node.Domain = strings.TrimSpace(*req.Domain)
	}
	if req.Enabled != nil {
		node.Enabled = *req.Enabled
	}
}

func validateNode(node *model.Node, isCreate bool) error {
	if strings.TrimSpace(node.Name) == "" {
		return errors.New("node name is required")
	}
	if node.Provider == "" {
		if isCreate {
			node.Provider = "railway"
		}
	}
	if !validNodeProviders[node.Provider] {
		return errors.New("provider must be one of railway|koyeb|render|fly|sealos|clawcloud|other")
	}
	for _, raw := range []string{node.ServiceUrl, node.ServiceDomain, node.Domain} {
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "://") && !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
			return errors.New("node URL must use http(s)")
		}
	}
	if node.TargetPort < 0 || node.TargetPort > 65535 {
		return errors.New("target_port must be 0-65535")
	}
	if isCreate && node.Branch == "" {
		node.Branch = "main"
	}
	if isCreate && node.TargetPort == 0 {
		node.TargetPort = 3000
	}
	return nil
}

// serializeNode 节点 API 展示（token 只回脱敏提示）。
func serializeNode(node *model.Node) gin.H {
	hint := ""
	if node.HasApiToken() {
		hint = common.MaskSecret(node.DecryptedToken())
	}
	return gin.H{
		"id": node.Id, "name": node.Name, "provider": node.Provider,
		"workspace_id": node.WorkspaceId, "project_id": node.ProjectId,
		"service_id": node.ServiceId, "environment_id": node.EnvironmentId,
		"repo": node.Repo, "branch": node.Branch, "target_port": node.TargetPort,
		"service_url": node.ServiceUrl, "service_domain": node.ServiceDomain, "domain": node.Domain,
		"healthy": node.Healthy, "latency_ms": node.LatencyMs, "last_beat": node.LastBeat,
		"balance_info": node.BalanceInfo, "last_error": node.LastError,
		"enabled": node.Enabled, "created_time": node.CreatedTime, "updated_time": node.UpdatedTime,
		"has_api_token": node.HasApiToken(), "api_token_hint": hint,
	}
}

// GetNodes GET /api/node
func GetNodes(c *gin.Context) {
	nodes, err := model.GetNodes()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	out := make([]gin.H, 0, len(nodes))
	for i := range nodes {
		out = append(out, serializeNode(&nodes[i]))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// CreateNode POST /api/node
func CreateNode(c *gin.Context) {
	var req nodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	node := &model.Node{Enabled: true}
	applyNodeRequest(node, &req)
	if err := validateNode(node, true); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := model.CreateNode(node); err != nil {
		common.ApiError(c, err)
		return
	}
	result := gin.H{"id": node.Id}
	if nodemonitor.NodeBaseURL(node) != "" {
		check := nodemonitor.CheckNode(c.Request.Context(), node)
		result["healthy"] = check.Healthy
		result["latency_ms"] = check.LatencyMs
		result["check_error"] = check.Error
	}
	recordManageAudit(c, "node.create", map[string]any{"id": node.Id, "name": node.Name, "provider": node.Provider})
	_ = model.InsertNodeAudit(&model.NodeAudit{
		NodeId: node.Id, Action: "create", Target: node.Name, Result: "success",
		OperatorId: c.GetInt("id"), OperatorName: c.GetString("username"),
		Detail: "provider=" + node.Provider, CreatedTime: common.GetTimestamp(),
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// UpdateNode PUT /api/node
func UpdateNode(c *gin.Context) {
	var req nodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	id := req.Id
	if id <= 0 {
		if queryId, qerr := strconv.Atoi(c.Query("id")); qerr == nil {
			id = queryId
		}
	}
	if id <= 0 {
		if paramId, perr := strconv.Atoi(c.Param("id")); perr == nil {
			id = paramId
		}
	}
	if id <= 0 {
		common.ApiErrorMsg(c, "invalid node id")
		return
	}
	node, err := model.GetNodeById(id)
	if err != nil {
		respondNodeError(c, err)
		return
	}
	applyNodeRequest(node, &req)
	if err := validateNode(node, false); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := model.UpdateNode(node); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "node.update", map[string]any{"id": node.Id, "name": node.Name})
	_ = model.InsertNodeAudit(&model.NodeAudit{
		NodeId: node.Id, Action: "update", Target: node.Name, Result: "success",
		OperatorId: c.GetInt("id"), OperatorName: c.GetString("username"),
		Detail: "fields updated", CreatedTime: common.GetTimestamp(),
	})
	updated, _ := model.GetNodeById(id)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": serializeNode(updated)})
}

// DeleteNode DELETE /api/node/:id
func DeleteNode(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid node id")
		return
	}
	node, err := model.GetNodeById(id)
	if err != nil {
		respondNodeError(c, err)
		return
	}
	if err := model.DeleteNode(id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "node.delete", map[string]any{"id": id, "name": node.Name})
	_ = model.InsertNodeAudit(&model.NodeAudit{
		NodeId: id, Action: "delete", Target: node.Name, Result: "success",
		OperatorId: c.GetInt("id"), OperatorName: c.GetString("username"),
		Detail: "node record removed; platform resources are untouched", CreatedTime: common.GetTimestamp(),
	})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// CheckNode POST /api/node/:id/check — 立即健康检查。
func CheckNode(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid node id")
		return
	}
	node, err := model.GetNodeById(id)
	if err != nil {
		respondNodeError(c, err)
		return
	}
	result := nodemonitor.CheckNode(c.Request.Context(), node)
	_ = model.InsertNodeAudit(&model.NodeAudit{
		NodeId: id, Action: "check", Target: node.Name, Result: boolResult(result.Healthy),
		OperatorId: c.GetInt("id"), OperatorName: c.GetString("username"),
		Detail: result.Error, CreatedTime: common.GetTimestamp(),
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func boolResult(ok bool) string {
	if ok {
		return "success"
	}
	return "failed"
}

// NodeDeployPlan POST /api/node/deploy/plan {nodeId, envOverrides}
func NodeDeployPlan(c *gin.Context) {
	var req struct {
		NodeId       int               `json:"nodeId"`
		EnvOverrides map[string]string `json:"envOverrides"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.NodeId <= 0 {
		common.ApiErrorMsg(c, "invalid request body (nodeId required)")
		return
	}
	plan, err := nodedeploy.BuildPlan(c.Request.Context(), req.NodeId, req.EnvOverrides)
	if err != nil {
		recordManageAudit(c, "node.deploy_plan", map[string]any{"node_id": req.NodeId, "success": false})
		_ = model.InsertNodeAudit(&model.NodeAudit{
			NodeId: req.NodeId, Action: "deploy_plan", Result: "rejected",
			OperatorId: c.GetInt("id"), OperatorName: c.GetString("username"),
			Detail: err.Error(), CreatedTime: common.GetTimestamp(),
		})
		respondNodeError(c, err)
		return
	}
	recordManageAudit(c, "node.deploy_plan", map[string]any{"node_id": req.NodeId, "plan_id": plan.PlanId, "actions": len(plan.Actions)})
	_ = model.InsertNodeAudit(&model.NodeAudit{
		NodeId: req.NodeId, Action: "deploy_plan", Target: plan.PlanId, Result: "success",
		OperatorId: c.GetInt("id"), OperatorName: c.GetString("username"),
		Detail: "actions=" + strconv.Itoa(len(plan.Actions)), CreatedTime: common.GetTimestamp(),
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": plan.Masked()})
}

// NodeDeployApply POST /api/node/deploy/apply {planId, confirm}
func NodeDeployApply(c *gin.Context) {
	var req struct {
		PlanId  string `json:"planId"`
		Confirm bool   `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.PlanId) == "" {
		common.ApiErrorMsg(c, "invalid request body (planId required)")
		return
	}
	result, err := nodedeploy.ApplyDeploy(c.Request.Context(), req.PlanId, req.Confirm, c.GetInt("id"), c.GetString("username"))
	if err != nil {
		recordManageAudit(c, "node.deploy_apply", map[string]any{"plan_id": req.PlanId, "success": false})
		respondNodeError(c, err)
		return
	}
	recordManageAudit(c, "node.deploy_apply", map[string]any{
		"plan_id": req.PlanId, "node_id": result.NodeId, "deployment_id": result.DeploymentId,
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// NodeFailoverPlan POST /api/node/:id/failover/plan
func NodeFailoverPlan(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid node id")
		return
	}
	var req struct {
		TargetNodeId int `json:"targetNodeId"`
	}
	_ = c.ShouldBindJSON(&req)
	plan, err := nodedeploy.BuildFailoverPlan(c.Request.Context(), id, req.TargetNodeId)
	if err != nil {
		_ = model.InsertNodeAudit(&model.NodeAudit{
			NodeId: id, Action: "failover_plan", Result: "rejected",
			OperatorId: c.GetInt("id"), OperatorName: c.GetString("username"),
			Detail: err.Error(), CreatedTime: common.GetTimestamp(),
		})
		respondNodeError(c, err)
		return
	}
	recordManageAudit(c, "node.failover_plan", map[string]any{"node_id": id, "plan_id": plan.PlanId, "to_node_id": plan.ToNodeId})
	_ = model.InsertNodeAudit(&model.NodeAudit{
		NodeId: id, Action: "failover_plan", Target: plan.PrimaryDomain, Result: "success",
		OperatorId: c.GetInt("id"), OperatorName: c.GetString("username"),
		Detail: "to=" + plan.ToNodeName, CreatedTime: common.GetTimestamp(),
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": plan})
}

// NodeFailoverApply POST /api/node/:id/failover/apply {planId, confirm}
func NodeFailoverApply(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid node id")
		return
	}
	var req struct {
		PlanId  string `json:"planId"`
		Confirm bool   `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.PlanId) == "" {
		common.ApiErrorMsg(c, "invalid request body (planId required)")
		return
	}
	result, err := nodedeploy.ApplyFailover(c.Request.Context(), req.PlanId, req.Confirm, c.GetInt("id"), c.GetString("username"))
	if err != nil {
		recordManageAudit(c, "node.failover_apply", map[string]any{"node_id": id, "plan_id": req.PlanId, "success": false})
		respondNodeError(c, err)
		return
	}
	recordManageAudit(c, "node.failover_apply", map[string]any{"node_id": id, "plan_id": req.PlanId, "to_node_id": result.ToNodeId})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// GetNodeSettings GET /api/node/settings
func GetNodeSettings(c *gin.Context) {
	smtpConfigured := strings.TrimSpace(common.SMTPServer) != "" && strings.TrimSpace(common.SMTPAccount) != ""
	cfTokenConfigured := strings.TrimSpace(operation_setting.CloudflareApiToken) != ""
	kvConfigured := strings.TrimSpace(operation_setting.CloudflareAccountId) != "" && strings.TrimSpace(operation_setting.CloudflareKvNamespaceId) != ""
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"balanceAlertThreshold":     operation_setting.NodeBalanceAlertThreshold,
		"alertWebhookUrl":           operation_setting.NodeAlertWebhookUrl,
		"alertEmail":                operation_setting.NodeAlertEmail,
		"monitorEnabled":            operation_setting.NodeMonitorEnabled,
		"autoFailover":              operation_setting.NodeAutoFailover,
		"primaryDomain":             operation_setting.NodePrimaryDomain,
		"deployPruneVariables":      operation_setting.NodeDeployPruneVariables,
		"deployPollTimeoutSeconds":  operation_setting.NodeDeployPollTimeoutSeconds,
		"deployEnvTemplate":         operation_setting.NodeDeployEnvTemplate,
		"nodeDenylist":              operation_setting.NodeDenylistList(),
		"nodeAllowlist":             operation_setting.NodeAllowlistList(),
		"smtpConfigured":            smtpConfigured,
		"cloudflareTokenConfigured": cfTokenConfigured,
		"cloudflareZoneId":          operation_setting.CloudflareZoneId,
		"cloudflareZoneName":        operation_setting.CloudflareZoneName,
		"cloudflareAccountId":       operation_setting.CloudflareAccountId,
		"cloudflareKvNamespaceId":   operation_setting.CloudflareKvNamespaceId,
		"cloudflareKvNodesKey":      operation_setting.CloudflareKvNodesKey,
		"cloudflareKvConfigured":    kvConfigured,
	}})
}

// UpdateNodeSettings PUT /api/node/settings
func UpdateNodeSettings(c *gin.Context) {
	var req struct {
		BalanceAlertThreshold    *float64 `json:"balanceAlertThreshold"`
		AlertWebhookUrl          *string  `json:"alertWebhookUrl"`
		AlertEmail               *string  `json:"alertEmail"`
		MonitorEnabled           *bool    `json:"monitorEnabled"`
		AutoFailover             *bool    `json:"autoFailover"`
		PrimaryDomain            *string  `json:"primaryDomain"`
		DeployPruneVariables     *bool    `json:"deployPruneVariables"`
		DeployPollTimeoutSeconds *int     `json:"deployPollTimeoutSeconds"`
		DeployEnvTemplate        *string  `json:"deployEnvTemplate"`
		NodeDenylist             []string `json:"nodeDenylist"`
		NodeAllowlist            []string `json:"nodeAllowlist"`
		CloudflareApiToken       *string  `json:"cloudflareApiToken"`
		ClearCloudflareApiToken  bool     `json:"clearCloudflareApiToken"`
		CloudflareZoneId         *string  `json:"cloudflareZoneId"`
		CloudflareZoneName       *string  `json:"cloudflareZoneName"`
		CloudflareAccountId      *string  `json:"cloudflareAccountId"`
		CloudflareKvNamespaceId  *string  `json:"cloudflareKvNamespaceId"`
		CloudflareKvNodesKey     *string  `json:"cloudflareKvNodesKey"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	values := map[string]string{}
	changed := make([]string, 0)
	if req.BalanceAlertThreshold != nil {
		values["NodeBalanceAlertThreshold"] = strconv.FormatFloat(*req.BalanceAlertThreshold, 'f', -1, 64)
		changed = append(changed, "balanceAlertThreshold")
	}
	if req.AlertWebhookUrl != nil {
		values["NodeAlertWebhookUrl"] = strings.TrimSpace(*req.AlertWebhookUrl)
		changed = append(changed, "alertWebhookUrl")
	}
	if req.AlertEmail != nil {
		values["NodeAlertEmail"] = strings.TrimSpace(*req.AlertEmail)
		changed = append(changed, "alertEmail")
	}
	if req.MonitorEnabled != nil {
		values["NodeMonitorEnabled"] = strconv.FormatBool(*req.MonitorEnabled)
		changed = append(changed, "monitorEnabled")
	}
	if req.AutoFailover != nil {
		values["NodeAutoFailover"] = strconv.FormatBool(*req.AutoFailover)
		changed = append(changed, "autoFailover")
	}
	if req.PrimaryDomain != nil {
		values["NodePrimaryDomain"] = strings.TrimSpace(*req.PrimaryDomain)
		changed = append(changed, "primaryDomain")
	}
	if req.DeployPruneVariables != nil {
		values["NodeDeployPruneVariables"] = strconv.FormatBool(*req.DeployPruneVariables)
		changed = append(changed, "deployPruneVariables")
	}
	if req.DeployPollTimeoutSeconds != nil && *req.DeployPollTimeoutSeconds > 0 {
		values["NodeDeployPollTimeoutSeconds"] = strconv.Itoa(*req.DeployPollTimeoutSeconds)
		changed = append(changed, "deployPollTimeoutSeconds")
	}
	if req.DeployEnvTemplate != nil {
		values["NodeDeployEnvTemplate"] = *req.DeployEnvTemplate
		changed = append(changed, "deployEnvTemplate")
	}
	if req.NodeDenylist != nil {
		encoded, _ := common.Marshal(req.NodeDenylist)
		values["NodeDenylist"] = string(encoded)
		changed = append(changed, "nodeDenylist")
	}
	if req.NodeAllowlist != nil {
		encoded, _ := common.Marshal(req.NodeAllowlist)
		values["NodeAllowlist"] = string(encoded)
		changed = append(changed, "nodeAllowlist")
	}
	if req.CloudflareApiToken != nil && strings.TrimSpace(*req.CloudflareApiToken) != "" {
		values["CloudflareApiToken"] = common.EncryptSecretBestEffort(strings.TrimSpace(*req.CloudflareApiToken))
		changed = append(changed, "cloudflareApiToken")
	}
	if req.ClearCloudflareApiToken {
		values["CloudflareApiToken"] = ""
		changed = append(changed, "cloudflareApiToken")
	}
	if req.CloudflareZoneId != nil {
		values["CloudflareZoneId"] = strings.TrimSpace(*req.CloudflareZoneId)
		changed = append(changed, "cloudflareZoneId")
	}
	if req.CloudflareZoneName != nil {
		values["CloudflareZoneName"] = strings.TrimSpace(*req.CloudflareZoneName)
		changed = append(changed, "cloudflareZoneName")
	}
	if req.CloudflareAccountId != nil {
		values["CloudflareAccountId"] = strings.TrimSpace(*req.CloudflareAccountId)
		changed = append(changed, "cloudflareAccountId")
	}
	if req.CloudflareKvNamespaceId != nil {
		values["CloudflareKvNamespaceId"] = strings.TrimSpace(*req.CloudflareKvNamespaceId)
		changed = append(changed, "cloudflareKvNamespaceId")
	}
	if req.CloudflareKvNodesKey != nil {
		values["CloudflareKvNodesKey"] = strings.TrimSpace(*req.CloudflareKvNodesKey)
		changed = append(changed, "cloudflareKvNodesKey")
	}
	if len(values) == 0 {
		common.ApiErrorMsg(c, "no settings provided")
		return
	}
	if err := model.UpdateOptionsBulk(values); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "node.settings_update", map[string]any{"keys": changed})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"updated": changed}})
}

// GetNodeAudit GET /api/node/audit?nodeId=&page=&page_size=
func GetNodeAudit(c *gin.Context) {
	nodeId, _ := strconv.Atoi(c.Query("nodeId"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	list, total, err := model.GetNodeAudits(nodeId, (page-1)*pageSize, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": list, "total": total, "page": page, "page_size": pageSize}})
}

// respondNodeError 把服务层错误映射为稳定的 HTTP 语义。
func respondNodeError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	code := "bad_request"
	switch {
	case errors.Is(err, nodedeploy.ErrNodeNotFound):
		status, code = http.StatusNotFound, "node_not_found"
	case errors.Is(err, nodedeploy.ErrDenylisted):
		status, code = http.StatusForbidden, "denylisted"
	case errors.Is(err, nodedeploy.ErrNotAllowed):
		status, code = http.StatusForbidden, "not_allowed"
	case errors.Is(err, nodedeploy.ErrPlanExpired):
		status, code = http.StatusBadRequest, "plan_expired"
	case errors.Is(err, nodedeploy.ErrPlanNodeChanged):
		status, code = http.StatusConflict, "plan_stale"
	case errors.Is(err, nodedeploy.ErrConfirmRequired):
		status, code = http.StatusBadRequest, "confirm_required"
	}
	c.JSON(status, gin.H{"success": false, "message": err.Error(), "code": code})
}

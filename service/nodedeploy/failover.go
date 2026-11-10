// failover.go 主域名切换：CF CNAME 更新 + Worker KV 节点表更新（dry-run plan + confirm apply）。
package nodedeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/cloudflare"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// FailoverPlan 主域名切换计划。
type FailoverPlan struct {
	PlanId        string   `json:"planId"`
	Kind          string   `json:"kind"` // failover
	FromNodeId    int      `json:"fromNodeId"`
	FromNodeName  string   `json:"fromNodeName"`
	ToNodeId      int      `json:"toNodeId"`
	ToNodeName    string   `json:"toNodeName"`
	PrimaryDomain string   `json:"primaryDomain"`
	ZoneId        string   `json:"zoneId"`
	FromTarget    string   `json:"fromTarget"`
	ToTarget      string   `json:"toTarget"`
	KVKey         string   `json:"kvKey"`
	KVFrom        string   `json:"kvFrom,omitempty"`
	KVTo          string   `json:"kvTo,omitempty"`
	Actions       []Action `json:"actions"`
	Warnings      []string `json:"warnings"`
	CreatedAt     int64    `json:"createdAt"`
	ExpiresAt     int64    `json:"expiresAt"`
	// 内部执行字段（API 展示可忽略）。
	KVSkipped bool `json:"kvSkipped,omitempty"`
}

// BuildFailoverPlan 生成主域名切换 dry-run 计划。targetNodeId=0 时自动选择首个健康备用节点。
func BuildFailoverPlan(ctx context.Context, sourceNodeId, targetNodeId int) (*FailoverPlan, error) {
	source, err := model.GetNodeById(sourceNodeId)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNodeNotFound, err)
	}
	opts := LoadOptions()
	primary := strings.TrimSpace(opts.PrimaryDomain)
	if primary == "" {
		return nil, fmt.Errorf("NodePrimaryDomain is not configured")
	}
	target, err := selectFailoverTarget(source, targetNodeId)
	if err != nil {
		return nil, err
	}
	if err := ValidateTargets(target, opts); err != nil {
		return nil, err
	}
	if !target.Enabled || !target.Healthy {
		return nil, fmt.Errorf("target node %d is not enabled/healthy", target.Id)
	}
	targetHost := nodeHost(target)
	if targetHost == "" {
		return nil, fmt.Errorf("target node %d has no Domain/ServiceDomain/ServiceUrl", target.Id)
	}

	cf := cloudflare.New(opts.Cloudflare)
	zoneId, err := cf.ResolveZoneId(ctx)
	if err != nil {
		return nil, err
	}
	current, err := cf.GetRecord(ctx, zoneId, primary, "CNAME")
	if err != nil {
		return nil, err
	}
	fromTarget := ""
	if current != nil {
		fromTarget = current.Content
	}

	warnings := make([]string, 0)
	kvConfigured := strings.TrimSpace(opts.Cloudflare.AccountId) != "" && strings.TrimSpace(opts.Cloudflare.KvNamespaceId) != ""
	kvKeyName := cloudflareKVKeyName()
	var kvFrom, kvTo string
	if kvConfigured {
		kvFrom, _ = cf.GetKV(ctx, kvKeyName)
		kvTo = buildWorkerKV(target, kvFrom)
	} else {
		warnings = append(warnings, "Cloudflare AccountId/KV namespace is not configured; Worker KV node table will not be updated")
	}

	now := time.Now()
	actions := []Action{{
		Op: "update", Resource: "dns", Name: primary,
		From: fromTarget, To: targetHost,
	}}
	if kvConfigured {
		actions = append(actions, Action{Op: "update", Resource: "worker_kv", Name: kvKeyName, From: truncateText(kvFrom, 500), To: kvTo})
	} else {
		actions = append(actions, Action{Op: "update", Resource: "worker_kv", Name: kvKeyName, Skipped: true})
	}
	plan := &FailoverPlan{
		PlanId:        newPlanId("failover"),
		Kind:          "failover",
		FromNodeId:    source.Id,
		FromNodeName:  source.Name,
		ToNodeId:      target.Id,
		ToNodeName:    target.Name,
		PrimaryDomain: primary,
		ZoneId:        zoneId,
		FromTarget:    fromTarget,
		ToTarget:      targetHost,
		KVKey:         kvKeyName,
		KVFrom:        kvFrom,
		KVTo:          kvTo,
		Actions:       actions,
		Warnings:      warnings,
		CreatedAt:     now.Unix(),
		ExpiresAt:     now.Add(PlanTTL).Unix(),
		KVSkipped:     !kvConfigured,
	}
	if err := DefaultStore.PutFailover(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// FailoverResult apply 结果。
type FailoverResult struct {
	PlanId        string `json:"planId"`
	PrimaryDomain string `json:"primaryDomain"`
	FromTarget    string `json:"fromTarget"`
	ToTarget      string `json:"toTarget"`
	ToNodeId      int    `json:"toNodeId"`
	KVUpdated     bool   `json:"kvUpdated"`
	DurationMs    int64  `json:"durationMs"`
}

// ApplyFailover 执行主域名切换（confirm=true；10 分钟内有效）。
func ApplyFailover(ctx context.Context, planId string, confirm bool, operatorId int, operatorName string) (*FailoverResult, error) {
	if !confirm {
		return nil, ErrConfirmRequired
	}
	plan, err := DefaultStore.GetFailover(planId)
	if err != nil {
		return nil, err
	}
	target, err := model.GetNodeById(plan.ToNodeId)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNodeNotFound, err)
	}
	opts := LoadOptions()
	if err := ValidateTargets(target, opts); err != nil {
		recordAudit(target.Id, "failover_apply", planId, "rejected", operatorId, operatorName, err.Error())
		return nil, err
	}
	if !target.Enabled || !target.Healthy {
		return nil, fmt.Errorf("target node %d is not enabled/healthy; refusing failover", target.Id)
	}
	started := time.Now()
	cf := cloudflare.New(opts.Cloudflare)
	zoneId := plan.ZoneId
	if zoneId == "" {
		zoneId, err = cf.ResolveZoneId(ctx)
		if err != nil {
			return nil, failApply(target.Id, plan.PlanId, operatorId, operatorName, "resolve cloudflare zone: "+err.Error())
		}
	}
	if _, err := cf.UpsertCNAME(ctx, zoneId, plan.PrimaryDomain, plan.ToTarget, true); err != nil {
		return nil, failApply(target.Id, plan.PlanId, operatorId, operatorName, "cloudflare CNAME update: "+err.Error())
	}
	kvUpdated := false
	if !plan.KVSkipped {
		kvValue := plan.KVTo
		if kvValue == "" {
			kvValue = buildWorkerKV(target, plan.KVFrom)
		}
		if err := cf.PutKV(ctx, plan.KVKey, kvValue); err != nil {
			return nil, failApply(target.Id, plan.PlanId, operatorId, operatorName, "cloudflare KV update: "+err.Error())
		}
		kvUpdated = true
	}
	DefaultStore.Delete(plan.PlanId)
	result := &FailoverResult{
		PlanId:        plan.PlanId,
		PrimaryDomain: plan.PrimaryDomain,
		FromTarget:    plan.FromTarget,
		ToTarget:      plan.ToTarget,
		ToNodeId:      target.Id,
		KVUpdated:     kvUpdated,
		DurationMs:    time.Since(started).Milliseconds(),
	}
	recordAudit(target.Id, "failover_apply", plan.PrimaryDomain, "success", operatorId, operatorName, fmt.Sprintf("%s -> %s kv=%t", plan.FromTarget, plan.ToTarget, kvUpdated))
	return result, nil
}

func selectFailoverTarget(source *model.Node, targetNodeId int) (*model.Node, error) {
	if targetNodeId > 0 {
		target, err := model.GetNodeById(targetNodeId)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNodeNotFound, err)
		}
		if target.Id == source.Id {
			return nil, fmt.Errorf("target node must differ from source node")
		}
		return target, nil
	}
	nodes, err := model.GetEnabledNodes()
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		node := &nodes[i]
		if node.Id == source.Id || !node.Healthy {
			continue
		}
		if nodeHost(node) == "" {
			continue
		}
		return node, nil
	}
	return nil, fmt.Errorf("no healthy spare node is available for failover")
}

// nodeHost 返回可 CNAME 指向的主机名（不含 scheme/路径）。
func nodeHost(node *model.Node) string {
	for _, candidate := range []string{node.Domain, node.ServiceDomain, node.ServiceUrl} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, "://") {
			if parsed, err := url.Parse(candidate); err == nil {
				candidate = parsed.Host
			}
		}
		candidate = strings.TrimSuffix(strings.TrimSpace(candidate), "/")
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func cloudflareKVKeyName() string {
	name := strings.TrimSpace(operation_setting.CloudflareKvNodesKey)
	if name == "" {
		return "nodes"
	}
	return name
}

// workerKVNode Worker KV 节点表中的一项。
type workerKVNode struct {
	Id      int    `json:"id"`
	Name    string `json:"name"`
	Url     string `json:"url"`
	Healthy bool   `json:"healthy"`
}

// workerKV Worker KV 节点表结构。
type workerKV struct {
	ActiveUrl    string         `json:"activeUrl"`
	ActiveNodeId int            `json:"activeNodeId"`
	UpdatedAt    int64          `json:"updatedAt"`
	Nodes        []workerKVNode `json:"nodes"`
}

// buildWorkerKV 合并现有 KV 内容（尽量保留未知字段）并更新 active 节点。
func buildWorkerKV(active *model.Node, existing string) string {
	nodes, err := model.GetEnabledNodes()
	if err != nil {
		nodes = []model.Node{*active}
	}
	table := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		_ = json.Unmarshal([]byte(existing), &table)
	}
	items := make([]workerKVNode, 0, len(nodes))
	seen := map[int]bool{}
	for i := range nodes {
		node := nodes[i]
		host := nodeHost(&node)
		if host == "" {
			continue
		}
		items = append(items, workerKVNode{Id: node.Id, Name: node.Name, Url: "https://" + host, Healthy: node.Healthy})
		seen[node.Id] = true
	}
	if !seen[active.Id] {
		items = append(items, workerKVNode{Id: active.Id, Name: active.Name, Url: "https://" + nodeHost(active), Healthy: active.Healthy})
	}
	table["activeUrl"] = "https://" + nodeHost(active)
	table["activeNodeId"] = active.Id
	table["updatedAt"] = time.Now().Unix()
	table["nodes"] = items
	encoded, err := json.Marshal(table)
	if err != nil {
		return fmt.Sprintf(`{"activeUrl":%q,"activeNodeId":%d}`, "https://"+nodeHost(active), active.Id)
	}
	return string(encoded)
}

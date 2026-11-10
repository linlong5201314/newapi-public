// monitor.go 节点健康巡检 + 余额检查后台任务：60s 周期，Redis 锁防多节点重复执行。
package nodemonitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/nodedeploy"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const (
	monitorInterval = 60 * time.Second
	monitorLockTTL  = 55 * time.Second
	monitorWorkers  = 5
)

// Summary 一轮巡检汇总。
type Summary struct {
	Checked       int `json:"checked"`
	Healthy       int `json:"healthy"`
	Unhealthy     int `json:"unhealthy"`
	Alerts        int `json:"alerts"`
	FailoverPlans int `json:"failover_plans"`
}

var (
	memoryLockMu   sync.Mutex
	memoryLockedAt time.Time
)

// Start 启动巡检循环（阻塞；main.go goroutine 调用）。
func Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	common.SysLog("node monitor started (interval 60s)")
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()
	// 启动先跑一轮，节点创建后立刻有健康数据。
	RunOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			common.SysLog("node monitor stopped")
			return
		case <-ticker.C:
			RunOnce(ctx)
		}
	}
}

// RunOnce 执行一轮巡检（e2e/运维手动触发复用）。
func RunOnce(ctx context.Context) Summary {
	summary := Summary{}
	if !operation_setting.NodeMonitorEnabled {
		return summary
	}
	if !acquireMonitorLock(ctx) {
		return summary
	}
	nodes, err := model.GetEnabledNodes()
	if err != nil {
		common.SysLog("node monitor: list nodes failed: " + err.Error())
		return summary
	}
	sem := make(chan struct{}, monitorWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range nodes {
		node := nodes[i]
		wg.Add(1)
		sem <- struct{}{}
		go func(node model.Node) {
			defer wg.Done()
			defer func() { <-sem }()
			local := inspectNode(ctx, &node)
			mu.Lock()
			summary.Checked++
			if local.Healthy {
				summary.Healthy++
			} else {
				summary.Unhealthy++
			}
			summary.Alerts += local.Alerts
			summary.FailoverPlans += local.FailoverPlans
			mu.Unlock()
		}(node)
	}
	wg.Wait()
	if summary.Checked > 0 {
		common.SysLog(fmt.Sprintf("node monitor: checked=%d healthy=%d unhealthy=%d alerts=%d failover_plans=%d",
			summary.Checked, summary.Healthy, summary.Unhealthy, summary.Alerts, summary.FailoverPlans))
	}
	return summary
}

type nodeInspection struct {
	Healthy       bool
	Alerts        int
	FailoverPlans int
}

// inspectNode 单节点：健康检查 → 状态迁移告警 → 余额检查 → 备用节点 plan/自动切换。
func inspectNode(ctx context.Context, node *model.Node) nodeInspection {
	out := nodeInspection{}
	wasHealthy := node.Healthy
	result := CheckNode(ctx, node)
	out.Healthy = result.Healthy

	if wasHealthy && !result.Healthy {
		message := fmt.Sprintf("node %s (ID %d) is DOWN: %s", node.Name, node.Id, result.Error)
		SendAlert(ctx, node, "unhealthy", message, 0)
		out.Alerts++
		out.FailoverPlans += ensureFailoverPlan(ctx, node, "unhealthy")
	} else if !wasHealthy && result.Healthy && node.LastBeat != 0 {
		SendAlert(ctx, node, "recovered", fmt.Sprintf("node %s (ID %d) recovered, latency %dms", node.Name, node.Id, result.LatencyMs), 0)
		out.Alerts++
	}
	if !result.Healthy {
		return out
	}

	snapshot, err := CheckBalance(ctx, node)
	if err != nil {
		common.SysLog(fmt.Sprintf("node monitor: balance check node %d failed: %s", node.Id, err.Error()))
		return out
	}
	if snapshot == nil {
		return out
	}
	if snapshot.Remaining < operation_setting.NodeBalanceAlertThreshold {
		message := fmt.Sprintf("node %s (ID %d) balance low: $%.2f remaining < threshold $%.2f",
			node.Name, node.Id, snapshot.Remaining, operation_setting.NodeBalanceAlertThreshold)
		SendAlert(ctx, node, "balance_low", message, snapshot.Remaining)
		out.Alerts++
		out.FailoverPlans += ensureFailoverPlan(ctx, node, "balance_low")
	}
	return out
}

// ensureFailoverPlan 生成（并在 NodeAutoFailover=true 时执行）主域名切换 plan。
// 默认只生成 plan，绝不自动 apply（蓝图 §3.2 余额告警红线）。
func ensureFailoverPlan(ctx context.Context, node *model.Node, reason string) int {
	cooldownKey := fmt.Sprintf("%d:failover:%s", node.Id, reason)
	if !alertAllowed(cooldownKey) {
		return 0
	}
	plan, err := nodedeploy.BuildFailoverPlan(ctx, node.Id, 0)
	if err != nil {
		common.SysLog(fmt.Sprintf("node monitor: failover plan for node %d skipped: %s", node.Id, err.Error()))
		return 0
	}
	_ = model.InsertNodeAudit(&model.NodeAudit{
		NodeId:       node.Id,
		Action:       "failover_plan",
		Target:       plan.PrimaryDomain,
		Result:       "success",
		OperatorId:   0,
		OperatorName: "system",
		Detail:       fmt.Sprintf("reason=%s to=%s plan=%s auto=%t", reason, plan.ToNodeName, plan.PlanId, operation_setting.NodeAutoFailover),
		CreatedTime:  common.GetTimestamp(),
	})
	if !operation_setting.NodeAutoFailover {
		return 1
	}
	if _, err := nodedeploy.ApplyFailover(ctx, plan.PlanId, true, 0, "system"); err != nil {
		common.SysLog(fmt.Sprintf("node monitor: auto failover apply failed: %s", err.Error()))
	}
	return 1
}

// ResetForTest 清空进程内巡检锁与告警冷却状态。
// 仅用于同一进程内重复执行 RunOnce 的测试（生产代码不要调用）。
func ResetForTest() {
	memoryLockMu.Lock()
	memoryLockedAt = time.Time{}
	memoryLockMu.Unlock()
	alertMu.Lock()
	alertLastSent = map[string]int64{}
	alertMu.Unlock()
}

// acquireMonitorLock 多节点互斥：Redis SETNX；无 Redis 仅 master 进程执行。
func acquireMonitorLock(ctx context.Context) bool {
	if common.RedisEnabled && common.RDB != nil {
		ok, err := common.RDB.SetNX(ctx, "node:monitor:lock", 1, monitorLockTTL).Result()
		if err != nil {
			common.SysLog("node monitor lock error: " + err.Error())
			return false
		}
		return ok
	}
	if !common.IsMasterNode {
		return false
	}
	memoryLockMu.Lock()
	defer memoryLockMu.Unlock()
	if time.Since(memoryLockedAt) < monitorLockTTL {
		return false
	}
	memoryLockedAt = time.Now()
	return true
}

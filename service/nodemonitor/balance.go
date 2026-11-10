// balance.go Railway 余额检查：workspaceBilling + estimatedUsage 双口径取 min。
package nodemonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/railway"
)

// BalanceSnapshot 余额快照（存 Node.BalanceInfo JSON）。
type BalanceSnapshot struct {
	Remaining                   float64  `json:"remaining"`
	Currency                    string   `json:"currency"`
	UpdatedAt                   int64    `json:"updatedAt"`
	Source                      string   `json:"source"`
	CreditBalance               *float64 `json:"creditBalance,omitempty"`
	CurrentUsage                *float64 `json:"currentUsage,omitempty"`
	EstimatedUsage              *float64 `json:"estimatedUsage,omitempty"`
	RemainingUsageCreditBalance *float64 `json:"remainingUsageCreditBalance,omitempty"`
	HasExhaustedFreePlan        bool     `json:"hasExhaustedFreePlan,omitempty"`
	TrialDaysRemaining          *int     `json:"trialDaysRemaining,omitempty"`
}

// ParseBalanceSnapshot 解析节点余额 JSON（无效返回 nil）。
func ParseBalanceSnapshot(raw string) *BalanceSnapshot {
	if raw == "" {
		return nil
	}
	var snapshot BalanceSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil
	}
	return &snapshot
}

// CheckBalance 查询节点 Railway 余额并回写 BalanceInfo。非 railway 节点返回 nil（不适用）。
func CheckBalance(ctx context.Context, node *model.Node) (*BalanceSnapshot, error) {
	if node.Provider != "railway" {
		return nil, nil
	}
	token := node.DecryptedToken()
	if token == "" {
		return nil, fmt.Errorf("node %d has no API token", node.Id)
	}
	client := railway.NewClient(token)
	billing, err := client.WorkspaceBilling(ctx, node.WorkspaceId)
	if err != nil {
		return nil, err
	}
	estimated, err := client.EstimatedUsage(ctx, node.WorkspaceId)
	if err != nil {
		// 实时计量是兜底口径，失败不阻断 billing 结果。
		estimated = nil
	}
	snapshot := &BalanceSnapshot{
		Currency:                    "USD",
		UpdatedAt:                   common.GetTimestamp(),
		Source:                      "railway:workspaceBilling+estimatedUsage",
		CreditBalance:               billing.CreditBalance,
		CurrentUsage:                billing.CurrentUsage,
		EstimatedUsage:              estimated,
		RemainingUsageCreditBalance: billing.RemainingUsageCreditBalance,
		HasExhaustedFreePlan:        billing.HasExhaustedFreePlan,
		TrialDaysRemaining:          billing.TrialDaysRemaining,
	}
	remaining, ok := computeRemaining(billing, estimated)
	if !ok {
		return nil, fmt.Errorf("railway billing returned no usable balance fields")
	}
	snapshot.Remaining = remaining
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if err := model.UpdateNodeBalance(node.Id, string(encoded)); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// computeRemaining 双口径取 min：剩余额度余额 / 额度硬上限-实时用量。
func computeRemaining(billing *railway.WorkspaceBilling, estimated *float64) (float64, bool) {
	candidates := make([]float64, 0, 2)
	if billing != nil {
		if billing.HasExhaustedFreePlan {
			return 0, true
		}
		if billing.RemainingUsageCreditBalance != nil {
			candidates = append(candidates, *billing.RemainingUsageCreditBalance)
		} else if billing.CreditBalance != nil {
			candidates = append(candidates, *billing.CreditBalance)
		}
		if billing.UsageLimit != nil && billing.UsageLimit.HardLimit != nil && *billing.UsageLimit.HardLimit > 0 {
			usage := 0.0
			if billing.CurrentUsage != nil {
				usage = *billing.CurrentUsage
			}
			if estimated != nil && *estimated > usage {
				usage = *estimated
			}
			remaining := *billing.UsageLimit.HardLimit - usage
			if remaining < 0 {
				remaining = 0
			}
			candidates = append(candidates, remaining)
		}
	}
	if len(candidates) == 0 {
		return 0, false
	}
	remaining := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate < remaining {
			remaining = candidate
		}
	}
	return remaining, true
}

// balanceFresh 判断快照是否在有效期内（巡检周期 60s，5 分钟内视为新鲜）。
func balanceFresh(snapshot *BalanceSnapshot) bool {
	if snapshot == nil || snapshot.UpdatedAt == 0 {
		return false
	}
	return time.Since(time.Unix(snapshot.UpdatedAt, 0)) < 5*time.Minute
}

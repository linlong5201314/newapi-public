package nodemonitor

import (
	"testing"

	"github.com/QuantumNous/new-api/service/railway"
)

func floatPtr(value float64) *float64 { return &value }
func intPtr(value int) *int           { return &value }

func TestComputeRemainingUsesMinimumOfTwoMeasures(t *testing.T) {
	billing := &railway.WorkspaceBilling{
		CreditBalance:               floatPtr(10),
		CurrentUsage:                floatPtr(1),
		RemainingUsageCreditBalance: floatPtr(7),
		UsageLimit: &railway.UsageLimit{
			HardLimit: floatPtr(10),
		},
	}
	// remainingUsageCreditBalance=7；hardLimit-estimated=10-4=6 → 取 min=6。
	remaining, ok := computeRemaining(billing, floatPtr(4))
	if !ok || remaining != 6 {
		t.Fatalf("expected min(7,6)=6, got %v ok=%v", remaining, ok)
	}
	// 实时计量低于 billing currentUsage 时取 currentUsage。
	remaining, ok = computeRemaining(billing, floatPtr(0.5))
	if !ok || remaining != 7 {
		t.Fatalf("expected min(7,9)=7, got %v ok=%v", remaining, ok)
	}
}

func TestComputeRemainingHandlesExhaustedAndMissingData(t *testing.T) {
	exhausted := &railway.WorkspaceBilling{HasExhaustedFreePlan: true, CreditBalance: floatPtr(3)}
	if remaining, ok := computeRemaining(exhausted, nil); !ok || remaining != 0 {
		t.Fatalf("exhausted plan must report 0, got %v ok=%v", remaining, ok)
	}
	if _, ok := computeRemaining(&railway.WorkspaceBilling{TrialDaysRemaining: intPtr(3)}, nil); ok {
		t.Fatal("billing without balance fields must report unavailable")
	}
	hardLimit := &railway.WorkspaceBilling{
		CurrentUsage: floatPtr(12),
		UsageLimit:   &railway.UsageLimit{HardLimit: floatPtr(10)},
	}
	if remaining, ok := computeRemaining(hardLimit, nil); !ok || remaining != 0 {
		t.Fatalf("over-limit remaining must clamp to 0, got %v ok=%v", remaining, ok)
	}
}

func TestParseBalanceSnapshot(t *testing.T) {
	if ParseBalanceSnapshot("") != nil {
		t.Fatal("empty balance must parse to nil")
	}
	if ParseBalanceSnapshot("{invalid") != nil {
		t.Fatal("invalid JSON must parse to nil")
	}
	snapshot := ParseBalanceSnapshot(`{"remaining":1.25,"currency":"USD"}`)
	if snapshot == nil || snapshot.Remaining != 1.25 || snapshot.Currency != "USD" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

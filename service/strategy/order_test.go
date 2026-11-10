package strategy

import (
	"testing"
)

func targets(ids ...int) []RouteTarget {
	out := make([]RouteTarget, len(ids))
	for i, id := range ids {
		out[i] = RouteTarget{ChannelId: id}
	}
	return out
}

func TestPriorityKeepsOrder(t *testing.T) {
	got := Get(Priority).Order(targets(3, 1, 2), &ReqCtx{})
	for i, want := range []int{3, 1, 2} {
		if got[i].ChannelId != want {
			t.Fatalf("priority order broken: %v", got)
		}
	}
}

func TestUnknownStrategyFallsBackToPriority(t *testing.T) {
	got := Get("does-not-exist").Order(targets(5, 6), &ReqCtx{})
	if got[0].ChannelId != 5 || got[1].ChannelId != 6 {
		t.Fatalf("fallback should keep order: %v", got)
	}
}

func TestWeightedFirstIsFromSet(t *testing.T) {
	ts := []RouteTarget{{ChannelId: 1, Weight: 100}, {ChannelId: 2, Weight: 1}}
	for i := 0; i < 50; i++ {
		got := Get(Weighted).Order(ts, &ReqCtx{})
		if len(got) != 2 {
			t.Fatalf("weighted must return all targets")
		}
		if got[0].ChannelId != 1 && got[0].ChannelId != 2 {
			t.Fatalf("weighted first not from set: %v", got[0])
		}
	}
}

func TestRoundRobinRotates(t *testing.T) {
	ctx := &ReqCtx{RuleId: 900001}
	seen := map[int]int{}
	for i := 0; i < 4; i++ {
		got := Get(RoundRobin).Order(targets(1, 2), ctx)
		seen[got[0].ChannelId]++
	}
	if seen[1] != 2 || seen[2] != 2 {
		t.Fatalf("round-robin should alternate: %v", seen)
	}
}

func TestLeastUsedSortsAscending(t *testing.T) {
	ctx := &ReqCtx{UsageToday: map[int]int64{1: 100, 2: 5, 3: 50}}
	got := Get(LeastUsed).Order(targets(1, 2, 3), ctx)
	if got[0].ChannelId != 2 || got[1].ChannelId != 3 || got[2].ChannelId != 1 {
		t.Fatalf("least-used order wrong: %v", got)
	}
}

func TestLowestLatencySortsAscending(t *testing.T) {
	ctx := &ReqCtx{Channels: map[int]*ChannelInfo{
		1: {Id: 1, ResponseTime: 900},
		2: {Id: 2, ResponseTime: 50},
		3: {Id: 3, ResponseTime: 0}, // 无数据视为最快
	}}
	got := Get(LowestLatency).Order(targets(1, 2, 3), ctx)
	if got[0].ChannelId != 3 || got[1].ChannelId != 2 || got[2].ChannelId != 1 {
		t.Fatalf("lowest-latency order wrong: %v", got)
	}
}

func TestLkgpPromotesLastGood(t *testing.T) {
	ruleId := 900002
	SetLastKnownGood(ruleId, 2)
	ctx := &ReqCtx{RuleId: ruleId}
	got := Get(LKGP).Order(targets(1, 2, 3), ctx)
	if got[0].ChannelId != 2 {
		t.Fatalf("lkgp should promote last good: %v", got)
	}
	// 其余保持原顺序
	if got[1].ChannelId != 1 || got[2].ChannelId != 3 {
		t.Fatalf("lkgp tail order wrong: %v", got)
	}
}

func TestResetAwarePrefersKnownBalance(t *testing.T) {
	ctx := &ReqCtx{Channels: map[int]*ChannelInfo{
		1: {Id: 1, BalanceUpdatedTime: 100, Balance: 0.5},
		2: {Id: 2, BalanceUpdatedTime: 100, Balance: 9.9},
		3: {Id: 3},
	}}
	got := Get(ResetAware).Order(targets(1, 2, 3), ctx)
	if got[0].ChannelId != 2 || got[1].ChannelId != 1 || got[2].ChannelId != 3 {
		t.Fatalf("reset-aware order wrong: %v", got)
	}
}

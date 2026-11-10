package strategy

import (
	"math/rand"
	"sort"
)

// priority：按 targets 原顺序，第一个健康目标即选，失败顺延（默认策略）。
type priorityStrategy struct{}

func (priorityStrategy) Name() string { return Priority }
func (priorityStrategy) Order(targets []RouteTarget, ctx *ReqCtx) []RouteTarget {
	return cloneTargets(targets)
}

// weighted：按 weight 轮盘赌选首目标，其余按权重降序回退。
type weightedStrategy struct{}

func (weightedStrategy) Name() string { return Weighted }
func (weightedStrategy) Order(targets []RouteTarget, ctx *ReqCtx) []RouteTarget {
	out := cloneTargets(targets)
	if len(out) <= 1 {
		return out
	}
	total := 0
	for _, t := range out {
		total += effectiveWeight(t)
	}
	firstIdx := 0
	if total > 0 {
		roll := rand.Intn(total)
		acc := 0
		for i, t := range out {
			acc += effectiveWeight(t)
			if roll < acc {
				firstIdx = i
				break
			}
		}
	}
	first := out[firstIdx]
	rest := append(out[:firstIdx], out[firstIdx+1:]...)
	// 其余按权重降序（稳定，保持同权重的声明顺序）
	sort.SliceStable(rest, func(i, j int) bool {
		return effectiveWeight(rest[i]) > effectiveWeight(rest[j])
	})
	return append([]RouteTarget{first}, rest...)
}

// round-robin：per-rule 计数器取模轮转。计数器优先 Redis INCR，
// 无 Redis 用进程内 map（多节点允许不精确）。
type roundRobinStrategy struct{}

func (roundRobinStrategy) Name() string { return RoundRobin }
func (roundRobinStrategy) Order(targets []RouteTarget, ctx *ReqCtx) []RouteTarget {
	out := cloneTargets(targets)
	if len(out) <= 1 {
		return out
	}
	var ruleId int
	if ctx != nil {
		ruleId = ctx.RuleId
	}
	n := NextRoundRobin(ruleId)
	start := int(n % int64(len(out)))
	if start < 0 {
		start = 0
	}
	rotated := append(out[start:], out[:start]...)
	return rotated
}

// random：Fisher-Yates 洗牌。
type randomStrategy struct{}

func (randomStrategy) Name() string { return Random }
func (randomStrategy) Order(targets []RouteTarget, ctx *ReqCtx) []RouteTarget {
	out := cloneTargets(targets)
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// least-used：按 (channelId) 今日用量升序；无用量数据视为 0。
type leastUsedStrategy struct{}

func (leastUsedStrategy) Name() string { return LeastUsed }
func (leastUsedStrategy) Order(targets []RouteTarget, ctx *ReqCtx) []RouteTarget {
	out := cloneTargets(targets)
	usage := map[int]int64{}
	if ctx != nil && ctx.UsageToday != nil {
		usage = ctx.UsageToday
	}
	sort.SliceStable(out, func(i, j int) bool {
		return usage[out[i].ChannelId] < usage[out[j].ChannelId]
	})
	return out
}

// lowest-latency：按 channels.response_time 滑动值升序；<=0（无数据）视为最快，
// 让未探测目标优先被尝试。
type lowestLatencyStrategy struct{}

func (lowestLatencyStrategy) Name() string { return LowestLatency }
func (lowestLatencyStrategy) Order(targets []RouteTarget, ctx *ReqCtx) []RouteTarget {
	out := cloneTargets(targets)
	latency := func(id int) int {
		if ctx != nil && ctx.Channels != nil {
			if ch, ok := ctx.Channels[id]; ok && ch != nil && ch.ResponseTime > 0 {
				return ch.ResponseTime
			}
		}
		return 0
	}
	sort.SliceStable(out, func(i, j int) bool {
		return latency(out[i].ChannelId) < latency(out[j].ChannelId)
	})
	return out
}

// lkgp（last known good provider）：上次成功的 channelId 提队首，其余保持原顺序。
type lkgpStrategy struct{}

func (lkgpStrategy) Name() string { return LKGP }
func (lkgpStrategy) Order(targets []RouteTarget, ctx *ReqCtx) []RouteTarget {
	out := cloneTargets(targets)
	if len(out) <= 1 || ctx == nil {
		return out
	}
	lastGood, ok := GetLastKnownGood(ctx.RuleId)
	if !ok {
		return out
	}
	for i, t := range out {
		if t.ChannelId == lastGood {
			if i == 0 {
				return out
			}
			first := out[i]
			rest := append(out[:i], out[i+1:]...)
			return append([]RouteTarget{first}, rest...)
		}
	}
	return out
}

// reset-aware：有额度信息（BalanceUpdatedTime>0）的目标优先，余额高的在前；
// 无额度信息的目标按声明顺序排在最后。
type resetAwareStrategy struct{}

func (resetAwareStrategy) Name() string { return ResetAware }
func (resetAwareStrategy) Order(targets []RouteTarget, ctx *ReqCtx) []RouteTarget {
	out := cloneTargets(targets)
	known := make([]RouteTarget, 0, len(out))
	unknown := make([]RouteTarget, 0, len(out))
	for _, t := range out {
		if ctx != nil && ctx.Channels != nil {
			if ch, ok := ctx.Channels[t.ChannelId]; ok && ch != nil && ch.BalanceUpdatedTime > 0 {
				known = append(known, t)
				continue
			}
		}
		unknown = append(unknown, t)
	}
	sort.SliceStable(known, func(i, j int) bool {
		var bi, bj float64
		if ch := ctx.Channels[known[i].ChannelId]; ch != nil {
			bi = ch.Balance
		}
		if ch := ctx.Channels[known[j].ChannelId]; ch != nil {
			bj = ch.Balance
		}
		return bi > bj // 余额低的排后
	})
	return append(known, unknown...)
}

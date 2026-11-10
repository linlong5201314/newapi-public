// Package strategy 实现 M2 用户级路由规则的目标排序策略集。
//
// 蓝图：docs/M2-路由与免费源设计.md §1.2（一期 8 策略）。
// 每个策略是纯排序函数（round-robin / lkgp 带外部状态，见 state_store.go），
// 输入目标序列，输出重排后的目标序列；执行与回退语义由 service 层的
// 规则执行器负责（瞬时错误集、per-target 超时、fallbackOnlyOnQuotaExhaustion）。
package strategy

import "strings"

// RouteTarget 与 model.RouteRuleTarget 同构（strategy 包不依赖 model，
// 由 service 层做转换，保持策略可独立单测）。
type RouteTarget struct {
	ChannelId                     int
	Weight                        int
	ModelOverride                 string
	FallbackOnlyOnQuotaExhaustion bool
}

// ChannelInfo 策略排序所需的渠道快照字段。
type ChannelInfo struct {
	Id                 int
	Enabled            bool
	ResponseTime       int // channels.response_time，毫秒滑动值；<=0 表示无数据
	Balance            float64
	BalanceUpdatedTime int64
}

// ReqCtx 一次请求排序所需的上下文。
type ReqCtx struct {
	RuleId     int
	UserId     int
	Channels   map[int]*ChannelInfo // channelId -> 快照
	UsageToday map[int]int64        // channelId -> 今日 tokens（least-used 用，可 nil）
}

// Strategy 统一抽象：对候选目标排序，队首为首选目标。
type Strategy interface {
	Name() string
	Order(targets []RouteTarget, ctx *ReqCtx) []RouteTarget
}

// 一期 8 策略名（与文档 §1.2 表格一致，落库值）。
const (
	Priority      = "priority"
	Weighted      = "weighted"
	RoundRobin    = "round-robin"
	Random        = "random"
	LeastUsed     = "least-used"
	LowestLatency = "lowest-latency"
	LKGP          = "lkgp"
	ResetAware    = "reset-aware"
)

var registry = map[string]Strategy{
	Priority:      priorityStrategy{},
	Weighted:      weightedStrategy{},
	RoundRobin:    roundRobinStrategy{},
	Random:        randomStrategy{},
	LeastUsed:     leastUsedStrategy{},
	LowestLatency: lowestLatencyStrategy{},
	LKGP:          lkgpStrategy{},
	ResetAware:    resetAwareStrategy{},
}

// Get 按名取策略；未知/空名回落 priority。
func Get(name string) Strategy {
	if s, ok := registry[strings.ToLower(strings.TrimSpace(name))]; ok {
		return s
	}
	return registry[Priority]
}

// Valid 判断策略名是否受支持（CRUD 校验用；空名视为 priority）。
func Valid(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return true
	}
	_, ok := registry[name]
	return ok
}

// Names 返回受支持的策略名列表（固定顺序，供 API/前端展示）。
func Names() []string {
	return []string{Priority, Weighted, RoundRobin, Random, LeastUsed, LowestLatency, LKGP, ResetAware}
}

// cloneTargets 复制目标切片，保证策略不修改调用方数组。
func cloneTargets(targets []RouteTarget) []RouteTarget {
	out := make([]RouteTarget, len(targets))
	copy(out, targets)
	return out
}

// effectiveWeight 缺省权重按 1 计，负权重按 0 计。
func effectiveWeight(t RouteTarget) int {
	if t.Weight < 0 {
		return 0
	}
	if t.Weight == 0 {
		return 1
	}
	return t.Weight
}

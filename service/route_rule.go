package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/strategy"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// M2 用户级路由规则：执行器
//
// 蓝图 docs/M2-路由与免费源设计.md §1.3：
//   1. 解析 model 后先查该用户 enabled 规则（60s 缓存防每请求查库）；
//   2. 命中 → 策略排序 + 回退循环语义（瞬时错误集 {408,429,500,502,503,504}、
//      per-target 超时 targetTimeoutMs、fallbackOnlyOnQuotaExhaustion 只在
//      配额耗尽类错误触发、目标耗尽才失败）；
//   3. 未命中 → M1 现有 owner 桶路径（行为不变）。
//
// 接入点：CacheGetRandomSatisfiedChannel（middleware 预选与 relay 重试循环
// 共用）；重试决策见 relay_error.go 中 DecideRelayRetry 的规则分支。
// ---------------------------------------------------------------------------

// routeRuleTransientStatuses 瞬时错误集（文档 §1.3/清单 §4）。
var routeRuleTransientStatuses = map[int]bool{
	408: true, 429: true, 500: true, 502: true, 503: true, 504: true,
}

// isRouteRuleQuotaStatus 配额耗尽类错误：402 付费耗尽 / 429 速率或配额触顶
// （new-api 业务码 14018 类错误上游统一落在 402/429 上）。
func isRouteRuleQuotaStatus(status int) bool {
	return status == 402 || status == 429
}

// isRouteRuleRetryableStatus 规则路径下可推进到下一尝试的状态码。
// status<100 视为网络层失败（连接拒绝/超时等），同样可回退。
func isRouteRuleRetryableStatus(status int) bool {
	if status < 100 {
		return true
	}
	return routeRuleTransientStatuses[status] || isRouteRuleQuotaStatus(status)
}

// ---------------------------------------------------------------------------
// 规则缓存（60s，防每请求查库；CRUD 写入时主动失效）
// ---------------------------------------------------------------------------

const routeRuleCacheTTL = 60 * time.Second

type routeRuleCacheEntry struct {
	rules     []*model.RouteRule
	expiresAt time.Time
}

var routeRuleCache sync.Map // userId -> routeRuleCacheEntry

// GetCachedEnabledRouteRules 返回用户 enabled 规则（60s 内存缓存）。
func GetCachedEnabledRouteRules(userId int) []*model.RouteRule {
	if userId <= 0 {
		return nil
	}
	if v, ok := routeRuleCache.Load(userId); ok {
		entry := v.(routeRuleCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.rules
		}
	}
	rules, err := model.GetEnabledRouteRulesByUserId(userId)
	if err != nil {
		common.SysError(fmt.Sprintf("failed to load route rules for user %d: %s", userId, err.Error()))
		return nil
	}
	routeRuleCache.Store(userId, routeRuleCacheEntry{rules: rules, expiresAt: time.Now().Add(routeRuleCacheTTL)})
	return rules
}

// InvalidateRouteRuleCache CRUD 写入后调用。
func InvalidateRouteRuleCache(userId int) {
	routeRuleCache.Delete(userId)
}

// MatchRouteRule 按虚拟模型名精确匹配用户 enabled 规则。
func MatchRouteRule(userId int, modelName string) *model.RouteRule {
	if userId <= 0 || modelName == "" {
		return nil
	}
	for _, rule := range GetCachedEnabledRouteRules(userId) {
		if rule != nil && rule.Name == modelName {
			return rule
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 规则执行状态（per-request，存 gin context）
// ---------------------------------------------------------------------------

type routeRuleExecState struct {
	rule            *model.RouteRule
	ordered         []strategy.RouteTarget
	channels        map[int]*strategy.ChannelInfo
	attempts        map[int]int // channelId -> 已消耗尝试次数
	memo            map[int]*strategy.RouteTarget
	lastChannelId   int
	maxRetries      int
	retryDelayMs    int
	targetTimeoutMs int
}

// GetRouteRuleExecState 读取当前请求的规则执行状态（未命中规则时为 nil）。
func GetRouteRuleExecState(c *gin.Context) *routeRuleExecState {
	if c == nil {
		return nil
	}
	if state, ok := common.GetContextKeyType[*routeRuleExecState](c, constant.ContextKeyRouteRuleState); ok {
		return state
	}
	return nil
}

func (s *routeRuleExecState) findTarget(channelId int) *strategy.RouteTarget {
	for i := range s.ordered {
		if s.ordered[i].ChannelId == channelId {
			return &s.ordered[i]
		}
	}
	return nil
}

// computeNext 计算下一尝试目标（只读，不推进状态）：
//  1. 上一目标瞬时错误且同目标重试次数未超 maxRetries → 留在原目标；
//  2. 否则取排序序列中首个未耗尽且本次可参与的目标
//     （fallbackOnlyOnQuotaExhaustion 目标仅在配额耗尽类错误后可参与）。
func (s *routeRuleExecState) computeNext(lastStatus int) *strategy.RouteTarget {
	quotaErr := isRouteRuleQuotaStatus(lastStatus)
	if lastStatus != 0 && s.lastChannelId != 0 && routeRuleTransientStatuses[lastStatus] {
		if s.attempts[s.lastChannelId] <= s.maxRetries {
			if t := s.findTarget(s.lastChannelId); t != nil {
				return t
			}
		}
	}
	for i := range s.ordered {
		t := &s.ordered[i]
		if s.attempts[t.ChannelId] > 0 {
			continue // 该目标已失败过（成功会直接结束请求），视为耗尽
		}
		if info := s.channels[t.ChannelId]; info == nil || !info.Enabled {
			continue
		}
		if t.FallbackOnlyOnQuotaExhaustion && !quotaErr {
			continue
		}
		return t
	}
	return nil
}

// HasNextAttempt 判定在给定错误下是否还有可尝试目标（DecideRelayRetry 用）。
func (s *routeRuleExecState) HasNextAttempt(lastStatus int) bool {
	return s.computeNext(lastStatus) != nil
}

// selectForRetry 选择第 retryIdx 次尝试的目标；memo 化保证 middleware 预选
// 与 relay 循环 retry=0 返回同一目标。
func (s *routeRuleExecState) selectForRetry(retryIdx int, lastStatus int) *strategy.RouteTarget {
	if t, ok := s.memo[retryIdx]; ok {
		return t
	}
	t := s.computeNext(lastStatus)
	if t == nil {
		return nil
	}
	s.attempts[t.ChannelId]++
	s.lastChannelId = t.ChannelId
	s.memo[retryIdx] = t
	return t
}

// ---------------------------------------------------------------------------
// 状态初始化
// ---------------------------------------------------------------------------

var routeRuleUsageCache sync.Map // userId -> {usage map[int]int64, expiresAt}

type routeRuleUsageCacheEntry struct {
	usage     map[int]int64
	expiresAt time.Time
}

// getRouteRuleUsageToday least-used 策略数据源：logs 表当日聚合，缓存 60s。
func getRouteRuleUsageToday(userId int) map[int]int64 {
	if v, ok := routeRuleUsageCache.Load(userId); ok {
		entry := v.(routeRuleUsageCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.usage
		}
	}
	usage := map[int]int64{}
	dayStart := time.Now().Truncate(24 * time.Hour).Unix()
	type row struct {
		ChannelId int   `gorm:"column:channel_id"`
		Tokens    int64 `gorm:"column:tokens"`
	}
	var rows []row
	err := model.LOG_DB.Model(&model.Log{}).
		Select("channel_id, SUM(prompt_tokens + completion_tokens) AS tokens").
		Where("user_id = ? AND type = ? AND created_at >= ? AND channel_id > 0", userId, model.LogTypeConsume, dayStart).
		Group("channel_id").Scan(&rows).Error
	if err != nil {
		common.SysError(fmt.Sprintf("failed to aggregate route rule usage for user %d: %s", userId, err.Error()))
	} else {
		for _, r := range rows {
			usage[r.ChannelId] = r.Tokens
		}
	}
	routeRuleUsageCache.Store(userId, routeRuleUsageCacheEntry{usage: usage, expiresAt: time.Now().Add(routeRuleCacheTTL)})
	return usage
}

// initRouteRuleExecState 首次命中时构建执行状态：过滤非法/越权目标渠道、
// 组装策略上下文并计算排序序列。
func initRouteRuleExecState(c *gin.Context, rule *model.RouteRule, ownerUserId int) *routeRuleExecState {
	cfg := rule.GetConfig()
	targets, err := rule.GetTargets()
	if err != nil || len(targets) == 0 {
		return nil
	}
	st := &routeRuleExecState{
		rule:            rule,
		channels:        map[int]*strategy.ChannelInfo{},
		attempts:        map[int]int{},
		memo:            map[int]*strategy.RouteTarget{},
		maxRetries:      cfg.MaxRetries,
		retryDelayMs:    cfg.RetryDelayMs,
		targetTimeoutMs: cfg.TargetTimeoutMs,
	}
	sTargets := make([]strategy.RouteTarget, 0, len(targets))
	for _, t := range targets {
		if t.ChannelId <= 0 {
			continue
		}
		channel, chErr := model.CacheGetChannel(t.ChannelId)
		if chErr != nil || channel == nil {
			continue
		}
		// 防御：目标渠道必须属于规则属主（CRUD 已校验，运行时再挡一次）
		if channel.OwnerId != ownerUserId {
			continue
		}
		st.channels[t.ChannelId] = &strategy.ChannelInfo{
			Id:                 channel.Id,
			Enabled:            channel.Status == common.ChannelStatusEnabled,
			ResponseTime:       channel.ResponseTime,
			Balance:            channel.Balance,
			BalanceUpdatedTime: channel.BalanceUpdatedTime,
		}
		sTargets = append(sTargets, strategy.RouteTarget{
			ChannelId:                     t.ChannelId,
			Weight:                        t.Weight,
			ModelOverride:                 t.ModelOverride,
			FallbackOnlyOnQuotaExhaustion: t.FallbackOnlyOnQuotaExhaustion,
		})
	}
	if len(sTargets) == 0 {
		return nil
	}
	reqCtx := &strategy.ReqCtx{
		RuleId:   rule.Id,
		UserId:   ownerUserId,
		Channels: st.channels,
	}
	if rule.Strategy == strategy.LeastUsed {
		reqCtx.UsageToday = getRouteRuleUsageToday(ownerUserId)
	}
	st.ordered = strategy.Get(rule.Strategy).Order(sTargets, reqCtx)
	common.SetContextKey(c, constant.ContextKeyRouteRuleState, st)
	return st
}

// ---------------------------------------------------------------------------
// 选路入口（CacheGetRandomSatisfiedChannel 调用）
// ---------------------------------------------------------------------------

// trySelectRouteRuleChannel 命中规则时返回本次尝试的渠道。
// matched=false 表示无规则命中，调用方继续 M1 路径；
// matched=true 且 err!=nil 表示目标耗尽。
func trySelectRouteRuleChannel(param *RetryParam, ownerUserId int) (channel *model.Channel, selectGroup string, matched bool, err error) {
	c := param.Ctx
	if c == nil || ownerUserId <= 0 || param.ModelName == "" {
		return nil, "", false, nil
	}
	state := GetRouteRuleExecState(c)
	if state == nil {
		rule := MatchRouteRule(ownerUserId, param.ModelName)
		if rule == nil {
			return nil, "", false, nil
		}
		state = initRouteRuleExecState(c, rule, ownerUserId)
		if state == nil {
			return nil, "", true, errors.New("route rule has no usable target channel")
		}
		logger.LogDebug(c, "route rule matched: rule=%s id=%d strategy=%s user=%d", rule.Name, rule.Id, rule.Strategy, ownerUserId)
	}
	retryIdx := param.GetRetry()
	lastStatus := common.GetContextKeyInt(c, constant.ContextKeyRouteRuleLastErrorStatus)
	target := state.selectForRetry(retryIdx, lastStatus)
	if target == nil {
		return nil, "", true, errors.New("route rule targets exhausted")
	}
	if retryIdx > 0 && state.retryDelayMs > 0 {
		time.Sleep(time.Duration(state.retryDelayMs) * time.Millisecond)
	}
	channel, err = model.CacheGetChannel(target.ChannelId)
	if err != nil || channel == nil {
		return nil, "", true, fmt.Errorf("route rule target channel %d unavailable", target.ChannelId)
	}
	if channel.Status != common.ChannelStatusEnabled || channel.OwnerId != ownerUserId {
		return nil, "", true, fmt.Errorf("route rule target channel %d disabled", target.ChannelId)
	}
	// 模型改写：targets[].modelOverride 优先，渠道自身 ModelMapping 由
	// SetupContextForSelectedChannel 照常应用。
	common.SetContextKey(c, constant.ContextKeyRouteRuleModelOverride, target.ModelOverride)
	return channel, param.TokenGroup, true, nil
}

// ---------------------------------------------------------------------------
// relay 层钩子
// ---------------------------------------------------------------------------

// decideRouteRuleRetry 规则路径的重试决策（DecideRelayRetry 调用）：
// 非瞬时/非配额错误直接停；有剩余目标才 retry；目标耗尽才失败。
func decideRouteRuleRetry(c *gin.Context, err *types.NewAPIError) (PolicyDecision, bool) {
	state := GetRouteRuleExecState(c)
	if state == nil {
		return PolicyDecision{}, false
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "route_rule"}, true
	}
	status := err.StatusCode
	if !isRouteRuleRetryableStatus(status) {
		return PolicyDecision{Action: "stop", Reason: "route_rule_status_not_retryable", Source: "route_rule"}, true
	}
	if state.HasNextAttempt(status) {
		return PolicyDecision{Action: "retry", Reason: "route_rule_next_target", Source: "route_rule"}, true
	}
	return PolicyDecision{Action: "stop", Reason: "route_rule_targets_exhausted", Source: "route_rule"}, true
}

// ReportRouteRuleSuccess 请求成功后上报：lkgp 策略回写"上次成功渠道"。
func ReportRouteRuleSuccess(c *gin.Context, channelId int) {
	state := GetRouteRuleExecState(c)
	if state == nil || channelId <= 0 {
		return
	}
	if state.rule.Strategy == strategy.LKGP {
		strategy.SetLastKnownGood(state.rule.Id, channelId)
	}
}

// RecordRouteRuleAttemptError relay 每次失败后写入状态码，供规则执行器
// 判定瞬时错误/配额耗尽。非规则请求写入无害（状态不存在则无人读取）。
func RecordRouteRuleAttemptError(c *gin.Context, statusCode int) {
	if c == nil {
		return
	}
	common.SetContextKey(c, constant.ContextKeyRouteRuleLastErrorStatus, statusCode)
}

// RouteRuleRetryBudget 返回规则路径的重试预算（最大 retry 下标）。
// 规则语义要求"目标耗尽才算失败"，与全局 RetryTimes 无关：
// 预算 = len(targets) * (1 + maxRetries) - 1；非规则请求返回 -1。
func RouteRuleRetryBudget(c *gin.Context) int {
	state := GetRouteRuleExecState(c)
	if state == nil || len(state.ordered) == 0 {
		return -1
	}
	return len(state.ordered)*(state.maxRetries+1) - 1
}

// RouteRuleTargetTimeout 返回规则路径 per-target 超时（0=非规则请求）。
func RouteRuleTargetTimeout(c *gin.Context) time.Duration {
	state := GetRouteRuleExecState(c)
	if state == nil || state.targetTimeoutMs <= 0 {
		return 0
	}
	return time.Duration(state.targetTimeoutMs) * time.Millisecond
}

// RouteRuleAttemptContext 为规则路径的本次尝试构造独立超时上下文
// （文档 §1.3：per-target 超时，超时不记故障、直接下一目标）。
// 返回 nil ctx 表示非规则请求。
// 注意：必须从 baseCtx（进入重试循环前的原始请求上下文）派生，
// 不能用 c.Request.Context()——c.Request 每轮被上一轮的超时包装替换，
// 逐轮派生会叠出已取消的父上下文。
func RouteRuleAttemptContext(c *gin.Context, baseCtx context.Context) (context.Context, context.CancelFunc) {
	timeout := RouteRuleTargetTimeout(c)
	if timeout <= 0 || baseCtx == nil {
		return nil, nil
	}
	return context.WithTimeout(baseCtx, timeout)
}

// MergeRouteRuleModelOverride 把 targets[].modelOverride 并入渠道 model_mapping
// （SetupContextForSelectedChannel 调用）：originModel -> override 置顶。
func MergeRouteRuleModelOverride(channelMapping string, originModel string, override string) string {
	if override == "" || originModel == "" {
		return channelMapping
	}
	mapping := map[string]string{}
	if channelMapping != "" && channelMapping != "{}" {
		if err := common.Unmarshal([]byte(channelMapping), &mapping); err != nil {
			mapping = map[string]string{}
		}
	}
	mapping[originModel] = override
	data, err := common.Marshal(mapping)
	if err != nil {
		return channelMapping
	}
	return string(data)
}

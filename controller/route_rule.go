package controller

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/strategy"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// 用户级路由规则 API（/api/route_rule，UserAuth）
//
// 蓝图 docs/M2-路由与免费源设计.md §1.4。全部端点强制 UserId=当前登录用户；
// 跨租户资源一律 404（不区分"不存在"与"不属于你"）；保存时校验 targets
// 渠道归属（必须是自己的渠道）与 RouteRuleMaxPerUser 上限。
// ---------------------------------------------------------------------------

var routeRuleNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,63}$`)

func writeRouteRuleNotFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{
		"success": false,
		"message": "route rule not found",
	})
}

func loadSelfRouteRule(c *gin.Context, id int) (*model.RouteRule, bool) {
	if id <= 0 {
		writeRouteRuleNotFound(c)
		return nil, false
	}
	rule, err := model.GetRouteRuleById(id)
	if err != nil || rule == nil || rule.UserId != c.GetInt("id") {
		writeRouteRuleNotFound(c)
		return nil, false
	}
	return rule, true
}

// validateRouteRulePayload 校验并规范化写入负载；返回错误消息（"" 通过）。
func validateRouteRulePayload(c *gin.Context, rule *model.RouteRule) string {
	uid := c.GetInt("id")
	rule.Name = strings.TrimSpace(rule.Name)
	if !routeRuleNamePattern.MatchString(rule.Name) {
		return "Invalid rule name: 1-64 chars, letters/digits/. _ : / -, must start with letter or digit"
	}
	rule.Strategy = strings.ToLower(strings.TrimSpace(rule.Strategy))
	if rule.Strategy == "" {
		rule.Strategy = strategy.Priority
	}
	if !strategy.Valid(rule.Strategy) {
		return fmt.Sprintf("Unknown strategy: %s (supported: %s)", rule.Strategy, strings.Join(strategy.Names(), ", "))
	}
	targets, err := rule.GetTargets()
	if err != nil || len(targets) == 0 {
		return "targets must be a non-empty JSON array of {channelId, weight?, modelOverride?, fallbackOnlyOnQuotaExhaustion?}"
	}
	if len(targets) > model.RouteRuleMaxTargets {
		return fmt.Sprintf("Too many targets: max %d per rule", model.RouteRuleMaxTargets)
	}
	// 渠道归属校验：targets 里的 channelId 必须全部属于当前用户
	seen := map[int]bool{}
	ids := make([]int, 0, len(targets))
	for _, t := range targets {
		if t.ChannelId <= 0 {
			return "target channelId must be a positive integer"
		}
		if t.Weight < 0 {
			return "target weight must be >= 0"
		}
		if !seen[t.ChannelId] {
			seen[t.ChannelId] = true
			ids = append(ids, t.ChannelId)
		}
	}
	var ownedCount int64
	if err := model.DB.Model(&model.Channel{}).
		Where("owner_id = ? AND id IN ?", uid, ids).
		Count(&ownedCount).Error; err != nil {
		common.SysError(common.LogText("failed to verify route rule target ownership: %s", err.Error()))
		return "Failed to verify target channels. Please try again later"
	}
	if int(ownedCount) != len(ids) {
		return "All target channels must be your own channels"
	}
	// Config 校验（非法 JSON 直接拒绝；合法 JSON 落库原样）
	if strings.TrimSpace(rule.Config) != "" {
		probe := struct {
			MaxRetries      *int `json:"maxRetries"`
			RetryDelayMs    *int `json:"retryDelayMs"`
			TargetTimeoutMs *int `json:"targetTimeoutMs"`
		}{}
		if err := common.Unmarshal([]byte(rule.Config), &probe); err != nil {
			return "Invalid config JSON: expect {maxRetries?, retryDelayMs?, targetTimeoutMs?}"
		}
		if probe.MaxRetries != nil && *probe.MaxRetries < 0 {
			return "config.maxRetries must be >= 0"
		}
		if probe.RetryDelayMs != nil && *probe.RetryDelayMs < 0 {
			return "config.retryDelayMs must be >= 0"
		}
		if probe.TargetTimeoutMs != nil && *probe.TargetTimeoutMs < 1000 {
			return "config.targetTimeoutMs must be >= 1000"
		}
	}
	// Targets 落库前重新序列化（去空白/统一字段）
	if err := rule.SetTargets(targets); err != nil {
		return "Invalid targets JSON"
	}
	return ""
}

// GetRouteRules GET /api/route_rule/
func GetRouteRules(c *gin.Context) {
	rules, err := model.GetRouteRulesByUserId(c.GetInt("id"))
	if err != nil {
		common.SysError(common.LogText("failed to list route rules: %s", err.Error()))
		common.ApiErrorT(c, "Failed to fetch route rules. Please try again later")
		return
	}
	if rules == nil {
		rules = []*model.RouteRule{}
	}
	common.ApiSuccess(c, gin.H{
		"items":        rules,
		"total":        len(rules),
		"max_per_user": operation_setting.RouteRuleMaxPerUser,
		"strategies":   strategy.Names(),
	})
}

// GetRouteRule GET /api/route_rule/:id
func GetRouteRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		writeRouteRuleNotFound(c)
		return
	}
	rule, ok := loadSelfRouteRule(c, id)
	if !ok {
		return
	}
	common.ApiSuccess(c, rule)
}

// routeRuleRequest 写入负载：targets/config 以结构化 JSON 接收，
// 落库前序列化为 RouteRule.Targets/Config 文本列。
type routeRuleRequest struct {
	Id       int                     `json:"id"`
	Name     string                  `json:"name"`
	Strategy string                  `json:"strategy"`
	Targets  []model.RouteRuleTarget `json:"targets"`
	Config   *model.RouteRuleConfig  `json:"config"`
	Enabled  *bool                   `json:"enabled"`
}

func (r *routeRuleRequest) toModel() (*model.RouteRule, error) {
	rule := &model.RouteRule{
		Id:       r.Id,
		Name:     r.Name,
		Strategy: r.Strategy,
	}
	if r.Targets != nil {
		if err := rule.SetTargets(r.Targets); err != nil {
			return nil, err
		}
	}
	if r.Config != nil {
		data, err := common.Marshal(r.Config)
		if err != nil {
			return nil, err
		}
		rule.Config = string(data)
	}
	if r.Enabled != nil {
		rule.Enabled = *r.Enabled
	}
	return rule, nil
}

// AddRouteRule POST /api/route_rule/
func AddRouteRule(c *gin.Context) {
	uid := c.GetInt("id")
	payload := &routeRuleRequest{}
	if err := c.ShouldBindJSON(payload); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	rule, err := payload.toModel()
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	rule.Id = 0
	rule.UserId = uid
	rule.Enabled = payload.Enabled == nil || *payload.Enabled
	if msg := validateRouteRulePayload(c, rule); msg != "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
		return
	}
	maxPerUser := operation_setting.RouteRuleMaxPerUser
	if maxPerUser <= 0 {
		maxPerUser = 20
	}
	count, err := model.CountRouteRulesByUserId(uid)
	if err != nil {
		common.SysError(common.LogText("failed to count route rules for user %d: %s", uid, err.Error()))
		common.ApiErrorT(c, "Failed to verify route rule quota. Please try again later")
		return
	}
	if int(count) >= maxPerUser {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("Route rule limit reached: max %d rules per user", maxPerUser),
		})
		return
	}
	if err := rule.Insert(); err != nil {
		if model.IsRouteRuleDuplicateNameErr(err) {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "A rule with this name already exists"})
			return
		}
		common.SysError(common.LogText("failed to insert route rule: %s", err.Error()))
		common.ApiErrorT(c, "Failed to create route rule. Please try again later")
		return
	}
	service.InvalidateRouteRuleCache(uid)
	common.ApiSuccess(c, rule)
}

// UpdateRouteRule PUT /api/route_rule/（body 带 id；归属不符 404；禁止改归属）
func UpdateRouteRule(c *gin.Context) {
	uid := c.GetInt("id")
	payload := &routeRuleRequest{}
	if err := c.ShouldBindJSON(payload); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	rule, ok := loadSelfRouteRule(c, payload.Id)
	if !ok {
		return
	}
	updated, err := payload.toModel()
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	rule.Name = updated.Name
	rule.Strategy = updated.Strategy
	rule.Targets = updated.Targets
	rule.Config = updated.Config
	if payload.Enabled != nil {
		rule.Enabled = *payload.Enabled
	}
	if msg := validateRouteRulePayload(c, rule); msg != "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
		return
	}
	if err := rule.Update(); err != nil {
		if model.IsRouteRuleDuplicateNameErr(err) {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "A rule with this name already exists"})
			return
		}
		common.SysError(common.LogText("failed to update route rule: %s", err.Error()))
		common.ApiErrorT(c, "Failed to update route rule. Please try again later")
		return
	}
	service.InvalidateRouteRuleCache(uid)
	common.ApiSuccess(c, rule)
}

// DeleteRouteRule DELETE /api/route_rule/:id
func DeleteRouteRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		writeRouteRuleNotFound(c)
		return
	}
	rule, ok := loadSelfRouteRule(c, id)
	if !ok {
		return
	}
	if err := rule.Delete(); err != nil {
		common.SysError(common.LogText("failed to delete route rule: %s", err.Error()))
		common.ApiErrorT(c, "Failed to delete route rule. Please try again later")
		return
	}
	service.InvalidateRouteRuleCache(c.GetInt("id"))
	common.ApiSuccess(c, nil)
}

// ToggleRouteRule POST /api/route_rule/:id/toggle
func ToggleRouteRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		writeRouteRuleNotFound(c)
		return
	}
	rule, ok := loadSelfRouteRule(c, id)
	if !ok {
		return
	}
	if err := rule.SetEnabled(!rule.Enabled); err != nil {
		common.SysError(common.LogText("failed to toggle route rule: %s", err.Error()))
		common.ApiErrorT(c, "Failed to toggle route rule. Please try again later")
		return
	}
	service.InvalidateRouteRuleCache(c.GetInt("id"))
	common.ApiSuccess(c, rule)
}

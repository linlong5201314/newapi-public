package controller

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// 自建渠道详情（我的渠道 → 点开一个渠道）
//
// 一个请求把详情抽屉要展示的东西全部取齐，避免前端串 5 个接口：
//   GET  /api/channel/self/:id/detail   渠道 + 健康度 + 额度 + 出口池 + 模板
//   POST /api/channel/self/:id/allow_ips 免费层 IP 白名单（允许清空）
//
// 免费层的口径：authMode=ip 的来源不需要密钥，只按出口 IP 白名单放行，
// 这类渠道不计量 token（也就没有「剩余额度」可显示）；密钥层才展示额度。
// ---------------------------------------------------------------------------

// GetSelfChannelDetail GET /api/channel/self/:id/detail
func GetSelfChannelDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		writeSelfChannelNotFound(c)
		return
	}
	channel, ok := loadSelfOwnedChannel(c, id, true)
	if !ok {
		return
	}
	uid := c.GetInt("id")

	setting := channel.GetSetting()
	otherInfo := channel.GetOtherInfo()
	templateId, _ := otherInfo["templateId"].(string)
	tpl := constant.GetFreeSourceTemplate(templateId)

	// 出口代理：""=跟随用户出口池，direct=直连，其余=固定代理（脱敏展示）。
	proxy := strings.TrimSpace(setting.Proxy)
	proxyView := gin.H{"mode": "pool", "label": "跟随出口 IP 池轮换", "value": ""}
	if proxy == "direct" {
		proxyView = gin.H{"mode": "direct", "label": "强制直连（不走代理）", "value": "direct"}
	} else if proxy != "" {
		proxyView = gin.H{"mode": "fixed", "label": "固定代理", "value": maskProxyUrl(proxy)}
	}

	// 健康度：近 5 次成功率 + 平均延迟（多节点共享 Redis 观测）。
	health := model.ChannelHealthOf(channel.Id)

	// 本月用量：额度是按渠道统计的，和 free_usage 的池级口径分开给，便于详情页展示。
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	var usedThisMonth int64
	if err := model.LOG_DB.Model(&model.Log{}).
		Select("COALESCE(SUM(prompt_tokens + completion_tokens), 0)").
		Where("user_id = ? AND channel_id = ? AND type = ? AND created_at >= ?",
			uid, channel.Id, model.LogTypeConsume, monthStart).
		Scan(&usedThisMonth).Error; err != nil {
		common.SysLog("failed to aggregate channel detail usage: " + err.Error())
	}

	// 免费层判定：模板写 authMode=ip 或 authType=none 且不需要 key，都按「IP 免费层」处理。
	ipFree := false
	authType := "key"
	monthlyTokens := int64(0)
	freeType := ""
	templateName := ""
	if tpl != nil {
		templateName = tpl.Name
		authType = tpl.EffectiveAuthType()
		monthlyTokens = tpl.MonthlyTokens
		freeType = tpl.FreeType
		ipFree = tpl.AuthMode == "ip" || (!tpl.KeyRequired && authType == "none")
	}
	if strings.EqualFold(channel.Tier, "free") && tpl == nil {
		ipFree = strings.TrimSpace(channel.AllowIps) != ""
	}

	tokenEstimate := gin.H{
		"measured":  false,
		"hidden":    ipFree,
		"reason":    "",
		"remaining": nil,
		"monthly":   monthlyTokens,
		"used":      usedThisMonth,
	}
	if ipFree {
		tokenEstimate["reason"] = "免费 IP 层不计量 token，只受上游限速与出口 IP 轮换影响"
	} else if monthlyTokens > 0 {
		remaining := monthlyTokens - usedThisMonth
		if remaining < 0 {
			remaining = 0
		}
		tokenEstimate["measured"] = true
		tokenEstimate["remaining"] = remaining
	} else {
		tokenEstimate["reason"] = "该来源未公布月度 token 预算，实际额度由上游动态限流"
	}

	// 用户出口池概况：多少个节点、多少条订阅、多少节点真的可拨号。
	endpoints, _ := model.ListProxyEndpoints(uid)
	subscriptions, _ := model.ListProxySubscriptions(uid)
	usableUrls, _ := model.ListUsableProxyUrls(uid)
	enabledEndpoints := 0
	regionCount := map[string]int{}
	for _, endpoint := range endpoints {
		if endpoint.Enabled {
			enabledEndpoints++
		}
		if region := strings.TrimSpace(endpoint.Region); region != "" {
			regionCount[region]++
		}
	}
	subNodes := 0
	for _, sub := range subscriptions {
		subNodes += sub.NodeCount
	}

	models := make([]string, 0)
	for _, item := range strings.Split(channel.Models, ",") {
		if v := strings.TrimSpace(item); v != "" {
			models = append(models, v)
		}
	}

	// key 一律不回给前端：详情页只需要「有没有配密钥」，不需要密钥本体。
	hasKey := strings.TrimSpace(channel.Key) != ""
	channel.Key = ""

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"channel":   channel,
			"models":    models,
			"health":    health,
			"proxy":     proxyView,
			"allow_ips": channel.AllowIps,
			"template": gin.H{
				"id":             templateId,
				"name":           templateName,
				"auth_type":      authType,
				"auth_mode":      templateAuthMode(tpl),
				"free_type":      freeType,
				"monthly_tokens": monthlyTokens,
				"ip_free":        ipFree,
			},
			"token_estimate": tokenEstimate,
			"proxy_pool": gin.H{
				"endpoint_total":     len(endpoints),
				"endpoint_enabled":   enabledEndpoints,
				"usable_url_count":   len(usableUrls),
				"subscription_total": len(subscriptions),
				"subscription_nodes": subNodes,
				"regions":            regionCount,
			},
			"has_key":  hasKey,
			"is_owned": true,
		},
	})
}

// templateAuthMode 模板的 authMode（模板缺失时给空串，避免 nil 解引用）。
func templateAuthMode(tpl *constant.FreeSourceTemplate) string {
	if tpl == nil {
		return ""
	}
	return tpl.AuthMode
}

// SetSelfChannelAllowIpsHandler POST /api/channel/self/:id/allow_ips
// body: {"allowIps":"1.2.3.4,10.0.0.0/8"}
// 与 visibility 接口的区别：这里允许传空串来「清空白名单」，
// 而 visibility 接口只在非空时更新（防老客户端误清）。
func SetSelfChannelAllowIpsHandler(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		writeSelfChannelNotFound(c)
		return
	}
	var req struct {
		AllowIps string `json:"allowIps"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	if err := model.SetSelfChannelAllowIps(c.GetInt("id"), id, strings.TrimSpace(req.AllowIps)); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"channelId": id, "allowIps": strings.TrimSpace(req.AllowIps)})
}

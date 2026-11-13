package controller

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// M2 免费源一键模板 + 免费额度 API（挂 /api/channel/self 组）
//
// 蓝图 docs/M2-路由与免费源设计.md §3/§4：
//   GET /api/channel/self/free_templates  模板列表
//   POST /api/channel/self/from_template  body {templateId, key, name?, extra?}
//   GET /api/channel/self/free_usage      模板渠道按 poolKey 聚合额度
// ---------------------------------------------------------------------------

// GetFreeSourceTemplates GET /api/channel/self/free_templates
func GetFreeSourceTemplates(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"items": constant.GetFreeSourceTemplates(),
		"total": len(constant.GetFreeSourceTemplates()),
	})
}

// AddSelfChannelFromTemplate POST /api/channel/self/from_template
// body: {"templateId":"groq","key":"gsk_...","name":"可选","extra":{"accountId":"..."}}
func AddSelfChannelFromTemplate(c *gin.Context) {
	uid := c.GetInt("id")
	req := struct {
		TemplateId string            `json:"templateId"`
		Key        string            `json:"key"`
		Name       string            `json:"name"`
		Extra      map[string]string `json:"extra"`
		// Proxy 可选：出口代理策略。
		//   ""        → 跟随用户出口 IP 池（逐请求轮换，默认）
		//   "direct"  → 强制直连（忽略用户出口池）
		//   "http://…"/"socks5://…" → 该渠道固定走这个代理
		Proxy string `json:"proxy"`
	}{}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	tpl := constant.GetFreeSourceTemplate(req.TemplateId)
	if tpl == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "Unknown templateId"})
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if tpl.KeyRequired && req.Key == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "API key is required for this template"})
		return
	}
	// 免 key 源（kilo-code / ovhcloud / pollinations 等匿名层）：渠道表 key 不能为空，
	// 灌一个可识别的占位 key，上游忽略 Authorization 即可（实测 pollinations/kilo/ovh 均接受任意非空 key）
	if req.Key == "" {
		req.Key = "sk-free-" + tpl.Id
	}
	// extraFields（如 cloudflare-ai 的 accountId）替换 baseUrl 占位符
	baseURL := tpl.BaseUrl
	for _, field := range tpl.ExtraFields {
		placeholder := "{" + field + "}"
		if !strings.Contains(baseURL, placeholder) {
			continue
		}
		value := strings.TrimSpace(req.Extra[field])
		if value == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": fmt.Sprintf("extra.%s is required for this template", field),
			})
			return
		}
		baseURL = strings.ReplaceAll(baseURL, placeholder, value)
	}
	if !checkSelfChannelLimit(c, 1) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = tpl.Name
	}
	channel := &model.Channel{
		Type:    tpl.ChannelType,
		Name:    name,
		Key:     req.Key,
		BaseURL: &baseURL,
		Models:  strings.Join(tpl.Models, ","),
		Group:   "default",
		OwnerId: uid,
	}
	channel.SetOtherInfo(map[string]any{
		"templateId": tpl.Id,
		"poolKey":    tpl.PoolKey,
	})
	// 出口代理策略：空=跟随用户出口池；direct=强制直连；其余=固定代理（创建时校验格式）
	proxy := strings.TrimSpace(req.Proxy)
	if proxy != "" {
		if proxy != "direct" {
			if _, err := common.ParseProxyURLStrict(proxy); err != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "invalid proxy: " + err.Error(),
				})
				return
			}
		}
		setting := channel.GetSetting()
		setting.Proxy = proxy
		channel.SetSetting(setting)
	}
	addChannelExecute(c, &AddChannelRequest{Mode: "single", Channel: channel})
}

// ---------------------------------------------------------------------------
// 免费额度仪表盘
// ---------------------------------------------------------------------------

// freeUsageResetAt 计算重置时间（UTC）：recurring-daily→明日 0 点，
// recurring-monthly→下月 1 号，uncapped/未知→nil。
func freeUsageResetAt(freeType string, now time.Time) *time.Time {
	now = now.UTC()
	switch freeType {
	case "recurring-daily":
		t := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		return &t
	case "recurring-monthly":
		t := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		return &t
	default:
		return nil
	}
}

// GetSelfFreeUsage GET /api/channel/self/free_usage
// 该用户模板渠道（OtherInfo 含 templateId）按 poolKey 聚合：
// monthlyTokens、本月已用（logs 按 user_id+channel_id 聚合）、
// remaining=max(0, 月量-已用)（uncapped 月量为 0 → remaining=null）、重置时间。
func GetSelfFreeUsage(c *gin.Context) {
	uid := c.GetInt("id")
	// 1. 拉用户全部渠道，解析 OtherInfo.templateId / poolKey
	var channels []*model.Channel
	if err := model.DB.Where("owner_id = ?", uid).
		Select("id", "name", "other_info").
		Find(&channels).Error; err != nil {
		common.SysError(common.LogText("failed to load template channels for user %d: %s", uid, err.Error()))
		common.ApiErrorT(c, "Failed to fetch free usage. Please try again later")
		return
	}
	type poolAgg struct {
		templateId string
		poolKey    string
		channelIds []int
	}
	pools := map[string]*poolAgg{}
	channelPool := map[int]string{}
	for _, ch := range channels {
		info := ch.GetOtherInfo()
		templateId, _ := info["templateId"].(string)
		if templateId == "" {
			continue
		}
		tpl := constant.GetFreeSourceTemplate(templateId)
		poolKey, _ := info["poolKey"].(string)
		if poolKey == "" && tpl != nil {
			poolKey = tpl.PoolKey
		}
		if poolKey == "" {
			poolKey = templateId
		}
		agg, ok := pools[poolKey]
		if !ok {
			agg = &poolAgg{templateId: templateId, poolKey: poolKey}
			pools[poolKey] = agg
		}
		agg.channelIds = append(agg.channelIds, ch.Id)
		channelPool[ch.Id] = poolKey
	}
	items := make([]gin.H, 0, len(pools))
	if len(pools) == 0 {
		common.ApiSuccess(c, gin.H{"items": items, "total": 0})
		return
	}
	// 2. 本月已用：logs 按 channel_id 聚合（UTC 本月 1 号起）
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	channelIds := make([]int, 0, len(channelPool))
	for id := range channelPool {
		channelIds = append(channelIds, id)
	}
	usedByPool := map[string]int64{}
	type usageRow struct {
		ChannelId int   `gorm:"column:channel_id"`
		Tokens    int64 `gorm:"column:tokens"`
	}
	var rows []usageRow
	if err := model.LOG_DB.Model(&model.Log{}).
		Select("channel_id, SUM(prompt_tokens + completion_tokens) AS tokens").
		Where("user_id = ? AND type = ? AND created_at >= ? AND channel_id IN ?", uid, model.LogTypeConsume, monthStart, channelIds).
		Group("channel_id").Scan(&rows).Error; err != nil {
		common.SysError(common.LogText("failed to aggregate free usage for user %d: %s", uid, err.Error()))
		common.ApiErrorT(c, "Failed to aggregate usage. Please try again later")
		return
	}
	for _, r := range rows {
		if poolKey, ok := channelPool[r.ChannelId]; ok {
			usedByPool[poolKey] += r.Tokens
		}
	}
	// 3. 组装响应（池 → 模板元数据；模板被删时退化为池级展示）
	for poolKey, agg := range pools {
		var tpl *constant.FreeSourceTemplate
		for _, t := range constant.GetFreeSourceTemplates() {
			if t.PoolKey == poolKey {
				tpl = t
				break
			}
		}
		if tpl == nil {
			tpl = constant.GetFreeSourceTemplate(agg.templateId)
		}
		name := poolKey
		var monthlyTokens int64
		freeType := ""
		if tpl != nil {
			name = tpl.Name
			monthlyTokens = tpl.MonthlyTokens
			freeType = tpl.FreeType
		}
		used := usedByPool[poolKey]
		var remaining any
		if monthlyTokens > 0 {
			r := monthlyTokens - used
			if r < 0 {
				r = 0
			}
			remaining = r
		} else {
			remaining = nil // uncapped / 未公布预算
		}
		var resetsAt any
		if t := freeUsageResetAt(freeType, now); t != nil {
			resetsAt = t.Unix()
		}
		items = append(items, gin.H{
			"templateId":    agg.templateId,
			"name":          name,
			"poolKey":       poolKey,
			"channelIds":    agg.channelIds,
			"monthlyTokens": monthlyTokens,
			"freeType":      freeType,
			"usedThisMonth": used,
			"remaining":     remaining,
			"resetsAt":      resetsAt,
		})
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": len(items)})
}

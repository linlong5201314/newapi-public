package controller

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

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
	items := constant.GetFreeSourceTemplates()
	// 普通用户看不到反代部署地址：账号/授权型模板隐藏 baseUrl / defaultExtra / signupUrl / extraFields。
	if c.GetInt("role") < common.RoleAdminUser {
		safe := make([]*constant.FreeSourceTemplate, 0, len(items))
		for _, t := range items {
			cp := *t
			// 老模板没写 authType 时按 authMode 推导，保证前端拿到统一口径。
			cp.AuthType = t.EffectiveAuthType()
			if cp.HiddenEndpoint || cp.AuthMode == "account" || cp.AuthMode == "oauth" {
				cp.BaseUrl = ""
				cp.DefaultExtra = nil
				cp.SignupUrl = ""
				cp.ExtraFields = nil
			}
			safe = append(safe, &cp)
		}
		items = safe
	} else {
		safe := make([]*constant.FreeSourceTemplate, 0, len(items))
		for _, t := range items {
			cp := *t
			cp.AuthType = t.EffectiveAuthType()
			safe = append(safe, &cp)
		}
		items = safe
	}
	common.ApiSuccess(c, gin.H{
		"items": items,
		"total": len(items),
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
		// Visibility 是否公开到公共池：0=私有（默认，仅自己可用），1=公开（所有登录用户可路由）
		Visibility int `json:"visibility"`
		// Tier 层级：free=免费层（只凭订阅来源/入口地址），api=密钥层（需要上游 API Key）
		Tier string `json:"tier"`
		// Keys 多密钥：非空时优先于 Key，合并成一个多 Key 渠道（轮询/随机）
		Keys []string `json:"keys"`
		// MultiKeyMode 多密钥模式：random / polling，缺省 random
		MultiKeyMode string `json:"multi_key_mode"`
		// AllowIps 免费层渠道的订阅 IP 白名单（逗号/换行分隔，支持 CIDR）；为空表示不限来源
		AllowIps string `json:"allow_ips"`
		// SubscriptionUrl 订阅型来源（或创建渠道时顺带添加的机场订阅链接）：
		// 平台会解析出节点数/剩余流量，并把节点加进该用户的出口池。
		SubscriptionUrl string `json:"subscription_url"`
		// Models 用户勾选要加入「我的渠道」的模型列表（空则用模板默认全部模型）
		Models []string `json:"models"`
		// PublicModels 用户勾选要公开到「公共池」的模型子集（支持部分公开）
		PublicModels []string `json:"public_models"`
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
	// 订阅型来源（authType=subscription）不是模型渠道，而是「出口 IP 池」的入口：
	// 用户粘机场订阅链接 → 平台解析节点与剩余流量 → 节点进该用户出口池，
	// 之后他用别的免费源时就能选这些节点出口。所以这里只登记订阅并同步，
	// 不建渠道、不要 Key、也不返回任何部署地址。
	if tpl.EffectiveAuthType() == "subscription" {
		subUrl := strings.TrimSpace(req.SubscriptionUrl)
		if subUrl == "" {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "订阅链接不能为空"})
			return
		}
		if !strings.HasPrefix(subUrl, "http://") && !strings.HasPrefix(subUrl, "https://") {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "订阅链接必须以 http(s):// 开头"})
			return
		}
		sub := &model.ProxySubscription{
			UserId:  uid,
			Name:    tpl.Name,
			Url:     subUrl,
			Enabled: true,
			Public:  req.Visibility == 1,
		}
		if err := model.CreateProxySubscription(sub); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "保存订阅失败：" + err.Error()})
			return
		}
		// 同步失败不算失败：节点数与错误信息在订阅列表里可见，用户可手动重试。
		nodeCount, format, syncErr := syncProxySubscription(sub)
		triggerKernelReload()
		resp := gin.H{
			"subscription_id": sub.Id,
			"node_count":      nodeCount,
			"format":          format,
		}
		if syncErr != nil {
			common.SysError(common.LogText("sync subscription %d failed: %s", sub.Id, syncErr.Error()))
			resp["warning"] = "订阅已保存，但首次同步失败：" + syncErr.Error()
		}
		common.ApiSuccess(c, resp)
		return
	}

	// 如果用户在创建普通渠道时也顺带填了一条机场订阅链接（Item 2.5），先把订阅同步进该用户的出口池
	if subUrl := strings.TrimSpace(req.SubscriptionUrl); strings.HasPrefix(subUrl, "http://") || strings.HasPrefix(subUrl, "https://") {
		sub := &model.ProxySubscription{
			UserId:  uid,
			Name:    tpl.Name + " 订阅",
			Url:     subUrl,
			Enabled: true,
			Public:  req.Visibility == 1,
		}
		if err := model.CreateProxySubscription(sub); err == nil {
			_, _, _ = syncProxySubscription(sub)
			triggerKernelReload()
		}
	}

	// 严格区分 API 密钥免费层 vs IP 免费层（Item 2.3）：
	// 凡是需要 API Key 的免费源（KeyRequired=true 且 AuthMode=key），必须填 API Key，绝不允许通过 tier=free 空手绕过！
	isIpFreeSource := tpl.AuthMode == "ip" || !tpl.KeyRequired || tpl.EffectiveAuthType() == "none"
	if !isIpFreeSource && req.Key == "" && len(req.Keys) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "该来源属于 API 密钥免费层，必须填写有效的 API Key 才能创建渠道（仅免密钥/IP 免费层支持直接绑定 IP 使用）"})
		return
	}
	if isIpFreeSource && req.Key == "" && len(req.Keys) == 0 {
		req.Tier = "free"
	}
	// 免 key 源（kilo-code / ovhcloud / pollinations / opencode-free 等匿名层）：渠道表 key 不能为空，
	// 灌一个可识别的占位 key，上游忽略 Authorization 即可
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
			// 用户留空时回落到模板默认值（如 ds2api 平台已部署的网关地址）
			value = strings.TrimSpace(tpl.DefaultExtra[field])
		}
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

	chosenModels := make([]string, 0, len(req.Models))
	for _, m := range req.Models {
		if v := strings.TrimSpace(m); v != "" {
			chosenModels = append(chosenModels, v)
		}
	}
	if len(chosenModels) == 0 {
		chosenModels = tpl.Models
	}

	publicModels := make([]string, 0, len(req.PublicModels))
	for _, m := range req.PublicModels {
		if v := strings.TrimSpace(m); v != "" {
			publicModels = append(publicModels, v)
		}
	}
	visibility := normalizeChannelVisibility(req.Visibility)
	if len(publicModels) > 0 {
		visibility = 1
	}

	channel := &model.Channel{
		Type:       tpl.ChannelType,
		Name:       name,
		Key:        req.Key,
		BaseURL:    &baseURL,
		Models:     strings.Join(chosenModels, ","),
		Group:      "default",
		OwnerId:    uid,
		Visibility: visibility,
		Tier:       normalizeChannelTier(req.Tier),
		AllowIps:   strings.TrimSpace(req.AllowIps),
	}
	otherInfo := map[string]any{
		"templateId": tpl.Id,
		"poolKey":    tpl.PoolKey,
	}
	if len(publicModels) > 0 {
		otherInfo["publicModels"] = publicModels
	}
	channel.SetOtherInfo(otherInfo)
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
	// 多密钥：Keys 非空时合并成 one-channel-many-keys（复用 new-api 的 multi_to_single 模式），
	// 这样用户不必为同一个上游建 N 个渠道。单密钥保持 single 模式不变。
	addMode := "single"
	if len(req.Keys) > 0 {
		cleaned := make([]string, 0, len(req.Keys))
		for _, k := range req.Keys {
			if v := strings.TrimSpace(k); v != "" {
				cleaned = append(cleaned, v)
			}
		}
		if len(cleaned) > 0 {
			channel.Key = strings.Join(cleaned, "\n")
			addMode = "multi_to_single"
		}
	}
	addChannelExecute(c, &AddChannelRequest{
		Mode:         addMode,
		Channel:      channel,
		MultiKeyMode: normalizeMultiKeyMode(req.MultiKeyMode),
	})
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

// ---------------------------------------------------------------------------
// 可见性 / 层级 / 多密钥
// ---------------------------------------------------------------------------

// normalizeChannelVisibility 只允许 0/1，防止前端传任意值绕过私有默认。
func normalizeChannelVisibility(v int) int {
	if v == 1 {
		return 1
	}
	return 0
}

// normalizeChannelTier 只允许 free / api，其余一律回落 api。
func normalizeChannelTier(t string) string {
	if strings.EqualFold(strings.TrimSpace(t), "free") {
		return "free"
	}
	return "api"
}

// normalizeMultiKeyMode 多密钥模式：默认随机，兼容 polling。
func normalizeMultiKeyMode(m string) constant.MultiKeyMode {
	if strings.EqualFold(strings.TrimSpace(m), "polling") {
		return constant.MultiKeyModePolling
	}
	return constant.MultiKeyModeRandom
}

// SetSelfChannelVisibility POST /api/channel/self/visibility
// body: {"channelId":12,"visibility":1,"tier":"free","publicModels":["m1","m2"]}
// 只能改自己的渠道：owner_id 必须等于当前登录用户，平台渠道（owner_id=0）不可动。
func SetSelfChannelVisibility(c *gin.Context) {
	uid := c.GetInt("id")
	req := struct {
		ChannelId    int      `json:"channelId"`
		Visibility   int      `json:"visibility"`
		Tier         string   `json:"tier"`
		AllowIps     string   `json:"allowIps"`
		PublicModels []string `json:"publicModels"`
	}{}
	if err := c.ShouldBindJSON(&req); err != nil || req.ChannelId <= 0 {
		common.ApiErrorMsg(c, "Invalid parameters")
		return
	}
	if err := model.SetSelfChannelVisibility(uid, req.ChannelId, normalizeChannelVisibility(req.Visibility), normalizeChannelTier(req.Tier)); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.PublicModels != nil {
		if ch, ok := loadSelfOwnedChannel(c, req.ChannelId, false); ok && ch != nil {
			info := ch.GetOtherInfo()
			if info == nil {
				info = map[string]any{}
			}
			cleaned := make([]string, 0, len(req.PublicModels))
			for _, m := range req.PublicModels {
				if v := strings.TrimSpace(m); v != "" {
					cleaned = append(cleaned, v)
				}
			}
			info["publicModels"] = cleaned
			ch.SetOtherInfo(info)
			_ = model.DB.Model(&model.Channel{}).Where("id = ? AND owner_id = ?", req.ChannelId, uid).Update("other_info", ch.OtherInfo).Error
		}
	}
	if strings.TrimSpace(req.AllowIps) != "" {
		if err := model.SetSelfChannelAllowIps(uid, req.ChannelId, strings.TrimSpace(req.AllowIps)); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	model.InitChannelCache()
	common.ApiSuccess(c, gin.H{"channelId": req.ChannelId, "visibility": normalizeChannelVisibility(req.Visibility), "tier": normalizeChannelTier(req.Tier)})
}

// ProbeTemplateModels POST /api/channel/self/probe_template_models
// 创建渠道前点击「获取模型」：优先使用延迟最低的可用出口节点探测上游 /v1/models，合并模板内置模型返回。
func ProbeTemplateModels(c *gin.Context) {
	uid := c.GetInt("id")
	var req struct {
		TemplateId string            `json:"templateId"`
		Key        string            `json:"key"`
		Proxy      string            `json:"proxy"`
		Extra      map[string]string `json:"extra"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	tpl := constant.GetFreeSourceTemplate(req.TemplateId)
	if tpl == nil {
		common.ApiErrorMsg(c, "unknown templateId")
		return
	}

	// 挑选最佳出口代理：若用户未指定固定代理，自动从该用户可见的出口池挑最优（已按延迟升序排列）
	proxyURL := strings.TrimSpace(req.Proxy)
	if proxyURL == "" {
		proxyURL = model.PickUserProxyUrl(uid)
	} else if proxyURL == "direct" {
		proxyURL = ""
	}

	modelSet := map[string]bool{}
	models := make([]string, 0, len(tpl.Models)+8)
	addModel := func(m string) {
		v := strings.TrimSpace(m)
		if v != "" && !modelSet[v] {
			modelSet[v] = true
			models = append(models, v)
		}
	}

	baseURL := strings.TrimRight(strings.TrimSpace(tpl.BaseUrl), "/")
	for _, field := range tpl.ExtraFields {
		val := strings.TrimSpace(req.Extra[field])
		if val == "" {
			val = strings.TrimSpace(tpl.DefaultExtra[field])
		}
		if val != "" {
			baseURL = strings.ReplaceAll(baseURL, "{"+field+"}", val)
		}
	}

	var latencyMs int64
	probedLive := false
	if baseURL != "" && !strings.Contains(baseURL, "{") {
		modelsEndpoint := baseURL + "/v1/models"
		if strings.HasSuffix(baseURL, "/v1") || strings.HasSuffix(baseURL, "/openai") {
			modelsEndpoint = baseURL + "/models"
		}
		if client, err := service.GetHttpClientWithProxySettings(proxyURL, dto.ChannelSettings{}); err == nil {
			probeClient := *client
			probeClient.Timeout = 6 * time.Second
			httpReq, rerr := http.NewRequest(http.MethodGet, modelsEndpoint, nil)
			if rerr == nil {
				key := strings.TrimSpace(req.Key)
				if key != "" {
					httpReq.Header.Set("Authorization", "Bearer "+key)
				}
				start := time.Now()
				if resp, derr := probeClient.Do(httpReq); derr == nil && resp != nil {
					latencyMs = time.Since(start).Milliseconds()
					body, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
					_ = resp.Body.Close()
					var payload struct {
						Data []struct {
							Id string `json:"id"`
						} `json:"data"`
					}
					if common.Unmarshal(body, &payload) == nil && len(payload.Data) > 0 {
						probedLive = true
						for _, item := range payload.Data {
							addModel(item.Id)
						}
					}
				}
			}
		}
	}

	for _, m := range tpl.Models {
		addModel(m)
	}

	common.ApiSuccess(c, gin.H{
		"models":      models,
		"probed_live": probedLive,
		"latency_ms":  latencyMs,
		"proxy_used":  maskProxyUrl(proxyURL),
	})
}

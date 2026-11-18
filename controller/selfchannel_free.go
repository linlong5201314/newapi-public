package controller

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
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
		Proxy       string                  `json:"proxy"`
		ProxyPolicy *dto.ChannelProxyPolicy `json:"proxy_policy"`
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
		// AllModels is the full discovered catalog, independent of the selected subset.
		AllModels []string `json:"all_models"`
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
	if err := model.ValidateSelfChannelProxyPolicy(uid, req.ProxyPolicy, req.Proxy); err != nil {
		common.ApiError(c, err)
		return
	}
	if tpl.Id == "freepi" {
		common.ApiErrorMsg(c, "Use GitHub device authorization to connect FreePi")
		return
	}
	if liveFreeCatalogProvider(tpl.BaseUrl, tpl.Id) == "opencode" {
		common.ApiErrorMsg(c, "OpenCode free models are not available through this gateway. Use the official OpenCode client")
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

	allCandidates := mergeModelCatalogs(tpl.Models, req.AllModels, req.Models)
	if liveFreeCatalogProvider(baseURL, tpl.Id) != "" {
		proxyURL, proxyErr := model.ResolveChannelProxyPolicy(uid, dto.ChannelSettings{Proxy: req.Proxy, ProxyPolicy: req.ProxyPolicy})
		if proxyErr != nil {
			common.ApiError(c, proxyErr)
			return
		}
		live, verified, _ := FetchLiveFreeModelsForUpstream(baseURL, tpl.Id, req.Key, proxyURL)
		if !verified || len(live) == 0 {
			common.ApiErrorMsg(c, "Unable to verify the upstream free model catalog. Please refresh and try again")
			return
		}
		allCandidates = live
		allowed := make(map[string]bool, len(live))
		for _, id := range live {
			allowed[id] = true
		}
		for _, id := range req.Models {
			if !allowed[strings.TrimSpace(id)] {
				common.ApiErrorMsg(c, "Selected model is not in the current free model catalog")
				return
			}
		}
	}
	chosenModels := mergeModelCatalogs(req.Models)
	if len(chosenModels) == 0 {
		chosenModels = allCandidates
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
	selected := make(map[string]bool, len(chosenModels))
	for _, id := range chosenModels {
		selected[id] = true
	}
	for _, id := range publicModels {
		if !selected[id] {
			common.ApiErrorMsg(c, "Public models must be selected channel models")
			return
		}
	}
	otherInfo := map[string]any{
		"templateId": tpl.Id,
		"poolKey":    tpl.PoolKey,
		"allModels":  allCandidates,
	}
	if len(publicModels) > 0 {
		otherInfo["publicModels"] = publicModels
	}
	channel.SetOtherInfo(otherInfo)
	setting := channel.GetSetting()
	setting.Proxy = strings.TrimSpace(req.Proxy)
	setting.ProxyPolicy = req.ProxyPolicy
	channel.SetSetting(setting)
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
	var req struct {
		ChannelId    int      `json:"channelId"`
		Visibility   int      `json:"visibility"`
		Tier         string   `json:"tier"`
		AllowIps     string   `json:"allowIps"`
		PublicModels []string `json:"publicModels"`
		AllModels    []string `json:"allModels"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ChannelId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	channel, ok := loadSelfOwnedChannel(c, req.ChannelId, false)
	if !ok {
		return
	}
	info := channel.GetOtherInfo()
	if info == nil {
		info = map[string]any{}
	}
	if req.PublicModels != nil {
		selected := make(map[string]bool)
		for _, id := range strings.Split(channel.Models, ",") {
			selected[strings.TrimSpace(id)] = true
		}
		public := mergeModelCatalogs(req.PublicModels)
		for _, id := range public {
			if !selected[id] {
				common.ApiErrorMsg(c, "Public models must be selected channel models")
				return
			}
		}
		info["publicModels"] = public
	}
	if req.AllModels != nil {
		templateID, _ := info["templateId"].(string)
		all := DefaultModelsForChannel(channel)
		// Free-source catalogs are verified on the server. A display-only update
		// cannot reintroduce paid models or shrink the catalog to the selection.
		if liveFreeCatalogProvider(channel.GetBaseURL(), templateID) == "" {
			all = mergeModelCatalogs(all, req.AllModels)
		}
		info["allModels"] = all
	}
	channel.SetOtherInfo(info)
	updates := map[string]any{"visibility": normalizeChannelVisibility(req.Visibility), "tier": normalizeChannelTier(req.Tier), "other_info": channel.OtherInfo}
	if strings.TrimSpace(req.AllowIps) != "" {
		updates["allow_ips"] = strings.TrimSpace(req.AllowIps)
	}
	if err := model.DB.Model(&model.Channel{}).Where("id = ? AND owner_id = ?", req.ChannelId, uid).Updates(updates).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	common.ApiSuccess(c, gin.H{"channelId": req.ChannelId, "visibility": normalizeChannelVisibility(req.Visibility), "tier": normalizeChannelTier(req.Tier)})
}

// liveFreeCatalogProvider uses exact hostnames; lookalike URLs are not trusted catalogs.
func liveFreeCatalogProvider(baseURL, templateID string) string {
	u, _ := url.Parse(baseURL)
	host := ""
	if u != nil {
		host = strings.ToLower(u.Hostname())
	}
	if templateID == "kilo-code" || host == "ai-gateway.kilo.ai" || host == "api.kilo.ai" {
		return "kilo"
	}
	if templateID == "openrouter-free" || host == "openrouter.ai" {
		return "openrouter"
	}
	if templateID == "opencode-free" || templateID == "opencode-zen" || host == "opencode.ai" {
		return "opencode"
	}
	return ""
}

// FetchLiveFreeModelsForUpstream returns only advertised, compatible free models.
// Never append retired model IDs or auto routers that can select paid models.
func FetchLiveFreeModelsForUpstream(baseURL, templateID, key, proxyURL string) ([]string, bool, int64) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	provider := liveFreeCatalogProvider(base, templateID)
	if base == "" || strings.Contains(base, "{") {
		return nil, false, 0
	}
	endpoint := base + "/v1/models"
	if strings.HasSuffix(base, "/v1") || strings.HasSuffix(base, "/openai") || strings.HasSuffix(base, "/compatibility") {
		endpoint = base + "/models"
	}
	if parsed, err := url.Parse(base); err == nil && strings.EqualFold(parsed.Hostname(), "opencode.ai") {
		endpoint = "https://opencode.ai/zen/v1/models"
	}
	client, err := service.GetHttpClientWithProxySettings(proxyURL, dto.ChannelSettings{})
	if err != nil {
		return nil, false, 0
	}
	probeClient := *client
	probeClient.Timeout = 7 * time.Second
	// A redirected catalog must never receive the user's API credential.
	probeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, 0
	}
	if provider == "" && key != "" && !strings.HasPrefix(key, "sk-free-") {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))
	}
	start := time.Now()
	resp, err := probeClient.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return nil, false, latency
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, latency
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(body) > 4<<20 {
		return nil, false, latency
	}
	var payload struct {
		Data []struct {
			ID           string         `json:"id"`
			Pricing      map[string]any `json:"pricing"`
			IsFree       *bool          `json:"isFree"`
			Architecture struct {
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			Cost *struct {
				Text struct {
					Input struct {
						Price *float64 `json:"price"`
					} `json:"input"`
					Output struct {
						Price *float64 `json:"price"`
					} `json:"output"`
				} `json:"text"`
			} `json:"cost"`
			API map[string]any `json:"api"`
		} `json:"data"`
	}
	if common.Unmarshal(body, &payload) != nil || payload.Data == nil {
		return nil, false, latency
	}
	out := make([]string, 0, len(payload.Data))
	seen := map[string]bool{}
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || strings.ContainsAny(id, ",\r\n") || seen[id] {
			continue
		}
		if provider == "kilo" {
			if item.IsFree == nil || !*item.IsFree || !slices.Contains(item.Architecture.OutputModalities, "text") || !zeroCatalogPrice(item.Pricing["prompt"]) || !zeroCatalogPrice(item.Pricing["completion"]) {
				continue
			}
			free := true
			for field, value := range item.Pricing {
				if field != "discount" && !zeroCatalogPrice(value) {
					free = false
					break
				}
			}
			if !free {
				continue
			}
		}
		if provider == "openrouter" {
			if id == "openrouter/auto" || !zeroCatalogPrice(item.Pricing["prompt"]) || !zeroCatalogPrice(item.Pricing["completion"]) {
				continue
			}
			free := true
			for _, value := range item.Pricing {
				if !zeroCatalogPrice(value) {
					free = false
					break
				}
			}
			if !free {
				continue
			}
		}
		if provider == "opencode" {
			// Current OpenCode catalog advertises per-model protocol endpoints and cost.
			// Legacy catalogs omit cost; only their explicit free IDs are accepted.
			if item.Cost != nil {
				input, output := item.Cost.Text.Input.Price, item.Cost.Text.Output.Price
				if input == nil || output == nil || *input != 0 || *output != 0 {
					continue
				}
			} else if !strings.HasSuffix(id, "-free") && id != "big-pickle" {
				continue
			}
			if len(item.API) > 0 {
				if _, ok := item.API["chat-completions"]; !ok {
					continue
				}
			}
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, true, latency
}

func zeroCatalogPrice(value any) bool {
	switch v := value.(type) {
	case string:
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n != 0 {
			return false
		}
		// Do not treat underflowed positive prices as zero.
		mantissa, _, _ := strings.Cut(strings.ToLower(v), "e")
		return !strings.ContainsAny(mantissa, "123456789")
	case float64:
		return v == 0
	default:
		return false
	}
}

// ProbeTemplateModels POST /api/channel/self/probe_template_models
// 创建渠道前点击「获取模型」：优先使用延迟最低的可用出口节点探测上游 /v1/models，合并模板内置模型返回。
func ProbeTemplateModels(c *gin.Context) {
	uid := c.GetInt("id")
	var req struct {
		TemplateId  string                  `json:"templateId"`
		Key         string                  `json:"key"`
		Proxy       string                  `json:"proxy"`
		ProxyPolicy *dto.ChannelProxyPolicy `json:"proxy_policy"`
		Extra       map[string]string       `json:"extra"`
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
	proxyURL, proxyErr := model.ResolveChannelProxyPolicy(uid, dto.ChannelSettings{Proxy: req.Proxy, ProxyPolicy: req.ProxyPolicy})
	if proxyErr != nil {
		common.ApiError(c, proxyErr)
		return
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

	liveModels, probedLive, latencyMs := FetchLiveFreeModelsForUpstream(baseURL, tpl.Id, req.Key, proxyURL)

	modelSet := map[string]bool{}
	models := make([]string, 0, len(liveModels)+len(tpl.Models)+8)
	addModel := func(m string) {
		v := strings.TrimSpace(m)
		if v != "" && !modelSet[v] {
			modelSet[v] = true
			models = append(models, v)
		}
	}
	for _, m := range liveModels {
		addModel(m)
	}
	if liveFreeCatalogProvider(baseURL, tpl.Id) == "" {
		for _, m := range tpl.Models {
			addModel(m)
		}
	} else if !probedLive {
		common.ApiErrorMsg(c, "Unable to verify the upstream free model catalog. Please refresh and try again")
		return
	}

	common.ApiSuccess(c, gin.H{
		"models":        models,
		"callable":      liveFreeCatalogProvider(baseURL, tpl.Id) != "opencode",
		"call_contract": freeModelCallContract(baseURL, tpl.Id),
		"probed_live":   probedLive,
		"latency_ms":    latencyMs,
		"proxy_used":    maskProxyUrl(proxyURL),
	})
}

// mergeModelCatalogs preserves catalog order without conflating it with model selection.
func mergeModelCatalogs(lists ...[]string) []string {
	out := make([]string, 0)
	seen := make(map[string]bool)
	for _, list := range lists {
		for _, id := range list {
			id = strings.TrimSpace(id)
			if id != "" && !strings.ContainsAny(id, ",\r\n") && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

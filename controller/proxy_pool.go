// proxy_pool.go 用户出口 IP 池 API：节点管理、订阅同步、连通性探测与配置导出。
package controller

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/proxy_pool"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	// proxyEndpointLimit 单用户出口节点上限。
	proxyEndpointLimit = 10000
	// proxySubscriptionLimit 单用户订阅上限。
	proxySubscriptionLimit = 20
	// proxyProbeTimeout 单节点连通性探测超时。
	proxyProbeTimeout = 12 * time.Second
	// proxySubscriptionTimeout 拉取订阅超时。
	proxySubscriptionTimeout = 25 * time.Second
	// proxySubscriptionUserAgent 部分机场按 UA 返回不同格式，固定成 clash 客户端。
	proxySubscriptionUserAgent = "ClashforWindows/0.20.39"
	// proxySubscriptionBodyLimit 订阅响应体上限，防止超大订阅打爆内存。
	proxySubscriptionBodyLimit = 16 << 20
	// proxyProbeUrl 出口 IP 回显服务。
	proxyProbeUrl = "https://api.ipify.org?format=json"
	// proxyMixedPortBase 导出 mihomo 配置时使用的 mixed-port。
	proxyMixedPortBase = 23456
)

// GetProxyEndpoints GET /api/proxy-pool/endpoints — 当前用户可见的出口节点列表（自己的 + 公共池公开的）。
func GetProxyEndpoints(c *gin.Context) {
	uid := c.GetInt("id")
	list, err := model.ListProxyEndpoints(uid)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, endpoint := range list {
		out = append(out, proxyEndpointViewForUser(endpoint, uid))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// CreateProxyEndpoint POST /api/proxy-pool/endpoints — 手动添加一个出口节点。
func CreateProxyEndpoint(c *gin.Context) {
	var req struct {
		Name   string `json:"name"`
		Url    string `json:"url"`
		Public bool   `json:"public"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	rawUrl := strings.TrimSpace(req.Url)
	if rawUrl == "" {
		common.ApiErrorMsg(c, "url is required")
		return
	}
	if name == "" {
		name = rawUrl
	}

	parsedURL, err := common.ParseProxyURLStrict(rawUrl)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if parsedURL == nil {
		common.ApiErrorMsg(c, "url is required")
		return
	}

	userId := c.GetInt("id")
	total, err := model.CountProxyEndpoints(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if total >= proxyEndpointLimit {
		common.ApiErrorMsg(c, fmt.Sprintf("proxy endpoint limit reached (%d)", proxyEndpointLimit))
		return
	}

	endpoint := &model.ProxyEndpoint{
		UserId:  userId,
		Name:    name,
		Kind:    parsedURL.Scheme,
		Url:     parsedURL.String(),
		Source:  "manual",
		Enabled: true,
		Public:  req.Public,
		Weight:  1,
	}
	if err := model.CreateProxyEndpoint(endpoint); err != nil {
		common.ApiError(c, err)
		return
	}
	triggerKernelReload()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": proxyEndpointViewForUser(*endpoint, userId)})
}

// UpdateProxyEndpoint PATCH /api/proxy-pool/endpoints/:id — 改 name/enabled/weight/public。
func UpdateProxyEndpoint(c *gin.Context) {
	id, ok := proxyPoolId(c)
	if !ok {
		return
	}
	var req struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
		Public  *bool   `json:"public"`
		Weight  *int    `json:"weight"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}

	fields := make(map[string]any, 4)
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			common.ApiErrorMsg(c, "name must not be empty")
			return
		}
		fields["name"] = name
	}
	if req.Enabled != nil {
		fields["enabled"] = *req.Enabled
	}
	if req.Public != nil {
		fields["public"] = *req.Public
	}
	if req.Weight != nil {
		if *req.Weight < 0 {
			common.ApiErrorMsg(c, "weight must not be negative")
			return
		}
		fields["weight"] = *req.Weight
	}

	userId := c.GetInt("id")
	if err := model.UpdateProxyEndpoint(userId, id, fields); err != nil {
		if err == gorm.ErrRecordNotFound {
			common.ApiErrorMsg(c, "proxy endpoint not found")
			return
		}
		common.ApiError(c, err)
		return
	}
	triggerKernelReload()
	endpoint, err := model.GetProxyEndpoint(userId, id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": proxyEndpointViewForUser(*endpoint, userId)})
}

// DeleteProxyEndpoint DELETE /api/proxy-pool/endpoints/:id — 删除一个出口节点。
func DeleteProxyEndpoint(c *gin.Context) {
	id, ok := proxyPoolId(c)
	if !ok {
		return
	}
	if err := model.DeleteProxyEndpoint(c.GetInt("id"), id); err != nil {
		if err == gorm.ErrRecordNotFound {
			common.ApiErrorMsg(c, "proxy endpoint not found")
			return
		}
		common.ApiError(c, err)
		return
	}
	triggerKernelReload()
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// TestProxyEndpoint POST /api/proxy-pool/endpoints/:id/test — 探测出口节点延迟与可用性（支持直连 URL 与内置 mihomo 客户端节点）。
func TestProxyEndpoint(c *gin.Context) {
	id, ok := proxyPoolId(c)
	if !ok {
		return
	}
	userId := c.GetInt("id")
	endpoint, err := model.GetProxyEndpointVisible(userId, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.ApiErrorMsg(c, "proxy endpoint not found")
			return
		}
		common.ApiError(c, err)
		return
	}
	// 客户端协议节点（vless/vmess/anytls/ss 等）：直接通过内置 mihomo controller 测速
	if strings.TrimSpace(endpoint.Url) == "" {
		if strings.TrimSpace(endpoint.RawUri) == "" {
			common.ApiErrorMsg(c, "this node has neither dialable url nor raw uri")
			return
		}
		delayMs, kerr := proxy_pool.ManagerInstance().ProbeEndpointDelay(endpoint.Id)
		if kerr != nil {
			message := kerr.Error()
			model.InvalidateProxyPoolCache(userId)
			_ = model.MarkProxyEndpointLatency(endpoint.Id, false, 0, message)
			c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
				"ok":         false,
				"error":      message,
				"latency_ms": 0,
			}})
			return
		}
		model.InvalidateProxyPoolCache(userId)
		_ = model.MarkProxyEndpointLatency(endpoint.Id, true, delayMs, "")
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"ok":         true,
			"ip":         endpoint.Region + " (" + endpoint.Kind + ")",
			"latency_ms": delayMs,
		}})
		return
	}

	startedAt := time.Now()
	ip, err := probeProxyEndpoint(endpoint.Url)
	latencyMs := int(time.Since(startedAt).Milliseconds())
	if err != nil {
		message := err.Error()
		model.InvalidateProxyPoolCache(userId)
		if markErr := model.MarkProxyEndpointLatency(endpoint.Id, false, 0, message); markErr != nil {
			common.ApiError(c, markErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"ok":         false,
			"error":      message,
			"latency_ms": latencyMs,
		}})
		return
	}
	model.InvalidateProxyPoolCache(userId)
	if err := model.MarkProxyEndpointLatency(endpoint.Id, true, latencyMs, ""); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"ok":         true,
		"ip":         ip,
		"latency_ms": latencyMs,
	}})
}

// probeProxyEndpoint 通过指定代理请求出口 IP 回显服务。
func probeProxyEndpoint(proxyURL string) (string, error) {
	client, err := service.GetHttpClientWithProxySettings(proxyURL, dto.ChannelSettings{})
	if err != nil {
		return "", err
	}
	// 缓存里的 client 是共享的，复制一份只改超时，避免影响其它调用方。
	probeClient := *client
	probeClient.Timeout = proxyProbeTimeout

	resp, err := probeClient.Get(proxyProbeUrl)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("probe endpoint returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	var payload struct {
		Ip string `json:"ip"`
	}
	if err := common.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode probe response: %w", err)
	}
	if strings.TrimSpace(payload.Ip) == "" {
		return "", fmt.Errorf("probe endpoint returned no ip")
	}
	return payload.Ip, nil
}

// GetProxySubscriptions GET /api/proxy-pool/subscriptions — 当前用户的订阅列表。
func GetProxySubscriptions(c *gin.Context) {
	list, err := model.ListProxySubscriptions(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, sub := range list {
		out = append(out, proxySubscriptionView(sub))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// CreateProxySubscription POST /api/proxy-pool/subscriptions — 添加订阅并立即拉取解析。
func CreateProxySubscription(c *gin.Context) {
	var req struct {
		Name   string `json:"name"`
		Url    string `json:"url"`
		Format string `json:"format"`
		Public bool   `json:"public"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	rawUrl := strings.TrimSpace(req.Url)
	if rawUrl == "" {
		common.ApiErrorMsg(c, "url is required")
		return
	}
	if !strings.HasPrefix(rawUrl, "http://") && !strings.HasPrefix(rawUrl, "https://") {
		common.ApiErrorMsg(c, "url must start with http:// or https://")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = subscriptionDisplayName(rawUrl)
	}

	userId := c.GetInt("id")
	subTotal, err := model.CountProxySubscriptions(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if subTotal >= proxySubscriptionLimit {
		common.ApiErrorMsg(c, fmt.Sprintf("proxy subscription limit reached (%d)", proxySubscriptionLimit))
		return
	}

	sub := &model.ProxySubscription{
		UserId:  userId,
		Name:    name,
		Url:     rawUrl,
		Format:  strings.TrimSpace(req.Format),
		Enabled: true,
		Public:  req.Public,
	}
	if err := model.CreateProxySubscription(sub); err != nil {
		common.ApiError(c, err)
		return
	}

	nodeCount, detectedFormat, syncErr := syncProxySubscription(sub)
	if syncErr != nil {
		triggerKernelReload()
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"id":         sub.Id,
			"node_count": nodeCount,
			"format":     detectedFormat,
			"error":      syncErr.Error(),
		}})
		return
	}
	triggerKernelReload()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"id":         sub.Id,
		"node_count": nodeCount,
		"format":     detectedFormat,
	}})
}

// SyncProxySubscription POST /api/proxy-pool/subscriptions/:id/sync — 重新拉取并重建节点。
func SyncProxySubscription(c *gin.Context) {
	id, ok := proxyPoolId(c)
	if !ok {
		return
	}
	userId := c.GetInt("id")
	sub, err := model.GetProxySubscription(userId, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.ApiErrorMsg(c, "proxy subscription not found")
			return
		}
		common.ApiError(c, err)
		return
	}
	nodeCount, detectedFormat, syncErr := syncProxySubscription(sub)
	if syncErr != nil {
		common.ApiError(c, syncErr)
		return
	}
	triggerKernelReload()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"id":         sub.Id,
		"node_count": nodeCount,
		"format":     detectedFormat,
	}})
}

// syncProxySubscription 拉取订阅 → 解析 → 按订阅重建节点，并回写订阅状态。
// 返回 (节点数, 实际格式, 错误)。重建时同订阅内按节点名去重。
func syncProxySubscription(sub *model.ProxySubscription) (int, string, error) {
	now := time.Now()
	body, traffic, fetchErr := fetchProxySubscription(sub.Url)
	if fetchErr != nil {
		message := truncateProxyError(fetchErr.Error())
		if err := model.UpdateProxySubscription(sub.UserId, sub.Id, map[string]any{
			"last_sync_at": now,
			"last_error":   message,
		}); err != nil {
			return 0, "", err
		}
		return 0, sub.Format, fetchErr
	}
	// 订阅头里的流量/到期信息随同步一起落库，前端据此显示「大概多少额度」。
	trafficUpdate := map[string]any{
		"traffic_used":  traffic.Used,
		"traffic_total": traffic.Total,
	}
	if traffic.ExpireAt != nil {
		trafficUpdate["expire_at"] = *traffic.ExpireAt
	}
	if err := model.UpdateProxySubscription(sub.UserId, sub.Id, trafficUpdate); err != nil {
		common.SysLog("failed to update proxy subscription traffic: " + err.Error())
	}

	nodes, detectedFormat, parseErr := proxy_pool.ParseSubscription(body, sub.Format)
	if parseErr != nil {
		message := truncateProxyError(parseErr.Error())
		if err := model.UpdateProxySubscription(sub.UserId, sub.Id, map[string]any{
			"last_sync_at": now,
			"last_error":   message,
		}); err != nil {
			return 0, detectedFormat, err
		}
		return 0, detectedFormat, parseErr
	}

	if err := model.DeleteProxyEndpointsBySubscription(sub.UserId, sub.Id); err != nil {
		return 0, detectedFormat, err
	}

	existing, err := model.CountProxyEndpoints(sub.UserId)
	if err != nil {
		return 0, detectedFormat, err
	}
	budget := proxyEndpointLimit - existing
	if budget < 0 {
		budget = 0
	}

	endpoints := make([]model.ProxyEndpoint, 0, min(len(nodes), budget))
	seenNames := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if len(endpoints) >= budget {
			break
		}
		name := strings.TrimSpace(node.Name)
		if name == "" {
			name = node.Kind
		}
		if _, duplicated := seenNames[name]; duplicated {
			continue
		}
		seenNames[name] = struct{}{}
		endpoints = append(endpoints, model.ProxyEndpoint{
			UserId:         sub.UserId,
			Name:           name,
			Kind:           node.Kind,
			Url:            node.Url,
			RawUri:         node.RawUri,
			Region:         node.Region,
			SubscriptionId: sub.Id,
			Source:         "subscription",
			Enabled:        true,
			Public:         sub.Public,
			Weight:         1,
		})
	}
	if err := model.CreateProxyEndpoints(endpoints); err != nil {
		return 0, detectedFormat, err
	}

	if err := model.UpdateProxySubscription(sub.UserId, sub.Id, map[string]any{
		"format":       detectedFormat,
		"node_count":   len(endpoints),
		"last_sync_at": now,
		"last_error":   "",
	}); err != nil {
		return len(endpoints), detectedFormat, err
	}
	sub.Format = detectedFormat
	sub.NodeCount = len(endpoints)
	sub.LastSyncAt = &now
	sub.LastError = ""
	return len(endpoints), detectedFormat, nil
}

// fetchProxySubscription 直连拉取订阅内容（不走代理，避免代理依赖循环）。
// proxyTrafficInfo 机场订阅在响应头 subscription-userinfo 里给出的流量与到期信息。
// 典型格式：upload=123; download=456; total=789; expire=1700000000
type proxyTrafficInfo struct {
	Used     int64
	Total    int64
	ExpireAt *time.Time
}

// parseSubscriptionUserInfo 解析 subscription-userinfo 头，解析不到的字段保持零值。
func parseSubscriptionUserInfo(header string) proxyTrafficInfo {
	var info proxyTrafficInfo
	if strings.TrimSpace(header) == "" {
		return info
	}
	var upload, download int64
	for _, part := range strings.Split(header, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		value, err := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "upload":
			upload = value
		case "download":
			download = value
		case "total":
			info.Total = value
		case "expire":
			if value > 0 {
				t := time.Unix(value, 0)
				info.ExpireAt = &t
			}
		}
	}
	info.Used = upload + download
	return info
}

func fetchProxySubscription(rawUrl string) ([]byte, proxyTrafficInfo, error) {
	client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
	if err != nil {
		return nil, proxyTrafficInfo{}, err
	}
	// 共享 client 只复制超时；请求级设置 UA 等头部。
	fetchClient := *client
	fetchClient.Timeout = proxySubscriptionTimeout

	req, err := http.NewRequest(http.MethodGet, rawUrl, nil)
	if err != nil {
		return nil, proxyTrafficInfo{}, err
	}
	req.Header.Set("User-Agent", proxySubscriptionUserAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, proxyTrafficInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, proxyTrafficInfo{}, fmt.Errorf("subscription endpoint returned status %d", resp.StatusCode)
	}
	traffic := parseSubscriptionUserInfo(resp.Header.Get("subscription-userinfo"))
	body, err := io.ReadAll(io.LimitReader(resp.Body, proxySubscriptionBodyLimit))
	if err != nil {
		return nil, traffic, err
	}
	if len(body) == 0 {
		return nil, traffic, fmt.Errorf("subscription body is empty")
	}
	return body, traffic, nil
}

// DeleteProxySubscription DELETE /api/proxy-pool/subscriptions/:id — 删订阅及其节点。
func DeleteProxySubscription(c *gin.Context) {
	id, ok := proxyPoolId(c)
	if !ok {
		return
	}
	userId := c.GetInt("id")
	if err := model.DeleteProxyEndpointsBySubscription(userId, id); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DeleteProxySubscription(userId, id); err != nil {
		if err == gorm.ErrRecordNotFound {
			common.ApiErrorMsg(c, "proxy subscription not found")
			return
		}
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetProxyPoolSummary GET /api/proxy-pool/summary — 统计卡数据。
func GetProxyPoolSummary(c *gin.Context) {
	userId := c.GetInt("id")
	stats, err := model.GetProxyPoolStatsForUser(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	subCount, err := model.CountProxySubscriptions(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	usableUrls, err := model.ListUsableProxyUrls(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	usableCount := len(usableUrls)
	if stats.Enabled > usableCount {
		usableCount = stats.Enabled
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"endpoint_total":     stats.Total,
		"endpoint_enabled":   stats.Enabled,
		"usable_url_count":   usableCount,
		"subscription_total": subCount,
		"client_node_count":  stats.ClientNodes,
		"public_node_count":  stats.PublicNodes,
		"haizhu_node_count":  stats.HaizhuNodes,
	}})
}

// ImportHaizhuPoolNodes POST /api/proxy-pool/import-haizhu
// body: {"mode":"full|top"} — 一键把本机 HaizhuClash 0.14.3 节点导入公共池（public=true）。
func ImportHaizhuPoolNodes(c *gin.Context) {
	var req struct {
		Mode string `json:"mode"`
	}
	_ = c.ShouldBindJSON(&req)
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "full"
	}
	count, err := proxy_pool.ImportHaizhuNodes(c.GetInt("id"), mode)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	triggerKernelReload()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"imported": count,
		"mode":     mode,
		"public":   true,
	}})
}

// ExportProxyPoolConfig GET /api/proxy-pool/export — 导出 mihomo 配置文本。
func ExportProxyPoolConfig(c *gin.Context) {
	endpoints, err := model.ListProxyEndpoints(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	config, err := proxy_pool.BuildMihomoConfig(endpoints, proxyMixedPortBase)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"config": config}})
}

// proxyEndpointViewForUser 出口节点的对外视图：代理 URL 脱敏（userinfo → ***），包含 public 与 is_owner。
func proxyEndpointViewForUser(endpoint model.ProxyEndpoint, currentUserId int) gin.H {
	view := gin.H{
		"id":              endpoint.Id,
		"user_id":         endpoint.UserId,
		"is_owner":        endpoint.UserId == currentUserId,
		"name":            endpoint.Name,
		"kind":            endpoint.Kind,
		"region":          endpoint.Region,
		"subscription_id": endpoint.SubscriptionId,
		"source":          endpoint.Source,
		"enabled":         endpoint.Enabled,
		"public":          endpoint.Public,
		"weight":          endpoint.Weight,
		"latency_ms":      endpoint.LatencyMs,
		"fail_count":      endpoint.FailCount,
		"last_error":      endpoint.LastError,
		"dialable":        strings.TrimSpace(endpoint.Url) != "" || strings.TrimSpace(endpoint.RawUri) != "",
		"created_at":      endpoint.CreatedAt.Format(time.RFC3339),
		"updated_at":      endpoint.UpdatedAt.Format(time.RFC3339),
	}
	if endpoint.Url != "" {
		view["url"] = maskProxyUrl(endpoint.Url)
	}
	if endpoint.LastOkAt != nil {
		view["last_ok_at"] = endpoint.LastOkAt.Format(time.RFC3339)
	}
	if strings.TrimSpace(endpoint.RawUri) != "" {
		view["raw_uri_prefix"] = rawUriPrefix(endpoint.RawUri)
	}
	return view
}

// proxySubscriptionView 订阅的对外视图：订阅链接只保留 scheme + host。
func proxySubscriptionView(sub model.ProxySubscription) gin.H {
	view := gin.H{
		"id":            sub.Id,
		"name":          sub.Name,
		"url":           maskSubscriptionUrl(sub.Url),
		"format":        sub.Format,
		"node_count":    sub.NodeCount,
		"traffic_used":  sub.TrafficUsed,
		"traffic_total": sub.TrafficTotal,
		"last_error":    sub.LastError,
		"enabled":       sub.Enabled,
		"public":        sub.Public,
		"created_at":    sub.CreatedAt.Format(time.RFC3339),
		"updated_at":    sub.UpdatedAt.Format(time.RFC3339),
	}
	if sub.LastSyncAt != nil {
		view["last_sync_at"] = sub.LastSyncAt.Format(time.RFC3339)
	}
	if sub.ExpireAt != nil {
		view["expire_at"] = sub.ExpireAt.Format(time.RFC3339)
	}
	return view
}

// maskProxyUrl 只回 scheme://host:port，userinfo 一律替换成 ***。
func maskProxyUrl(rawUrl string) string {
	parsedURL, err := url.Parse(rawUrl)
	if err != nil || parsedURL.Host == "" {
		return "***"
	}
	return parsedURL.Scheme + "://***@" + parsedURL.Host
}

// maskSubscriptionUrl 只保留 scheme + host，path 全替换成 ***。
func maskSubscriptionUrl(rawUrl string) string {
	parsedURL, err := url.Parse(rawUrl)
	if err != nil || parsedURL.Host == "" {
		return "***"
	}
	return parsedURL.Scheme + "://" + parsedURL.Host + "/***"
}

// rawUriPrefix 客户端型节点只回 scheme 与少量前缀，供前端展示。
func rawUriPrefix(rawUri string) string {
	scheme, _, found := strings.Cut(rawUri, "://")
	if !found {
		scheme, _, _ = strings.Cut(rawUri, ":")
	}
	if scheme == "" {
		return "***"
	}
	return scheme + "://***"
}

// subscriptionDisplayName 未给名字时用订阅域名当默认名。
func subscriptionDisplayName(rawUrl string) string {
	parsedURL, err := url.Parse(rawUrl)
	if err != nil || parsedURL.Host == "" {
		return "subscription"
	}
	return parsedURL.Host
}

// truncateProxyError 把错误文本截断到 last_error 字段长度。
func truncateProxyError(message string) string {
	const maxLength = 500
	if len([]rune(message)) > maxLength {
		return string([]rune(message)[:maxLength])
	}
	return message
}

// proxyPoolId 解析 :id 路径参数。
func proxyPoolId(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid id")
		return 0, false
	}
	return id, true
}

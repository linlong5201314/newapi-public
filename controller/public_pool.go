package controller

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// requirePublicPoolRoot is also checked in handlers so future route registration
// cannot accidentally turn this platform-wide surface into an admin/user API.
func requirePublicPoolRoot(c *gin.Context) bool {
	if c.GetInt("role") != common.RoleRootUser || c.GetInt("id") <= 0 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "Unauthorized, insufficient privileges"})
		return false
	}
	return true
}

func loadPublicPoolAdminChannel(c *gin.Context) (*model.PublicPoolAdminChannel, bool) {
	if !requirePublicPoolRoot(c) {
		return nil, false
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"success": false, "message": "Public free channel not found"})
		return nil, false
	}
	channel, err := model.GetPublicPoolAdminChannel(id)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"success": false, "message": "Public free channel not found"})
		return nil, false
	}
	return channel, true
}

func ListPublicPoolAdminChannels(c *gin.Context) {
	if !requirePublicPoolRoot(c) {
		return
	}
	page := min(1000000, parseLimitOrDefault(c.Query("page"), 1))
	limit := min(100, parseLimitOrDefault(c.Query("page_size"), 20))
	status := 0
	if raw := c.Query("status"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 3 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		status = parsed
	}
	items, total, err := model.ListPublicPoolAdminChannels(c.Query("keyword"), status, (page-1)*limit, limit)
	if err != nil {
		common.ApiErrorT(c, "Failed to fetch channels. Please try again later")
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": total, "page": page, "page_size": limit})
}

func GetPublicPoolAdminChannel(c *gin.Context) {
	channel, ok := loadPublicPoolAdminChannel(c)
	if ok {
		common.ApiSuccess(c, channel)
	}
}

// publicPoolAdminMutationWriter buffers only the existing synchronous JSON
// management handler response. Its full Channel payload must never reach a
// browser: URL/query, provider settings and metadata may all hold credentials.
type publicPoolAdminMutationWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (writer *publicPoolAdminMutationWriter) Write(data []byte) (int, error) {
	return writer.body.Write(data)
}

func (writer *publicPoolAdminMutationWriter) WriteString(data string) (int, error) {
	return writer.body.WriteString(data)
}

func runPublicPoolAdminMutation(c *gin.Context, execute gin.HandlerFunc) {
	originalWriter := c.Writer
	buffer := &publicPoolAdminMutationWriter{ResponseWriter: originalWriter}
	c.Writer = buffer
	defer func() { c.Writer = originalWriter }()
	execute(c)
	c.Writer = originalWriter
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if common.Unmarshal(buffer.body.Bytes(), &response) != nil || !response.Success {
		// Driver/validator errors can embed key, URL or provider configuration.
		common.ApiErrorT(c, "Unable to save public free channel. Check the channel settings")
		return
	}
	common.ApiSuccess(c, gin.H{"id": response.Data.ID})
}

// bindPublicPoolAdminFields keeps ownership, tier, status, metadata and provider
// settings outside this deliberately narrow management form. Credentials/URLs
// are write-only; blank replacements on edit mean preserve the saved value.
func bindPublicPoolAdminFields(c *gin.Context, isAdd bool) (map[string]any, bool) {
	var fields map[string]any
	if c.ShouldBindJSON(&fields) != nil || len(fields) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return nil, false
	}
	for name := range fields {
		switch name {
		case "name", "type", "models", "group", "key", "base_url", "priority", "weight":
		default:
			common.ApiErrorT(c, "Invalid parameters")
			return nil, false
		}
	}
	for _, name := range []string{"name", "models", "group"} {
		value, ok := fields[name].(string)
		if !ok || strings.TrimSpace(value) == "" {
			common.ApiErrorT(c, "Invalid parameters")
			return nil, false
		}
		fields[name] = strings.TrimSpace(value)
	}
	if len([]rune(fields["name"].(string))) > 128 || len(fields["group"].(string)) > 64 {
		common.ApiErrorT(c, "Invalid parameters")
		return nil, false
	}
	for _, field := range []string{"models", "group"} {
		parts := strings.Split(fields[field].(string), ",")
		for i, part := range parts {
			parts[i] = strings.TrimSpace(part)
			if parts[i] == "" || len(parts[i]) > 255 {
				common.ApiErrorT(c, "Invalid parameters")
				return nil, false
			}
		}
		fields[field] = strings.Join(parts, ",")
	}
	if kind, exists := fields["type"]; exists {
		number, ok := kind.(float64)
		if !ok || number <= 0 || number >= constant.ChannelTypeDummy || number != float64(int(number)) {
			common.ApiErrorT(c, "Invalid parameters")
			return nil, false
		}
		if _, known := constant.ChannelTypeNames[int(number)]; !known {
			common.ApiErrorT(c, "Invalid parameters")
			return nil, false
		}
	}
	for _, field := range []string{"priority", "weight"} {
		if value, exists := fields[field]; exists {
			number, ok := value.(float64)
			if !ok || number < 0 || number > 2147483647 || number != float64(int64(number)) {
				common.ApiErrorT(c, "Invalid parameters")
				return nil, false
			}
		}
	}
	for name := range fields {
		if name != "key" && name != "base_url" {
			continue
		}
		value, ok := fields[name].(string)
		if !ok {
			common.ApiErrorT(c, "Invalid parameters")
			return nil, false
		}
		if !isAdd && strings.TrimSpace(value) == "" {
			delete(fields, name)
			continue
		}
		if name == "base_url" && value != "" {
			parsed, err := url.Parse(value)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				common.ApiErrorT(c, "Invalid parameters")
				return nil, false
			}
		}
	}
	if isAdd {
		if key, ok := fields["key"].(string); !ok || strings.TrimSpace(key) == "" {
			common.ApiErrorT(c, "Invalid parameters")
			return nil, false
		}
	}
	return fields, true
}

func AddPublicPoolAdminChannel(c *gin.Context) {
	if !requirePublicPoolRoot(c) {
		return
	}
	fields, ok := bindPublicPoolAdminFields(c, true)
	if !ok {
		return
	}
	raw, err := common.Marshal(fields)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	channel := &model.Channel{}
	if common.Unmarshal(raw, channel) != nil || channel.Type <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	channel.OwnerId, channel.Tier, channel.Visibility = 0, "free", 1
	channel.Status = common.ChannelStatusManuallyDisabled
	request := AddChannelRequest{Mode: "single", Channel: channel}
	// Use the supported admin controller for plugin permissions, validation,
	// abilities, encryption, cache refresh and audit; do not duplicate DB writes.
	raw, err = common.Marshal(request)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	c.Request.ContentLength = int64(len(raw))
	runPublicPoolAdminMutation(c, AddChannel)
}

func UpdatePublicPoolAdminChannel(c *gin.Context) {
	public, ok := loadPublicPoolAdminChannel(c)
	if !ok {
		return
	}
	fields, ok := bindPublicPoolAdminFields(c, false)
	if !ok {
		return
	}
	original, err := model.GetChannelById(public.Id, false)
	if err != nil || original.OwnerId != 0 || original.Tier != "free" {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"success": false, "message": "Public free channel not found"})
		return
	}
	if kind, exists := fields["type"]; exists && kind != float64(original.Type) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	fields["id"], fields["type"] = original.Id, original.Type
	fields["other"], fields["settings"] = original.Other, original.OtherSettings
	if _, exists := fields["base_url"]; !exists {
		fields["base_url"] = original.BaseURL
	}
	if original.Setting != nil {
		fields["setting"] = original.Setting
	}
	raw, err := common.Marshal(fields)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	c.Request.ContentLength = int64(len(raw))
	runPublicPoolAdminMutation(c, UpdateChannel)
}

func DeletePublicPoolAdminChannel(c *gin.Context) {
	channel, ok := loadPublicPoolAdminChannel(c)
	if ok {
		deleteChannelById(c, channel.Id)
	}
}

func CopyPublicPoolAdminChannel(c *gin.Context) {
	public, ok := loadPublicPoolAdminChannel(c)
	if !ok {
		return
	}
	original, err := model.GetChannelById(public.Id, true)
	if err != nil || original.OwnerId != 0 || original.Tier != "free" {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"success": false, "message": "Public free channel not found"})
		return
	}
	// A newly copied public channel also requires an explicit enable after test.
	original.Status = common.ChannelStatusManuallyDisabled
	copyChannelExecute(c, original)
}

func SetPublicPoolAdminChannelStatus(c *gin.Context) {
	if _, ok := loadPublicPoolAdminChannel(c); ok {
		UpdateChannelStatus(c)
	}
}

type publicOwner struct {
	UserId int    `json:"user_id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

type publicModelAgg struct {
	Model     string              `json:"model"`
	Providers int                 `json:"providers"`
	FreeCount int                 `json:"free_count"`
	FastestMs *int64              `json:"fastest_ms"`
	Rating    float64             `json:"rating"`
	Comments  int                 `json:"comments"`
	Owners    []publicOwner       `json:"owners"`
	Channels  []publicChannelView `json:"channels"`
}

type publicChannelView struct {
	ChannelId   int    `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	OwnerName   string `json:"owner_name"`
	OwnerAvatar string `json:"owner_avatar"`
	Tier        string `json:"tier"`
	SourceKind  string `json:"source_kind"`
	Provider    string `json:"provider"`
	Enabled     bool   `json:"enabled"`
	Status      string `json:"status"`
	HealthState string `json:"health_state"`
	// Health remains channel-scoped over the last five outcomes. Observed relay
	// metrics use an authorized channel/model's last 24h consume logs; TPS uses
	// end-to-end seconds, not generation time. Missing measurements stay null.
	MetricScope   string   `json:"metric_scope"`
	MetricSource  *string  `json:"metric_source"`
	WindowSeconds *int64   `json:"window_seconds"`
	TPSBasis      *string  `json:"tps_basis"`
	HealthScope   string   `json:"health_scope"`
	HealthWindow  string   `json:"health_window"`
	LatencySource *string  `json:"latency_source"`
	LatencyWindow string   `json:"latency_window"`
	Throughput    *float64 `json:"throughput"`
	TPS           *float64 `json:"tps"`
	LatencyMs     *int64   `json:"latency_ms"`
	SampleCount   *int     `json:"sample_count"`
	SampledAt     *int64   `json:"sampled_at"`
	ResponseTime  *int64   `json:"response_time"`
	SuccessRate   *float64 `json:"success_rate"`
	AvgLatencyMs  *int64   `json:"avg_latency_ms"`
	Samples       *int     `json:"samples"`
}

func publicPoolRequestGroups(c *gin.Context) ([]string, error) {
	user, err := model.GetUserCache(c.GetInt("id"))
	if err != nil {
		return nil, err
	}
	usable := service.GetUserUsableGroups(user.Group)
	groups := make([]string, 0, len(usable))
	for group := range usable {
		if group != "auto" {
			groups = append(groups, group)
		}
	}
	return groups, nil
}

// GetPublicPoolModels lists only models the current user's groups can relay.
func GetPublicPoolModels(c *gin.Context) {
	groups, err := publicPoolRequestGroups(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	briefs, err := model.ListPublicChannelsForGroups(groups)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	byModel := make(map[string]*publicModelAgg)
	ownerSeen := make(map[string]map[int]bool)
	channelIDs := make([]int, 0, len(briefs))
	channelSeen := make(map[int]bool)
	for _, brief := range briefs {
		// Health also contains private-era history without a public snapshot.
		if brief.OwnerId != 0 || brief.SourceKind != "platform_free" || brief.Tier != "free" {
			continue
		}
		if !channelSeen[brief.ChannelId] {
			channelSeen[brief.ChannelId] = true
			channelIDs = append(channelIDs, brief.ChannelId)
		}
	}
	// Resolve metrics only after tenant/group/subset filtering. Private IDs never
	// reach the health cache on this public endpoint.
	health := model.ChannelHealthBatch(channelIDs)
	logMetrics, metricsErr := model.GetPublicRelayLogMetrics(c.Request.Context(), briefs, groups)
	if metricsErr != nil {
		// Metrics are optional. Never expose driver SQL, DSNs, or private names.
		common.SysError("public relay metrics unavailable")
	}
	for _, brief := range briefs {
		agg := byModel[brief.Model]
		if agg == nil {
			agg = &publicModelAgg{Model: brief.Model, Owners: []publicOwner{}, Channels: []publicChannelView{}}
			byModel[brief.Model] = agg
			ownerSeen[brief.Model] = make(map[int]bool)
		}
		agg.Providers++
		if strings.EqualFold(brief.Tier, "free") {
			agg.FreeCount++
		}
		agg.Rating, agg.Comments = brief.Rating, brief.Comments
		if !ownerSeen[brief.Model][brief.OwnerId] {
			ownerSeen[brief.Model][brief.OwnerId] = true
			agg.Owners = append(agg.Owners, publicOwner{UserId: brief.OwnerId, Name: brief.OwnerName, Avatar: brief.OwnerAvatar})
		}
		view := publicChannelView{ChannelId: brief.ChannelId, ChannelName: brief.ChannelName, OwnerName: brief.OwnerName, OwnerAvatar: brief.OwnerAvatar, Tier: brief.Tier, SourceKind: brief.SourceKind, Provider: channelOwnerName(brief.ChannelType), Enabled: brief.Enabled, Status: "unknown", HealthState: "unknown", MetricScope: "channel", HealthWindow: "last_5_results", LatencyWindow: "accumulated_successful_results"}
		if h, ok := health[brief.ChannelId]; ok && h.Samples > 0 {
			count, rate := h.Samples, h.SuccessRate
			view.HealthState, view.Status = h.State, h.State
			view.SampleCount, view.Samples, view.SuccessRate = &count, &count, &rate
			if h.AvgLatencyMs > 0 {
				latency := h.AvgLatencyMs
				source := "accumulated_successful_results"
				view.LatencySource = &source
				view.LatencyMs, view.ResponseTime, view.AvgLatencyMs = &latency, &latency, &latency
			}
		}
		view.HealthScope = "channel"
		if metric, ok := logMetrics[model.PublicChannelModelKey{ChannelID: brief.ChannelId, ModelName: brief.Model}]; ok && metric.RequestCount > 0 {
			source, basis, window := "relay_logs_24h", "end_to_end", int64(86400)
			count, sampledAt := int(metric.RequestCount), metric.SampledAt
			rpm := float64(metric.RequestCount) / 1440.0
			view.MetricScope, view.MetricSource, view.WindowSeconds = "channel_model", &source, &window
			view.Throughput, view.SampleCount, view.SampledAt = &rpm, &count, &sampledAt
			// A channel-wide health latency is not a model-specific fallback once
			// the card switches scope to a channel/model log window.
			view.LatencyMs, view.ResponseTime, view.AvgLatencyMs, view.LatencySource = nil, nil, nil, nil
			view.LatencyWindow = "last_24_hours"
			if metric.TimedCount > 0 {
				latency := int64(metric.DurationSeconds / float64(metric.TimedCount) * 1000)
				view.LatencyMs, view.ResponseTime, view.AvgLatencyMs, view.LatencySource = &latency, &latency, &latency, &source
			}
			if metric.TokenDurationSeconds > 0 {
				tps := metric.OutputTokens / metric.TokenDurationSeconds
				view.TPS, view.TPSBasis = &tps, &basis
			}
		}
		if view.LatencyMs != nil && (agg.FastestMs == nil || *view.LatencyMs < *agg.FastestMs) {
			agg.FastestMs = view.LatencyMs
		}
		agg.Channels = append(agg.Channels, view)
	}
	items := make([]*publicModelAgg, 0, len(byModel))
	for _, agg := range byModel {
		sort.SliceStable(agg.Channels, func(i, j int) bool {
			a, b := agg.Channels[i], agg.Channels[j]
			if (a.Status == "down") != (b.Status == "down") {
				return b.Status == "down"
			}
			if a.LatencyMs == nil || b.LatencyMs == nil {
				return a.LatencyMs != nil && b.LatencyMs == nil
			}
			return *a.LatencyMs < *b.LatencyMs
		})
		items = append(items, agg)
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if (a.FastestMs == nil) != (b.FastestMs == nil) {
			return a.FastestMs != nil
		}
		if a.FastestMs != nil && b.FastestMs != nil && *a.FastestMs != *b.FastestMs {
			return *a.FastestMs < *b.FastestMs
		}
		if a.Providers != b.Providers {
			return a.Providers > b.Providers
		}
		return a.Model < b.Model
	})
	common.ApiSuccess(c, gin.H{"items": items, "total": len(items)})
}

func GetPublicPoolComments(c *gin.Context) {
	groups, err := publicPoolRequestGroups(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	rows, err := model.ListPublicComments(c.Query("model"), parseLimitOrDefault(c.Query("limit"), 50), groups)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows, "total": len(rows)})
}

func AddPublicPoolComment(c *gin.Context) {
	req := struct {
		Model     string `json:"model"`
		ChannelId int    `json:"channel_id"`
		Content   string `json:"content"`
		Rating    int    `json:"rating"`
	}{}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "Invalid parameters")
		return
	}
	req.Model, req.Content = strings.TrimSpace(req.Model), strings.TrimSpace(req.Content)
	if req.Model == "" || req.Content == "" {
		common.ApiErrorMsg(c, "model and content are required")
		return
	}
	if len([]rune(req.Content)) > 500 {
		common.ApiErrorMsg(c, "content too long (max 500)")
		return
	}
	if req.Rating < 0 || req.Rating > 5 {
		req.Rating = 0
	}
	groups, err := publicPoolRequestGroups(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	briefs, err := model.ListPublicChannelsForGroups(groups)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channelName := ""
	eligible := false
	for _, brief := range briefs {
		if brief.ChannelId == req.ChannelId && brief.Model == req.Model {
			channelName, eligible = brief.ChannelName, true
			break
		}
	}
	if !eligible {
		common.ApiErrorMsg(c, "channel is not public")
		return
	}
	uid := c.GetInt("id")
	user, err := model.GetUserById(uid, false)
	if err != nil || user == nil {
		common.ApiErrorMsg(c, "user not found")
		return
	}
	row := &model.PublicPoolComment{Model: req.Model, ChannelId: req.ChannelId, ChannelName: channelName, UserId: uid, Username: user.Username, DisplayName: user.DisplayName, Avatar: user.Avatar, Content: req.Content, Rating: req.Rating}
	if err := model.AddPublicComment(row); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, row)
}

func parseLimitOrDefault(raw string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
		return n
	}
	return def
}

// ---------------------------------------------------------------------------
// 公共池模型 × 公共节点 IP 维度可用性（root 专属）
// ---------------------------------------------------------------------------

// publicPoolProbeResult 单条模型×节点的测量行（IP 维度）。
type publicPoolProbeResult struct {
	EndpointID int    `json:"endpoint_id"`
	Status     string `json:"status"` // available|unavailable|unknown
	Reason     string `json:"reason"`
	LatencyMS  int64  `json:"latency_ms"`
}

// publicPoolModelIPView 一个模型在所有被测公共端点上的可用性汇总。
type publicPoolModelIPView struct {
	Model     string                  `json:"model"`
	Passed    bool                    `json:"passed"` // 所有被测端点全通过
	Available int                     `json:"available_endpoints"`
	Total     int                     `json:"total_endpoints"`
	Results   []publicPoolProbeResult `json:"results"`
}

// ListPublicPoolProbeEndpoints 返回公共渠道可选的公共端点（IP 维度展示用）。
// root 专属；只暴露端点 id / 归属订阅 / public 标记，绝不暴露地址或凭据。
func ListPublicPoolProbeEndpoints(c *gin.Context) {
	if !requirePublicPoolRoot(c) {
		return
	}
	channel, ok := loadPublicPoolAdminChannel(c)
	if !ok {
		return
	}
	endpoints, hasMore, err := model.ListModelProbeEndpoints(c.Request.Context(), c.GetInt("id"), 0, 100, true)
	if err != nil {
		common.ApiErrorT(c, "Model test endpoints are unavailable")
		return
	}
	common.ApiSuccess(c, gin.H{
		"channel_id":           channel.Id,
		"endpoints":            endpoints,
		"endpoint_has_more":    hasMore,
		"configured_egress_only": service.ModelProbeConfiguredEgressOnly(channel.Type),
	})
}

// PostPublicPoolProbeAllPublicEndpoints root 一键对公共渠道的所选模型
// 在全部公共节点上做 IP 维度实测。每模型×每端点一次，最大 3 模型 × 50 端点。
// 账号型渠道（owner_id=0 但绑凭据）只用配置出口，不轮换公共节点。
func PostPublicPoolProbeAllPublicEndpoints(c *gin.Context) {
	if !requirePublicPoolRoot(c) {
		return
	}
	channel, ok := loadPublicPoolAdminChannel(c)
	if !ok {
		return
	}
	var request struct {
		Models []string `json:"models"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	probeReq := service.ModelProbeRequest{Models: request.Models}
	if err := probeReq.Validate(); err != nil {
		common.ApiErrorT(c, err.Error())
		return
	}
	// 账号型渠道不能用公共节点轮换（凭据绑死配置出口）。
	if service.ModelProbeConfiguredEgressOnly(channel.Type) {
		common.ApiErrorT(c, "Account model tests must use configured egress")
		return
	}
	// 拉全部公共端点（上限 50，超出需分批）。
	endpoints, _, err := model.ListModelProbeEndpoints(c.Request.Context(), c.GetInt("id"), 0, 50, true)
	if err != nil {
		common.ApiErrorT(c, "Model test endpoints are unavailable")
		return
	}
	endpointIDs := []int{0} // 0 = 渠道配置出口（direct/fixed/public_pool 的稳定出口）
	for _, ep := range endpoints {
		if ep.ID > 0 {
			endpointIDs = append(endpointIDs, ep.ID)
		}
	}
	if len(endpointIDs) > 50 {
		endpointIDs = endpointIDs[:50]
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), service.ModelProbeBatchTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, modelProbeUserContextKey{}, c.GetInt("id"))

	views := make([]publicPoolModelIPView, 0, len(probeReq.Models))
	probeChannel, cerr := model.GetChannelById(channel.Id, true)
	if cerr != nil {
		common.ApiErrorT(c, "Public free channel not found")
		return
	}
	for _, modelName := range probeReq.Models {
		view := publicPoolModelIPView{Model: modelName, Results: []publicPoolProbeResult{}}
		allPassed := true
		for _, endpointID := range endpointIDs {
			resolved, rerr := model.ResolveModelProbeEndpoint(ctx, c.GetInt("id"), probeChannel, endpointID)
			if rerr != nil {
				view.Results = append(view.Results, publicPoolProbeResult{EndpointID: endpointID, Status: model.ModelEndpointUnknown, Reason: "endpoint_unavailable"})
				allPassed = false
				continue
			}
			started := time.Now()
			attemptCtx, attemptCancel := context.WithTimeout(ctx, service.ModelProbeAttemptTimeout)
			outcome := runSelfModelProbeRelay(attemptCtx, probeChannel, modelName, resolved)
			if attemptCtx.Err() != nil {
				outcome = service.ClassifyModelProbeError(attemptCtx, nil)
			}
			attemptCancel()
			latency := time.Since(started).Milliseconds()
			// 持久化测量（公共渠道 owner_id=0，scope 以公共端点计）。
			sample := model.ModelEndpointHealth{
				ChannelID:      channel.Id,
				Model:          modelName,
				EndpointID:     resolved.ID,
				SubscriptionID: resolved.SubscriptionID,
				Status:         outcome.Status,
				Reason:         outcome.Reason,
				HTTPStatus:     outcome.HTTPStatus,
				LatencyMS:      latency,
				CheckedAt:      time.Now().Unix(),
				ScopeKey:       model.ModelEndpointHealthScope(probeChannel, modelName, resolved.Endpoint, resolved.ProxyURL),
			}
			if _, err := model.RecordModelEndpointHealth(ctx, sample); err != nil {
				common.SysError("failed to record public pool model endpoint health")
			}
			view.Results = append(view.Results, publicPoolProbeResult{
				EndpointID: endpointID,
				Status:     outcome.Status,
				Reason:     outcome.Reason,
				LatencyMS:  latency,
			})
			if outcome.Status == model.ModelEndpointAvailable {
				view.Available++
			} else {
				allPassed = false
			}
		}
		view.Total = len(endpointIDs)
		view.Passed = allPassed && view.Available == view.Total
		views = append(views, view)
	}
	common.ApiSuccess(c, gin.H{
		"channel_id":        channel.Id,
		"endpoint_ids":      endpointIDs,
		"endpoint_count":    len(endpointIDs),
		"models":            views,
		"max_output_tokens": service.ModelProbeMaxOutputTokens,
	})
}

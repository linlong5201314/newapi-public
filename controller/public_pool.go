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
	"github.com/QuantumNous/new-api/setting/ratio_setting"
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
	// Model-level aggregates computed from the same visible channel metrics.
	// null when no authorized sample exists; zero is a real measured value.
	HealthScore    *float64 `json:"health_score"`
	SuccessRate24h *float64 `json:"success_rate_24h"`
	ActiveRequests *int64   `json:"active_requests"`
	ActiveUsers    *int64   `json:"active_users"`
	// Aliases whose requests already route to this canonical display name.
	Aliases []string `json:"aliases,omitempty"`
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
	// Current relay activity for this channel/model pair, counted from the
	// shared health cache; null when unknown rather than fabricated zero.
	ActiveRequests *int64 `json:"active_requests"`
	ActiveUsers    *int64 `json:"active_users"`
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
		// Merge entries that resolve to the same routing name (e.g. served alias
		// and normalized form) so one model has one card, while contributors
		// stay per-channel below. Never merge entries with different upstream
		// targets or capabilities.
		canonical := ratio_setting.RoutingMatchModelName(brief.Model)
		if canonical == "" {
			canonical = brief.Model
		}
		agg := byModel[canonical]
		if agg == nil {
			agg = &publicModelAgg{Model: canonical, Owners: []publicOwner{}, Channels: []publicChannelView{}}
			byModel[canonical] = agg
			ownerSeen[canonical] = make(map[int]bool)
		}
		if canonical != brief.Model {
			agg.Aliases = append(agg.Aliases, brief.Model)
		}
		agg.Providers++
		if strings.EqualFold(brief.Tier, "free") {
			agg.FreeCount++
		}
		agg.Rating, agg.Comments = brief.Rating, brief.Comments
		if !ownerSeen[canonical][brief.OwnerId] {
			ownerSeen[canonical][brief.OwnerId] = true
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
		// Model-level aggregates: weighted success across visible channels only.
		// No sample means unknown (null), never a fabricated zero or green.
		var weightedOk, weightedTotal float64
		var hasRate bool
		aliasSet := make(map[string]bool, len(agg.Aliases))
		uniqueAliases := agg.Aliases[:0]
		for _, alias := range agg.Aliases {
			if alias != "" && !aliasSet[alias] {
				aliasSet[alias] = true
				uniqueAliases = append(uniqueAliases, alias)
			}
		}
		agg.Aliases = uniqueAliases
		for _, view := range agg.Channels {
			if view.SuccessRate != nil && view.SampleCount != nil && *view.SampleCount > 0 {
				weightedOk += *view.SuccessRate / 100 * float64(*view.SampleCount)
				weightedTotal += float64(*view.SampleCount)
				hasRate = true
			}
		}
		if hasRate {
			score := weightedOk / weightedTotal * 100
			agg.HealthScore = &score
			agg.SuccessRate24h = &score
		}
		// Active request/user counts stay null until a relay-side shared
		// lease metric exists; never fabricate from channel health samples.
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

// publicPoolModelIPView 一个模型在被测公共端点样本上的可用性汇总。
// total_endpoints 恒等于本模型被测的端点数；attempted/available/unavailable/
// unknown/not_run 拆分该样本的结果，绝不把 unknown 计入 unavailable。
type publicPoolModelIPView struct {
	Model       string                  `json:"model"`
	Passed      bool                    `json:"passed"` // 所有被测端点全通过
	Attempted   int                     `json:"attempted"`
	Available   int                     `json:"available_endpoints"`
	Unavailable int                     `json:"unavailable_endpoints"`
	Unknown     int                     `json:"unknown_endpoints"`
	NotRun      int                     `json:"not_run_endpoints"`
	Total       int                     `json:"total_endpoints"`
	Results     []publicPoolProbeResult `json:"results"`
}

// publicPoolProbeBatchResponse 是一次抽样批次的契约，不是全量公共节点扫描。
type publicPoolProbeBatchResponse struct {
	ChannelID             int                     `json:"channel_id"`
	EndpointIDs           []int                   `json:"endpoint_ids"`
	SelectedCount         int                     `json:"selected_count"`
	EligibleTotal         int64                   `json:"eligible_total"`
	Sampled               bool                    `json:"sampled"`
	Models                []publicPoolModelIPView `json:"models"`
	CheckedAt             int64                   `json:"checked_at"`
	TTLSeconds            int64                   `json:"ttl_seconds"`
	StorageError          string                  `json:"storage_error,omitempty"`
	StorageFailedAttempts int                     `json:"storage_failed_attempts"`
	MaxOutputTokens       int                     `json:"max_output_tokens"`
	HealthNeverDeletes    bool                    `json:"health_never_deletes"`
}

// ListPublicPoolProbeEndpoints 返回公共渠道可选的公共端点（IP 维度展示用）。
// root 专属；只暴露端点 id / 归属订阅 / public 标记，绝不暴露地址或凭据。
// 支持 offset/limit 分页（limit ≤ 100），并返回 has_more 与合格总数。
func ListPublicPoolProbeEndpoints(c *gin.Context) {
	if !requirePublicPoolRoot(c) {
		return
	}
	channel, ok := loadPublicPoolAdminChannel(c)
	if !ok {
		return
	}
	offset, limit := 0, 100
	if raw := strings.TrimSpace(c.Query("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 1000000 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		offset = parsed
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		limit = parsed
	}
	ctx := c.Request.Context()
	endpoints, hasMore, err := model.ListModelProbeEndpoints(ctx, c.GetInt("id"), offset, limit, true)
	if err != nil {
		common.ApiErrorT(c, "Model test endpoints are unavailable")
		return
	}
	total, err := model.CountModelProbeEndpoints(ctx, c.GetInt("id"), true)
	if err != nil {
		common.ApiErrorT(c, "Model test endpoints are unavailable")
		return
	}
	common.ApiSuccess(c, gin.H{
		"channel_id":             channel.Id,
		"endpoints":              endpoints,
		"endpoint_has_more":      hasMore,
		"endpoint_next_offset":   offset + limit,
		"eligible_total":         total,
		"configured_egress_only": service.ModelProbeConfiguredEgressOnly(channel.Type),
	})
}

// PostPublicPoolProbeAllPublicEndpoints root 一键对公共渠道的所选模型在抽样
// 公共节点上做 IP 维度实测。配置出口（id 0）单独测试，不与公共节点样本混合；
// 解析后的端点 id 去重；最多 3 模型 × ModelProbeMaxPublicEndpoints 端点，串行、
// 逐个执行、遇致命错误（403/429/quota/output_limit_exceeded）停止本批。
// 复用共享授权与执行管线，绝不绕过。这是抽样批次，不是全部公共节点的全量扫描。
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
	// 账号型渠道不能用公共节点轮换（凭据绑死配置出口）。
	if service.ModelProbeConfiguredEgressOnly(channel.Type) {
		common.ApiErrorT(c, "Account model tests must use configured egress")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), service.ModelProbeBatchTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, modelProbeUserContextKey{}, c.GetInt("id"))

	// 共享授权：活跃会话 + root 平台免费范围 + allow_ips + 渠道模型目录。
	probeReq := service.ModelProbeRequest{Models: request.Models}.WithMaxEndpoints(service.ModelProbeMaxPublicEndpoints)
	authorize := func(ctx context.Context) (*model.Channel, error) {
		return authorizeSelfModelProbe(c, ctx, channel.Id, probeReq.Models)
	}
	if _, err := authorize(ctx); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}

	// 拉取本批抽样的公共端点（不含配置出口 id 0），并解析去重。
	endpoints, _, err := model.ListModelProbeEndpoints(ctx, c.GetInt("id"), 0, service.ModelProbeMaxPublicEndpoints, true)
	if err != nil {
		common.ApiErrorT(c, "Model test endpoints are unavailable")
		return
	}
	probeChannel, cerr := model.GetChannelById(channel.Id, true)
	if cerr != nil {
		common.ApiErrorT(c, "Public free channel not found")
		return
	}
	seen := map[int]bool{}
	endpointIDs := []int{}
	for _, ep := range endpoints {
		if ep.ID <= 0 || seen[ep.ID] {
			continue
		}
		resolved, rerr := model.ResolveModelProbeEndpoint(ctx, c.GetInt("id"), probeChannel, ep.ID)
		if rerr != nil || resolved.ID <= 0 || seen[resolved.ID] {
			continue
		}
		seen[resolved.ID] = true
		endpointIDs = append(endpointIDs, resolved.ID)
	}
	if len(endpointIDs) > service.ModelProbeMaxPublicEndpoints {
		endpointIDs = endpointIDs[:service.ModelProbeMaxPublicEndpoints]
	}
	probeReq.EndpointIDs = endpointIDs
	if err := probeReq.Validate(); err != nil {
		common.ApiErrorT(c, err.Error())
		return
	}

	// 存储预检：在消耗任何上游额度之前确认测量表可写。
	var preflight int64
	if err := model.DB.WithContext(ctx).Model(&model.ModelEndpointHealth{}).Where("channel_id = ?", channel.Id).Limit(1).Count(&preflight).Error; err != nil {
		common.ApiErrorT(c, "Model test history is unavailable")
		return
	}
	release, err := service.AcquireModelProbe(ctx, c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer release()

	batch, err := service.RunModelEndpointProbes(ctx, c.GetInt("id"), probeReq, authorize, runSelfModelProbeRelay)
	if err != nil {
		// 共享管线在存储失败、ACL/配置中途变更、授权失败时返回错误；
		// 不向调用方泄露内部细节，仅返回固定英文消息。
		common.ApiErrorMsg(c, err.Error())
		return
	}

	eligibleTotal, cerr := model.CountModelProbeEndpoints(ctx, c.GetInt("id"), true)
	if cerr != nil {
		eligibleTotal = int64(len(endpointIDs))
	}
	response := buildPublicPoolProbeResponse(channel.Id, endpointIDs, eligibleTotal, &batch)
	common.ApiSuccess(c, response)
}

// buildPublicPoolProbeResponse 把共享批次结果折叠为按模型的 IP 维度汇总，
// 并明确这是抽样批次。healthy 永不删除模型，仅记录测量。
func buildPublicPoolProbeResponse(channelID int, endpointIDs []int, eligibleTotal int64, batch *service.ModelProbeBatch) publicPoolProbeBatchResponse {
	views := map[string]*publicPoolModelIPView{}
	order := []string{}
	for _, sample := range batch.Results {
		view, ok := views[sample.Model]
		if !ok {
			view = &publicPoolModelIPView{Model: sample.Model, Results: []publicPoolProbeResult{}}
			views[sample.Model] = view
			order = append(order, sample.Model)
		}
		view.Total++
		view.Results = append(view.Results, publicPoolProbeResult{
			EndpointID: sample.EndpointID,
			Status:     sample.Status,
			Reason:     sample.Reason,
			LatencyMS:  sample.LatencyMS,
		})
		switch {
		case sample.Reason == "not_run":
			view.NotRun++
		case sample.Status == model.ModelEndpointAvailable:
			view.Attempted++
			view.Available++
		case sample.Status == model.ModelEndpointUnavailable:
			view.Attempted++
			view.Unavailable++
		default:
			view.Attempted++
			view.Unknown++
		}
	}
	models := make([]publicPoolModelIPView, 0, len(order))
	for _, name := range order {
		view := views[name]
		view.Passed = view.Total > 0 && view.Available == view.Total
		models = append(models, *view)
	}
	return publicPoolProbeBatchResponse{
		ChannelID:          channelID,
		EndpointIDs:        endpointIDs,
		SelectedCount:      len(endpointIDs),
		EligibleTotal:      eligibleTotal,
		Sampled:            true,
		Models:             models,
		CheckedAt:          time.Now().Unix(),
		TTLSeconds:         int64(model.ModelEndpointHealthTTL / time.Second),
		MaxOutputTokens:    service.ModelProbeMaxOutputTokens,
		HealthNeverDeletes: true,
	}
}

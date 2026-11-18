package controller

import (
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

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

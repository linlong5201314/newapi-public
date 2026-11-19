package controller

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func authorizeSelfModelProbe(c *gin.Context, ctx context.Context, channelID int, models []string) (*model.Channel, error) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || identity.UserID != c.GetInt("id") || c.GetBool("use_access_token") {
		return nil, errors.New("An active dashboard session is required")
	}
	// Check authoritative user/session versions for every inference, not merely
	// the role and user ID copied into a browser-supplied request or old cache.
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return model.ValidateAuthSessionWithTx(tx, model.AuthSessionIdentity{UserID: identity.UserID, SessionID: identity.SessionID, UserAuthVersion: identity.UserAuthVersion, SessionVersion: identity.SessionVersion})
	})
	if err != nil {
		return nil, errors.New("An active dashboard session is required")
	}
	var user model.User
	if err := model.DB.WithContext(ctx).Select("id, role, status").First(&user, identity.UserID).Error; err != nil || user.Status != common.UserStatusEnabled {
		return nil, errors.New("An active dashboard session is required")
	}
	channel, err := model.GetChannelById(channelID, true)
	if err != nil || channel.OwnerId != identity.UserID && !(channel.OwnerId == 0 && channel.Tier == "free" && user.Role == common.RoleRootUser) {
		return nil, errors.New("Channel not found")
	}
	if strings.EqualFold(strings.TrimSpace(channel.Tier), "free") && strings.TrimSpace(channel.AllowIps) != "" && !model.IpAllowedByRules(channel.AllowIps, c.ClientIP()) {
		return nil, errors.New("This free channel is restricted to its subscribed IP list")
	}
	catalog := map[string]bool{}
	for _, name := range channel.GetModels() {
		catalog[strings.TrimSpace(name)] = true
	}
	info := channel.GetOtherInfo()
	if all, ok := info["allModels"].([]any); ok {
		for _, value := range all {
			if name, ok := value.(string); ok {
				catalog[name] = true
			}
		}
	}
	for _, name := range models {
		if !catalog[name] {
			return nil, errors.New("Selected model is not in the channel catalog")
		}
	}
	return channel, nil
}

// GetSelfModelProbe is a passive, ACL-checked view. It never invokes inference.
func GetSelfModelProbe(c *gin.Context) {
	if _, ok := accountAuthorizationIdentity(c); !ok {
		return
	}
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid parameters"})
		return
	}
	models := []string{}
	if value := strings.TrimSpace(c.Query("models")); value != "" {
		request := service.ModelProbeRequest{Models: strings.Split(value, ",")}
		if err := request.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
			return
		}
		models = request.Models
	}
	channel, err := authorizeSelfModelProbe(c, c.Request.Context(), channelID, models)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
		return
	}
	var historyRows int64
	if err := model.DB.WithContext(c.Request.Context()).Model(&model.ModelEndpointHealth{}).Where("channel_id = ?", channelID).Limit(1).Count(&historyRows).Error; err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Model test history is unavailable"})
		return
	}
	offset, limit := 0, 50
	if value := c.Query("endpoint_offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil {
			offset = -1
		}
	}
	if value := c.Query("endpoint_limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			limit = -1
		}
	}
	endpoints, hasMore, err := model.ListModelProbeEndpoints(c.Request.Context(), c.GetInt("id"), offset, limit, channel.OwnerId == 0)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Model test endpoints are unavailable"})
		return
	}
	results := []model.ModelEndpointHealth{}
	visible := []model.ModelProbeEndpoint{{ID: 0, Status: model.ModelEndpointUnknown}}
	if selected := c.Query("endpoint_ids"); selected != "" {
		ids := strings.Split(selected, ",")
		if len(ids) > 3 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Select at most three endpoints"})
			return
		}
		selectedOptions := []model.ModelProbeEndpoint{}
		for _, raw := range ids {
			id, err := strconv.Atoi(raw)
			if err != nil || id <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid parameters"})
				return
			}
			endpoint, err := model.ModelProbeEndpointVisible(c.Request.Context(), c.GetInt("id"), id)
			if err != nil || channel.OwnerId == 0 && !endpoint.Public {
				c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Selected endpoint is unavailable or not permitted"})
				return
			}
			selectedOptions = append(selectedOptions, model.ModelProbeEndpoint{ID: id, SubscriptionID: endpoint.SubscriptionId, Public: endpoint.Public, Status: model.ModelEndpointUnknown})
		}
		for _, selectedOption := range selectedOptions {
			if !slices.ContainsFunc(endpoints, func(option model.ModelProbeEndpoint) bool { return option.ID == selectedOption.ID }) {
				endpoints = append(endpoints, selectedOption)
			}
		}
	}
	if configured, known := model.PeekModelProbeConfiguredEndpoint(c.Request.Context(), c.GetInt("id"), channel); known {
		for _, name := range models {
			scope := model.ModelEndpointHealthScope(channel, name, configured.Endpoint, configured.ProxyURL)
			sample, err := model.GetModelEndpointHealth(c.Request.Context(), scope, channelID, name, configured.ID, configured.SubscriptionID)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Unable to read model test history"})
				return
			}
			results = append(results, sample)
		}
	}
	for _, option := range endpoints {
		if channel.OwnerId == 0 && option.ID > 0 && !option.Public {
			continue
		}
		visible = append(visible, option)
		for _, name := range models {
			// Default egress is intentionally unknown in a passive view: do not
			// create a binding or hot-load a listener while merely opening UI.
			if option.ID == 0 {
				results = append(results, model.ModelEndpointHealth{ChannelID: channelID, Model: name, Status: model.ModelEndpointUnknown, Reason: "not_tested"})
				continue
			}
			endpoint, err := model.ModelProbeEndpointVisible(c.Request.Context(), c.GetInt("id"), option.ID)
			if err != nil {
				continue
			}
			scope := model.ModelEndpointHealthScope(channel, name, endpoint, "")
			sample, err := model.GetModelEndpointHealth(c.Request.Context(), scope, channelID, name, endpoint.Id, endpoint.SubscriptionId)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Model test history is unavailable"})
				return
			}
			if !slices.ContainsFunc(results, func(previous model.ModelEndpointHealth) bool {
				return previous.Model == sample.Model && previous.EndpointID == sample.EndpointID
			}) {
				results = append(results, sample)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"channel_id": channelID, "endpoints": visible, "results": results, "endpoint_has_more": hasMore, "endpoint_next_offset": offset + limit, "max_models": service.ModelProbeMaxModels, "max_endpoints": service.ModelProbeMaxEndpoints, "max_output_tokens": service.ModelProbeMaxOutputTokens, "configured_egress_only": service.ModelProbeConfiguredEgressOnly(channel.Type)}})
}

func PostSelfModelProbe(c *gin.Context) {
	if _, ok := accountAuthorizationIdentity(c); !ok {
		return
	}
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid parameters"})
		return
	}
	request := service.ModelProbeRequest{}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid parameters"})
		return
	}
	if err := request.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), service.ModelProbeBatchTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, modelProbeUserContextKey{}, c.GetInt("id"))
	authorize := func(ctx context.Context) (*model.Channel, error) {
		return authorizeSelfModelProbe(c, ctx, channelID, request.Models)
	}
	channel, err := authorize(ctx)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
		return
	}
	if service.ModelProbeConfiguredEgressOnly(channel.Type) && (len(request.EndpointIDs) != 1 || request.EndpointIDs[0] != 0) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Account model tests must use configured egress"})
		return
	}
	// Validate every selected ACL before the first paid request.
	for _, endpointID := range request.EndpointIDs {
		if endpointID == 0 {
			continue
		}
		endpoint, err := model.ModelProbeEndpointVisible(ctx, c.GetInt("id"), endpointID)
		if err != nil || channel.OwnerId == 0 && !endpoint.Public {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Selected endpoint is unavailable or not permitted"})
			return
		}
	}
	// Fail before inference if the parent has not installed the measurement
	// migration. Never spend quota and then discover storage was not ready.
	var count int64
	if err := model.DB.WithContext(ctx).Model(&model.ModelEndpointHealth{}).Where("channel_id = ?", channelID).Limit(1).Count(&count).Error; err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Model test history is unavailable"})
		return
	}
	release, err := service.AcquireModelProbe(ctx, c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer release()
	batch, err := service.RunModelEndpointProbes(ctx, c.GetInt("id"), request, authorize, runSelfModelProbeRelay)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": batch})
}

// modelProbeWriter bounds even a misbehaving upstream's captured response. No
// generated body or raw provider error is returned by the management API.
type modelProbeWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *modelProbeWriter) Header() http.Header { return w.header }
func (w *modelProbeWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *modelProbeWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len()+len(data) <= 65536 {
		_, _ = w.body.Write(data)
	} else {
		w.overflow = true
	}
	return len(data), nil
}
func (w *modelProbeWriter) Flush() {}

func runSelfModelProbeRelay(ctx context.Context, channel *model.Channel, name string, endpoint model.ModelProbeEndpoint) service.ModelProbeOutcome {
	// These adapters preserve the explicitly supplied output limit. Media,
	// arbitrary custom adapters and account wrappers with larger minimum output
	// are honest unknown/unsupported rather than an unbounded paid request.
	switch channel.Type {
	case constant.ChannelTypeOpenAI, constant.ChannelTypeAzure, constant.ChannelTypeOpenRouter, constant.ChannelTypeDeepSeek, constant.ChannelTypeNewAPI, constant.ChannelTypeCline, constant.ChannelTypeRaccoon, constant.ChannelTypeZCode, constant.ChannelTypeQoder:
	default:
		return service.ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "unsupported"}
	}
	if channel.ParamOverride != nil && strings.TrimSpace(*channel.ParamOverride) != "" && strings.TrimSpace(*channel.ParamOverride) != "{}" || channel.GetOtherSettings().AdvancedCustom != nil {
		return service.ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "unsafe_configuration"}
	}
	modelNames := []string{name}
	if channel.ModelMapping != nil && strings.TrimSpace(*channel.ModelMapping) != "" {
		var mappings map[string]string
		if common.Unmarshal([]byte(*channel.ModelMapping), &mappings) != nil {
			return service.ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "unsafe_configuration"}
		}
		for _, target := range mappings {
			modelNames = append(modelNames, target)
		}
	}
	for _, candidate := range modelNames {
		lower := strings.ToLower(candidate)
		for _, word := range []string{"image", "dall-e", "video", "sora", "tts", "audio", "whisper", "embed", "rerank", "codex", "thinking", "-reasoning"} {
			if strings.Contains(lower, word) {
				return service.ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "unsupported"}
			}
		}
	}
	budget := uint(service.ModelProbeMaxOutputTokens)
	request := &dto.GeneralOpenAIRequest{Model: name, Messages: []dto.Message{{Role: "user", Content: "Reply OK."}}, MaxTokens: &budget, Stream: common.GetPointer(false), N: common.GetPointer(1)}
	requestBytes, err := common.Marshal(request)
	if err != nil {
		return service.ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "local_rejection"}
	}
	w := &modelProbeWriter{header: http.Header{}}
	userID, _ := ctx.Value(modelProbeUserContextKey{}).(int)
	if userID <= 0 {
		return service.ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "local_rejection"}
	}
	engine := gin.New()
	user, err := model.GetUserCache(userID)
	if err != nil {
		return service.ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "local_rejection"}
	}
	engine.Use(func(c *gin.Context) {
		c.Set("id", userID)
		user.WriteContext(c)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, user.Group)
		common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
		_ = middleware.SetupContextForToken(c, &model.Token{UserId: userID, Name: "Model availability test", Group: user.Group})
		c.Next()
	})
	outcome := service.ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "local_rejection"}
	engine.POST("/pg/chat/completions", middleware.SystemPerformanceCheck(), middleware.ModelRequestRateLimit(), func(c *gin.Context) {
		userID := c.GetInt("id")
		user, err := model.GetUserCache(userID)
		if err != nil {
			return
		}
		user.WriteContext(c)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, user.Group)
		if err := middleware.SetupContextForToken(c, &model.Token{UserId: userID, Name: "Model availability test", Group: user.Group}); err != nil {
			return
		}
		if apiError := middleware.SetupContextForSelectedChannel(c, channel, name); apiError != nil {
			outcome = service.ClassifyModelProbeError(ctx, apiError)
			return
		}
		settings := channel.GetSetting()
		settings.Proxy, settings.ProxyPolicy = "direct", &dto.ChannelProxyPolicy{Mode: "direct"}
		if endpoint.ProxyURL != "" {
			settings.Proxy, settings.ProxyPolicy = endpoint.ProxyURL, &dto.ChannelProxyPolicy{Mode: "fixed"}
		}
		// Freeze the already resolved egress, and never inherit arbitrary system
		// prompts or proxy selection from the management caller's headers.
		settings.SystemPrompt = ""
		common.SetContextKey(c, constant.ContextKeyChannelSetting, settings)
		c.Set("status_code_mapping", "")
		c.Set("model_endpoint_probe", true)
		validated, err := helper.GetAndValidateRequest(c, types.RelayFormatOpenAI)
		if err != nil {
			return
		}
		info, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAI, validated, nil)
		if err != nil {
			return
		}
		info.InitChannelMeta(c)
		var apiError *types.NewAPIError
		defer func() { apiError = relay.RefundFailedRequestBilling(c, info, apiError) }()
		apiError = relay.PrepareRequestBilling(c, info)
		if apiError != nil {
			outcome = service.ClassifyModelProbeError(ctx, apiError)
			return
		}
		apiError = service.PrepareTieredBillingForSelectedGroup(c, info)
		if apiError != nil {
			outcome = service.ClassifyModelProbeError(ctx, apiError)
			return
		}
		apiError = relay.TextHelper(c, info)
		outcome = service.ClassifyModelProbeError(ctx, apiError)
		if apiError != nil {
			c.Status(max(http.StatusBadRequest, apiError.StatusCode))
			return
		}
		var response struct {
			Usage struct {
				CompletionTokens int64 `json:"completion_tokens"`
			} `json:"usage"`
			Error   any `json:"error"`
			Choices []struct {
				Message struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"message"`
			} `json:"choices"`
		}
		valid := !w.overflow && !info.IsStream && common.Unmarshal(w.body.Bytes(), &response) == nil && response.Error == nil && len(response.Choices) > 0
		if valid {
			valid = strings.TrimSpace(response.Choices[0].Message.Content+response.Choices[0].Message.ReasoningContent) != ""
		}
		outputTokens := response.Usage.CompletionTokens
		if info.IsStream && !w.overflow && bytes.Contains(w.body.Bytes(), []byte("data: [DONE]")) && validateTestResponseBody(w.body.Bytes(), true) == nil {
			for line := range bytes.SplitSeq(w.body.Bytes(), []byte{10}) {
				payload, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
				if !ok {
					continue
				}
				var chunk struct {
					Usage struct {
						CompletionTokens int64 `json:"completion_tokens"`
					} `json:"usage"`
					Choices []struct {
						Delta struct {
							Content          string `json:"content"`
							ReasoningContent string `json:"reasoning_content"`
						} `json:"delta"`
					} `json:"choices"`
				}
				if common.Unmarshal(bytes.TrimSpace(payload), &chunk) == nil {
					outputTokens = max(outputTokens, chunk.Usage.CompletionTokens)
					if len(chunk.Choices) > 0 && strings.TrimSpace(chunk.Choices[0].Delta.Content+chunk.Choices[0].Delta.ReasoningContent) != "" {
						valid = true
					}
				}
			}
		}
		if outputTokens > service.ModelProbeMaxOutputTokens {
			// A provider can ignore the requested cap. Preserve actual settlement,
			// never label that response a passing bounded probe, and stop the batch.
			outcome = service.ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "output_limit_exceeded", HTTPStatus: http.StatusOK}
		} else if !valid {
			outcome = service.ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "invalid_response"}
		}
	})
	httpRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/pg/chat/completions", bytes.NewReader(requestBytes))
	httpRequest.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, httpRequest)
	if w.status == http.StatusTooManyRequests {
		return service.ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "rate_limited", HTTPStatus: w.status}
	}
	return outcome
}

type modelProbeUserContextKey struct{}

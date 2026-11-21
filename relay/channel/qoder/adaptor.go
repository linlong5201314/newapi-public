// Package qoder implements a bounded native COSY adapter for owner accounts.
// Protocol reference: shuishuipingan/qoder2api-hub@25d05944cdc9b36cb69b162d9cba0d8e8989f4db
// (MIT; see LICENSE). Only text chat is supported; no upstream tools are executed.
package qoder

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

var renewal singleflight.Group
var errUnsupported = errors.New("Qoder native adapter supports text chat only; unsupported request fields must be removed")

type Adaptor struct{ openai.Adaptor }

func (a *Adaptor) GetChannelName() string { return "qoder" }
func (a *Adaptor) GetModelList() []string { return nil }

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info == nil || info.ChannelMeta == nil || info.ChannelType != constant.ChannelTypeQoder || info.RelayMode != relayconstant.RelayModeChatCompletions || info.ChannelBaseUrl != "" {
		return "", errUnsupported
	}
	var cred service.QoderCredential
	if common.Unmarshal([]byte(info.ApiKey), &cred) != nil {
		return "", service.ErrAccountProtocol
	}
	base, err := service.QoderGateway(cred.Realm)
	if err != nil {
		return "", err
	}
	return base + service.QoderInferencePath, nil
}

// Native signing occurs only after effective body validation. Generic header
// and parameter overrides must never replace owner account proof or native fields.
func (a *Adaptor) SetupRequestHeader(c *gin.Context, h *http.Header, info *relaycommon.RelayInfo) error {
	if info == nil || len(relaycommon.GetEffectiveHeaderOverride(info)) != 0 {
		return service.ErrAccountProtocol
	}
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if err := validateTextRequest(request); err != nil {
		return nil, err
	}
	return request, nil
}

func validateTextRequest(request *dto.GeneralOpenAIRequest) error {
	if request == nil || len(request.Messages) == 0 || len(request.Messages) > 1000 || !service.HeaderCredential(request.Model, 256) {
		return service.ErrAccountProtocol
	}
	if helper.ExceedsMaxTokensLimit(request.MaxTokens, request.MaxCompletionTokens) {
		return errUnsupported
	}
	if len(request.Tools) > 0 || request.ToolChoice != nil || len(request.Functions) > 0 || len(request.FunctionCall) > 0 || request.ResponseFormat != nil || request.Prompt != nil || request.N != nil && *request.N != 1 || len(request.ExtraBody) > 0 || len(request.Modalities) > 0 || len(request.Audio) > 0 || len(request.Reasoning) > 0 || request.ReasoningEffort != "" || request.TopK != nil || request.Seed != nil || request.FrequencyPenalty != nil || request.PresencePenalty != nil || len(request.LogitBias) > 0 || len(request.Prediction) > 0 || request.LogProbs != nil || request.TopLogProbs != nil || len(request.EnableThinking) > 0 || len(request.ThinkingBudget) > 0 {
		return errUnsupported
	}
	for _, m := range request.Messages {
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" {
			return errUnsupported
		}
		if _, ok := m.Content.(string); !ok {
			return errUnsupported
		}
		if len(m.ToolCalls) > 0 || m.ToolCallId != "" || len(m.Tools) > 0 || m.ReasoningContent != nil || m.Reasoning != nil {
			return errUnsupported
		}
	}
	return nil
}

func storedCredential(info *relaycommon.RelayInfo) (*model.Channel, *service.QoderCredential, error) {
	if info == nil || info.ChannelMeta == nil || info.ChannelId <= 0 || info.ChannelType != constant.ChannelTypeQoder || info.ApiKey == "" {
		return nil, nil, service.ErrAccountProtocol
	}
	stored, err := model.GetChannelById(info.ChannelId, true)
	if err != nil || stored.OwnerId <= 0 || stored.Type != constant.ChannelTypeQoder || !common.IsEncryptedSecret(stored.Key) {
		return nil, nil, service.ErrAccountProtocol
	}
	secret, err := common.DecryptSecret(stored.Key)
	if err != nil || secret != info.ApiKey {
		return nil, nil, service.ErrAccountProtocol
	}
	var cred service.QoderCredential
	if common.Unmarshal([]byte(secret), &cred) != nil || !service.HeaderCredential(cred.UserID, 256) || !service.HeaderCredential(cred.Token, 16384) || !service.HeaderCredential(cred.MachineID, 128) {
		return nil, nil, service.ErrAccountProtocol
	}
	if _, err := service.QoderGateway(cred.Realm); err != nil {
		return nil, nil, err
	}
	return stored, &cred, nil
}

func renew(ctx context.Context, stored *model.Channel, cred *service.QoderCredential, secret, proxy string) (*service.QoderCredential, error) {
	if cred.ExpiresAt == 0 || cred.ExpiresAt > time.Now().Add(2*time.Minute).Unix() {
		return cred, nil
	}
	key := strconv.Itoa(stored.Id) + ":" + common.GenerateHMACWithKey([]byte(secret), "qoder-renew")
	ch := renewal.DoChan(key, func() (any, error) {
		latest, err := model.GetChannelById(stored.Id, true)
		if err != nil || latest.OwnerId != stored.OwnerId || latest.Type != stored.Type || latest.Key != stored.Key {
			return nil, service.ErrAccountProtocol
		}
		settings := latest.GetSetting()
		if settings.TLSInsecureSkipVerify {
			return nil, service.ErrAccountProtocol
		}
		pinned, err := cred.Egress.Resolve(latest.OwnerId, latest.Id, settings)
		if err != nil || pinned != proxy {
			return nil, service.ErrAccountEgress
		}
		next, err := service.RenewQoderCredential(ctx, proxy, cred)
		if err != nil {
			return nil, err
		}
		raw, err := common.Marshal(next)
		if err != nil {
			return nil, service.ErrAccountProtocol
		}
		encrypted, err := common.EncryptSecret(string(raw))
		if err != nil || !common.IsEncryptedSecret(encrypted) {
			return nil, service.ErrAccountProtocol
		}
		// Network I/O has completed before taking any database write lock.
		result := model.DB.WithContext(ctx).Model(&model.Channel{}).Where(map[string]any{"id": stored.Id, "owner_id": stored.OwnerId, "type": stored.Type, "key": stored.Key}).Update("key", encrypted)
		if result.Error != nil {
			return nil, service.ErrAccountProtocol
		}
		if result.RowsAffected != 1 {
			return nil, service.ErrAccountProtocol
		}
		model.InitChannelCache()
		return next, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			return nil, result.Err
		}
		next, ok := result.Val.(*service.QoderCredential)
		if !ok {
			return nil, service.ErrAccountProtocol
		}
		copyCred := *next
		return &copyCred, nil
	}
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	if _, err := a.GetRequestURL(info); err != nil {
		return nil, err
	}
	if len(relaycommon.GetEffectiveHeaderOverride(info)) != 0 {
		return nil, service.ErrAccountProtocol
	}
	stored, cred, err := storedCredential(info)
	if err != nil {
		return nil, err
	}
	settings := stored.GetSetting()
	if settings.TLSInsecureSkipVerify {
		return nil, service.ErrAccountProtocol
	}
	proxy, err := cred.Egress.Resolve(stored.OwnerId, stored.Id, settings)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(body, (8<<20)+1))
	if err != nil || len(raw) > 8<<20 {
		return nil, service.ErrAccountProtocol
	}
	var request dto.GeneralOpenAIRequest
	if common.Unmarshal(raw, &request) != nil {
		return nil, service.ErrAccountProtocol
	}
	// DoRequest also validates after channel parameter overrides or passthrough.
	if err := validateTextRequest(&request); err != nil {
		return nil, err
	}
	modelKey := info.UpstreamModelName
	if modelKey == "" {
		modelKey = info.OriginModelName
	}
	if request.Model != modelKey || !containsModel(stored.Models, modelKey) {
		return nil, service.ErrAccountProtocol
	}
	cred, err = renew(c.Request.Context(), stored, cred, info.ApiKey, proxy)
	if err != nil {
		return nil, err
	}
	// Resolve the selected model from the same owner's real current native catalog.
	catalog, err := service.FetchQoderModelCatalog(c.Request.Context(), proxy, cred)
	if err != nil {
		return nil, err
	}
	var selected *service.QoderModel
	for i := range catalog {
		if catalog[i].Key == modelKey {
			selected = &catalog[i]
			break
		}
	}
	if selected == nil {
		return nil, service.ErrAccountProtocol
	}
	native, err := nativeTextBody(&request, cred, *selected)
	if err != nil {
		return nil, err
	}
	plain, err := common.Marshal(native)
	if err != nil || len(plain) > 8<<20 {
		return nil, service.ErrAccountProtocol
	}
	encoded := service.EncodeQoderBody(plain)
	base, err := service.QoderGateway(cred.Realm)
	if err != nil {
		return nil, err
	}
	target := base + service.QoderInferencePath
	headers, err := service.QoderSignedHeaders(cred, encoded, target, modelKey)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target, strings.NewReader(encoded))
	if err != nil {
		return nil, service.ErrAccountProtocol
	}
	req.Header = headers
	client, err := service.NewQoderHTTPClient(proxy)
	if err != nil || client == nil {
		return nil, service.ErrAccountEgress
	}
	copyClient := *client
	copyClient.Jar = nil
	copyClient.Timeout = 10 * time.Minute
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := copyClient.Do(req)
	if err != nil {
		return nil, service.QoderRequestError(c.Request.Context(), err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		resp.Body.Close()
		// Operator-facing diagnostic only: never surfaced to the API caller.
		common.SysLog("qoder inference rejected: status=" + strconv.Itoa(resp.StatusCode) + " body=" + qoderSnippet(snippet))
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, service.ErrDesktopLoginExpired
		}
		return nil, service.ErrAccountUpstream
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		resp.Body.Close()
		common.SysLog("qoder inference non-sse: content_type=" + resp.Header.Get("Content-Type") + " body=" + qoderSnippet(snippet))
		return nil, service.ErrAccountProtocol
	}
	// No provider cookies, credentials or diagnostic response headers are exposed.
	resp.Header = http.Header{"Content-Type": {"text/event-stream"}}
	return resp, nil
}

// qoderSnippet bounds and flattens an upstream diagnostic fragment.
func qoderSnippet(raw []byte) string {
	text := strings.Join(strings.Fields(string(raw)), " ")
	if len(text) > 320 {
		text = text[:320]
	}
	return text
}

func containsModel(models, key string) bool {
	for id := range strings.SplitSeq(models, ",") {
		if id == key {
			return true
		}
	}
	return false
}

func nativeTextBody(request *dto.GeneralOpenAIRequest, cred *service.QoderCredential, selected service.QoderModel) (map[string]any, error) {
	if err := validateTextRequest(request); err != nil {
		return nil, err
	}
	messages := make([]any, 0, len(request.Messages))
	prompt := ""
	for _, m := range request.Messages {
		text := m.Content.(string)
		if m.Role == "user" {
			prompt = text
			messages = append(messages, map[string]any{"role": "user", "content": "", "contents": []any{map[string]any{"type": "text", "text": text}}})
		} else {
			messages = append(messages, map[string]any{"role": m.Role, "content": text})
		}
	}
	mc := map[string]any{"key": selected.Key, "display_name": selected.DisplayName, "model": "", "format": "openai", "is_vl": selected.IsVL, "is_reasoning": selected.IsReasoning, "api_key": "", "url": "", "source": "system"}
	if selected.MaxInputTokens > 0 {
		mc["max_input_tokens"] = selected.MaxInputTokens
	}
	params := map[string]any{}
	// Keep absent distinct from explicit zero; root request validator supplies
	// the common max-tokens bound, which is rechecked above after overrides.
	if request.MaxTokens != nil {
		params["max_tokens"] = *request.MaxTokens
	}
	if request.MaxCompletionTokens != nil {
		params["max_tokens"] = *request.MaxCompletionTokens
	}
	if request.Temperature != nil {
		params["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		params["top_p"] = *request.TopP
	}
	if request.Stop != nil {
		params["stop"] = request.Stop
	}
	id := uuid.NewString()
	out := map[string]any{"request_id": id, "request_set_id": uuid.NewString(), "chat_record_id": uuid.NewString(), "session_id": uuid.NewString(), "stream": true, "chat_task": "FREE_INPUT", "image_urls": nil, "is_reply": true, "is_retry": false, "code_language": "", "source": 1, "version": "3", "chat_prompt": "", "parameters": params, "aliyun_user_type": cred.UserType, "session_type": "qodercli", "agent_id": "agent_common", "task_id": "common", "model_config": mc, "messages": messages, "tools": []any{}, "business": map[string]any{"id": id, "begin_at": time.Now().UnixMilli(), "scene": "chat", "type": "chat", "sub_type": "free_input", "name": "", "language": "", "parent_type": ""}, "chat_context": map[string]any{"chatPrompt": "", "extra": map[string]any{"context": []any{}, "modelConfig": mc, "originalContent": map[string]any{"type": "text", "text": prompt}}, "features": []any{}, "imageUrls": nil, "text": map[string]any{"type": "text", "text": prompt}}}
	return out, nil
}

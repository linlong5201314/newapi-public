// adaptor.go Trae 渠道的 new-api adaptor 实现：
// solo_work_lite 改写 / SOLO SSE → OpenAI SSE 翻译 / notify_usage 提取写渠道 Balance /
// 错误分类 → 渠道禁用/冷却语义。
package trae

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

const (
	// ChannelName 渠道类型名。
	ChannelName = "Trae"
)

// defaultModels 渠道创建时的默认模型清单（get_detail_param 实测常见配置名）。
var defaultModels = []string{
	"glm-5.2",
	"deepseek-v3.2",
	"kimi-k2.5",
	"gpt-5.5",
	"claude-sonnet-4.5",
}

// Adaptor Trae 渠道 adaptor（无状态；凭据每请求从 info.ApiKey 解析）。
type Adaptor struct {
	cred *Cred
}

// Init 解析渠道凭据。
func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	cred, err := ParseCred(info.ApiKey)
	if err != nil {
		common.SysLog("trae adaptor: credential parse failed: " + err.Error())
		return
	}
	a.cred = cred
}

// GetRequestURL chat 端点（adaptor 自实现 DoRequest，返回值供日志/观测）。
func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayMode != relayconstant.RelayModeChatCompletions {
		return "", errors.New("trae channel: only /v1/chat/completions is supported")
	}
	base := AgentHost
	if a.cred != nil {
		base = NewClient(a.cred).agentBase()
	} else if info.ChannelBaseUrl != "" {
		base = strings.TrimSuffix(info.ChannelBaseUrl, "/")
	}
	return base + EpChat, nil
}

// SetupRequestHeader 由 DoRequest 内部构造。
func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	return nil
}

// ConvertOpenAIRequest 原样透传（payload 改写管线在 DoRequest 的字节层执行）。
func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("trae channel: request is nil")
	}
	return request, nil
}

// DoRequest 完整出站流程：凭据刷新（ExchangeToken 单飞）→ payload 改写 → 上游 SSE。
func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if a.cred == nil {
		return nil, errors.New("trae channel: invalid credential JSON in channel key")
	}
	body, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, fmt.Errorf("trae channel: read request body: %w", err)
	}
	cred, err := RefreshIfNeeded(info.ChannelId, a.cred)
	if err != nil {
		return nil, fmt.Errorf("trae channel: token refresh failed: %w", err)
	}
	a.cred = cred

	client := NewClient(cred)
	rc, status, respBody, chatErr := client.ChatStream(c.Request.Context(), body)
	if chatErr == nil && rc != nil {
		// 上游恒为 SSE；Content-Type 按客户端流式意图标记，否则 relay 核心
		// 会把 info.IsStream 翻成 true，非流式请求永远拿不到聚合 JSON。
		contentType := "application/json"
		if info.IsStream {
			contentType = "text/event-stream"
		}
		resp := &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{contentType}},
			Body:       rc,
		}
		return resp, nil
	}
	if chatErr != nil && respBody == nil {
		return nil, fmt.Errorf("trae channel: upstream transport: %w", chatErr)
	}
	var ue *Error
	if errors.As(chatErr, &ue) {
		a.applyChannelHealth(info, ue)
	}
	errResp := &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}
	return errResp, nil
}

// applyChannelHealth 错误分类 → new-api 渠道禁用/冷却语义。
func (a *Adaptor) applyChannelHealth(info *relaycommon.RelayInfo, ue *Error) {
	if info == nil || info.ChannelId <= 0 {
		return
	}
	var reason string
	switch ue.Kind {
	case ErrSessionDead:
		// 401/1001 session 失效 → 显式禁用（「需重新导入凭据」终态）。
		reason = "trae session dead, re-import credential required: " + ue.Msg
	case ErrPlanLimit:
		reason = "trae plan limit (1005), will re-enable after checkin: " + ue.Msg
	case ErrQuotaExhausted:
		reason = "trae quota exhausted (4008), will re-enable after checkin: " + ue.Msg
	default:
		return
	}
	ch, err := model.GetChannelById(info.ChannelId, true)
	if err != nil || ch == nil || !ch.GetAutoBan() {
		return
	}
	common.SysLog(fmt.Sprintf("trae channel #%d auto action (%s): %s", info.ChannelId, ue.Kind, reason))
	model.UpdateChannelStatus(info.ChannelId, "", common.ChannelStatusAutoDisabled, truncate(reason, 200))
}

// applyStreamErrorHealth SOLO 流内错误帧 → 渠道健康语义（映射到 HTTP 状态码等价物）。
func (a *Adaptor) applyStreamErrorHealth(info *relaycommon.RelayInfo, se *SOLOStreamError) {
	if info == nil || info.ChannelId <= 0 {
		return
	}
	var reason string
	switch se.Kind() {
	case ErrSessionDead:
		reason = fmt.Sprintf("trae session dead (1001), re-import credential required: %s", se.Msg)
	case ErrPlanLimit:
		reason = fmt.Sprintf("trae plan limit (1005), will re-enable after checkin: %s", se.Msg)
	case ErrQuotaExhausted:
		reason = fmt.Sprintf("trae quota exhausted (4008), will re-enable after checkin: %s", se.Msg)
	default:
		return
	}
	ch, err := model.GetChannelById(info.ChannelId, true)
	if err != nil || ch == nil || !ch.GetAutoBan() {
		return
	}
	common.SysLog(fmt.Sprintf("trae channel #%d auto action (stream %d): %s", info.ChannelId, se.Code, reason))
	model.UpdateChannelStatus(info.ChannelId, "", common.ChannelStatusAutoDisabled, truncate(reason, 200))
}

// DoResponse 处理上游 SOLO SSE：翻译为 OpenAI SSE / 聚合为单帧；
// notify_usage 帧提取 ide_credits 写渠道 Balance。
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(errors.New("trae channel: empty upstream response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer resp.Body.Close()
	if info.IsStream {
		return a.streamResponse(c, resp, info)
	}
	return a.aggregateResponse(c, resp, info)
}

// streamResponse 流式翻译：SOLO output/token_usage/notify_usage/done/error → OpenAI chunks。
func (a *Adaptor) streamResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	var (
		pendingUsage map[string]any
		res          = &SOLOStreamResult{}
		chunkID      = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	)
	st := &sseState{}
	br := resp.Body
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 32*1024)

	flushChunk := func(delta map[string]any, finish string) error {
		chunk := map[string]any{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   info.UpstreamModelName,
			"choices": []any{
				map[string]any{"index": 0, "delta": delta},
			},
		}
		choice := chunk["choices"].([]any)[0].(map[string]any)
		if finish != "" {
			choice["finish_reason"] = finish
		}
		if pendingUsage != nil {
			chunk["usage"] = pendingUsage
			pendingUsage = nil
		}
		return helper.ObjectData(c, chunk)
	}

	// readLine 按行产出 SSE 行。注意 http.Response.Body 在最后一个数据块
	// 允许同时返回 (n>0, io.EOF)：数据必须先入 buf 再按换行切分，
	// EOF 只作终止标记缓存，绝不在 n>0 时直接返回。
	var pendingErr error
	readLine := func() (string, error) {
		for {
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				line := string(buf[:i])
				buf = buf[i+1:]
				return strings.TrimRight(line, "\r"), nil
			}
			if pendingErr != nil {
				if len(buf) > 0 {
					line := string(buf)
					buf = buf[:0]
					return strings.TrimRight(line, "\r"), nil
				}
				return "", pendingErr
			}
			n, err := br.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if err != nil {
				pendingErr = err
			}
		}
	}

	for {
		line, err := readLine()
		if ev := scanLine(st, line); ev != nil {
			switch ev.Event {
			case "output":
				res.SawValid = true
				delta := map[string]any{}
				if ev.Response != "" {
					delta["content"] = ev.Response
				}
				if ev.Reasoning != "" {
					delta["reasoning_content"] = ev.Reasoning
				}
				if len(ev.ToolCalls) > 0 && string(ev.ToolCalls) != "null" {
					var tc []map[string]any
					if err := json.Unmarshal(ev.ToolCalls, &tc); err == nil {
						for _, call := range tc {
							if fc, ok := call["function_call"].(map[string]any); ok {
								call["function"] = fc
								delete(call, "function_call")
							}
							if fn, ok := call["function"].(map[string]any); ok {
								delete(fn, "namespace")
								delete(fn, "partial_arguments")
							}
						}
						delta["tool_calls"] = tc
					}
				}
				if len(delta) > 0 {
					if err := flushChunk(delta, ""); err != nil {
						return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
					}
				}
			case "token_usage":
				pendingUsage = ev.Usage
				res.Usage = ev.Usage
			case "notify_usage":
				// 真实额度写渠道 Balance（ide_credits 口径，ent_usage 聚合会虚高）。
				if credits, ok := ExtractIdeCredits(ev.NotifyUsage); ok {
					res.IdeCredits, res.HasIdeCred = credits, true
				}
			case "done":
				if err := flushChunk(map[string]any{}, ev.FinishReason); err != nil {
					return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
				}
			case "error":
				se := &SOLOStreamError{Code: ev.ErrorCode, Msg: ev.ErrorMessage}
				res.StreamError = se
			}
		}
		if err != nil {
			break
		}
	}

	// 渠道健康副作用（流内错误帧）。
	if res.StreamError != nil {
		a.applyStreamErrorHealth(info, res.StreamError)
		// 流已开（200），以 error 事件帧收尾告知客户端。
		errFrame, _ := json.Marshal(map[string]any{"error": map[string]any{
			"message": res.StreamError.Error(),
			"type":    "upstream_error",
			"code":    res.StreamError.Code,
		}})
		_ = helper.StringData(c, string(errFrame))
		helper.Done(c)
		a.persistIdeCredits(info, res)
		status := http.StatusBadRequest
		if res.StreamError.Kind() == ErrSessionDead {
			status = http.StatusUnauthorized
		} else if res.StreamError.Kind() == ErrSoftRate {
			status = http.StatusTooManyRequests
		}
		return nil, types.NewOpenAIError(res.StreamError, types.ErrorCodeBadResponse, status)
	}

	a.persistIdeCredits(info, res)
	if !res.SawValid && res.Usage == nil {
		return nil, types.NewOpenAIError(errEmptyStream, types.ErrorCodeEmptyResponse, http.StatusBadGateway)
	}
	helper.Done(c)
	return buildUsage(res.Usage, info), nil
}

// persistIdeCredits notify_usage 帧余额写渠道 Balance 字段。
func (a *Adaptor) persistIdeCredits(info *relaycommon.RelayInfo, res *SOLOStreamResult) {
	if !res.HasIdeCred || info == nil || info.ChannelId <= 0 {
		return
	}
	if ch, err := model.GetChannelById(info.ChannelId, true); err == nil && ch != nil {
		ch.UpdateBalance(float64(res.IdeCredits))
	}
}

// aggregateResponse 非流式：聚合 SOLO SSE 为单个 chat.completion JSON。
func (a *Adaptor) aggregateResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	full, res, err := Aggregate(resp.Body)
	a.persistIdeCredits(info, res)
	if err != nil {
		var se *SOLOStreamError
		if errors.As(err, &se) {
			a.applyStreamErrorHealth(info, se)
			status := http.StatusBadRequest
			if se.Kind() == ErrSessionDead {
				status = http.StatusUnauthorized
			} else if se.Kind() == ErrSoftRate {
				status = http.StatusTooManyRequests
			}
			return nil, types.NewOpenAIError(se, types.ErrorCodeBadResponse, status)
		}
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	full["model"] = info.UpstreamModelName
	raw, err := json.Marshal(full)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	if _, err := c.Writer.Write(raw); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	return buildUsage(res.Usage, info), nil
}

// buildUsage 从 token_usage 帧构造 dto.Usage。
func buildUsage(usageMap map[string]any, info *relaycommon.RelayInfo) *dto.Usage {
	usage := &dto.Usage{}
	if usageMap == nil {
		return usage
	}
	num := func(k string) int {
		switch v := usageMap[k].(type) {
		case float64:
			return int(v)
		case int64:
			return int(v)
		case json.Number:
			i, _ := v.Int64()
			return int(i)
		}
		return 0
	}
	usage.PromptTokens = num("prompt_tokens")
	usage.CompletionTokens = num("completion_tokens")
	usage.TotalTokens = num("total_tokens")
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage
}

// GetModelList 渠道类型的默认模型清单。
func (a *Adaptor) GetModelList() []string {
	return defaultModels
}

// GetChannelName 渠道类型名。
func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

// ---------------------------------------------------------------------------
// 不支持的端点
// ---------------------------------------------------------------------------

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("trae channel: /v1/rerank endpoint not supported")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("trae channel: /v1/embeddings endpoint not supported")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("trae channel: audio endpoint not supported")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("trae channel: image endpoint not supported")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("trae channel: /v1/responses endpoint not supported")
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("trae channel: /v1/messages endpoint not supported")
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("trae channel: gemini endpoint not supported")
}

// ---------------------------------------------------------------------------
// 渠道级扩展（签到中心/导入端点调用）
// ---------------------------------------------------------------------------

// FetchUpstreamModels 拉取账号实时模型目录。
func FetchUpstreamModels(credJSON string) ([]string, error) {
	cred, err := ParseCred(credJSON)
	if err != nil {
		return nil, err
	}
	infos, err := NewClient(cred).FetchModels()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(infos))
	for _, mi := range infos {
		out = append(out, mi.ID)
	}
	return out, nil
}

// RefreshBalance 查询 EntUsage 余额并写渠道 Balance 字段。
func RefreshBalance(channelId int, cred *Cred) (remain int64, err error) {
	remain, err = NewClient(cred).UserEntUsage()
	if err != nil {
		return 0, err
	}
	if channelId > 0 {
		if ch, cerr := model.GetChannelById(channelId, true); cerr == nil && ch != nil {
			ch.UpdateBalance(float64(remain))
		}
	}
	return remain, nil
}

var _ channel.Adaptor = (*Adaptor)(nil)

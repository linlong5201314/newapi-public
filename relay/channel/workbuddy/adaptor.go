// adaptor.go WorkBuddy 渠道的 new-api adaptor 实现：
// Init/GetRequestURL/ConvertRequest/DoRequest/DoResponse + 错误分类 → 渠道健康语义。
package workbuddy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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
	// ChannelName 渠道类型名（与 constant.ChannelTypeNames 对齐）。
	ChannelName = "WorkBuddy"
	// defaultTestModel 渠道测试缺省模型。
	defaultTestModel = "deepseek-v4.1-flash"
)

// defaultModels 渠道创建时的默认模型清单（用户可通过「拉取模型」换成上游实时目录）。
var defaultModels = []string{
	"deepseek-v4.1-flash",
	"deepseek-v4-flash",
	"glm-5.2",
	"gpt-6-astra",
	"gpt-6-sol",
	"gpt-6-luna",
	"gpt-5.6-sol",
	"kimi-k2.8-preview",
	"hy3",
	"hy4-preview",
	"hy4-preview-f",
	"o4-mini",
	"enhance-1.0",
	"auto-chat",
}

// Adaptor WorkBuddy 渠道 adaptor（无状态；凭据每请求从 info.ApiKey 解析）。
type Adaptor struct {
	cred *Cred
}

// Init 解析渠道凭据（info.ApiKey = channels.key JSON）。
func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	cred, err := ParseCred(info.ApiKey)
	if err != nil {
		// 凭据非法：留 nil，DoRequest 返回配置错误（不禁用渠道——这是配置问题不是账号问题）。
		common.SysLog("workbuddy adaptor: credential parse failed: " + err.Error())
		return
	}
	a.cred = cred
}

// GetRequestURL chat 端点（adaptor 自实现 DoRequest，返回值主要供日志/观测）。
func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayMode != relayconstant.RelayModeChatCompletions {
		return "", errors.New("workbuddy channel: only /v1/chat/completions is supported")
	}
	base := defaultChatBaseCN
	if a.cred != nil {
		base = NewClient(a.cred).chatBase()
	} else if info.ChannelBaseUrl != "" {
		base = strings.TrimSuffix(info.ChannelBaseUrl, "/")
	}
	return base + chatCompletionsPath, nil
}

// SetupRequestHeader 由 DoRequest 内部构造（本 adaptor 不走 channel.DoApiRequest）。
func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	return nil
}

// ConvertOpenAIRequest 原样透传（payload 改写管线在 DoRequest 的字节层执行）。
func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("workbuddy channel: request is nil")
	}
	return request, nil
}

// DoRequest 完整出站流程：凭据刷新（单飞）→ payload 改写 → 上游 chat SSE。
// 上游 ≥400 时构造合成 *http.Response 返回（err=nil），由 relay 核心统一做
// 错误→状态码→重试/禁用映射；渠道健康副作用（硬冷却/禁用）在此按 12 层分类落地。
func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if a.cred == nil {
		return nil, errors.New("workbuddy channel: invalid credential JSON in channel key")
	}
	body, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, fmt.Errorf("workbuddy channel: read request body: %w", err)
	}
	// 请求前刷新（NeedsRefresh 10min → 单飞刷新 → 写回 channels.key + 缓存失效）。
	cred, err := RefreshIfNeeded(info.ChannelId, a.cred)
	if err != nil {
		return nil, fmt.Errorf("workbuddy channel: token refresh failed: %w", err)
	}
	a.cred = cred

	client := NewClient(cred)
	rc, status, respBody, chatErr := client.ChatStream(c.Request.Context(), body)
	if chatErr == nil && rc != nil {
		// 成功流：包装为 *http.Response 交给 DoResponse。
		// 关键：上游恒为 SSE（payload 强制 stream:true），但 relay 核心按
		// Content-Type 自动翻转 info.IsStream——只有客户端真要流式时才标
		// event-stream，否则标 application/json，让 DoResponse 走聚合路径。
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
		// 传输层错误：不作为上游错误处理（不罚渠道）。
		return nil, fmt.Errorf("workbuddy channel: upstream transport: %w", chatErr)
	}
	// 上游业务错误（≥400 或 200 信封 code!=0 的 chat 场景已在上游展开为 ≥400）。
	var ue *Error
	if errors.As(chatErr, &ue) {
		a.applyChannelHealth(info, ue)
	}
	if chatErr == nil {
		chatErr = fmt.Errorf("workbuddy upstream status %d", status)
	}
	// 合成错误响应：relay 核心 RelayErrorHandler 按状态码产出 NewAPIError。
	header := http.Header{"Content-Type": []string{"application/json"}}
	errResp := &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}
	return errResp, nil
}

// applyChannelHealth 12 层错误分类 → new-api 渠道禁用/冷却语义。
// 只处理「账号级」终态；请求级/模型级/容量级错误不罚渠道（交给重试与客户端）。
func (a *Adaptor) applyChannelHealth(info *relaycommon.RelayInfo, ue *Error) {
	if info == nil || info.ChannelId <= 0 {
		return
	}
	var reason string
	switch ue.Kind {
	case ErrHardCredit:
		// 402/14018/余额词 → 冷却（签到后 ReenableIfCredits 解冻）。
		reason = "workbuddy credits exhausted (402/14018), will re-enable after checkin: " + ue.Msg
	case ErrAccountFault:
		// 11140 request illegal / 14017 trial not activated → 禁用（账号级故障等不来自愈）。
		reason = "workbuddy account fault: " + ue.Msg
	case ErrSessionDead:
		// 12153/401 session 失效 → 显式禁用（「需重新登录」终态）。
		// 不依赖全局 AutomaticDisableChannelEnabled 开关：这是账号级语义。
		reason = "workbuddy session dead (12153), re-login required: " + ue.Msg
	default:
		return
	}
	ch, err := model.GetChannelById(info.ChannelId, true)
	if err != nil || ch == nil {
		return
	}
	if !ch.GetAutoBan() {
		return
	}
	common.SysLog(fmt.Sprintf("workbuddy channel #%d auto action (%s): %s", info.ChannelId, ue.Kind, reason))
	model.UpdateChannelStatus(info.ChannelId, "", common.ChannelStatusAutoDisabled, truncate(reason, 200))
}

// DoResponse 处理上游 SSE：流式走白名单重建逐帧透出，非流式聚合为单帧。
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(errors.New("workbuddy channel: empty upstream response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer resp.Body.Close()
	if info.IsStream {
		return a.streamResponse(c, resp, info)
	}
	return a.aggregateResponse(c, resp, info)
}

// streamResponse 流式：逐帧 normalizeFrame 白名单重建 + tool_call name 收敛 +
// id 续传；末帧 usage 提取（ensureUsageTotal + 缓存别名归一）。
func (a *Adaptor) streamResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	var (
		usageMap      map[string]any
		responseText  strings.Builder
		toolCallSeen  = map[int]bool{}
		firstID       string
		sawValidFrame bool
		streamErr     *Error
	)
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if data == "" || data == "[DONE]" {
			return
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			return // 非 JSON 帧静默跳过（与上游实现一致）
		}
		// 上游 200 + error 帧（6004 限流/内容拦截/审核）：透传原帧并按分类处置渠道。
		if errField, ok := obj["error"]; ok && errField != nil {
			raw, _ := json.Marshal(errField)
			kind := Classify(http.StatusOK, string(raw))
			if kind != ErrNone && kind != ErrClient {
				streamErr = &Error{Kind: kind, Status: http.StatusOK, Msg: truncate(string(raw), 200)}
			}
			_ = helper.StringData(c, data)
			return
		}
		sawValidFrame = true
		// tool_calls name 收敛（每 index 仅首片带 name）。
		stripToolCallNames(obj, toolCallSeen)
		// id 续传：首帧非空真实 id 缓存，后续缺 id/空 id 帧复用。
		if firstID == "" {
			if v, ok := obj["id"].(string); ok && v != "" {
				firstID = v
			}
		} else if v, ok := obj["id"].(string); !ok || v == "" {
			obj["id"] = firstID
		}
		// 正文聚合（兜底 usage 估算用）。
		if chs, ok := obj["choices"].([]any); ok {
			for _, ci := range chs {
				cc, _ := ci.(map[string]any)
				if cc == nil {
					continue
				}
				if d, ok := cc["delta"].(map[string]any); ok {
					if txt, ok := d["content"].(string); ok {
						responseText.WriteString(txt)
					}
				}
			}
		}
		if u, ok := obj["usage"].(map[string]any); ok && len(u) > 0 {
			usageMap = u
		}
		normalized := normalizeFrame(obj)
		if err := helper.ObjectData(c, normalized); err != nil {
			sr.Error(err)
		}
	})
	if streamErr != nil {
		a.applyChannelHealth(info, streamErr)
		return nil, types.NewOpenAIError(streamErr, types.ErrorCodeBadResponse, http.StatusTooManyRequests)
	}
	if !sawValidFrame {
		return nil, types.NewOpenAIError(errEmptyStream, types.ErrorCodeEmptyResponse, http.StatusBadGateway)
	}
	helper.Done(c)
	return buildUsage(usageMap, responseText.String(), info), nil
}

// aggregateResponse 非流式：聚合上游 SSE 为单个 chat.completion JSON。
func (a *Adaptor) aggregateResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	full, err := Aggregate(resp.Body)
	if err != nil {
		if IsEmptyStreamError(err) {
			return nil, types.NewOpenAIError(err, types.ErrorCodeEmptyResponse, http.StatusBadGateway)
		}
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	if model, ok := full["model"].(string); !ok || model == "" {
		full["model"] = info.UpstreamModelName
	}
	raw, err := json.Marshal(full)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	if _, err := c.Writer.Write(raw); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	usage, _ := full["usage"].(map[string]any)
	return buildUsage(usage, "", info), nil
}

// buildUsage 从上游 usage map 构造 dto.Usage；缺失时按回传正文估算。
func buildUsage(usageMap map[string]any, fallbackText string, info *relaycommon.RelayInfo) *dto.Usage {
	usage := &dto.Usage{}
	if usageMap != nil {
		usageMap = ensureUsageTotal(usageMap)
		if v, ok := num64(usageMap["prompt_tokens"]); ok {
			usage.PromptTokens = int(v)
		}
		if v, ok := num64(usageMap["completion_tokens"]); ok {
			usage.CompletionTokens = int(v)
		}
		if v, ok := num64(usageMap["total_tokens"]); ok {
			usage.TotalTokens = int(v)
		}
	}
	if usage.TotalTokens == 0 && usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		// 上游末帧未给 usage：按正文估算（与 openai handler 兜底口径一致）。
		if fallbackText != "" && info != nil {
			est := estimateTokens(fallbackText)
			usage.CompletionTokens = est
			usage.PromptTokens = info.GetEstimatePromptTokens()
			usage.TotalTokens = usage.PromptTokens + est
		}
	}
	return usage
}

// estimateTokens 粗略 token 估算（3.5 字节/token 的保守口径）。
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	n := len(s)/4 + 1
	if n < 1 {
		n = 1
	}
	return n
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
// 不支持的端点（账号型渠道只接 chat completions）
// ---------------------------------------------------------------------------

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("workbuddy channel: /v1/rerank endpoint not supported")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("workbuddy channel: /v1/embeddings endpoint not supported")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("workbuddy channel: audio endpoint not supported")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("workbuddy channel: image endpoint not supported")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("workbuddy channel: /v1/responses endpoint not supported")
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("workbuddy channel: /v1/messages endpoint not supported")
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("workbuddy channel: gemini endpoint not supported")
}

// ---------------------------------------------------------------------------
// 渠道级扩展（登录/导入/签到中心调用）
// ---------------------------------------------------------------------------

// FetchUpstreamModels 拉取账号实时模型目录（controller fetch_models 用）。
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

// RefreshBalance 查询账号余额并写渠道 Balance 字段（签到中心/手动刷新用）。
// 返回 (remain, total)。channelId>0 时写库。
func RefreshBalance(channelId int, cred *Cred) (remain, total int64, err error) {
	remain, total, err = NewClient(cred).UserResource()
	if err != nil {
		return 0, 0, err
	}
	if channelId > 0 {
		if ch, cerr := model.GetChannelById(channelId, true); cerr == nil && ch != nil {
			ch.UpdateBalance(float64(remain))
		}
	}
	return remain, total, nil
}

var _ channel.Adaptor = (*Adaptor)(nil)

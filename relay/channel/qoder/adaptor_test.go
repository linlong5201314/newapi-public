package qoder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ channel.Adaptor = (*Adaptor)(nil)

func TestQoderNativeTextBoundary(t *testing.T) {
	zero := uint(0)
	temp := 0.0
	req := &dto.GeneralOpenAIRequest{Model: "real-catalog-key", MaxTokens: &zero, Temperature: &temp, Messages: []dto.Message{{Role: "system", Content: "Keep original system instructions"}, {Role: "user", Content: "hello"}}}
	cred := &service.QoderCredential{UserID: "owner42", UserType: "personal_standard"}
	body, err := nativeTextBody(req, cred, service.QoderModel{Key: req.Model, DisplayName: "Live catalog name", MaxInputTokens: 12345})
	require.NoError(t, err)
	assert.Equal(t, "personal_standard", body["aliyun_user_type"])
	assert.Equal(t, true, body["stream"])
	assert.Empty(t, body["tools"])
	params := body["parameters"].(map[string]any)
	assert.Equal(t, zero, params["max_tokens"])
	assert.Equal(t, temp, params["temperature"])
	assert.NotContains(t, params, "top_p")
	messages := body["messages"].([]any)
	require.Len(t, messages, 2)
	assert.Equal(t, "Keep original system instructions", messages[0].(map[string]any)["content"])
	user := messages[1].(map[string]any)
	assert.Equal(t, "", user["content"])
	assert.Equal(t, []any{map[string]any{"type": "text", "text": "hello"}}, user["contents"])
	assert.Equal(t, int64(12345), body["model_config"].(map[string]any)["max_input_tokens"])
	huge := uint(math.MaxUint32)
	req.MaxTokens = &huge
	assert.Error(t, validateTextRequest(req))
	req.MaxTokens = nil
	req.Tools = []dto.ToolCallRequest{{Type: "function"}}
	assert.ErrorIs(t, validateTextRequest(req), errUnsupported)
	req.Tools = nil
	req.Messages[1].Content = []any{map[string]any{"type": "image_url"}}
	assert.ErrorIs(t, validateTextRequest(req), errUnsupported)
}

func nativeFrame(t *testing.T, inner string, status int) string {
	t.Helper()
	raw, err := common.Marshal(map[string]any{"body": inner, "statusCodeValue": status})
	require.NoError(t, err)
	return "data:" + string(raw) + "\n\n"
}
func TestQoderStreamNativeEnvelope(t *testing.T) {
	first := `{"id":"upstream-id","object":"chat.completion.chunk","model":"resolved-live","choices":[{"index":0,"delta":{"content":"hello","reasoning_content":"reason"}}]}`
	tail := `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":80},"completion_tokens_details":{"reasoning_tokens":7},"usage_semantic":"not-trusted","billing_usage":{"input_tokens":999999}}}`
	input := nativeFrame(t, first, 200) + nativeFrame(t, tail, 200) + nativeFrame(t, "[DONE]", 200) + "event:finish{}\n"
	reader := newNativeStream(io.NopCloser(strings.NewReader(input)))
	result, err := aggregateNative(reader, "requested")
	require.NoError(t, err)
	require.Len(t, result.Choices, 1)
	assert.Equal(t, "hello", result.Choices[0].Content)
	require.NotNil(t, result.Choices[0].ReasoningContent)
	assert.Equal(t, "reason", *result.Choices[0].ReasoningContent)
	assert.Equal(t, "resolved-live", result.Model)
	assert.Equal(t, "stop", result.Choices[0].FinishReason)
	assert.Equal(t, 100, result.Usage.PromptTokens)
	assert.Equal(t, 20, result.Usage.CompletionTokens)
	assert.Equal(t, 80, result.Usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 7, result.Usage.CompletionTokenDetails.ReasoningTokens)
	assert.Empty(t, result.Usage.UsageSemantic)
	assert.Nil(t, result.Usage.BillingUsage)
	stream := newNativeStream(io.NopCloser(strings.NewReader(input)))
	wire, err := io.ReadAll(stream)
	require.NoError(t, err)
	assert.Contains(t, string(wire), "data: [DONE]\n\n")
	assert.NotContains(t, string(wire), "statusCodeValue")
	assert.NotContains(t, string(wire), "not-trusted")
}

func TestQoderStreamFailsClosed(t *testing.T) {
	stop := nativeFrame(t, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, 200)
	for _, tc := range []struct {
		name, input string
		want        error
	}{
		{"truncated-after-stop", stop, service.ErrAccountProtocol},
		{"done-without-finish", nativeFrame(t, "[DONE]", 200), service.ErrAccountProtocol},
		{"late-error-not-hidden", stop + nativeFrame(t, "token-secret", 403), service.ErrAccountUpstream},
		{"rejection-without-body", "data: {\"statusCodeValue\":403}\n\n", service.ErrAccountUpstream},
		{"rejection-with-null-body", "data: {\"statusCodeValue\":403,\"body\":null}\n\n", service.ErrAccountUpstream},
		{"inner-error", nativeFrame(t, `{"error":{"message":"token-secret"}}`, 200), service.ErrAccountUpstream},
		{"malformed", "data:{invalid\n\n", service.ErrAccountProtocol},
		{"negative-usage", nativeFrame(t, `{"usage":{"prompt_tokens":-1}}`, 200), service.ErrAccountProtocol},
		{"absurd-usage", nativeFrame(t, `{"usage":{"prompt_tokens":9223372036854775807}}`, 200), service.ErrAccountProtocol},
		{"unsupported-tool-return", nativeFrame(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"id":"call","type":"function","function":{"name":"exec"}}]}}]}`, 200), service.ErrAccountProtocol},
		{"oversized-frame", "data:" + strings.Repeat("x", (1<<20)+1) + "\n", service.ErrAccountProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newNativeStream(io.NopCloser(strings.NewReader(tc.input)))
			_, err := io.ReadAll(r)
			require.ErrorIs(t, err, tc.want)
			assert.NotContains(t, err.Error(), "token-secret")
		})
	}
}

type qoderTrackedBody struct {
	*bytes.Reader
	closed bool
}

func (b *qoderTrackedBody) Close() error { b.closed = true; return nil }
func TestQoderAdapterDestinationAndClose(t *testing.T) {
	a := &Adaptor{}
	raw, err := common.Marshal(service.QoderCredential{Realm: "global"})
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, RelayMode: relayconstant.RelayModeChatCompletions}
	info.ChannelType = constant.ChannelTypeQoder
	info.ApiKey = string(raw)
	target, err := a.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api3.qoder.sh"+service.QoderInferencePath, target)
	info.ChannelBaseUrl = "https://evil.invalid"
	_, err = a.GetRequestURL(info)
	assert.Error(t, err)
	info.ChannelBaseUrl = ""
	info.RelayMode = relayconstant.RelayModeResponses
	_, err = a.GetRequestURL(info)
	assert.Error(t, err)
	body := &qoderTrackedBody{Reader: bytes.NewReader(nil)}
	r := newNativeStream(body)
	require.NoError(t, r.Close())
	assert.True(t, body.closed)
	assert.True(t, containsModel("one,two", "two"))
	assert.False(t, containsModel("one,two", "tw"))
	assert.Equal(t, "", http.Header{}.Get("Cookie"))
}

func TestQoderQueueNoticeNestedEnvelope(t *testing.T) {
	// Real shape captured from the live provider: the native body is stringified
	// JSON whose message is stringified again; isQueued lives innermost.
	body := `{"code":"403","message":"{\"code\":\"10605\",\"message\":\"{\\\"errorCode\\\":0,\\\"isQueued\\\":true,\\\"modelKey\\\":\\\"auto\\\",\\\"queueCount\\\":1,\\\"queueType\\\":\\\"p4\\\",\\\"retryAfterSeconds\\\":60,\\\"serviceAvailable\\\":true,\\\"subCode\\\":\\\"RESOURCE_LIMIT\\\",\\\"waitTime\\\":60}\",\"subCode\":\"RESOURCE_LIMIT\"}"}`
	require.True(t, qoderQueueNotice(body))

	require.False(t, qoderQueueNotice(`{"code":"403","message":"{\"code\":\"10605\",\"message\":\"{\\\"isQueued\\\":false,\\\"subCode\\\":\\\"RESOURCE_LIMIT\\\"}\",\"subCode\":\"RESOURCE_LIMIT\"}"}`))
	require.False(t, qoderQueueNotice(`{"code":"403","message":"ordinary rejection"}`))
	require.False(t, qoderQueueNotice(""))
}

func TestQoderQueuedResponseDoesNotCommitStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			body := &qoderTrackedBody{Reader: bytes.NewReader([]byte(nativeFrame(t, `{"isQueued":true,"subCode":"RESOURCE_LIMIT"}`, 403)))}
			response := &http.Response{StatusCode: http.StatusOK, Body: body, Header: http.Header{}}
			info := &relaycommon.RelayInfo{IsStream: stream, ChannelMeta: &relaycommon.ChannelMeta{}}
			info.UpstreamModelName = "auto"
			_, apiError := (&Adaptor{}).DoResponse(c, response, info)
			require.ErrorIs(t, apiError, service.ErrQoderQueued)
			assert.Equal(t, types.ErrorCode("qoder_queued"), apiError.GetErrorCode())
			assert.Equal(t, http.StatusTooManyRequests, apiError.StatusCode)
			assert.True(t, types.IsSkipRetryError(apiError))
			assert.False(t, c.Writer.Written(), "queued replies must not commit SSE success headers")
			assert.Empty(t, recorder.Body.String())
			assert.True(t, body.closed)
			outcome := service.ClassifyModelProbeError(context.Background(), apiError)
			assert.Equal(t, "unknown", outcome.Status)
			assert.Equal(t, "rate_limited", outcome.Reason)
		})
	}
}

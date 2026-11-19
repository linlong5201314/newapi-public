package zcode

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZCodeAdapterProtocolBoundary(t *testing.T) {
	a := &Adaptor{}
	for _, mode := range []int{relayconstant.RelayModeChatCompletions, relayconstant.RelayModeResponses, relayconstant.RelayModeUnknown} {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeZCode}, RelayMode: mode, RelayFormat: types.RelayFormatClaude}
		target, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, service.ZCodeStartBase+"/v1/messages", target)
		info.ChannelBaseUrl = "https://attacker.invalid"
		_, err = a.GetRequestURL(info)
		require.Error(t, err)
	}
	_, err := a.GetRequestURL(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeZCode}, RelayMode: relayconstant.RelayModeEmbeddings})
	require.Error(t, err)
	require.Empty(t, a.GetModelList(), "unauthed global catalog must not become account capabilities")
}

func TestZCodeNativeResponseCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	jsonBody := `{"id":"msg_fixture","type":"message","role":"assistant","model":"GLM-5.3-Flash","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`
	stream := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"GLM-5.3-Flash","content":[],"usage":{"input_tokens":3,"output_tokens":1}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n\n")
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, streaming := range []bool{false, true} {
			name := string(format)
			if streaming {
				name += "-stream"
			}
			t.Run(name, func(t *testing.T) {
				rr := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rr)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeZCode, UpstreamModelName: "GLM-5.3-Flash"}, RelayFormat: format, OriginModelName: "GLM-5.3-Flash", IsStream: streaming, DisablePing: true}
				body := jsonBody
				if streaming {
					body = stream
				}
				res := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
				usage, err := (&Adaptor{}).DoResponse(c, res, info)
				require.Nil(t, err)
				require.NotNil(t, usage)
				u, ok := usage.(*dto.Usage)
				require.True(t, ok)
				assert.Equal(t, 3, u.PromptTokens)
				assert.Equal(t, 2, u.CompletionTokens)
				assert.Contains(t, rr.Body.String(), "hello")
				if streaming {
					require.NotNil(t, info.StreamStatus)
					assert.False(t, info.StreamStatus.HasErrors(), info.StreamStatus.Summary())
				}
			})
		}
	}
}

func TestZCodeConvertsChatToNativeMessages(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeZCode, UpstreamModelName: "GLM-5.3-Flash"}, RelayFormat: types.RelayFormatOpenAI, OriginModelName: "GLM-5.3-Flash"}
	request := &dto.GeneralOpenAIRequest{Model: "GLM-5.3-Flash", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	result, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	native, ok := result.(*dto.ClaudeRequest)
	require.True(t, ok)
	assert.Equal(t, "GLM-5.3-Flash", native.Model)
	require.NotEmpty(t, native.Messages)
}

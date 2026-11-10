// adaptor_test.go Trae adaptor 的 DoResponse 回归测试：
// 覆盖 http.Response.Body 最后一帧同时返回 (n>0, io.EOF) 的真实传输形态
// （曾经把整块缓冲当一行解析导致 empty_response）。
package trae

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

// eofBody 模拟 http 传输层 body：一次 Read 返回全部数据 + io.EOF（合法 io.Reader 形态）。
type eofBody struct {
	data []byte
	done bool
}

func (b *eofBody) Read(p []byte) (int, error) {
	if b.done {
		return 0, io.EOF
	}
	b.done = true
	return copy(p, b.data), io.EOF // 关键：n>0 与 EOF 同帧
}

func (b *eofBody) Close() error { return nil }

const probeSSEBody = "event:metadata\ndata:{\"model\":\"\",\"session_id\":\"s1\"}\n\n" +
	"event:output\ndata:{\"response\":\"trae-mock-response\",\"reasoning_content\":\"\",\"tool_calls\":null}\n\n" +
	"event:token_usage\ndata:{\"prompt_tokens\":21,\"completion_tokens\":7,\"total_tokens\":28}\n\n" +
	"event:notify_usage\ndata:{\"cn_credits_remain_info\":{\"ide_credits\":4321}}\n\n" +
	"event:done\ndata:{\"finish_reason\":\"stop\"}\n\n"

func newTestGin() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

func newTestInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "trae-glm"}}
}

func TestStreamResponseWithEarlyEOF(t *testing.T) {
	c, w := newTestGin()
	resp := &http.Response{StatusCode: 200, Body: &eofBody{data: []byte(probeSSEBody)}}
	a := &Adaptor{}
	usage, apiErr := a.streamResponse(c, resp, newTestInfo())
	if apiErr != nil {
		t.Fatalf("apiErr: %v", apiErr)
	}
	body := w.Body.String()
	if !strings.Contains(body, "trae-mock-response") {
		t.Fatalf("content missing: %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("DONE missing: %s", body)
	}
	if usage == nil || usage.TotalTokens != 28 {
		t.Fatalf("usage wrong: %+v", usage)
	}
}

func TestAggregateResponseExtractsUsageAndCredits(t *testing.T) {
	c, w := newTestGin()
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(probeSSEBody))}
	a := &Adaptor{}
	usage, apiErr := a.aggregateResponse(c, resp, newTestInfo())
	if apiErr != nil {
		t.Fatalf("apiErr: %v", apiErr)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"trae-mock-response"`) {
		t.Fatalf("content missing: %s", body)
	}
	if !strings.Contains(body, `"total_tokens":28`) {
		t.Fatalf("usage missing in aggregate: %s", body)
	}
	if usage == nil || usage.TotalTokens != 28 {
		t.Fatalf("usage wrong: %+v", usage)
	}
}

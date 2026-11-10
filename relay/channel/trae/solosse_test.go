// solosse_test.go Trae SOLO SSE 事件翻译 + notify_usage 提取单测。
package trae

import (
	"strings"
	"testing"
)

const soloStream = `id:1
event:metadata
data:{"model":"","session_id":"s1"}

event:timing_cost
data:{"name":"llm_raw_chat_v2"}

event:output
data:{"response":"Hello","reasoning_content":"thinking...","tool_calls":null}

event:output
data:{"response":" world","reasoning_content":"","tool_calls":null}

event:extra_info
data:{"foo":"bar"}

event:token_usage
data:{"prompt_tokens":21,"completion_tokens":142,"total_tokens":163,"reasoning_tokens":135}

event:notify_usage
data:{"cn_credits_remain_info":{"ide_credits":4321,"other":1},"other_usage":{}}

event:done
data:{"finish_reason":"stop"}

`

func TestParseSOLOLine(t *testing.T) {
	ev, err := ParseSOLOLine("output", `{"response":"hi","reasoning_content":"r","tool_calls":null}`)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Event != "output" || ev.Response != "hi" || ev.Reasoning != "r" {
		t.Fatalf("output parse wrong: %+v", ev)
	}
	ev, err = ParseSOLOLine("notify_usage", `{"cn_credits_remain_info":{"ide_credits":100}}`)
	if err != nil {
		t.Fatal(err)
	}
	if ev.NotifyUsage == nil {
		t.Fatalf("notify_usage not captured: %+v", ev)
	}
}

func TestExtractIdeCredits(t *testing.T) {
	credits, ok := ExtractIdeCredits(map[string]any{
		"cn_credits_remain_info": map[string]any{"ide_credits": float64(4321)},
	})
	if !ok || credits != 4321 {
		t.Fatalf("ide_credits extract failed: %d %v", credits, ok)
	}
	if _, ok := ExtractIdeCredits(map[string]any{"other": 1}); ok {
		t.Fatalf("false positive ide_credits")
	}
	if _, ok := ExtractIdeCredits(nil); ok {
		t.Fatalf("nil false positive")
	}
}

func TestAggregateSoloStream(t *testing.T) {
	full, res, err := Aggregate(strings.NewReader(soloStream))
	if err != nil {
		t.Fatal(err)
	}
	if !res.SawValid {
		t.Fatalf("no valid output seen")
	}
	if !res.HasIdeCred || res.IdeCredits != 4321 {
		t.Fatalf("notify_usage not extracted: %+v", res)
	}
	if res.Usage == nil || res.Usage["total_tokens"] != float64(163) {
		t.Fatalf("token_usage not captured: %+v", res.Usage)
	}
	choices := full["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "Hello world" {
		t.Fatalf("content aggregate wrong: %q", msg["content"])
	}
	if msg["reasoning_content"] != "thinking..." {
		t.Fatalf("reasoning aggregate wrong: %q", msg["reasoning_content"])
	}
	usage := full["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(21) {
		t.Fatalf("usage not in response: %+v", usage)
	}
}

func TestAggregateErrorFrame(t *testing.T) {
	stream := `event:error
data:{"code":1005,"message":"plan limit reached"}

`
	_, res, err := Aggregate(strings.NewReader(stream))
	if err == nil {
		t.Fatalf("error frame should surface")
	}
	se, ok := err.(*SOLOStreamError)
	if !ok || se.Code != 1005 {
		t.Fatalf("error frame not typed: %v", err)
	}
	if res.StreamError == nil || res.StreamError.Kind() != ErrPlanLimit {
		t.Fatalf("error kind wrong: %+v", res.StreamError)
	}
}

func TestStreamErrorKindMapping(t *testing.T) {
	cases := []struct {
		code int64
		want ErrKind
	}{
		{1001, ErrSessionDead},
		{1005, ErrPlanLimit},
		{4008, ErrQuotaExhausted},
		{4011, ErrSoftRate},
		{9074, ErrSoftRate},
		{9999, ErrClient},
	}
	for _, tc := range cases {
		se := &SOLOStreamError{Code: tc.code}
		if se.Kind() != tc.want {
			t.Fatalf("code %d kind = %v, want %v", tc.code, se.Kind(), tc.want)
		}
	}
}

func TestAggregateToolCalls(t *testing.T) {
	stream := `event:output
data:{"response":"","reasoning_content":"","tool_calls":[{"index":0,"id":"c1","function_call":{"name":"bash","arguments":"{\"cmd\":"}}]}

event:output
data:{"response":"","reasoning_content":"","tool_calls":[{"index":0,"function_call":{"arguments":"\"ls\"}"}}]}

event:done
data:{"finish_reason":"tool_calls"}

`
	full, _, err := Aggregate(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	msg := full["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	tcs, ok := msg["tool_calls"].([]map[string]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("tool_calls not merged: %+v", msg["tool_calls"])
	}
	fn := tcs[0]["function"].(map[string]any)
	if fn["name"] != "bash" || fn["arguments"] != `{"cmd":"ls"}` {
		t.Fatalf("tool_call merge wrong: %+v", fn)
	}
}

// payload_test.go WorkBuddy payload 改写管线单测：
// stream 强制 / tool_choice 归一 / max_completion_tokens→max_tokens 映射 /
// role developer→system / GPT max_tokens 下限钳制 / deepseek thinking 注入。
package workbuddy

import (
	"encoding/json"
	"testing"
)

func prepare(t *testing.T, in string) map[string]any {
	t.Helper()
	out := PrepareBody([]byte(in))
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("prepare output not json: %v (%s)", err, out)
	}
	return obj
}

func TestPrepareBodyForcesStream(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`)
	if obj["stream"] != true {
		t.Fatalf("stream not forced: %v", obj["stream"])
	}
	// 缺省补 stream_options.include_usage（末帧 usage 依据）
	so, ok := obj["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Fatalf("stream_options not injected: %v", obj["stream_options"])
	}
}

func TestPrepareBodyKeepsExplicitStreamOptions(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[],"stream_options":{"include_usage":false}}`)
	so, _ := obj["stream_options"].(map[string]any)
	if so["include_usage"] != false {
		t.Fatalf("explicit stream_options overwritten: %v", so)
	}
}

func TestTranslateMaxCompletionTokens(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[],"max_completion_tokens":128000}`)
	if _, has := obj["max_completion_tokens"]; has {
		t.Fatalf("alias not removed: %v", obj)
	}
	if obj["max_tokens"] != float64(128000) {
		t.Fatalf("max_tokens not translated: %v", obj["max_tokens"])
	}
}

func TestTranslateMaxCompletionTokensExplicitWins(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[],"max_tokens":64,"max_completion_tokens":128000}`)
	if obj["max_tokens"] != float64(64) {
		t.Fatalf("explicit max_tokens overwritten: %v", obj["max_tokens"])
	}
}

func TestTranslateMaxCompletionTokensNonPositiveDropped(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[],"max_completion_tokens":0}`)
	if _, has := obj["max_tokens"]; has {
		t.Fatalf("non-positive alias translated: %v", obj["max_tokens"])
	}
}

func TestClampGPTMinMaxTokens(t *testing.T) {
	obj := prepare(t, `{"model":"gpt-6-sol","messages":[],"max_tokens":4}`)
	if obj["max_tokens"] != float64(16) {
		t.Fatalf("gpt max_tokens not clamped: %v", obj["max_tokens"])
	}
	// 非 GPT 模型不钳
	obj = prepare(t, `{"model":"glm-5.2","messages":[],"max_tokens":4}`)
	if obj["max_tokens"] != float64(4) {
		t.Fatalf("non-gpt max_tokens clamped: %v", obj["max_tokens"])
	}
}

func TestNormalizeToolChoiceNone(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[],"tool_choice":"none","tools":[{"type":"function","function":{"name":"f"}}]}`)
	if _, has := obj["tool_choice"]; has {
		t.Fatalf("tool_choice none not removed: %v", obj)
	}
	if _, has := obj["tools"]; has {
		t.Fatalf("tools not suppressed on none: %v", obj)
	}
}

func TestNormalizeToolChoiceObject(t *testing.T) {
	// {"type":"function","function":{"name":"bash"}} → 字符串 "bash"
	obj := prepare(t, `{"model":"glm-5.2","messages":[],"tool_choice":{"type":"function","function":{"name":"bash"}}}`)
	if obj["tool_choice"] != "bash" {
		t.Fatalf("function tool_choice not flattened: %v", obj["tool_choice"])
	}
	// {"type":"auto"} → "auto"
	obj = prepare(t, `{"model":"glm-5.2","messages":[],"tool_choice":{"type":"auto"}}`)
	if obj["tool_choice"] != "auto" {
		t.Fatalf("auto tool_choice not flattened: %v", obj["tool_choice"])
	}
}

func TestNormalizeRolesDeveloperToSystem(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[{"role":"developer","content":"be terse"},{"role":"user","content":"hi"}]}`)
	msgs := obj["messages"].([]any)
	first := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("developer not normalized to system: %v", first["role"])
	}
	second := msgs[1].(map[string]any)
	if second["role"] != "user" {
		t.Fatalf("user role mutated: %v", second["role"])
	}
}

func TestNormalizeImageURLStringToObject(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[{"role":"user","content":[{"type":"image_url","image_url":"data:image/png;base64,AAA"}]}]}`)
	part := obj["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	iu, ok := part["image_url"].(map[string]any)
	if !ok || iu["url"] != "data:image/png;base64,AAA" {
		t.Fatalf("image_url not objectified: %v", part["image_url"])
	}
}

func TestDropReasoningContentParts(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[{"role":"assistant","content":[{"type":"reasoning_text","text":"..."},{"type":"text","text":"answer"}]}]}`)
	parts := obj["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(parts) != 1 {
		t.Fatalf("reasoning part not dropped: %v", parts)
	}
	if parts[0].(map[string]any)["type"] != "text" {
		t.Fatalf("wrong part kept: %v", parts[0])
	}
}

func TestInjectThinkingDeepSeek(t *testing.T) {
	obj := prepare(t, `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}]}`)
	th, ok := obj["thinking"].(map[string]any)
	if !ok || th["type"] != "enabled" {
		t.Fatalf("thinking not injected: %v", obj["thinking"])
	}
	if obj["reasoning_effort"] != "high" {
		t.Fatalf("default effort not injected: %v", obj["reasoning_effort"])
	}
	// 非 deepseek 模型零改动
	obj = prepare(t, `{"model":"glm-5.2","messages":[]}`)
	if _, has := obj["thinking"]; has {
		t.Fatalf("thinking injected for non-deepseek: %v", obj["thinking"])
	}
}

func TestInjectThinkingExplicitDisabled(t *testing.T) {
	obj := prepare(t, `{"model":"deepseek-v4.1-flash","messages":[],"thinking":{"type":"disabled"},"reasoning_effort":"high"}`)
	th := obj["thinking"].(map[string]any)
	if th["type"] != "disabled" {
		t.Fatalf("explicit disabled overwritten: %v", th)
	}
	if _, has := obj["reasoning_effort"]; has {
		t.Fatalf("effort not removed on disabled: %v", obj)
	}
}

func TestBackfillReasoningContent(t *testing.T) {
	obj := prepare(t, `{"model":"deepseek-v4.1-flash","messages":[{"role":"assistant","content":"answer"},{"role":"user","content":"next"}]}`)
	first := obj["messages"].([]any)[0].(map[string]any)
	if _, has := first["reasoning_content"]; !has {
		t.Fatalf("reasoning_content not backfilled: %v", first)
	}
	if first["reasoning"] != " " {
		t.Fatalf("reasoning mirror not backfilled: %v", first["reasoning"])
	}
}

func TestPrepareBodyPromptCacheKeySeparate(t *testing.T) {
	body := PrepareBody([]byte(`{"model":"glm-5.2","messages":[]}`))
	withKey := InjectPromptCacheKey(body, "uid-1234567890", "")
	var obj map[string]any
	if err := json.Unmarshal(withKey, &obj); err != nil {
		t.Fatal(err)
	}
	key, _ := obj["prompt_cache_key"].(string)
	if key == "" {
		t.Fatalf("cache key not injected: %v", obj)
	}
	// 同 uid 同会话稳定
	again := InjectPromptCacheKey(body, "uid-1234567890", "")
	var obj2 map[string]any
	_ = json.Unmarshal(again, &obj2)
	if obj2["prompt_cache_key"] != key {
		t.Fatalf("cache key not stable")
	}
	// 不同 uid 不碰撞（账号隔离红线）
	other := InjectPromptCacheKey(body, "uid-abcdefghij", "")
	var obj3 map[string]any
	_ = json.Unmarshal(other, &obj3)
	if obj3["prompt_cache_key"] == key {
		t.Fatalf("cache key leaked across accounts")
	}
}

func TestPrepareBodyBadJSONPassthrough(t *testing.T) {
	in := []byte("{not json")
	out := PrepareBody(in)
	if string(out) != string(in) {
		t.Fatalf("bad json not passthrough")
	}
}

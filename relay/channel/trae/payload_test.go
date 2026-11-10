// payload_test.go Trae payload 改写单测：
// content 字符串→数组 / function 注入（solo_work_lite）/ config_name+model 双填 /
// tools parameters 对象→JSON 字符串 / assistant function→function_call。
package trae

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

func TestPrepareBodyBasics(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`)
	if obj["stream"] != true {
		t.Fatalf("stream not forced")
	}
	if obj["function"] != "solo_work_lite" {
		t.Fatalf("function not injected: %v", obj["function"])
	}
	if obj["config_name"] != "glm-5.2" || obj["model"] != "glm-5.2" {
		t.Fatalf("config_name/model not double-filled: %v", obj)
	}
}

func TestContentStringToArray(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"world"}]}`)
	msgs := obj["messages"].([]any)
	for i, m := range msgs {
		arr, ok := m.(map[string]any)["content"].([]any)
		if !ok || len(arr) != 1 {
			t.Fatalf("msg %d content not array: %v", i, m)
		}
		part := arr[0].(map[string]any)
		if part["type"] != "text" {
			t.Fatalf("msg %d part type wrong: %v", i, part)
		}
	}
	if msgs[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] != "hello" {
		t.Fatalf("text content lost")
	}
}

func TestContentArrayPassthrough(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	arr := obj["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(arr) != 1 || arr[0].(map[string]any)["type"] != "text" {
		t.Fatalf("array content not passthrough: %v", arr)
	}
}

func TestEmptyModelDefaults(t *testing.T) {
	obj := prepare(t, `{"messages":[{"role":"user","content":"hi"}]}`)
	if obj["config_name"] != DefaultConfigName || obj["model"] != DefaultConfigName {
		t.Fatalf("default model not filled: %v", obj)
	}
}

func TestNormalizeToolsParametersStringify(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[],"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}}]}`)
	tools := obj["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	params, ok := fn["parameters"].(string)
	if !ok {
		t.Fatalf("parameters not stringified: %T %v", fn["parameters"], fn["parameters"])
	}
	var check map[string]any
	if err := json.Unmarshal([]byte(params), &check); err != nil || check["type"] != "object" {
		t.Fatalf("stringified parameters not valid json: %v", err)
	}
}

func TestAssistantFunctionToFunctionCall(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"bash","arguments":"{}"}}]}]}`)
	msg := obj["messages"].([]any)[0].(map[string]any)
	tcs := msg["tool_calls"].([]any)
	tc := tcs[0].(map[string]any)
	if _, has := tc["function"]; has {
		t.Fatalf("function not renamed: %v", tc)
	}
	fc, ok := tc["function_call"].(map[string]any)
	if !ok || fc["name"] != "bash" {
		t.Fatalf("function_call missing: %v", tc)
	}
}

func TestToolChoiceNoneSuppressesTools(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[],"tool_choice":"none","tools":[{"type":"function","function":{"name":"f"}}]}`)
	if _, has := obj["tool_choice"]; has {
		t.Fatalf("tool_choice none not removed")
	}
	if _, has := obj["tools"]; has {
		t.Fatalf("tools not suppressed")
	}
}

func TestToolChoiceFunctionFlattened(t *testing.T) {
	obj := prepare(t, `{"model":"glm-5.2","messages":[],"tool_choice":{"type":"function","function":{"name":"bash"}}}`)
	if obj["tool_choice"] != "bash" {
		t.Fatalf("tool_choice not flattened: %v", obj["tool_choice"])
	}
}

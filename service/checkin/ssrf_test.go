// ssrf_test.go SSRF 防护单测。
package checkin

import "testing"

func TestValidateExternalURL(t *testing.T) {
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"http://8.8.8.8:8080/run", false},
		{"https://agent.example.com/hook", true}, // 离线环境不可解析 → fail-closed（正确行为）
		{"ftp://example.com/x", true},
		{"file:///etc/passwd", true},
		{"http://127.0.0.1:18080/authorize", true},
		{"http://localhost/run", true},
		{"http://192.168.1.10/run", true},
		{"http://10.0.0.5/run", true},
		{"http://172.16.0.1/run", true},
		{"http://169.254.169.254/latest/meta-data", true}, // 云元数据
		{"http://100.64.0.1/run", true},                   // CGNAT
		{"http://0.0.0.0/run", true},
		{"https://example.internal/run", true},
		{"http://192.0.2.1/run", true}, // TEST-NET-1 文档段
		{"https://", true},
	}
	for _, tc := range cases {
		err := ValidateExternalURL(tc.url)
		if tc.wantErr && err == nil {
			t.Fatalf("ValidateExternalURL(%q) = nil, want error", tc.url)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("ValidateExternalURL(%q) = %v, want nil", tc.url, err)
		}
	}
}

func TestValidateExternalURLAllowPrivateEnv(t *testing.T) {
	t.Setenv("CHECKIN_SSRF_ALLOW_PRIVATE", "true")
	if err := ValidateExternalURL("http://127.0.0.1:12997/agent"); err != nil {
		t.Fatalf("env bypass should allow private host: %v", err)
	}
	// scheme 校验仍生效
	if err := ValidateExternalURL("file:///etc/passwd"); err == nil {
		t.Fatalf("env bypass must not allow non-http scheme")
	}
}

func TestJSONPathGet(t *testing.T) {
	doc := map[string]any{
		"code": float64(0),
		"data": map[string]any{
			"checked_in": true,
			"list":       []any{"a", "b"},
		},
	}
	if v, ok := jsonPathGet(doc, "code"); !ok || v != float64(0) {
		t.Fatalf("code path failed: %v %v", v, ok)
	}
	if v, ok := jsonPathGet(doc, "data.checked_in"); !ok || v != true {
		t.Fatalf("nested path failed: %v %v", v, ok)
	}
	if v, ok := jsonPathGet(doc, "data.list.1"); !ok || v != "b" {
		t.Fatalf("array index failed: %v %v", v, ok)
	}
	if _, ok := jsonPathGet(doc, "data.missing"); ok {
		t.Fatalf("missing path false negative")
	}
}

func TestJudgeCustomSuccess(t *testing.T) {
	cfg := customConfig{SuccessPath: "code", Expect: "0"}
	ok, _ := judgeCustomSuccess(cfg, []byte(`{"code":0,"msg":"ok"}`))
	if !ok {
		t.Fatalf("expect 0 not matched")
	}
	ok, _ = judgeCustomSuccess(cfg, []byte(`{"code":100,"msg":"fail"}`))
	if ok {
		t.Fatalf("expect 0 false positive")
	}
	// 真值判定（无 expect）
	cfg = customConfig{SuccessPath: "data.checked_in"}
	ok, _ = judgeCustomSuccess(cfg, []byte(`{"data":{"checked_in":true}}`))
	if !ok {
		t.Fatalf("truthy judge failed")
	}
	// 无 success_path：HTTP 2xx 即成功
	cfg = customConfig{}
	ok, _ = judgeCustomSuccess(cfg, []byte(`anything`))
	if !ok {
		t.Fatalf("empty path should pass")
	}
}

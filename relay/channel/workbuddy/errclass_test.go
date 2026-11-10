// errclass_test.go WorkBuddy 12 层错误分类映射表单测（判定顺序不可换）。
package workbuddy

import (
	"net/http"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   ErrKind
	}{
		// 0. 模型负缓存最先判（400/404 + 11102/11158）
		{"model blocked 11102", 400, `{"code":11102,"msg":"service info not found"}`, ErrModelBlocked},
		{"model forbidden 11158", 400, `{"code":11158,"msg":"model is not allowed for user type"}`, ErrModelBlocked},
		{"model blocked nested error", 404, `{"error":{"code":"11102","message":"service info not found"}}`, ErrModelBlocked},
		// 1. 402 余额耗尽
		{"402 hard credit", 402, `{"code":402,"msg":"payment required"}`, ErrHardCredit},
		// 2. session 失效（12153 精确词优先于限流宽泛词）
		{"12153 session dead", 401, `{"code":12153,"msg":"Offline user session not found"}`, ErrSessionDead},
		{"12153 with rate limit mixed", 401, `{"code":12153,"msg":"Offline user session not found, rate limit"}`, ErrSessionDead},
		// 3. 11128 通道校验失败（文案同 11140，code 分流）vs 11140 账号限制
		{"11128 channel refused", 403, `{"code":"11-128","msg":"request illegal"}`, ErrChannelRefused},
		{"11140 account restricted", 403, `{"code":11140,"msg":"request illegal"}`, ErrAccountFault},
		{"14017 trial not activated", 429, `{"code":14017,"msg":"The trial version is not yet activated"}`, ErrAccountFault},
		// 4. 429+14018 积分耗尽（先于通用 429）
		{"429 14018 hard credit", 429, `{"code":14018,"msg":"credits exhausted"}`, ErrHardCredit},
		// 5. 429+6021 满载（先于通用 429）
		{"429 6021 upstream busy", 429, `{"code":6021,"msg":"queue.full.title"}`, ErrUpstreamBusy},
		{"queue full text any status", 200, `{"code":6021,"msg":"queue full"}`, ErrUpstreamBusy},
		// 6. 429 兜底软限流（先于 hardMarkers——429 body 常带 quota 措辞）
		{"429 soft rate with quota words", 429, `{"code":429,"msg":"quota exceeded"}`, ErrSoftRate},
		{"429 plain", 429, `{"code":429,"msg":"too many requests"}`, ErrSoftRate},
		// 7. hardMarkers（非 429 携带计费词；含 200 业务信封）
		{"200 envelope credit words", 200, `{"code":10001,"msg":"积分不足"}`, ErrHardCredit},
		{"403 envelope credit words", 403, `{"code":100,"msg":"insufficient credit"}`, ErrHardCredit},
		// 8. softRateMarkers（非 429 限流文案；11140 限流变体不归 account fault）
		{"11140 rate limiting variant", 200, `{"code":11140,"msg":"The model provider is rate-limiting requests."}`, ErrSoftRate},
		{"400 rate limit words", 400, `{"code":100,"msg":"rate limit reached"}`, ErrSoftRate},
		// 9. 11115 prompt too long（先于 404 兜底）
		{"404 11115 prompt too long", 404, `{"code":11115,"msg":"prompt is too long"}`, ErrPromptTooLong},
		{"400 prompt too long text", 400, `{"msg":"prompt is too long"}`, ErrPromptTooLong},
		// 10. 404 / 5xx
		{"404 plain", 404, `{"code":404,"msg":"not found"}`, ErrNotFound},
		{"500 server", 500, `{"code":500,"msg":"internal error"}`, ErrServer},
		{"502 server", 502, `bad gateway`, ErrServer},
		// 11. WAF 403（无业务信封）
		{"403 waf html", 403, `<html><body>Forbidden</body></html>`, ErrWafBlock},
		{"403 waf empty", 403, ``, ErrWafBlock},
		// 12. 图片 / 内容策略 / 参数错误 / 其他 4xx
		{"400 invalid image code", 400, `{"code":11135,"msg":"invalid image data"}`, ErrImageInvalid},
		{"400 invalid image text", 400, `{"msg":"Parse message failed: invalid image_url content"}`, ErrImageInvalid},
		{"400 content blocked", 400, `{"code":100,"msg":"blocked by security policy"}`, ErrContentBlocked},
		{"400 bad params 11101", 400, `{"code":11101,"msg":"Unmarshal chat params failed: invalid character"}`, ErrBadParams},
		{"400 plain client", 400, `{"code":100,"msg":"some error"}`, ErrClient},
		// 成功
		{"200 ok", 200, `{"code":0,"msg":"OK","data":{}}`, ErrNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.status, tc.body); got != tc.want {
				t.Fatalf("Classify(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

// TestClassifyOrderGuards 关键判定序回归：任何一层错位都会在这组用例上显形。
func TestClassifyOrderGuards(t *testing.T) {
	// 429+11102 仍属限流语义（IsModelBlocked 只认 400/404）
	if got := Classify(429, `{"code":11102,"msg":"service info not found"}`); got != ErrSoftRate {
		t.Fatalf("429+11102 = %v, want soft_rate", got)
	}
	// 403 带业务信封不是 WAF（11140 归 account_fault）
	if got := Classify(403, `{"code":11140,"msg":"request illegal"}`); got != ErrAccountFault {
		t.Fatalf("403 envelope = %v, want account_fault", got)
	}
	// 429+14017 是账号级故障不是软限流
	if got := Classify(429, `{"code":14017,"msg":"trial not activated"}`); got != ErrAccountFault {
		t.Fatalf("429+14017 = %v, want account_fault", got)
	}
}

func TestIsAlreadyCheckin(t *testing.T) {
	if IsAlreadyCheckin(&Error{Kind: ErrClient, Msg: "code=10001 msg=今天已签到"}) != true {
		t.Fatalf("already checkin not detected")
	}
	if IsAlreadyCheckin(&Error{Kind: ErrClient, Msg: "code=100 msg=other"}) != false {
		t.Fatalf("false positive already checkin")
	}
	// 网络层错误不得当幂等成功
	if IsAlreadyCheckin(http.ErrHijacked) != false {
		t.Fatalf("transport error misdetected as already checkin")
	}
}

func TestParseRateReset(t *testing.T) {
	if ts, ok := ParseRateReset(`{"msg":"将在 2026-11-11 04:00:00 重置"}`); !ok || ts.Hour() != 4 {
		t.Fatalf("CN reset parse failed: %v %v", ts, ok)
	}
	if _, ok := ParseRateReset(`{"msg":"reset at 2026-11-11 04:00:00 UTC+8"}`); !ok {
		t.Fatalf("EN reset parse failed")
	}
	if _, ok := ParseRateReset(`{"msg":"no reset time"}`); ok {
		t.Fatalf("bogus reset parsed")
	}
}

func TestIsModelRateLimit(t *testing.T) {
	if !IsModelRateLimit(`{"code":6004,"msg":"将在 04:00 重置"}`) {
		t.Fatalf("6004 not detected")
	}
	if IsModelRateLimit(`{"code":6005,"msg":"other"}`) {
		t.Fatalf("6005 false positive")
	}
}

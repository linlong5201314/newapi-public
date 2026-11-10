// credimport_test.go Trae 凭据导入解析单测（回调链接两种形态 + userJwt 兜底）。
package trae

import (
	"net/url"
	"testing"
)

func TestParseCallbackRefreshToken(t *testing.T) {
	ui := `{"UserID":"u123","ScreenName":"tester","TenantID":""}`
	raw := "http://127.0.0.1:18080/authorize?refreshToken=rt-abc&userInfo=" + url.QueryEscape(ui)
	info, err := ParseCallback(raw)
	if err != nil {
		t.Fatal(err)
	}
	if info.RefreshToken != "rt-abc" {
		t.Fatalf("refreshToken not parsed: %+v", info)
	}
	if info.UID != "u123" || info.Nickname != "tester" {
		t.Fatalf("userInfo not parsed: %+v", info)
	}
}

func TestParseCallbackUserJwtFallback(t *testing.T) {
	uj := `{"Token":"jwt-token-1","RefreshToken":"rt-from-jwt","TokenExpireAt":1786847930141}`
	raw := "http://127.0.0.1:18080/authorize?userJwt=" + url.QueryEscape(uj)
	info, err := ParseCallback(raw)
	if err != nil {
		t.Fatal(err)
	}
	if info.RefreshToken != "rt-from-jwt" {
		t.Fatalf("userJwt.RefreshToken fallback failed: %+v", info)
	}
}

func TestParseCallbackUserJwtTokenOnly(t *testing.T) {
	uj := `{"Token":"jwt-token-only","TokenExpireAt":1786847930141}`
	raw := "http://127.0.0.1:18080/authorize?userJwt=" + url.QueryEscape(uj)
	info, err := ParseCallback(raw)
	if err != nil {
		t.Fatal(err)
	}
	if info.AccessToken != "jwt-token-only" {
		t.Fatalf("userJwt.Token fallback failed: %+v", info)
	}
	// 毫秒时间戳归一化为秒
	if info.ExpiresAt != 1786847930 {
		t.Fatalf("expire not normalized: %d", info.ExpiresAt)
	}
}

func TestParseCallbackEmpty(t *testing.T) {
	if _, err := ParseCallback(""); err == nil {
		t.Fatalf("empty callback should fail")
	}
	if _, err := ParseCallback("http://127.0.0.1/authorize"); err == nil {
		t.Fatalf("callback without tokens should fail")
	}
}

func TestNormalizeExpire(t *testing.T) {
	if NormalizeExpire(1786847930141) != 1786847930 {
		t.Fatalf("ms not normalized")
	}
	if NormalizeExpire(1786847930) != 1786847930 {
		t.Fatalf("seconds mutated")
	}
}

func TestCredMasked(t *testing.T) {
	c := &Cred{AccessToken: "at-1234567890abcdef", RefreshToken: "rt-1234567890abcdef", UID: "u1", Nickname: "n"}
	m := c.Masked()
	if m["accessToken"] == c.AccessToken {
		t.Fatalf("accessToken not masked")
	}
	if m["uid"] != "u1" {
		t.Fatalf("uid missing in mask: %+v", m)
	}
}

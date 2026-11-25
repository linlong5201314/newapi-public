// credimport_test.go Trae 凭据导入解析单测（回调链接两种形态 + userJwt 兜底）。
package trae

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportedClientSurvivesCredentialRoundTripAndRefresh(t *testing.T) {
	t.Setenv("TRAE_BASE_URL", "")
	for _, tc := range []struct {
		name, credential, client string
	}{
		{"existing SOLO credential", `{"refreshToken":"legacy-refresh"}`, ClientID},
		{"current IDE flat credential", `{"refreshToken":"ide-refresh","clientId":"ono9krqynydwx5","apiHost":"https://api.trae.cn"}`, IDEClientID},
		{"current IDE nested credential", `{"auth":{"refreshToken":"ide-refresh","clientId":"ono9krqynydwx5","apiHost":"https://api.trae.cn"}}`, IDEClientID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credential, err := ParseCred(tc.credential)
			require.NoError(t, err)
			credential, err = ParseCred(credential.Marshal())
			require.NoError(t, err)
			if credential.ClientID == "" {
				assert.Equal(t, OAuthHost, NewClient(credential).oauthBase())
			} else {
				assert.Equal(t, DomesticAuthHost, NewClient(credential).oauthBase())
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				assert.Equal(t, http.MethodPost, request.Method)
				assert.Equal(t, EpExchange, request.URL.Path)
				var body map[string]any
				assert.NoError(t, common.DecodeJson(request.Body, &body))
				assert.Equal(t, tc.client, body["ClientID"])
				assert.Equal(t, credential.RefreshToken, body["RefreshToken"])
				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"Result":{"Token":"renewed-access","RefreshToken":"renewed-refresh","TokenExpireAt":1900000000}}`))
				assert.NoError(t, err)
			}))
			defer server.Close()
			t.Setenv("TRAE_BASE_URL", server.URL)
			fetch := system_setting.GetFetchSetting()
			previous := *fetch
			fetch.AllowPrivateIp = true
			fetch.AllowedPorts = []string{strings.TrimPrefix(server.URL, "http://127.0.0.1:")}
			t.Cleanup(func() { *fetch = previous })
			credential.BaseOverride = server.URL
			renewed, err := NewClient(credential).refreshTokenOnce()
			require.NoError(t, err)
			assert.Equal(t, "renewed-access", renewed.AccessToken)
			assert.Equal(t, "renewed-refresh", renewed.RefreshToken)
			assert.Equal(t, credential.ClientID, renewed.ClientID)
			assert.Equal(t, credential.ApiHost, renewed.ApiHost)
		})
	}
}

func TestDomesticCallbackMatchesCurrentMagpieProtocol(t *testing.T) {
	for _, tokenJSON := range []string{
		`{"Token":"jwt-1","RefreshToken":"r-1","TokenExpireAt":1900000000000,"ClientID":"ono9krqynydwx5"}`,
		`{"token":"jwt-1","refreshToken":"r-1","tokenExpireAt":"2030-03-17T17:46:40Z","clientId":"ono9krqynydwx5"}`,
	} {
		query := url.Values{"userJwt": {tokenJSON}, "userInfo": {`{"UserID":"u-1","ScreenName":"Ann","Region":"CN","Host":"https://api.trae.cn"}`}}
		info, err := ParseCallback("http://127.0.0.1:18080/authorize?" + query.Encode())
		require.NoError(t, err)
		assert.Equal(t, "jwt-1", info.AccessToken)
		assert.Equal(t, "r-1", info.RefreshToken)
		assert.Equal(t, "u-1", info.UID)
		assert.Equal(t, "Ann", info.Nickname)
		assert.Equal(t, int64(1900000000), info.ExpiresAt)
		assert.Equal(t, IDEClientID, info.ClientID)
		assert.Equal(t, DomesticAuthHost, info.AuthHost)
	}
	info, err := ParseCallback("http://127.0.0.1:18080/authorize?refreshToken=legacy&clientId=ono9krqynydwx5&userId=u-2")
	require.NoError(t, err)
	assert.Equal(t, "legacy", info.RefreshToken)
	assert.Equal(t, IDEClientID, info.ClientID)
	assert.Equal(t, "u-2", info.UID)
}

func TestDomesticCallbackRejectsAmbiguousOrUnsupportedAuthorization(t *testing.T) {
	for _, callback := range []string{
		"https://attacker.invalid/authorize?refreshToken=private-token",
		"http://localhost:18080/authorize?refreshToken=private-token",
		"http://127.0.0.1:0/authorize?refreshToken=private-token",
		"http://127.0.0.1:65536/authorize?refreshToken=private-token",
		"http://127.0.0.1:18080/%61uthorize?refreshToken=private-token",
		"http://127.0.0.1:18080/authorize?refreshToken=private-token#fragment",
		"http://user@127.0.0.1:18080/authorize?refreshToken=private-token",
		"http://127.0.0.1:18080/authorize?refreshToken=private-token&refreshToken=other",
		"http://127.0.0.1:18080/authorize?refreshToken=private-token&userJwt=%zz",
		"http://127.0.0.1:18080/authorize?refreshToken=private-token&userJwt=not-json",
		"http://127.0.0.1:18080/authorize?refreshToken=private-token&error=access_denied",
		"http://127.0.0.1:18080/authorize?code=private-token",
		"http://127.0.0.1:18080/authorize?refreshToken=private-token&realm=global",
		"http://127.0.0.1:18080/authorize?refreshToken=private-token&authHost=https%3A%2F%2Fapi.trae.ai",
		"http://127.0.0.1:18080/authorize?refreshToken=private-token&auth_host=https%3A%2F%2Fattacker.invalid",
		"http://127.0.0.1:18080/authorize?refreshToken=private-token&clientId=unregistered-client",
	} {
		_, err := ParseCallback(callback)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-token")
	}
	for _, account := range []map[string]any{
		{"Region": "US"}, {"Host": "https://api.trae.ai"}, {"AuthHost": "https://127.0.0.1"},
		{"Host": "https://api.trae.cn.attacker.invalid"}, {"Host": []string{"https://api.trae.cn"}},
	} {
		raw, err := common.Marshal(account)
		require.NoError(t, err)
		_, err = ParseCallback("http://127.0.0.1:18080/authorize?refreshToken=private-token&userInfo=" + url.QueryEscape(string(raw)))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-token")
	}
}

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

package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type zcodeTestTransport func(*http.Request) (*http.Response, error)

func (f zcodeTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func zcodeTestJWT(until int64) string {
	return "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, until))) + ".fixture-signature"
}
func TestZCodeOfficialLoginTransport(t *testing.T) {
	InitHttpClient()
	client := GetHttpClient()
	old := *client
	t.Cleanup(func() { *client = old })
	var pollToken string
	status := "pending"
	calls := 0
	client.Transport = zcodeTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "zcode.z.ai", r.URL.Host)
		var body string
		switch r.URL.Path {
		case "/api/v1/oauth/cli/init":
			require.Equal(t, http.MethodPost, r.Method)
			raw, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"provider":"bigmodel"}`, string(raw))
			pollToken = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			require.Len(t, pollToken, 64)
			body = fmt.Sprintf(`{"code":0,"data":{"flow_id":"flow-fixture","poll_token":"ignored-server-field","authorize_url":"https://bigmodel.cn/login?appId=zcode&state=fixture","expires_at":%d,"poll_interval_sec":2}}`, time.Now().Add(5*time.Minute).Unix())
		case "/api/v1/oauth/cli/poll/flow-fixture":
			require.Equal(t, "Bearer "+pollToken, r.Header.Get("Authorization"))
			body = `{"code":0,"data":{"status":"pending"}}`
			if status == "ready" {
				body = fmt.Sprintf(`{"code":0,"data":{"status":"ready","token":%q,"user":{"user_id":"verified-owner"},"bigmodel":{"accessToken":"private-business-fixture","refreshToken":"private-refresh-fixture"}}}`, zcodeTestJWT(time.Now().Add(time.Hour).Unix()))
			}
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	ticket, auth, err := StartZCodeSignIn(context.Background(), "", "bigmodel")
	require.NoError(t, err)
	require.Contains(t, auth, "https://bigmodel.cn/login?")
	require.Equal(t, pollToken, ticket.PollToken)
	cred, err := PollZCodeSignIn(context.Background(), "", ticket)
	require.NoError(t, err)
	require.Nil(t, cred)
	status = "ready"
	cred, err = PollZCodeSignIn(context.Background(), "", ticket)
	require.NoError(t, err)
	require.Equal(t, "verified-owner", cred.UserID)
	require.Equal(t, "bigmodel", cred.Provider)
	require.Greater(t, cred.ExpiresAt, time.Now().Unix())
	ticket.Until = time.Now().Add(-time.Second).Unix()
	_, err = PollZCodeSignIn(context.Background(), "", ticket)
	require.Error(t, err)
	require.Equal(t, 3, calls)
}
func TestZCodeAuthorizationDestinations(t *testing.T) {
	for _, v := range []string{"https://bigmodel.cn/login?appId=zcode&state=a", "https://chat.z.ai/api/oauth/authorize?state=a", "https://zcode.z.ai/app/oauth/login?state=a"} {
		require.True(t, ValidZCodeAuthorizationURL(v), v)
	}
	for _, v := range []string{"http://bigmodel.cn/login", "https://bigmodel.cn.evil.invalid/login", "https://bigmodel.cn:443/login", "https://u:p@bigmodel.cn/login", "https://bigmodel.cn/login#token", "https://bigmodel.cn/not-login", "https://attacker.invalid/login", "https://chat.z.ai/api/oauth/authorize/extra"} {
		require.False(t, ValidZCodeAuthorizationURL(v), v)
	}
}
func TestZCodeLoginRejectsRedirectAndMalformedResults(t *testing.T) {
	InitHttpClient()
	client := GetHttpClient()
	old := *client
	t.Cleanup(func() { *client = old })
	for _, fixture := range []struct {
		name   string
		status int
		body   string
	}{
		{"redirect", 302, `{}`}, {"missing-code", 200, `{"data":{}}`}, {"business-error", 200, `{"code":7,"msg":"private-upstream-error"}`}, {"oversized", 200, strings.Repeat("x", 65537)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			count := 0
			client.Transport = zcodeTestTransport(func(r *http.Request) (*http.Response, error) {
				count++
				h := make(http.Header)
				h.Set("Location", "https://attacker.invalid/steal")
				return &http.Response{StatusCode: fixture.status, Header: h, Body: io.NopCloser(strings.NewReader(fixture.body)), Request: r}, nil
			})
			_, _, err := StartZCodeSignIn(context.Background(), "", "zai")
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-upstream-error")
			require.Equal(t, 1, count)
		})
	}
}

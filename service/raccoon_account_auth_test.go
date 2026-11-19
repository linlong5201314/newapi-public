package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type raccoonTestTransport func(*http.Request) (*http.Response, error)

func (f raccoonTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func raccoonTestClient(t *testing.T, transport raccoonTestTransport) {
	t.Helper()
	client, err := GetAccountAuthHTTPClient("", "")
	require.NoError(t, err)
	previous := client.Transport
	client.Transport = transport
	t.Cleanup(func() { client.Transport = previous })
}

func raccoonTestResponse(body string) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
}

func raccoonTestJWT(t *testing.T, uid string, expires int64) string {
	t.Helper()
	raw, err := common.Marshal(map[string]any{"sid": uid, "exp": expires})
	require.NoError(t, err)
	return "fixture." + base64.RawURLEncoding.EncodeToString(raw) + ".issued-signature"
}

func TestRaccoonAuthorizationUsesOfficialEndpointsAndLiveModels(t *testing.T) {
	expires := time.Now().Add(time.Hour).Unix()
	token := raccoonTestJWT(t, "ff", expires)
	var paths []string
	raccoonTestClient(t, func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "https", r.URL.Scheme)
		assert.Equal(t, "xiaohuanxiong.com", r.URL.Host)
		assert.Empty(t, r.URL.RawQuery)
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/web/auth/v1/login_with_authorization_code":
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Empty(t, r.Header.Get("Authorization"))
			var body map[string]string
			require.NoError(t, common.DecodeJson(r.Body, &body))
			assert.Equal(t, map[string]string{"authorization_code": "one-shot-code"}, body)
			return raccoonTestResponse(fmt.Sprintf(`{"code":200,"data":{"access_token":%q,"refresh_token":"private-refresh"}}`, token))
		case "/api/web/auth/v1/user_info":
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
			return raccoonTestResponse(`{"code":200,"data":{"user_id":"255","phone":"fixture-profile"}}`)
		case "/api/web/llm/v2/model_catalog":
			assert.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
			return raccoonTestResponse(`{"code":200,"data":{"categories":[{"models":[{"name":"upstream-live-a"},{"model_name":"upstream-live-b"},{"name":"upstream-live-a"}]}]}}`)
		default:
			t.Errorf("unexpected provider request: %s", r.URL.Path)
			return nil, errors.New("unexpected fixture request")
		}
	})
	credential, models, err := CompleteRaccoonAuthorization(context.Background(), "", "one-shot-code")
	require.NoError(t, err)
	require.NotNil(t, credential)
	assert.Equal(t, "255", credential.UserID)
	assert.Equal(t, token, credential.Token)
	assert.Equal(t, "private-refresh", credential.RefreshToken)
	assert.Equal(t, expires, credential.ExpiresAt)
	assert.Equal(t, []string{"upstream-live-a", "upstream-live-b"}, models)
	assert.Len(t, paths, 3)
}

func TestRaccoonAuthorizationRejectsInvalidProviderResponses(t *testing.T) {
	validToken := raccoonTestJWT(t, "ff", time.Now().Add(time.Hour).Unix())
	for _, tc := range []struct {
		name, stage, body string
		transportError    bool
	}{
		{"expired code", "exchange", `{"code":200035,"message":"private-code","data":{}}`, false},
		{"missing refresh", "exchange", fmt.Sprintf(`{"data":{"access_token":%q}}`, validToken), false},
		{"expired token", "exchange", fmt.Sprintf(`{"data":{"access_token":%q,"refresh_token":"secret"}}`, raccoonTestJWT(t, "ff", time.Now().Add(-time.Minute).Unix())), false},
		{"identity mismatch", "identity", `{"data":{"user_id":"256"}}`, false},
		{"identity rejected", "identity", `{"code":401,"data":{"user_id":"255"}}`, false},
		{"missing identity response", "identity", `{"data":{}}`, false},
		{"empty catalog", "catalog", `{"data":{"categories":[]}}`, false},
		{"catalog invalid model", "catalog", `{"data":{"categories":[{"models":[{"name":"a,b"}]}]}}`, false},
		{"catalog application failure", "catalog", `{"code":500,"categories":[{"models":[{"name":"not-authorized"}]}]}`, false},
		{"secret-bearing transport error", "exchange", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raccoonTestClient(t, func(r *http.Request) (*http.Response, error) {
				stage := "exchange"
				if strings.HasSuffix(r.URL.Path, "user_info") {
					stage = "identity"
				}
				if strings.HasSuffix(r.URL.Path, "model_catalog") {
					stage = "catalog"
				}
				if stage == tc.stage {
					if tc.transportError {
						return nil, errors.New("https://secret-user:secret-pass@private.invalid/?code=private-code")
					}
					return raccoonTestResponse(tc.body)
				}
				if stage == "exchange" {
					return raccoonTestResponse(fmt.Sprintf(`{"data":{"access_token":%q,"refresh_token":"private-refresh"}}`, validToken))
				}
				if stage == "identity" {
					return raccoonTestResponse(`{"data":{"user_id":"255"}}`)
				}
				return raccoonTestResponse(`{"data":{"categories":[{"models":[{"name":"live"}]}]}}`)
			})
			credential, models, err := CompleteRaccoonAuthorization(context.Background(), "", "private-code")
			require.Error(t, err)
			assert.Nil(t, credential)
			assert.Empty(t, models)
			assert.NotContains(t, err.Error(), "private")
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestRaccoonRefreshPreservesOwnerAndStableEgress(t *testing.T) {
	prior := &RaccoonCredential{Token: "expired", RefreshToken: "private-refresh", UserID: "255", Egress: AccountEgress{Key: "stable-owner-route", Proxy: ""}}
	for _, switched := range []bool{false, true} {
		t.Run(fmt.Sprintf("switched-%t", switched), func(t *testing.T) {
			uid := "ff"
			if switched {
				uid = "fe"
			}
			token := raccoonTestJWT(t, uid, time.Now().Add(time.Hour).Unix())
			raccoonTestClient(t, func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/refresh") {
					assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
					var body map[string]string
					require.NoError(t, common.DecodeJson(r.Body, &body))
					assert.Equal(t, map[string]string{"refresh_token": "private-refresh"}, body)
					return raccoonTestResponse(fmt.Sprintf(`{"data":{"access_token":%q,"refresh_token":"rotated-refresh"}}`, token))
				}
				return raccoonTestResponse(`{"data":{"user_id":"255"}}`)
			})
			next, err := RefreshRaccoonAccount(context.Background(), "", prior)
			if switched {
				require.ErrorIs(t, err, ErrAccountProtocol)
				assert.Nil(t, next)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, prior.Egress, next.Egress)
			assert.Equal(t, prior.UserID, next.UserID)
			assert.Equal(t, "rotated-refresh", next.RefreshToken)
			assert.Equal(t, "private-refresh", prior.RefreshToken)
		})
	}
}

func TestRaccoonAuthorizationNeverFollowsRedirect(t *testing.T) {
	calls := 0
	raccoonTestClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://attacker.invalid/?private-code"}}, Body: io.NopCloser(strings.NewReader("private-body"))}, nil
	})
	credential, models, err := CompleteRaccoonAuthorization(context.Background(), "", "private-code")
	require.ErrorIs(t, err, ErrAccountUpstream)
	assert.Nil(t, credential)
	assert.Empty(t, models)
	assert.Equal(t, 1, calls)
	assert.NotContains(t, err.Error(), "private")
}

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every provider reply in this file is synthetic. No real login, token store,
// consent, provider request, or inference is used by these tests.
type clineTestTransport func(*http.Request) (*http.Response, error)

func (f clineTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func clineTestClient(t *testing.T, transport clineTestTransport) *http.Client {
	t.Helper()
	client, err := GetAccountAuthHTTPClient("", "")
	require.NoError(t, err)
	old := *client
	t.Cleanup(func() { *client = old })
	client.Transport = transport
	return client
}

func clineTestResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestClineStartAuthorizesOnlyExactWorkOSBrowser(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprintf("complete-%t", complete), func(t *testing.T) {
			start := time.Now().Unix()
			clineTestClient(t, func(r *http.Request) (*http.Response, error) {
				assert.Equal(t, "https://api.workos.com/user_management/authorize/device", r.URL.String())
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Empty(t, r.Header.Get("Authorization"))
				raw, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				assert.Equal(t, "client_id=client_01K3A541FN8TA3EPPHTD2325AR", string(raw))
				d := map[string]any{"device_code": "synthetic-private-device", "user_code": "ABCD-EFGH", "verification_uri": "https://authkit.cline.bot/device", "expires_in": 1200, "interval": 5}
				if complete {
					d["verification_uri_complete"] = "https://authkit.cline.bot/device?user_code=ABCD-EFGH"
				}
				body, err := common.Marshal(d)
				require.NoError(t, err)
				return clineTestResponse(r, 200, string(body)), nil
			})
			ticket, target, err := StartClineSignIn(context.Background(), "")
			require.NoError(t, err)
			require.NotNil(t, ticket)
			assert.Equal(t, "https://authkit.cline.bot/device?user_code=ABCD-EFGH", target)
			assert.Equal(t, "synthetic-private-device", ticket.DeviceCode)
			assert.Equal(t, 5, ticket.Interval)
			assert.InDelta(t, start+600, ticket.Until, 2)
			assert.NotContains(t, target, ticket.DeviceCode)
		})
	}
	for _, target := range []string{
		"http://authkit.cline.bot/device?user_code=ABCD-EFGH",
		"https://authkit.cline.bot.evil.invalid/device?user_code=ABCD-EFGH",
		"https://arbitrary.authkit.app/device?user_code=ABCD-EFGH",
		"https://authkit.cline.bot:443/device?user_code=ABCD-EFGH",
		"https://user:pass@authkit.cline.bot/device?user_code=ABCD-EFGH",
		"https://authkit.cline.bot/device/extra?user_code=ABCD-EFGH",
		"https://authkit.cline.bot/de%76ice?user_code=ABCD-EFGH",
		"https://authkit.cline.bot/device?user_code=ABCD-EFGH#secret",
		"https://authkit.cline.bot/device?user_code=ABCD-EFGH&redirect_uri=https://attacker.invalid",
		"https://authkit.cline.bot/device?user_code=ABCD-EFGH&access_token=private-secret",
		"https://authkit.cline.bot/device?user_code=ABCD-EFGH&user_code=duplicate",
		"https://authkit.cline.bot/device?user_code=WRONG-CODE",
	} {
		t.Run(target, func(t *testing.T) {
			clineTestClient(t, func(r *http.Request) (*http.Response, error) {
				body, err := common.Marshal(map[string]any{"device_code": "private-secret", "user_code": "ABCD-EFGH", "expires_in": 300, "interval": 5, "verification_uri": "https://authkit.cline.bot/device", "verification_uri_complete": target})
				require.NoError(t, err)
				return clineTestResponse(r, 200, string(body)), nil
			})
			ticket, authURL, err := StartClineSignIn(context.Background(), "")
			require.ErrorIs(t, err, ErrAccountProtocol)
			assert.Nil(t, ticket)
			assert.Empty(t, authURL)
			assert.NotContains(t, err.Error(), "private-secret")
		})
	}
}

func TestClinePollExchangesWorkOSOnlyAtRegister(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	step := 0
	client := clineTestClient(t, func(r *http.Request) (*http.Response, error) {
		step++
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.Empty(t, r.Header.Get("Proxy-Authorization"))
		deadline, ok := r.Context().Deadline()
		require.True(t, ok, "each provider call must have its own deadline")
		assert.LessOrEqual(t, time.Until(deadline), 30*time.Second)
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		switch step {
		case 1, 2:
			assert.Equal(t, "https://api.workos.com/user_management/authenticate", r.URL.String())
			assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
			form, err := url.ParseQuery(string(raw))
			require.NoError(t, err)
			assert.Equal(t, url.Values{"client_id": {"client_01K3A541FN8TA3EPPHTD2325AR"}, "device_code": {"synthetic-private-device"}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, form)
			if step == 1 {
				return clineTestResponse(r, 400, `{"error":"authorization_pending"}`), nil
			}
			return clineTestResponse(r, 200, `{"access_token":"synthetic-workos-access","refresh_token":"synthetic-workos-refresh"}`), nil
		case 3:
			assert.Equal(t, "https://api.cline.bot/api/v1/auth/register", r.URL.String())
			assert.Equal(t, "Cline/3.0.68", r.Header.Get("User-Agent"))
			assert.Equal(t, "cline-cli", r.Header.Get("X-CLIENT-TYPE"))
			assert.JSONEq(t, `{"accessToken":"synthetic-workos-access","refreshToken":"synthetic-workos-refresh"}`, string(raw))
			return clineTestResponse(r, 200, fmt.Sprintf(`{"success":true,"data":{"accessToken":"synthetic-cline-access","refreshToken":"synthetic-cline-refresh","expiresAt":%q,"userInfo":{"clineUserId":"cline-user-fixture","subject":"workos-user-fixture","email":"fixture@example.invalid"}}}`, expires.Format(time.RFC3339))), nil
		default:
			t.Fatalf("unexpected extra provider call %d", step)
			return nil, errors.New("unexpected call")
		}
	})
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	for _, host := range []string{"https://api.workos.com", "https://api.cline.bot"} {
		u, err := url.Parse(host)
		require.NoError(t, err)
		jar.SetCookies(u, []*http.Cookie{{Name: "private-local-cookie", Value: "not-forwarded"}})
	}
	client.Jar = jar
	ticket := &ClineTicket{DeviceCode: "synthetic-private-device", Until: time.Now().Add(5 * time.Minute).Unix(), Interval: 5}
	cred, err := PollClineSignIn(context.Background(), "", ticket)
	require.NoError(t, err)
	assert.Nil(t, cred)
	cred, err = PollClineSignIn(context.Background(), "", ticket)
	require.NoError(t, err)
	require.NotNil(t, cred)
	assert.Equal(t, "synthetic-cline-access", cred.Token)
	assert.Equal(t, "synthetic-cline-refresh", cred.RefreshToken)
	assert.Equal(t, "cline-user-fixture", cred.UserID)
	assert.Equal(t, expires.Unix(), cred.ExpiresAt)
	assert.Equal(t, 3, step)
	assert.Same(t, jar, client.Jar, "shared client must not be mutated")
}

func TestClinePollRetryAndTerminalSemantics(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		want     error
		interval int
	}{
		{"pending", 400, `{"error":"authorization_pending"}`, nil, 5},
		{"slow-down", 400, `{"error":"slow_down"}`, nil, 10},
		{"provider-unavailable", 503, `private-upstream-body`, ErrAccountUpstream, 5},
		{"rate-limit", 429, `{}`, ErrAccountUpstream, 10},
		{"denied", 400, `{"error":"access_denied","error_description":"private-token-value"}`, ErrDesktopLoginExpired, 5},
		{"expired", 400, `{"error":"expired_token"}`, model.ErrAuthFlowExpired, 5},
		{"spent-code", 400, `{"error":"invalid_grant"}`, ErrDesktopLoginExpired, 5},
		{"invalid-json", 400, `private-upstream-body`, ErrAccountProtocol, 5},
		{"pending-on-success", 200, `{"error":"authorization_pending"}`, ErrAccountProtocol, 5},
		{"missing-access", 200, `{"refresh_token":"private-token-value"}`, ErrAccountProtocol, 5},
		{"oversized", 200, strings.Repeat("x", 65537), ErrAccountProtocol, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			clineTestClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				return clineTestResponse(r, tc.status, tc.body), nil
			})
			ticket := &ClineTicket{DeviceCode: "synthetic-device", Until: time.Now().Add(time.Minute).Unix(), Interval: 5}
			cred, err := PollClineSignIn(context.Background(), "", ticket)
			assert.Nil(t, cred)
			if tc.want == nil {
				assert.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.want)
				assert.NotContains(t, err.Error(), "private-")
			}
			assert.Equal(t, tc.interval, ticket.Interval)
			assert.Equal(t, 1, calls, "no hidden retries on side-effecting requests")
		})
	}
}

func TestClineRefreshRotationAndIdentity(t *testing.T) {
	expires := time.Now().Add(time.Hour).Unix()
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		want        error
		wantRefresh string
	}{
		{"rotated", 200, fmt.Sprintf(`{"success":true,"data":{"accessToken":"new-access-fixture","refreshToken":"new-refresh-fixture","expiresAt":%d,"userInfo":{"clineUserId":"owner-fixture"}}}`, expires*1000), nil, "new-refresh-fixture"},
		{"refresh-omitted", 200, fmt.Sprintf(`{"accessToken":"new-access-fixture","expiresAt":%q}`, time.Unix(expires, 0).UTC().Format(time.RFC3339)), nil, "old-refresh-fixture"},
		{"identity-swapped", 200, fmt.Sprintf(`{"accessToken":"new-access-fixture","refreshToken":"new-refresh-fixture","expiresAt":%d,"userInfo":{"clineUserId":"different-owner"}}`, expires*1000), ErrAccountProtocol, ""},
		{"invalid-grant", 400, `{"error":"invalid_grant","error_description":"private-refresh-fixture"}`, ErrDesktopLoginExpired, ""},
		{"transient-400", 400, `{"error":"temporary_failure"}`, ErrAccountUpstream, ""},
		{"provider-error", 503, `private-refresh-fixture`, ErrAccountUpstream, ""},
		{"envelope-failure", 200, `{"success":false,"error":"private-refresh-fixture"}`, ErrAccountProtocol, ""},
		{"missing-expiry", 200, `{"accessToken":"new-access-fixture"}`, ErrAccountProtocol, ""},
		{"expired-access", 200, `{"accessToken":"new-access-fixture","expiresAt":"2000-01-01T00:00:00Z"}`, ErrDesktopLoginExpired, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			clineTestClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, "https://api.cline.bot/api/v1/auth/refresh", r.URL.String())
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Empty(t, r.Header.Get("Authorization"))
				raw, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				assert.JSONEq(t, `{"refreshToken":"old-refresh-fixture","grantType":"refresh_token"}`, string(raw))
				return clineTestResponse(r, tc.status, tc.body), nil
			})
			old := ClineCredential{Token: "old-access-fixture", RefreshToken: "old-refresh-fixture", UserID: "owner-fixture", ExpiresAt: time.Now().Add(-time.Minute).Unix(), Egress: AccountEgress{Key: "egress-fixture", Proxy: "fixed-egress"}}
			snapshot := old
			next, err := RenewClineCredential(context.Background(), "", &old)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				assert.Nil(t, next)
				assert.NotContains(t, err.Error(), "private-")
			} else {
				require.NoError(t, err)
				require.NotNil(t, next)
				assert.Equal(t, "new-access-fixture", next.Token)
				assert.Equal(t, tc.wantRefresh, next.RefreshToken)
				assert.Equal(t, old.UserID, next.UserID)
				assert.Equal(t, expires, next.ExpiresAt)
				assert.Equal(t, old.Egress, next.Egress)
			}
			assert.Equal(t, snapshot, old, "caller owns committing the entire rotated pair")
			assert.Equal(t, 1, calls)
		})
	}
}

func TestClineModelFeedsUseProviderIDsWithoutCredentials(t *testing.T) {
	for _, optionalFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("optional-catalog-fails-%t", optionalFails), func(t *testing.T) {
			calls := 0
			clineTestClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, "api.cline.bot", r.URL.Host)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Empty(t, r.Header.Get("Authorization"), "both catalogs are public; do not disclose account tokens")
				assert.Empty(t, r.Header.Get("Cookie"))
				switch r.URL.Path {
				case "/api/v1/ai/cline/recommended-models":
					return clineTestResponse(r, 200, `{"success":true,"data":{"recommended":[{"id":"maker/model-one"},"maker/model-one",{"modelId":"maker/model-two"}],"free":["cline-free/fixture"],"clinePass":[{"model":"cline-pass/fixture"}],"clineCloud":["exclude/not-enabled"],"description":"exclude/not-a-model"}}`), nil
				case "/api/v1/ai/cline/models":
					if optionalFails {
						return clineTestResponse(r, 503, `private-model-error`), nil
					}
					return clineTestResponse(r, 200, `{"data":[{"id":"cloud-bare-fixture"},{"id":"maker/model-one"},{"id":""},{"id":"bad\nheader"}]}`), nil
				default:
					t.Fatalf("unexpected identity or inference endpoint %s", r.URL.Path)
					return nil, errors.New("unexpected endpoint")
				}
			})
			credential := &ClineCredential{Token: "private-access-fixture", UserID: "owner-fixture", ExpiresAt: time.Now().Add(time.Hour).Unix()}
			models, err := FetchClineModels(context.Background(), "", credential)
			require.NoError(t, err)
			want := []string{"maker/model-one", "maker/model-two", "cline-free/fixture", "cline-pass/fixture"}
			if !optionalFails {
				want = append(want, "cloud-bare-fixture")
			}
			assert.Equal(t, want, models)
			assert.Equal(t, 2, calls)
		})
	}
}

func TestClineRejectsRedirectsAndSecretBearingErrors(t *testing.T) {
	for _, op := range []string{"start", "poll", "refresh", "models"} {
		t.Run(op, func(t *testing.T) {
			calls := 0
			client := clineTestClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				res := clineTestResponse(r, http.StatusTemporaryRedirect, `private-upstream-secret`)
				res.Header.Set("Location", "https://attacker.invalid/steal?token=private-upstream-secret")
				return res, nil
			})
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				t.Fatal("shared redirect policy must not be used")
				return nil
			}
			var err error
			switch op {
			case "start":
				_, _, err = StartClineSignIn(context.Background(), "")
			case "poll":
				_, err = PollClineSignIn(context.Background(), "", &ClineTicket{DeviceCode: "fixture", Until: time.Now().Add(time.Minute).Unix(), Interval: 5})
			case "refresh":
				_, err = RenewClineCredential(context.Background(), "", &ClineCredential{Token: "fixture", RefreshToken: "fixture", UserID: "owner"})
			case "models":
				_, err = FetchClineModels(context.Background(), "", &ClineCredential{Token: "fixture", UserID: "owner", ExpiresAt: time.Now().Add(time.Hour).Unix()})
			}
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-")
			assert.NotContains(t, err.Error(), "attacker")
			assert.Equal(t, 1, calls)
		})
	}
}

func TestClineInvalidOrExpiredInputDoesNotReachProvider(t *testing.T) {
	clineTestClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid input must never reach the provider")
		return nil, errors.New("unexpected provider call")
	})
	_, err := PollClineSignIn(context.Background(), "", nil)
	assert.ErrorIs(t, err, ErrAccountProtocol)
	_, err = PollClineSignIn(context.Background(), "", &ClineTicket{DeviceCode: "fixture", Until: time.Now().Add(-time.Second).Unix(), Interval: 5})
	assert.ErrorIs(t, err, model.ErrAuthFlowExpired)
	_, err = PollClineSignIn(context.Background(), "", &ClineTicket{DeviceCode: "fixture\r\nInjected: yes", Until: time.Now().Add(time.Minute).Unix(), Interval: 5})
	assert.ErrorIs(t, err, ErrAccountProtocol)
	_, err = RenewClineCredential(context.Background(), "", &ClineCredential{UserID: "owner"})
	assert.ErrorIs(t, err, ErrDesktopLoginExpired)
	_, err = FetchClineModels(context.Background(), "", nil)
	assert.ErrorIs(t, err, ErrAccountProtocol)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = StartClineSignIn(canceled, "")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestClineModelFeedFailuresDoNotInventModels(t *testing.T) {
	for _, body := range []string{`{}`, `{"success":false,"data":{"recommended":["maker/not-successful"]}}`, `{"recommended":[]}`, `{"data":{"free":["invalid-no-prefix"],"description":"maker/not-a-model"}}`} {
		t.Run(body, func(t *testing.T) {
			clineTestClient(t, func(r *http.Request) (*http.Response, error) { return clineTestResponse(r, 200, body), nil })
			models, err := FetchClineModels(context.Background(), "", &ClineCredential{Token: "fixture", UserID: "owner", ExpiresAt: time.Now().Add(time.Hour).Unix()})
			require.Error(t, err)
			assert.Empty(t, models)
		})
	}
}

func TestClineRegistrationFailuresNeverReturnPartialCredentials(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"register-failed-after-exchange", 503, `private-register-fixture`, ErrAccountUpstream},
		{"register-redirect", 307, `private-register-fixture`, ErrAccountUpstream},
		{"register-unauthorized", 401, `private-register-fixture`, ErrDesktopLoginExpired},
		{"register-missing-identity", 200, fmt.Sprintf(`{"accessToken":"fixture-access","expiresAt":%q}`, expires), ErrAccountProtocol},
		{"register-untrusted-identity", 200, fmt.Sprintf(`{"accessToken":"fixture-access","expiresAt":%q,"userInfo":{"clineUserId":"bad\r\nowner"}}`, expires), ErrAccountProtocol},
		{"register-missing-expiry", 200, `{"accessToken":"fixture-access","userInfo":{"clineUserId":"owner"}}`, ErrAccountProtocol},
		{"register-empty-envelope", 200, `{"success":true,"data":null}`, ErrAccountProtocol},
		{"register-oversized", 200, strings.Repeat("x", 65537), ErrAccountProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			clineTestClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					assert.Equal(t, "api.workos.com", r.URL.Host)
					return clineTestResponse(r, 200, `{"access_token":"private-workos-fixture","refresh_token":"private-refresh-fixture"}`), nil
				}
				assert.Equal(t, "https://api.cline.bot/api/v1/auth/register", r.URL.String())
				res := clineTestResponse(r, tc.status, tc.body)
				res.Header.Set("Location", "https://attacker.invalid/steal")
				return res, nil
			})
			ticket := &ClineTicket{DeviceCode: "device-fixture", Until: time.Now().Add(time.Minute).Unix(), Interval: 5}
			snapshot := *ticket
			credential, err := PollClineSignIn(context.Background(), "", ticket)
			require.ErrorIs(t, err, tc.want)
			assert.Nil(t, credential)
			assert.Equal(t, snapshot, *ticket, "no registration tokens retained in the ticket")
			assert.Equal(t, 2, calls, "failed registration must not retry a consumed exchange")
			assert.NotContains(t, err.Error(), "private-")
		})
	}
}

func TestClineRegistrationSupportsSubjectAndOptionalRefresh(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	calls := 0
	clineTestClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return clineTestResponse(r, 200, `{"access_token":"workos-access-fixture"}`), nil
		}
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"accessToken":"workos-access-fixture","refreshToken":""}`, string(raw))
		return clineTestResponse(r, 200, fmt.Sprintf(`{"success":true,"data":{"accessToken":"cline-access-fixture","expiresAt":%q,"userInfo":{"subject":"verified-subject-fixture"}}}`, expires.Format(time.RFC3339))), nil
	})
	credential, err := PollClineSignIn(context.Background(), "", &ClineTicket{DeviceCode: "device-fixture", Until: time.Now().Add(time.Minute).Unix(), Interval: 5})
	require.NoError(t, err)
	require.NotNil(t, credential)
	assert.Equal(t, "verified-subject-fixture", credential.UserID)
	assert.Equal(t, expires.Unix(), credential.ExpiresAt)
	assert.Empty(t, credential.RefreshToken)
}

func TestClineTransportFailureAndCancellationDoNotLeakSecrets(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled-%t", canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			clineTestClient(t, func(*http.Request) (*http.Response, error) {
				calls++
				if canceled {
					cancel()
				}
				return nil, errors.New("private-provider-token and proxy-password-fixture")
			})
			ticket, target, err := StartClineSignIn(ctx, "")
			if canceled {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, ErrAccountUpstream)
			}
			assert.Nil(t, ticket)
			assert.Empty(t, target)
			assert.NotContains(t, err.Error(), "private-")
			assert.NotContains(t, err.Error(), "proxy-password")
			assert.Equal(t, 1, calls)
		})
	}
}

func TestClineCredentialJSONContainsOnlyRequiredAccountFields(t *testing.T) {
	raw, err := common.Marshal(ClineCredential{Token: "fixture-access", UserID: "owner", ExpiresAt: 123, Egress: AccountEgress{Key: "fixture-egress"}})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "refreshToken")
	assert.NotContains(t, string(raw), "email")
	assert.Contains(t, string(raw), `"egress"`)
}

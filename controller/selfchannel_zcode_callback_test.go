package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func zcodeCallbackRequest(identity model.AuthSessionIdentity, handler gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	return urlCallbackRequest(identity, func(c *gin.Context) { c.Request.Host = "gateway.example"; handler(c) }, body)
}

func zcodeCallbackStart(t *testing.T, identity model.AuthSessionIdentity, realm string) (string, string) {
	t.Helper()
	response := zcodeCallbackRequest(identity, SelfZCodeOAuthStart, fmt.Sprintf(`{"realm":%q,"proxy":"direct"}`, realm))
	data := urlCallbackData(t, response)
	flow := data["sessionId"].(string)
	original, err := url.Parse(data["authUrl"].(string))
	require.NoError(t, err)
	state := original.Query().Get("state")
	require.NotEmpty(t, state)
	returned := "https://zcode.z.ai/api/v1/oauth/cli/callback/" + realm + "?" + url.Values{"code": {"private-zcode-once-" + state}, "state": {state}}.Encode()
	return flow, returned
}

func zcodeCallbackInitResponse(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "zcode.z.ai" || r.URL.Path != "/api/v1/oauth/cli/init" {
		return nil, fmt.Errorf("unexpected ZCode fixture endpoint")
	}
	state := uuid.NewString()
	return selfContractResponse(200, fmt.Sprintf(`{"code":0,"data":{"flow_id":"private-flow-%s","authorize_url":"https://bigmodel.cn/login?state=%s","expires_at":%d,"poll_interval_sec":2}}`, state, state, time.Now().Add(5*time.Minute).Unix()))
}

func TestZCodeURLCompletionDashboardIdentityBinding(t *testing.T) {
	identity := setupURLCallbackTest(t)
	var completions atomic.Int32
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/callback/") {
			completions.Add(1)
			return selfContractResponse(302, "private-provider-body")
		}
		return zcodeCallbackInitResponse(r)
	})
	flow, returned := zcodeCallbackStart(t, identity, "bigmodel")
	for _, mutation := range []string{"owner", "sid", "auth_version", "session_version", "missing_sid", "token_auth", "flow_token"} {
		t.Run(mutation, func(t *testing.T) {
			requestIdentity, token := identity, flow
			switch mutation {
			case "owner":
				requestIdentity.UserID++
			case "sid":
				var session model.UserSession
				require.NoError(t, model.DB.Where("sid = ?", identity.SessionID).First(&session).Error)
				session.SID = uuid.NewString()
				require.NoError(t, model.DB.Create(&session).Error)
				requestIdentity.SessionID = session.SID
			case "auth_version":
				requestIdentity.UserAuthVersion++
			case "session_version":
				requestIdentity.SessionVersion++
			case "missing_sid":
				requestIdentity.SessionID = ""
			case "flow_token":
				token = strings.Repeat("x", 43)
			}
			response := zcodeCallbackRequest(requestIdentity, func(c *gin.Context) {
				if mutation == "token_auth" {
					c.Set("use_access_token", true)
				}
				SelfZCodeURLComplete(c)
			}, urlCallbackBody(t, token, returned))
			assert.NotEqual(t, http.StatusOK, response.Code)
			assert.Contains(t, response.Body.String(), `"success":false`)
			assert.NotContains(t, response.Body.String(), "private-")
			assert.Zero(t, completions.Load())
		})
	}
	assertBrokerNoChannel(t, identity.UserID)
}

func TestZCodeURLCompletionLiveSessionRevocation(t *testing.T) {
	for _, mutation := range []string{"revoke", "expire", "auth_version", "session_version"} {
		t.Run(mutation, func(t *testing.T) {
			identity := setupURLCallbackTest(t)
			var completions atomic.Int32
			setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/callback/") {
					completions.Add(1)
					return selfContractResponse(200, `{}`)
				}
				return zcodeCallbackInitResponse(r)
			})
			flow, returned := zcodeCallbackStart(t, identity, "bigmodel")
			query := model.DB.Model(&model.UserSession{}).Where("sid = ?", identity.SessionID)
			switch mutation {
			case "revoke":
				require.NoError(t, query.Update("status", model.UserSessionStatusRevoked).Error)
			case "expire":
				require.NoError(t, query.Update("expires_at", time.Now().Add(-time.Second).Unix()).Error)
			case "session_version":
				require.NoError(t, query.Update("version", identity.SessionVersion+1).Error)
			case "auth_version":
				require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", identity.UserID).Update("auth_version", identity.UserAuthVersion+1).Error)
			}
			response := zcodeCallbackRequest(identity, SelfZCodeURLComplete, urlCallbackBody(t, flow, returned))
			assert.Equal(t, http.StatusUnauthorized, response.Code)
			assert.Contains(t, response.Body.String(), "ACCOUNT_SESSION_REVOKED")
			assert.Zero(t, completions.Load())
			assertBrokerNoChannel(t, identity.UserID)
		})
	}
}

func TestZCodeURLCompletionStateAndDestinationBinding(t *testing.T) {
	identity := setupURLCallbackTest(t)
	var completions atomic.Int32
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/callback/") {
			completions.Add(1)
			return selfContractResponse(200, `{}`)
		}
		return zcodeCallbackInitResponse(r)
	})
	flow, returned := zcodeCallbackStart(t, identity, "bigmodel")
	for _, mutation := range []string{"wrong_state", "duplicate_state", "missing_state", "duplicate_code", "wrong_provider", "wrong_host", "fragment", "extra_token"} {
		t.Run(mutation, func(t *testing.T) {
			parsed, err := url.Parse(returned)
			require.NoError(t, err)
			q := parsed.Query()
			switch mutation {
			case "wrong_state":
				q.Set("state", "foreign-state")
			case "duplicate_state":
				q.Add("state", q.Get("state"))
			case "missing_state":
				q.Del("state")
			case "duplicate_code":
				q.Add("code", "other-code")
			case "wrong_provider":
				parsed.Path = "/api/v1/oauth/cli/callback/zai"
			case "wrong_host":
				parsed.Host = "zcode.z.ai.attacker.invalid"
			case "fragment":
				parsed.Fragment = "private-token"
			case "extra_token":
				q.Set("token", "private-untrusted-token")
			}
			parsed.RawQuery = q.Encode()
			response := zcodeCallbackRequest(identity, SelfZCodeURLComplete, urlCallbackBody(t, flow, parsed.String()))
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Contains(t, response.Body.String(), "ACCOUNT_FLOW_INVALID")
			assert.NotContains(t, response.Body.String(), "private-")
			assert.Zero(t, completions.Load())
		})
	}
}

func TestZCodeURLCompletionOutsideWriterTransactionAndReplay(t *testing.T) {
	for _, realm := range []string{"bigmodel", "zai"} {
		t.Run(realm, func(t *testing.T) {
			identity := setupURLCallbackTest(t)
			var flow string
			var calls atomic.Int32
			setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
				if !strings.Contains(r.URL.Path, "/callback/") {
					return zcodeCallbackInitResponse(r)
				}
				calls.Add(1)
				assert.Equal(t, "zcode.z.ai", r.URL.Host)
				assert.Empty(t, r.Header.Get("Authorization"))
				assert.Empty(t, r.Header.Get("Cookie"))
				assert.Empty(t, r.Header.Get("X-Account-Action"))
				// Independent writer proves no provider I/O is inside the claim's writer TX.
				err := model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
					assert.Equal(t, "polling", s.Status)
					var claim zcodePollClaim
					if err := common.UnmarshalJsonStr(s.UpstreamState, &claim); err != nil {
						return err
					}
					assert.True(t, claim.BrowserReturnUsed)
					assert.NotEmpty(t, claim.Nonce)
					return nil
				})
				assert.NoError(t, err)
				response, _ := selfContractResponse(302, "private-provider-body")
				response.Header.Set("Location", "https://attacker.invalid/private-redirect")
				return response, nil
			})
			var returned string
			flow, returned = zcodeCallbackStart(t, identity, realm)
			response := zcodeCallbackRequest(identity, SelfZCodeURLComplete, urlCallbackBody(t, flow, returned))
			data := urlCallbackData(t, response)
			assert.Equal(t, false, data["done"])
			assert.Equal(t, "pending", data["status"])
			assert.EqualValues(t, 2, data["interval"])
			assert.NotContains(t, response.Body.String(), "private-")
			require.NoError(t, model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
				var claim zcodePollClaim
				require.NoError(t, common.UnmarshalJsonStr(s.UpstreamState, &claim))
				assert.True(t, claim.BrowserReturnUsed)
				assert.Empty(t, claim.Nonce)
				assert.Zero(t, claim.LeaseUntil)
				assert.Equal(t, "pending", s.Status)
				return nil
			}))
			replay := zcodeCallbackRequest(identity, SelfZCodeURLComplete, urlCallbackBody(t, flow, returned))
			assert.Equal(t, http.StatusConflict, replay.Code)
			assert.EqualValues(t, 1, calls.Load())
			assertBrokerNoChannel(t, identity.UserID)
		})
	}
}

func TestZCodeURLCompletionExpiryConsumesSensitiveState(t *testing.T) {
	identity := setupURLCallbackTest(t)
	var calls atomic.Int32
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/callback/") {
			calls.Add(1)
			return selfContractResponse(200, `{}`)
		}
		return zcodeCallbackInitResponse(r)
	})
	flow, returned := zcodeCallbackStart(t, identity, "bigmodel")
	require.NoError(t, model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
		var claim zcodePollClaim
		if err := common.UnmarshalJsonStr(s.UpstreamState, &claim); err != nil {
			return err
		}
		claim.Until = time.Now().Add(-time.Second).Unix()
		raw, err := common.Marshal(claim)
		s.UpstreamState = string(raw)
		return err
	}))
	response := zcodeCallbackRequest(identity, SelfZCodeURLComplete, urlCallbackBody(t, flow, returned))
	assert.Equal(t, http.StatusGone, response.Code)
	assert.Contains(t, response.Body.String(), "ACCOUNT_FLOW_EXPIRED")
	assert.Zero(t, calls.Load())
	persisted, state := brokerStoredState(t, identity, "zcode")
	assert.NotNil(t, persisted.ConsumedAt)
	assert.Equal(t, "failed", state.Status)
	assert.Empty(t, state.UpstreamState)
	assert.Empty(t, state.Credential)
	assertBrokerNoChannel(t, identity.UserID)
}

func TestZCodeURLCompletionCancellationAndClaimLifecycle(t *testing.T) {
	for _, mutation := range []string{"cancel_before", "cancel_during", "revoke_during", "nonce_changed", "lease_expired", "ticket_expired", "provider_failure"} {
		t.Run(mutation, func(t *testing.T) {
			identity := setupURLCallbackTest(t)
			var flow string
			var calls atomic.Int32
			setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
				if !strings.Contains(r.URL.Path, "/callback/") {
					return zcodeCallbackInitResponse(r)
				}
				calls.Add(1)
				switch mutation {
				case "cancel_during":
					cancelled := zcodeCallbackRequest(identity, SelfZCodeOAuthCancel, urlCallbackBody(t, flow, ""))
					assert.Equal(t, http.StatusOK, cancelled.Code)
				case "revoke_during":
					assert.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", identity.SessionID).Update("status", model.UserSessionStatusRevoked).Error)
				case "nonce_changed", "lease_expired", "ticket_expired":
					err := model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
						var claim zcodePollClaim
						if err := common.UnmarshalJsonStr(s.UpstreamState, &claim); err != nil {
							return err
						}
						switch mutation {
						case "nonce_changed":
							claim.Nonce = "foreign-worker"
						case "lease_expired":
							claim.LeaseUntil = time.Now().Add(-time.Second).Unix()
						case "ticket_expired":
							claim.Until = time.Now().Add(-time.Second).Unix()
						}
						raw, err := common.Marshal(claim)
						s.UpstreamState = string(raw)
						return err
					})
					assert.NoError(t, err)
				case "provider_failure":
					return selfContractResponse(503, "private-provider-body")
				}
				return selfContractResponse(200, "private-provider-body")
			})
			var returned string
			flow, returned = zcodeCallbackStart(t, identity, "bigmodel")
			if mutation == "cancel_before" {
				cancel := zcodeCallbackRequest(identity, SelfZCodeOAuthCancel, urlCallbackBody(t, flow, ""))
				require.Equal(t, http.StatusOK, cancel.Code)
			}
			response := zcodeCallbackRequest(identity, SelfZCodeURLComplete, urlCallbackBody(t, flow, returned))
			expected := http.StatusConflict
			if mutation == "revoke_during" {
				expected = http.StatusUnauthorized
			}
			if mutation == "ticket_expired" {
				expected = http.StatusGone
			}
			if mutation == "provider_failure" {
				expected = http.StatusBadGateway
			}
			assert.Equal(t, expected, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "private-")
			if mutation == "cancel_before" {
				assert.Zero(t, calls.Load())
			} else {
				assert.EqualValues(t, 1, calls.Load())
			}
			assertBrokerNoChannel(t, identity.UserID)
			if mutation == "cancel_before" || mutation == "cancel_during" || mutation == "provider_failure" || mutation == "ticket_expired" {
				persisted, state := brokerStoredState(t, identity, "zcode")
				assert.NotNil(t, persisted.ConsumedAt)
				assert.Empty(t, state.UpstreamState)
				assert.Empty(t, state.Credential)
			}
		})
	}
}

func TestZCodeURLCompletionConcurrentClaimDeniesReplay(t *testing.T) {
	identity := setupURLCallbackTest(t)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var calls atomic.Int32
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/callback/") {
			return zcodeCallbackInitResponse(r)
		}
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return selfContractResponse(200, `{}`)
	})
	flow, returned := zcodeCallbackStart(t, identity, "bigmodel")
	body := urlCallbackBody(t, flow, returned)
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- zcodeCallbackRequest(identity, SelfZCodeURLComplete, body) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("callback request did not start")
	}
	second := zcodeCallbackRequest(identity, SelfZCodeURLComplete, body)
	assert.Equal(t, http.StatusConflict, second.Code)
	close(release)
	select {
	case response := <-first:
		assert.Equal(t, false, urlCallbackData(t, response)["done"])
	case <-time.After(5 * time.Second):
		t.Fatal("callback request did not terminate")
	}
	assert.EqualValues(t, 1, calls.Load())
}

func TestZCodeURLCompletionCanonicalReplayAcrossFlows(t *testing.T) {
	identity := setupURLCallbackTest(t)
	var calls atomic.Int32
	assertionState := uuid.NewString()
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/callback/") {
			calls.Add(1)
			return selfContractResponse(200, `{}`)
		}
		if r.URL.Path != "/api/v1/oauth/cli/init" {
			return nil, fmt.Errorf("unexpected replay fixture endpoint")
		}
		return selfContractResponse(200, fmt.Sprintf(`{"code":0,"data":{"flow_id":"private-replay-flow","authorize_url":"https://bigmodel.cn/login?state=%s","expires_at":%d,"poll_interval_sec":2}}`, assertionState, time.Now().Add(5*time.Minute).Unix()))
	})
	first, returned := zcodeCallbackStart(t, identity, "bigmodel")
	require.Equal(t, http.StatusOK, zcodeCallbackRequest(identity, SelfZCodeURLComplete, urlCallbackBody(t, first, returned)).Code)
	second, returned := zcodeCallbackStart(t, identity, "bigmodel")
	parsed, err := url.Parse(returned)
	require.NoError(t, err)
	q := parsed.Query()
	// Reordered and differently escaped query bytes are the same provider assertion.
	parsed.RawQuery = "state=" + url.QueryEscape(q.Get("state")) + "&code=" + strings.ReplaceAll(url.QueryEscape(q.Get("code")), "-", "%2D")
	replay := zcodeCallbackRequest(identity, SelfZCodeURLComplete, urlCallbackBody(t, second, parsed.String()))
	assert.Equal(t, http.StatusConflict, replay.Code)
	assert.EqualValues(t, 1, calls.Load())
	assertBrokerNoChannel(t, identity.UserID)
}

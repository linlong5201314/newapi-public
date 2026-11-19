package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func brokerRequest(identity model.AuthSessionIdentity, provider string, handler gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	return urlCallbackRequest(identity, func(c *gin.Context) {
		c.Request.Host = "gateway.example"
		c.Params = gin.Params{{Key: "provider", Value: provider}}
		handler(c)
	}, body)
}

// Qoder deliberately owns a separate pinned transport and returns client copies.
// Redirect every TLS dial on that transport to one trusted local server; never
// delegate an unexpected request to a real provider or environment proxy.
func setBrokerProviderTransport(t *testing.T, provider string, handle selfContractTransport) {
	t.Helper()
	if provider != "qoder" {
		setSelfContractTransport(t, handle)
		for _, protocol := range []string{"", "http1"} {
			client, err := service.GetAccountAuthHTTPClient("", protocol)
			require.NoError(t, err)
			_, mocked := client.Transport.(selfContractTransport)
			require.True(t, mocked, "strict auth fixture must never use a real transport")
		}
		return
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response, err := handle(r)
		if !assert.NoError(t, err) || !assert.NotNil(t, response) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			w.Header()[key] = values
		}
		w.WriteHeader(response.StatusCode)
		_, err = io.Copy(w, response.Body)
		assert.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	client, err := service.NewQoderHTTPClient("")
	require.NoError(t, err)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	previousTLS, previousDial, previousProxy := transport.DialTLSContext, transport.DialContext, transport.Proxy
	transport.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	dialer := tls.Dialer{Config: &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12}}
	transport.Proxy = nil
	transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("broker fixture forbids non-TLS network access")
	}
	transport.DialTLSContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		switch addr {
		case "openapi.qoder.com.cn:443", "openapi.qoder.sh:443", "gateway.qoder.com.cn:443", "api3.qoder.sh:443":
			return dialer.DialContext(ctx, "tcp", server.Listener.Addr().String())
		default:
			return nil, fmt.Errorf("broker fixture forbids unexpected host")
		}
	}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		transport.DialTLSContext, transport.DialContext, transport.Proxy = previousTLS, previousDial, previousProxy
	})
}

func brokerProviderResponse(r *http.Request) (*http.Response, error) {
	switch r.URL.Path {
	case "/user_management/authorize/device":
		return selfContractResponse(200, `{"device_code":"private-cline-device","user_code":"PUBLIC-APPROVAL","verification_uri":"https://authkit.cline.bot/device","expires_in":600,"interval":2}`)
	case "/user_management/authenticate":
		return selfContractResponse(200, `{"access_token":"private-workos-access","refresh_token":"private-workos-refresh"}`)
	case "/api/v1/auth/register":
		return selfContractResponse(200, fmt.Sprintf(`{"accessToken":"private-cline-access","refreshToken":"private-cline-refresh","expiresAt":%d,"userInfo":{"clineUserId":"verified-cline-owner"}}`, time.Now().Add(time.Hour).UnixMilli()))
	case "/api/v1/ai/cline/recommended-models":
		return selfContractResponse(200, `{"recommended":["deepseek/reasoner"]}`)
	case "/api/v1/ai/cline/models":
		return selfContractResponse(200, `[]`)
	case "/api/v1/deviceToken/poll":
		return selfContractResponse(200, `{"token":"private-qoder-access","refresh_token":"drt-private-qoder-refresh","expires_in":3600000}`)
	case "/api/v1/userinfo":
		return selfContractResponse(200, `{"id":"verified-qoder-owner","name":"Owner"}`)
	case "/algo/api/v2/model/list":
		return selfContractResponse(200, `{"chat":[{"key":"deepseek-v3","enable":true}]}`)
	}
	return nil, fmt.Errorf("unexpected broker fixture endpoint")
}

func brokerStartTestFlow(t *testing.T, identity model.AuthSessionIdentity, provider string) string {
	t.Helper()
	response := brokerRequest(identity, provider, SelfBrokerAccountStart, `{"realm":"cn","proxy":"direct"}`)
	data := urlCallbackData(t, response)
	assert.Equal(t, "device_poll", data["completionMode"])
	assert.Equal(t, false, data["automaticCallbackSupported"])
	assert.Equal(t, false, data["manualCallbackSupported"])
	assert.Equal(t, true, data["automaticPollingSupported"])
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.NotContains(t, response.Body.String(), "private-")
	flow := data["sessionId"].(string)
	require.NoError(t, model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error { s.NextPollAt = 0; return nil }))
	return flow
}

func brokerStoredState(t *testing.T, identity model.AuthSessionIdentity, provider string) (model.AuthFlow, model.WorkBuddyAuthState) {
	t.Helper()
	var flow model.AuthFlow
	require.NoError(t, model.DB.Where("provider = ? AND user_id = ? AND session_id = ? AND purpose = ?", provider, identity.UserID, identity.SessionID, model.WorkBuddyAuthPurpose).Last(&flow).Error)
	require.True(t, common.IsEncryptedSecret(flow.Payload))
	raw, err := common.DecryptSecret(flow.Payload)
	require.NoError(t, err)
	var state model.WorkBuddyAuthState
	require.NoError(t, common.UnmarshalJsonStr(raw, &state))
	return flow, state
}

func assertBrokerNoChannel(t *testing.T, ownerID int) {
	t.Helper()
	var count int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("owner_id = ?", ownerID).Count(&count).Error)
	assert.Zero(t, count)
	ownedChannels := model.DB.Model(&model.Channel{}).Select("id").Where("owner_id = ?", ownerID)
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id IN (?)", ownedChannels).Count(&count).Error)
	assert.Zero(t, count)
}

func TestBrokerDashboardIdentityAndOwnerBinding(t *testing.T) {
	for _, provider := range []string{"qoder", "cline"} {
		t.Run(provider, func(t *testing.T) {
			identity := setupURLCallbackTest(t)
			var calls atomic.Int32
			setBrokerProviderTransport(t, provider, func(r *http.Request) (*http.Response, error) { calls.Add(1); return brokerProviderResponse(r) })
			flow := brokerStartTestFlow(t, identity, provider)
			initialCalls := calls.Load()
			_, stored := brokerStoredState(t, identity, provider)
			assert.Equal(t, identity, stored.Identity)
			for _, mutation := range []string{"owner", "sid", "auth_version", "session_version", "token_auth", "personal_token", "missing_sid", "flow_token"} {
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
					case "personal_token":
						requestIdentity.SessionID = "pat:fixture"
					case "missing_sid":
						requestIdentity.SessionID = ""
					case "flow_token":
						token = strings.Repeat("x", 43)
					}
					response := brokerRequest(requestIdentity, provider, func(c *gin.Context) {
						if mutation == "token_auth" {
							c.Set("use_access_token", true)
						}
						SelfBrokerAccountPoll(c)
					}, urlCallbackBody(t, token, ""))
					assert.NotEqual(t, http.StatusOK, response.Code)
					assert.Contains(t, response.Body.String(), `"success":false`)
					assert.NotContains(t, response.Body.String(), "private-")
					assert.Equal(t, initialCalls, calls.Load())
				})
			}
			success := brokerRequest(identity, provider, SelfBrokerAccountPoll, urlCallbackBody(t, flow, ""))
			data := urlCallbackData(t, success)
			assert.Equal(t, true, data["done"])
			assert.NotContains(t, success.Body.String(), "private-")
			finishedCalls := calls.Load()
			replay := brokerRequest(identity, provider, SelfBrokerAccountPoll, urlCallbackBody(t, flow, ""))
			assert.Equal(t, true, urlCallbackData(t, replay)["done"])
			assert.Equal(t, finishedCalls, calls.Load())
			var channels int64
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("owner_id = ?", identity.UserID).Count(&channels).Error)
			assert.EqualValues(t, 1, channels)
			persisted, terminal := brokerStoredState(t, identity, provider)
			assert.NotNil(t, persisted.ConsumedAt)
			assert.Empty(t, terminal.Credential)
			assert.Empty(t, terminal.UpstreamState)
		})
	}
}

func TestBrokerLiveDashboardRevocationDeniesPoll(t *testing.T) {
	for _, provider := range []string{"qoder", "cline"} {
		for _, mutation := range []string{"revoke", "expire", "auth_version", "session_version"} {
			t.Run(provider+"/"+mutation, func(t *testing.T) {
				identity := setupURLCallbackTest(t)
				var calls atomic.Int32
				setBrokerProviderTransport(t, provider, func(r *http.Request) (*http.Response, error) { calls.Add(1); return brokerProviderResponse(r) })
				flow := brokerStartTestFlow(t, identity, provider)
				initial := calls.Load()
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
				denied := brokerRequest(identity, provider, SelfBrokerAccountPoll, urlCallbackBody(t, flow, ""))
				assert.Equal(t, http.StatusUnauthorized, denied.Code)
				assert.Contains(t, denied.Body.String(), "ACCOUNT_SESSION_REVOKED")
				assert.Equal(t, initial, calls.Load())
				assertBrokerNoChannel(t, identity.UserID)
			})
		}
	}
}

func TestBrokerCancellationWinsOverProviderResponse(t *testing.T) {
	for _, provider := range []string{"qoder", "cline"} {
		t.Run(provider, func(t *testing.T) {
			identity := setupURLCallbackTest(t)
			var flow string
			var cancelled atomic.Bool
			setBrokerProviderTransport(t, provider, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/v1/deviceToken/poll" || r.URL.Path == "/user_management/authenticate" {
					// This independent writer must complete while provider I/O is active.
					response := brokerRequest(identity, provider, SelfBrokerAccountCancel, urlCallbackBody(t, flow, ""))
					assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
					cancelled.Store(true)
				}
				return brokerProviderResponse(r)
			})
			flow = brokerStartTestFlow(t, identity, provider)
			response := brokerRequest(identity, provider, SelfBrokerAccountPoll, urlCallbackBody(t, flow, ""))
			assert.True(t, cancelled.Load())
			assert.Equal(t, http.StatusConflict, response.Code)
			assert.NotContains(t, response.Body.String(), "private-")
			stored, state := brokerStoredState(t, identity, provider)
			assert.Equal(t, "cancelled", state.Status)
			assert.NotNil(t, stored.ConsumedAt)
			assert.Empty(t, state.Credential)
			assert.Empty(t, state.UpstreamState)
			assertBrokerNoChannel(t, identity.UserID)
		})
	}
}

func TestBrokerExpiryAndLeaseLifecycle(t *testing.T) {
	for _, provider := range []string{"qoder", "cline"} {
		for _, lifecycle := range []string{"grant_expired", "active_lease", "abandoned_lease", "wrong_nonce", "late_response"} {
			t.Run(provider+"/"+lifecycle, func(t *testing.T) {
				identity := setupURLCallbackTest(t)
				var flow string
				var polls atomic.Int32
				setBrokerProviderTransport(t, provider, func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/api/v1/deviceToken/poll" || r.URL.Path == "/user_management/authenticate" {
						polls.Add(1)
						if lifecycle == "wrong_nonce" || lifecycle == "late_response" {
							err := model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
								var claim brokerClaim
								if err := common.UnmarshalJsonStr(s.UpstreamState, &claim); err != nil {
									return err
								}
								if lifecycle == "wrong_nonce" {
									claim.Nonce = "different-worker"
								} else {
									claim.LeaseUntil = time.Now().Add(-time.Second).Unix()
								}
								raw, err := common.Marshal(claim)
								s.UpstreamState = string(raw)
								return err
							})
							assert.NoError(t, err)
						}
					}
					return brokerProviderResponse(r)
				})
				flow = brokerStartTestFlow(t, identity, provider)
				if lifecycle != "wrong_nonce" && lifecycle != "late_response" {
					require.NoError(t, model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
						var claim brokerClaim
						if err := common.UnmarshalJsonStr(s.UpstreamState, &claim); err != nil {
							return err
						}
						switch lifecycle {
						case "grant_expired":
							claim.Until = time.Now().Add(-time.Second).Unix()
						case "active_lease":
							s.Status = "polling"
							claim.Nonce = "existing-worker"
							claim.LeaseUntil = time.Now().Add(time.Minute).Unix()
						case "abandoned_lease":
							s.Status = "polling"
							claim.Nonce = "abandoned-worker"
							claim.LeaseUntil = time.Now().Add(-time.Second).Unix()
						}
						raw, err := common.Marshal(claim)
						s.UpstreamState = string(raw)
						return err
					}))
				}
				response := brokerRequest(identity, provider, SelfBrokerAccountPoll, urlCallbackBody(t, flow, ""))
				expected := http.StatusGone
				if lifecycle == "active_lease" {
					expected = http.StatusTooManyRequests
				}
				if lifecycle == "wrong_nonce" || lifecycle == "late_response" {
					expected = http.StatusConflict
				}
				assert.Equal(t, expected, response.Code, response.Body.String())
				if lifecycle == "wrong_nonce" || lifecycle == "late_response" {
					assert.EqualValues(t, 1, polls.Load())
				} else {
					assert.Zero(t, polls.Load())
				}
				assertBrokerNoChannel(t, identity.UserID)
			})
		}
	}
}

func TestBrokerConcurrentPollClaimsOnlyOneNonce(t *testing.T) {
	for _, provider := range []string{"qoder", "cline"} {
		t.Run(provider, func(t *testing.T) {
			identity := setupURLCallbackTest(t)
			entered, release := make(chan struct{}, 1), make(chan struct{})
			var polls atomic.Int32
			setBrokerProviderTransport(t, provider, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/v1/deviceToken/poll" || r.URL.Path == "/user_management/authenticate" {
					polls.Add(1)
					entered <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
				}
				return brokerProviderResponse(r)
			})
			flow := brokerStartTestFlow(t, identity, provider)
			first := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				first <- brokerRequest(identity, provider, SelfBrokerAccountPoll, urlCallbackBody(t, flow, ""))
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("provider poll did not start")
			}
			second := brokerRequest(identity, provider, SelfBrokerAccountPoll, urlCallbackBody(t, flow, ""))
			assert.Equal(t, http.StatusTooManyRequests, second.Code)
			assert.Contains(t, second.Body.String(), "ACCOUNT_POLL_EARLY")
			close(release)
			select {
			case response := <-first:
				assert.Equal(t, true, urlCallbackData(t, response)["done"])
			case <-time.After(5 * time.Second):
				t.Fatal("claimed poll did not terminate")
			}
			assert.EqualValues(t, 1, polls.Load())
		})
	}
}

func TestBrokerCachedCredentialRetriesCatalogWithoutRedeeming(t *testing.T) {
	for _, provider := range []string{"qoder", "cline"} {
		t.Run(provider, func(t *testing.T) {
			identity := setupURLCallbackTest(t)
			var redeemed, catalogs atomic.Int32
			setBrokerProviderTransport(t, provider, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/v1/deviceToken/poll" || r.URL.Path == "/user_management/authenticate" {
					redeemed.Add(1)
				}
				if r.URL.Path == "/algo/api/v2/model/list" || r.URL.Path == "/api/v1/ai/cline/recommended-models" {
					if catalogs.Add(1) == 1 {
						return selfContractResponse(503, `{"message":"private-provider-error"}`)
					}
				}
				return brokerProviderResponse(r)
			})
			flow := brokerStartTestFlow(t, identity, provider)
			response := brokerRequest(identity, provider, SelfBrokerAccountPoll, urlCallbackBody(t, flow, ""))
			assert.Equal(t, http.StatusBadGateway, response.Code)
			assert.NotContains(t, response.Body.String(), "private-")
			assertBrokerNoChannel(t, identity.UserID)
			require.NoError(t, model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
				assert.Equal(t, "pending", s.Status)
				assert.Contains(t, s.Credential, "private-")
				var claim brokerClaim
				require.NoError(t, common.UnmarshalJsonStr(s.UpstreamState, &claim))
				assert.Empty(t, claim.Ticket)
				assert.Empty(t, claim.Nonce)
				assert.Zero(t, claim.LeaseUntil)
				// The redeemed device grant may expire while the authenticated catalog retries.
				claim.Until = time.Now().Add(-time.Second).Unix()
				raw, err := common.Marshal(claim)
				s.UpstreamState = string(raw)
				s.NextPollAt = 0
				return err
			}))
			retry := brokerRequest(identity, provider, SelfBrokerAccountPoll, urlCallbackBody(t, flow, ""))
			assert.Equal(t, true, urlCallbackData(t, retry)["done"])
			assert.EqualValues(t, 1, redeemed.Load())
			assert.EqualValues(t, 2, catalogs.Load())
		})
	}
}

func TestBrokerClineSlowDownPersistsCooldown(t *testing.T) {
	identity := setupURLCallbackTest(t)
	var polls atomic.Int32
	setBrokerProviderTransport(t, "cline", func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/user_management/authenticate" {
			polls.Add(1)
			return selfContractResponse(400, `{"error":"slow_down"}`)
		}
		return brokerProviderResponse(r)
	})
	flow := brokerStartTestFlow(t, identity, "cline")
	before := time.Now().Unix()
	response := brokerRequest(identity, "cline", SelfBrokerAccountPoll, urlCallbackBody(t, flow, ""))
	data := urlCallbackData(t, response)
	assert.Equal(t, false, data["done"])
	assert.EqualValues(t, 7, data["interval"])
	require.NoError(t, model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
		var claim brokerClaim
		require.NoError(t, common.UnmarshalJsonStr(s.UpstreamState, &claim))
		var ticket service.ClineTicket
		require.NoError(t, common.Unmarshal(claim.Ticket, &ticket))
		assert.Equal(t, 7, ticket.Interval)
		assert.Equal(t, 7, claim.Interval)
		assert.GreaterOrEqual(t, s.NextPollAt, before+7)
		return nil
	}))
	early := brokerRequest(identity, "cline", SelfBrokerAccountPoll, urlCallbackBody(t, flow, ""))
	assert.Equal(t, http.StatusTooManyRequests, early.Code)
	assert.EqualValues(t, 1, polls.Load())
	assertBrokerNoChannel(t, identity.UserID)
}

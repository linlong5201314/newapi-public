package controller

// Regression tests for req-6 auth failure classification: every free-source
// authorization start must return an actionable body (string message plus
// message_key) and an honest status. Configuration problems are never a
// vague 502; provider transport failures stay 502; pool misconfiguration is
// 503 with a named cause; Nous Portal flows through the shared broker.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// authClassEnvelope is the contract the web console parses: a plain string
// message for generic clients, message_key for i18n, and a stable code.
type authClassEnvelope struct {
	Success    bool   `json:"success"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	MessageKey string `json:"message_key"`
}

func decodeAuthClass(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) authClassEnvelope {
	t.Helper()
	require.Equal(t, status, recorder.Code, recorder.Body.String())
	var envelope authClassEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope), recorder.Body.String())
	assert.False(t, envelope.Success)
	assert.Equal(t, code, envelope.Code)
	assert.NotEmpty(t, envelope.Message, "message must be a plain string, not an object")
	assert.Equal(t, envelope.Message, envelope.MessageKey)
	return envelope
}

// authClassSession reuses the DS2 session fixture: an active dashboard session
// on an isolated sqlite gateway with encryption configured.
func authClassSession(t *testing.T, serverAddress string) model.AuthSessionIdentity {
	t.Helper()
	uid := setupChannelCreateTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.UserSession{}, &model.AuthFlow{}, &model.Option{}))
	session := selfContractDS2Session(t, uid)
	oldAddress, oldSecret, oldConfigured := system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured
	system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = serverAddress, "auth-class-regression-key", true
	t.Cleanup(func() {
		system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = oldAddress, oldSecret, oldConfigured
	})
	return model.AuthSessionIdentity{UserID: uid, SessionID: session.SID, UserAuthVersion: session.UserAuthVersion, SessionVersion: session.Version}
}

func authClassRequest(identity model.AuthSessionIdentity, origin string, handler gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", identity.UserID)
	c.Set("session_id", identity.SessionID)
	c.Set("auth_version", identity.UserAuthVersion)
	c.Set("session_version", identity.SessionVersion)
	c.Set("role", common.RoleCommonUser)
	c.Request = httptest.NewRequest(http.MethodPost, origin+"/api/channel/self/oauth/fixture/start", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Origin", origin)
	c.Request.Header.Set("X-Account-Action", "authorize")
	handler(c)
	return recorder
}

func authClassBroker(provider string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Params = gin.Params{{Key: "provider", Value: provider}}
		SelfBrokerAccountStart(c)
	}
}

// TestAccountStartFailureBodiesAreActionable locks the response shape the web
// console parses: a string message plus message_key, never the object form
// that collapsed every failure into the generic axios status text.
func TestAccountStartFailureBodiesAreActionable(t *testing.T) {
	t.Run("provider unreachable is 502 with a string message", func(t *testing.T) {
		identity := authClassSession(t, "https://gateway.example")
		setSelfContractTransport(t, func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial tcp: fixture provider unreachable")
		})
		for _, tc := range []struct {
			name    string
			handler gin.HandlerFunc
			body    string
		}{
			{"cline", authClassBroker("cline"), `{"proxy":"direct"}`},
			{"zcode", SelfZCodeOAuthStart, `{"realm":"bigmodel","proxy":"direct"}`},
			{"nous", authClassBroker("nous"), `{"proxy":"direct"}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				recorder := authClassRequest(identity, "https://gateway.example", tc.handler, tc.body)
				envelope := decodeAuthClass(t, recorder, http.StatusBadGateway, "ACCOUNT_UPSTREAM_FAILED")
				assert.Contains(t, envelope.Message, "provider")
			})
		}
	})

	t.Run("empty proxy pool is 503 naming the pool, not a vague 502", func(t *testing.T) {
		identity := authClassSession(t, "https://gateway.example")
		require.NoError(t, model.EnsureProxyPoolSchema())
		setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
			t.Errorf("start must fail before provider HTTP: %s", r.URL.Host)
			return selfContractResponse(500, `{}`)
		})
		for _, tc := range []struct {
			name    string
			handler gin.HandlerFunc
			body    string
		}{
			{"cline", authClassBroker("cline"), `{}`},
			{"qoder", authClassBroker("qoder"), `{"realm":"cn"}`},
			{"nous", authClassBroker("nous"), `{}`},
			{"zcode", SelfZCodeOAuthStart, `{"realm":"bigmodel"}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				recorder := authClassRequest(identity, "https://gateway.example", tc.handler, tc.body)
				envelope := decodeAuthClass(t, recorder, http.StatusServiceUnavailable, "ACCOUNT_PROXY_POOL_EMPTY")
				assert.Contains(t, envelope.Message, "proxy endpoint")
			})
		}
	})

	t.Run("proxy validation failure keeps egress classification", func(t *testing.T) {
		identity := authClassSession(t, "https://gateway.example")
		recorder := authClassRequest(identity, "https://gateway.example", authClassBroker("cline"), `{"proxy":"not-a-proxy-url"}`)
		envelope := decodeAuthClass(t, recorder, http.StatusServiceUnavailable, "ACCOUNT_PROXY_UNAVAILABLE")
		assert.Contains(t, envelope.Message, "proxy")
	})
}

// TestTraeURLOriginLoopbackVsProduction proves the URL-mode start works in
// local development (http loopback ServerAddress) while production misuse
// (http non-loopback, or an unparseable address) fails with an actionable
// 503 that names ServerAddress instead of blaming the account proxy.
func TestTraeURLOriginLoopbackVsProduction(t *testing.T) {
	for _, tc := range []struct {
		name          string
		serverAddress string
		origin        string
		wantStatus    int
		wantCode      string
	}{
		{"https production", "https://gateway.example", "https://gateway.example", http.StatusOK, ""},
		{"http loopback dev", "http://127.0.0.1:3100", "http://127.0.0.1:3100", http.StatusOK, ""},
		{"http localhost dev", "http://localhost:8080", "http://localhost:8080", http.StatusOK, ""},
		{"http LAN rejected", "http://192.168.1.10:3000", "", http.StatusServiceUnavailable, "ACCOUNT_SERVER_ADDRESS_INVALID"},
		{"http public rejected", "http://gateway.example", "", http.StatusServiceUnavailable, "ACCOUNT_SERVER_ADDRESS_INVALID"},
		{"empty rejected", "", "", http.StatusServiceUnavailable, "ACCOUNT_SERVER_ADDRESS_INVALID"},
		{"garbage rejected", "://no-scheme", "", http.StatusServiceUnavailable, "ACCOUNT_SERVER_ADDRESS_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity := authClassSession(t, tc.serverAddress)
			setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
				t.Errorf("URL start must not make HTTP requests: %s", r.URL.Host)
				return selfContractResponse(500, `{}`)
			})
			origin := tc.origin
			if origin == "" {
				origin = "https://gateway.example"
			}
			for provider, handler := range map[string]gin.HandlerFunc{"trae": SelfTraeURLStart, "raccoon": SelfRaccoonURLStart} {
				body := `{"proxy":"direct"}`
				if provider == "trae" {
					body = `{"confirmRotation":true,"proxy":"direct"}`
				}
				recorder := authClassRequest(identity, origin, handler, body)
				if tc.wantStatus == http.StatusOK {
					data := urlCallbackData(t, recorder)
					assert.NotEmpty(t, data["authUrl"], provider)
					assert.NotEmpty(t, data["expectedCallback"], provider)
				} else {
					envelope := decodeAuthClass(t, recorder, tc.wantStatus, tc.wantCode)
					assert.Contains(t, envelope.Message, "Server address", provider)
				}
			}
		})
	}
}

// TestTraeURLLoopbackOriginExactBinding proves the loopback origin is not a
// wildcard: a different loopback port in the Origin header is still rejected,
// and the stored bridge state binds the exact configured loopback origin.
func TestTraeURLLoopbackOriginExactBinding(t *testing.T) {
	identity := authClassSession(t, "http://127.0.0.1:3100")
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("start must not make HTTP requests: %s", r.URL.Host)
		return selfContractResponse(500, `{}`)
	})
	// Origin on a different loopback port than configured is a different origin.
	recorder := authClassRequest(identity, "http://127.0.0.1:9999", SelfTraeURLStart, `{"confirmRotation":true,"proxy":"direct"}`)
	assert.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "ACCOUNT_ORIGIN_REJECTED")

	response := authClassRequest(identity, "http://127.0.0.1:3100", SelfTraeURLStart, `{"confirmRotation":true,"proxy":"direct"}`)
	data := urlCallbackData(t, response)
	flow := data["sessionId"].(string)
	require.NoError(t, model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		var bridge traeURLState
		require.NoError(t, common.UnmarshalJsonStr(state.UpstreamState, &bridge))
		assert.Equal(t, "http://127.0.0.1:3100", bridge.Origin)
		return nil
	}))
	// The same flow remains cancellable through the loopback identity guard.
	cancelled := authClassRequest(identity, "http://127.0.0.1:3100", SelfTraeURLCancel, urlCallbackBody(t, flow, ""))
	assert.Equal(t, http.StatusOK, cancelled.Code, cancelled.Body.String())
}

// TestNousPortalStartContracts covers the whitelist regression that returned
// 400 ACCOUNT_FLOW_INVALID for every Nous Portal start, plus the success and
// provider-failure classifications through the shared broker.
func TestNousPortalStartContracts(t *testing.T) {
	t.Run("valid config returns a usable device link", func(t *testing.T) {
		identity := authClassSession(t, "https://gateway.example")
		setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "portal.nousresearch.com", r.URL.Host)
			assert.Equal(t, "/api/oauth/device/code", r.URL.Path)
			return selfContractResponse(200, `{"device_code":"fixture-device-code","user_code":"ABCD-EFGH","verification_uri":"https://portal.nousresearch.com/device","expires_in":600,"interval":2}`)
		})
		recorder := authClassRequest(identity, "https://gateway.example", authClassBroker("nous"), `{"proxy":"direct"}`)
		data := urlCallbackData(t, recorder)
		assert.Equal(t, "https://portal.nousresearch.com/device", data["authUrl"])
		assert.Equal(t, "device_poll", data["completionMode"])
		assert.EqualValues(t, 2, data["interval"])
	})

	t.Run("provider 400 stays an upstream 502, never a flow 400", func(t *testing.T) {
		identity := authClassSession(t, "https://gateway.example")
		setSelfContractTransport(t, func(*http.Request) (*http.Response, error) {
			return selfContractResponse(400, `{"error":"invalid_client"}`)
		})
		recorder := authClassRequest(identity, "https://gateway.example", authClassBroker("nous"), `{"proxy":"direct"}`)
		envelope := decodeAuthClass(t, recorder, http.StatusBadGateway, "ACCOUNT_UPSTREAM_FAILED")
		assert.Contains(t, envelope.Message, "provider")
	})

	t.Run("malformed provider payload is a protocol 502", func(t *testing.T) {
		identity := authClassSession(t, "https://gateway.example")
		setSelfContractTransport(t, func(*http.Request) (*http.Response, error) {
			return selfContractResponse(200, `<html>blocked</html>`)
		})
		recorder := authClassRequest(identity, "https://gateway.example", authClassBroker("nous"), `{"proxy":"direct"}`)
		decodeAuthClass(t, recorder, http.StatusBadGateway, "ACCOUNT_UPSTREAM_FAILED")
	})

	t.Run("verification uri outside the portal is rejected", func(t *testing.T) {
		identity := authClassSession(t, "https://gateway.example")
		setSelfContractTransport(t, func(*http.Request) (*http.Response, error) {
			return selfContractResponse(200, `{"device_code":"fixture-device-code","user_code":"ABCD-EFGH","verification_uri":"https://attacker.invalid/device","expires_in":600,"interval":2}`)
		})
		recorder := authClassRequest(identity, "https://gateway.example", authClassBroker("nous"), `{"proxy":"direct"}`)
		decodeAuthClass(t, recorder, http.StatusBadGateway, "ACCOUNT_UPSTREAM_FAILED")
	})
}

// TestBrokerStartSuccessContracts proves cline and qoder starts return usable
// links under a valid configuration, and that cancel still terminates the
// ceremony while a foreign session is rejected.
func TestBrokerStartSuccessContracts(t *testing.T) {
	t.Run("cline device link and cancel lifecycle", func(t *testing.T) {
		identity := authClassSession(t, "https://gateway.example")
		setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "api.workos.com", r.URL.Host)
			return selfContractResponse(200, `{"device_code":"fixture-device-code","user_code":"ABCD-EFGH","verification_uri":"https://authkit.cline.bot/device","expires_in":600,"interval":2}`)
		})
		recorder := authClassRequest(identity, "https://gateway.example", authClassBroker("cline"), `{"proxy":"direct"}`)
		data := urlCallbackData(t, recorder)
		assert.Equal(t, "https://authkit.cline.bot/device?user_code=ABCD-EFGH", data["authUrl"])
		flow := data["sessionId"].(string)

		// A foreign dashboard session cannot cancel or poll the flow.
		foreign := identity
		foreign.SessionID = "0fae6b2e-26fb-48ad-96fe-696d8fb6b22d"
		foreignCancel := authClassRequest(foreign, "https://gateway.example", func(c *gin.Context) {
			c.Params = gin.Params{{Key: "provider", Value: "cline"}}
			SelfBrokerAccountCancel(c)
		}, urlCallbackBody(t, flow, ""))
		assert.NotEqual(t, http.StatusOK, foreignCancel.Code, foreignCancel.Body.String())

		cancel := authClassRequest(identity, "https://gateway.example", func(c *gin.Context) {
			c.Params = gin.Params{{Key: "provider", Value: "cline"}}
			SelfBrokerAccountCancel(c)
		}, urlCallbackBody(t, flow, ""))
		assert.Equal(t, http.StatusOK, cancel.Code, cancel.Body.String())

		// Polling a cancelled flow fails closed.
		poll := authClassRequest(identity, "https://gateway.example", func(c *gin.Context) {
			c.Params = gin.Params{{Key: "provider", Value: "cline"}}
			SelfBrokerAccountPoll(c)
		}, urlCallbackBody(t, flow, ""))
		assert.NotEqual(t, http.StatusOK, poll.Code, poll.Body.String())
		var count int64
		require.NoError(t, model.DB.Model(&model.Channel{}).Count(&count).Error)
		assert.Zero(t, count)
	})

	t.Run("qoder start needs no provider call and survives provider outage", func(t *testing.T) {
		identity := authClassSession(t, "https://gateway.example")
		setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
			t.Errorf("qoder start must build its link locally: %s", r.URL.Host)
			return selfContractResponse(500, `{}`)
		})
		for realm, host := range map[string]string{"cn": "qoder.com.cn", "global": "qoder.com"} {
			recorder := authClassRequest(identity, "https://gateway.example", authClassBroker("qoder"), fmt.Sprintf(`{"realm":%q,"proxy":"direct"}`, realm))
			data := urlCallbackData(t, recorder)
			authURL := data["authUrl"].(string)
			assert.Contains(t, authURL, "https://"+host+"/device/selectAccounts?")
			assert.Contains(t, authURL, "challenge_method=S256")
			assert.NotContains(t, authURL, "fixture")
		}
	})

	t.Run("zcode device link success", func(t *testing.T) {
		identity := authClassSession(t, "https://gateway.example")
		setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "zcode.z.ai", r.URL.Host)
			assert.Equal(t, "/api/v1/oauth/cli/init", r.URL.Path)
			return selfContractResponse(200, fmt.Sprintf(`{"code":0,"data":{"flow_id":"fixture-flow","authorize_url":"https://bigmodel.cn/login","expires_at":%d,"poll_interval_sec":2}}`, time.Now().Add(10*time.Minute).Unix()))
		})
		recorder := authClassRequest(identity, "https://gateway.example", SelfZCodeOAuthStart, `{"realm":"bigmodel","proxy":"direct"}`)
		data := urlCallbackData(t, recorder)
		assert.Equal(t, "https://bigmodel.cn/login", data["authUrl"])
	})
}

// TestBrokerRejectsUnknownProvider keeps the provider whitelist fail-closed.
func TestBrokerRejectsUnknownProvider(t *testing.T) {
	identity := authClassSession(t, "https://gateway.example")
	recorder := authClassRequest(identity, "https://gateway.example", authClassBroker("attacker"), `{}`)
	decodeAuthClass(t, recorder, http.StatusBadRequest, "ACCOUNT_FLOW_INVALID")
}

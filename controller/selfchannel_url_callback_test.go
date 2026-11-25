package controller

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/trae"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupURLCallbackTest(t *testing.T) model.AuthSessionIdentity {
	t.Helper()
	uid := setupChannelCreateTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.UserSession{}, &model.AuthFlow{}, &model.Option{}))
	session := selfContractDS2Session(t, uid)
	oldAddress, oldSecret, oldConfigured := system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured
	oldEnabled, oldLimit := operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser
	system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = "https://gateway.example", "url-callback-regression-encryption-key", true
	operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = true, 50
	t.Cleanup(func() {
		system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = oldAddress, oldSecret, oldConfigured
		operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = oldEnabled, oldLimit
	})
	return model.AuthSessionIdentity{UserID: uid, SessionID: session.SID, UserAuthVersion: session.UserAuthVersion, SessionVersion: session.Version}
}

func urlCallbackRequest(identity model.AuthSessionIdentity, handler gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", identity.UserID)
	c.Set("session_id", identity.SessionID)
	c.Set("auth_version", identity.UserAuthVersion)
	c.Set("session_version", identity.SessionVersion)
	c.Set("role", common.RoleCommonUser)
	c.Request = httptest.NewRequest(http.MethodPost, "https://gateway.example/api/channel/self/oauth/fixture/url", strings.NewReader(body))
	c.Request.Host = "internal-proxy-node.invalid"
	c.Request.Header.Set("Origin", "https://gateway.example")
	c.Request.Header.Set("X-Forwarded-Host", "attacker.invalid")
	c.Request.Header.Set("Sec-Fetch-Site", "same-origin")
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Account-Action", "authorize")
	handler(c)
	return recorder
}

func urlCallbackData(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.NotNil(t, envelope.Data)
	return envelope.Data
}

func urlCallbackBody(t *testing.T, flow, callback string) string {
	t.Helper()
	raw, err := common.Marshal(map[string]string{"sessionId": flow, "callbackUrl": callback})
	require.NoError(t, err)
	return string(raw)
}

func urlTraeCallback(t *testing.T, expected, suffix, uid string) string {
	t.Helper()
	jwt, err := common.Marshal(map[string]any{"Token": "private-trae-access-" + suffix, "RefreshToken": "private-trae-refresh-" + suffix, "TokenExpireAt": time.Now().Add(time.Hour).Unix(), "ClientID": trae.IDEClientID})
	require.NoError(t, err)
	account, err := common.Marshal(map[string]string{"UserID": uid, "ScreenName": "Untrusted display name", "AuthHost": trae.DomesticAuthHost})
	require.NoError(t, err)
	return expected + "?" + url.Values{"userJwt": {string(jwt)}, "userInfo": {string(account)}}.Encode()
}

func urlRaccoonCallback(t *testing.T, data map[string]any, code string) string {
	t.Helper()
	u, err := url.Parse(data["authUrl"].(string))
	require.NoError(t, err)
	return data["expectedCallback"].(string) + "?" + url.Values{"code": {code}, "state": {u.Query().Get("state")}}.Encode()
}

func TestGeneratedURLStartsUseRealDesktopRedirectsWithoutDownloads(t *testing.T) {
	identity := setupURLCallbackTest(t)
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("start must not make HTTP requests: %s", r.URL.Host)
		return selfContractResponse(500, `{}`)
	})
	for _, tc := range []struct {
		name       string
		handler    gin.HandlerFunc
		host, path string
	}{
		{"trae", SelfTraeURLStart, "www.trae.cn", "/authorize"},
		{"raccoon", SelfRaccoonURLStart, "xiaohuanxiong.com", "/auth/callback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := urlCallbackRequest(identity, tc.handler, `{"realm":"cn","confirmRotation":true,"proxy":"direct"}`)
			data := urlCallbackData(t, response)
			authURL, err := url.Parse(data["authUrl"].(string))
			require.NoError(t, err)
			assert.Equal(t, "https", authURL.Scheme)
			assert.Equal(t, tc.host, authURL.Host)
			assert.Equal(t, true, data["manualCallbackSupported"])
			assert.Equal(t, false, data["automaticCallbackSupported"])
			assert.EqualValues(t, 600, data["expiresIn"])
			assert.Equal(t, true, data["requiresConsent"])
			assert.NotContains(t, data, "helperUrl")
			assert.NotContains(t, data, "requiresLocalHelper")
			assert.Empty(t, response.Header().Get("Content-Disposition"))
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
			callback, err := url.Parse(data["expectedCallback"].(string))
			require.NoError(t, err)
			assert.Equal(t, "http", callback.Scheme)
			assert.Equal(t, "127.0.0.1", callback.Hostname())
			assert.NotEmpty(t, callback.Port())
			assert.Equal(t, tc.path, callback.Path)
			assert.Empty(t, callback.RawQuery)
			flow := data["sessionId"].(string)
			require.NoError(t, model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
				if tc.name == "trae" {
					var bridge traeURLState
					require.NoError(t, common.UnmarshalJsonStr(state.UpstreamState, &bridge))
					assert.True(t, bridge.URLMode)
					assert.Equal(t, "https://gateway.example", bridge.Origin)
					assert.Equal(t, authURL.Query().Get("machine_id"), bridge.MachineID)
					assert.Regexp(t, `^[0-9a-f]{32}$`, bridge.MachineID)
					assert.Equal(t, authURL.Query().Get("device_id"), bridge.DeviceID)
					assert.Regexp(t, `^[1-9][0-9]{15}$`, bridge.DeviceID)
					assert.Equal(t, authURL.Query().Get("login_trace_id"), bridge.LoginTrace)
					assert.NotEmpty(t, bridge.LoginTrace)
					assert.Equal(t, callback.String(), authURL.Query().Get("auth_callback_url"))
					assert.Empty(t, authURL.Query().Get("state"))
				} else {
					stored, err := readRaccoonURL(state, "https://gateway.example")
					require.NoError(t, err)
					assert.Len(t, stored.State, 64)
					assert.Equal(t, stored.State, authURL.Query().Get("state"))
					assert.Equal(t, callback.String(), authURL.Query().Get("redirect"))
					assert.Equal(t, "loopback", authURL.Query().Get("desktop_callback"))
					assert.Empty(t, state.Models, "no static placeholder catalog")
				}
				return nil
			}))
			var rows []model.AuthFlow
			require.NoError(t, model.DB.Find(&rows).Error)
			require.NotEmpty(t, rows)
			for _, row := range rows {
				assert.True(t, common.IsEncryptedSecret(row.Payload))
				assert.NotContains(t, row.Payload, identity.SessionID)
			}
		})
	}
}

func TestRaccoonReturnedURLRequiresExactStateAndEndpoint(t *testing.T) {
	state := strings.Repeat("ab", 32)
	flow := &raccoonURLState{State: state, ExpectedCallback: "http://127.0.0.1:53210/auth/callback"}
	good := flow.ExpectedCallback + "?code=one-shot&state=" + state
	for _, raw := range []string{good, "office-raccoon://auth/callback?code=one-shot&state=" + state} {
		code, err := parseRaccoonReturnedURL(raw, flow)
		require.NoError(t, err)
		assert.Equal(t, "one-shot", code)
	}
	for _, tc := range []struct{ name, raw string }{
		{"missing state", flow.ExpectedCallback + "?code=one-shot"},
		{"duplicate state", good + "&state=" + state},
		{"wrong state", strings.Replace(good, state, strings.Repeat("cd", 32), 1)},
		{"case changed state", strings.Replace(good, state, strings.ToUpper(state), 1)},
		{"duplicate code", good + "&code=second"},
		{"empty code", strings.Replace(good, "one-shot", "", 1)},
		{"oversized code", strings.Replace(good, "one-shot", strings.Repeat("x", 4097), 1)},
		{"encoded control code", strings.Replace(good, "one-shot", "%0aone-shot", 1)},
		{"wrong port", strings.Replace(good, "53210", "53211", 1)},
		{"attacker host", strings.Replace(good, "127.0.0.1", "attacker.invalid", 1)},
		{"localhost alias", strings.Replace(good, "127.0.0.1", "localhost", 1)},
		{"userinfo", strings.Replace(good, "127.0.0.1", "user@127.0.0.1", 1)},
		{"path encoding", strings.Replace(good, "/auth/", "/%61uth/", 1)},
		{"fragment", good + "#fragment"},
		{"credential smuggling", good + "&access_token=secret"},
		{"error callback", good + "&error=denied"},
		{"unsupported custom host", "office-raccoon://attacker.invalid/callback?code=one-shot&state=" + state},
		{"bare code", "one-shot"},
		{"bare token", "header.payload.signature"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, err := parseRaccoonReturnedURL(tc.raw, flow)
			require.Error(t, err)
			assert.Empty(t, code)
		})
	}
}

func TestTraeURLCompletionVerifiesAccountAndRequiresConsent(t *testing.T) {
	identity := setupURLCallbackTest(t)
	calls := 0
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		calls++
		assert.Equal(t, "api.trae.cn", r.URL.Host)
		assert.Equal(t, trae.EpUserInfo, r.URL.Path)
		return selfContractResponse(200, `{"Result":{"UserID":"verified-trae-owner","ScreenName":"Provider verified name"}}`)
	})
	data := urlCallbackData(t, urlCallbackRequest(identity, SelfTraeURLStart, `{"confirmRotation":true,"proxy":"direct"}`))
	flow := data["sessionId"].(string)
	callback := urlTraeCallback(t, data["expectedCallback"].(string), "first", "verified-trae-owner")
	wrongEndpoint := strings.Replace(callback, "127.0.0.1", "attacker.invalid", 1)
	assert.Equal(t, 400, urlCallbackRequest(identity, SelfTraeURLComplete, urlCallbackBody(t, flow, wrongEndpoint)).Code)
	other := identity
	other.SessionID = uuid.NewString()
	assert.NotEqual(t, 200, urlCallbackRequest(other, SelfTraeURLComplete, urlCallbackBody(t, flow, callback)).Code)
	response := urlCallbackRequest(identity, SelfTraeURLComplete, urlCallbackBody(t, flow, callback))
	consent := urlCallbackData(t, response)
	assert.Equal(t, "awaiting_consent", consent["status"])
	assert.Equal(t, "Provider verified name", consent["account"].(map[string]any)["nickname"])
	assert.NotContains(t, response.Body.String(), "private-trae")
	assert.Equal(t, 1, calls)
	var count int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Count(&count).Error)
	assert.Zero(t, count)
	_, err := PollAccountAuthorization(context.Background(), identity, flow)
	require.Error(t, err, "generic poll cannot bypass verified identity consent")
	assert.NotEqual(t, 200, urlCallbackRequest(identity, SelfTraeURLComplete, urlCallbackBody(t, flow, callback)).Code)
	wrongConsent := fmt.Sprintf(`{"sessionId":%q,"consentToken":%q,"uid":"wrong-owner"}`, flow, consent["consentToken"])
	assert.NotEqual(t, 200, urlCallbackRequest(identity, SelfTraeBridgeConsent, wrongConsent).Code)
	consentBody := fmt.Sprintf(`{"sessionId":%q,"consentToken":%q,"uid":"verified-trae-owner"}`, flow, consent["consentToken"])
	completed := urlCallbackData(t, urlCallbackRequest(identity, SelfTraeBridgeConsent, consentBody))
	assert.Equal(t, true, completed["done"])
	polled := urlCallbackData(t, urlCallbackRequest(identity, SelfTraeURLPoll, urlCallbackBody(t, flow, "")))
	assert.Equal(t, true, polled["done"])
	assert.Equal(t, 1, calls, "provider pair is redeemed exactly once")
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel).Error)
	assert.Equal(t, constant.ChannelTypeTrae, channel.Type)
	assert.True(t, common.IsEncryptedSecret(channel.Key))
	second := urlCallbackData(t, urlCallbackRequest(identity, SelfTraeURLStart, `{"confirmRotation":true,"proxy":"direct"}`))
	replayed := urlTraeCallback(t, second["expectedCallback"].(string), "first", "verified-trae-owner")
	assert.NotEqual(t, 200, urlCallbackRequest(identity, SelfTraeURLComplete, urlCallbackBody(t, second["sessionId"].(string), replayed)).Code)
	assert.Equal(t, 1, calls, "shared assertion lock also protects generated URL imports")
}

func urlMockRaccoonProvider(t *testing.T, calls *atomic.Int32, entered chan struct{}, release <-chan struct{}) {
	t.Helper()
	claims, err := common.Marshal(map[string]any{"sid": "ff", "exp": time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	token := "fixture." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		assert.Equal(t, "xiaohuanxiong.com", r.URL.Host)
		assert.Empty(t, r.URL.RawQuery)
		switch r.URL.Path {
		case "/api/web/auth/v1/login_with_authorization_code":
			if entered != nil {
				close(entered)
				<-release
			}
			return selfContractResponse(200, fmt.Sprintf(`{"code":200,"data":{"access_token":%q,"refresh_token":"private-raccoon-refresh"}}`, token))
		case "/api/web/auth/v1/user_info":
			return selfContractResponse(200, `{"code":200,"data":{"user_id":"255"}}`)
		case "/api/web/llm/v2/model_catalog":
			return selfContractResponse(200, `{"data":{"categories":[{"models":[{"name":"provider-live-model"}]}]}}`)
		default:
			t.Errorf("unexpected provider path %s", r.URL.Path)
			return selfContractResponse(500, `{}`)
		}
	})
}

func TestRaccoonImportRejectsClientSuppliedLocalVerification(t *testing.T) {
	identity := setupURLCallbackTest(t)
	var calls atomic.Int32
	urlMockRaccoonProvider(t, &calls, nil, nil)
	claims, err := common.Marshal(map[string]any{"sid": "ff", "exp": time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	token := "fixture." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
	body, err := common.Marshal(map[string]any{
		"proxy": "direct",
		"credentials": map[string]any{
			"accessToken": token,
			"refreshToken": "private-raccoon-refresh",
			"localVerified": true,
			"models": []string{"invented-by-client"},
		},
	})
	require.NoError(t, err)
	response := urlCallbackRequest(identity, SelfImportRaccoon, string(body))
	data := urlCallbackData(t, response)
	assert.Equal(t, true, data["done"])
	assert.EqualValues(t, 2, calls.Load(), "import must use live provider verification and its live catalog")
	var channel model.Channel
	require.NoError(t, model.DB.Where("owner_id = ?", identity.UserID).First(&channel).Error)
	assert.Equal(t, "provider-live-model", channel.Models)
	assert.NotEqual(t, "invented-by-client", channel.Models)
	assert.NotContains(t, response.Body.String(), token)
}

func TestRaccoonURLCompletionSingleUseVerifiedConsentAndEncryptedEgress(t *testing.T) {
	identity := setupURLCallbackTest(t)
	var calls atomic.Int32
	urlMockRaccoonProvider(t, &calls, nil, nil)
	data := urlCallbackData(t, urlCallbackRequest(identity, SelfRaccoonURLStart, `{"proxy":"direct"}`))
	flow := data["sessionId"].(string)
	callback := urlRaccoonCallback(t, data, "private-raccoon-code")
	bad := strings.Replace(callback, "&state=", "&missing=", 1)
	assert.NotEqual(t, 200, urlCallbackRequest(identity, SelfRaccoonURLComplete, urlCallbackBody(t, flow, bad)).Code)
	assert.Zero(t, calls.Load())
	response := urlCallbackRequest(identity, SelfRaccoonURLComplete, urlCallbackBody(t, flow, callback))
	consent := urlCallbackData(t, response)
	assert.Equal(t, "awaiting_consent", consent["status"])
	assert.Equal(t, "255", consent["account"].(map[string]any)["uid"])
	assert.NotContains(t, response.Body.String(), "private-raccoon")
	assert.NotContains(t, response.Body.String(), "signature")
	assert.EqualValues(t, 3, calls.Load())
	var count int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Count(&count).Error)
	assert.Zero(t, count)
	_, err := PollAccountAuthorization(context.Background(), identity, flow)
	require.Error(t, err)
	assert.NotEqual(t, 200, urlCallbackRequest(identity, SelfRaccoonURLComplete, urlCallbackBody(t, flow, callback)).Code)
	badConsent := fmt.Sprintf(`{"sessionId":%q,"consentToken":%q,"uid":"attacker"}`, flow, consent["consentToken"])
	assert.NotEqual(t, 200, urlCallbackRequest(identity, SelfRaccoonURLConsent, badConsent).Code)
	consentBody := fmt.Sprintf(`{"sessionId":%q,"consentToken":%q,"uid":"255"}`, flow, consent["consentToken"])
	response = urlCallbackRequest(identity, SelfRaccoonURLConsent, consentBody)
	done := urlCallbackData(t, response)
	assert.Equal(t, true, done["done"])
	assert.NotContains(t, response.Body.String(), "private-raccoon")
	assert.NotEqual(t, 200, urlCallbackRequest(identity, SelfRaccoonURLConsent, consentBody).Code)
	assert.Equal(t, true, urlCallbackData(t, urlCallbackRequest(identity, SelfRaccoonURLPoll, urlCallbackBody(t, flow, "")))["done"])
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel).Error)
	assert.Equal(t, constant.ChannelTypeRaccoon, channel.Type)
	assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
	assert.Equal(t, "provider-live-model", channel.Models)
	assert.True(t, common.IsEncryptedSecret(channel.Key))
	raw, err := common.DecryptSecret(channel.Key)
	require.NoError(t, err)
	var credential service.RaccoonCredential
	require.NoError(t, common.UnmarshalJsonStr(raw, &credential))
	assert.Equal(t, "255", credential.UserID)
	assert.NotEmpty(t, credential.Egress.Key)
	_, err = credential.Egress.Resolve(identity.UserID, channel.Id, channel.GetSetting())
	require.NoError(t, err)
	second := urlCallbackData(t, urlCallbackRequest(identity, SelfRaccoonURLStart, `{"proxy":"direct"}`))
	assert.NotEqual(t, 200, urlCallbackRequest(identity, SelfRaccoonURLComplete, urlCallbackBody(t, second["sessionId"].(string), urlRaccoonCallback(t, second, "private-raccoon-code"))).Code)
	assert.EqualValues(t, 3, calls.Load(), "code replay across ceremonies must not hit provider")
}

func TestURLCallbacksEnforceOriginSIDVersionExpiryAndCancellation(t *testing.T) {
	for _, provider := range []string{"trae", "raccoon"} {
		for _, mutation := range []string{"other SID", "auth version", "expired flow", "cancelled", "cross origin", "query credential", "PAT", "non JSON"} {
			t.Run(provider+"/"+mutation, func(t *testing.T) {
				identity := setupURLCallbackTest(t)
				start, complete, cancel := SelfTraeURLStart, SelfTraeURLComplete, SelfTraeURLCancel
				if provider == "raccoon" {
					start, complete, cancel = SelfRaccoonURLStart, SelfRaccoonURLComplete, SelfRaccoonURLCancel
				}
				data := urlCallbackData(t, urlCallbackRequest(identity, start, `{"confirmRotation":true,"proxy":"direct"}`))
				flow := data["sessionId"].(string)
				callback := urlTraeCallback(t, data["expectedCallback"].(string), mutation, "owner")
				if provider == "raccoon" {
					callback = urlRaccoonCallback(t, data, "private-code")
				}
				setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
					t.Error("rejected ceremony reached provider")
					return selfContractResponse(500, `{}`)
				})
				caller := identity
				switch mutation {
				case "other SID":
					var session model.UserSession
					require.NoError(t, model.DB.Where("sid = ?", identity.SessionID).First(&session).Error)
					session.SID = uuid.NewString()
					require.NoError(t, model.DB.Create(&session).Error)
					caller.SessionID = session.SID
				case "auth version":
					require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", identity.UserID).Update("auth_version", 2).Error)
				case "expired flow":
					require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("user_id = ?", identity.UserID).Update("expires_at", time.Now().Add(-time.Minute)).Error)
				case "cancelled":
					assert.Equal(t, true, urlCallbackData(t, urlCallbackRequest(identity, cancel, urlCallbackBody(t, flow, "")))["cancelled"])
				}
				response := urlCallbackRequest(caller, func(c *gin.Context) {
					switch mutation {
					case "cross origin":
						c.Request.Header.Set("Origin", "https://attacker.invalid")
					case "query credential":
						c.Request.URL.RawQuery = "callbackUrl=secret"
					case "PAT":
						c.Set("use_access_token", true)
					case "non JSON":
						c.Request.Header.Set("Content-Type", "text/plain")
					}
					complete(c)
				}, urlCallbackBody(t, flow, callback))
				assert.NotEqual(t, 200, response.Code, response.Body.String())
				assert.NotContains(t, response.Body.String(), "private")
				var count int64
				require.NoError(t, model.DB.Model(&model.Channel{}).Count(&count).Error)
				assert.Zero(t, count)
			})
		}
	}
}

func TestURLCompletionNetworkNeverHoldsWriterAndCancelWins(t *testing.T) {
	for _, provider := range []string{"trae", "raccoon"} {
		t.Run(provider, func(t *testing.T) {
			identity := setupURLCallbackTest(t)
			sqlDB, err := model.DB.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			start, complete, cancel, poll := SelfTraeURLStart, SelfTraeURLComplete, SelfTraeURLCancel, SelfTraeURLPoll
			if provider == "raccoon" {
				start, complete, cancel, poll = SelfRaccoonURLStart, SelfRaccoonURLComplete, SelfRaccoonURLCancel, SelfRaccoonURLPoll
			}
			data := urlCallbackData(t, urlCallbackRequest(identity, start, `{"confirmRotation":true,"proxy":"direct"}`))
			flow := data["sessionId"].(string)
			callback := urlTraeCallback(t, data["expectedCallback"].(string), "race", "owner")
			if provider == "raccoon" {
				callback = urlRaccoonCallback(t, data, "private-code")
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var calls atomic.Int32
			if provider == "raccoon" {
				urlMockRaccoonProvider(t, &calls, entered, release)
			} else {
				setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
					calls.Add(1)
					close(entered)
					<-release
					return selfContractResponse(200, `{"Result":{"UserID":"owner","ScreenName":"Verified owner"}}`)
				})
			}
			completed := make(chan *httptest.ResponseRecorder, 1)
			go func() { completed <- urlCallbackRequest(identity, complete, urlCallbackBody(t, flow, callback)) }()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("provider request did not begin")
			}
			// With one available DB connection this can only finish if provider HTTP
			// is outside all flow transactions and the write connection is released.
			cancelled := make(chan *httptest.ResponseRecorder, 1)
			go func() { cancelled <- urlCallbackRequest(identity, cancel, urlCallbackBody(t, flow, "")) }()
			select {
			case response := <-cancelled:
				require.Equal(t, 200, response.Code, response.Body.String())
			case <-time.After(5 * time.Second):
				unblock()
				t.Fatal("cancellation blocked behind provider HTTP transaction")
			}
			unblock()
			select {
			case response := <-completed:
				assert.NotEqual(t, 200, response.Code, response.Body.String())
			case <-time.After(10 * time.Second):
				t.Fatal("completion did not exit after cancellation")
			}
			assert.NotEqual(t, 200, urlCallbackRequest(identity, poll, urlCallbackBody(t, flow, "")).Code)
			var count int64
			require.NoError(t, model.DB.Model(&model.Channel{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

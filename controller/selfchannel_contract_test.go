package controller

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type selfContractTransport func(*http.Request) (*http.Response, error)

func (f selfContractTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func setSelfContractTransport(t *testing.T, transport selfContractTransport) {
	t.Helper()
	setSelfContractProxyTransport(t, "", transport)
}

func setSelfContractProxyTransport(t *testing.T, proxy string, transport selfContractTransport) {
	t.Helper()
	// Isolate generic and strict account caches, including DS2 HTTP/1.1, so
	// fixture credentials never escape through a real provider connection.
	seen := make(map[*http.Client]bool)
	install := func(client *http.Client, err error) {
		require.NoError(t, err)
		if seen[client] {
			return
		}
		seen[client] = true
		previous := client.Transport
		client.Transport = transport
		t.Cleanup(func() { client.Transport = previous })
	}
	for _, settings := range []dto.ChannelSettings{{}, {HTTPProtocol: "http1"}} {
		install(service.GetHttpClientWithProxySettings(proxy, settings))
	}
	for _, protocol := range []string{"", "http1"} {
		install(service.GetAccountAuthHTTPClient(proxy, protocol))
	}
}
func selfContractResponse(status int, body string) (*http.Response, error) {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
}
func selfContractRequest(uid int, method, path, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", uid)
	c.Set("role", common.RoleCommonUser)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return recorder
}

func TestSelfChannelNullAndLegacyProxyContracts(t *testing.T) {
	for _, field := range []string{"setting", "other_info"} {
		t.Run(field+" rejects null", func(t *testing.T) {
			original := &model.Channel{OwnerId: 7, Models: "deepseek"}
			var err error
			require.NotPanics(t, func() { err = prepareSelfChannelUpdate(7, original, map[string]any{field: "null"}) })
			require.Error(t, err)
		})
	}
	for _, tc := range []struct{ proxy, mode string }{{"direct", "direct"}, {"https://user:fixture-password@proxy.example:443", "fixed"}, {"", "user_pool"}, {"userpool", "user_pool"}} {
		t.Run("legacy "+tc.mode+tc.proxy, func(t *testing.T) {
			original := &model.Channel{OwnerId: 7}
			original.SetSetting(dto.ChannelSettings{Proxy: tc.proxy})
			sanitizeSelfChannel(original)
			require.NotNil(t, original.GetSetting().ProxyPolicy)
			assert.Equal(t, tc.mode, original.GetSetting().ProxyPolicy.Mode)
			assert.Empty(t, original.GetSetting().Proxy)
		})
	}
}

func TestSelfChannelUpdateRedactsStoredProxy(t *testing.T) {
	uid := setupChannelCreateTest(t)
	channel := &model.Channel{OwnerId: uid, Name: "redaction", Type: constant.ChannelTypeOpenAI, Key: "fixture-key", Models: "fixture-model", Group: "default"}
	channel.SetSetting(dto.ChannelSettings{Proxy: "https://user:fixture-password@proxy.example:443", ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "fixed"}})
	channel.SetOtherInfo(map[string]any{"deviceProfile": "fixture-device-secret"})
	require.NoError(t, channel.Insert())
	body := fmt.Sprintf(`{"id":%d,"type":1,"setting":"{\"proxy\":\"\",\"proxy_policy\":{\"mode\":\"fixed\"}}"}`, channel.Id)
	recorder := selfContractRequest(uid, http.MethodPut, "/", body, UpdateSelfChannel)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	assert.NotContains(t, recorder.Body.String(), "fixture-password")
	assert.NotContains(t, recorder.Body.String(), "fixture-device-secret")
	require.NoError(t, model.DB.First(channel, channel.Id).Error)
	assert.Contains(t, channel.GetSetting().Proxy, "fixture-password")
}

func TestSelfChannelModelCaseBypass(t *testing.T) {
	uid := setupChannelCreateTest(t)
	setSelfContractTransport(t, func(*http.Request) (*http.Response, error) {
		return selfContractResponse(200, `{"data":[{"id":"deepseek:free","pricing":{"prompt":"0","completion":"0"}}]}`)
	})
	base := "https://openrouter.ai/api"
	channel := &model.Channel{OwnerId: uid, Name: "free-model-case", Type: constant.ChannelTypeOpenAI, Key: "fixture", BaseURL: &base, Models: "deepseek:free", Group: "default"}
	channel.SetSetting(dto.ChannelSettings{Proxy: "direct"})
	channel.SetOtherInfo(map[string]any{"templateId": "openrouter-free"})
	require.NoError(t, channel.Insert())
	for _, fields := range []string{`"Models":"paid"`, `"MODELS":"paid"`, `"models":"deepseek:free","Models":"paid"`, `"Models":"paid","models":"deepseek:free"`} {
		recorder := selfContractRequest(uid, http.MethodPut, "/", fmt.Sprintf(`{"id":%d,"type":1,%s}`, channel.Id, fields), UpdateSelfChannel)
		assert.Contains(t, recorder.Body.String(), `"success":false`, fields)
		require.NoError(t, model.DB.First(channel, channel.Id).Error)
		assert.Equal(t, "deepseek:free", channel.Models)
	}
}

func TestSelfChannelFreeCatalogContracts(t *testing.T) {
	t.Run("Kilo requires explicit free zero price and text output", func(t *testing.T) {
		setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "/api/v1/models", r.URL.Path)
			assert.Empty(t, r.Header.Get("Authorization"))
			return selfContractResponse(200, `{"data":[{"id":"valid-free","isFree":true,"pricing":{"prompt":"0","completion":"0","discount":0.5},"architecture":{"output_modalities":["text"]}},{"id":"paid-free","isFree":true,"pricing":{"prompt":"0.001","completion":"0"},"architecture":{"output_modalities":["text"]}},{"id":"unmarked","pricing":{"prompt":"0","completion":"0"},"architecture":{"output_modalities":["text"]}},{"id":"image-free","isFree":true,"pricing":{"prompt":"0","completion":"0"},"architecture":{"output_modalities":["image"]}}]}`)
		})
		ids, verified, _ := FetchLiveFreeModelsForUpstream("https://ai-gateway.kilo.ai/api", "kilo-code", "not-for-public-catalog", "")
		assert.True(t, verified)
		assert.Equal(t, []string{"valid-free"}, ids)
	})

	t.Run("OpenRouter rejects paid auto and contradictory free labels", func(t *testing.T) {
		setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "/api/v1/models", r.URL.Path)
			assert.Empty(t, r.Header.Get("Authorization"))
			return selfContractResponse(200, `{"data":[{"id":"openrouter/auto","pricing":{"prompt":"0","completion":"0"}},{"id":"paid:free","pricing":{"prompt":"0.1","completion":"0"}},{"id":"free-with-search-cost","pricing":{"prompt":"0","completion":"0","web_search":"0.1"}},{"id":"unknown:free"},{"id":"deepseek/free:free","pricing":{"prompt":"0.0000","completion":"0e0"}},{"id":"openrouter/free","pricing":{"prompt":"0","completion":"0"}}]}`)
		})
		ids, verified, _ := FetchLiveFreeModelsForUpstream("https://openrouter.ai/api", "openrouter-free", "secret-not-for-public-catalog", "")
		assert.True(t, verified)
		assert.Equal(t, []string{"deepseek/free:free", "openrouter/free"}, ids)
	})
	t.Run("OpenCode checks zero cost and chat protocol", func(t *testing.T) {
		setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
			return selfContractResponse(200, `{"data":[{"id":"deepseek-free","cost":{"text":{"input":{"price":0},"output":{"price":0}}},"api":{"chat-completions":{}}},{"id":"paid-free","cost":{"text":{"input":{"price":0.1},"output":{"price":0}}},"api":{"chat-completions":{}}},{"id":"responses-only-free","cost":{"text":{"input":{"price":0},"output":{"price":0}}},"api":{"responses":{}}},{"id":"gpt-paid"}]}`)
		})
		ids, verified, _ := FetchLiveFreeModelsForUpstream("https://opencode.ai/zen/v1", "opencode-free", "", "")
		assert.True(t, verified)
		assert.Equal(t, []string{"deepseek-free"}, ids)
	})
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"http failure", `{"data":[{"id":"invented-free"}]}`, 401},
		{"invalid json", `<html>blocked</html>`, 200},
		{"missing catalog", `{}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setSelfContractTransport(t, func(*http.Request) (*http.Response, error) { return selfContractResponse(tc.status, tc.body) })
			ids, verified, _ := FetchLiveFreeModelsForUpstream("https://openrouter.ai/api", "openrouter-free", "", "")
			assert.False(t, verified)
			assert.Empty(t, ids)
		})
	}
}

func TestSelfChannelDedicatedSourceGuards(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		handler          gin.HandlerFunc
		message          string
	}{
		{"freepi template", "/from_template", `{"templateId":"freepi","key":"arbitrary-token"}`, AddSelfChannelFromTemplate, "GitHub device authorization"},
		{"freepi binding", "/bindings", `{"provider":"freepi","accounts":"arbitrary-token"}`, CreateSelfBinding, "GitHub device authorization"},
		{"opencode template", "/from_template", `{"templateId":"opencode-free"}`, AddSelfChannelFromTemplate, "Unknown templateId"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := selfContractRequest(9, http.MethodPost, tc.path, tc.body, tc.handler)
			assert.Contains(t, recorder.Body.String(), tc.message)
			assert.Contains(t, recorder.Body.String(), `"success":false`)
		})
	}
}

func TestSelfChannelCatalogSaveRefreshOwnership(t *testing.T) {
	uid := setupChannelCreateTest(t)
	t.Setenv("CRYPTO_SECRET", "self-channel-contract-test-encryption-key")
	previousSecret := common.CryptoSecret
	common.CryptoSecret = "self-channel-contract-test-encryption-key"
	t.Cleanup(func() { common.CryptoSecret = previousSecret })
	var template *constant.FreeSourceTemplate
	for _, candidate := range constant.GetFreeSourceTemplates() {
		if candidate.Id == "openrouter-free" {
			template = candidate
			break
		}
	}
	require.NotNil(t, template)
	setSelfContractTransport(t, func(*http.Request) (*http.Response, error) {
		return selfContractResponse(200, `{"data":[{"id":"deepseek:free","pricing":{"prompt":"0","completion":"0"}},{"id":"qwen:free","pricing":{"prompt":"0","completion":"0"}},{"id":"paid","pricing":{"prompt":"1","completion":"1"}}]}`)
	})
	request := `{"templateId":"openrouter-free","key":"fixture-api-key","proxy":"direct","models":["deepseek:free"],"all_models":["deepseek:free","qwen:free","paid"]}`
	recorder := selfContractRequest(uid, http.MethodPost, "/from_template", request, AddSelfChannelFromTemplate)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	var stored model.Channel
	require.NoError(t, model.DB.Where("owner_id = ?", uid).Last(&stored).Error)
	assert.Equal(t, "deepseek:free", stored.Models)
	assert.Equal(t, []string{"deepseek:free", "qwen:free"}, DefaultModelsForChannel(&stored)[:2])
	var metadata map[string]any
	require.NoError(t, common.UnmarshalJsonStr(stored.OtherInfo, &metadata))
	assert.Equal(t, []any{"deepseek:free", "qwen:free"}, metadata["allModels"])
	recorder = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", uid)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(stored.Id)}}
	c.Request = httptest.NewRequest(http.MethodGet, "/fetch_models/"+strconv.Itoa(stored.Id), nil)
	FetchSelfChannelModels(c)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	assert.NotContains(t, recorder.Body.String(), `"paid"`)
	require.NoError(t, model.DB.First(&stored, stored.Id).Error)
	assert.Equal(t, "deepseek:free", stored.Models)
	for _, foreignUID := range []int{uid + 1, 0} {
		recorder = httptest.NewRecorder()
		c, _ = gin.CreateTestContext(recorder)
		c.Set("id", foreignUID)
		c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(stored.Id)}}
		c.Request = httptest.NewRequest(http.MethodGet, "/detail", nil)
		GetSelfChannelDetail(c)
		assert.Equal(t, 404, recorder.Code)
		assert.NotContains(t, recorder.Body.String(), "fixture-api-key")
	}
	recorder = selfContractRequest(uid, http.MethodPost, "/from_template", `{"templateId":"openrouter-free","key":"fixture-api-key","proxy":"direct","models":["paid"]}`, AddSelfChannelFromTemplate)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
}

func TestSelfChannelSettingsPreserveCatalogAndProxy(t *testing.T) {
	original := &model.Channel{OwnerId: 7, Models: "deepseek"}
	original.SetSetting(dto.ChannelSettings{Proxy: "https://user:fixture-password@proxy.example:443", ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "fixed"}})
	original.SetOtherInfo(map[string]any{"allModels": []string{"deepseek", "qwen"}, "templateId": "fixture"})
	req := map[string]any{"setting": `{"proxy":"","proxy_policy":{"mode":"fixed"}}`, "other_info": `{"allModels":["deepseek"]}`}
	require.NoError(t, prepareSelfChannelUpdate(7, original, req))
	var setting dto.ChannelSettings
	require.NoError(t, common.UnmarshalJsonStr(req["setting"].(string), &setting))
	assert.Equal(t, original.GetSetting().Proxy, setting.Proxy)
	var info map[string]any
	require.NoError(t, common.UnmarshalJsonStr(req["other_info"].(string), &info))
	assert.Equal(t, []any{"deepseek", "qwen"}, info["allModels"])
	sanitizeSelfChannel(original)
	assert.Empty(t, original.GetSetting().Proxy)
	assert.NotContains(t, *original.Setting, "fixture-password")
	req = map[string]any{"setting": `{"proxy":"https://user:***@proxy.example"}`}
	require.ErrorContains(t, prepareSelfChannelUpdate(7, original, req), "masked")
}

func selfContractDS2Session(t *testing.T, uid int) *model.UserSession {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.UserSession{}))
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", uid).Update("auth_version", 1).Error)
	session := &model.UserSession{SID: common.GetUUID(), UserID: uid, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: common.GenerateHMACWithKey(func() []byte {
		mac := hmac.New(sha256.New, []byte(common.SessionSecret))
		_, _ = mac.Write([]byte("new-api/auth/refresh/v1"))
		return mac.Sum(nil)
	}(), "fixture-refresh"), LoginMethod: "password", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, model.DB.Create(session).Error)
	return session
}

func selfContractDS2Start(session *model.UserSession, body string) *httptest.ResponseRecorder {
	return selfContractRequest(session.UserID, http.MethodPost, "/start", body, func(c *gin.Context) {
		c.Set("session_id", session.SID)
		c.Set("auth_version", session.UserAuthVersion)
		c.Set("session_version", session.Version)
		SelfDs2ApiOAuthStart(c)
	})
}

func TestDS2PlatformSessionBindingFailures(t *testing.T) {
	for _, mutation := range []string{"revoked", "expired", "session_version", "auth_version", "different_sid", "missing_refresh", "forged_refresh"} {
		t.Run(mutation, func(t *testing.T) {
			uid := setupChannelCreateTest(t)
			require.NoError(t, model.DB.AutoMigrate(&model.SelfOAuthSession{}))
			platform := selfContractDS2Session(t, uid)
			oldAddress, oldSecret, oldConfigured := system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured
			system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = "https://newapi.example", "ds2-binding-test-key", true
			t.Cleanup(func() {
				system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = oldAddress, oldSecret, oldConfigured
			})
			t.Setenv("DS2API_BASE_URL", "https://ds2.example/v1")
			start := selfContractDS2Start(platform, `{"proxy":"direct"}`)
			require.Contains(t, start.Body.String(), `"success":true`)
			var response struct {
				Data struct {
					State   string `json:"sessionId"`
					AuthURL string `json:"authUrl"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(start.Body.Bytes(), &response))
			assert.Contains(t, response.Data.AuthURL, "/api/user/auth/ds2api/login?")
			cookie := start.Result().Cookies()[0]
			assert.Equal(t, "/api/user/auth/ds2api", cookie.Path)
			var flow model.SelfOAuthSession
			require.NoError(t, model.DB.Where("state = ?", ds2SessionKey(response.Data.State)).First(&flow).Error)
			var options map[string]any
			require.NoError(t, common.UnmarshalJsonStr(common.DecryptSecretOrLegacy(flow.Refresh), &options))
			refreshSID := platform.SID
			switch mutation {
			case "revoked":
				require.NoError(t, model.DB.Model(platform).Update("status", model.UserSessionStatusRevoked).Error)
			case "expired":
				require.NoError(t, model.DB.Model(platform).Update("expires_at", time.Now().Add(-time.Second).Unix()).Error)
			case "session_version":
				require.NoError(t, model.DB.Model(platform).Update("version", 2).Error)
			case "auth_version":
				require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", uid).Update("auth_version", 2).Error)
			case "different_sid":
				refreshSID = "0fae6b2e-26fb-48ad-96fe-696d8fb6b22d"
			}
			calls := 0
			setSelfContractTransport(t, func(*http.Request) (*http.Response, error) { calls++; return selfContractResponse(500, `{}`) })
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				form := url.Values{"state": {response.Data.State}, "csrf": {fmt.Sprint(options["csrf"])}, "account": {"test@example.com"}, "password": {"fixture-password"}}
				c.Request = httptest.NewRequest(method, response.Data.AuthURL, strings.NewReader(form.Encode()))
				c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				c.Request.Header.Set("Origin", "https://newapi.example")
				c.Request.AddCookie(cookie)
				if mutation != "missing_refresh" {
					secret := "fixture-refresh"
					if mutation == "forged_refresh" {
						secret = "forged-secret"
					}
					c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: refreshSID + "." + secret})
				}
				if method == http.MethodGet {
					SelfDs2ApiOAuthAuthorizePage(c)
				} else {
					SelfDs2ApiOAuthCallback(c)
				}
				assert.Equal(t, http.StatusForbidden, recorder.Code, method+": "+recorder.Body.String())
			}
			assert.Zero(t, calls)
			var count int64
			require.NoError(t, model.DB.Model(&model.Channel{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestDS2RevocationDuringExchangeRollsBack(t *testing.T) {
	uid := setupChannelCreateTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SelfOAuthSession{}, &model.SelfAccountBinding{}))
	platform := selfContractDS2Session(t, uid)
	oldAddress, oldSecret, oldConfigured := system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured
	system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = "https://newapi.example", "ds2-midflight-test-key", true
	t.Cleanup(func() {
		system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = oldAddress, oldSecret, oldConfigured
	})
	t.Setenv("DS2API_BASE_URL", "https://ds2.example/v1")
	start := selfContractDS2Start(platform, `{"proxy":"direct"}`)
	require.Contains(t, start.Body.String(), `"success":true`)
	var response struct {
		Data struct {
			State string `json:"sessionId"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(start.Body.Bytes(), &response))
	var flow model.SelfOAuthSession
	require.NoError(t, model.DB.Where("state = ?", ds2SessionKey(response.Data.State)).First(&flow).Error)
	var options ds2LoginOptions
	require.NoError(t, common.UnmarshalJsonStr(common.DecryptSecretOrLegacy(flow.Refresh), &options))
	assert.Equal(t, platform.SID, options.Identity.SessionID)
	assert.Equal(t, platform.UserAuthVersion, options.Identity.UserAuthVersion)
	assert.Equal(t, platform.Version, options.Identity.SessionVersion)
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "chat.deepseek.com" {
			return selfContractResponse(200, `{"code":0,"data":{"biz_code":0,"biz_data":{"user":{"token":"fixture-midflight-token"}}}}`)
		}
		require.NoError(t, model.DB.Model(platform).Update("status", model.UserSessionStatusRevoked).Error)
		return selfContractResponse(200, `{"data":[{"id":"deepseek-chat"}]}`)
	})
	form := url.Values{"state": {response.Data.State}, "csrf": {options.CSRF}, "account": {"test@example.com"}, "password": {"fixture-password"}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "https://newapi.example/api/user/auth/ds2api/credentials", strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request.Header.Set("Origin", "https://newapi.example")
	c.Request.AddCookie(start.Result().Cookies()[0])
	c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: platform.SID + ".fixture-refresh"})
	SelfDs2ApiOAuthCallback(c)
	assert.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "fixture-midflight-token")
	for _, target := range []any{&model.Channel{}, &model.SelfAccountBinding{}, &model.Ability{}} {
		var count int64
		require.NoError(t, model.DB.Model(target).Count(&count).Error)
		assert.Zero(t, count)
	}
	require.NoError(t, model.DB.Where("state = ?", flow.State).First(&flow).Error)
	assert.Equal(t, "failed", flow.Status)
	assert.Empty(t, flow.Refresh)
}

func TestDS2AccountLoginSessionEndToEnd(t *testing.T) {
	uid := setupChannelCreateTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SelfOAuthSession{}, &model.SelfAccountBinding{}))
	previousConfigured := common.CryptoSecretConfigured
	common.CryptoSecretConfigured = true
	t.Cleanup(func() { common.CryptoSecretConfigured = previousConfigured })
	previousAddress, previousSecret := system_setting.ServerAddress, common.CryptoSecret
	system_setting.ServerAddress = "https://newapi.example"
	common.CryptoSecret = "ds2-contract-test-secret-key"
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress; common.CryptoSecret = previousSecret })
	t.Setenv("DS2API_BASE_URL", "https://ds2.example/v1")
	previousLimit := operation_setting.SelfChannelMaxPerUser
	operation_setting.SelfChannelMaxPerUser = 20
	t.Cleanup(func() { operation_setting.SelfChannelMaxPerUser = previousLimit })
	calls := 0
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "chat.deepseek.com" {
			assert.Equal(t, "/api/v0/users/login", r.URL.Path)
			assert.Equal(t, "android", r.Header.Get("x-client-platform"))
			raw, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var req map[string]any
			require.NoError(t, common.Unmarshal(raw, &req))
			assert.Equal(t, "test@example.com", req["email"])
			assert.Equal(t, "fixture-password", req["password"])
			return selfContractResponse(200, `{"code":0,"data":{"biz_code":0,"biz_data":{"user":{"token":"fixture-session-token-not-a-real-credential"}}}}`)
		}
		assert.Equal(t, "ds2.example", r.URL.Host)
		assert.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "Bearer fixture-session-token-not-a-real-credential", r.Header.Get("Authorization"))
		return selfContractResponse(200, `{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`)
	})
	platform := selfContractDS2Session(t, uid)
	start := selfContractDS2Start(platform, `{"proxy":"direct","models":["deepseek-chat"]}`)
	require.Contains(t, start.Body.String(), `"success":true`)
	var response struct {
		Data struct {
			State   string `json:"sessionId"`
			AuthURL string `json:"authUrl"`
			Flow    string `json:"flow"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(start.Body.Bytes(), &response))
	assert.Equal(t, "account_login", response.Data.Flow)
	cookies := start.Result().Cookies()
	require.NotEmpty(t, cookies)
	assert.True(t, cookies[0].HttpOnly)
	assert.True(t, cookies[0].Secure)
	assert.Equal(t, http.SameSiteStrictMode, cookies[0].SameSite)
	var session model.SelfOAuthSession
	require.NoError(t, model.DB.Where("state = ?", ds2SessionKey(response.Data.State)).First(&session).Error)
	assert.NotEqual(t, response.Data.State, session.State)
	assert.True(t, common.IsEncryptedSecret(session.Refresh))
	page := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(page)
	c.Request = httptest.NewRequest(http.MethodGet, response.Data.AuthURL, nil)
	c.Request.AddCookie(cookies[0])
	c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: platform.SID + ".fixture-refresh"})
	SelfDs2ApiOAuthAuthorizePage(c)
	require.Equal(t, 200, page.Code)
	// no-referrer makes native browser form POSTs send Origin: null. Keep
	// the origin while stripping the path/query containing the login state.
	assert.Equal(t, "strict-origin", page.Header().Get("Referrer-Policy"))
	assert.Equal(t, "no-store", page.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", page.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'", page.Header().Get("Content-Security-Policy"))
	assert.Contains(t, page.Body.String(), `<form method="post" action="/api/user/auth/ds2api/credentials">`)
	assert.Contains(t, page.Body.String(), `href="https://chat.deepseek.com" target="_blank" rel="noopener noreferrer"`)
	assert.Contains(t, page.Body.String(), "不是 DeepSeek OAuth")
	assert.NotContains(t, page.Body.String(), "localStorage")
	assert.NotContains(t, page.Body.String(), "javascript:")
	match := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	require.Len(t, match, 2)
	form := url.Values{"state": {response.Data.State}, "csrf": {match[1]}, "account": {"test@example.com"}, "password": {"fixture-password"}}
	finish := func(origin string, form url.Values) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "https://newapi.example/api/user/auth/ds2api/credentials", strings.NewReader(form.Encode()))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		c.Request.Header.Set("Origin", origin)
		c.Request.Header.Set("Referer", "https://newapi.example/")
		c.Request.Header.Set("Sec-Fetch-Site", "same-origin")
		// TLS terminates at ingress and the load balancer replaces Host.
		// Neither that internal host nor forwarded headers establish trust.
		c.Request.TLS = nil
		c.Request.Host = "node.internal:3000"
		c.Request.Header.Set("X-Forwarded-Proto", "https")
		c.Request.Header.Set("X-Forwarded-Host", origin)
		c.Request.AddCookie(cookies[0])
		c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: platform.SID + ".fixture-refresh"})
		SelfDs2ApiOAuthCallback(c)
		return recorder
	}
	for _, origin := range []string{"https://attacker.example", "null", "", "https://sibling.newapi.example", "https://newapi.example.attacker.example", "http://newapi.example", "https://newapi.example:8443"} {
		t.Run("reject_origin_"+origin, func(t *testing.T) {
			rejected := finish(origin, form)
			assert.Equal(t, http.StatusForbidden, rejected.Code)
			assert.Equal(t, "Invalid login request origin", rejected.Body.String())
			assert.Equal(t, "no-referrer", rejected.Header().Get("Referrer-Policy"))
			assert.Zero(t, calls)
			require.NoError(t, model.DB.Where("state = ?", session.State).First(&session).Error)
			assert.Equal(t, "pending", session.Status)
			assert.True(t, common.IsEncryptedSecret(session.Refresh))
		})
	}
	badCSRF := form.Get("csrf")
	form.Set("csrf", "invalid")
	rejected := finish("https://newapi.example", form)
	assert.Equal(t, 403, rejected.Code)
	assert.Zero(t, calls)
	form.Set("csrf", badCSRF)
	finished := finish("https://newapi.example", form)
	require.Equal(t, 200, finished.Code, finished.Body.String())
	assert.NotContains(t, finished.Body.String(), "fixture-session-token")
	assert.Equal(t, 2, calls)
	var channel model.Channel
	require.NoError(t, model.DB.Where("owner_id = ?", uid).Last(&channel).Error)
	assert.Zero(t, channel.Visibility)
	assert.Equal(t, "deepseek-chat", channel.Models)
	assert.True(t, common.IsEncryptedSecret(channel.Key))
	assert.Equal(t, "fixture-session-token-not-a-real-credential", model.DecryptChannelKey(channel.Type, channel.Key))
	assert.NotContains(t, channel.OtherInfo, "fixture-password")
	require.NoError(t, model.DB.Where("state = ?", session.State).First(&session).Error)
	assert.Equal(t, "done", session.Status)
	assert.Empty(t, session.Refresh)
	replay := finish("https://newapi.example", form)
	assert.Equal(t, 410, replay.Code)
	assert.Equal(t, 2, calls)
	pollPath := "/poll?sessionId=" + url.QueryEscape(response.Data.State)
	foreign := selfContractRequest(uid+1, http.MethodGet, pollPath, "", SelfDs2ApiOAuthPoll)
	assert.Contains(t, foreign.Body.String(), `"success":false`)
	for range 2 {
		poll := selfContractRequest(uid, http.MethodGet, pollPath, "", SelfDs2ApiOAuthPoll)
		assert.Contains(t, poll.Body.String(), `"done":true`)
		assert.NotContains(t, poll.Body.String(), "fixture-session-token")
	}
	var count int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("owner_id = ?", uid).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, model.DB.Model(&session).Update("created_at", time.Now().Add(-2*ds2LoginTTL)).Error)
	expired := selfContractRequest(uid, http.MethodGet, pollPath, "", SelfDs2ApiOAuthPoll)
	assert.Contains(t, expired.Body.String(), `"success":false`)
}

// An explicit empty models selection is a user error and must fail without
// creating a channel. Omitting the field keeps the legacy whole-catalog default.
func TestDS2ModelSelectionEmptyVsOmitted(t *testing.T) {
	cases := []struct {
		name              string
		startBody         string
		wantCode          int
		wantChannel       bool
		wantChannelModels string
		wantErrSubstr     string
	}{
		{name: "explicit_empty_fails", startBody: `{"proxy":"direct","models":[]}`, wantCode: http.StatusBadGateway, wantChannel: false, wantErrSubstr: "Select at least one model"},
		{name: "omitted_defaults_to_catalog", startBody: `{"proxy":"direct"}`, wantCode: http.StatusOK, wantChannel: true, wantChannelModels: "deepseek-chat,deepseek-reasoner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uid := setupChannelCreateTest(t)
			require.NoError(t, model.DB.AutoMigrate(&model.SelfOAuthSession{}, &model.SelfAccountBinding{}))
			oldAddress, oldSecret, oldConfigured := system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured
			system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = "https://newapi.example", "ds2-modelsel-test-key", true
			t.Cleanup(func() {
				system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = oldAddress, oldSecret, oldConfigured
			})
			t.Setenv("DS2API_BASE_URL", "https://ds2.example/v1")
			previousLimit := operation_setting.SelfChannelMaxPerUser
			operation_setting.SelfChannelMaxPerUser = 20
			t.Cleanup(func() { operation_setting.SelfChannelMaxPerUser = previousLimit })
			setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "chat.deepseek.com" {
					return selfContractResponse(200, `{"code":0,"data":{"biz_code":0,"biz_data":{"user":{"token":"fixture-modelsel-token"}}}}`)
				}
				return selfContractResponse(200, `{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`)
			})
			platform := selfContractDS2Session(t, uid)
			start := selfContractDS2Start(platform, tc.startBody)
			require.Contains(t, start.Body.String(), `"success":true`)
			var response struct {
				Data struct {
					State string `json:"sessionId"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(start.Body.Bytes(), &response))
			var flow model.SelfOAuthSession
			require.NoError(t, model.DB.Where("state = ?", ds2SessionKey(response.Data.State)).First(&flow).Error)
			var options ds2LoginOptions
			require.NoError(t, common.UnmarshalJsonStr(common.DecryptSecretOrLegacy(flow.Refresh), &options))
			form := url.Values{"state": {response.Data.State}, "csrf": {options.CSRF}, "account": {"test@example.com"}, "password": {"fixture-password"}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "https://newapi.example/api/user/auth/ds2api/credentials", strings.NewReader(form.Encode()))
			c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			c.Request.Header.Set("Origin", "https://newapi.example")
			c.Request.AddCookie(start.Result().Cookies()[0])
			c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: platform.SID + ".fixture-refresh"})
			SelfDs2ApiOAuthCallback(c)
			require.Equal(t, tc.wantCode, recorder.Code, recorder.Body.String())
			var channel model.Channel
			channelErr := model.DB.Where("owner_id = ?", uid).Last(&channel).Error
			if tc.wantChannel {
				require.NoError(t, channelErr)
				assert.Equal(t, tc.wantChannelModels, channel.Models)
			} else {
				assert.Error(t, channelErr)
				assert.Contains(t, recorder.Body.String(), tc.wantErrSubstr)
				require.NoError(t, model.DB.Where("state = ?", flow.State).First(&flow).Error)
				assert.Equal(t, "failed", flow.Status)
			}
		})
	}
}

func TestDS2LoginFailuresDoNotCreateChannel(t *testing.T) {
	uid := setupChannelCreateTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SelfOAuthSession{}, &model.SelfAccountBinding{}))
	previousConfigured := common.CryptoSecretConfigured
	common.CryptoSecretConfigured = true
	t.Cleanup(func() { common.CryptoSecretConfigured = previousConfigured })
	previousAddress, previousSecret := system_setting.ServerAddress, common.CryptoSecret
	system_setting.ServerAddress = "https://newapi.example"
	common.CryptoSecret = "ds2-failure-test-key"
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress; common.CryptoSecret = previousSecret })
	t.Setenv("DS2API_BASE_URL", "")
	platform := selfContractDS2Session(t, uid)
	unconfigured := selfContractDS2Start(platform, `{}`)
	assert.Equal(t, 503, unconfigured.Code)
	t.Setenv("DS2API_BASE_URL", "https://ds2.example/v1")
	for _, tc := range []struct {
		name, body, message string
	}{
		{"missing_code", `{}`, "Upstream login failed or requires additional verification. Complete verification on the official website and retry"},
		{"generic_failure", `{"code":0,"data":{"biz_code":1,"biz_msg":"fixture-password-secret"}}`, "Upstream login failed or requires additional verification. Complete verification on the official website and retry"},
		{"verification_required", `{"code":0,"data":{"biz_code":11,"biz_msg":"fixture-password-secret","biz_data":{"user":{"token":"fixture-rejected-token"}}}}`, "DeepSeek requires verification on its official website before this account can be linked"},
		{"outer_failure", `{"code":1,"data":{"biz_code":11}}`, "Upstream login failed or requires additional verification. Complete verification on the official website and retry"},
		{"missing_token", `{"code":0,"data":{"biz_code":0,"biz_data":{}}}`, "Invalid upstream login response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			setSelfContractTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				return selfContractResponse(200, tc.body)
			})
			token, err := service.LoginDS2Account(t.Context(), "test@example.com", "fixture-password-secret", "")
			require.EqualError(t, err, tc.message)
			assert.Empty(t, token)
			assert.Equal(t, 1, calls)
		})
	}
	start := selfContractDS2Start(platform, `{"proxy":"direct"}`)
	require.Contains(t, start.Body.String(), `"success":true`)
	var response struct {
		Data struct {
			State string `json:"sessionId"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(start.Body.Bytes(), &response))
	var flow model.SelfOAuthSession
	require.NoError(t, model.DB.Where("state = ?", ds2SessionKey(response.Data.State)).First(&flow).Error)
	var options ds2LoginOptions
	require.NoError(t, common.UnmarshalJsonStr(common.DecryptSecretOrLegacy(flow.Refresh), &options))
	calls := 0
	setSelfContractTransport(t, func(r *http.Request) (*http.Response, error) {
		calls++
		assert.Equal(t, "chat.deepseek.com", r.URL.Host)
		return selfContractResponse(200, `{"code":0,"data":{"biz_code":11,"biz_msg":"fixture-password-secret","biz_data":{"user":{"token":"fixture-rejected-token"}}}}`)
	})
	form := url.Values{"state": {response.Data.State}, "csrf": {options.CSRF}, "account": {"test@example.com"}, "password": {"fixture-password-secret"}}
	verification := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(verification)
	c.Request = httptest.NewRequest(http.MethodPost, "https://newapi.example/api/user/auth/ds2api/credentials", strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request.Header.Set("Origin", "https://newapi.example")
	c.Request.AddCookie(start.Result().Cookies()[0])
	c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: platform.SID + ".fixture-refresh"})
	SelfDs2ApiOAuthCallback(c)
	assert.Equal(t, http.StatusBadRequest, verification.Code)
	assert.Contains(t, verification.Body.String(), "DeepSeek")
	assert.Equal(t, "no-referrer", verification.Header().Get("Referrer-Policy"))
	assert.Equal(t, "no-store", verification.Header().Get("Cache-Control"))
	assert.Equal(t, 1, calls)
	require.NoError(t, model.DB.Where("state = ?", flow.State).First(&flow).Error)
	assert.Equal(t, "failed", flow.Status)
	assert.Equal(t, "DeepSeek requires verification on its official website before this account can be linked", flow.Err)
	assert.Empty(t, flow.Access)
	assert.Empty(t, flow.Refresh)
	for _, target := range []any{&model.Channel{}, &model.SelfAccountBinding{}, &model.Ability{}} {
		var count int64
		require.NoError(t, model.DB.Model(target).Count(&count).Error)
		assert.Zero(t, count)
	}
	recorder := httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/callback?token=fixture-query-token", nil)
	SelfDs2ApiOAuthCallback(c)
	assert.Equal(t, 405, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "fixture-query-token")
	var count int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestSelfBindingOwnershipAndCredentialRedaction(t *testing.T) {
	uid := setupChannelCreateTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SelfAccountBinding{}))
	previous := common.CryptoSecret
	common.CryptoSecret = "binding-owner-test-key"
	t.Cleanup(func() { common.CryptoSecret = previous })
	channel := &model.Channel{OwnerId: uid + 1, Name: "foreign", Type: 1, Key: "fixture", Group: "default", Models: "deepseek"}
	require.NoError(t, model.DB.Create(channel).Error)
	binding := &model.SelfAccountBinding{UserId: uid, Provider: "ds2api", Status: "pending"}
	require.NoError(t, binding.EncodeAccounts([]model.SelfAccountCred{{Account: "test@example.com", Password: "fixture-password"}}))
	require.NoError(t, model.CreateSelfBinding(binding))
	require.ErrorContains(t, model.MarkBindingStatus(binding.Id, "ready", channel.Id, "", ""), "ownership")
	require.NoError(t, model.DB.First(binding, binding.Id).Error)
	assert.Zero(t, binding.ChannelId)
	for _, raw := range []string{"malformed-secret-input", "account----"} {
		_, err := model.ParseAccountOrTokenLines(raw)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), raw)
	}
	_, err := model.ParseGatewayKeys("fixture-secret-value")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "fixture-secret-value")
	_ = bytes.MinRead
	_ = fmt.Sprintf
}

func TestRaccoonUnavailableNeverExchangesCodeOrReflectsInput(t *testing.T) {
	for _, tc := range []struct {
		handler gin.HandlerFunc
		method  string
		code    int
	}{
		{SelfRaccoonOAuthStart, http.MethodPost, http.StatusServiceUnavailable},
		{SelfRaccoonOAuthPoll, http.MethodGet, http.StatusServiceUnavailable},
		{SelfRaccoonOAuthCallback, http.MethodGet, http.StatusGone},
	} {
		w := selfContractRequest(7, tc.method, "/auth?state=attacker&code=%3Cscript%3E", "{}", tc.handler)
		assert.Equal(t, tc.code, w.Code)
		assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		assert.NotContains(t, w.Body.String(), "<script>")
		assert.NotContains(t, w.Body.String(), "authUrl")
	}
}

func TestDS2CancellationBlocksBrowserRedemption(t *testing.T) {
	uid := setupChannelCreateTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SelfOAuthSession{}))
	platform := selfContractDS2Session(t, uid)
	oldAddress, oldSecret, oldConfigured := system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured
	system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = "https://newapi.example", "ds2-cancel-fixture", true
	t.Cleanup(func() {
		system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = oldAddress, oldSecret, oldConfigured
	})
	t.Setenv("DS2API_BASE_URL", "https://ds2.example/v1")
	start := selfContractDS2Start(platform, `{"proxy":"direct"}`)
	require.Contains(t, start.Body.String(), `"success":true`)
	var response struct {
		Data struct {
			State string `json:"sessionId"`
			URL   string `json:"authUrl"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(start.Body.Bytes(), &response))
	cancel := selfContractRequest(uid, http.MethodPost, "/cancel", fmt.Sprintf(`{"sessionId":%q}`, response.Data.State), func(c *gin.Context) {
		c.Set("session_id", platform.SID)
		c.Set("auth_version", platform.UserAuthVersion)
		c.Set("session_version", platform.Version)
		c.Request.Header.Set("Sec-Fetch-Site", "same-origin")
		SelfDs2ApiOAuthCancel(c)
	})
	require.Contains(t, cancel.Body.String(), `"status":"cancelled"`)
	var flow model.SelfOAuthSession
	require.NoError(t, model.DB.Where("state = ?", ds2SessionKey(response.Data.State)).First(&flow).Error)
	assert.Empty(t, flow.Refresh)
	assert.Empty(t, flow.Access)
	page := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(page)
	c.Request = httptest.NewRequest(http.MethodGet, response.Data.URL, nil)
	c.Request.AddCookie(start.Result().Cookies()[0])
	c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: platform.SID + ".fixture-refresh"})
	SelfDs2ApiOAuthAuthorizePage(c)
	assert.Equal(t, http.StatusGone, page.Code)
}

func TestDS2PoolActivationPreservesOwnerManualDisable(t *testing.T) {
	uid := setupChannelCreateTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SelfOAuthSession{}, &model.SelfAccountBinding{}))
	platform := selfContractDS2Session(t, uid)
	oldAddress, oldSecret, oldConfigured := system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured
	system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = "https://newapi.example", "ds2-owner-disable-test", true
	t.Cleanup(func() {
		system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = oldAddress, oldSecret, oldConfigured
	})
	t.Setenv("DS2API_BASE_URL", "https://ds2.example/v1")
	start := selfContractDS2Start(platform, `{"proxy":"direct"}`)
	require.Contains(t, start.Body.String(), `"success":true`)
	var response struct {
		Data struct {
			State string `json:"sessionId"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(start.Body.Bytes(), &response))
	var flow model.SelfOAuthSession
	require.NoError(t, model.DB.Where("state = ?", ds2SessionKey(response.Data.State)).First(&flow).Error)
	var options ds2LoginOptions
	require.NoError(t, common.UnmarshalJsonStr(common.DecryptSecretOrLegacy(flow.Refresh), &options))
	require.NoError(t, model.EnsureProxyPoolSchema())
	endpoint := model.ProxyEndpoint{UserId: uid, Name: "ds2-owner-disable", Kind: "http", Url: "http://127.0.0.1:1", Enabled: true, Public: true}
	require.NoError(t, model.DB.Create(&endpoint).Error)
	options.Proxy, options.ProxyPolicy = "", &dto.ChannelProxyPolicy{Mode: "public_pool"}
	raw, err := common.Marshal(options)
	require.NoError(t, err)
	encrypted, err := common.EncryptSecret(string(raw))
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&flow).Update("refresh", encrypted).Error)
	proxy, err := model.ResolveStableChannelProxy(uid, 0, options.EgressKey, dto.ChannelSettings{Proxy: options.Proxy, ProxyPolicy: options.ProxyPolicy})
	require.NoError(t, err)
	setSelfContractProxyTransport(t, proxy, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "chat.deepseek.com" {
			return selfContractResponse(200, `{"code":0,"data":{"biz_code":0,"biz_data":{"user":{"token":"owner-disable-token"}}}}`)
		}
		return selfContractResponse(200, `{"data":[{"id":"deepseek-chat"}]}`)
	})
	intercepted := false
	const hook = "test:ds2-owner-disable-at-bind"
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		if intercepted || tx.Statement.Table != "channels" || strings.Join(tx.Statement.Selects, ",") != "id, owner_id" {
			return
		}
		channel, ok := tx.Statement.Dest.(*model.Channel)
		if !ok || channel.OwnerId != uid {
			return
		}
		intercepted = true
		require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(hook)) })
	form := url.Values{"state": {response.Data.State}, "csrf": {options.CSRF}, "account": {"owner@example.com"}, "password": {"fixture-password"}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "https://newapi.example/api/user/auth/ds2api/credentials", strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request.Header.Set("Origin", "https://newapi.example")
	c.Request.AddCookie(start.Result().Cookies()[0])
	c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: platform.SID + ".fixture-refresh"})
	SelfDs2ApiOAuthCallback(c)
	require.True(t, intercepted)
	assert.NotEqual(t, http.StatusOK, recorder.Code)
	var channel model.Channel
	require.NoError(t, model.DB.Where("owner_id = ?", uid).Last(&channel).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status)
	var enabled int64
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ? AND enabled = ?", channel.Id, true).Count(&enabled).Error)
	assert.Zero(t, enabled)
}

package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/freepi"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type freePiTestTransport func(*http.Request) (*http.Response, error)

func (f freePiTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFreePiAuthorizationDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			prevDB, prevLogDB, prevType := model.DB, model.LOG_DB, common.MainDatabaseType()
			prevRedis, prevMemory := common.RedisEnabled, common.MemoryCacheEnabled
			prevCrypto, prevConfigured, prevSessionSecret := common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret
			model.DB, model.LOG_DB = db, db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			common.RedisEnabled, common.MemoryCacheEnabled = false, false
			common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret = "freepi-test-encryption", true, "freepi-test-session"
			t.Cleanup(func() {
				model.DB, model.LOG_DB = prevDB, prevLogDB
				common.SetMainDatabaseType(prevType)
				common.RedisEnabled, common.MemoryCacheEnabled = prevRedis, prevMemory
				common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret = prevCrypto, prevConfigured, prevSessionSecret
				assert.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.Channel{}, &model.Ability{}, &model.SelfAccountBinding{}, &model.AuditLog{}))
			var version string
			versionSQL := "SELECT version()"
			if dialect == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("database version: %s", version)
			name := "freepi-" + uuid.NewString()[:8]
			user := model.User{Username: name, AffCode: name, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, Group: "default"}
			require.NoError(t, db.Create(&user).Error)
			session := model.UserSession{SID: uuid.NewString(), UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: name, LoginMethod: "password"}
			require.NoError(t, db.Create(&session).Error)
			identity := model.AuthSessionIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}
			t.Cleanup(func() {
				var channels []model.Channel
				require.NoError(t, db.Where("owner_id = ?", user.Id).Find(&channels).Error)
				for _, channel := range channels {
					assert.NoError(t, db.Where("channel_id = ?", channel.Id).Delete(&model.Ability{}).Error)
				}
				for _, table := range []any{&model.AuthFlow{}, &model.SelfAccountBinding{}, &model.AuditLog{}} {
					assert.NoError(t, db.Where("user_id = ?", user.Id).Delete(table).Error)
				}
				assert.NoError(t, db.Where("owner_id = ?", user.Id).Delete(&model.Channel{}).Error)
				assert.NoError(t, db.Where("user_id = ?", user.Id).Delete(&model.UserSession{}).Error)
				assert.NoError(t, db.Unscoped().Delete(&user).Error)
			})
			service.InitHttpClient()
			client := service.GetHttpClient()
			originalClient := *client
			t.Cleanup(func() { *client = originalClient })
			pollCalls, consentCalls := 0, 0
			consentRequired, maliciousVerification, slowDown := false, false, false
			client.Transport = freePiTestTransport(func(request *http.Request) (*http.Response, error) {
				assert.Equal(t, "api.freepi.ai", request.URL.Host)
				text, status := "", http.StatusOK
				switch request.URL.Path {
				case "/client-version":
					text = `{"latest":"0.6.22","models":[{"id":"test-freepi-model","free":true},{"id":"not-available","unavailable":true}]}`
				case "/auth/github/device":
					uri := "https://github.com/login/device"
					if maliciousVerification {
						uri = "https://attacker.invalid/login/device"
					}
					text = fmt.Sprintf(`{"session_id":"private-upstream-device-session","user_code":"ABCD-EFGH","verification_uri":%q,"interval":5,"expires_in":900}`, uri)
				case "/auth/token":
					pollCalls++
					if slowDown {
						text = `{"status":"slow_down","retry_after":75}`
					} else if consentRequired {
						status, text = 200, `{"code":"consent_required"}`
					} else {
						text = `{"token":"private-upstream-account-token"}`
					}
				case "/auth/consent":
					consentCalls++
					consentRequired = false
					status, text = http.StatusNoContent, ""
				case "/me":
					assert.Equal(t, "Bearer private-upstream-account-token", request.Header.Get("Authorization"))
					text = `{"handle":"account-owner"}`
				default:
					return nil, errors.New("unexpected upstream request")
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(text)), Request: request}, nil
			})
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("id", user.Id)
				c.Set("session_id", session.SID)
				c.Set("auth_version", int64(1))
				c.Set("session_version", int64(1))
				c.Set("role", common.RoleCommonUser)
			})
			router.POST("/start", StartFreePiOAuth)
			router.POST("/poll", PollFreePiOAuth)
			router.POST("/consent", ConsentFreePiOAuth)
			router.POST("/cancel", CancelFreePiOAuth)
			request := func(path, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-FreePi-Action", "authorize")
				r.Header.Set("Sec-Fetch-Site", "same-origin")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				return w
			}
			start := func(body string) string {
				w := request("/start", body)
				require.Equal(t, 200, w.Code, w.Body.String())
				var response struct {
					Data struct {
						State string `json:"state"`
					} `json:"data"`
				}
				require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
				require.Len(t, response.Data.State, 43)
				assert.NotContains(t, w.Body.String(), "private-upstream")
				assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				return response.Data.State
			}
			ready := func(state string) {
				require.NoError(t, model.WithFreePiAuthFlow(state, identity, func(_ *gorm.DB, s *model.FreePiAuthState) error { s.NextPollAt = 0; return nil }))
			}

			t.Run("private_success_and_idempotent_replay_do_not_reveal_credentials", func(t *testing.T) {
				state := start(`{"displayName":"my-freepi"}`)
				ready(state)
				w := request("/poll", fmt.Sprintf(`{"state":%q}`, state))
				require.Equal(t, 200, w.Code, w.Body.String())
				assert.Contains(t, w.Body.String(), `"done":true`)
				assert.NotContains(t, w.Body.String(), "private-upstream")
				var channel model.Channel
				require.NoError(t, db.Where("owner_id = ?", user.Id).Last(&channel).Error)
				assert.Zero(t, channel.Visibility)
				assert.Equal(t, constant.ChannelTypeFreePi, channel.Type)
				assert.Equal(t, "test-freepi-model", channel.Models)
				assert.True(t, common.IsEncryptedSecret(channel.Key))
				assert.Equal(t, "private-upstream-account-token", model.DecryptChannelKey(channel.Type, channel.Key))
				var binding model.SelfAccountBinding
				require.NoError(t, db.Where("channel_id = ?", channel.Id).First(&binding).Error)
				assert.True(t, common.IsEncryptedSecret(binding.AccountsEnc))
				assert.NotContains(t, binding.AccountMask, "private-upstream")
				before := pollCalls
				w = request("/poll", fmt.Sprintf(`{"state":%q}`, state))
				require.Equal(t, 200, w.Code)
				assert.Equal(t, before, pollCalls)
				var count int64
				require.NoError(t, db.Model(&model.Channel{}).Where("owner_id = ?", user.Id).Count(&count).Error)
				assert.EqualValues(t, 1, count)
				adaptor := &freepi.Adaptor{}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: channel.Type, ChannelBaseUrl: *channel.BaseURL, ApiKey: model.DecryptChannelKey(channel.Type, channel.Key)}, RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI}
				h1, h2 := make(http.Header), make(http.Header)
				require.NoError(t, adaptor.SetupRequestHeader(c, &h1, info))
				require.NoError(t, adaptor.SetupRequestHeader(c, &h2, info))
				assert.Equal(t, h1.Get("x-session-id"), h2.Get("x-session-id"))
				assert.NotEqual(t, h1.Get("x-turn-id"), h2.Get("x-turn-id"))
				assert.Equal(t, "0.6.22", h1.Get("x-client-version"))
				info.HeadersOverride = map[string]any{"Authorization": "Bearer attacker"}
				require.Error(t, adaptor.SetupRequestHeader(c, &h1, info))
				info.HeadersOverride = nil
				info.ChannelBaseUrl = "https://attacker.invalid"
				_, err := adaptor.GetRequestURL(info)
				require.Error(t, err)
				info.ChannelBaseUrl = *channel.BaseURL
				resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"verified-response","object":"chat.completion","model":"test-freepi-model","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":3,"total_tokens":12}}`))}
				usage, apiErr := adaptor.DoResponse(c, resp, info)
				require.Nil(t, apiErr)
				actual, ok := usage.(*dto.Usage)
				require.True(t, ok)
				assert.Equal(t, 12, actual.TotalTokens)
				assert.Equal(t, 9, actual.PromptTokens)
				assert.Equal(t, 3, actual.CompletionTokens)
			})
			t.Run("consent_requires_explicit_acceptance_and_public_sharing_is_explicit", func(t *testing.T) {
				assert.Equal(t, 400, request("/start", `{"public":true}`).Code)
				consentRequired = true
				state := start(`{"public":true,"confirmPublic":true}`)
				ready(state)
				w := request("/poll", fmt.Sprintf(`{"state":%q}`, state))
				require.Equal(t, 200, w.Code)
				assert.Contains(t, w.Body.String(), "consent_required")
				assert.Zero(t, consentCalls)
				assert.Equal(t, 400, request("/consent", fmt.Sprintf(`{"state":%q,"accepted":false,"consentVersion":"4"}`, state)).Code)
				assert.Zero(t, consentCalls)
				require.Equal(t, 200, request("/consent", fmt.Sprintf(`{"state":%q,"accepted":true,"consentVersion":"4"}`, state)).Code)
				require.Equal(t, 1, consentCalls)
				ready(state)
				require.Equal(t, 200, request("/poll", fmt.Sprintf(`{"state":%q}`, state)).Code)
				var channel model.Channel
				require.NoError(t, db.Where("owner_id = ?", user.Id).Last(&channel).Error)
				assert.Equal(t, 1, channel.Visibility)
			})
			t.Run("server_slowdown_prevents_premature_upstream_poll", func(t *testing.T) {
				state := start(`{}`)
				ready(state)
				slowDown = true
				w := request("/poll", fmt.Sprintf(`{"state":%q}`, state))
				require.Equal(t, 200, w.Code)
				assert.Contains(t, w.Body.String(), `"interval":75`)
				before := pollCalls
				w = request("/poll", fmt.Sprintf(`{"state":%q}`, state))
				require.Equal(t, 200, w.Code)
				assert.Equal(t, before, pollCalls)
				slowDown = false
				require.Equal(t, 200, request("/cancel", fmt.Sprintf(`{"state":%q}`, state)).Code)
			})
			t.Run("expiry_owner_session_and_cancellation_prevent_redemption", func(t *testing.T) {
				state := start(`{}`)
				wrong := identity
				wrong.SessionID = "not-owner-session"
				require.Error(t, model.WithFreePiAuthFlow(state, wrong, func(*gorm.DB, *model.FreePiAuthState) error { t.Fatal("wrong owner executed action"); return nil }))
				wrong = identity
				wrong.UserID++
				require.Error(t, model.WithFreePiAuthFlow(state, wrong, func(*gorm.DB, *model.FreePiAuthState) error { t.Fatal("wrong user executed action"); return nil }))
				ready(state)
				before := pollCalls
				require.Equal(t, 200, request("/cancel", fmt.Sprintf(`{"state":%q}`, state)).Code)
				assert.Equal(t, 400, request("/poll", fmt.Sprintf(`{"state":%q}`, state)).Code)
				assert.Equal(t, before, pollCalls)
				state = start(`{}`)
				require.NoError(t, db.Model(&model.AuthFlow{}).Where("user_id = ? AND consumed_at IS NULL", user.Id).Update("expires_at", time.Now().Add(-time.Minute)).Error)
				assert.Equal(t, 400, request("/poll", fmt.Sprintf(`{"state":%q}`, state)).Code)
				assert.Equal(t, before, pollCalls)
			})
			t.Run("rollback_removes_channel_abilities_binding_and_consumption_together", func(t *testing.T) {
				state := start(`{}`)
				var before int64
				require.NoError(t, db.Model(&model.Channel{}).Where("owner_id = ?", user.Id).Count(&before).Error)
				err := model.WithFreePiAuthFlow(state, identity, func(tx *gorm.DB, s *model.FreePiAuthState) error {
					require.NoError(t, model.CompleteFreePiChannel(tx, s, "rollback-token", "rollback-owner", constant.ChannelTypeFreePi))
					return errors.New("forced persistence failure")
				})
				require.Error(t, err)
				var after int64
				require.NoError(t, db.Model(&model.Channel{}).Where("owner_id = ?", user.Id).Count(&after).Error)
				assert.Equal(t, before, after)
				require.NoError(t, model.WithFreePiAuthFlow(state, identity, func(_ *gorm.DB, s *model.FreePiAuthState) error {
					assert.Equal(t, "pending", s.Status)
					s.Status = "cancelled"
					return nil
				}))
			})
			t.Run("csrf_unencrypted_storage_redirect_and_revoked_session_fail_closed", func(t *testing.T) {
				r := httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(`{}`))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-FreePi-Action", "authorize")
				r.Header.Set("Sec-Fetch-Site", "cross-site")
				r.Header.Set("Origin", "https://attacker.invalid")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				assert.Equal(t, 403, w.Code)
				common.CryptoSecretConfigured = false
				assert.Equal(t, 503, request("/start", `{}`).Code)
				common.CryptoSecretConfigured = true
				maliciousVerification = true
				assert.Equal(t, 502, request("/start", `{}`).Code)
				maliciousVerification = false
				state := start(`{}`)
				require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", session.SID).Update("status", model.UserSessionStatusRevoked).Error)
				assert.Equal(t, 401, request("/poll", fmt.Sprintf(`{"state":%q}`, state)).Code)
			})
		})
	}
}

func TestExplicitEncryptedChannelKeyFailsClosed(t *testing.T) {
	oldSecret, oldConfigured := common.CryptoSecret, common.CryptoSecretConfigured
	common.CryptoSecret, common.CryptoSecretConfigured = "explicit-channel-test", true
	t.Cleanup(func() { common.CryptoSecret, common.CryptoSecretConfigured = oldSecret, oldConfigured })
	encrypted, err := common.EncryptSecret("private-gateway-token")
	require.NoError(t, err)
	for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeFreePi} {
		assert.Equal(t, "private-gateway-token", model.DecryptChannelKey(channelType, encrypted))
		assert.Empty(t, model.DecryptChannelKey(channelType, "enc:v1:malformed-ciphertext"))
		for _, multi := range []bool{false, true} {
			channel := model.Channel{Type: channelType, Key: "enc:v1:malformed-ciphertext", ChannelInfo: model.ChannelInfo{IsMultiKey: multi}}
			key, _, keyErr := channel.GetNextEnabledKey()
			assert.Empty(t, key)
			assert.NotNil(t, keyErr, "invalid encrypted credentials must stop relay")
		}
	}
	assert.Equal(t, "ordinary-openai-api-key", model.DecryptChannelKey(constant.ChannelTypeOpenAI, "ordinary-openai-api-key"))
	common.CryptoSecretConfigured = false
	assert.Empty(t, model.DecryptChannelKey(constant.ChannelTypeOpenAI, encrypted))
}

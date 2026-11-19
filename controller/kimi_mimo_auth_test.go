package controller

import (
	"context"
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
	"github.com/QuantumNous/new-api/relay/channel/desktopaccount"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
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

func TestDesktopAccountsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN unavailable: real MySQL matrix blocked")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN unavailable: real PostgreSQL matrix blocked")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			oldDB, oldLog, oldType := model.DB, model.LOG_DB, common.MainDatabaseType()
			oldRedis, oldMemory := common.RedisEnabled, common.MemoryCacheEnabled
			oldCrypto, oldConfigured, oldSession := common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret
			oldEnabled, oldLimit := operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser
			model.DB, model.LOG_DB = db, db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			common.RedisEnabled, common.MemoryCacheEnabled = false, false
			common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret = "desktop-account-test-key", true, "desktop-account-test-session"
			operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = true, 30
			t.Cleanup(func() {
				model.DB, model.LOG_DB = oldDB, oldLog
				common.SetMainDatabaseType(oldType)
				common.RedisEnabled, common.MemoryCacheEnabled = oldRedis, oldMemory
				common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret = oldCrypto, oldConfigured, oldSession
				operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = oldEnabled, oldLimit
				assert.NoError(t, sqlDB.Close())
			})
			for range 2 {
				require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.Channel{}, &model.Ability{}, &model.Option{}))
			}
			versionSQL := "SELECT version()"
			if dialect == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("%s version %s", dialect, version)
			name := "desktop-" + uuid.NewString()[:8]
			user := model.User{Username: name, AffCode: name, Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AuthVersion: 1, Group: "default"}
			require.NoError(t, db.Create(&user).Error)
			platform := model.UserSession{SID: uuid.NewString(), UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: uuid.NewString(), LoginMethod: "password"}
			require.NoError(t, db.Create(&platform).Error)
			identity := model.AuthSessionIdentity{UserID: user.Id, SessionID: platform.SID, UserAuthVersion: 1, SessionVersion: 1}
			t.Cleanup(func() {
				var channels []model.Channel
				require.NoError(t, db.Where("owner_id = ?", user.Id).Find(&channels).Error)
				for _, ch := range channels {
					assert.NoError(t, db.Where("channel_id = ?", ch.Id).Delete(&model.Ability{}).Error)
				}
				assert.NoError(t, db.Where("owner_id = ?", user.Id).Delete(&model.Channel{}).Error)
				assert.NoError(t, db.Where("user_id = ?", user.Id).Delete(&model.AuthFlow{}).Error)
				assert.NoError(t, db.Where("user_id = ?", user.Id).Delete(&model.UserSession{}).Error)
				assert.NoError(t, db.Unscoped().Delete(&user).Error)
			})
			request := func(handler gin.HandlerFunc, method, body, origin string, id model.AuthSessionIdentity) *httptest.ResponseRecorder {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Set("id", id.UserID)
				c.Set("session_id", id.SessionID)
				c.Set("auth_version", id.UserAuthVersion)
				c.Set("session_version", id.SessionVersion)
				c.Request = httptest.NewRequest(method, "https://dashboard.test/api/channel/self/account", strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request.Header.Set("Origin", origin)
				handler(c)
				return recorder
			}
			service.InitHttpClient()
			client := service.GetHttpClient()
			oldClient := *client
			t.Cleanup(func() { *client = oldClient })
			pollCount, meCount, modelCount, relayCount := 0, 0, 0, 0
			pending, badURL, badRedirect, badKimi, failSession := false, false, false, false, false
			client.Transport = workBuddyTestTransport(func(r *http.Request) (*http.Response, error) {
				body := "{}"
				status := http.StatusOK
				h := make(http.Header)
				switch r.URL.Host + r.URL.Path {
				case "mimo-server-sgp.xiaomimimo.com/api/user/xiaomi/me":
					meCount++
					if strings.Contains(r.Header.Get("Cookie"), "serviceToken=mimo-session-fixture") {
						body = `{"code":0,"data":{"userId":"12345","region":"SGP"}}`
					} else {
						status = 302
						h.Set("Location", "https://account.xiaomi.com/pass/serviceLogin?sid=mimosgp&callback=https%3A%2F%2Fmimo-server-sgp.xiaomimimo.com%2Fapi%2Fsts")
					}
					assert.NotContains(t, r.Header.Get("Cookie"), "passToken")
				case "account.xiaomi.com/longPolling/loginUrl":
					assert.Contains(t, r.Header.Get("Cookie"), "deviceId=pc_")
					assert.Equal(t, "mimosgp", r.URL.Query().Get("sid"))
					body = `&&&START&&&{"code":0,"loginUrl":"https://account.xiaomi.com/pass/lp?ticket=public-login-ticket","lp":"https://account.xiaomi.com/longPolling/listen?ticket=private-poll-ticket","timeout":600}`
					if badURL {
						body = strings.ReplaceAll(body, "https://account.xiaomi.com/longPolling/listen", "https://attacker.invalid/leak")
					}
				case "account.xiaomi.com/longPolling/listen":
					pollCount++
					if pending {
						body = `&&&START&&&{"code":70016}`
					} else {
						body = `&&&START&&&{"userId":"12345","cUserId":"hashed-user","passToken":"private-xiaomi-pass-fixture"}`
					}
				case "account.xiaomi.com/pass/serviceLogin":
					assert.Contains(t, r.Header.Get("Cookie"), "passToken=private-xiaomi-pass-fixture")
					if failSession {
						status = 503
						break
					}
					status = 302
					h.Set("Location", "http://mimo-server-sgp.xiaomimimo.com/api/sts?ticket=private-sts-fixture")
					if badRedirect {
						h.Set("Location", "https://attacker.invalid/leak")
					}
				case "mimo-server-sgp.xiaomimimo.com/api/sts":
					assert.Equal(t, "https", r.URL.Scheme)
					assert.NotContains(t, r.Header.Get("Cookie"), "passToken")
					status = 302
					h.Set("Location", "/api/user/xiaomi/me")
					h.Add("Set-Cookie", "serviceToken=mimo-session-fixture; Path=/api; Secure; HttpOnly")
				case "agent-gw.kimi.com/coding/v1/models":
					modelCount++
					assert.Equal(t, "Bearer private-kimi-work-fixture", r.Header.Get("Authorization"))
					body = `{"data":[{"id":"k3-agent"},{"id":"k2d8-preview"}]}`
					if badKimi {
						status = 401
					}
				case "agent-gw.kimi.com/coding/v1/chat/completions":
					relayCount++
					assert.Equal(t, "Bearer private-kimi-work-fixture", r.Header.Get("Authorization"))
					assert.Empty(t, r.Header.Get("Cookie"))
					body = `{"choices":[]}`
				case "mimo-server-sgp.xiaomimimo.com/api/route/chat/completions":
					relayCount++
					assert.Empty(t, r.Header.Get("Authorization"))
					assert.Contains(t, r.Header.Get("Cookie"), "serviceToken=mimo-session-fixture")
					assert.NotContains(t, r.Header.Get("Cookie"), "passToken")
					assert.Equal(t, "mimocode-cli-free", r.Header.Get("X-Mimo-Source"))
					body = `{"choices":[]}`
				default:
					t.Errorf("unexpected outbound endpoint %s%s", r.URL.Host, r.URL.Path)
					return nil, service.ErrAccountProtocol
				}
				return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			decode := func(res *httptest.ResponseRecorder) map[string]any {
				require.Equal(t, 200, res.Code, res.Body.String())
				var envelope map[string]any
				require.NoError(t, common.Unmarshal(res.Body.Bytes(), &envelope))
				require.Equal(t, true, envelope["success"], res.Body.String())
				data, ok := envelope["data"].(map[string]any)
				require.True(t, ok, res.Body.String())
				return data
			}
			start := func() string {
				res := request(SelfMiMoOAuthStart, "POST", `{"proxyPolicy":{"mode":"direct"}}`, "https://dashboard.test", identity)
				data := decode(res)
				assert.Equal(t, "https://account.xiaomi.com/pass/lp?ticket=public-login-ticket", data["authUrl"])
				assert.NotContains(t, res.Body.String(), "private-poll-ticket")
				token := data["sessionId"].(string)
				require.Len(t, token, 43)
				return token
			}
			reset := func(token string) {
				require.NoError(t, model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error { s.NextPollAt = 0; return nil }))
			}
			poll := func(token string) *httptest.ResponseRecorder {
				return request(SelfMiMoOAuthPoll, "POST", fmt.Sprintf(`{"sessionId":%q}`, token), "https://dashboard.test", identity)
			}
			token := start()
			var flow model.AuthFlow
			require.NoError(t, db.Where("user_id = ?", user.Id).First(&flow).Error)
			assert.True(t, common.IsEncryptedSecret(flow.Payload))
			assert.NotContains(t, flow.Payload, "private-poll-ticket")
			pending = true
			data := decode(poll(token))
			assert.Equal(t, false, data["done"])
			tooEarly := poll(token)
			assert.Equal(t, 429, tooEarly.Code)
			pending = false
			reset(token)
			data = decode(poll(token))
			assert.Equal(t, true, data["done"])
			channelJSON := data["channel"].(map[string]any)
			assert.Equal(t, "", channelJSON["key"])
			assert.Equal(t, float64(constant.ChannelTypeMiMo), channelJSON["type"])
			assert.NotContains(t, fmt.Sprint(channelJSON), "private-xiaomi-pass")
			mimoChannelID := int(channelJSON["id"].(float64))
			channel, err := model.GetChannelById(mimoChannelID, true)
			require.NoError(t, err)
			assert.Equal(t, 0, channel.Visibility)
			assert.Equal(t, user.Id, channel.OwnerId)
			assert.True(t, common.IsEncryptedSecret(channel.Key))
			assert.NotContains(t, channel.Key, "private-xiaomi-pass")
			assert.Equal(t, "mimo-pro,mimo-flash", channel.Models)
			before := pollCount
			decode(poll(token))
			assert.Equal(t, before, pollCount, "completed retry must not redeem again")
			// The foreign initiating SID may not view or finish another ceremony.
			foreign := identity
			foreign.SessionID = uuid.NewString()
			stolen := request(SelfMiMoOAuthPoll, "POST", fmt.Sprintf(`{"sessionId":%q}`, token), "https://dashboard.test", foreign)
			assert.NotEqual(t, 200, stolen.Code)
			assert.Equal(t, before, pollCount)
			cancelToken := start()
			res := request(SelfMiMoOAuthCancel, "POST", fmt.Sprintf(`{"sessionId":%q}`, cancelToken), "https://dashboard.test", identity)
			assert.Equal(t, 200, res.Code)
			assert.Equal(t, 409, poll(cancelToken).Code)
			expiredToken := start()
			require.NoError(t, model.WithWorkBuddyAuthFlow(expiredToken, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
				var ticket service.MiMoTicket
				require.NoError(t, common.Unmarshal([]byte(s.UpstreamState), &ticket))
				ticket.Until = time.Now().Add(-time.Second).Unix()
				raw, e := common.Marshal(ticket)
				s.UpstreamState = string(raw)
				return e
			}))
			assert.Equal(t, 410, poll(expiredToken).Code)
			badURL = true
			res = request(SelfMiMoOAuthStart, "POST", `{"proxyPolicy":{"mode":"direct"}}`, "https://dashboard.test", identity)
			assert.Equal(t, 502, res.Code)
			badURL = false
			badRedirect = true
			retryToken := start()
			assert.Equal(t, 502, poll(retryToken).Code)
			redeemed := pollCount
			badRedirect = false
			reset(retryToken)
			decode(poll(retryToken))
			assert.Equal(t, redeemed, pollCount, "retry after session error must use encrypted redeemed passToken")
			importBody := `{"proxyPolicy":{"mode":"direct"},"credentials":{"source":"kimi-work-desktop","workToken":"private-kimi-work-fixture","userId":"kimi-user-fixture","deviceId":"local-device-fixture"}}`
			badKimi = true
			res = request(SelfImportKimiWork, "POST", importBody, "https://dashboard.test", identity)
			assert.Equal(t, 401, res.Code)
			badKimi = false
			data = decode(request(SelfImportKimiWork, "POST", importBody, "https://dashboard.test", identity))
			kimiJSON := data["channel"].(map[string]any)
			assert.Equal(t, float64(constant.ChannelTypeKimiWork), kimiJSON["type"])
			assert.Equal(t, "", kimiJSON["key"])
			assert.Equal(t, "k3-agent,k2d8-preview", kimiJSON["models"])
			kimiChannelID := int(kimiJSON["id"].(float64))
			assert.Equal(t, 2, modelCount)
			// A token or arbitrary source cannot masquerade as an approved local export.
			res = request(SelfImportKimiWork, "POST", strings.ReplaceAll(importBody, "kimi-work-desktop", "web-token"), "https://dashboard.test", identity)
			assert.NotEqual(t, 200, res.Code)
			assert.Equal(t, 2, modelCount)
			helper := request(SelfKimiWorkImportHelper, "GET", "", "https://dashboard.test", identity)
			require.Equal(t, 200, helper.Code)
			assert.Contains(t, helper.Header().Get("Content-Disposition"), "attachment")
			assert.Contains(t, helper.Body.String(), "SaveFileDialog")
			assert.NotContains(t, helper.Body.String(), "Write-Host $export")
			assert.NotContains(t, helper.Body.String(), "Invoke-WebRequest")
			assert.Contains(t, helper.Header().Get("Cache-Control"), "no-store")
			for _, id := range []int{mimoChannelID, kimiChannelID} {
				ch, e := model.GetChannelById(id, true)
				require.NoError(t, e)
				plain, e := common.DecryptSecret(ch.Key)
				require.NoError(t, e)
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: ch.Id, ChannelType: ch.Type, ApiKey: plain, ChannelBaseUrl: ch.GetBaseURL(), UpstreamModelName: strings.Split(ch.Models, ",")[0], ChannelSetting: ch.GetSetting()}, RelayMode: relayconstant.RelayModeChatCompletions}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest("POST", "https://dashboard.test/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
				// Public callers may differ; egress and credentials still belong to owner.
				info.UserId = user.Id + 100
				adaptor := &desktopaccount.Adaptor{}
				_, e = adaptor.GetRequestURL(info)
				require.NoError(t, e)
				response, e := adaptor.DoRequest(c, info, strings.NewReader(`{"messages":[]}`))
				require.NoError(t, e)
				require.NotNil(t, response)
				httpResponse, ok := response.(*http.Response)
				require.True(t, ok)
				require.NoError(t, httpResponse.Body.Close())
				info.ChannelBaseUrl = "https://attacker.invalid"
				_, e = adaptor.GetRequestURL(info)
				require.Error(t, e)
			}

			assert.Equal(t, 2, relayCount)
			// Renew MiMo on its original owner lease and persist only encrypted CAS data.
			ch, err := model.GetChannelById(mimoChannelID, true)
			require.NoError(t, err)
			plain, err := common.DecryptSecret(ch.Key)
			require.NoError(t, err)
			var stale service.MiMoCredential
			require.NoError(t, common.Unmarshal([]byte(plain), &stale))
			stale.ExpiresAt = time.Now().Add(-time.Minute).Unix()
			raw, err := common.Marshal(stale)
			require.NoError(t, err)
			encrypted, err := common.EncryptSecret(string(raw))
			require.NoError(t, err)
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", mimoChannelID).Update("key", encrypted).Error)
			renewedInfo := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: mimoChannelID, ChannelType: constant.ChannelTypeMiMo, ApiKey: string(raw), ChannelSetting: ch.GetSetting()}, RelayMode: relayconstant.RelayModeChatCompletions}
			rr := httptest.NewRecorder()
			cc, _ := gin.CreateTestContext(rr)
			cc.Request = httptest.NewRequest("POST", "https://dashboard.test/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
			failSession = true
			_, err = (&desktopaccount.Adaptor{}).DoRequest(cc, renewedInfo, strings.NewReader(`{"messages":[]}`))
			require.Error(t, err)
			assert.Equal(t, 2, relayCount)
			failSession = false
			response, err := (&desktopaccount.Adaptor{}).DoRequest(cc, renewedInfo, strings.NewReader(`{"messages":[]}`))
			require.NoError(t, err)
			require.NoError(t, response.(*http.Response).Body.Close())
			assert.Equal(t, 3, relayCount)
			latest, err := model.GetChannelById(mimoChannelID, true)
			require.NoError(t, err)
			assert.True(t, common.IsEncryptedSecret(latest.Key))
			assert.NotEqual(t, encrypted, latest.Key)
			// Egress settings cannot be changed after authorization to move account material.
			latest.SetSetting(accountLoginProxySettings("global", "http://proxy-fixture.invalid:8080", nil))
			require.NoError(t, db.Model(latest).Update("setting", latest.Setting).Error)
			_, err = (&desktopaccount.Adaptor{}).DoRequest(cc, renewedInfo, strings.NewReader(`{"messages":[]}`))
			require.Error(t, err)
			assert.Equal(t, 3, relayCount)
			// All old placeholder-token routes reject provider-account creation, even with valid input.
			for _, provider := range []string{"kimi-work", "xiaomi-mimo", "trae-cn"} {
				for _, item := range []struct {
					handler gin.HandlerFunc
					body    string
				}{
					{CreateSelfBinding, fmt.Sprintf(`{"provider":%q,"accounts":"fixture-token"}`, provider)},
					{AddSelfChannelFromTemplate, fmt.Sprintf(`{"templateId":%q,"key":"fixture-token","proxyPolicy":{"mode":"direct"}}`, provider)},
				} {
					res := request(item.handler, "POST", item.body, "https://dashboard.test", identity)
					assert.Contains(t, res.Body.String(), `"success":false`, provider)
					assert.Contains(t, res.Body.String(), "dedicated account connection flow", provider)
				}
			}
			// A lost encryption configuration must reject before any provider request.
			common.CryptoSecretConfigured = false
			beforeEncryption := meCount
			res = request(SelfMiMoOAuthStart, "POST", `{"proxyPolicy":{"mode":"direct"}}`, "https://dashboard.test", identity)
			assert.Equal(t, 503, res.Code)
			assert.Equal(t, beforeEncryption, meCount)
			common.CryptoSecretConfigured = true
			// Capability responses are explicit: Kimi local-only, MiMo true official QR.
			for _, provider := range []string{"kimi-work", "xiaomi-mimo"} {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Set("id", identity.UserID)
				c.Set("session_id", identity.SessionID)
				c.Set("auth_version", identity.UserAuthVersion)
				c.Set("session_version", identity.SessionVersion)
				c.Request = httptest.NewRequest("GET", "https://dashboard.test/account_capabilities?provider="+provider, nil)
				c.Request.Header.Set("Origin", "https://dashboard.test")
				SelfDesktopAccountCapabilities(c)
				data := decode(recorder)
				assert.Equal(t, provider == "xiaomi-mimo", data["browserLoginAvailable"])
				assert.Equal(t, provider == "kimi-work", data["importAvailable"])
			}

			// Every endpoint requires live SID and same-origin, even helper downloads.
			for _, handler := range []gin.HandlerFunc{SelfMiMoOAuthStart, SelfMiMoOAuthPoll, SelfMiMoOAuthCancel, SelfImportKimiWork, SelfKimiWorkImportHelper, SelfDesktopAccountCapabilities} {
				assert.Equal(t, 403, request(handler, "POST", `{}`, "https://attacker.invalid", identity).Code)
				assert.Equal(t, 401, request(handler, "POST", `{}`, "https://dashboard.test", model.AuthSessionIdentity{}).Code)
			}

			// Deterministic stalled-upstream regression: neither long polling nor STS
			// exchange may hold SQLite global write locks or prevent cancellation.
			another := model.User{Username: "other-" + uuid.NewString()[:8], AffCode: uuid.NewString()[:32], Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AuthVersion: 1, Group: "default"}
			require.NoError(t, db.Create(&another).Error)
			defer db.Unscoped().Delete(&another)
			for _, blockedPath := range []string{"/longPolling/listen", "/pass/serviceLogin"} {
				stalledToken := start()
				entered, release := make(chan struct{}), make(chan struct{})
				originalTransport := client.Transport
				client.Transport = workBuddyTestTransport(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == blockedPath {
						close(entered)
						<-release
					}
					return originalTransport.RoundTrip(r)
				})
				var released bool
				releaseOnce := func() {
					if !released {
						close(release)
						released = true
					}
				}
				result := make(chan *httptest.ResponseRecorder, 1)
				go func() { result <- poll(stalledToken) }()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					releaseOnce()
					t.Fatal("upstream did not reach deterministic blocking point")
				}
				writer := make(chan error, 1)
				go func() { writer <- db.Model(&another).Update("display_name", "Concurrent owner write").Error }()
				select {
				case err := <-writer:
					require.NoError(t, err)
				case <-time.After(2 * time.Second):
					releaseOnce()
					<-result
					t.Fatal("stalled provider network holds the database write transaction")
				}
				// The active nonce is exclusive even across another HTTP poll.
				assert.Equal(t, 429, poll(stalledToken).Code)
				cancelled := request(SelfMiMoOAuthCancel, "POST", fmt.Sprintf(`{"sessionId":%q}`, stalledToken), "https://dashboard.test", identity)
				require.Equal(t, 200, cancelled.Code, cancelled.Body.String())
				releaseOnce()
				select {
				case late := <-result:
					assert.Equal(t, 409, late.Code, late.Body.String())
				case <-time.After(5 * time.Second):
					t.Fatal("late worker failed to stop after cancellation")
				}
				client.Transport = originalTransport
				assert.Equal(t, 409, poll(stalledToken).Code)
				assert.Error(t, model.WithWorkBuddyAuthFlow(stalledToken, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
					t.Error("cancelled flow was reopened")
					return nil
				}))
			}
			// Completed flow state remains owner-bound after a platform session revocation.
			require.NoError(t, db.Model(&platform).Update("status", model.UserSessionStatusRevoked).Error)
			assert.Equal(t, 401, poll(token).Code)
			_, _, err = service.StartMiMoSignIn(context.Background(), "http://invalid-proxy.test:1")
			require.Error(t, err)
			assert.Greater(t, meCount, 0)
		})
	}
}

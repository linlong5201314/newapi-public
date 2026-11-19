package controller

import (
	"context"
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/trae"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkBuddyAuthorizationRequiresDashboardSession(t *testing.T) {
	for _, tc := range []struct {
		name, method, body string
		handler            gin.HandlerFunc
	}{
		{"start", http.MethodPost, `{"type":"unsupported"}`, SelfOAuthStart},
		{"poll", http.MethodGet, ``, SelfOAuthPoll},
		{"cancel", http.MethodPost, `{}`, SelfOAuthCancel},
		{"trae_import", http.MethodPost, `{}`, SelfImportTrae},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Handle(tc.method, "/auth", tc.handler)
			req := httptest.NewRequest(tc.method, "/auth", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			assert.Equal(t, http.StatusUnauthorized, res.Code)
			var envelope map[string]any
			require.NoError(t, common.Unmarshal(res.Body.Bytes(), &envelope))
			assert.NotContains(t, envelope, "data")
		})
	}
}

type workBuddyTestTransport func(*http.Request) (*http.Response, error)

func (f workBuddyTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWorkBuddyDurableAuthorization(t *testing.T) {
	oldDB, oldLog, oldType := model.DB, model.LOG_DB, common.MainDatabaseType()
	oldCrypto, oldConfigured, oldSecret := common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret
	oldRedis, oldMemory := common.RedisEnabled, common.MemoryCacheEnabled
	oldEnabled, oldLimit := operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser
	common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret = "wb-test-crypto", true, "wb-test-session"
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.SetMainDatabaseType(common.DatabaseType("sqlite"))
	operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = true, 20
	path := filepath.Join(t.TempDir(), "flows.db")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		return db
	}
	db := open()
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		sqlDB, err := model.DB.DB()
		require.NoError(t, err)
		assert.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB = oldDB, oldLog
		common.SetMainDatabaseType(oldType)
		common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret = oldCrypto, oldConfigured, oldSecret
		common.RedisEnabled, common.MemoryCacheEnabled = oldRedis, oldMemory
		operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = oldEnabled, oldLimit
	})
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.Channel{}, &model.Ability{}, &model.Option{}))
	}
	var version string
	require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	t.Logf("SQLite version %s", version)
	name := uuid.NewString()
	user := model.User{Username: name, AffCode: name, Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AuthVersion: 1, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	session := model.UserSession{SID: uuid.NewString(), UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: uuid.NewString(), LoginMethod: "password"}
	require.NoError(t, db.Create(&session).Error)
	identity := model.AuthSessionIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}
	settings := dto.ChannelSettings{ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "direct"}}
	service.InitHttpClient()
	client := service.GetHttpClient()
	oldClient := *client
	t.Cleanup(func() { *client = oldClient })
	mode, accountFail, tokenCalls, calls := "pending", false, 0, 0
	client.Transport = workBuddyTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		assert.Contains(t, []string{"copilot.tencent.com", "api.trae.cn"}, r.URL.Host)
		body, status := "", 200
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			assert.Equal(t, "CLI", r.URL.Query().Get("platform"))
			body = `{"code":0,"data":{"state":"private-upstream-state","authUrl":"https://www.codebuddy.cn/login?state=private-upstream-state"}}`
		case "/v2/plugin/auth/token":
			tokenCalls++
			assert.Equal(t, "private-upstream-state", r.URL.Query().Get("state"))
			switch mode {
			case "pending":
				body = `{"code":1,"msg":"login ing"}`
			case "error":
				status = 503
				body = `{"secret":"upstream-secret"}`
			case "malformed":
				body = `{"code":0,"data":{}}`
			default:
				body = `{"code":0,"data":{"accessToken":"private-access-token","refreshToken":"private-refresh-token","expiresIn":3600,"domain":"codebuddy.cn"}}`
			}
		case "/v2/plugin/login/account":
			if accountFail {
				status = 503
				body = `{}`
			} else {
				body = `{"code":0,"data":{"uid":"owner","nickname":"Account"}}`
			}
		case "/cloudide/api/v3/trae/oauth/ExchangeToken":
			body = `{"Result":{"Token":"private-trae-access","RefreshToken":"private-trae-refresh","TokenExpireDuration":3600}}`
		case "/cloudide/api/v3/trae/GetUserInfo":
			body = `{"Result":{"UserID":"trae-owner","ScreenName":"Trae"}}`
		default:
			return nil, errors.New("unexpected path")
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	start := func() string {
		token, _, err := StartWorkBuddyAuthorization(context.Background(), identity, "cn", "Account", false, settings)
		require.NoError(t, err)
		require.Len(t, token, 43)
		assert.NotEqual(t, "private-upstream-state", token)
		return token
	}
	reset := func(token string) {
		require.NoError(t, model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error { state.NextPollAt = 0; return nil }))
	}
	token := start()
	var flow model.AuthFlow
	require.NoError(t, db.Where("purpose = ?", model.WorkBuddyAuthPurpose).First(&flow).Error)
	assert.True(t, common.IsEncryptedSecret(flow.Payload))
	assert.NotContains(t, flow.Payload, "private-upstream-state")
	assert.NotEqual(t, token, flow.TokenHash)
	for range 7 {
		sqlDB, err := model.DB.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
		db = open()
		model.DB, model.LOG_DB = db, db
		channel, err := PollAccountAuthorization(context.Background(), identity, token)
		require.NoError(t, err)
		assert.Nil(t, channel)
		reset(token)
	}
	second := session
	second.SID = uuid.NewString()
	second.RefreshHash = uuid.NewString()
	require.NoError(t, db.Create(&second).Error)
	wrong := identity
	wrong.SessionID = second.SID
	_, err := PollAccountAuthorization(context.Background(), wrong, token)
	assert.ErrorIs(t, err, model.ErrAuthFlowInvalid)
	wrong = identity
	wrong.UserID++
	_, err = PollAccountAuthorization(context.Background(), wrong, token)
	require.Error(t, err)
	for _, failure := range []string{"error", "malformed"} {
		mode = failure
		_, err = PollAccountAuthorization(context.Background(), identity, token)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "upstream-secret")
		reset(token)
	}
	mode, accountFail = "done", true
	before := tokenCalls
	_, err = PollAccountAuthorization(context.Background(), identity, token)
	require.Error(t, err)
	assert.Equal(t, before+1, tokenCalls)
	reset(token)
	accountFail = false
	channel, err := PollAccountAuthorization(context.Background(), identity, token)
	require.NoError(t, err)
	require.NotNil(t, channel)
	id := channel.Id
	assert.Zero(t, channel.Visibility)
	assert.Equal(t, user.Id, channel.OwnerId)
	assert.True(t, common.IsEncryptedSecret(channel.Key))
	channel, err = PollAccountAuthorization(context.Background(), identity, token)
	require.NoError(t, err)
	assert.Equal(t, id, channel.Id)
	assert.Equal(t, before+1, tokenCalls)
	sanitizeSelfChannel(channel)
	raw, err := common.Marshal(channel)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "private-access-token")
	require.NoError(t, db.First(&flow, flow.Id).Error)
	plain, err := common.DecryptSecret(flow.Payload)
	require.NoError(t, err)
	assert.NotContains(t, plain, "private-access-token")
	assert.NotContains(t, plain, "private-upstream-state")
	cancel := start()
	require.NoError(t, CancelAccountAuthorization(identity, cancel))
	_, err = PollAccountAuthorization(context.Background(), identity, cancel)
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	expired := start()
	require.NoError(t, db.Model(&model.AuthFlow{}).Where("purpose = ? AND consumed_at IS NULL", model.WorkBuddyAuthPurpose).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	_, err = PollAccountAuthorization(context.Background(), identity, expired)
	assert.ErrorIs(t, err, model.ErrAuthFlowExpired)
	revoked := start()
	require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", session.SID).Update("version", 2).Error)
	before = calls
	_, err = PollAccountAuthorization(context.Background(), identity, revoked)
	require.Error(t, err)
	assert.Equal(t, before, calls)
	require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", session.SID).Update("version", 1).Error)
	require.NoError(t, CancelAccountAuthorization(identity, revoked))
	quota := start()
	operation_setting.SelfChannelMaxPerUser = 1
	_, err = PollAccountAuthorization(context.Background(), identity, quota)
	assert.ErrorIs(t, err, model.ErrWorkBuddyAuthLimit)
	operation_setting.SelfChannelMaxPerUser = 20
	require.NoError(t, CancelAccountAuthorization(identity, quota))
	rollback := start()
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("workbuddy_ability_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "abilities" {
			tx.AddError(errors.New("ability write failed"))
		}
	}))
	_, err = PollAccountAuthorization(context.Background(), identity, rollback)
	require.Error(t, err)
	require.NoError(t, db.Callback().Create().Remove("workbuddy_ability_failure"))
	var staged model.Channel
	require.NoError(t, db.Where("owner_id = ? AND status = ?", user.Id, common.ChannelStatusManuallyDisabled).First(&staged).Error)
	var count int64
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", staged.Id).Count(&count).Error)
	assert.Zero(t, count)
	channel, err = PollAccountAuthorization(context.Background(), identity, rollback)
	require.NoError(t, err)
	assert.Equal(t, staged.Id, channel.Id)
	for _, callback := range []string{"https://attacker.invalid/authorize?refreshToken=secret", "file:///authorize?refreshToken=secret", "http://127.0.0.1:1234/not-authorize?refreshToken=secret"} {
		before = calls
		_, err = ImportTraeAuthorization(context.Background(), identity, callback, "", "", "", "", "", false, true, settings)
		require.Error(t, err)
		assert.Equal(t, before, calls)
	}
	_, err = ImportTraeAuthorization(context.Background(), identity, "", "source-token", "trae-owner", "", "", "Trae", false, false, settings)
	require.Error(t, err)
	traeToken, err := ImportTraeAuthorization(context.Background(), identity, "", "source-token", "trae-owner", "", "", "Trae", false, true, settings)
	require.NoError(t, err)
	channel, err = PollAccountAuthorization(context.Background(), identity, traeToken)
	require.NoError(t, err)
	assert.True(t, common.IsEncryptedSecret(channel.Key))
	_, err = ImportTraeAuthorization(context.Background(), identity, "", "source-token", "trae-owner", "", "", "Trae", false, true, settings)
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)

	t.Run("Trae current official callback and dashboard boundary", func(t *testing.T) {
		callback := "http://127.0.0.1:18080/authorize?" + url.Values{
			"userJwt":  {`{"Token":"official-private-access","RefreshToken":"official-private-refresh","TokenExpireAt":1900000000000,"ClientID":"ono9krqynydwx5"}`},
			"userInfo": {`{"UserID":"trae-owner","ScreenName":"Trae","Region":"CN","Host":"https://api.trae.cn"}`},
		}.Encode()
		raw, err := common.Marshal(map[string]any{"realm": "cn", "callbackUrl": callback, "confirmRotation": true, "mode": "preview"})
		require.NoError(t, err)
		request := func(body, origin, fetchSite string, pat bool, version int64) *httptest.ResponseRecorder {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("id", identity.UserID)
				c.Set("session_id", identity.SessionID)
				c.Set("auth_version", identity.UserAuthVersion)
				c.Set("session_version", version)
				c.Set("use_access_token", pat)
			})
			router.POST("/auth", SelfImportTrae)
			req := httptest.NewRequest(http.MethodPost, "https://dashboard.test/auth", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			if fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", fetchSite)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			return response
		}
		for _, failure := range []struct {
			name, origin, site string
			pat                bool
			version            int64
			status             int
		}{
			{"cross-origin", "https://attacker.invalid", "", false, 1, http.StatusForbidden},
			{"cross-site metadata", "https://dashboard.test", "cross-site", false, 1, http.StatusForbidden},
			{"missing origin and opt-in", "", "", false, 1, http.StatusForbidden},
			{"access token cannot authorize", "https://dashboard.test", "same-origin", true, 1, http.StatusUnauthorized},
			{"stale session", "https://dashboard.test", "same-origin", false, 2, http.StatusUnauthorized},
		} {
			t.Run(failure.name, func(t *testing.T) {
				before := calls
				response := request(string(raw), failure.origin, failure.site, failure.pat, failure.version)
				assert.Equal(t, failure.status, response.Code)
				assert.Equal(t, before, calls)
				assert.NotContains(t, response.Body.String(), "official-private")
			})
		}
		for _, realm := range []string{"global", "international", "us"} {
			before := calls
			body := strings.Replace(string(raw), `"realm":"cn"`, `"realm":"`+realm+`"`, 1)
			response := request(body, "https://dashboard.test", "same-origin", false, 1)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Equal(t, before, calls)
		}
		before := calls
		response := request(string(raw), "https://dashboard.test", "same-origin", false, 1)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, before+1, calls, "fresh pair must be verified, not gratuitously exchanged")
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		assert.NotContains(t, response.Body.String(), "official-private")
		var envelope struct {
			Data struct {
				SessionID string `json:"sessionId"`
				Ready     bool   `json:"ready"`
				Browser   bool   `json:"browserLoginAvailable"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
		require.NotEmpty(t, envelope.Data.SessionID)
		assert.True(t, envelope.Data.Ready)
		assert.False(t, envelope.Data.Browser)
		require.NoError(t, model.WithWorkBuddyAuthFlow(envelope.Data.SessionID, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
			credential, err := trae.ParseCred(state.Credential)
			require.NoError(t, err)
			assert.Equal(t, "official-private-access", credential.AccessToken)
			assert.Equal(t, "official-private-refresh", credential.RefreshToken)
			assert.Equal(t, trae.IDEClientID, credential.ClientID)
			assert.Equal(t, trae.DomesticAuthHost, credential.ApiHost)
			assert.Equal(t, int64(1900000000), credential.ExpiresAt)
			return nil
		}))
		wrong := identity
		wrong.SessionID = second.SID
		_, err = PollAccountAuthorization(context.Background(), wrong, envelope.Data.SessionID)
		assert.ErrorIs(t, err, model.ErrAuthFlowInvalid)
		_, err = PollAccountAuthorization(context.Background(), identity, "forged-flow-state")
		assert.ErrorIs(t, err, model.ErrAuthFlowInvalid)
		bound, err := PollAccountAuthorization(context.Background(), identity, envelope.Data.SessionID)
		require.NoError(t, err)
		require.NotNil(t, bound)
		assert.Equal(t, constant.ChannelTypeTrae, bound.Type)
		assert.True(t, common.IsEncryptedSecret(bound.Key))
		for _, replay := range []string{callback, "http://127.0.0.1:18080/authorize?userJwt=" + url.QueryEscape(`{"Token":"official-private-access","TokenExpireAt":1900000000}`), "http://127.0.0.1:18080/authorize?refreshToken=official-private-refresh"} {
			before = calls
			_, err = ImportTraeAuthorization(context.Background(), identity, replay, "", "", "", "", "", false, true, settings)
			assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
			assert.Equal(t, before, calls)
		}
		for _, bridgeStatus := range []string{"bridge_pending", "bridge_uploaded", "bridge_consent"} {
			var channelsBefore, channelsAfter int64
			require.NoError(t, db.Model(&model.Channel{}).Count(&channelsBefore).Error)
			flow, err := newAccountFlow(identity, "trae", "cn", "Needs explicit consent", false, settings, (&trae.Adaptor{}).GetModelList())
			require.NoError(t, err)
			require.NoError(t, model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
				state.Status = bridgeStatus
				// A verified credential cannot substitute for dashboard consent.
				state.Credential = `{"accessToken":"unconsented-private-access","refreshToken":"unconsented-private-refresh","uid":"trae-owner","expiresAt":1900000000}`
				return nil
			}))
			before = calls
			unconsented, err := PollAccountAuthorization(context.Background(), identity, flow)
			assert.ErrorIs(t, err, model.ErrAuthFlowInvalid, bridgeStatus)
			assert.Nil(t, unconsented)
			assert.Equal(t, before, calls)
			require.NoError(t, db.Model(&model.Channel{}).Count(&channelsAfter).Error)
			assert.Equal(t, channelsBefore, channelsAfter, bridgeStatus)
			require.NoError(t, model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
				assert.Equal(t, bridgeStatus, state.Status)
				assert.Zero(t, state.ChannelID)
				return nil
			}))
			require.NoError(t, CancelAccountAuthorization(identity, flow))
		}
		before = calls
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		client.Transport = workBuddyTestTransport(func(r *http.Request) (*http.Response, error) {
			assert.ErrorIs(t, r.Context().Err(), context.Canceled)
			return nil, r.Context().Err()
		})
		_, err = RedeemTraeAuthorization(ctx, "", &trae.CallbackInfo{AccessToken: "cancelled-access", ExpiresAt: 1900000000}, "", "")
		assert.ErrorIs(t, err, service.ErrAccountUpstream)
		assert.Equal(t, before, calls)
	})
}

func TestAccountLoginProxyDefaultsPreserveExplicitChoice(t *testing.T) {
	for _, tc := range []struct {
		name, realm, proxy string
		policy             *dto.ChannelProxyPolicy
		mode               string
	}{
		{name: "domestic untouched permits direct", realm: "cn", mode: "direct"},
		{name: "global untouched remains personal pool", realm: "global", mode: "user_pool"},
		{name: "fixed URL remains fixed", realm: "cn", proxy: "http://127.0.0.1:7890", mode: "fixed"},
		{name: "explicit personal pool never falls back", realm: "cn", policy: &dto.ChannelProxyPolicy{Mode: "user_pool"}, mode: "user_pool"},
		{name: "explicit subscription stays selected", realm: "cn", policy: &dto.ChannelProxyPolicy{Mode: "subscription", SubscriptionID: 7}, mode: "subscription"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := accountLoginProxySettings(tc.realm, tc.proxy, tc.policy)
			require.NotNil(t, got.ProxyPolicy)
			assert.Equal(t, tc.mode, got.ProxyPolicy.Mode)
			assert.Equal(t, tc.proxy, got.Proxy)
			if tc.policy != nil {
				assert.Equal(t, tc.policy, got.ProxyPolicy)
			}
		})
	}
}

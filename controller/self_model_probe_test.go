package controller

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type modelProbeFixture struct {
	userID         int
	channel        *model.Channel
	session        *model.UserSession
	calls          atomic.Int32
	responseStatus atomic.Int32
	empty          atomic.Bool
	overBudget     atomic.Bool
}

func newModelProbeFixture(t *testing.T) *modelProbeFixture {
	t.Helper()
	model.ResetChannelHealthStoreForTest()
	require.NoError(t, i18n.Init())
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-3.5-turbo":1,"gpt-4":1}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })
	uid := setupChannelCreateTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.ModelEndpointHealth{}, &model.ProxyEndpoint{}, &model.ProxySubscription{}))
	require.NoError(t, model.DB.AutoMigrate(&model.ModelEndpointHealth{}))
	session := selfContractDS2Session(t, uid)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", uid).Updates(map[string]any{"role": common.RoleCommonUser, "group": "default", "quota": 1000000, "setting": `{"billing_preference":"wallet_only"}`}).Error)
	f := &modelProbeFixture{userID: uid, session: session}
	f.responseStatus.Store(http.StatusOK)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		var body map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &body))
		assert.Equal(t, float64(8), body["max_tokens"])
		assert.Equal(t, false, body["stream"])
		assert.Equal(t, float64(1), body["n"])
		assert.Equal(t, "Bearer fixture-probe-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		status := int(f.responseStatus.Load())
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = w.Write([]byte(`{"error":{"message":"secret-upstream-body fixture-probe-key http://private","type":"invalid_request_error","code":"provider_code"}}`))
			return
		}
		if f.overBudget.Load() {
			_, _ = w.Write([]byte(`{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":22,"total_tokens":24}}`))
			return
		}
		if f.empty.Load() {
			_, _ = w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":0,"total_tokens":2}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"fixture","model":"gpt-3.5-turbo","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	t.Cleanup(upstream.Close)
	// These fixture servers intentionally listen on loopback. Production keeps
	// the tenant private-address guard enabled; only this test opts into it.
	fetch := system_setting.GetFetchSetting()
	previousAllowPrivate, previousAllowedPorts := fetch.AllowPrivateIp, fetch.AllowedPorts
	fetch.AllowPrivateIp = true
	_, upstreamPort, err := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	require.NoError(t, err)
	fetch.AllowedPorts = []string{upstreamPort}
	t.Cleanup(func() {
		fetch.AllowPrivateIp, fetch.AllowedPorts = previousAllowPrivate, previousAllowedPorts
	})
	settings := `{"proxy":"direct","proxy_policy":{"mode":"direct"}}`
	f.channel = &model.Channel{OwnerId: uid, Type: constant.ChannelTypeOpenAI, Name: "model probe fixture", Key: "fixture-probe-key", Models: "gpt-3.5-turbo,gpt-4", OtherInfo: `{"allModels":["gpt-3.5-turbo","gpt-4","discovered-only"]}`, Group: "default", BaseURL: &upstream.URL, Setting: &settings, Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(f.channel).Error)
	previousAll, previousSelf, previousPublic := operation_setting.AllModelsFreeBilling, operation_setting.SelfKeyFreeBilling, operation_setting.PlatformPublicFreeBilling
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	previousCount := constant.CountToken
	operation_setting.AllModelsFreeBilling, operation_setting.SelfKeyFreeBilling, operation_setting.PlatformPublicFreeBilling, constant.CountToken = false, false, false, false
	t.Cleanup(func() {
		operation_setting.AllModelsFreeBilling, operation_setting.SelfKeyFreeBilling, operation_setting.PlatformPublicFreeBilling, constant.CountToken = previousAll, previousSelf, previousPublic, previousCount
	})
	service.InitHttpClient()
	return f
}

func (f *modelProbeFixture) request(method, query, body string, mutate func(*gin.Context)) *httptest.ResponseRecorder {
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("id", f.userID)
		c.Set("session_id", f.session.SID)
		c.Set("auth_version", int64(1))
		c.Set("session_version", int64(1))
		if mutate != nil {
			mutate(c)
		}
		c.Next()
	})
	engine.GET("/:id", GetSelfModelProbe)
	engine.POST("/:id", PostSelfModelProbe)
	r := httptest.NewRequest(method, fmt.Sprintf("/%d%s", f.channel.Id, query), strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)
	return w
}

func TestSelfModelProbeAuthenticatedRelayAndRecentHistory(t *testing.T) {
	f := newModelProbeFixture(t)
	w := f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo"]}`, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Success bool                    `json:"success"`
		Data    service.ModelProbeBatch `json:"data"`
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Results, 1)
	result := response.Data.Results[0]
	assert.Equal(t, model.ModelEndpointAvailable, result.Status, w.Body.String())
	assert.Equal(t, []string{"gpt-3.5-turbo"}, response.Data.PassedModels)
	assert.EqualValues(t, 1, result.LifetimeSamples)
	assert.EqualValues(t, 1, result.LifetimePasses)
	assert.Greater(t, result.ExpiresAt, result.CheckedAt)
	assert.Equal(t, int32(1), f.calls.Load())
	assert.NotContains(t, w.Body.String(), "fixture-probe-key")
	assert.NotContains(t, w.Body.String(), "scope_key")
	health := model.ChannelHealthOf(f.channel.Id)
	assert.Zero(t, health.Samples)
	var user model.User
	require.NoError(t, model.DB.First(&user, f.userID).Error)
	assert.Less(t, user.Quota, 1000000, "normal wallet billing applies")
	w = f.request(http.MethodGet, "?models=gpt-3.5-turbo", "", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"status":"available"`)
	assert.Equal(t, int32(1), f.calls.Load(), "opening history never infers")
	require.NoError(t, model.DB.Model(&model.ModelEndpointHealth{}).Where("channel_id = ?", f.channel.Id).Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error)
	w = f.request(http.MethodGet, "?models=gpt-3.5-turbo", "", nil)
	assert.Contains(t, w.Body.String(), `"reason":"stale"`)
	assert.Contains(t, w.Body.String(), `"status":"unknown"`)
}

func TestSelfModelProbeRejectsIdentityScopeOriginAndOversizedSamples(t *testing.T) {
	f := newModelProbeFixture(t)
	endpoint := &model.ProxyEndpoint{UserId: f.userID + 100, Enabled: true, Url: "http://fixture-secret:password@proxy.invalid:8080"}
	require.NoError(t, model.DB.Create(endpoint).Error)
	for _, tc := range []struct {
		name, body string
		mutate     func(*gin.Context)
		status     int
	}{
		{name: "relay token is not session", body: `{"models":["gpt-3.5-turbo"]}`, mutate: func(c *gin.Context) { c.Set("session_id", "") }, status: 401},
		{name: "PAT is not session", body: `{"models":["gpt-3.5-turbo"]}`, mutate: func(c *gin.Context) { c.Set("use_access_token", true) }, status: 401},
		{name: "session ownership", body: `{"models":["gpt-3.5-turbo"]}`, mutate: func(c *gin.Context) { c.Set("id", f.userID+100) }, status: 401},
		{name: "foreign origin", body: `{"models":["gpt-3.5-turbo"]}`, mutate: func(c *gin.Context) {
			c.Request.Header.Set("Sec-Fetch-Site", "cross-site")
			c.Request.Header.Set("Origin", "https://evil.invalid")
		}, status: 403},
		{name: "non json", body: `{"models":["gpt-3.5-turbo"]}`, mutate: func(c *gin.Context) { c.Request.Header.Set("Content-Type", "text/plain") }, status: 403},
		{name: "four models", body: `{"models":["a","b","c","d"]}`, status: 400},
		{name: "four endpoints", body: `{"models":["gpt-3.5-turbo"],"endpoint_ids":[1,2,3,4]}`, status: 400},
		{name: "unknown model", body: `{"models":["not-in-catalog"]}`, status: 403},
		{name: "foreign private endpoint", body: fmt.Sprintf(`{"models":["gpt-3.5-turbo"],"endpoint_ids":[%d]}`, endpoint.Id), status: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.request(http.MethodPost, "", tc.body, tc.mutate)
			assert.Equal(t, tc.status, w.Code, w.Body.String())
			assert.NotContains(t, w.Body.String(), "fixture-secret")
		})
	}
	assert.Zero(t, f.calls.Load())
	require.NoError(t, model.DB.Model(f.channel).Update("owner_id", f.userID+100).Error)
	w := f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo"]}`, nil)
	assert.Equal(t, 403, w.Code)
	require.NoError(t, model.DB.Model(f.channel).Updates(map[string]any{"owner_id": 0, "tier": "free"}).Error)
	w = f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo"]}`, nil)
	assert.Equal(t, 403, w.Code, "ordinary user cannot test platform free channel")
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.userID).Update("role", common.RoleRootUser).Error)
	require.NoError(t, model.DB.Model(f.channel).Update("tier", "api").Error)
	w = f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo"]}`, nil)
	assert.Equal(t, 403, w.Code, "root cannot use public tests for paid platform channel")
	assert.Zero(t, f.calls.Load())
}

func TestSelfModelProbeQuotaDenialAndProviderAdmissionStopBatch(t *testing.T) {
	t.Run("Qoder queue stops admission without marking models unavailable", func(t *testing.T) {
		f := newModelProbeFixture(t)
		calls := 0
		batch, err := service.RunModelEndpointProbes(context.Background(), f.userID, service.ModelProbeRequest{Models: []string{"gpt-3.5-turbo", "gpt-4"}}, func(context.Context) (*model.Channel, error) { return f.channel, nil }, func(ctx context.Context, _ *model.Channel, _ string, _ model.ModelProbeEndpoint) service.ModelProbeOutcome {
			calls++
			return service.ClassifyModelProbeError(ctx, types.NewErrorWithStatusCode(service.ErrQoderQueued, types.ErrorCode("qoder_queued"), http.StatusTooManyRequests, types.ErrOptionWithSkipRetry()))
		})
		require.NoError(t, err)
		assert.Equal(t, 1, calls)
		require.Len(t, batch.Results, 2)
		assert.Equal(t, model.ModelEndpointUnknown, batch.Results[0].Status)
		assert.Equal(t, "rate_limited", batch.Results[0].Reason)
		assert.Equal(t, "not_run", batch.Results[1].Reason)
		assert.Empty(t, batch.PassedModels)
	})
	for _, status := range []int{403, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newModelProbeFixture(t)
			f.responseStatus.Store(int32(status))
			w := f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo","gpt-4"]}`, nil)
			require.Equal(t, 200, w.Code, w.Body.String())
			assert.Equal(t, int32(1), f.calls.Load())
			assert.Contains(t, w.Body.String(), `"reason":"not_run"`)
			assert.NotContains(t, w.Body.String(), "secret-upstream-body")
			assert.NotContains(t, w.Body.String(), "fixture-probe-key")
			assert.Zero(t, model.ChannelHealthOf(f.channel.Id).Samples)
			var stored []model.ModelEndpointHealth
			require.NoError(t, model.DB.Where("channel_id = ?", f.channel.Id).Find(&stored).Error)
			require.Len(t, stored, 1)
			if status == 403 {
				assert.Equal(t, "access_denied", stored[0].Reason)
			} else {
				assert.Equal(t, "rate_limited", stored[0].Reason)
				assert.Equal(t, model.ModelEndpointUnknown, stored[0].Status)
			}
		})
	}
	t.Run("zero wallet prevents upstream even for root public free test", func(t *testing.T) {
		f := newModelProbeFixture(t)
		require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.userID).Updates(map[string]any{"quota": 0, "role": common.RoleRootUser}).Error)
		require.NoError(t, model.DB.Model(f.channel).Updates(map[string]any{"owner_id": 0, "tier": "free"}).Error)
		w := f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo"]}`, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), `"reason":"quota_exhausted"`)
		assert.Zero(t, f.calls.Load())
	})
}

func TestSelfModelProbePerNodeHistoryAndCancellation(t *testing.T) {
	f := newModelProbeFixture(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer proxy.Close()
	_, proxyPort, err := net.SplitHostPort(strings.TrimPrefix(proxy.URL, "http://"))
	require.NoError(t, err)
	fetch := system_setting.GetFetchSetting()
	previousPorts := fetch.AllowedPorts
	fetch.AllowedPorts = []string{strings.TrimPrefix(f.channel.GetBaseURL(), "http://127.0.0.1:"), proxyPort}
	t.Cleanup(func() { fetch.AllowedPorts = previousPorts })
	sub := &model.ProxySubscription{UserId: f.userID, Enabled: true}
	require.NoError(t, model.DB.Create(sub).Error)
	ep := &model.ProxyEndpoint{UserId: f.userID, Enabled: true, SubscriptionId: sub.Id, Url: proxy.URL}
	require.NoError(t, model.DB.Create(ep).Error)
	t.Cleanup(model.SetProxyEndpointURLProvidersForTest(func(endpoint model.ProxyEndpoint) (string, error) { return endpoint.Url, nil }))
	w := f.request(http.MethodPost, "", fmt.Sprintf(`{"models":["gpt-3.5-turbo"],"endpoint_ids":[%d]}`, ep.Id), nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"status":"available"`)
	var fresh model.ProxyEndpoint
	require.NoError(t, model.DB.First(&fresh, ep.Id).Error)
	assert.Zero(t, fresh.FailCount)
	assert.Nil(t, fresh.LastOkAt, "model probe does not mutate global node health")
	w = f.request(http.MethodGet, fmt.Sprintf("?models=gpt-3.5-turbo&endpoint_ids=%d", ep.Id), "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"status":"available"`)
	require.NoError(t, model.DB.Model(sub).Update("enabled", false).Error)
	w = f.request(http.MethodPost, "", fmt.Sprintf(`{"models":["gpt-3.5-turbo"],"endpoint_ids":[%d]}`, ep.Id), nil)
	assert.Equal(t, 403, w.Code, "subscription ACL revoked after listing")
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	batch, err := service.RunModelEndpointProbes(ctx, f.userID, service.ModelProbeRequest{Models: []string{"gpt-3.5-turbo", "gpt-4"}}, func(context.Context) (*model.Channel, error) { return f.channel, nil }, func(ctx context.Context, _ *model.Channel, _ string, _ model.ModelProbeEndpoint) service.ModelProbeOutcome {
		calls++
		cancel()
		return service.ClassifyModelProbeError(ctx, nil)
	})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Empty(t, batch.PassedModels)
	require.Len(t, batch.Results, 2)
	assert.Equal(t, "cancelled", batch.Results[1].Reason)
	for _, tc := range []struct {
		status int
		code   types.ErrorCode
		reason string
	}{{403, types.ErrorCodeBadResponse, "access_denied"}, {429, types.ErrorCodeBadResponse, "rate_limited"}, {500, types.ErrorCodeDoRequestFailed, "transport_error"}, {404, types.ErrorCodeBadResponse, "model_not_found"}} {
		outcome := service.ClassifyModelProbeError(context.Background(), types.NewErrorWithStatusCode(errors.New("raw secret"), tc.code, tc.status))
		assert.Equal(t, tc.reason, outcome.Reason)
	}
}

func TestSelfModelProbeScopedCountersPaginationAndPreflight(t *testing.T) {
	f := newModelProbeFixture(t)
	require.NoError(t, model.DB.Model(f.channel).Update("status", common.ChannelStatusManuallyDisabled).Error)
	w := f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo"]}`, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"status":"available"`)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, f.channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status, "test never enables a disabled channel")
	var first model.ModelEndpointHealth
	require.NoError(t, model.DB.Where("channel_id = ?", f.channel.Id).First(&first).Error)
	before := first.CheckedAt
	for _, state := range []string{model.ModelEndpointUnavailable, model.ModelEndpointUnknown, model.ModelEndpointAvailable, model.ModelEndpointUnavailable, model.ModelEndpointAvailable} {
		sample := first
		sample.CheckedAt = before + 1
		sample.Status = state
		sample.Reason = "upstream_error"
		if state == model.ModelEndpointAvailable {
			sample.Reason = "passed"
		}
		if state == model.ModelEndpointUnknown {
			sample.Reason = "timeout"
		}
		firstErr := error(nil)
		first, firstErr = model.RecordModelEndpointHealth(context.Background(), sample)
		require.NoError(t, firstErr)
		before++
	}
	assert.EqualValues(t, 6, first.LifetimeSamples)
	assert.EqualValues(t, 3, first.LifetimePasses)
	assert.EqualValues(t, 2, first.LifetimeFailures)
	assert.Equal(t, 5, first.RecentSampleCount)
	assert.Equal(t, 2, first.RecentPasses)
	assert.Equal(t, 2, first.RecentFailures)
	stale := first
	stale.CheckedAt = first.CheckedAt - 1
	stale.Status = model.ModelEndpointUnavailable
	kept, err := model.RecordModelEndpointHealth(context.Background(), stale)
	require.NoError(t, err)
	assert.Equal(t, first.LifetimeSamples, kept.LifetimeSamples)
	assert.Equal(t, model.ModelEndpointAvailable, kept.Status)
	scope := model.ModelEndpointHealthScope(&channel, first.Model, nil, "")
	assert.Equal(t, first.ScopeKey, scope)
	channel.Key = "rotated-fixture-key"
	unknown, err := model.GetModelEndpointHealth(context.Background(), model.ModelEndpointHealthScope(&channel, first.Model, nil, ""), channel.Id, first.Model, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, model.ModelEndpointUnknown, unknown.Status)
	endpoints := make([]model.ProxyEndpoint, 0, 55)
	for i := range 55 {
		endpoints = append(endpoints, model.ProxyEndpoint{UserId: f.userID, Enabled: true, Url: fmt.Sprintf("http://secret:private@node-%d.invalid:8888", i)})
	}
	require.NoError(t, model.DB.Create(&endpoints).Error)
	page, more, err := model.ListModelProbeEndpoints(context.Background(), f.userID, 50, 50, false)
	require.NoError(t, err)
	assert.False(t, more)
	require.Len(t, page, 5)
	w = f.request(http.MethodGet, fmt.Sprintf("?models=gpt-3.5-turbo&endpoint_ids=%d&endpoint_offset=0&endpoint_limit=50", endpoints[54].Id), "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), fmt.Sprintf(`"id":%d`, endpoints[54].Id))
	assert.Contains(t, w.Body.String(), `"endpoint_has_more":true`)
	assert.NotContains(t, w.Body.String(), "secret:private")
	require.NoError(t, model.DB.Model(f.channel).Updates(map[string]any{"tier": "free", "allow_ips": "203.0.113.10"}).Error)
	w = f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo"]}`, nil)
	assert.Equal(t, 403, w.Code, "free channel source allowlist still enforced")
}

func TestSelfModelProbeProviderExceedsRequestedLimitStopsBatch(t *testing.T) {
	f := newModelProbeFixture(t)
	f.overBudget.Store(true)
	w := f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo","gpt-4"]}`, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, int32(1), f.calls.Load(), "stop sampling an upstream that ignored the requested limit")
	assert.Contains(t, w.Body.String(), `"reason":"output_limit_exceeded"`)
	assert.Contains(t, w.Body.String(), `"reason":"not_run"`)
	assert.Contains(t, w.Body.String(), `"passed_models":[]`)
	var samples []model.ModelEndpointHealth
	require.NoError(t, model.DB.Where("channel_id = ?", f.channel.Id).Find(&samples).Error)
	require.Len(t, samples, 1)
	assert.Equal(t, model.ModelEndpointUnknown, samples[0].Status)
}

func TestSelfModelProbeEmptyRepliesAndAccountEgressRestriction(t *testing.T) {
	f := newModelProbeFixture(t)
	f.empty.Store(true)
	w := f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo"]}`, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"reason":"invalid_response"`)
	assert.Contains(t, w.Body.String(), `"passed_models":[]`)
	for _, kind := range []int{constant.ChannelTypeCline, constant.ChannelTypeRaccoon, constant.ChannelTypeZCode, constant.ChannelTypeQoder} {
		require.NoError(t, model.DB.Model(f.channel).Update("type", kind).Error)
		w = f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo"],"endpoint_ids":[42]}`, nil)
		assert.Equal(t, 403, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "Account model tests must use configured egress")
	}
	assert.Equal(t, int32(1), f.calls.Load())
	release, err := service.AcquireModelProbe(context.Background(), f.userID)
	require.NoError(t, err)
	_, err = service.AcquireModelProbe(context.Background(), f.userID)
	require.ErrorIs(t, err, service.ErrModelProbeBusy)
	release()
	release, err = service.AcquireModelProbe(context.Background(), f.userID)
	require.NoError(t, err)
	release()
}

func TestSelfModelProbeNativeAccountsKeepBoundedConfiguredEgress(t *testing.T) {
	for _, kind := range []int{constant.ChannelTypeCline, constant.ChannelTypeRaccoon, constant.ChannelTypeZCode, constant.ChannelTypeQoder} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			f := newModelProbeFixture(t)
			previousCrypto, previousConfigured := common.CryptoSecret, common.CryptoSecretConfigured
			common.CryptoSecret, common.CryptoSecretConfigured = "model-probe-fixture-crypto-key", true
			t.Cleanup(func() { common.CryptoSecret, common.CryptoSecretConfigured = previousCrypto, previousConfigured })
			egress := service.AccountEgress{Key: "model-probe-fixture-owner", Proxy: ""}
			var credential any
			switch kind {
			case constant.ChannelTypeCline:
				credential = service.ClineCredential{UserID: "fixture-owner", Token: "fixture-native-token", ExpiresAt: time.Now().Add(time.Hour).Unix(), Egress: egress}
			case constant.ChannelTypeRaccoon:
				credential = service.RaccoonCredential{UserID: "fixture-owner", Token: "fixture-native-token", ExpiresAt: time.Now().Add(time.Hour).Unix(), Egress: egress}
			case constant.ChannelTypeZCode:
				credential = service.ZCodeCredential{Provider: "zai", UserID: "fixture-owner", Token: "fixture-native-token", ExpiresAt: time.Now().Add(time.Hour).Unix(), Egress: egress}
			case constant.ChannelTypeQoder:
				credential = service.QoderCredential{Realm: "global", UserID: "fixture-owner", MachineID: "fixture-machine", Token: "fixture-native-token", ExpiresAt: time.Now().Add(time.Hour).Unix(), Egress: egress}
			}
			raw, err := common.Marshal(credential)
			require.NoError(t, err)
			encrypted, err := common.EncryptSecret(string(raw))
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(f.channel).Updates(map[string]any{"type": kind, "key": encrypted, "base_url": ""}).Error)
			calls := 0
			transport := selfContractTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if kind == constant.ChannelTypeQoder && r.Method == http.MethodGet {
					return selfContractResponse(200, `{"chat":[{"key":"gpt-3.5-turbo","display_name":"fixture-model","enable":true}]}`)
				}
				assert.Equal(t, http.MethodPost, r.Method)
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				if kind == constant.ChannelTypeQoder {
					const coded = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
					const standard = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
					encoded := []byte(body)
					for i, b := range encoded {
						if b == '$' {
							encoded[i] = '='
						} else {
							at := strings.IndexByte(coded, b)
							require.GreaterOrEqual(t, at, 0)
							encoded[i] = standard[at]
						}
					}
					segment := len(encoded) / 3
					plain, err := base64.StdEncoding.DecodeString(string(encoded[len(encoded)-segment:]) + string(encoded[segment:len(encoded)-segment]) + string(encoded[:segment]))
					require.NoError(t, err)
					var payload struct {
						Parameters map[string]any `json:"parameters"`
					}
					require.NoError(t, common.Unmarshal(plain, &payload))
					assert.Equal(t, float64(8), payload.Parameters["max_tokens"])
				}
				if kind != constant.ChannelTypeQoder {
					var payload map[string]any
					require.NoError(t, common.Unmarshal(body, &payload))
					assert.Equal(t, float64(8), payload["max_tokens"])
				}
				if kind == constant.ChannelTypeZCode {
					return selfContractResponse(200, `{"id":"fixture","type":"message","role":"assistant","model":"gpt-3.5-turbo","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`)
				}
				if kind == constant.ChannelTypeQoder {
					frame, err := common.Marshal(map[string]any{"statusCodeValue": 200, "body": `{"id":"fixture","model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`})
					require.NoError(t, err)
					done, err := common.Marshal(map[string]any{"statusCodeValue": 200, "body": "[DONE]"})
					require.NoError(t, err)
					response, err := selfContractResponse(200, "data:"+string(frame)+string([]byte{10, 10})+"data:"+string(done)+string([]byte{10, 10}))
					response.Header.Set("Content-Type", "text/event-stream")
					return response, err
				}
				return selfContractResponse(200, `{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
			})
			setSelfContractTransport(t, transport)
			t.Cleanup(service.SetTenantHTTPClientFactoryForTest(func(*url.URL, service.HTTPTransportPolicy) (*http.Client, error) {
				return &http.Client{Transport: transport}, nil
			}))
			if kind == constant.ChannelTypeZCode {
				// The production ZCode adapter forbids insecure TLS; use the
				// transport seam rather than weakening that security invariant.
				w := f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo"]}`, nil)
				require.Equal(t, 200, w.Code, w.Body.String())
				assert.Contains(t, w.Body.String(), `"status":"available"`, w.Body.String())
				assert.NotContains(t, w.Body.String(), "fixture-native-token")
				assert.Equal(t, 1, calls)
				return
			}
			fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response, err := transport(r)
				require.NoError(t, err)
				defer response.Body.Close()
				for key, values := range response.Header {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.WriteHeader(response.StatusCode)
				_, err = io.Copy(w, response.Body)
				require.NoError(t, err)
			}))
			t.Cleanup(fixture.Close)
			// This isolated fixture transport does not mutate any shared client.
			strict := &http.Transport{ForceAttemptHTTP2: false, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
			strict.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				dialer := &tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}}
				return dialer.DialContext(ctx, "tcp", fixture.Listener.Addr().String())
			}
			nativeClient := &http.Client{Transport: strict}
			previousNativeFactory := service.QoderHTTPClientFactory
			service.QoderHTTPClientFactory = func(string) (*http.Client, error) { return nativeClient, nil }
			t.Cleanup(func() {
				service.QoderHTTPClientFactory = previousNativeFactory
				strict.CloseIdleConnections()
			})
			w := f.request(http.MethodPost, "", `{"models":["gpt-3.5-turbo"]}`, nil)
			require.Equal(t, 200, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `"status":"available"`, w.Body.String())
			assert.NotContains(t, w.Body.String(), "fixture-native-token")
			expectedCalls := 1
			if kind == constant.ChannelTypeQoder {
				expectedCalls = 2
			}
			assert.Equal(t, expectedCalls, calls)
		})
	}
}

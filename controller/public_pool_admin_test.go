package controller

import (
	"bytes"
	"fmt"
	"io"
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
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func publicAdminRequest(t *testing.T, handler gin.HandlerFunc, method, target, body string, role, userID, channelID int) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(method, target, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", userID)
	c.Set("role", role)
	if channelID != 0 {
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(channelID)}}
	}
	handler(c)
	return response
}

func TestPublicPoolAdminRejectsNonRootBeforeDatabaseAccess(t *testing.T) {
	for _, handler := range []gin.HandlerFunc{ListPublicPoolAdminChannels, GetPublicPoolAdminChannel, AddPublicPoolAdminChannel, UpdatePublicPoolAdminChannel, DeletePublicPoolAdminChannel, CopyPublicPoolAdminChannel, SetPublicPoolAdminChannelStatus} {
		for _, role := range []int{0, common.RoleCommonUser, common.RoleAdminUser} {
			response := publicAdminRequest(t, handler, http.MethodPost, "/api/public_pool/admin/channels/1", `{}`, role, 1, 1)
			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.Contains(t, response.Body.String(), `"success":false`)
		}
	}
}

func TestPublicPoolAdminListsOnlyFreePlatformAndNeverReturnsProviderSecrets(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	secretURL := "https://fixture.invalid/?access_token=fixture-secret-url"
	settings := `{"proxy":"http://fixture-user:fixture-password@fixture.invalid:8080"}`
	header := `{"Authorization":"Bearer fixture-secret-header"}`
	for _, channel := range []*model.Channel{
		{Name: "platform-free", OwnerId: 0, Tier: "free", Key: "fixture-secret-key", Type: 1, Models: "model-a", Group: "default", BaseURL: &secretURL, Setting: &settings, HeaderOverride: &header},
		{Name: "platform-paid", OwnerId: 0, Tier: "api", Key: "paid-key", Type: 1, Models: "model-paid", Group: "default"},
		{Name: "private-owner", OwnerId: rootID, Tier: "free", Key: "private-key", Type: 1, Models: "private-model", Group: "default"},
		{Name: "shared-owner", OwnerId: 42, Tier: "free", Visibility: 1, Key: "shared-key", Type: 1, Models: "shared-model", Group: "default"},
	} {
		require.NoError(t, model.DB.Create(channel).Error)
	}
	response := publicAdminRequest(t, ListPublicPoolAdminChannels, http.MethodGet, "/api/public_pool/admin/channels?keyword=platform&page=1&page_size=10", "", common.RoleRootUser, rootID, 0)
	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Len(t, payload.Data.Items, 1)
	assert.Equal(t, 1, payload.Data.Total)
	assert.Equal(t, "platform-free", payload.Data.Items[0]["name"])
	for _, secret := range []string{"fixture-secret", "fixture-password", "private-owner", "shared-owner", "platform-paid", `"key"`, `"base_url"`, `"setting"`, `"header_override"`} {
		assert.NotContains(t, response.Body.String(), secret)
	}
	for _, query := range []string{"no-match", "private-model", "shared-model", "model-paid"} {
		response = publicAdminRequest(t, ListPublicPoolAdminChannels, http.MethodGet, "/api/public_pool/admin/channels?keyword="+query, "", common.RoleRootUser, rootID, 0)
		assert.Contains(t, response.Body.String(), `"items":[]`, "query=%s", query)
	}
}

func TestPublicPoolAdminRejectsOtherOwnersAndPaidChannelsWithoutMutation(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	for _, channel := range []*model.Channel{
		{Name: "private", OwnerId: rootID, Tier: "free", Type: 1, Key: "fixture-private", Models: "model-a", Group: "default"},
		{Name: "shared", OwnerId: 42, Visibility: 1, Tier: "free", Type: 1, Key: "fixture-shared", Models: "model-a", Group: "default"},
		{Name: "paid", OwnerId: 0, Tier: "api", Type: 1, Key: "fixture-paid", Models: "model-a", Group: "default"},
	} {
		require.NoError(t, model.DB.Create(channel).Error)
		for _, handler := range []gin.HandlerFunc{GetPublicPoolAdminChannel, UpdatePublicPoolAdminChannel, DeletePublicPoolAdminChannel, CopyPublicPoolAdminChannel, SetPublicPoolAdminChannelStatus} {
			response := publicAdminRequest(t, handler, http.MethodPost, "/api/public_pool/admin/channels/1", `{"name":"changed","type":1,"models":"model-b","group":"default","status":1}`, common.RoleRootUser, rootID, channel.Id)
			assert.Equal(t, http.StatusNotFound, response.Code)
		}
		unchanged, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		assert.Equal(t, channel.Name, unchanged.Name)
		assert.Equal(t, channel.OwnerId, unchanged.OwnerId)
		assert.Equal(t, channel.Tier, unchanged.Tier)
	}
}

func TestPublicPoolAdminCRUDKeepsPlatformFreeScopeAndKeyServerSide(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	response := publicAdminRequest(t, AddPublicPoolAdminChannel, http.MethodPost, "/api/public_pool/admin/channels", `{"name":"new-free","type":1,"models":"model-a","group":"default","key":"fixture-provider-key","base_url":"https://fixture.invalid/v1"}`, common.RoleRootUser, rootID, 0)
	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success, response.Body.String())
	require.Positive(t, payload.Data.ID)
	id := payload.Data.ID
	created, err := model.GetChannelById(id, true)
	require.NoError(t, err)
	assert.Zero(t, created.OwnerId)
	assert.Equal(t, "free", created.Tier)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, created.Status, "new public channels must not enable untested models")
	createdKey, err := common.DecryptSecret(created.Key)
	require.NoError(t, err)
	assert.Equal(t, "fixture-provider-key", createdKey)

	response = publicAdminRequest(t, UpdatePublicPoolAdminChannel, http.MethodPut, "/api/public_pool/admin/channels/1", `{"name":"renamed","type":1,"models":"model-a,model-b","group":"default","priority":0,"weight":0}`, common.RoleRootUser, rootID, id)
	require.Contains(t, response.Body.String(), `"success":true`)
	assert.NotContains(t, response.Body.String(), "fixture-provider-key")
	updated, err := model.GetChannelById(id, true)
	require.NoError(t, err)
	assert.Equal(t, "renamed", updated.Name)
	updatedKey, err := common.DecryptSecret(updated.Key)
	require.NoError(t, err)
	assert.Equal(t, "fixture-provider-key", updatedKey)
	assert.Equal(t, "model-a,model-b", updated.Models)

	response = publicAdminRequest(t, SetPublicPoolAdminChannelStatus, http.MethodPost, "/api/public_pool/admin/channels/1/status", `{"status":1}`, common.RoleRootUser, rootID, id)
	require.Contains(t, response.Body.String(), `"success":true`)
	enabled, err := model.GetChannelById(id, false)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, enabled.Status)
	var ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ? AND model = ?", id, "model-b").First(&ability).Error)
	assert.True(t, ability.Enabled)

	response = publicAdminRequest(t, CopyPublicPoolAdminChannel, http.MethodPost, "/api/public_pool/admin/channels/1/copy", "", common.RoleRootUser, rootID, id)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success, response.Body.String())
	clone, err := model.GetChannelById(payload.Data.ID, true)
	require.NoError(t, err)
	assert.Equal(t, "renamed_copy", clone.Name)
	assert.Equal(t, updated.Key, clone.Key)
	assert.Equal(t, 0, clone.OwnerId)
	assert.Equal(t, "free", clone.Tier)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, clone.Status)
	assert.NotContains(t, response.Body.String(), updated.Key)

	response = publicAdminRequest(t, GetPublicPoolAdminChannel, http.MethodGet, "/api/public_pool/admin/channels/1", "", common.RoleRootUser, rootID, id)
	assert.Contains(t, response.Body.String(), `"name":"renamed"`)
	assert.NotContains(t, response.Body.String(), `"key"`)
	response = publicAdminRequest(t, DeletePublicPoolAdminChannel, http.MethodDelete, "/api/public_pool/admin/channels/1", "", common.RoleRootUser, rootID, id)
	require.Contains(t, response.Body.String(), `"success":true`)
	_, err = model.GetChannelById(id, true)
	assert.Error(t, err)
	_, err = model.GetChannelById(clone.Id, true)
	assert.NoError(t, err)
}

func TestPublicPoolAdminRejectsInvalidRoutingInputs(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	for _, patch := range []map[string]any{
		{"type": 9999}, {"type": -1}, {"priority": -1}, {"weight": -1},
		{"name": strings.Repeat("x", 129)}, {"models": "model-a,,model-b"},
		{"models": strings.Repeat("x", 256)}, {"group": strings.Repeat("x", 65)},
		{"base_url": "file:///fixture"}, {"base_url": "https://"},
	} {
		fields := map[string]any{"name": "invalid-routing", "type": 1, "models": "model-a", "group": "default", "key": "fixture-input-key"}
		for key, value := range patch {
			fields[key] = value
		}
		raw, err := common.Marshal(fields)
		require.NoError(t, err)
		response := publicAdminRequest(t, AddPublicPoolAdminChannel, http.MethodPost, "/api/public_pool/admin/channels", string(raw), common.RoleRootUser, rootID, 0)
		assert.Contains(t, response.Body.String(), `"success":false`, "input=%v", patch)
		assert.NotContains(t, response.Body.String(), "fixture-input-key")
	}
}

func TestPublicPoolAdminRejectsScopeAndStatusInjection(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	for _, extra := range []string{`,"owner_id":42`, `,"tier":"api"`, `,"status":1`, `,"header_override":"secret"`} {
		body := `{"name":"invalid","type":1,"models":"model-a","group":"default","key":"fixture-key"` + extra + `}`
		response := publicAdminRequest(t, AddPublicPoolAdminChannel, http.MethodPost, "/api/public_pool/admin/channels", body, common.RoleRootUser, rootID, 0)
		assert.Contains(t, response.Body.String(), `"success":false`)
	}
	var count int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("name = ?", "invalid").Count(&count).Error)
	assert.Zero(t, count)
}

// ---------------------------------------------------------------------------
// Public-pool IP-dimension probe (root, sampled batch through shared pipeline)
// ---------------------------------------------------------------------------

type publicPoolProbeFixture struct {
	rootID    int
	channel   *model.Channel
	session   *model.UserSession
	calls     atomic.Int32
	endpoints []*model.ProxyEndpoint
}

// newPublicPoolProbeFixture builds a root-owned platform free channel whose
// upstream is a local server, plus a set of public egress endpoints that all
// resolve to the same local server so resolution succeeds and dedupe is
// observable only through explicit duplicate rows.
func newPublicPoolProbeFixture(t *testing.T, endpointRows int) *publicPoolProbeFixture {
	t.Helper()
	require.NoError(t, i18n.Init())
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"model-a":1,"model-b":1}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })
	rootID := setupChannelCreateTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.ModelEndpointHealth{}, &model.ProxyEndpoint{}, &model.ProxySubscription{}, &model.UserSession{}))
	session := selfContractDS2Session(t, rootID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", rootID).Updates(map[string]any{"role": common.RoleRootUser, "group": "default", "quota": 1000000, "setting": `{"billing_preference":"wallet_only"}`}).Error)
	f := &publicPoolProbeFixture{rootID: rootID, session: session}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"fixture","model":"model-a","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	t.Cleanup(upstream.Close)
	// A local HTTP proxy that forwards every sampled egress request to the
	// upstream. Each public endpoint carries a distinct dial port so resolution
	// yields unique egress URLs while still reaching a live listener.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, _ := url.Parse(upstream.URL + r.URL.RequestURI())
		out, _ := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
		out.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(out)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(proxy.Close)
	settings := `{"proxy":"direct","proxy_policy":{"mode":"direct"}}`
	f.channel = &model.Channel{OwnerId: 0, Tier: "free", Type: constant.ChannelTypeOpenAI, Name: "public probe fixture", Key: "fixture-public-key", Models: "model-a,model-b", Group: "default", BaseURL: &upstream.URL, Setting: &settings, Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(f.channel).Error)
	// Public egress endpoints. subscription_id=0 keeps the ACL simple; public=true
	// is mandatory for root platform tests (ResolveModelProbeEndpoint rejects
	// non-public exits for owner_id=0 channels). Distinct ports guarantee unique
	// resolved egress URLs so the sampled batch is not deduplicated away.
	for range endpointRows {
		ep := &model.ProxyEndpoint{UserId: rootID, Enabled: true, Public: true, Url: proxy.URL}
		require.NoError(t, model.DB.Create(ep).Error)
		f.endpoints = append(f.endpoints, ep)
	}
	previousAll, previousSelf, previousPublic := operation_setting.AllModelsFreeBilling, operation_setting.SelfKeyFreeBilling, operation_setting.PlatformPublicFreeBilling
	previousCount := constant.CountToken
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	operation_setting.AllModelsFreeBilling, operation_setting.SelfKeyFreeBilling, operation_setting.PlatformPublicFreeBilling, constant.CountToken = false, false, false, false
	t.Cleanup(func() {
		operation_setting.AllModelsFreeBilling, operation_setting.SelfKeyFreeBilling, operation_setting.PlatformPublicFreeBilling, constant.CountToken = previousAll, previousSelf, previousPublic, previousCount
		constant.StreamingTimeout = previousTimeout
	})
	service.InitHttpClient()
	return f
}

// request issues an authenticated root request against the public-pool admin
// probe handlers, wiring the same session keys the shared authorization reads.
func (f *publicPoolProbeFixture) request(t *testing.T, handler gin.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(method, target, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", f.rootID)
	c.Set("role", common.RoleRootUser)
	c.Set("session_id", f.session.SID)
	c.Set("auth_version", int64(1))
	c.Set("session_version", int64(1))
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(f.channel.Id)}}
	handler(c)
	return response
}

func TestPublicPoolProbeEndpointsPaginationAndEligibleTotal(t *testing.T) {
	f := newPublicPoolProbeFixture(t, 5)
	// Page 1 with a small limit exposes has_more and the full eligible total.
	w := f.request(t, ListPublicPoolProbeEndpoints, http.MethodGet, "/?offset=0&limit=2", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var page struct {
		Success bool `json:"success"`
		Data    struct {
			Endpoints          []model.ModelProbeEndpoint `json:"endpoints"`
			EndpointHasMore    bool                       `json:"endpoint_has_more"`
			EndpointNextOffset int                        `json:"endpoint_next_offset"`
			EligibleTotal      int64                      `json:"eligible_total"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &page))
	require.True(t, page.Success)
	assert.Len(t, page.Data.Endpoints, 2)
	assert.True(t, page.Data.EndpointHasMore)
	assert.Equal(t, 2, page.Data.EndpointNextOffset)
	assert.EqualValues(t, 5, page.Data.EligibleTotal)
	// Page past the first window returns the remainder and clears has_more.
	w = f.request(t, ListPublicPoolProbeEndpoints, http.MethodGet, "/?offset=2&limit=100", "")
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &page))
	assert.Len(t, page.Data.Endpoints, 3)
	assert.False(t, page.Data.EndpointHasMore)
	// Listing never leaks addresses or credentials.
	assert.NotContains(t, w.Body.String(), "fixture-node")
	// Invalid pagination is rejected.
	w = f.request(t, ListPublicPoolProbeEndpoints, http.MethodGet, "/?offset=-1", "")
	assert.Contains(t, w.Body.String(), `"success":false`)
	w = f.request(t, ListPublicPoolProbeEndpoints, http.MethodGet, "/?limit=101", "")
	assert.Contains(t, w.Body.String(), `"success":false`)
}

func TestPublicPoolProbeAllRunsSampledBatchAndDedupes(t *testing.T) {
	f := newPublicPoolProbeFixture(t, 4)
	w := f.request(t, PostPublicPoolProbeAllPublicEndpoints, http.MethodPost, "/", `{"models":["model-a"]}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Success bool                         `json:"success"`
		Data    publicPoolProbeBatchResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success, w.Body.String())
	// Configured egress (id 0) is excluded; only real public nodes are sampled.
	assert.NotContains(t, response.Data.EndpointIDs, 0)
	assert.Equal(t, len(response.Data.EndpointIDs), response.Data.SelectedCount)
	assert.Equal(t, 4, response.Data.SelectedCount)
	assert.EqualValues(t, 4, response.Data.EligibleTotal)
	assert.True(t, response.Data.Sampled)
	assert.True(t, response.Data.HealthNeverDeletes)
	assert.Greater(t, response.Data.CheckedAt, int64(0))
	assert.EqualValues(t, int64(model.ModelEndpointHealthTTL/time.Second), response.Data.TTLSeconds)
	// IDs are deduplicated and unique.
	seen := map[int]bool{}
	for _, id := range response.Data.EndpointIDs {
		assert.False(t, seen[id], "duplicate resolved endpoint id %d", id)
		seen[id] = true
	}
	// Per-model counts reconcile: attempted = available + unavailable + unknown,
	// and total_endpoints equals the sampled endpoint count.
	require.Len(t, response.Data.Models, 1)
	view := response.Data.Models[0]
	assert.Equal(t, "model-a", view.Model)
	assert.Equal(t, response.Data.SelectedCount, view.Total)
	assert.Equal(t, view.Available+view.Unavailable+view.Unknown, view.Attempted)
	assert.Equal(t, view.Total, view.Attempted+view.NotRun)
	assert.Equal(t, response.Data.SelectedCount, view.Available, w.Body.String())
	assert.Zero(t, view.Unknown)
	assert.True(t, view.Passed)
	// Each sampled endpoint was inferred exactly once for the single model.
	assert.Equal(t, int32(response.Data.SelectedCount), f.calls.Load())
	// Health was persisted per endpoint; nothing is deleted, only measured.
	var stored []model.ModelEndpointHealth
	require.NoError(t, model.DB.Where("channel_id = ?", f.channel.Id).Find(&stored).Error)
	assert.Len(t, stored, response.Data.SelectedCount)
	// Response never leaks endpoint addresses or the provider key.
	assert.NotContains(t, w.Body.String(), "fixture-node")
	assert.NotContains(t, w.Body.String(), "fixture-public-key")
}

func TestPublicPoolProbeAllRejectsUnauthorizedAndAccountEgress(t *testing.T) {
	f := newPublicPoolProbeFixture(t, 2)
	// Non-root role is rejected before any work.
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"models":["model-a"]}`))
	c.Set("id", f.rootID)
	c.Set("role", common.RoleAdminUser)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(f.channel.Id)}}
	PostPublicPoolProbeAllPublicEndpoints(c)
	assert.Equal(t, http.StatusForbidden, response.Code)
	// Account-bound channels (credential pinned to configured egress) cannot
	// rotate across public nodes.
	require.NoError(t, model.DB.Model(f.channel).Update("type", constant.ChannelTypeCline).Error)
	w := f.request(t, PostPublicPoolProbeAllPublicEndpoints, http.MethodPost, "/", `{"models":["model-a"]}`)
	assert.Contains(t, w.Body.String(), "Account model tests must use configured egress")
	require.NoError(t, model.DB.Model(f.channel).Update("type", constant.ChannelTypeOpenAI).Error)
	// A model outside the channel catalog is rejected by the shared authorization.
	w = f.request(t, PostPublicPoolProbeAllPublicEndpoints, http.MethodPost, "/", `{"models":["not-in-catalog"]}`)
	assert.Contains(t, w.Body.String(), `"success":false`)
	assert.Zero(t, f.calls.Load())
}

func TestPublicPoolProbeAllStopsOnFatalUpstream(t *testing.T) {
	f := newPublicPoolProbeFixture(t, 4)
	// Force the upstream to deny every request; the shared runner must stop the
	// batch on the first fatal admission error rather than probe every node.
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.rootID).Update("quota", 0).Error)
	w := f.request(t, PostPublicPoolProbeAllPublicEndpoints, http.MethodPost, "/", `{"models":["model-a","model-b"]}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Success bool                         `json:"success"`
		Data    publicPoolProbeBatchResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success, w.Body.String())
	// Quota exhaustion is fatal: only one real attempt happened across both
	// models; the rest of the Cartesian sample is marked not_run.
	assert.Zero(t, f.calls.Load(), "quota exhaustion stops before any paid inference")
	var attempted, notRun int
	for _, view := range response.Data.Models {
		attempted += view.Attempted
		notRun += view.NotRun
	}
	assert.Equal(t, 1, attempted, "exactly one fatal attempt is recorded")
	assert.Greater(t, notRun, 0, "remaining sample cells are not_run")
}

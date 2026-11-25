package nousportal

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type nousTestTransport func(*http.Request) (*http.Response, error)

func (f nousTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func setupNousRefreshTest(t *testing.T) (*model.Channel, *service.NousCredential) {
	t.Helper()
	previousDB, previousCache := model.DB, common.MemoryCacheEnabled
	previousCrypto, previousConfigured := common.CryptoSecret, common.CryptoSecretConfigured
	previousRenew, previousFactory := renewNousCredential, service.QoderHTTPClientFactory
	common.CryptoSecret, common.CryptoSecretConfigured = "nousportal-refresh-test-crypto-key", true
	common.MemoryCacheEnabled = false
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = database
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}))
	cred := &service.NousCredential{AccessToken: "old-access-fixture", RefreshToken: "old-refresh-fixture", ExpiresAt: time.Now().Add(time.Minute).Unix(), Egress: service.AccountEgress{Key: "fixture-account", Proxy: ""}}
	raw, err := common.Marshal(cred)
	require.NoError(t, err)
	encrypted, err := common.EncryptSecret(string(raw))
	require.NoError(t, err)
	channel := &model.Channel{OwnerId: 701, Type: constant.ChannelTypeNousPortal, Key: encrypted, Models: "fixture-model", Group: "default", Status: common.ChannelStatusEnabled}
	direct := `{"proxy":"direct","proxy_policy":{"mode":"direct"}}`
	channel.Setting = &direct
	require.NoError(t, database.Create(channel).Error)
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled = previousDB, previousCache
		common.CryptoSecret, common.CryptoSecretConfigured = previousCrypto, previousConfigured
		renewNousCredential, service.QoderHTTPClientFactory = previousRenew, previousFactory
		nousRefresh.Forget(strconv.Itoa(channel.Id))
	})
	return channel, cred
}

func TestNousRequestUsesRefreshedCredentialAndPreservesEgress(t *testing.T) {
	channel, cred := setupNousRefreshTest(t)
	var refreshCalls atomic.Int32
	renewNousCredential = func(_ context.Context, proxy string, old *service.NousCredential) (*service.NousCredential, error) {
		refreshCalls.Add(1)
		assert.Empty(t, proxy)
		assert.Equal(t, cred.AccessToken, old.AccessToken)
		return &service.NousCredential{AccessToken: "new-access-fixture", RefreshToken: "new-refresh-fixture", ExpiresAt: time.Now().Add(time.Hour).Unix(), BaseURL: service.NousInferenceBase}, nil
	}
	service.QoderHTTPClientFactory = func(proxy string) (*http.Client, error) {
		assert.Empty(t, proxy)
		return &http.Client{Transport: nousTestTransport(func(request *http.Request) (*http.Response, error) {
			assert.Equal(t, "Bearer new-access-fixture", request.Header.Get("Authorization"))
			assert.NotContains(t, request.Header.Get("Authorization"), cred.AccessToken)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
		})}, nil
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{OriginModelName: "fixture-model", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: channel.Type, ApiKey: channel.Key, UpstreamModelName: "fixture-model"}}
	response, err := (&Adaptor{}).DoRequest(ctx, info, bytes.NewBufferString(`{"model":"fixture-model"}`))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.(*http.Response).StatusCode)
	assert.EqualValues(t, 1, refreshCalls.Load())
	var stored model.Channel
	require.NoError(t, model.DB.First(&stored, channel.Id).Error)
	updated := model.DecryptChannelKey(stored.Type, stored.Key)
	assert.Contains(t, updated, "new-access-fixture")
	assert.Contains(t, updated, "new-refresh-fixture")
	assert.Contains(t, updated, `"key":"fixture-account"`)
}

func TestNousRefreshWriteConflictUsesNewestCredential(t *testing.T) {
	channel, oldCred := setupNousRefreshTest(t)
	next := &service.NousCredential{AccessToken: "winner-access", RefreshToken: "winner-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(), Egress: oldCred.Egress}
	raw, err := common.Marshal(next)
	require.NoError(t, err)
	encrypted, err := common.EncryptSecret(string(raw))
	require.NoError(t, err)
	renewNousCredential = func(context.Context, string, *service.NousCredential) (*service.NousCredential, error) {
		// Simulate another node committing between our provider refresh and CAS.
		require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("key", encrypted).Error)
		return &service.NousCredential{AccessToken: "loser-access", RefreshToken: "loser-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()}, nil
	}
	stored, cred, err := refreshStoredNousCredential(context.Background(), channel, *oldCred)
	require.NoError(t, err)
	assert.Equal(t, encrypted, stored.Key)
	assert.Equal(t, "winner-access", cred.AccessToken)
	assert.Equal(t, "winner-refresh", cred.RefreshToken)
	assert.Equal(t, oldCred.Egress, cred.Egress)
}

func TestNousConcurrentlyRotatedCredentialIsReused(t *testing.T) {
	channel, oldCred := setupNousRefreshTest(t)
	next := &service.NousCredential{AccessToken: "fresh-from-other-node", RefreshToken: "fresh-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(), Egress: oldCred.Egress}
	raw, err := common.Marshal(next)
	require.NoError(t, err)
	encrypted, err := common.EncryptSecret(string(raw))
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(channel).Update("key", encrypted).Error)
	renewNousCredential = func(context.Context, string, *service.NousCredential) (*service.NousCredential, error) {
		t.Fatal("must not replay the old refresh token")
		return nil, nil
	}
	service.QoderHTTPClientFactory = func(string) (*http.Client, error) {
		return &http.Client{Transport: nousTestTransport(func(request *http.Request) (*http.Response, error) {
			assert.Equal(t, "Bearer fresh-from-other-node", request.Header.Get("Authorization"))
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
		})}, nil
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{OriginModelName: "fixture-model", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: channel.Type, ApiKey: channel.Key, UpstreamModelName: "fixture-model"}}
	_, err = (&Adaptor{}).DoRequest(ctx, info, strings.NewReader(`{}`))
	require.NoError(t, err)
}

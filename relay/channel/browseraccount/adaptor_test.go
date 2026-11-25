package browseraccount

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func browserAccountFixture(t *testing.T, kind int, token string, expiry int64) (*model.Channel, *relaycommon.RelayInfo, *gin.Context, service.AccountEgress) {
	t.Helper()
	oldDB := model.DB
	oldSecret, oldConfigured := common.CryptoSecret, common.CryptoSecretConfigured
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Option{}))
	model.DB = db
	common.CryptoSecret = "browser-account-adapter-test-only"
	common.CryptoSecretConfigured = true
	t.Cleanup(func() {
		model.DB = oldDB
		common.CryptoSecret, common.CryptoSecretConfigured = oldSecret, oldConfigured
		require.NoError(t, sqlDB.Close())
	})
	egress := service.AccountEgress{Key: "test-owner-egress"}
	var cred any
	if kind == constant.ChannelTypeCline {
		cred = service.ClineCredential{Token: token, UserID: "owner-42", ExpiresAt: expiry, Egress: egress}
	} else {
		cred = service.RaccoonCredential{Token: token, UserID: "owner-42", ExpiresAt: expiry, Egress: egress}
	}
	raw, err := common.Marshal(cred)
	require.NoError(t, err)
	enc, err := common.EncryptSecret(string(raw))
	require.NoError(t, err)
	require.True(t, common.IsEncryptedSecret(enc))
	setting, err := common.Marshal(dto.ChannelSettings{ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "direct"}})
	require.NoError(t, err)
	settingText := string(setting)
	stored := &model.Channel{OwnerId: 42, Type: kind, Key: enc, Models: "live/model,other", Setting: &settingText, Status: 1}
	require.NoError(t, db.Create(stored).Error)
	stored, err = model.GetChannelById(stored.Id, true)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: kind, ChannelId: stored.Id, ApiKey: string(raw), UpstreamModelName: "live/model"}, RelayMode: relayconstant.RelayModeChatCompletions}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/chat/completions", nil)
	return stored, info, c, egress
}

func TestBrowserAccountNilMetadataAndFixedDestination(t *testing.T) {
	a := &Adaptor{}
	for _, info := range []*relaycommon.RelayInfo{nil, {}, {ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}} {
		_, err := a.GetRequestURL(info)
		assert.ErrorIs(t, err, service.ErrAccountProtocol)
		_, _, _, _, err = account(info)
		assert.ErrorIs(t, err, service.ErrAccountProtocol)
	}
	for _, kind := range []int{constant.ChannelTypeCline, constant.ChannelTypeRaccoon} {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: kind}, RelayMode: relayconstant.RelayModeChatCompletions}
		target, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, baseURL(kind)+"/chat/completions", target)
		info.ChannelBaseUrl = "https://evil.invalid"
		_, err = a.GetRequestURL(info)
		assert.ErrorIs(t, err, service.ErrAccountProtocol)
	}
}

func TestBrowserAccountHeaderAndModelBoundary(t *testing.T) {
	for _, tc := range []struct {
		name        string
		kind        int
		token, auth string
	}{
		{"cline-raw", constant.ChannelTypeCline, "raw-token", "Bearer workos:raw-token"},
		{"cline-once", constant.ChannelTypeCline, "workos:raw-token", "Bearer workos:raw-token"},
		{"raccoon", constant.ChannelTypeRaccoon, "raw-token", "Bearer raw-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, info, c, _ := browserAccountFixture(t, tc.kind, tc.token, time.Now().Add(time.Hour).Unix())
			headers := http.Header{"Cookie": {"do-not-send"}, "X-Api-Key": {"do-not-send"}, "Origin": {"https://evil.invalid"}, "Authorization": {"do-not-send"}}
			require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &headers, info))
			assert.Equal(t, tc.auth, headers.Get("Authorization"))
			assert.Empty(t, headers.Get("Cookie"))
			assert.Empty(t, headers.Get("X-Api-Key"))
			assert.Equal(t, "application/json", headers.Get("Content-Type"))
			if tc.kind == constant.ChannelTypeRaccoon {
				assert.Equal(t, service.RaccoonOrigin, headers.Get("Origin"))
				assert.Equal(t, service.RaccoonOrigin+"/", headers.Get("Referer"))
			} else {
				assert.Equal(t, service.ClineHeaders().Get("X-Client-Type"), headers.Get("X-Client-Type"))
				assert.Empty(t, headers.Get("Origin"))
			}
			info.UpstreamModelName = "not-discovered"
			_, err := (&Adaptor{}).DoRequest(c, info, strings.NewReader(`{"model":"not-discovered"}`))
			assert.ErrorIs(t, err, service.ErrAccountProtocol)
			info.UpstreamModelName = "live/model"
			info.ApiKey = "different-owner-secret"
			_, err = (&Adaptor{}).DoRequest(c, info, strings.NewReader(`{}`))
			assert.ErrorIs(t, err, service.ErrAccountProtocol)
		})
	}
	t.Run("expired", func(t *testing.T) {
		_, info, c, _ := browserAccountFixture(t, constant.ChannelTypeCline, "expired", time.Now().Add(-time.Second).Unix())
		h := make(http.Header)
		assert.ErrorIs(t, (&Adaptor{}).SetupRequestHeader(c, &h, info), service.ErrDesktopLoginExpired)
	})
	t.Run("nested-prefix", func(t *testing.T) {
		_, info, _, _ := browserAccountFixture(t, constant.ChannelTypeCline, "workos:workos:token", 0)
		_, _, _, _, err := account(info)
		assert.ErrorIs(t, err, service.ErrAccountProtocol)
	})
}

func TestBrowserAccountUsesConcurrentlyRotatedCredential(t *testing.T) {
	for _, kind := range []int{constant.ChannelTypeCline, constant.ChannelTypeRaccoon} {
		t.Run(strconv.Itoa(kind), func(t *testing.T) {
			stored, info, _, egress := browserAccountFixture(t, kind, "old-token", time.Now().Add(time.Minute).Unix())
			var next any
			if kind == constant.ChannelTypeCline {
				next = service.ClineCredential{Token: "new-token", UserID: "owner-42", ExpiresAt: time.Now().Add(time.Hour).Unix(), Egress: egress}
			} else {
				next = service.RaccoonCredential{Token: "new-token", RefreshToken: "new-refresh", UserID: "owner-42", ExpiresAt: time.Now().Add(time.Hour).Unix(), Egress: egress}
			}
			raw, err := common.Marshal(next)
			require.NoError(t, err)
			encrypted, err := common.EncryptSecret(string(raw))
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", stored.Id).Update("key", encrypted).Error)
			latest, err := model.GetChannelById(stored.Id, true)
			require.NoError(t, err)
			rotated, err := rotatedAccountSecret(stored, latest, info.ApiKey)
			require.NoError(t, err)
			assert.JSONEq(t, string(raw), rotated)
			oldSecret := info.ApiKey
			refreshes.Forget(refreshKey(stored, oldSecret))
			<-refreshes.DoChan(refreshKey(stored, oldSecret), func() (any, error) { return nil, nil })
			rotated, err = refreshCredential(context.Background(), stored, egress, oldSecret, "")
			require.NoError(t, err)
			assert.JSONEq(t, string(raw), rotated)
			info.ApiKey = rotated
			updated, nextEgress, token, _, err := account(info)
			require.NoError(t, err)
			assert.Equal(t, encrypted, updated.Key)
			assert.Equal(t, egress, nextEgress)
			assert.Equal(t, "new-token", token)
		})
	}
}

func TestBrowserAccountRefreshRejectsChangedSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch map[string]any
	}{
		{"owner", map[string]any{"owner_id": 43}},
		{"type", map[string]any{"type": constant.ChannelTypeRaccoon}},
		{"key", map[string]any{"key": "replacement"}},
		{"setting", map[string]any{"setting": `{"proxy":"http://different.invalid:9000"}`}},
		{"catalog", map[string]any{"models": "revoked"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored, info, _, egress := browserAccountFixture(t, constant.ChannelTypeCline, "token", time.Now().Add(time.Minute).Unix())
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", stored.Id).Updates(tc.patch).Error)
			_, err := refreshCredential(context.Background(), stored, egress, info.ApiKey, "")
			assert.ErrorIs(t, err, service.ErrAccountProtocol)
		})
	}
}

// browserWaitContext observes when refreshCredential reaches its post-DoChan
// cancellation select, proving the caller actually joined the in-flight leader.
type browserWaitContext struct {
	context.Context
	reached chan struct{}
	once    sync.Once
}

func (c *browserWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.reached) })
	return c.Context.Done()
}

func TestBrowserAccountRefreshWaiterCancellation(t *testing.T) {
	stored := &model.Channel{Id: 919, OwnerId: 42, Type: constant.ChannelTypeCline}
	secret := "test-secret-not-a-real-credential"
	entered := make(chan struct{})
	release := make(chan struct{})
	leader := refreshes.DoChan(refreshKey(stored, secret), func() (any, error) { close(entered); <-release; return "unused", nil })
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	observed := &browserWaitContext{Context: ctx, reached: make(chan struct{})}
	go func() {
		_, err := refreshCredential(observed, stored, service.AccountEgress{}, secret, "")
		done <- err
	}()
	select {
	case <-observed.reached:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("duplicate did not join refresh flight")
	}
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("canceled refresh waiter did not return")
	}
	close(release)
	select {
	case result := <-leader:
		assert.NoError(t, result.Err)
	case <-time.After(2 * time.Second):
		t.Fatal("test leader did not terminate")
	}
}

func TestBrowserAccountStrictTransportPinnedEgress(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://environment.invalid:9")
	client, err := service.NewQoderHTTPClient("")
	require.NoError(t, err)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, transport.Proxy)
	require.NotNil(t, transport.TLSClientConfig)
	assert.False(t, transport.TLSClientConfig.InsecureSkipVerify)
	assert.GreaterOrEqual(t, transport.TLSClientConfig.MinVersion, uint16(tls.VersionTLS12))
	assert.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	entered := make(chan string, 1)
	finished := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- r.Method + " " + r.Host
		<-r.Context().Done()
		close(finished)
	}))
	defer proxy.Close()
	pinned, err := service.NewQoderHTTPClient(proxy.URL)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, service.ClineAPIBase+"/chat/completions", strings.NewReader(`{}`))
		resp, err := pinned.Do(req)
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		done <- err
	}()
	select {
	case target := <-entered:
		assert.True(t, strings.HasPrefix(target, "CONNECT "))
		assert.NotContains(t, target, "environment.invalid")
	case <-time.After(2 * time.Second):
		t.Fatal("pinned proxy was not contacted")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.Canceled))
	case <-time.After(2 * time.Second):
		t.Fatal("pinned request did not cancel")
	}
	// The CONNECT handler must be released explicitly when cancellation tears down
	// the tunnel; closing the local server is part of the integration fixture.
	proxy.CloseClientConnections()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("local proxy request did not terminate")
	}
}

func TestBrowserAccountDispatchPinnedProxyCancellation(t *testing.T) {
	stored, info, c, egress := browserAccountFixture(t, constant.ChannelTypeCline, "owner-token", time.Now().Add(time.Hour).Unix())
	entered := make(chan string, 1)
	finished := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- r.Method + " " + r.Host
		<-r.Context().Done()
		close(finished)
	}))
	defer proxy.Close()
	egress.Proxy = proxy.URL
	cred := service.ClineCredential{Token: "owner-token", UserID: "owner-42", ExpiresAt: time.Now().Add(time.Hour).Unix(), Egress: egress}
	raw, err := common.Marshal(cred)
	require.NoError(t, err)
	encrypted, err := common.EncryptSecret(string(raw))
	require.NoError(t, err)
	settings, err := common.Marshal(dto.ChannelSettings{Proxy: proxy.URL, ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "fixed"}})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", stored.Id).Updates(map[string]any{"key": encrypted, "setting": string(settings)}).Error)
	info.ApiKey = string(raw)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	// Even a different incoming method must not change the fixed upstream POST.
	c.Request.Method = http.MethodPatch
	done := make(chan error, 1)
	go func() {
		resp, err := (&Adaptor{}).DoRequest(c, info, strings.NewReader(`{"model":"live/model","messages":[{"role":"user","content":"hi"}]}`))
		if resp != nil {
			resp.(*http.Response).Body.Close()
		}
		done <- err
	}()
	select {
	case target := <-entered:
		assert.True(t, strings.HasPrefix(target, "CONNECT "))
		expected, err := url.Parse(service.ClineAPIBase)
		require.NoError(t, err)
		assert.Equal(t, "CONNECT "+expected.Host+":443", target)
	case <-time.After(2 * time.Second):
		t.Fatal("adapter did not use the owner's pinned proxy")
	}
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("adapter request did not return after cancellation")
	}
	proxy.CloseClientConnections()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("proxy fixture did not terminate")
	}
}

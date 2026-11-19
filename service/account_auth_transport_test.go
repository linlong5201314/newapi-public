package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every endpoint is a local server or an intercepted RoundTripper. The synthetic
// credentials in this file must never be sent to a public provider.
func isolateAccountAuthTestCache(t *testing.T) {
	t.Helper()
	accountAuthTransports.mutex.Lock()
	previousClients, previousAliases := accountAuthTransports.clients, accountAuthTransports.aliases
	accountAuthTransports.clients = make(map[string]*http.Client)
	accountAuthTransports.aliases = make(map[string]string)
	accountAuthTransports.mutex.Unlock()
	t.Cleanup(func() {
		for _, client := range accountAuthTransports.reset() {
			client.CloseIdleConnections()
		}
		accountAuthTransports.mutex.Lock()
		accountAuthTransports.clients, accountAuthTransports.aliases = previousClients, previousAliases
		accountAuthTransports.mutex.Unlock()
	})
}

func accountAuthTestClient(t *testing.T, proxy, protocol string) *http.Client {
	t.Helper()
	pooled, err := GetAccountAuthHTTPClient(proxy, protocol)
	require.NoError(t, err)
	transport, ok := pooled.Transport.(*http.Transport)
	require.True(t, ok)
	client := *pooled
	client.Transport = transport.Clone()
	t.Cleanup(client.CloseIdleConnections)
	return &client
}

func TestAccountAuthDirectIgnoresAmbientProxyAndDefaultTransport(t *testing.T) {
	isolateAccountAuthTestCache(t)
	var ambientCalls atomic.Int32
	ambient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ambientCalls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(ambient.Close)
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, ambient.URL)
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		t.Setenv(key, "")
	}
	oldDefault := http.DefaultTransport
	http.DefaultTransport = &http.Transport{Proxy: http.ProxyURL(mustAccountAuthURL(t, ambient.URL)), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	t.Cleanup(func() { http.DefaultTransport = oldDefault })
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer synthetic-access", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	for _, protocol := range []string{"", dto.HTTPProtocolHTTP1} {
		t.Run("protocol-"+protocol, func(t *testing.T) {
			client := accountAuthTestClient(t, "", protocol)
			transport := client.Transport.(*http.Transport)
			assert.Nil(t, transport.Proxy, "direct must not consult cached ProxyFromEnvironment")
			transport.TLSClientConfig.RootCAs = testTLSClientConfig(t, server).RootCAs
			// Use a non-loopback hostname so Go's built-in localhost proxy bypass
			// cannot make an accidentally ambient-proxied request pass this test.
			transport.TLSClientConfig.ServerName = "example.com"
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				assert.Equal(t, "provider.invalid:443", address)
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			req, err := http.NewRequest(http.MethodGet, "https://provider.invalid/identity", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer synthetic-access")
			response, err := client.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, response.StatusCode)
			drainClose(t, response)
		})
	}
	assert.Zero(t, ambientCalls.Load())
}

func mustAccountAuthURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func TestAccountAuthRejectsUntrustedTLSDespiteGlobalBypass(t *testing.T) {
	isolateAccountAuthTestCache(t)
	oldInsecure, oldConfig := common.TLSInsecureSkipVerify, common.InsecureTLSConfig
	common.TLSInsecureSkipVerify = true
	common.InsecureTLSConfig = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10}
	t.Cleanup(func() { common.TLSInsecureSkipVerify, common.InsecureTLSConfig = oldInsecure, oldConfig })
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	for _, protocol := range []string{"", dto.HTTPProtocolHTTP1} {
		t.Run("protocol-"+protocol, func(t *testing.T) {
			client := accountAuthTestClient(t, "", protocol)
			transport := client.Transport.(*http.Transport)
			assert.False(t, transport.TLSClientConfig.InsecureSkipVerify)
			assert.GreaterOrEqual(t, transport.TLSClientConfig.MinVersion, uint16(tls.VersionTLS12))
			req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("synthetic-password"))
			require.NoError(t, err)
			_, err = client.Do(req)
			var certError *tls.CertificateVerificationError
			require.ErrorAs(t, err, &certError)
		})
	}
	assert.Zero(t, calls.Load(), "no credential body before verified TLS")
}

func TestAccountAuthDS2NegotiatesHTTP1WithoutDowngradingOtherProviders(t *testing.T) {
	isolateAccountAuthTestCache(t)
	server := startHTTP2TLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test-Protocol", r.Proto)
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		protocol string
		want     int
	}{{"", 2}, {dto.HTTPProtocolHTTP1, 1}} {
		t.Run(fmt.Sprintf("http-%d", tc.want), func(t *testing.T) {
			client := accountAuthTestClient(t, "", tc.protocol)
			transport := client.Transport.(*http.Transport)
			transport.TLSClientConfig.RootCAs = testTLSClientConfig(t, server).RootCAs
			if tc.protocol == dto.HTTPProtocolHTTP1 {
				// Exercise the exact DS2 wrapper as well as the HTTP/1.1 transport.
				client.Transport = accountAuthNoReplayTransport{RoundTripper: transport}
			}
			response, err := client.Get(server.URL)
			require.NoError(t, err)
			assert.Equal(t, tc.want, response.ProtoMajor)
			require.NotNil(t, response.TLS)
			assert.GreaterOrEqual(t, response.TLS.Version, uint16(tls.VersionTLS12))
			drainClose(t, response)
		})
	}
}

func TestAccountAuthExplicitHTTPProxyPinsExitAndFailsClosed(t *testing.T) {
	isolateAccountAuthTestCache(t)
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "Basic Zml4dHVyZS11c2VyOmZpeHR1cmUtcGFzcw==", r.Header.Get("Proxy-Authorization"))
		assert.Equal(t, "http://provider.invalid/identity", r.URL.String())
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(proxy.Close)
	u := mustAccountAuthURL(t, proxy.URL)
	u.User = url.UserPassword("fixture-user", "fixture-pass")
	for _, key := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
		t.Setenv(key, "http://127.0.0.1:1")
	}
	t.Setenv("NO_PROXY", "*")
	client := accountAuthTestClient(t, u.String(), "")
	response, err := client.Get("http://provider.invalid/identity")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	drainClose(t, response)
	assert.Equal(t, int32(1), calls.Load(), "explicit proxy is used even when NO_PROXY=*")
	// The same pinned exit fails; there is no retry via environment or direct.
	proxy.Close()
	client.CloseIdleConnections()
	_, err = client.Get("http://provider.invalid/identity")
	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load())
	for _, raw := range []string{" 	", "http://fixture-user:fixture-pass@proxy.invalid/path", "http://proxy.invalid?token=fixture-secret", "file:///tmp/proxy"} {
		_, err := GetAccountAuthHTTPClient(raw, "")
		require.ErrorIs(t, err, ErrAccountEgress)
		assert.NotContains(t, err.Error(), "fixture-")
	}
}

func TestAccountAuthRedirectNeverReplaysPOSTOrStoresCookies(t *testing.T) {
	isolateAccountAuthTestCache(t)
	var leaked atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(fmt.Sprintf("redirect-%d", status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Empty(t, r.Header.Get("Cookie"))
				raw, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, "synthetic-one-shot-secret", string(raw))
				http.SetCookie(w, &http.Cookie{Name: "upstream-session", Value: "synthetic-cookie"})
				w.Header().Set("Location", target.URL+"/steal?secret=synthetic-one-shot-secret")
				w.WriteHeader(status)
			}))
			t.Cleanup(server.Close)
			client := accountAuthTestClient(t, "", "")
			pool := x509.NewCertPool()
			pool.AddCert(server.Certificate())
			pool.AddCert(target.Certificate())
			client.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool
			assert.Nil(t, client.Jar)
			for range 2 {
				response, err := client.Post(server.URL, "application/json", strings.NewReader("synthetic-one-shot-secret"))
				require.NoError(t, err)
				assert.Equal(t, status, response.StatusCode)
				drainClose(t, response)
			}
			assert.Equal(t, int32(2), requests.Load())
		})
	}
	assert.Zero(t, leaked.Load(), "neither token-bearing GET redirect nor 307/308 POST may escape")
}

func TestAccountAuthProvidersBoundAndNeverReplayCredentialPOST(t *testing.T) {
	isolateAccountAuthTestCache(t)
	for _, provider := range []string{"cline-poll", "cline-register", "cline-refresh", "raccoon-exchange", "raccoon-refresh", "ds2-login", "zcode-init"} {
		t.Run(provider, func(t *testing.T) {
			protocol := ""
			if provider == "ds2-login" {
				protocol = dto.HTTPProtocolHTTP1
			}
			client, err := GetAccountAuthHTTPClient("", protocol)
			require.NoError(t, err)
			old := *client
			t.Cleanup(func() { *client = old })
			jar, err := cookiejar.New(nil)
			require.NoError(t, err)
			for _, host := range []string{"https://api.workos.com", "https://api.cline.bot", RaccoonOrigin, "https://chat.deepseek.com", "https://zcode.z.ai"} {
				jar.SetCookies(mustAccountAuthURL(t, host), []*http.Cookie{{Name: "local-cookie", Value: "synthetic-session"}})
			}
			client.Jar = jar
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				t.Fatal("provider must not use shared redirect policy")
				return nil
			}
			calls := 0
			client.Transport = clineTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Nil(t, r.GetBody, "transport must not rewind possibly spent credentials")
				assert.Empty(t, r.Header.Get("Cookie"))
				assert.Empty(t, r.Header.Get("Proxy-Authorization"))
				deadline, ok := r.Context().Deadline()
				assert.True(t, ok)
				assert.LessOrEqual(t, time.Until(deadline), 30*time.Second)
				if provider == "cline-register" && calls == 1 {
					return clineTestResponse(r, 200, `{"access_token":"synthetic-access","refresh_token":"synthetic-refresh"}`), nil
				}
				return nil, errors.New("https://fixture-user:fixture-password@proxy.invalid/?secret=synthetic-one-shot-secret")
			})
			switch provider {
			case "cline-poll", "cline-register":
				_, err = PollClineSignIn(context.Background(), "", &ClineTicket{DeviceCode: "synthetic-code", Until: time.Now().Add(time.Minute).Unix(), Interval: 5})
			case "cline-refresh":
				_, err = RenewClineCredential(context.Background(), "", &ClineCredential{UserID: "owner", RefreshToken: "synthetic-refresh"})
			case "raccoon-exchange":
				_, _, err = CompleteRaccoonAuthorization(context.Background(), "", "synthetic-code")
			case "raccoon-refresh":
				_, err = RefreshRaccoonAccount(context.Background(), "", &RaccoonCredential{UserID: "owner", RefreshToken: "synthetic-refresh"})
			case "ds2-login":
				_, err = LoginDS2Account(context.Background(), "fixture@example.invalid", "synthetic-password", "")
			case "zcode-init":
				_, _, err = StartZCodeSignIn(context.Background(), "", "bigmodel")
			}
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "fixture-password")
			assert.NotContains(t, err.Error(), "synthetic-")
			assert.NotContains(t, err.Error(), "proxy.invalid")
			wantCalls := 1
			if provider == "cline-register" {
				wantCalls = 2
			}
			assert.Equal(t, wantCalls, calls)
			assert.Same(t, jar, client.Jar, "shared policy must remain unchanged")
		})
	}
}

// Exercise the active helpers, not merely the strict client factory. Every dial
// is pinned to the local TLS fixture, including when a regression is detected.
func TestAccountAuthActiveDesktopPathsUseStrictTransport(t *testing.T) {
	for _, path := range []struct {
		name      string
		run       func(context.Context) error
		wantCalls int32
	}{
		{"zcode-init", func(ctx context.Context) error {
			_, _, err := StartZCodeSignIn(ctx, "", "bigmodel")
			return err
		}, 1},
		{"zcode-browser-return", func(ctx context.Context) error {
			return CompleteZCodeBrowserReturn(ctx, "", "https://bigmodel.cn/login?state=synthetic-state", "https://zcode.z.ai/api/v1/oauth/cli/callback/bigmodel?state=synthetic-state&code=synthetic-code", "bigmodel")
		}, 1},
		{"kimi-work-models", func(ctx context.Context) error {
			_, err := ValidateKimiWork(ctx, "", KimiWorkCredential{Source: "kimi-work-desktop", WorkToken: "synthetic-access", UserID: "synthetic-owner"})
			return err
		}, 1},
		{"mimo-start", func(ctx context.Context) error {
			_, _, err := StartMiMoSignIn(ctx, "")
			return err
		}, 2},
	} {
		for _, trusted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/trusted-%t", path.name, trusted), func(t *testing.T) {
				isolateAccountAuthTestCache(t)
				var ambientCalls, providerCalls atomic.Int32
				ambient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ambientCalls.Add(1)
					w.WriteHeader(http.StatusBadGateway)
				}))
				t.Cleanup(ambient.Close)
				for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
					t.Setenv(key, ambient.URL)
				}
				t.Setenv("NO_PROXY", "")
				t.Setenv("no_proxy", "")
				oldDefault, oldClient := http.DefaultTransport, httpClient
				oldInsecure, oldTLS := common.TLSInsecureSkipVerify, common.InsecureTLSConfig
				common.TLSInsecureSkipVerify = true
				common.InsecureTLSConfig = &tls.Config{InsecureSkipVerify: true}
				http.DefaultTransport = &http.Transport{Proxy: http.ProxyURL(mustAccountAuthURL(t, ambient.URL)), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
				httpClient = &http.Client{Transport: clineTestTransport(func(*http.Request) (*http.Response, error) {
					t.Error("active authentication path used the generic relay client")
					return nil, errors.New("generic transport forbidden")
				})}
				t.Cleanup(func() {
					http.DefaultTransport, httpClient = oldDefault, oldClient
					common.TLSInsecureSkipVerify, common.InsecureTLSConfig = oldInsecure, oldTLS
				})
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					providerCalls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/v1/oauth/cli/init":
						assert.Equal(t, http.MethodPost, r.Method)
						_, _ = fmt.Fprintf(w, `{"code":0,"data":{"flow_id":"synthetic-flow","authorize_url":"https://bigmodel.cn/login?state=synthetic-state","expires_at":%d,"poll_interval_sec":2}}`, time.Now().Add(time.Minute).Unix())
					case "/api/v1/oauth/cli/callback/bigmodel":
						assert.Empty(t, r.Header.Get("Authorization"))
						assert.Empty(t, r.Header.Get("Cookie"))
						w.Header().Set("Location", "https://forbidden.invalid/synthetic-code")
						w.WriteHeader(http.StatusFound)
					case "/coding/v1/models":
						assert.Equal(t, "Bearer synthetic-access", r.Header.Get("Authorization"))
						_, _ = io.WriteString(w, `{"data":[{"id":"k3-agent"}],"has_more":false}`)
					case "/api/user/xiaomi/me":
						w.Header().Set("Location", "https://account.xiaomi.com/pass/serviceLogin?sid=mimosgp&callback=https%3A%2F%2Fmimo-server-sgp.xiaomimimo.com%2Fapi%2Fsts")
						w.WriteHeader(http.StatusFound)
					case "/longPolling/loginUrl":
						_, _ = io.WriteString(w, `{"code":0,"loginUrl":"https://account.xiaomi.com/pass/lp?ticket=synthetic","lp":"https://ak.lp.account.xiaomi.com/longPolling/listen?ticket=synthetic","timeout":60}`)
					default:
						t.Errorf("unexpected fixture request path %s", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				t.Cleanup(server.Close)
				client, err := GetAccountAuthHTTPClient("", "")
				require.NoError(t, err)
				transport, ok := client.Transport.(*http.Transport)
				require.True(t, ok)
				assert.Nil(t, transport.Proxy)
				assert.False(t, transport.TLSClientConfig.InsecureSkipVerify)
				assert.GreaterOrEqual(t, transport.TLSClientConfig.MinVersion, uint16(tls.VersionTLS12))
				transport.TLSClientConfig.ServerName = "example.com"
				if trusted {
					transport.TLSClientConfig.RootCAs = testTLSClientConfig(t, server).RootCAs
				}
				transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
				}
				err = path.run(context.Background())
				if trusted {
					require.NoError(t, err)
					assert.Equal(t, path.wantCalls, providerCalls.Load())
				} else {
					require.ErrorIs(t, err, ErrAccountUpstream)
					assert.Zero(t, providerCalls.Load(), "no authorization material before verified TLS")
					assert.NotContains(t, err.Error(), "synthetic")
				}
				assert.Zero(t, ambientCalls.Load())
			})
		}
	}
}

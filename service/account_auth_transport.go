package service

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

var accountAuthTransports = proxyHTTPClientCache{clients: make(map[string]*http.Client), aliases: make(map[string]string)}

// accountAuthRequestTimeout bounds one provider authorization call. It stays
// above the observed 8-12s proxy latency so a spent one-time code is not
// discarded by a premature local timeout, while remaining well under the
// provider's own grant lifetime.
const accountAuthRequestTimeout = 25 * time.Second

// GetAccountAuthHTTPClient returns the pooled transport for an already resolved
// account exit. Empty proxy means direct, never the process environment. Only
// auto and provider-scoped HTTP/1.1 are supported; relay/global TLS bypasses,
// redirect policies, cookies and customized http.DefaultTransport are not used.
// Callers must copy the client before changing its timeout or other fields and
// must not mutate its shared transport. Credential POSTs must clear GetBody so
// the transport cannot replay a consumed authorization code or refresh token.
func GetAccountAuthHTTPClient(proxy, protocol string) (*http.Client, error) {
	policy := defaultHTTPTransportPolicy()
	switch protocol {
	case "", dto.HTTPProtocolAuto:
	case dto.HTTPProtocolHTTP1:
		policy.Protocol = dto.HTTPProtocolHTTP1
	default:
		return nil, ErrAccountEgress
	}
	var parsed *url.URL
	var config *proxyURLConfig
	if proxy != "" {
		var err error
		parsed, err = common.ParseProxyURLStrict(proxy)
		if err != nil || parsed == nil {
			return nil, ErrAccountEgress
		}
		config = newProxyURLConfig(parsed)
	}
	return accountAuthTransports.getOrCreate(proxy, config, policy, func() (*http.Client, error) {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		// Construct independently: even a cloned default transport can carry an
		// ambient proxy, insecure TLS, alternate dialer, or TLSNextProto hook.
		transport := &http.Transport{
			DialContext:            dialer.DialContext,
			TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:    10 * time.Second,
			ResponseHeaderTimeout:  accountAuthRequestTimeout,
			ExpectContinueTimeout:  time.Second,
			MaxResponseHeaderBytes: 64 << 10,
			MaxIdleConns:           100,
			MaxIdleConnsPerHost:    10,
			IdleConnTimeout:        90 * time.Second,
		}
		if parsed != nil {
			if err := configureProxyTransport(transport, parsed); err != nil {
				return nil, ErrAccountEgress
			}
		}
		applyHTTPTransportPolicy(transport, policy)
		return &http.Client{
			Transport: transport,
			Timeout:   20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}, nil
	})
}

// accountAuthJSON is the strict counterpart to the legacy shared authorization
// helper. Keep this opt-in: unrelated providers retain their existing policy.
func accountAuthJSON(ctx context.Context, proxy string, request *http.Request, out any) error {
	if ctx == nil || request == nil {
		return ErrAccountProtocol
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	upstream, err := GetAccountAuthHTTPClient(proxy, "")
	if err != nil {
		return ErrAccountEgress
	}
	client := *upstream
	// Domestic account providers reached through a rotating proxy routinely need
	// 8-12s for the code exchange. An 8s cap failed the exchange after the
	// one-time code had already been spent, so the retry could never succeed.
	client.Timeout = accountAuthRequestTimeout
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	requestCtx, cancel := context.WithTimeout(ctx, client.Timeout)
	defer cancel()
	request = request.Clone(requestCtx)
	// No replay of code exchange or rotated refresh credentials, including the
	// transport's automatic HTTP/2 retry after a refused stream or GOAWAY.
	request.GetBody = nil
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrAccountUpstream
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrAccountUpstream
	}
	if len(raw) > 1<<20 {
		return ErrAccountProtocol
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrAccountUpstream
	}
	if common.Unmarshal(raw, out) != nil {
		return ErrAccountProtocol
	}
	return nil
}

// accountAuthNoReplayTransport protects login requests constructed by callers
// outside this transport file without changing their request body or headers.
type accountAuthNoReplayTransport struct{ http.RoundTripper }

func (t accountAuthNoReplayTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.GetBody = nil
	return t.RoundTripper.RoundTrip(request)
}

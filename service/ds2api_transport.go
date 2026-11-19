package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

// DS2TransportFailure never retains or exposes raw transport errors, which can
// contain proxy credentials. Only fixed, actionable classifications leave here.
type DS2TransportFailure struct{ Kind string }

func (e *DS2TransportFailure) Error() string {
	switch e.Kind {
	case "dns":
		return "DeepSeek hostname lookup failed on the selected exit. Check this proxy or DNS and try again"
	case "timeout":
		return "DeepSeek login timed out on the selected exit. Test this node before retrying"
	case "tls":
		return "DeepSeek TLS connection failed on the selected exit. Check the proxy TLS path"
	case "cancelled":
		return "DeepSeek login request was cancelled"
	case "proxy":
		return "The selected account proxy could not connect. Test or change your own proxy first"
	default:
		return "DeepSeek could not be reached on the selected exit. Test this node before retrying"
	}
}
func ds2TransportFailure(err error) *DS2TransportFailure {
	kind := "connection"
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var timeout net.Error
	var operation *net.OpError
	var remote *url.Error
	switch {
	case errors.Is(err, context.Canceled):
		kind = "cancelled"
	case errors.As(err, &dns):
		kind = "dns"
	case errors.As(err, &cert) || errors.As(err, &unknown):
		kind = "tls"
	case errors.Is(err, context.DeadlineExceeded):
		kind = "timeout"
	case errors.As(err, &timeout) && timeout.Timeout():
		kind = "timeout"
	case errors.As(err, &operation) && operation.Op == "proxyconnect":
		kind = "proxy"
	case errors.As(err, &remote) && remote.Op == "proxyconnect":
		kind = "proxy"
	}
	return &DS2TransportFailure{Kind: kind}
}

func ds2LoginClient(proxy string) (*http.Client, error) {
	client, err := GetAccountAuthHTTPClient(proxy, dto.HTTPProtocolHTTP1)
	if err != nil {
		return nil, &DS2TransportFailure{Kind: "proxy"}
	}
	bounded := *client
	bounded.Transport = accountAuthNoReplayTransport{RoundTripper: client.Transport}
	bounded.Timeout = 20 * time.Second
	bounded.Jar = nil
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &bounded, nil
}

// DiagnoseDS2Connectivity is a single credential-free GET on the same selected
// exit. Reachability is not account login success or proof of available models.
func DiagnoseDS2Connectivity(ctx context.Context, proxy string) (int, string) {
	client, err := ds2LoginClient(proxy)
	if err != nil {
		return 0, "proxy"
	}
	client.Timeout = 8 * time.Second
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chat.deepseek.com/", nil)
	if err != nil {
		return 0, "connection"
	}
	request.Header.Set("Accept", "text/html")
	response, err := client.Do(request)
	if err != nil {
		return 0, ds2TransportFailure(err).Kind
	}
	defer response.Body.Close()
	_, _ = io.CopyN(io.Discard, response.Body, 4096)
	if response.StatusCode == http.StatusForbidden {
		return response.StatusCode, "provider_restricted"
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return response.StatusCode, "rate_limited"
	}
	if response.StatusCode >= 500 {
		return response.StatusCode, "provider_unavailable"
	}
	if response.StatusCode >= 200 && response.StatusCode < 400 {
		return response.StatusCode, "reachable"
	}
	return response.StatusCode, strings.ToLower(http.StatusText(response.StatusCode))
}

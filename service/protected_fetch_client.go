package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

type ssrfResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

func protectedProxyTransport(proxyURL *url.URL, policy HTTPTransportPolicy) (*http.Transport, error) {
	if proxyURL == nil {
		return nil, nil
	}
	transport := newRelayHTTPTransport()
	transport.Proxy = nil
	if err := configureProxyTransport(transport, proxyURL); err != nil {
		return nil, err
	}
	applyHTTPTransportPolicy(transport, policy)
	return transport, nil
}

func newTenantProtectedHTTPClient(proxyURL *url.URL, policy HTTPTransportPolicy) (*http.Client, error) {
	proxyTransport, err := protectedProxyTransport(proxyURL, policy)
	if err != nil {
		return nil, err
	}
	client := newProtectedFetchHTTPClientWithProxy(nil, nil, nil, func(*http.Request) (*url.URL, error) { return proxyURL, nil })
	rt := client.Transport.(*ssrfProtectedRoundTripper)
	// For SOCKS, the dialer's network peer is the proxy but its protocol-level
	// destination is the target. Validate both, including DNS resolved targets.
	if proxyTransport != nil {
		if proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
			proxyDial := proxyTransport.DialContext
			proxyTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if err := ValidateSSRFProtectedFetchURL((&url.URL{Scheme: "http", Host: address, Path: "/"}).String()); err != nil {
					return nil, err
				}
				return proxyDial(ctx, network, address)
			}
		}
		rt.transports[proxyURL.String()] = proxyTransport
	}
	client.CheckRedirect = checkProtectedFetchRedirect
	return client, nil
}

type protectedFetchDialer struct {
	resolver      ssrfResolver
	dialContext   func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection func() (*common.SSRFProtection, bool, error)
}

type ssrfProtectedRoundTripper struct {
	resolver      ssrfResolver
	dialContext   func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection func() (*common.SSRFProtection, bool, error)
	proxy         func(*http.Request) (*url.URL, error)

	mutex      sync.Mutex
	transports map[string]*http.Transport
}

func currentFetchProtection() (*common.SSRFProtection, bool, error) {
	fetchSetting := system_setting.GetFetchSetting()
	if !fetchSetting.EnableSSRFProtection {
		return nil, false, nil
	}

	protection, err := common.NewSSRFProtectionFromFetchSetting(
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		fetchSetting.AllowedPorts,
		fetchSetting.ApplyIPFilterForDomain,
	)
	if err != nil {
		return nil, true, err
	}
	return protection, true, nil
}

func newProtectedFetchHTTPClient() *http.Client {
	return newProtectedFetchHTTPClientWithDialer(nil, nil, nil)
}

func newProtectedFetchHTTPClientWithDialer(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error)) *http.Client {
	return newProtectedFetchHTTPClientWithProxy(resolver, dialContext, getProtection, http.ProxyFromEnvironment)
}

func newProtectedFetchHTTPClientWithProxy(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error), proxy func(*http.Request) (*url.URL, error)) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialContext == nil {
		netDialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		dialContext = netDialer.DialContext
	}
	if getProtection == nil {
		getProtection = currentFetchProtection
	}
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}

	client := &http.Client{
		Transport: &ssrfProtectedRoundTripper{
			resolver:      resolver,
			dialContext:   dialContext,
			getProtection: getProtection,
			proxy:         proxy,
			transports:    make(map[string]*http.Transport),
		},
		CheckRedirect: checkProtectedFetchRedirect,
	}
	if common.RelayTimeout != 0 {
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
	}
	return client
}

func (t *ssrfProtectedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("invalid request")
	}
	if err := ValidateSSRFProtectedFetchURL(req.URL.String()); err != nil {
		return nil, err
	}

	proxyURL, err := t.proxy(req)
	if err != nil {
		return nil, err
	}
	transport, err := t.transportFor(proxyURL)
	if err != nil {
		return nil, err
	}
	return transport.RoundTrip(req)
}

func (t *ssrfProtectedRoundTripper) CloseIdleConnections() {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	for _, transport := range t.transports {
		transport.CloseIdleConnections()
	}
}

func (t *ssrfProtectedRoundTripper) transportFor(proxyURL *url.URL) (*http.Transport, error) {
	// 只按代理地址分组：代理来自环境变量，取值有限，map 有界；
	// 目标 origin 是用户可控输入，不能作为缓存 key。
	key := "direct"
	if proxyURL != nil {
		key = proxyURL.String()
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if transport, ok := t.transports[key]; ok {
		return transport, nil
	}

	// A proxy is the actual TCP peer for both plain HTTP and CONNECT. Validate
	// its destination too; target validation in RoundTrip is not sufficient.
	if proxyURL != nil {
		protection, enabled, err := t.getProtection()
		if err != nil {
			return nil, err
		}
		if enabled {
			port, err := strconv.Atoi(proxyURL.Port())
			if proxyURL.Port() == "" {
				if proxyURL.Scheme == "https" {
					port = 443
				} else {
					port = 80
				}
			}
			if err != nil || protection.ValidateNetworkTarget(proxyURL.Hostname(), port) != nil {
				return nil, fmt.Errorf("proxy destination is not permitted")
			}
		}
	}
	transport, err := t.newTransport(proxyURL)
	if err != nil {
		return nil, err
	}
	t.transports[key] = transport
	return transport, nil
}

func (t *ssrfProtectedRoundTripper) newTransport(proxyURL *url.URL) (*http.Transport, error) {
	dialContext := t.dialContext
	proxyFunc := http.ProxyURL(proxyURL)
	if proxyURL == nil {
		protectedDialer := &protectedFetchDialer{
			resolver:      t.resolver,
			dialContext:   t.dialContext,
			getProtection: t.getProtection,
		}
		dialContext = protectedDialer.DialContext
		proxyFunc = nil
	} else {
		protectedDialer := &protectedFetchDialer{
			resolver:      t.resolver,
			dialContext:   t.dialContext,
			getProtection: t.getProtection,
		}
		dialContext = protectedDialer.DialContext
	}

	transport := &http.Transport{
		MaxIdleConns:        common.RelayMaxIdleConns,
		MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
		IdleConnTimeout:     time.Duration(common.RelayIdleConnTimeout) * time.Second,
		ForceAttemptHTTP2:   true,
		Proxy:               proxyFunc,
		DialContext:         dialContext,
	}
	if common.TLSInsecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig.Clone()
	}
	return transport, nil
}

func (d *protectedFetchDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	protection, enabled, err := d.getProtection()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return d.dialContext(ctx, network, addr)
	}

	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address %s: %w", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %s", portText)
	}
	if err := protection.ValidateNetworkTarget(host, port); err != nil {
		return nil, err
	}

	if ip := net.ParseIP(host); ip != nil {
		return d.dialContext(ctx, network, net.JoinHostPort(ip.String(), portText))
	}
	if !protection.ApplyIPFilterForDomain {
		return d.dialContext(ctx, network, addr)
	}

	resolved, err := d.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed for %s: %v", host, err)
	}

	var candidateIPs []net.IP
	for _, ipAddr := range resolved {
		ip := ipAddr.IP
		if ip == nil || !networkAllowsIP(network, ip) {
			continue
		}
		if err := protection.ValidateResolvedIP(host, ip); err != nil {
			return nil, err
		}
		candidateIPs = append(candidateIPs, ip)
	}

	var lastDialErr error
	for _, ip := range candidateIPs {
		conn, err := d.dialContext(ctx, network, net.JoinHostPort(ip.String(), portText))
		if err == nil {
			return conn, nil
		}
		lastDialErr = err
	}

	if lastDialErr != nil {
		return nil, lastDialErr
	}
	return nil, fmt.Errorf("DNS resolution for %s returned no usable IP addresses", host)
}

func networkAllowsIP(network string, ip net.IP) bool {
	switch network {
	case "tcp4":
		return ip.To4() != nil
	case "tcp6":
		return ip.To4() == nil
	default:
		return true
	}
}

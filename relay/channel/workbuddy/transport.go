// transport.go 出站 Transport 构造（连接层加固，移植自
// _research/workbuddy2api-panel-main internal/upstream/transport.go）：
// 真正禁 h2 / TLS 握手超时 / 短 keepalive 探测。
package workbuddy

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

const (
	dialTimeout           = 10 * time.Second
	dialKeepAlive         = 15 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	idleConnTimeout       = 30 * time.Second
	responseHeaderTimeout = 60 * time.Second

	maxIdleConns        = 100
	maxIdleConnsPerHost = 20
)

// newTransport 构造共享出站 Transport。
// 空 TLSNextProto（非 nil）真正禁 h2：nil 会让标准库注入默认 h2 映射，
// 半死 h2 流复用表现为 "http2: timeout awaiting response headers"。
func newTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: dialKeepAlive}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       idleConnTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		Proxy:                 http.ProxyFromEnvironment,
	}
}

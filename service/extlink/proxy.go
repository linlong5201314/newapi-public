// proxy.go M4 /app/<slug> 同域反代（蓝图 docs/M4-外链模块设计.md §2）。
//
// 改写矩阵：
//   - 请求：Host=目标 host；剥 Accept-Encoding（交给 Transport 自动 gzip 解压，
//     保证 ModifyResponse 看到的 text/html 是明文）；目标 base path 与站内路径单斜杠拼接。
//   - 响应：剥 X-Frame-Options 与 CSP frame-ancestors；Location 重写回 /app/<slug> 前缀；
//     Set-Cookie 剥 Domain、Path 重写为 /app/<slug>/；text/html 注入
//     <base href="/app/<slug>/">（gzip 先解）。
//   - SSRF：自定义 DialContext 对每个实际拨号 IP 过 ValidatePublicIP（第二道闸，
//     防 DNS 重绑定——解析与拨号同一批已校验 IP）。
package extlink

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
)

const (
	// maxHTMLBodyBytes HTML 注入读取上限（32MB，防异常超大页面打爆内存）。
	maxHTMLBodyBytes = 32 << 20
)

// safeDialContext 解析目标 host，逐个 IP 过 SSRF 校验后直接拨已校验 IP
// （而不是让 net 内部再解析一次，消除解析-拨号间的重绑定窗口）。
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if ip := net.ParseIP(host); ip != nil {
		if err := ValidatePublicIP(ip); err != nil {
			return nil, fmt.Errorf("extlink proxy dial blocked: %w", err)
		}
		return dialer.DialContext(ctx, network, addr)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ipa := range ips {
		if err := ValidatePublicIP(ipa.IP); err != nil {
			lastErr = err
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipa.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no resolvable addresses for %s", host)
	}
	return nil, fmt.Errorf("extlink proxy dial blocked: %w", lastErr)
}

// sharedTransport 全反代共享（连接复用关键——不能每请求新建 Transport）。
var sharedTransport = &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           safeDialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          256,
	MaxIdleConnsPerHost:   32,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 60 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

// NewReverseProxy 为单条外链构建反向代理。target 已确认 http(s)。
func NewReverseProxy(link *model.ExtLink) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(link.Url)
	if err != nil {
		return nil, err
	}
	slug := model.NormalizeExtLinkSlug(link.Slug)
	proxy := &httputil.ReverseProxy{
		Transport: sharedTransport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Host 改写为目标 host（SetURL 已同步 Host；显式兜底端口形态）。
			pr.Out.Host = target.Host
			// 剥 Accept-Encoding：Transport 会自动补 gzip 并透明解压，
			// ModifyResponse 因此总是看到明文 body；br/zstd 不透传。
			pr.Out.Header.Del("Accept-Encoding")
		},
		ModifyResponse: func(resp *http.Response) error {
			stripFramingHeaders(resp)
			rewriteLocationHeader(resp, target, slug)
			rewriteSetCookieHeaders(resp, slug)
			if isHTMLResponse(resp) {
				return injectBaseTag(resp, slug)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"success":false,"message":"extlink upstream error: ` +
				strings.ReplaceAll(err.Error(), `"`, `'`) + `"}`))
		},
	}
	return proxy, nil
}

// stripFramingHeaders 剥 X-Frame-Options 与 CSP 的 frame-ancestors 指令。
func stripFramingHeaders(resp *http.Response) {
	resp.Header.Del("X-Frame-Options")
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		return
	}
	directives := strings.Split(csp, ";")
	kept := make([]string, 0, len(directives))
	for _, d := range directives {
		trimmed := strings.TrimSpace(d)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "frame-ancestors") {
			continue
		}
		kept = append(kept, trimmed)
	}
	if len(kept) == 0 {
		resp.Header.Del("Content-Security-Policy")
		return
	}
	resp.Header.Set("Content-Security-Policy", strings.Join(kept, "; "))
}

// rewriteLocationHeader 把指回目标站自身的重定向改写回 /app/<slug> 前缀，
// 地址栏不跳走。绝对 URL（host 匹配）与 path-absolute（/ 开头）两类都处理。
func rewriteLocationHeader(resp *http.Response, target *url.URL, slug string) {
	locations := resp.Header.Values("Location")
	if len(locations) == 0 {
		return
	}
	resp.Header.Del("Location")
	for _, loc := range locations {
		resp.Header.Add("Location", rewriteLocation(loc, target, slug))
	}
}

func rewriteLocation(loc string, target *url.URL, slug string) string {
	trimmed := strings.TrimSpace(loc)
	if trimmed == "" {
		return loc
	}
	// scheme-relative：//host/path
	if strings.HasPrefix(trimmed, "//") {
		if u, err := url.Parse("http:" + trimmed); err == nil && strings.EqualFold(u.Hostname(), target.Hostname()) {
			return prefixPath(slug, stripBasePath2(u, target))
		}
		return loc
	}
	if u, err := url.Parse(trimmed); err == nil && u.IsAbs() {
		// 绝对 URL：仅当指回目标站自身（scheme+host 一致）时改写，外链跳转保持原样。
		if strings.EqualFold(u.Hostname(), target.Hostname()) &&
			(u.Scheme == target.Scheme || u.Scheme == "http" || u.Scheme == "https") {
			return prefixPath(slug, stripBasePath2(u, target))
		}
		return loc
	}
	if strings.HasPrefix(trimmed, "/") {
		// path-absolute：剥目标 base path 后挂回 /app/<slug>。
		if u, err := url.Parse(trimmed); err == nil {
			return prefixPath(slug, stripBasePath2(u, target))
		}
	}
	// 相对路径与异常形态：交给浏览器 + <base> 解析。
	return loc
}

// stripBasePath2 从重定向目标中剥掉 ExtLink.Url 自带的 base path 前缀，
// 返回拼好 query 的完整路径，供挂回 /app/<slug> 下。
func stripBasePath2(u *url.URL, target *url.URL) string {
	path, query := stripBasePath(u, target)
	if query != "" {
		return path + "?" + query
	}
	return path
}

// stripBasePath 从重定向目标中剥掉 ExtLink.Url 自带的 base path 前缀，
// 返回 (path, rawquery)。
func stripBasePath(u *url.URL, target *url.URL) (string, string) {
	path := u.Path
	base := strings.TrimSuffix(target.Path, "/")
	if base != "" && base != "/" {
		if path == base {
			path = "/"
		} else if strings.HasPrefix(path, base+"/") {
			path = strings.TrimPrefix(path, base)
		}
	}
	return path, u.RawQuery
}

// prefixPath 把完整路径（可含 query）挂到 /app/<slug> 前缀下（单斜杠拼接）。
func prefixPath(slug, fullPath string) string {
	p := "/app/" + slug
	path, query, _ := strings.Cut(fullPath, "?")
	if path == "" || path == "/" {
		p += "/"
	} else if strings.HasPrefix(path, "/") {
		p += path
	} else {
		p += "/" + path
	}
	if query != "" {
		p += "?" + query
	}
	return p
}

// rewriteSetCookieHeaders 剥 Domain 属性、Path 重写为 /app/<slug>/，
// 让目标站登录态 cookie 只在我们域名的反代路径下生效。
func rewriteSetCookieHeaders(resp *http.Response, slug string) {
	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) == 0 {
		return
	}
	resp.Header.Del("Set-Cookie")
	for _, cookie := range cookies {
		resp.Header.Add("Set-Cookie", rewriteSetCookie(cookie, slug))
	}
}

func rewriteSetCookie(cookie, slug string) string {
	parts := strings.Split(cookie, ";")
	out := make([]string, 0, len(parts)+1)
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		if i > 0 {
			eq := strings.Index(trimmed, "=")
			name := trimmed
			if eq >= 0 {
				name = trimmed[:eq]
			}
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "domain":
				continue // 剥 Domain——cookie 归我们域名
			case "path":
				continue // 原 Path 丢弃，统一重写
			}
		}
		out = append(out, trimmed)
	}
	out = append(out, "Path=/app/"+slug+"/")
	return strings.Join(out, "; ")
}

// isHTMLResponse 仅 text/html 进 body 改写。
func isHTMLResponse(resp *http.Response) bool {
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	return strings.Contains(ct, "text/html")
}

// injectBaseTag 在 <head> 后注入 <base href="/app/<slug>/">，
// 让目标站相对资源（css/js/img/链接）都解析回反代路径下。gzip body 先解压，
// 改写后以明文回传（Content-Length 同步修正、Content-Encoding 剥除）。
func injectBaseTag(resp *http.Response, slug string) error {
	if resp.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLBodyBytes+1))
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if len(raw) > maxHTMLBodyBytes {
		return fmt.Errorf("html body exceeds %d bytes", maxHTMLBodyBytes)
	}

	body := raw
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Encoding")), "gzip") {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		plain, err := io.ReadAll(io.LimitReader(zr, maxHTMLBodyBytes+1))
		_ = zr.Close()
		if err != nil {
			return err
		}
		body = plain
	}
	resp.Header.Del("Content-Encoding")

	baseTag := []byte(`<base href="/app/` + slug + `/">`)
	body = insertBaseTag(body, baseTag)

	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	resp.Header.Del("ETag") // body 已变，强校验缓存令牌作废
	return nil
}

// insertBaseTag 优先插在 <head ...> 之后；无 <head> 退 <html>；再退直接前置。
// 幂等：已存在 <base 标签则不重复注入。
func insertBaseTag(body, baseTag []byte) []byte {
	lower := bytes.ToLower(body)
	if bytes.Contains(lower, []byte("<base")) {
		return body
	}
	if idx := bytes.Index(lower, []byte("<head")); idx >= 0 {
		if closeIdx := bytes.IndexByte(body[idx:], '>'); closeIdx >= 0 {
			pos := idx + closeIdx + 1
			out := make([]byte, 0, len(body)+len(baseTag))
			out = append(out, body[:pos]...)
			out = append(out, baseTag...)
			out = append(out, body[pos:]...)
			return out
		}
	}
	if idx := bytes.Index(lower, []byte("<html")); idx >= 0 {
		if closeIdx := bytes.IndexByte(body[idx:], '>'); closeIdx >= 0 {
			pos := idx + closeIdx + 1
			out := make([]byte, 0, len(body)+len(baseTag))
			out = append(out, body[:pos]...)
			out = append(out, baseTag...)
			out = append(out, body[pos:]...)
			return out
		}
	}
	out := make([]byte, 0, len(body)+len(baseTag))
	out = append(out, baseTag...)
	out = append(out, body...)
	return out
}

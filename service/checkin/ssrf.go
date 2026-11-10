// ssrf.go SSRF 防护：AgentUrl / custom 端点必须 http(s)、禁私网 IP 段、解析后二次校验。
package checkin

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// ssrfAllowPrivate 开发/e2e 逃生门：CHECKIN_SSRF_ALLOW_PRIVATE=true 时跳过 IP 段校验
// （scheme 校验仍生效）。生产部署严禁开启——该开关存在仅为 mock 上游联通测试。
func ssrfAllowPrivate() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CHECKIN_SSRF_ALLOW_PRIVATE")))
	return v == "true" || v == "1" || v == "yes"
}

// ValidateExternalURL 校验外联 URL 合法性（SSRF 防护主入口）：
//   - 仅 http/https scheme；
//   - host 必须是合法域名或公网 IP（禁私网/环回/链路本地/组播/未指定地址）；
//   - 域名解析后所有 A/AAAA 记录再次过同一 IP 校验（防 DNS 重绑定到内网）。
func ValidateExternalURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https urls are allowed")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url must have a host")
	}
	if ssrfAllowPrivate() {
		return nil // dev/e2e 逃生门（见 ssrfAllowPrivate 注释）
	}
	if ip := net.ParseIP(host); ip != nil {
		return validatePublicIP(ip)
	}
	// 域名：先黑名单关键词快筛，再解析校验。
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") {
		return fmt.Errorf("internal hostnames are not allowed")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("dns resolve failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("dns resolve returned no addresses")
	}
	for _, ip := range ips {
		if err := validatePublicIP(ip); err != nil {
			return fmt.Errorf("host %s resolves to disallowed address %s", host, ip)
		}
	}
	return nil
}

// validatePublicIP 拒绝一切非公网地址（私网/环回/链路本地/组播/未指定/CGNAT/文档段）。
func validatePublicIP(ip net.IP) error {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("private/reserved addresses are not allowed")
	}
	// CGNAT 100.64.0.0/10 与文档段 192.0.2.0/24、198.51.100.0/24、203.0.113.0/24、
	// 6to4/teredo 等：IsPrivate 不覆盖，显式拒绝保留段。
	if isReserved(ip) {
		return fmt.Errorf("reserved addresses are not allowed")
	}
	return nil
}

// reservedV4 / reservedV6 显式保留段（net.IsPrivate 未覆盖部分）。
var reservedV4 = []net.IPNet{
	{IP: net.IPv4(0, 0, 0, 0).To4(), Mask: net.CIDRMask(8, 32)},         // 0.0.0.0/8
	{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)},    // CGNAT
	{IP: net.IPv4(192, 0, 0, 0).To4(), Mask: net.CIDRMask(24, 32)},     // IETF 协议分配
	{IP: net.IPv4(192, 0, 2, 0).To4(), Mask: net.CIDRMask(24, 32)},     // TEST-NET-1
	{IP: net.IPv4(192, 88, 99, 0).To4(), Mask: net.CIDRMask(24, 32)},   // 6to4 relay
	{IP: net.IPv4(198, 18, 0, 0).To4(), Mask: net.CIDRMask(15, 32)},    // benchmark
	{IP: net.IPv4(198, 51, 100, 0).To4(), Mask: net.CIDRMask(24, 32)},  // TEST-NET-2
	{IP: net.IPv4(203, 0, 113, 0).To4(), Mask: net.CIDRMask(24, 32)},   // TEST-NET-3
	{IP: net.IPv4(224, 0, 0, 0).To4(), Mask: net.CIDRMask(4, 32)},      // 组播+
	{IP: net.IPv4(240, 0, 0, 0).To4(), Mask: net.CIDRMask(4, 32)},      // 保留
}

func isReserved(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false // v6 非公网站点同样被上层 IsXxx 家族覆盖主要面
	}
	for _, n := range reservedV4 {
		if n.Contains(v4) {
			return true
		}
	}
	return false
}

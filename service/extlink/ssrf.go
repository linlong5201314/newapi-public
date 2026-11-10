// ssrf.go M4 外链模块 SSRF 防护：
//   - 仅 http/https scheme；
//   - host 命中 ExtLinkAllowHosts 白名单（空=不限，启动 WARN 已在 model/option.go 落地）；
//   - 禁私网/环回/链路本地/组播/未指定/保留段 IP；
//   - 域名解析后所有 A/AAAA 记录再次过同一 IP 校验（防 DNS 重绑定到内网）。
//
// 校验在「保存时（create/update）」与「反代拨号时（DialContext 逐 IP）」双重落地。
package extlink

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// AllowPrivate 开发/e2e 逃生门：EXTLINK_SSRF_ALLOW_PRIVATE=true 时跳过 IP 段校验
// （scheme 与白名单校验仍生效）。生产部署严禁开启——该开关存在仅为本地 mock 目标站联调。
func AllowPrivate() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("EXTLINK_SSRF_ALLOW_PRIVATE")))
	return v == "true" || v == "1" || v == "yes"
}

// ValidateExtLinkURL 校验外链目标 URL 合法性（保存时主入口）。
func ValidateExtLinkURL(raw string) error {
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
	if !operation_setting.IsExtLinkHostAllowed(host) {
		return fmt.Errorf("host %s is not in ExtLinkAllowHosts whitelist", host)
	}
	if AllowPrivate() {
		return nil // dev/e2e 逃生门（见 AllowPrivate 注释）
	}
	if ip := net.ParseIP(host); ip != nil {
		return ValidatePublicIP(ip)
	}
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
		if err := ValidatePublicIP(ip); err != nil {
			return fmt.Errorf("host %s resolves to disallowed address %s", host, ip)
		}
	}
	return nil
}

// ValidatePublicIP 拒绝一切非公网地址（私网/环回/链路本地/组播/未指定/CGNAT/文档段）。
// 反代 DialContext 对每个实际拨号 IP 调用本函数（第二道闸）。
func ValidatePublicIP(ip net.IP) error {
	if AllowPrivate() {
		return nil
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("private/reserved addresses are not allowed")
	}
	if isReserved(ip) {
		return fmt.Errorf("reserved addresses are not allowed")
	}
	return nil
}

// reservedV4 显式保留段（net.IsPrivate 未覆盖部分）。
var reservedV4 = []net.IPNet{
	{IP: net.IPv4(0, 0, 0, 0).To4(), Mask: net.CIDRMask(8, 32)},        // 0.0.0.0/8
	{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)},   // CGNAT
	{IP: net.IPv4(192, 0, 0, 0).To4(), Mask: net.CIDRMask(24, 32)},    // IETF 协议分配
	{IP: net.IPv4(192, 0, 2, 0).To4(), Mask: net.CIDRMask(24, 32)},    // TEST-NET-1
	{IP: net.IPv4(192, 88, 99, 0).To4(), Mask: net.CIDRMask(24, 32)},  // 6to4 relay
	{IP: net.IPv4(198, 18, 0, 0).To4(), Mask: net.CIDRMask(15, 32)},   // benchmark
	{IP: net.IPv4(198, 51, 100, 0).To4(), Mask: net.CIDRMask(24, 32)}, // TEST-NET-2
	{IP: net.IPv4(203, 0, 113, 0).To4(), Mask: net.CIDRMask(24, 32)},  // TEST-NET-3
	{IP: net.IPv4(224, 0, 0, 0).To4(), Mask: net.CIDRMask(4, 32)},     // 组播+
	{IP: net.IPv4(240, 0, 0, 0).To4(), Mask: net.CIDRMask(4, 32)},     // 保留
}

func isReserved(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false // v6 非公网站点由 IsXxx 家族覆盖主要面
	}
	for _, n := range reservedV4 {
		if n.Contains(v4) {
			return true
		}
	}
	return false
}

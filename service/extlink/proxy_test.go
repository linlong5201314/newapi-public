package extlink

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func TestRewriteSetCookie(t *testing.T) {
	in := "session=abc; Domain=example.com; Path=/deep; HttpOnly; SameSite=Lax"
	got := rewriteSetCookie(in, "app1")
	want := "session=abc; HttpOnly; SameSite=Lax; Path=/app/app1/"
	if got != want {
		t.Fatalf("rewriteSetCookie = %q, want %q", got, want)
	}
	// 无属性时不追加空段
	if got := rewriteSetCookie("k=v", "s"); got != "k=v; Path=/app/s/" {
		t.Fatalf("bare cookie rewrite = %q", got)
	}
}

func TestRewriteLocation(t *testing.T) {
	target, _ := url.Parse("https://up.example.com/base")
	cases := []struct{ in, want string }{
		{"https://up.example.com/base/page?x=1", "/app/s/page?x=1"},
		{"https://up.example.com/other", "/app/s/other"},
		{"//up.example.com/base/a", "/app/s/a"},
		{"/base/sub", "/app/s/sub"},
		{"/sub?q=2", "/app/s/sub?q=2"},
		{"relative/path", "relative/path"},
		{"https://elsewhere.com/x", "https://elsewhere.com/x"},
	}
	for _, c := range cases {
		if got := rewriteLocation(c.in, target, "s"); got != c.want {
			t.Errorf("rewriteLocation(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestInsertBaseTag(t *testing.T) {
	base := []byte(`<base href="/app/s/">`)
	got := string(insertBaseTag([]byte("<html><head><title>x</title></head></html>"), base))
	if !strings.Contains(got, `<head><base href="/app/s/">`) {
		t.Fatalf("head injection failed: %s", got)
	}
	got = string(insertBaseTag([]byte("<html><body>hi</body></html>"), base))
	if !strings.Contains(got, `<html><base href="/app/s/">`) {
		t.Fatalf("html fallback injection failed: %s", got)
	}
	got = string(insertBaseTag([]byte("<p>plain</p>"), base))
	if !strings.HasPrefix(got, `<base href="/app/s/">`) {
		t.Fatalf("prepend fallback failed: %s", got)
	}
	// 幂等
	if got := string(insertBaseTag([]byte(`<head><base href="/x/"></head>`), base)); strings.Count(got, "<base") != 1 {
		t.Fatalf("existing base duplicated: %s", got)
	}
}

func TestStripFramingHeaders(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("X-Frame-Options", "DENY")
	resp.Header.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; img-src *")
	stripFramingHeaders(resp)
	if resp.Header.Get("X-Frame-Options") != "" {
		t.Fatal("X-Frame-Options not stripped")
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if strings.Contains(csp, "frame-ancestors") || !strings.Contains(csp, "default-src") || !strings.Contains(csp, "img-src") {
		t.Fatalf("CSP rewrite wrong: %q", csp)
	}
	// 只有 frame-ancestors 时整条 CSP 移除
	resp.Header.Set("Content-Security-Policy", "frame-ancestors 'none'")
	stripFramingHeaders(resp)
	if resp.Header.Get("Content-Security-Policy") != "" {
		t.Fatal("empty CSP should be removed")
	}
}

func TestAllowHostWhitelist(t *testing.T) {
	old := operation_setting.ExtLinkAllowHosts
	defer func() { operation_setting.ExtLinkAllowHosts = old }()

	operation_setting.ExtLinkAllowHosts = ""
	if !operation_setting.IsExtLinkHostAllowed("anything.example") {
		t.Fatal("empty whitelist must allow all hosts")
	}
	operation_setting.ExtLinkAllowHosts = "example.com, .foo.dev"
	if !operation_setting.IsExtLinkHostAllowed("example.com") {
		t.Fatal("exact host should match")
	}
	if operation_setting.IsExtLinkHostAllowed("sub.example.com") {
		t.Fatal("exact entry must not match subdomains")
	}
	if !operation_setting.IsExtLinkHostAllowed("a.b.foo.dev") || !operation_setting.IsExtLinkHostAllowed("foo.dev") {
		t.Fatal("dot-prefixed entry should match domain and subdomains")
	}
	if operation_setting.IsExtLinkHostAllowed("evil.dev") {
		t.Fatal("unlisted host must not match")
	}
}

func TestValidateExtLinkURLSchemeAndHost(t *testing.T) {
	old := operation_setting.ExtLinkAllowHosts
	defer func() { operation_setting.ExtLinkAllowHosts = old }()
	operation_setting.ExtLinkAllowHosts = ""

	for _, bad := range []string{"ftp://example.com", "file:///etc/passwd", "example.com", ""} {
		if err := ValidateExtLinkURL(bad); err == nil {
			t.Errorf("ValidateExtLinkURL(%q) should fail", bad)
		}
	}
	// 白名单不命中
	operation_setting.ExtLinkAllowHosts = "example.com"
	if err := ValidateExtLinkURL("https://other.org"); err == nil {
		t.Fatal("host outside whitelist should fail")
	}
}

func TestValidatePublicIP(t *testing.T) {
	if AllowPrivate() {
		t.Skip("EXTLINK_SSRF_ALLOW_PRIVATE is set; IP-deny assertions do not apply")
	}
	for _, bad := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.1", "169.254.1.1", "100.64.0.1", "0.0.0.0", "224.0.0.1"} {
		ip := net.ParseIP(bad)
		if ip == nil {
			t.Fatalf("bad test ip %s", bad)
		}
		if err := ValidatePublicIP(ip); err == nil {
			t.Errorf("ValidatePublicIP(%s) should fail", bad)
		}
	}
	if err := ValidatePublicIP(net.ParseIP("93.184.216.34")); err != nil {
		t.Errorf("public IP should pass: %v", err)
	}
}

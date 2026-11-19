package service

import (
	"strings"
	"testing"
)

func TestMiMoDestinationMetadataRedactsSecrets(t *testing.T) {
	for _, target := range []string{
		"https://private-user:private-pass@ak.account.xiaomi.com:8443/private-path?ticket=private-query#private-fragment",
		"https://ak.account.xiaomi.com/another-private-path?passToken=private-token",
		"custom:private-opaque",
		"https://%invalid-private-parse",
	} {
		metadata := miMoDestinationMetadata(target)
		if strings.Contains(metadata, "private") || strings.Contains(metadata, "ticket") || strings.Contains(metadata, "passToken") {
			t.Fatalf("diagnostics leaked URL contents: %s", metadata)
		}
	}
	got := miMoDestinationMetadata("https://ak.account.xiaomi.com/path#secret")
	want := "scheme=https host=ak.account.xiaomi.com port=false userinfo=false fragment=true opaque=false"
	if got != want {
		t.Fatalf("unexpected safe metadata: %s", got)
	}
}

func TestMiMoObservedAuthorizationHosts(t *testing.T) {
	loginHosts := []string{"account.xiaomi.com", "ak.account.xiaomi.com"}
	pollHosts := []string{"account.xiaomi.com", "ak.lp.account.xiaomi.com"}
	for _, tc := range []struct {
		name      string
		hosts     []string
		valid     string
		wrongRole string
	}{
		{"login", loginHosts, "https://ak.account.xiaomi.com/pass/lp?ticket=fixture", "https://ak.lp.account.xiaomi.com/listen"},
		{"poll", pollHosts, "https://ak.lp.account.xiaomi.com/longPolling/listen?ticket=fixture", "https://ak.account.xiaomi.com/listen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{tc.valid, "https://account.xiaomi.com/legacy"} {
				if _, err := miMoURL(target, tc.hosts...); err != nil {
					t.Fatalf("official host rejected: %v", err)
				}
			}
			for _, target := range []string{tc.wrongRole, "https://attacker.invalid/leak", "https://ak.account.xiaomi.com.attacker.invalid/leak", "https://evil.account.xiaomi.com/leak", "https://ak.lp.account.xiaomi.com.attacker.invalid/leak", "http://account.xiaomi.com/plain", "https://account.xiaomi.com:8443/port", "https://fixture@account.xiaomi.com/userinfo", "https://account.xiaomi.com/path#fragment"} {
				if _, err := miMoURL(target, tc.hosts...); err == nil {
					t.Fatalf("unapproved destination accepted: %s", target)
				}
			}
		})
	}
}

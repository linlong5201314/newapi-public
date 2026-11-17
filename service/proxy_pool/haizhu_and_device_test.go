package proxy_pool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/model"
)

func TestParseHaizhuFullYaml(t *testing.T) {
	yamlPath := filepath.Join(HaizhuDefaultExtractDir, "nodes", "clash", "full.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Skipf("haizhu extract dir not present on this machine: %v", err)
	}
	nodes, format, err := ParseSubscription(raw, "auto")
	if err != nil {
		t.Fatalf("ParseSubscription failed: %v", err)
	}
	if format != "clash" {
		t.Fatalf("format = %q, want clash", format)
	}
	if len(nodes) < 7000 {
		t.Fatalf("parsed %d nodes from haizhu full.yaml, want >= 7000", len(nodes))
	}
	t.Logf("Successfully parsed %d nodes from Haizhu full.yaml (sample: %s [%s/%s])",
		len(nodes), nodes[0].Name, nodes[0].Kind, nodes[0].Region)
}

func TestDeepSeekBearerExtractionAndDeviceVirtualization(t *testing.T) {
	rawHeaders := `
POST https://chat.deepseek.com/api/v0/telemetry HTTP/1.1
authorization: Bearer SlCmRF5j+IX2LBorDe4nBY5OHI1F8bmSp2HEkCsAX1pPWJ38B7GOPyYhcqGzoCMD
cookie: HWWAFSESID=349689a3ea18fb48585; ds_session_id=c70d161b2aa44cb78970e5e466a3c748
`
	creds, err := model.ParseAccountOrTokenLines(rawHeaders)
	if err != nil {
		t.Fatalf("ParseAccountOrTokenLines failed: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("len(creds) = %d, want 1", len(creds))
	}
	if creds[0].Password != "SlCmRF5j+IX2LBorDe4nBY5OHI1F8bmSp2HEkCsAX1pPWJ38B7GOPyYhcqGzoCMD" {
		t.Fatalf("extracted token = %q", creds[0].Password)
	}
	if creds[0].Device == nil || creds[0].Device.DeviceId == "" {
		t.Fatalf("device profile was not generated: %+v", creds[0].Device)
	}

	// 验证不同账号生成的设备指纹完全独立
	devA := model.BuildDeepSeekDeviceProfile("acc1@example.com|tok1")
	devB := model.BuildDeepSeekDeviceProfile("acc2@example.com|tok2")
	if devA.DeviceId == devB.DeviceId || devA.DsSessionId == devB.DsSessionId || devA.HwWafSesId == devB.HwWafSesId {
		t.Fatalf("device profiles collided between two accounts: %+v vs %+v", devA, devB)
	}
	t.Logf("Account 1 device_id=%s cookie=%s", devA.DeviceId, devA.Cookie)
	t.Logf("Account 2 device_id=%s cookie=%s", devB.DeviceId, devB.Cookie)
}

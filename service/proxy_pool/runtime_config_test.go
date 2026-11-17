package proxy_pool

import (
	"strings"
	"testing"
)

func TestBuildRuntimeConfigAssignsPerUserListener(t *testing.T) {
	clashJSON, err := encodeClashJSON(map[string]any{
		"type": "anytls", "server": "hk01.example.com", "port": 443,
		"password": "secret", "sni": "hk01.example.com", "name": "ignored",
	})
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	plan, err := BuildRuntimeConfig([]RuntimeUser{
		{UID: 7, Nodes: []RuntimeNode{
			{Name: "香港 01", Kind: "anytls", RawURI: clashJSON},
			{Name: "直连 HTTP", Kind: "http", URL: "http://user:pw@10.0.0.1:8080"},
		}},
		{UID: 3, Nodes: []RuntimeNode{
			{Name: "新加坡 02", Kind: "ss", RawURI: clashJSON},
		}},
		{UID: 9}, // 没节点：不应产生监听口
	}, RuntimeOptions{BasePort: 20000, Controller: "127.0.0.1:19090"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if plan.Proxies != 3 {
		t.Fatalf("proxies = %d, want 3", plan.Proxies)
	}
	// 端口按 uid 升序分配：3 → 20000，7 → 20001。
	if plan.Ports[3] != 20000 || plan.Ports[7] != 20001 {
		t.Fatalf("ports = %v, want {3:20000, 7:20001}", plan.Ports)
	}
	if _, exists := plan.Ports[9]; exists {
		t.Fatalf("user 9 has no nodes but got a listener: %v", plan.Ports)
	}

	yamlText := string(plan.YAML)
	for _, want := range []string{
		"u3·新加坡 02", "u7·香港 01", "u7·直连 HTTP",
		"type: anytls", "type: http", "port: 20000", "port: 20001",
		"external-controller: 127.0.0.1:19090", "MATCH,DIRECT",
	} {
		if !strings.Contains(yamlText, want) {
			t.Fatalf("config missing %q\n%s", want, yamlText)
		}
	}
	if strings.Contains(yamlText, "u9") {
		t.Fatalf("user 9 leaked into config:\n%s", yamlText)
	}
	if plan.Groups[7] != "u7" {
		t.Fatalf("group of user 7 = %q, want u7", plan.Groups[7])
	}
	if len(plan.NodeNames[7]) != 2 {
		t.Fatalf("node names of user 7 = %v, want 2", plan.NodeNames[7])
	}
}

func TestBuildRuntimeConfigEmptyUsers(t *testing.T) {
	plan, err := BuildRuntimeConfig(nil, RuntimeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if plan.Proxies != 0 || len(plan.Ports) != 0 {
		t.Fatalf("empty plan should have no proxies/ports, got %d/%v", plan.Proxies, plan.Ports)
	}
	if !strings.Contains(string(plan.YAML), "rules:") {
		t.Fatalf("empty config should still be valid yaml:\n%s", plan.YAML)
	}
}

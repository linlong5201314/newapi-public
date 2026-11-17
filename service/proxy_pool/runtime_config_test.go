package proxy_pool

import (
	"strings"
	"testing"
)

func TestBuildRuntimeConfigSharedPortAndVisibility(t *testing.T) {
	clashJSON, err := encodeClashJSON(map[string]any{
		"type": "anytls", "server": "hk01.example.com", "port": 443,
		"password": "secret", "sni": "hk01.example.com", "name": "ignored",
	})
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	plan, err := BuildRuntimeConfig([]RuntimeUser{
		{UID: 7, Nodes: []RuntimeNode{
			{ID: 101, Name: "香港 01", Kind: "anytls", RawURI: clashJSON, Public: false},
			{ID: 102, Name: "直连 HTTP", Kind: "http", URL: "http://user:pw@10.0.0.1:8080", Public: true},
		}},
		{UID: 3, Nodes: []RuntimeNode{
			{ID: 103, Name: "新加坡 02", Kind: "ss", RawURI: clashJSON, Public: true},
		}},
		{UID: 9}, // 没私有节点，但能看到公开节点
	}, RuntimeOptions{BasePort: 23456, Controller: "127.0.0.1:23459"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if plan.Proxies != 3 {
		t.Fatalf("proxies = %d, want 3", plan.Proxies)
	}
	// 所有有可见节点的用户共用单一 mixed-port 23456
	if plan.Ports[3] != 23456 || plan.Ports[7] != 23456 {
		t.Fatalf("ports = %v, want shared 23456", plan.Ports)
	}
	// 用户 7 有 1 个私有 + 2 个公开 = 3 个可见节点；用户 3 只有 2 个公开节点（看不到用户 7 的私有节点）
	if len(plan.NodeNames[7]) != 3 {
		t.Fatalf("user 7 visible nodes = %v, want 3", plan.NodeNames[7])
	}
	if len(plan.NodeNames[3]) != 2 {
		t.Fatalf("user 3 visible nodes = %v, want 2 (private node of user 7 must be hidden)", plan.NodeNames[3])
	}

	yamlText := string(plan.YAML)
	for _, want := range []string{
		"u3·新加坡 02", "u7·香港 01", "u7·直连 HTTP",
		"type: anytls", "type: http", "mixed-port: 23456",
		"external-controller: 127.0.0.1:23459", "MATCH,PROXY",
	} {
		if !strings.Contains(yamlText, want) {
			t.Fatalf("config missing %q\n%s", want, yamlText)
		}
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

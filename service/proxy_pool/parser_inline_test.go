package proxy_pool

import "testing"

// 真实机场订阅（Super Flash）里 33/50 个节点是 anytls，旧解析器直接丢弃。
// 这里用等价的最小样片锁住行为：anytls/hysteria 必须被认，信息节点必须被跳过。
const inlineMapSubscription = `port: 7890
socks-port: 7891
proxies:
  - {name: "剩余流量：421.14 GB", type: ss, server: info.example.com, port: 1, cipher: aes-128-gcm, password: x}
  - {name: "极速 专线 香港 01", type: anytls, server: hk01.example.com, port: 443, password: secret, sni: hk01.example.com}
  - {name: "极速 专线 日本 02", type: vless, server: jp02.example.com, port: 443, uuid: 11111111-2222-3333-4444-555555555555, tls: true}
  - {name: "中转 新加坡 03", type: ss, server: sg03.example.com, port: 8388, cipher: aes-256-gcm, password: pass}
proxy-groups:
  - {name: PROXY, type: select, proxies: ["极速 专线 香港 01"]}
rules:
  - MATCH,PROXY
`

func TestParseSubscriptionInlineMapsWithAnyTLS(t *testing.T) {
	nodes, format, err := ParseSubscription([]byte(inlineMapSubscription), "auto")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if format != "clash" {
		t.Fatalf("format = %q, want clash", format)
	}
	kinds := map[string]int{}
	for _, n := range nodes {
		kinds[n.Kind]++
		if n.Name == "" {
			t.Fatalf("node without name: %+v", n)
		}
		if n.RawUri == "" && n.Url == "" {
			t.Fatalf("node %q has neither raw uri nor dialable url", n.Name)
		}
	}
	if kinds["anytls"] != 1 {
		t.Fatalf("anytls nodes = %d, want 1 (kinds=%v)", kinds["anytls"], kinds)
	}
	if kinds["vless"] != 1 || kinds["ss"] != 1 {
		t.Fatalf("unexpected kind mix: %v (info node should be filtered)", kinds)
	}
	if len(nodes) != 3 {
		t.Fatalf("nodes = %d, want 3 (info node dropped)", len(nodes))
	}
}

func TestParseSubscriptionInlineMapRegions(t *testing.T) {
	nodes, _, err := ParseSubscription([]byte(inlineMapSubscription), "clash")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]int{}
	for _, n := range nodes {
		if n.Region != "" {
			got[n.Region]++
		}
	}
	if got["HK"] != 1 || got["JP"] != 1 || got["SG"] != 1 {
		t.Fatalf("regions = %v, want HK/JP/SG one each", got)
	}
}

func TestInfoNodesAreDropped(t *testing.T) {
	for _, name := range []string{
		"剩余流量：421.11 GB",
		"距离下次重置剩余：24 天",
		"套餐到期：2026-12-11",
		"x-superflash.com -官网",
		"su.unxn.cn 备用",
		"进不去官网？先关闭开关",
		"旧线路无法再使用",
	} {
		if !isInfoNode(name) {
			t.Fatalf("info node %q not filtered", name)
		}
	}
	for _, name := range []string{
		"极速 专线 香港 01",
		"备用专线 香港",
		"Ai专用节点-日本",
	} {
		if isInfoNode(name) {
			t.Fatalf("real node %q wrongly filtered", name)
		}
	}
}

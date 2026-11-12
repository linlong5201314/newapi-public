// Package proxy_pool 解析机场订阅与节点 URI，并把客户端型节点拼成 mihomo 配置。
// 本包只做纯解析与文本生成，不访问数据库、不发网络请求。
package proxy_pool

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"gopkg.in/yaml.v3"
)

const (
	// maxParsedNodes 单次订阅解析的节点上限，防止超大订阅打爆内存与数据库。
	maxParsedNodes = 500
	// clashJSONPrefix 标记 RawUri 里存放的是 base64(JSON) 形式的 clash 节点定义。
	clashJSONPrefix = "clash-json:"
	// defaultMixedPort 导出配置未指定端口时使用的 mihomo mixed-port。
	defaultMixedPort = 7890
	// mihomoProxyGroupName 导出配置里唯一的 select 策略组名称。
	mihomoProxyGroupName = "PROXY"
)

// supportedSchemes 订阅行解析时认可的 URI scheme。
var supportedSchemes = []string{
	"vmess", "vless", "trojan", "ss",
	"socks5", "socks5h", "http", "https",
	"hysteria2", "tuic",
}

// directProxySchemes 这些 scheme 的 URI 可以直接交给 HTTP 客户端当代理使用。
var directProxySchemes = []string{"http", "https", "socks5", "socks5h"}

// ParsedNode 解析出的单个节点。
type ParsedNode struct {
	Name   string
	Kind   string // http|https|socks5|socks5h|vless|vmess|trojan|hysteria2|ss
	Url    string // 可直接使用时有值
	RawUri string // 客户端型节点原始 URI
	Region string // 从名字里猜地区（香港/新加坡/美国/日本/英国/HK/SG/US/JP/UK）
}

// ParseSubscription 解析订阅内容。
// format 取 "" | "auto" | "clash" | "base64" | "uri-list"；空值与 auto 表示自动探测。
// 返回解析出的节点、实际使用的格式，以及整体性错误。单条节点损坏只会被跳过。
func ParseSubscription(body []byte, format string) ([]ParsedNode, string, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, "", fmt.Errorf("subscription body is empty")
	}

	normalized := strings.ToLower(strings.TrimSpace(format))
	detected := normalized
	if normalized == "" || normalized == "auto" {
		detected = detectFormat(text)
	}

	switch detected {
	case "clash":
		nodes, err := parseClash(text)
		return nodes, "clash", err
	case "base64":
		decoded, err := decodeBase64Loose(text)
		if err != nil {
			return nil, "base64", fmt.Errorf("decode base64 subscription: %w", err)
		}
		return parseUriList(string(decoded)), "base64", nil
	case "uri-list":
		return parseUriList(text), "uri-list", nil
	default:
		return nil, detected, fmt.Errorf("unsupported subscription format %q", format)
	}
}

// detectFormat 按内容猜订阅格式：clash 配置 → base64 包裹的行列表 → 纯行列表。
func detectFormat(text string) string {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "proxies:") || strings.HasPrefix(lower, "port:") {
		return "clash"
	}
	if decoded, err := decodeBase64Loose(text); err == nil && strings.Contains(string(decoded), "://") {
		return "base64"
	}
	return "uri-list"
}

// parseClash 解析 clash / mihomo 配置里的 proxies 段。
func parseClash(text string) ([]ParsedNode, error) {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("parse clash yaml: %w", err)
	}

	nodes := make([]ParsedNode, 0, min(len(doc.Proxies), maxParsedNodes))
	for _, proxy := range doc.Proxies {
		if len(nodes) >= maxParsedNodes {
			break
		}
		node, ok := clashProxyToNode(proxy)
		if !ok {
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// clashProxyToNode 把单条 clash proxy 转成 ParsedNode。字段不全或类型不支持时返回 false。
func clashProxyToNode(proxy map[string]any) (ParsedNode, bool) {
	proxyType := strings.ToLower(strings.TrimSpace(yamlString(proxy["type"])))
	switch proxyType {
	case "http", "https", "socks5", "socks5h", "ss", "vmess", "vless", "trojan", "hysteria2", "tuic":
	default:
		return ParsedNode{}, false
	}

	server := strings.TrimSpace(yamlString(proxy["server"]))
	port := strings.TrimSpace(yamlString(proxy["port"]))
	if server == "" || port == "" {
		return ParsedNode{}, false
	}

	name := strings.TrimSpace(yamlString(proxy["name"]))
	if name == "" {
		name = proxyType
	}

	node := ParsedNode{Name: name, Region: guessRegion(name)}
	if !slices.Contains(directProxySchemes, proxyType) {
		rawUri, err := encodeClashJSON(proxy)
		if err != nil {
			return ParsedNode{}, false
		}
		node.Kind = proxyType
		node.RawUri = rawUri
		return node, true
	}

	kind := proxyType
	if yamlBool(proxy["tls"]) {
		switch kind {
		case "http":
			kind = "https"
		case "socks5":
			kind = "socks5h"
		}
	}
	node.Kind = kind
	node.Url = buildProxyUrl(kind, server, port, proxy)
	return node, true
}

// buildProxyUrl 拼出可直接使用的代理 URL，保留 clash 里的账号密码。
func buildProxyUrl(scheme, server, port string, proxy map[string]any) string {
	host := net.JoinHostPort(server, port)
	user := yamlString(proxy["username"])
	if user == "" {
		return scheme + "://" + host
	}
	password := yamlString(proxy["password"])
	if password == "" {
		return scheme + "://" + url.User(user).String() + "@" + host
	}
	return scheme + "://" + url.UserPassword(user, password).String() + "@" + host
}

// encodeClashJSON 把 clash 节点定义编码成 clash-json:<base64(JSON)>，供导出配置时还原。
func encodeClashJSON(proxy map[string]any) (string, error) {
	data, err := common.Marshal(proxy)
	if err != nil {
		return "", err
	}
	return clashJSONPrefix + base64.StdEncoding.EncodeToString(data), nil
}

// decodeClashJSON 还原 clash-json:<base64(JSON)> 形式的节点定义。
func decodeClashJSON(rawUri string) (map[string]any, error) {
	encoded, found := strings.CutPrefix(rawUri, clashJSONPrefix)
	if !found {
		return nil, fmt.Errorf("not a clash-json node")
	}
	data, err := decodeBase64Loose(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode clash-json node: %w", err)
	}
	var proxy map[string]any
	if err := common.Unmarshal(data, &proxy); err != nil {
		return nil, fmt.Errorf("unmarshal clash-json node: %w", err)
	}
	if len(proxy) == 0 {
		return nil, fmt.Errorf("clash-json node is empty")
	}
	return proxy, nil
}

// parseUriList 按行解析 URI 列表，单行坏掉只跳过该行。
func parseUriList(text string) []ParsedNode {
	nodes := make([]ParsedNode, 0, 16)
	for line := range strings.SplitSeq(text, "\n") {
		if len(nodes) >= maxParsedNodes {
			break
		}
		node, ok := parseUriLine(line, len(nodes))
		if !ok {
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// parseUriLine 解析单行 URI；index 用于无名字节点生成 kind-index 形式的名称。
func parseUriLine(line string, index int) (ParsedNode, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
		return ParsedNode{}, false
	}

	scheme, rest, found := strings.Cut(trimmed, "://")
	if !found {
		return ParsedNode{}, false
	}
	kind := strings.ToLower(scheme)
	if !slices.Contains(supportedSchemes, kind) {
		return ParsedNode{}, false
	}

	name := uriName(rest, kind)
	if name == "" {
		name = fmt.Sprintf("%s-%d", kind, index+1)
	}

	node := ParsedNode{Name: name, Kind: kind, Region: guessRegion(name)}
	if slices.Contains(directProxySchemes, kind) {
		node.Url = trimmed
	} else {
		node.RawUri = trimmed
	}
	return node, true
}

// uriName 取节点显示名：优先 #fragment，其次 vmess 载荷里的 ps 字段。
func uriName(rest, kind string) string {
	body, fragment, found := strings.Cut(rest, "#")
	if found {
		if decoded, err := url.QueryUnescape(fragment); err == nil {
			if name := strings.TrimSpace(decoded); name != "" {
				return name
			}
		}
		if name := strings.TrimSpace(fragment); name != "" {
			return name
		}
	}
	if kind == "vmess" {
		return vmessName(body)
	}
	return ""
}

// vmessName 从 vmess 的 base64 JSON 载荷里取 ps 显示名。
func vmessName(body string) string {
	decoded, err := decodeBase64Loose(strings.TrimSpace(body))
	if err != nil {
		return ""
	}
	var payload struct {
		Ps string `json:"ps"`
	}
	if err := common.Unmarshal(decoded, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Ps)
}

// decodeBase64Loose 兼容标准与 URL-safe 字母表，允许换行与缺失的 padding。
func decodeBase64Loose(text string) ([]byte, error) {
	compact := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, text)
	compact = strings.TrimRight(compact, "=")
	if compact == "" {
		return nil, fmt.Errorf("empty base64 payload")
	}
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(compact); err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("invalid base64 payload")
}

// regionRule 地区猜测规则：命中任一关键词即判定为该地区。
type regionRule struct {
	code     string
	keywords []string
}

// regionRules 顺序即优先级（英国先于美国，避免 "United Kingdom" 被判成 US）。
var regionRules = []regionRule{
	{code: "HK", keywords: []string{"香港", "hk", "hong"}},
	{code: "SG", keywords: []string{"新加坡", "sg", "singapore", "狮城"}},
	{code: "UK", keywords: []string{"英国", "uk", "united kingdom"}},
	{code: "US", keywords: []string{"美国", "us", "united"}},
	{code: "JP", keywords: []string{"日本", "jp", "japan"}},
	{code: "TW", keywords: []string{"台湾", "tw"}},
	{code: "KR", keywords: []string{"韩国", "kr"}},
}

// guessRegion 从节点名字里猜地区码，猜不到返回空串。
func guessRegion(name string) string {
	lower := strings.ToLower(name)
	for _, rule := range regionRules {
		for _, keyword := range rule.keywords {
			if containsRegionKeyword(lower, keyword) {
				return rule.code
			}
		}
	}
	return ""
}

// containsRegionKeyword 对短 ASCII 关键词（us/sg/hk 等）做词边界匹配，
// 避免 "Russia" 命中 us、"Ukraine" 命中 uk；中文与长单词直接子串匹配。
func containsRegionKeyword(text, keyword string) bool {
	if keyword == "" {
		return false
	}
	if !isASCIIWord(keyword) || len(keyword) > 3 {
		return strings.Contains(text, keyword)
	}
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], keyword)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(keyword)
		leftOK := start == 0 || !isASCIIAlnum(text[start-1])
		rightOK := end == len(text) || !isASCIIAlnum(text[end])
		if leftOK && rightOK {
			return true
		}
		offset = start + 1
	}
	return false
}

func isASCIIWord(text string) bool {
	for i := range len(text) {
		if text[i] >= 0x80 {
			return false
		}
	}
	return true
}

func isASCIIAlnum(b byte) bool {
	switch {
	case b >= '0' && b <= '9':
		return true
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	}
	return false
}

// yamlString 把 yaml 解出的任意标量转成字符串（clash 里 port 常写成数字）。
func yamlString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return fmt.Sprint(typed)
	}
}

// yamlBool 把 yaml 解出的值当布尔读。
func yamlBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

// BuildMihomoConfig 把客户端型节点（RawUri 非空）拼成一份 mihomo / clash 配置。
// 只有 clash-json 形式的节点能原样还原成 mihomo 的 proxies 条目；
// 其它 URI（vless:// 等）mihomo 不认，会被写成注释并跳过。
func BuildMihomoConfig(nodes []model.ProxyEndpoint, mixedPortBase int) (string, error) {
	mixedPort := mixedPortBase
	if mixedPort <= 0 || mixedPort > 65535 {
		mixedPort = defaultMixedPort
	}

	proxies := make([]map[string]any, 0, len(nodes))
	proxyNames := make([]string, 0, len(nodes))
	unsupported := make([]string, 0)
	usedNames := make(map[string]struct{}, len(nodes))

	for _, node := range nodes {
		rawUri := strings.TrimSpace(node.RawUri)
		if rawUri == "" {
			continue
		}
		proxy, err := decodeClashJSON(rawUri)
		if err != nil {
			unsupported = append(unsupported, rawUri)
			continue
		}

		name := strings.TrimSpace(yamlString(proxy["name"]))
		if name == "" {
			name = strings.TrimSpace(node.Name)
		}
		if name == "" {
			name = fmt.Sprintf("%s-%d", node.Kind, node.Id)
		}
		name = uniqueProxyName(name, usedNames)
		proxy["name"] = name

		proxies = append(proxies, proxy)
		proxyNames = append(proxyNames, name)
	}

	groupMembers := make([]string, 0, len(proxyNames)+1)
	groupMembers = append(groupMembers, proxyNames...)
	groupMembers = append(groupMembers, "DIRECT")

	document := struct {
		MixedPort   int              `yaml:"mixed-port"`
		AllowLan    bool             `yaml:"allow-lan"`
		Mode        string           `yaml:"mode"`
		LogLevel    string           `yaml:"log-level"`
		Proxies     []map[string]any `yaml:"proxies"`
		ProxyGroups []map[string]any `yaml:"proxy-groups"`
		Rules       []string         `yaml:"rules"`
	}{
		MixedPort: mixedPort,
		AllowLan:  false,
		Mode:      "rule",
		LogLevel:  "info",
		Proxies:   proxies,
		ProxyGroups: []map[string]any{{
			"name":    mihomoProxyGroupName,
			"type":    "select",
			"proxies": groupMembers,
		}},
		// 出口指向 select 组：客户端连 mixed-port 后才会真正用上面的节点拨号。
		Rules: []string{"MATCH," + mihomoProxyGroupName},
	}

	body, err := yaml.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("marshal mihomo config: %w", err)
	}

	var builder strings.Builder
	builder.WriteString("# Generated by New API proxy pool export.\n")
	builder.WriteString("# 只包含 clash-json 形式的客户端节点；其余 URI 见下方 unsupported 注释。\n")
	for _, rawUri := range unsupported {
		builder.WriteString("# unsupported-uri: ")
		builder.WriteString(sanitizeComment(rawUri))
		builder.WriteString("\n")
	}
	builder.Write(body)
	return builder.String(), nil
}

// uniqueProxyName 保证 mihomo 配置里的节点名唯一（重名会让 mihomo 启动失败）。
func uniqueProxyName(name string, used map[string]struct{}) string {
	candidate := name
	for suffix := 2; ; suffix++ {
		if _, exists := used[candidate]; !exists {
			used[candidate] = struct{}{}
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", name, suffix)
	}
}

// sanitizeComment 把 URI 压成单行并截断，避免破坏 YAML 注释结构。
func sanitizeComment(raw string) string {
	replaced := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		}
		return r
	}, raw)
	if len([]rune(replaced)) > 200 {
		replaced = string([]rune(replaced)[:200]) + "..."
	}
	return replaced
}

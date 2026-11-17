package proxy_pool

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// 本文件负责把「用户 + 启用节点」编译成一份可直接交给 mihomo 运行的配置：
//   - 每个用户一个 select 策略组 u<uid>，组里是该用户的全部节点
//   - 每个用户一个 mixed 监听口（basePort 起递增），listener 的 proxy 指向自己的组
//
// 这样一台 mihomo 就能同时服务多个用户：每个用户拿 http://127.0.0.1:<port> 当出口，
// 切节点只要改自己那个组，互不影响。

// RuntimeNode 一个待注入内核的节点。RawURI（clash-json:）优先，其次 URL（可直连的 http/socks）。
type RuntimeNode struct {
	Name   string
	Kind   string
	URL    string
	RawURI string
}

// RuntimeUser 一个用户的内核视图。
type RuntimeUser struct {
	UID   int
	Nodes []RuntimeNode
}

// RuntimePlan 编译结果：YAML 正文、用户端口表、组名表。
type RuntimePlan struct {
	YAML   []byte
	Ports  map[int]int
	Groups map[int]string
	// NodeNames 用户可选节点名（已带 uid 前缀，和 YAML 里的名字一致）。
	NodeNames map[int][]string
	Proxies   int
}

// RuntimeOptions 内核配置参数。
type RuntimeOptions struct {
	BasePort   int
	Controller string // 127.0.0.1:19090
	Secret     string
	// LogLevel 默认 warning，避免把节点域名刷进日志。
	LogLevel string
}

type runtimeDoc struct {
	MixedPort          int              `yaml:"mixed-port"`
	AllowLan           bool             `yaml:"allow-lan"`
	IPv6               bool             `yaml:"ipv6"`
	Mode               string           `yaml:"mode"`
	LogLevel           string           `yaml:"log-level"`
	ExternalController string           `yaml:"external-controller"`
	Secret             string           `yaml:"secret,omitempty"`
	Proxies            []map[string]any `yaml:"proxies"`
	ProxyGroups        []map[string]any `yaml:"proxy-groups"`
	Listeners          []map[string]any `yaml:"listeners"`
	Rules              []string         `yaml:"rules"`
}

// nodeNamePrefix 给节点名加 uid 前缀，保证跨用户重名也不会让 mihomo 启动失败。
func nodeNamePrefix(uid int) string { return fmt.Sprintf("u%d·", uid) }

// BuildRuntimeConfig 把若干用户编译成 mihomo 配置。没有可用节点的用户不出监听口。
func BuildRuntimeConfig(users []RuntimeUser, opts RuntimeOptions) (*RuntimePlan, error) {
	if opts.BasePort <= 0 {
		opts.BasePort = 23456
	}
	if opts.Controller == "" {
		opts.Controller = "127.0.0.1:23459"
	}
	if opts.LogLevel == "" {
		opts.LogLevel = "warning"
	}

	doc := runtimeDoc{
		AllowLan:           false,
		IPv6:               false,
		Mode:               "rule",
		LogLevel:           opts.LogLevel,
		ExternalController: opts.Controller,
		Secret:             opts.Secret,
		Rules:              []string{"MATCH,DIRECT"},
	}
	plan := &RuntimePlan{
		Ports:     map[int]int{},
		Groups:    map[int]string{},
		NodeNames: map[int][]string{},
	}

	sorted := slices.Clone(users)
	slices.SortFunc(sorted, func(a, b RuntimeUser) int { return a.UID - b.UID })

	port := opts.BasePort
	for _, user := range sorted {
		if user.UID <= 0 {
			continue
		}
		prefix := nodeNamePrefix(user.UID)
		names := make([]string, 0, len(user.Nodes))
		seen := map[string]struct{}{}
		for _, node := range user.Nodes {
			proxy, ok := runtimeProxy(node, prefix)
			if !ok {
				continue
			}
			name, _ := proxy["name"].(string)
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			doc.Proxies = append(doc.Proxies, proxy)
			names = append(names, name)
		}
		if len(names) == 0 {
			continue
		}
		group := fmt.Sprintf("u%d", user.UID)
		doc.ProxyGroups = append(doc.ProxyGroups, map[string]any{
			"name":    group,
			"type":    "select",
			"proxies": names,
		})
		doc.Listeners = append(doc.Listeners, map[string]any{
			"name":  group,
			"type":  "mixed",
			"port":  port,
			"proxy": group,
		})
		plan.Ports[user.UID] = port
		plan.Groups[user.UID] = group
		plan.NodeNames[user.UID] = names
		port++
	}
	plan.Proxies = len(doc.Proxies)

	if len(doc.Proxies) == 0 {
		// 没有节点时不生成监听口，只保留一个能启动的空配置。
		doc.ProxyGroups = []map[string]any{}
		doc.Listeners = []map[string]any{}
	}
	encoded, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal mihomo config: %w", err)
	}
	plan.YAML = encoded
	return plan, nil
}

// runtimeProxy 把节点转成 mihomo proxies 条目。
func runtimeProxy(node RuntimeNode, prefix string) (map[string]any, bool) {
	if strings.TrimSpace(node.RawURI) != "" {
		proxy, err := decodeClashJSON(node.RawURI)
		if err != nil {
			return nil, false
		}
		name := strings.TrimSpace(node.Name)
		if name == "" {
			name = node.Kind
		}
		proxy["name"] = prefix + name
		return proxy, true
	}
	rawURL := strings.TrimSpace(node.URL)
	if rawURL == "" {
		return nil, false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return nil, false
	}
	scheme := strings.ToLower(parsed.Scheme)
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		host = parsed.Host
		switch scheme {
		case "https":
			port = "443"
		default:
			port = "80"
		}
	}
	proxyType := "http"
	if strings.HasPrefix(scheme, "socks") {
		proxyType = "socks5"
	}
	name := strings.TrimSpace(node.Name)
	if name == "" {
		name = scheme + "://" + parsed.Host
	}
	entry := map[string]any{
		"name":   prefix + name,
		"type":   proxyType,
		"server": host,
		"port":   port,
	}
	if proxyType == "http" && scheme == "https" {
		entry["tls"] = true
	}
	if parsed.User != nil {
		entry["username"] = parsed.User.Username()
		if password, ok := parsed.User.Password(); ok {
			entry["password"] = password
		}
	}
	if _, err := strconv.Atoi(port); err != nil {
		return nil, false
	}
	return entry, true
}

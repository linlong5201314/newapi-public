package proxy_pool

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// 本文件负责把「启用节点（公共池公开节点 + 用户私有节点）」编译成一份单共享 mihomo 配置：
//   - 全局唯一 mixed-port（默认 23456），external-controller（默认 127.0.0.1:23459）
//   - 每个节点固定独立 loopback listener，私有节点不进入共享选择组
//   - 公共池节点（Public=true）进入 PUBLIC_FAST (url-test) 与 PROXY 组，所有用户共享可用
//   - 用户未勾选公开的私有节点（Public=false）仅进入所有者视图与节点专属 listener，
//     对其他用户既不可见（数据库与接口层过滤）也不可被其他用户调用或切换。

const (
	SharedGroupName     = "PROXY"
	PublicFastGroupName = "PUBLIC_FAST"
	maxUrlTestNodes     = 120
	maxKernelProxies    = 1500
)

// RuntimeNode 一个待注入内核的节点。RawURI（clash-json:）优先，其次 URL（可直连的 http/socks）。
type RuntimeNode struct {
	ID     int
	Name   string
	Kind   string
	URL    string
	RawURI string
	Public bool
}

// RuntimeUser 一个用户的内核视图。
type RuntimeUser struct {
	UID   int
	Nodes []RuntimeNode
}

// RuntimePlan 编译结果：YAML 正文、用户端口表、组名表与可见节点名。
type RuntimePlan struct {
	YAML       []byte
	SharedPort int
	Ports      map[int]int
	Groups     map[int]string
	// NodeNames 用户可选节点名（自己的私有节点 + 全部公开节点）。
	NodeNames   map[int][]string
	PublicNodes []string
	AllNodes    []string
	// EndpointNodeName 节点 ID -> 内核里的唯一节点名（供单节点测速使用）。
	EndpointNodeName map[int]string
	EndpointPorts    map[int]int
	EndpointURLs     map[int]string
	EndpointKeys     map[int]uint64
	Proxies          int
}

// RuntimeOptions 内核配置参数。
type RuntimeOptions struct {
	BasePort   int
	Controller string // 127.0.0.1:23459
	Secret     string
	LogLevel   string
}

type runtimeDoc struct {
	MixedPort          int              `yaml:"mixed-port"`
	BindAddress        string           `yaml:"bind-address"`
	AllowLan           bool             `yaml:"allow-lan"`
	IPv6               bool             `yaml:"ipv6"`
	Mode               string           `yaml:"mode"`
	LogLevel           string           `yaml:"log-level"`
	ExternalController string           `yaml:"external-controller"`
	Secret             string           `yaml:"secret,omitempty"`
	Proxies            []map[string]any `yaml:"proxies"`
	ProxyGroups        []map[string]any `yaml:"proxy-groups"`
	Rules              []string         `yaml:"rules"`
	Listeners          []map[string]any `yaml:"listeners,omitempty"`
}

// nodeNamePrefix 给节点名加 uid 前缀，保证跨用户重名也不会让 mihomo 启动失败。
func nodeNamePrefix(uid int) string { return fmt.Sprintf("u%d·", uid) }

// BuildRuntimeConfig 编译单实例共享 mihomo 配置（mixed-port 23456 + MATCH,PROXY）。
func BuildRuntimeConfig(users []RuntimeUser, opts RuntimeOptions) (*RuntimePlan, error) {
	if opts.BasePort <= 0 {
		opts.BasePort = 23456
	}
	if opts.Controller == "" {
		opts.Controller = "127.0.0.1:23459"
	}
	if opts.LogLevel == "" {
		opts.LogLevel = "info"
	}

	doc := runtimeDoc{
		MixedPort:          opts.BasePort,
		BindAddress:        "127.0.0.1",
		AllowLan:           false,
		IPv6:               false,
		Mode:               "rule",
		LogLevel:           opts.LogLevel,
		ExternalController: opts.Controller,
		Secret:             opts.Secret,
		Rules:              []string{"MATCH," + SharedGroupName},
	}
	plan := &RuntimePlan{
		SharedPort:       opts.BasePort,
		Ports:            map[int]int{},
		Groups:           map[int]string{},
		NodeNames:        map[int][]string{},
		EndpointNodeName: map[int]string{},
		EndpointPorts:    map[int]int{},
		EndpointURLs:     map[int]string{},
		EndpointKeys:     map[int]uint64{},
	}

	sorted := slices.Clone(users)
	slices.SortFunc(sorted, func(a, b RuntimeUser) int { return a.UID - b.UID })

	seen := map[string]struct{}{}
	privateByUser := map[int][]string{}

	for _, user := range sorted {
		if user.UID <= 0 {
			continue
		}
		prefix := nodeNamePrefix(user.UID)
		for _, node := range user.Nodes {
			if len(doc.Proxies) >= maxKernelProxies {
				break
			}
			proxy, ok := runtimeProxy(node, prefix)
			if !ok {
				continue
			}
			baseName, _ := proxy["name"].(string)
			name := uniqueProxyName(baseName, seen)
			proxy["name"] = name
			doc.Proxies = append(doc.Proxies, proxy)
			plan.AllNodes = append(plan.AllNodes, name)
			if node.ID > 0 {
				port := opts.BasePort + 100 + len(plan.EndpointPorts)
				if port > 65535 {
					return nil, fmt.Errorf("endpoint listener port exceeds 65535")
				}
				key := stableHash(node.URL + "\x00" + node.RawURI)
				mac := hmac.New(sha256.New, []byte(opts.Secret))
				_, _ = fmt.Fprintf(mac, "%d:%d:%d", user.UID, node.ID, key)
				token := fmt.Sprintf("%x", mac.Sum(nil))
				plan.EndpointNodeName[node.ID] = name
				plan.EndpointPorts[node.ID] = port
				plan.EndpointKeys[node.ID] = key
				plan.EndpointURLs[node.ID] = fmt.Sprintf("http://endpoint:%s@127.0.0.1:%d", token, port)
				doc.Listeners = append(doc.Listeners, map[string]any{
					"name": fmt.Sprintf("endpoint-%d", node.ID), "type": "mixed",
					"listen": "127.0.0.1", "port": port, "proxy": name,
					"users": []map[string]string{{"username": "endpoint", "password": token}},
				})
			}
			if node.Public {
				plan.PublicNodes = append(plan.PublicNodes, name)
			} else {
				privateByUser[user.UID] = append(privateByUser[user.UID], name)
			}
		}
	}

	plan.Proxies = len(doc.Proxies)
	if len(doc.Proxies) == 0 {
		doc.ProxyGroups = []map[string]any{}
		doc.Rules = []string{"MATCH,DIRECT"}
		encoded, err := yaml.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("marshal mihomo config: %w", err)
		}
		plan.YAML = encoded
		return plan, nil
	}

	// Shared selectors must never route other users through private nodes.
	fastCandidates := plan.PublicNodes
	if len(fastCandidates) > maxUrlTestNodes {
		fastCandidates = fastCandidates[:maxUrlTestNodes]
	}
	if len(fastCandidates) == 0 {
		fastCandidates = []string{"REJECT"}
	}

	doc.ProxyGroups = append(doc.ProxyGroups, map[string]any{
		"name":      PublicFastGroupName,
		"type":      "url-test",
		"url":       "http://cp.cloudflare.com/generate_204",
		"interval":  180,
		"lazy":      true,
		"tolerance": 80,
		"proxies":   fastCandidates,
	})

	selectMembers := make([]string, 0, len(plan.PublicNodes)+2)
	selectMembers = append(selectMembers, PublicFastGroupName)
	selectMembers = append(selectMembers, plan.PublicNodes...)
	doc.ProxyGroups = append(doc.ProxyGroups, map[string]any{
		"name":    SharedGroupName,
		"type":    "select",
		"proxies": selectMembers,
	})

	// 为每个有节点的用户生成可见节点视图：自己的私有节点 + 公开节点
	for _, user := range sorted {
		if user.UID <= 0 {
			continue
		}
		own := privateByUser[user.UID]
		visible := make([]string, 0, len(own)+len(plan.PublicNodes))
		visible = append(visible, own...)
		visible = append(visible, plan.PublicNodes...)
		if len(visible) == 0 {
			continue
		}
		plan.Ports[user.UID] = opts.BasePort
		plan.Groups[user.UID] = SharedGroupName
		plan.NodeNames[user.UID] = visible
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
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return nil, false
	}
	entry := map[string]any{
		"name":   prefix + name,
		"type":   proxyType,
		"server": host,
		"port":   portNum,
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
	return entry, true
}

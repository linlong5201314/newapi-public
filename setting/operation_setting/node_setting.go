// node_setting.go M5 节点管理/部署/告警的平台配置（Option 键与运行期变量）。
//
// 所有键都由 model/option.go 注册进 OptionMap；密钥类键（CloudflareApiToken）
// 由 controller 加密后落库，且不会被 GET /api/option 返回。
package operation_setting

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// encryptNodeRailwayTokenIfNeeded 与 model 包 node 的 encryptNodeTokenIfNeeded 同语义：
// 已加密（crypto:）或显式 plain: 前缀的值不再处理，否则按明文加密。
// 与 model.CreateNode 同一份实现，避免循环 import，保留副本。
func encryptNodeRailwayTokenIfNeeded(token string) string {
	if common.IsEncryptedSecret(token) || common.IsPlainPrefixedSecret(token) {
		return token
	}
	return common.EncryptSecretBestEffort(token)
}

// DefaultNodeDeployEnvTemplate 一键部署的默认环境变量模板（KEY=VALUE 行）。
// 键存在而值为空 → plan 输出 warnings 要求 envOverrides 补齐；值非空 → 作为默认值。
const DefaultNodeDeployEnvTemplate = `SQL_DSN=
REDIS_CONN_STRING=
SESSION_SECRET=
CRYPTO_SECRET=
OMNIROUTE_BASE_URL=
# Railway RAILPACK/mise 构建坑：python artifact attestation 校验会导致构建失败
MISE_PYTHON_GITHUB_ATTESTATIONS=false
`

var (
	// NodeBalanceAlertThreshold Railway 剩余额度告警阈值（美元）。
	NodeBalanceAlertThreshold = 1.0
	// NodeAlertWebhookUrl 告警 webhook（POST JSON）；空=不发 webhook。
	NodeAlertWebhookUrl = ""
	// NodeAlertEmail 告警邮件收件人；空=不发邮件（需 SMTP 已配置）。
	NodeAlertEmail = ""
	// NodeMonitorEnabled 健康巡检后台任务总开关。
	NodeMonitorEnabled = true
	// NodeAutoFailover 余额/健康告警时是否自动 apply failover plan（默认只生成 plan）。
	NodeAutoFailover = false
	// NodeDeployEnvTemplate 一键部署环境变量模板（KEY=VALUE 行，支持 # 注释）。
	NodeDeployEnvTemplate = DefaultNodeDeployEnvTemplate
	// NodeDeployPruneVariables 是否删除目标项目里模板之外的变量（默认不删，仅展示）。
	NodeDeployPruneVariables = false
	// NodePrimaryDomain 主域名（failover 时切换的 CF CNAME 记录名），如 gate.ailong.love。
	NodePrimaryDomain = ""
	// NodeDeployPollTimeoutSeconds 部署轮询超时（秒）。
	NodeDeployPollTimeoutSeconds = 900

	// CloudflareApiToken CF API Token（落库加密；读取时由调用方解密）。
	CloudflareApiToken = ""
	// CloudflareZoneId zone id；CloudflareZoneName 为 zone 主域（如 ailong.love）。
	CloudflareZoneId   = ""
	CloudflareZoneName = ""
	// CloudflareAccountId KV API 需要的账户 ID。
	CloudflareAccountId = ""
	// CloudflareKvNamespaceId Worker KV 命名空间 ID。
	CloudflareKvNamespaceId = ""
	// CloudflareKvNodesKey Worker 节点表在 KV 中的键名。
	CloudflareKvNodesKey = "nodes"
)

var (
	nodeDenylist  = []string{}
	nodeAllowlist = []string{}
	// nodeRailwayServicesMu 保护 nodeRailwayServices（Option 写入与并发请求并发访问）。
	nodeRailwayServicesMu sync.RWMutex
	// nodeRailwayServices 七台 Railway 服务的服务器端登记表（不落前端代码）。
	// 键 "NodeRailwayServices" 是 JSON 数组：[{name,workspace_id,project_id,environment_id,service_id,service_domain?,domain?,api_token?}, ...]。
	// api_token 入库前由 controller 加密；读取时通过 DecryptedToken 还原。
	nodeRailwayServices = []NodeRailwayServiceEntry{}
)

// NodeRailwayServiceEntry 单个受管 Railway 服务的注册项（不落前端代码）。
// 全部字段都是 Railway 标识符或脱敏后的展示字段；api_token_enc 绝不回给前端。
type NodeRailwayServiceEntry struct {
	Name          string `json:"name"`
	WorkspaceId   string `json:"workspace_id"`
	ProjectId     string `json:"project_id"`
	EnvironmentId string `json:"environment_id"`
	ServiceId     string `json:"service_id"`
	ServiceDomain string `json:"service_domain,omitempty"`
	Domain        string `json:"domain,omitempty"`
	// ApiTokenEnc 平台 token 密文（AES-GCM），由 controller 写入；
	// 留空表示该项没有独立 token，回退到第一个匹配 workspace 的本地 Node.ApiTokenEnc。
	ApiTokenEnc string `json:"api_token,omitempty"`
}

// NodeRailwayServicesList 返回当前注册的 Railway 服务清单（已脱敏副本）。
func NodeRailwayServicesList() []NodeRailwayServiceEntry {
	nodeRailwayServicesMu.RLock()
	defer nodeRailwayServicesMu.RUnlock()
	out := make([]NodeRailwayServiceEntry, len(nodeRailwayServices))
	copy(out, nodeRailwayServices)
	return out
}

// NodeRailwayServicesJSON 序列化当前注册表（用于 Option 默认值）。
func NodeRailwayServicesJSON() string {
	nodeRailwayServicesMu.RLock()
	defer nodeRailwayServicesMu.RUnlock()
	encoded, err := json.Marshal(nodeRailwayServices)
	if err != nil || encoded == nil {
		return "[]"
	}
	return string(encoded)
}

// SetNodeRailwayServicesJSON 校验并覆盖注册表。输入接受 JSON 数组；
// 空白值视为显式清空（节点管理页随后显示「未配置」状态）。
func SetNodeRailwayServicesJSON(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		nodeRailwayServicesMu.Lock()
		nodeRailwayServices = []NodeRailwayServiceEntry{}
		nodeRailwayServicesMu.Unlock()
		return nil
	}
	var entries []NodeRailwayServiceEntry
	if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
		return err
	}
	normalized := make([]NodeRailwayServiceEntry, 0, len(entries))
	for _, entry := range entries {
		entry.Name = strings.TrimSpace(entry.Name)
		entry.WorkspaceId = strings.TrimSpace(entry.WorkspaceId)
		entry.ProjectId = strings.TrimSpace(entry.ProjectId)
		entry.EnvironmentId = strings.TrimSpace(entry.EnvironmentId)
		entry.ServiceId = strings.TrimSpace(entry.ServiceId)
		entry.ServiceDomain = strings.TrimSpace(entry.ServiceDomain)
		entry.Domain = strings.TrimSpace(entry.Domain)
		entry.ApiTokenEnc = strings.TrimSpace(entry.ApiTokenEnc)
		if entry.Name == "" || entry.ServiceId == "" || entry.EnvironmentId == "" {
			return errNodeRailwayServiceInvalid
		}
		// 写入侧统一加密：已有 crypto: / plain: 前缀幂等跳过；否则按明文加密。
		// 与 model.CreateNode 的 encryptNodeTokenIfNeeded 语义对齐。
		if entry.ApiTokenEnc != "" {
			entry.ApiTokenEnc = encryptNodeRailwayTokenIfNeeded(entry.ApiTokenEnc)
		}
		normalized = append(normalized, entry)
	}
	nodeRailwayServicesMu.Lock()
	nodeRailwayServices = normalized
	nodeRailwayServicesMu.Unlock()
	return nil
}

// errNodeRailwayServiceInvalid 单测可读校验错误（避免依赖 errors.New 文本匹配）。
var errNodeRailwayServiceInvalid = &nodeRailwayServiceError{"name, service_id and environment_id are required for each railway service entry"}

type nodeRailwayServiceError struct{ msg string }

func (e *nodeRailwayServiceError) Error() string { return e.msg }

// NodeDenylistList 返回内置 denylist（JSON 数组配置的解析结果）。
func NodeDenylistList() []string { return nodeDenylist }

// NodeAllowlistList 返回 allowlist（JSON 数组配置的解析结果；空=不限制）。
func NodeAllowlistList() []string { return nodeAllowlist }

// NodeDenylistJSON 序列化 denylist 为 Option 值。
func NodeDenylistJSON() string { return marshalStringList(nodeDenylist) }

// NodeAllowlistJSON 序列化 allowlist 为 Option 值。
func NodeAllowlistJSON() string { return marshalStringList(nodeAllowlist) }

func marshalStringList(list []string) string {
	if list == nil {
		list = []string{}
	}
	encoded, _ := json.Marshal(list)
	return string(encoded)
}

// ParseNodeStringList 解析 JSON 数组或逗号/换行分隔的目标列表。
func ParseNodeStringList(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}, nil
	}
	if strings.HasPrefix(raw, "[") {
		var values []string
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return nil, err
		}
		return normalizeNodeList(values), nil
	}
	return normalizeNodeList(strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})), nil
}

func normalizeNodeList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

// SetNodeDenylistJSON 校验并写入 denylist。
func SetNodeDenylistJSON(raw string) error {
	list, err := ParseNodeStringList(raw)
	if err != nil {
		return err
	}
	nodeDenylist = list
	return nil
}

// SetNodeAllowlistJSON 校验并写入 allowlist。
func SetNodeAllowlistJSON(raw string) error {
	list, err := ParseNodeStringList(raw)
	if err != nil {
		return err
	}
	nodeAllowlist = list
	return nil
}

// ApplyNodeOption 处理节点管理相关 Option 的运行期状态更新。
// 返回 true 表示该键由本模块接管。
func ApplyNodeOption(key, value string) bool {
	switch key {
	case "NodeBalanceAlertThreshold":
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && parsed >= 0 {
			NodeBalanceAlertThreshold = parsed
		}
	case "NodeAlertWebhookUrl":
		NodeAlertWebhookUrl = strings.TrimSpace(value)
	case "NodeAlertEmail":
		NodeAlertEmail = strings.TrimSpace(value)
	case "NodeMonitorEnabled":
		NodeMonitorEnabled = value == "true"
	case "NodeAutoFailover":
		NodeAutoFailover = value == "true"
	case "NodeDeployEnvTemplate":
		if strings.TrimSpace(value) == "" {
			NodeDeployEnvTemplate = DefaultNodeDeployEnvTemplate
		} else {
			NodeDeployEnvTemplate = value
		}
	case "NodeDeployPruneVariables":
		NodeDeployPruneVariables = value == "true"
	case "NodePrimaryDomain":
		NodePrimaryDomain = strings.TrimSpace(value)
	case "NodeDeployPollTimeoutSeconds":
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && parsed > 0 {
			NodeDeployPollTimeoutSeconds = parsed
		}
	case "NodeDenylist":
		_ = SetNodeDenylistJSON(value)
	case "NodeAllowlist":
		_ = SetNodeAllowlistJSON(value)
	case "CloudflareApiToken":
		CloudflareApiToken = value
	case "CloudflareZoneId":
		CloudflareZoneId = strings.TrimSpace(value)
	case "CloudflareZoneName":
		CloudflareZoneName = strings.TrimSpace(value)
	case "CloudflareAccountId":
		CloudflareAccountId = strings.TrimSpace(value)
	case "CloudflareKvNamespaceId":
		CloudflareKvNamespaceId = strings.TrimSpace(value)
	case "CloudflareKvNodesKey":
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			CloudflareKvNodesKey = trimmed
		}
	case "NodeRailwayServices":
		_ = SetNodeRailwayServicesJSON(value)
	default:
		return false
	}
	return true
}

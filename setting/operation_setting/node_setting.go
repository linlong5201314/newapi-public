// node_setting.go M5 节点管理/部署/告警的平台配置（Option 键与运行期变量）。
//
// 所有键都由 model/option.go 注册进 OptionMap；密钥类键（CloudflareApiToken）
// 由 controller 加密后落库，且不会被 GET /api/option 返回。
package operation_setting

import (
	"encoding/json"
	"strconv"
	"strings"
)

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
)

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
	default:
		return false
	}
	return true
}

// Package nodedeploy M5 部署编排：dry-run 差异计划（10 分钟有效 planId）+ root 二次确认执行。
//
// 影响面保护（蓝图 §3.3）：
//   - 所有部署/迁移先 BuildPlan 输出差异清单，Apply 必须 confirm=true 且 plan 未过期；
//   - Node.ProjectId/ServiceId/Domain 进 allowlist，denylist 命中硬拒绝；
//   - 执行全程写 NodeAudit；变量批量 upsert 强制 skipDeploys，最后统一 deploy 一次。
package nodedeploy

import (
	"bufio"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/cloudflare"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// EnvVar 模板中的一条环境变量。
type EnvVar struct {
	Key   string
	Value string
}

// Options 部署编排的运行期配置快照（来自 OptionMap，含 CF token 明文）。
type Options struct {
	EnvTemplate      string
	PruneVariables   bool
	Denylist         []string
	Allowlist        []string
	PrimaryDomain    string
	DeployTimeout    time.Duration
	AutoFailover     bool
	BalanceThreshold float64
	AlertWebhookUrl  string
	AlertEmail       string
	Cloudflare       cloudflare.Config
}

// LoadOptions 从 operation_setting / OptionMap 构造配置快照。
func LoadOptions() Options {
	cfToken := common.DecryptSecretOrLegacy(operation_setting.CloudflareApiToken)
	timeout := time.Duration(operation_setting.NodeDeployPollTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	primary := strings.TrimSpace(operation_setting.NodePrimaryDomain)
	return Options{
		EnvTemplate:      operation_setting.NodeDeployEnvTemplate,
		PruneVariables:   operation_setting.NodeDeployPruneVariables,
		Denylist:         append([]string{}, operation_setting.NodeDenylistList()...),
		Allowlist:        append([]string{}, operation_setting.NodeAllowlistList()...),
		PrimaryDomain:    primary,
		DeployTimeout:    timeout,
		AutoFailover:     operation_setting.NodeAutoFailover,
		BalanceThreshold: operation_setting.NodeBalanceAlertThreshold,
		AlertWebhookUrl:  operation_setting.NodeAlertWebhookUrl,
		AlertEmail:       operation_setting.NodeAlertEmail,
		Cloudflare: cloudflare.Config{
			ApiToken:      cfToken,
			ZoneId:        operation_setting.CloudflareZoneId,
			ZoneName:      operation_setting.CloudflareZoneName,
			AccountId:     operation_setting.CloudflareAccountId,
			KvNamespaceId: operation_setting.CloudflareKvNamespaceId,
		},
	}
}

// DefaultTemplatePath 模板文件回落路径（Option 为空时读取；容器内可挂载）。
const DefaultTemplatePath = "deploy/.env.template"

// LoadTemplateText 返回模板文本：Option 非空优先，否则读取模板文件，最后内置默认。
func LoadTemplateText(opt Options) string {
	if strings.TrimSpace(opt.EnvTemplate) != "" {
		return opt.EnvTemplate
	}
	if path := strings.TrimSpace(os.Getenv("NODE_DEPLOY_ENV_TEMPLATE_FILE")); path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			return string(raw)
		}
	}
	if raw, err := os.ReadFile(DefaultTemplatePath); err == nil {
		return string(raw)
	}
	return operation_setting.DefaultNodeDeployEnvTemplate
}

// ParseEnvTemplate 解析 KEY=VALUE 行；忽略空行与 # 注释，剥离可选引号。
func ParseEnvTemplate(text string) []EnvVar {
	scanner := bufio.NewScanner(strings.NewReader(text))
	vars := make([]EnvVar, 0)
	seen := map[string]int{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if !validEnvKey(key) {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if idx, exists := seen[key]; exists {
			vars[idx].Value = value
			continue
		}
		seen[key] = len(vars)
		vars = append(vars, EnvVar{Key: key, Value: value})
	}
	return vars
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
		if i == 0 && r >= '0' && r <= '9' {
			return false
		}
	}
	return true
}

// ResolveVariables 合并模板与 envOverrides，返回变量名→值（排序由调用方处理）。
// 空值保留并产生 warning，由调用方决定是否列入 plan。
func ResolveVariables(template []EnvVar, overrides map[string]string) (map[string]string, []string) {
	values := make(map[string]string, len(template)+len(overrides))
	warnings := make([]string, 0)
	for _, item := range template {
		if override, ok := overrides[item.Key]; ok {
			values[item.Key] = override
			continue
		}
		values[item.Key] = item.Value
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, exists := values[key]; exists {
			continue
		}
		if !validEnvKey(key) {
			warnings = append(warnings, "ignored invalid env override name: "+key)
			continue
		}
		values[key] = overrides[key]
	}
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			warnings = append(warnings, "variable "+key+" has no value (set it in NodeDeployEnvTemplate or supply envOverrides)")
		}
	}
	sort.Strings(warnings)
	return values, warnings
}

// sensitiveVariable 名称命中密钥模式时不回显值（plan 展示脱敏，内部仍保存明文）。
func sensitiveVariable(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{"SECRET", "TOKEN", "PASSWORD", "PASSWD", "PRIVATE", "KEY", "DSN", "CREDENTIAL"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

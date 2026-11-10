// extlink.go M4 外链模块平台开关（Option 三项）。
package operation_setting

import "strings"

var (
	// ExtLinkEnabled 外链模块总开关；关闭时 /api/extlink 全组 404 且 /app/:slug 反代停用。
	ExtLinkEnabled = true
	// ExtLinkMaxCount 可配置的外链数量上限。
	ExtLinkMaxCount = 20
	// ExtLinkAllowHosts 反代目标 host 白名单（逗号分隔；空=不限，但启动时 WARN——
	// 反代有成为开放代理的风险面，生产建议配置）。支持精确匹配与前缀 ".example.com"
	// 形式的子域通配。
	ExtLinkAllowHosts = ""
)

// ExtLinkAllowHostList 返回解析后的白名单条目（去空白、去空项、小写化）。
func ExtLinkAllowHostList() []string {
	if strings.TrimSpace(ExtLinkAllowHosts) == "" {
		return nil
	}
	parts := strings.Split(ExtLinkAllowHosts, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// IsExtLinkHostAllowed 校验目标 host 是否命中白名单。白名单为空时放行（不限）。
func IsExtLinkHostAllowed(host string) bool {
	list := ExtLinkAllowHostList()
	if len(list) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSpace(host))
	for _, entry := range list {
		if strings.HasPrefix(entry, ".") {
			// ".example.com" 形式：匹配 example.com 及其全部子域。
			if host == entry[1:] || strings.HasSuffix(host, entry) {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
	}
	return false
}

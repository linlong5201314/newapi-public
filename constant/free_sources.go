package constant

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// ---------------------------------------------------------------------------
// M2 免费源一键模板（内嵌数据）。
//
// 数据来自 assets/free-pools.json + free-model-catalog.json 精选 12 个
// API-key 型免费源；baseUrl/signupUrl/freeNote 对齐 OmniRoute 元数据仓
// （open-sse/config/providers/registry + src/shared/constants/providers）。
// 蓝图：docs/M2-路由与免费源设计.md §3。
// ---------------------------------------------------------------------------

//go:embed free_sources.json
var freeSourcesJSON []byte

const XiaoyiWorkTemplateID = "xiaoyiwork"

// FreeSourceTemplate 一条免费源模板。
type FreeSourceTemplate struct {
	Id          string   `json:"id"`
	Name        string   `json:"name"`
	ChannelType int      `json:"channelType"`
	BaseUrl     string   `json:"baseUrl"`
	SignupUrl   string   `json:"signupUrl"`
	KeyRequired bool     `json:"keyRequired"`
	ExtraFields []string `json:"extraFields,omitempty"`
	// DefaultExtra 给 extraFields 的默认取值（用户留空时使用）。
	// 例：ds2api 的 {base} 默认填平台已部署的网关地址，用户仍可覆盖。
	DefaultExtra  map[string]string `json:"defaultExtra,omitempty"`
	Models        []string          `json:"models"`
	FreeNote      string            `json:"freeNote"`
	MonthlyTokens int64             `json:"monthlyTokens"`
	FreeType      string            `json:"freeType"` // recurring-daily / recurring-monthly / recurring-uncapped
	Tos           string            `json:"tos"`
	PoolKey       string            `json:"poolKey"`
	// AuthMode 授权方式：account=账号密码 / oauth=跳转授权登录 / ip=IP 直连（无需密钥） / key=API Key
	AuthMode string `json:"authMode,omitempty"`
	// AuthType 接入形态（前端据此决定显示哪个输入框）：
	//   none         → 无需密钥（IP 白名单 / 账号授权 / 配套网关）
	//   key          → 需要填上游 API Key
	//   subscription → 填机场/订阅链接，节点与额度由解析器读出来
	AuthType string `json:"authType,omitempty"`
	// HiddenEndpoint 为 true 时，普通用户拿到的模板不含 baseUrl / defaultExtra（只有管理员能看部署地址）
	HiddenEndpoint bool `json:"hiddenEndpoint,omitempty"`
}

type freeSourcesFile struct {
	Version   int                   `json:"version"`
	CuratedAt string                `json:"curatedAt"`
	Templates []*FreeSourceTemplate `json:"templates"`
}

var freeSources *freeSourcesFile

func loadFreeSources() *freeSourcesFile {
	if freeSources != nil {
		return freeSources
	}
	parsed := &freeSourcesFile{}
	if err := json.Unmarshal(freeSourcesJSON, parsed); err != nil {
		// 内嵌数据在编译期固定，解析失败属于构建事故，给空表不炸进程
		parsed.Templates = []*FreeSourceTemplate{}
	}
	freeSources = parsed
	return freeSources
}

// GetFreeSourceTemplates 返回全部模板（只读共享切片，调用方不得修改）。
func GetFreeSourceTemplates() []*FreeSourceTemplate {
	return loadFreeSources().Templates
}

// EffectiveAuthType 返回模板的接入形态；老模板没写 authType 时按 authMode 推导，
// 保证前后端在迁移期看到同一个口径。
func (t *FreeSourceTemplate) EffectiveAuthType() string {
	if t == nil {
		return "key"
	}
	if v := strings.TrimSpace(t.AuthType); v != "" {
		return v
	}
	switch t.AuthMode {
	case "ip", "oauth", "account", "gateway", "bridge":
		return "none"
	default:
		if t.KeyRequired {
			return "key"
		}
		return "none"
	}
}

// GetFreeSourceTemplate 按 id 取模板。
func GetFreeSourceTemplate(id string) *FreeSourceTemplate {
	for _, t := range loadFreeSources().Templates {
		if t.Id == strings.TrimSpace(id) {
			return t
		}
	}
	return nil
}

// Package workbuddy 把 WorkBuddy/CodeBuddy 账号型上游接入 new-api 渠道体系。
//
// account.go 凭据结构（channels.key JSON）+ 刷新判定 + 持久化写回。
// 移植自 _research/workbuddy2api-panel-main internal/auth（文件凭证 → DB 凭证）。
package workbuddy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// Cred WorkBuddy 渠道凭据（channels.key 的 JSON 内容；M5 统一做落库加密）。
// JSON 字段名与 M3 设计文档 §1 对齐（扁平 camelCase）。
type Cred struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt"` // Unix 秒
	Domain       string `json:"domain,omitempty"`
	Realm        string `json:"realm,omitempty"` // cn | global（空=按 domain 推断）
	UID          string `json:"uid,omitempty"`
	EnterpriseID string `json:"enterpriseId,omitempty"`
	Nickname     string `json:"nickname,omitempty"`
	DeviceToken  string `json:"deviceToken,omitempty"`

	// BaseOverride 测试/mock 用：非空时四域端点全部改走该 base（生产留空）。
	BaseOverride string `json:"baseOverride,omitempty"`
}

// ParseCred 解析渠道 key。兼容两种形态：
//   - 扁平形（本仓规范）：{"accessToken":..., "refreshToken":..., ...}
//   - 嵌套形（workbuddy2api-panel 插件 OAuth 输出）：{"auth":{...},"account":{...},"device_token":...}
func ParseCred(raw string) (*Cred, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("workbuddy channel key is empty (expect credential JSON)")
	}
	if !strings.HasPrefix(raw, "{") {
		return nil, fmt.Errorf("workbuddy channel key must be a credential JSON object")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil, fmt.Errorf("workbuddy credential parse: %w", err)
	}
	var c Cred
	if _, nested := probe["auth"]; nested {
		var n struct {
			Auth struct {
				AccessToken  string `json:"accessToken"`
				RefreshToken string `json:"refreshToken"`
				ExpiresAt    int64  `json:"expiresAt"`
				Domain       string `json:"domain"`
				Realm        string `json:"realm"`
			} `json:"auth"`
			Account struct {
				UID          string `json:"uid"`
				EnterpriseID string `json:"enterpriseId"`
				Nickname     string `json:"nickname"`
			} `json:"account"`
			DeviceToken  string `json:"device_token"`
			BaseOverride string `json:"baseOverride"`
		}
		if err := json.Unmarshal([]byte(raw), &n); err != nil {
			return nil, fmt.Errorf("workbuddy credential parse: %w", err)
		}
		c = Cred{
			AccessToken:  n.Auth.AccessToken,
			RefreshToken: n.Auth.RefreshToken,
			ExpiresAt:    n.Auth.ExpiresAt,
			Domain:       n.Auth.Domain,
			Realm:        n.Auth.Realm,
			UID:          n.Account.UID,
			EnterpriseID: n.Account.EnterpriseID,
			Nickname:     n.Account.Nickname,
			DeviceToken:  n.DeviceToken,
			BaseOverride: n.BaseOverride,
		}
	} else {
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, fmt.Errorf("workbuddy credential parse: %w", err)
		}
	}
	if strings.TrimSpace(c.AccessToken) == "" {
		return nil, fmt.Errorf("workbuddy credential missing accessToken")
	}
	return &c, nil
}

// Marshal 序列化回扁平 JSON（写回 channels.key）。
func (c *Cred) Marshal() string {
	raw, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(raw)
}

// Masked 返回脱敏副本（API 响应用：token 只留前缀）。
func (c *Cred) Masked() map[string]any {
	mask := func(s string) string {
		if len(s) <= 8 {
			if s == "" {
				return ""
			}
			return "***"
		}
		return s[:8] + "***"
	}
	return map[string]any{
		"accessToken":  mask(c.AccessToken),
		"refreshToken": mask(c.RefreshToken),
		"expiresAt":    c.ExpiresAt,
		"domain":       c.Domain,
		"realm":        c.RealmValue(),
		"uid":          c.UID,
		"enterpriseId": c.EnterpriseID,
		"nickname":     c.Nickname,
		"deviceToken":  mask(c.DeviceToken),
	}
}

// NeedsRefresh 报告 token 是否将在 within 内过期（或已过期/无 expiry）。
func (c *Cred) NeedsRefresh(within time.Duration) bool {
	if c.ExpiresAt <= 0 {
		return true
	}
	return time.Now().Add(within).Unix() >= c.ExpiresAt
}

// RealmValue 归一化域：显式 realm 优先，否则按 domain 后缀推断，缺省 cn。
func (c *Cred) RealmValue() string {
	if r := strings.TrimSpace(c.Realm); r == "global" || r == "cn" {
		return r
	}
	d := strings.ToLower(strings.TrimSpace(c.Domain))
	if d == "workbuddy.ai" || strings.HasSuffix(d, ".workbuddy.ai") {
		return "global"
	}
	return "cn"
}

// IsGlobal 报告账号是否 global realm。
func (c *Cred) IsGlobal() bool { return c.RealmValue() == "global" }

// IsEnterprise 报告账号是否企业版（无个人成长体系：签到/任务/连登全部跳过）。
func (c *Cred) IsEnterprise() bool { return strings.TrimSpace(c.EnterpriseID) != "" }

// IsEnterpriseSession 报告当前凭证是否企业上下文（企业版或企业切换会话签发的 JWT）。
// 该形态下 /v2/report 行为事件被上游静默丢弃（200 不计分），行为类任务必须跳过。
func (c *Cred) IsEnterpriseSession() bool {
	if c.IsEnterprise() {
		return true
	}
	return jwtTokenSource(c.AccessToken) == "enterprise_switch"
}

// jwtTokenSource 解出 JWT payload 的 token_source 声明；非 JWT/解码失败返回 ""。
func jwtTokenSource(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if raw, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return ""
		}
	}
	var payload struct {
		TokenSource string `json:"token_source"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return payload.TokenSource
}

// ---------------------------------------------------------------------------
// 凭据写回（刷新后）：更新 channels.key + 缓存失效。
// ---------------------------------------------------------------------------

// channelCredLocks 按渠道 ID 的进程内互斥锁（刷新单飞的第二道：DB 写回串行）。
var channelCredLocks sync.Map // map[int]*sync.Mutex

// ChannelLock 取渠道级互斥锁。
func ChannelLock(channelId int) *sync.Mutex {
	l, _ := channelCredLocks.LoadOrStore(channelId, &sync.Mutex{})
	return l.(*sync.Mutex)
}

// SaveCred 把刷新后的凭据写回 channels.key 并使渠道缓存失效。
// 调用方应持有 ChannelLock（adaptor 的刷新单飞已保证同渠道串行）。
// M5：写回前加密（WorkBuddy 属账号型渠道，CRYPTO_SECRET 派生 AES-GCM）。
func SaveCred(channelId int, cred *Cred) error {
	raw := cred.Marshal()
	if raw == "" {
		return fmt.Errorf("workbuddy credential marshal failed")
	}
	raw = model.EncryptChannelKey(constant.ChannelTypeWorkBuddy, raw)
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", channelId).Update("key", raw).Error; err != nil {
		return fmt.Errorf("workbuddy credential persist: %w", err)
	}
	// 缓存失效：重读并刷新内存渠道缓存（MemoryCacheEnabled 时才有意义，函数内部安全）。
	if common.MemoryCacheEnabled {
		if ch, err := model.GetChannelById(channelId, true); err == nil {
			model.CacheUpdateChannel(ch)
		}
	}
	return nil
}

// LoadChannelCred 从渠道记录读出凭据（跨节点单飞等待时对比快照用）。
func LoadChannelCred(channelId int) (*Cred, error) {
	ch, err := model.GetChannelById(channelId, true)
	if err != nil {
		return nil, err
	}
	key := model.DecryptChannelKey(ch.Type, ch.Key)
	if ch.ChannelInfo.IsMultiKey {
		keys := ch.GetKeys()
		if len(keys) > 0 {
			key = keys[0]
		}
	}
	return ParseCred(key)
}

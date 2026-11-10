// Package trae 把 Trae SOLO 账号型上游接入 new-api 渠道体系。
//
// account.go 凭据结构（channels.key JSON）+ 刷新判定。
// 移植自 _research/trae2api-web-main internal/auth（文件凭证 → DB 凭证）。
package trae

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Cred Trae 渠道凭据（channels.key 的 JSON 内容；M5 统一做落库加密）。
// JSON 字段名与 M3 设计文档 §1 对齐（扁平 camelCase）。
type Cred struct {
	AccessToken  string `json:"accessToken"` // Cloud-IDE-JWT 头用
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt"` // Unix 秒
	UID          string `json:"uid,omitempty"`
	MachineID    string `json:"machineId,omitempty"` // hex32
	DeviceID     string `json:"deviceId,omitempty"`  // hex32
	Nickname     string `json:"nickname,omitempty"`
	Domain       string `json:"domain,omitempty"`  // "trae.cn"
	ApiHost      string `json:"apiHost,omitempty"` // ExchangeToken host，空=默认 api.trae.com.cn
	EnterpriseID string `json:"enterpriseId,omitempty"`

	// BaseOverride 测试/mock 用：非空时三 host 全部改走该 base（生产留空）。
	BaseOverride string `json:"baseOverride,omitempty"`
}

// ParseCred 解析渠道 key。兼容扁平形与嵌套形（trae2api 登录脚本产出）。
func ParseCred(raw string) (*Cred, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("trae channel key is empty (expect credential JSON)")
	}
	if !strings.HasPrefix(raw, "{") {
		return nil, fmt.Errorf("trae channel key must be a credential JSON object")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil, fmt.Errorf("trae credential parse: %w", err)
	}
	var c Cred
	if _, nested := probe["auth"]; nested {
		var n struct {
			Auth struct {
				AccessToken  string `json:"accessToken"`
				RefreshToken string `json:"refreshToken"`
				ExpiresAt    int64  `json:"expiresAt"`
				Domain       string `json:"domain"`
				ApiHost      string `json:"apiHost"`
				MachineID    string `json:"machineId"`
				DeviceID     string `json:"deviceId"`
			} `json:"auth"`
			Account struct {
				UID          string `json:"uid"`
				EnterpriseID string `json:"enterpriseId"`
				Nickname     string `json:"nickname"`
			} `json:"account"`
			BaseOverride string `json:"baseOverride"`
		}
		if err := json.Unmarshal([]byte(raw), &n); err != nil {
			return nil, fmt.Errorf("trae credential parse: %w", err)
		}
		c = Cred{
			AccessToken:  n.Auth.AccessToken,
			RefreshToken: n.Auth.RefreshToken,
			ExpiresAt:    n.Auth.ExpiresAt,
			Domain:       n.Auth.Domain,
			ApiHost:      n.Auth.ApiHost,
			MachineID:    n.Auth.MachineID,
			DeviceID:     n.Auth.DeviceID,
			UID:          n.Account.UID,
			EnterpriseID: n.Account.EnterpriseID,
			Nickname:     n.Account.Nickname,
			BaseOverride: n.BaseOverride,
		}
	} else {
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, fmt.Errorf("trae credential parse: %w", err)
		}
	}
	if strings.TrimSpace(c.AccessToken) == "" && strings.TrimSpace(c.RefreshToken) == "" {
		return nil, fmt.Errorf("trae credential requires accessToken or refreshToken")
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

// Masked 返回脱敏副本（API 响应用）。
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
		"uid":          c.UID,
		"machineId":    c.MachineID,
		"deviceId":     c.DeviceID,
		"nickname":     c.Nickname,
		"domain":       c.Domain,
	}
}

// NeedsRefresh 报告 token 是否将在 within 内过期（或已过期/无 expiry）。
func (c *Cred) NeedsRefresh(within time.Duration) bool {
	if c.ExpiresAt <= 0 {
		return true
	}
	return time.Now().Add(within).Unix() >= c.ExpiresAt
}

// MaskToken 保留前 n 字符 + 省略号（导入预览脱敏用）。
func MaskToken(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("…(%d chars)", len(s))
}

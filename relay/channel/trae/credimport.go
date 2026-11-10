// credimport.go Trae 凭据导入解析：回调链接（含 refreshToken+userJwt 的 302 URL）
// 或手填 refreshToken+uid → ExchangeToken 换正式 token + GetUserInfo 补全。
// 移植自 _research/trae2api-web-main internal/server/callback.go。
package trae

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CallbackInfo 回调链接解析结果（仅服务端内部使用）。
type CallbackInfo struct {
	RefreshToken string // 优先 query.refreshToken，缺省回退 userJwt.RefreshToken
	AccessToken  string // 无 refreshToken 时回退 userJwt.Token（兜底）
	UID          string // userInfo.UserID
	Nickname     string // userInfo.ScreenName
	EnterpriseID string // userInfo.TenantID（注意回调字段名是 TenantID）
	ExpiresAt    int64  // userJwt 兜底路径的过期时间
}

// parseJSONParam 解回调里 URL 编码的 JSON 参数（容错双层 unquote）。
func parseJSONParam(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	candidates := []string{raw}
	if uq, err := url.QueryUnescape(raw); err == nil && uq != raw {
		candidates = append(candidates, uq)
	}
	for _, c := range candidates {
		var obj map[string]any
		if json.Unmarshal([]byte(c), &obj) == nil && obj != nil {
			return obj
		}
	}
	return nil
}

func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	}
	return fmt.Sprintf("%v", v)
}

func getInt64(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

// ParseCallback 解析 TRAE 登录回调链接，提取凭证字段。
// 回调形如：http://127.0.0.1:18080/authorize?refreshToken=...&userInfo={...}&userJwt={...}
// refreshToken 优先；缺失时回退 userJwt.RefreshToken，再兜底 userJwt.Token。
func ParseCallback(rawURL string) (*CallbackInfo, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("empty callback url")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse callback url: %w", err)
	}
	q := u.Query()
	info := &CallbackInfo{
		RefreshToken: q.Get("refreshToken"),
	}

	userInfo := parseJSONParam(q.Get("userInfo"))
	info.UID = getString(userInfo, "UserID")
	info.Nickname = getString(userInfo, "ScreenName")
	info.EnterpriseID = getString(userInfo, "TenantID")

	userJwt := parseJSONParam(q.Get("userJwt"))
	jwtToken := getString(userJwt, "Token")
	jwtRefresh := getString(userJwt, "RefreshToken")

	if info.RefreshToken == "" {
		info.RefreshToken = jwtRefresh
	}
	if info.RefreshToken == "" {
		info.AccessToken = jwtToken
		if jwtToken == "" {
			return nil, fmt.Errorf("callback missing refreshToken and userJwt.Token")
		}
		if exp := getInt64(userJwt, "TokenExpireAt"); exp > 0 {
			info.ExpiresAt = NormalizeExpire(exp)
		}
	}
	return info, nil
}

// NewDeviceIDs 生成一对 hex32 machineID/deviceID（导入凭据时缺省补齐）。
func NewDeviceIDs() (machineID, deviceID string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b[:16]), hex.EncodeToString(b[16:])
}

// ImportCredential 把解析后的登录材料换成正式凭据：
//   - 有 refreshToken → ExchangeToken 轮换整族（注意：会踢同账号其他客户端下线）
//   - 仅 accessToken（userJwt 兜底）→ 直接用，无法刷新（短命）
// 随后 GetUserInfo 补全 uid/nickname（best-effort）。
// machineID/deviceID 为空时自动生成一对。baseOverride 仅测试用
// （空串时回落 TRAE_BASE_URL env，生产两者皆空走默认 host）。
func ImportCredential(info *CallbackInfo, machineID, deviceID, baseOverride string) (*Cred, error) {
	if machineID == "" || deviceID == "" {
		machineID, deviceID = NewDeviceIDs()
	}
	if baseOverride == "" {
		baseOverride = envBaseOverride()
	}
	cred := &Cred{
		UID:          info.UID,
		Nickname:     info.Nickname,
		EnterpriseID: info.EnterpriseID,
		MachineID:    machineID,
		DeviceID:     deviceID,
		Domain:       "trae.cn",
		ExpiresAt:    info.ExpiresAt,
		BaseOverride: baseOverride,
	}
	client := NewClient(cred)
	if info.RefreshToken != "" {
		cred.RefreshToken = info.RefreshToken
		newCred, err := client.refreshTokenOnce()
		if err != nil {
			return nil, fmt.Errorf("ExchangeToken failed: %w", err)
		}
		cred = newCred
	} else if info.AccessToken != "" {
		cred.AccessToken = info.AccessToken
	} else {
		return nil, fmt.Errorf("no refreshToken or accessToken to import")
	}
	// GetUserInfo 补全（best-effort：失败不阻断导入）。
	if uid, nick, ent, err := NewClient(cred).GetUserInfo(); err == nil {
		if cred.UID == "" {
			cred.UID = uid
		}
		if cred.Nickname == "" {
			cred.Nickname = nick
		}
		if cred.EnterpriseID == "" {
			cred.EnterpriseID = ent
		}
	}
	if cred.ExpiresAt <= 0 && cred.RefreshToken == "" {
		// userJwt 兜底且无过期信息：保守标记 1h（到期后强制重新导入）。
		cred.ExpiresAt = time.Now().Add(time.Hour).Unix()
	}
	return cred, nil
}

// ImportFromRefreshToken 手填 refreshToken+uid 的导入路径（与回调链接同终点）。
func ImportFromRefreshToken(refreshToken, uid, machineID, deviceID, baseOverride string) (*Cred, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("refreshToken is required")
	}
	return ImportCredential(&CallbackInfo{RefreshToken: refreshToken, UID: strings.TrimSpace(uid)}, machineID, deviceID, baseOverride)
}

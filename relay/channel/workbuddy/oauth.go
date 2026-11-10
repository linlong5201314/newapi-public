// oauth.go WorkBuddy OAuth 设备授权流（全平台可用）：
// POST /v2/plugin/auth/state?platform=CLI → 浏览器授权 →
// GET /v2/plugin/auth/token?state= 轮询 → GET /v2/plugin/login/account 取 uid。
// 移植自 _research/workbuddy2api-panel-main internal/panel/login.go（面板内嵌版）。
package workbuddy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 设备授权端点（按 realm 分域；baseOverride 仅测试用——生产经 controller 传
// WORKBUDDY_BASE_URL env 或空串）。
func oauthEndpoints(realm, baseOverride string) (state, token, account, origin string) {
	base := "https://copilot.tencent.com"
	origin = originRefererCN
	if realm == "global" {
		base = defaultGlobalBase
		origin = originRefererGlobal
	}
	if baseOverride != "" {
		base = strings.TrimSuffix(baseOverride, "/")
		// mock 全局域：origin 指回 CN 形态无妨（mock 不校验），生产 base 不覆盖 origin。
	}
	return base + "/v2/plugin/auth/state?platform=CLI",
		base + "/v2/plugin/auth/token?state=",
		base + "/v2/plugin/login/account?state=",
		origin
}

// loginUA 设备授权专用 UA（CLI 形态，与官方登录脚本一致）。
const loginUA = "CLI/2.63.2 CodeBuddy/2.63.2"

var oauthHTTP = &http.Client{Timeout: 30 * time.Second}

// OAuthSession 一次设备流会话（10min 过期）。
type OAuthSession struct {
	State        string    `json:"state"`
	AuthURL      string    `json:"auth_url"`
	Realm        string    `json:"realm"`
	BaseOverride string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresIn    int64     `json:"expires_in"` // 会话剩余秒数（返回给前端的提示值）
}

// OAuthSessionTTL 会话态过期时间（对齐上游授权页有效期）。
const OAuthSessionTTL = 10 * time.Minute

var (
	oauthSessionsMu sync.Mutex
	oauthSessions   = map[string]*OAuthSession{}
)

// oauthDoJSON 设备流专用 JSON 请求（解 {code,msg,data} 信封）。
func oauthDoJSON(method, fullURL, bearer string, body io.Reader, origin string) (json.RawMessage, error) {
	req, err := http.NewRequest(method, fullURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("User-Agent", loginUA)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := oauthHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http_error: upstream %d", resp.StatusCode)
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse failed: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("code=%d msg=%s", env.Code, env.Msg)
	}
	return env.Data, nil
}

// OAuthStart 发起设备授权：拿 state+authUrl 并登记会话（返回给前端打开浏览器）。
// realm: "cn"（缺省）| "global"；baseOverride 仅测试用。
func OAuthStart(realm, baseOverride string) (*OAuthSession, error) {
	if realm != "global" {
		realm = "cn"
	}
	epState, _, _, origin := oauthEndpoints(realm, baseOverride)
	data, err := oauthDoJSON(http.MethodPost, epState, "", bytes.NewReader([]byte("{}")), origin)
	if err != nil {
		return nil, fmt.Errorf("auth state: %w", err)
	}
	var st struct {
		State   string `json:"state"`
		AuthURL string `json:"authUrl"`
	}
	if err := json.Unmarshal(data, &st); err != nil || st.State == "" || st.AuthURL == "" {
		return nil, fmt.Errorf("auth state: missing state or authUrl")
	}
	sess := &OAuthSession{
		State:        st.State,
		AuthURL:      st.AuthURL,
		Realm:        realm,
		BaseOverride: baseOverride,
		CreatedAt:    time.Now(),
		ExpiresIn:    int64(OAuthSessionTTL / time.Second),
	}
	oauthSessionsMu.Lock()
	// 顺手回收过期会话。
	for s, old := range oauthSessions {
		if time.Since(old.CreatedAt) > OAuthSessionTTL {
			delete(oauthSessions, s)
		}
	}
	oauthSessions[st.State] = sess
	oauthSessionsMu.Unlock()
	return sess, nil
}

// GetOAuthSession 查询会话（controller 校验 sessionId 归属用）。
func GetOAuthSession(state string) (*OAuthSession, bool) {
	oauthSessionsMu.Lock()
	defer oauthSessionsMu.Unlock()
	sess, ok := oauthSessions[state]
	if !ok {
		return nil, false
	}
	if time.Since(sess.CreatedAt) > OAuthSessionTTL {
		delete(oauthSessions, state)
		return nil, false
	}
	return sess, true
}

// dropOAuthSession 完成后销毁会话。
func dropOAuthSession(state string) {
	oauthSessionsMu.Lock()
	delete(oauthSessions, state)
	oauthSessionsMu.Unlock()
}

// OAuthPoll 轮询设备流状态。
// 返回 (done, cred, error)：done=false 表示用户尚未完成授权（非错误）；
// done=true 时 cred 为完整凭据（已含 uid/nickname/domain/expiresAt）。
func OAuthPoll(state string) (bool, *Cred, error) {
	sess, ok := GetOAuthSession(state)
	if !ok {
		return false, nil, fmt.Errorf("unknown or expired state（请重新发起授权）")
	}
	_, epToken, epAcct, origin := oauthEndpoints(sess.Realm, sess.BaseOverride)
	// auth/token 是权威登录状态端点：pending 时业务 code 非 0（"login ing"）。
	tokRaw, err := oauthDoJSON(http.MethodGet, epToken+state, "", nil, origin)
	if err != nil {
		return false, nil, nil // pending：前端继续轮询
	}
	var tok struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if err := json.Unmarshal(tokRaw, &tok); err != nil || tok.AccessToken == "" {
		return false, nil, nil
	}
	// 完成：取 uid/nickname（失败不阻塞——uid 为空时签到/稳定设备头降级）。
	var acct struct {
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterpriseId"`
		Nickname     string `json:"nickname"`
	}
	if acctRaw, err := oauthDoJSON(http.MethodGet, epAcct+state, tok.AccessToken, nil, origin); err == nil {
		_ = json.Unmarshal(acctRaw, &acct)
	}
	cred := &Cred{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Domain:       tok.Domain,
		Realm:        sess.Realm,
		UID:          acct.UID,
		EnterpriseID: acct.EnterpriseID,
		Nickname:     acct.Nickname,
		BaseOverride: sess.BaseOverride,
	}
	if tok.ExpiresIn > 0 {
		cred.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix()
	}
	dropOAuthSession(state)
	return true, cred, nil
}

// OAuthSessionGC 供测试/运维手动回收过期会话。
func OAuthSessionGC() {
	oauthSessionsMu.Lock()
	defer oauthSessionsMu.Unlock()
	for s, old := range oauthSessions {
		if time.Since(old.CreatedAt) > OAuthSessionTTL {
			delete(oauthSessions, s)
		}
	}
}

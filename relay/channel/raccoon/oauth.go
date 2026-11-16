package raccoon

// oauth.go 小浣熊（办公小浣熊 / Raccoon Work）浏览器授权闭环。
//
// 协议（从 magpie 社区插件 raccoon-auth 的真实实现核对过）：
//  1. 打开授权页 GET https://xiaohuanxiong.com/code/authorize
//     ?login_source=desktop&appname=办公小浣熊客户端&state=<64hex>
//     &desktop_callback=loopback&redirect=<我们的回调地址>
//  2. 用户登录后浏览器 302 到 redirect?code=<授权码>&state=<原样带回>
//  3. 换票 POST https://xiaohuanxiong.com/api/web/auth/v1/login_with_authorization_code
//     body {"authorization_code": code} → data.access_token / data.refresh_token
//
// 关键点：redirect 指向我们自己的公网地址（默认 https://gw.ailong.love），
// 这样浏览器会把 code 直接送到服务端，用户不需要手工复制回调链接。
// 假 code 实测返回 400 {"code":200035,"message":"authorization_code_not_found_error"}。

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// AuthorizeBase 网页授权页（注意是 /code/authorize，不是 /authorize）。
	AuthorizeBase = "https://xiaohuanxiong.com/code/authorize"
	// AuthAPIBase 换票接口前缀。
	AuthAPIBase = "https://xiaohuanxiong.com/api/web/auth/v1"
	// AppName 授权页展示的应用名，必须是桌面客户端名，否则上游拒绝。
	AppName = "办公小浣熊客户端"
	// SessionTTL 授权会话有效期（用户从点授权到扫码登录完成的时间窗）。
	SessionTTL = 15 * time.Minute
)

// OAuthSession 一次浏览器授权会话。
type OAuthSession struct {
	State     string
	CreatedAt time.Time
	Done      bool
	Access    string
	Refresh   string
	AccountID string
	Phone     string
	Err       string
}

var (
	sessMu   sync.Mutex
	sessions = map[string]*OAuthSession{}
)

// PublicBaseURL 返回对外可访问的基址，用于拼接回调地址。
// 优先 PUBLIC_BASE_URL 环境变量（生产指向 https://gw.ailong.love），
// 其次 SERVER_ADDRESS，最后回落到本机。
func PublicBaseURL() string {
	for _, key := range []string{"PUBLIC_BASE_URL", "SERVER_ADDRESS"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return "http://localhost:3000"
}

// CallbackPath 我们的回调路由（挂在 /api/channel/self/oauth/raccoon/callback）。
const CallbackPath = "/api/channel/self/oauth/raccoon/callback"

// CallbackURL 拼出完整回调地址。
func CallbackURL() string {
	return PublicBaseURL() + CallbackPath
}

func randomState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Start 创建授权会话并返回可打开的授权页地址。
func Start() (*OAuthSession, error) {
	state, err := randomState()
	if err != nil {
		return nil, err
	}
	sess := &OAuthSession{State: state, CreatedAt: time.Now()}
	sessMu.Lock()
	// 顺手清理过期会话，避免内存无限增长。
	for k, v := range sessions {
		if time.Since(v.CreatedAt) > SessionTTL {
			delete(sessions, k)
		}
	}
	sessions[state] = sess
	sessMu.Unlock()
	return sess, nil
}

// AuthorizeURL 由会话 state 拼出授权页地址。
func AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("login_source", "desktop")
	q.Set("appname", AppName)
	q.Set("state", state)
	q.Set("desktop_callback", "loopback")
	q.Set("redirect", CallbackURL())
	return AuthorizeBase + "?" + q.Encode()
}

// Get 读取会话（返回副本，避免并发读写同一结构）。
func Get(state string) *OAuthSession {
	sessMu.Lock()
	defer sessMu.Unlock()
	s, ok := sessions[state]
	if !ok {
		return nil
	}
	cp := *s
	return &cp
}

// Finish 由回调处理函数调用：校验 state → 换票 → 写回会话。
func Finish(state, code string) (*OAuthSession, error) {
	if strings.TrimSpace(state) == "" {
		return nil, errors.New("缺少 state，无法确认授权来源")
	}
	if strings.TrimSpace(code) == "" {
		return nil, errors.New("缺少授权码 code")
	}
	sessMu.Lock()
	s, ok := sessions[state]
	sessMu.Unlock()
	if !ok {
		return nil, errors.New("授权会话不存在或已过期，请重新点一次授权")
	}
	if time.Since(s.CreatedAt) > SessionTTL {
		return nil, errors.New("授权会话已过期，请重新点一次授权")
	}
	access, refresh, accountID, phone, err := Exchange(code)
	if err != nil {
		sessMu.Lock()
		s.Err = err.Error()
		sessMu.Unlock()
		return nil, err
	}
	sessMu.Lock()
	s.Done = true
	s.Access = access
	s.Refresh = refresh
	s.AccountID = accountID
	s.Phone = phone
	s.Err = ""
	sessMu.Unlock()
	cp := *s
	return &cp, nil
}

// Exchange 用授权码换 access/refresh token。
func Exchange(code string) (access, refresh, accountID, phone string, err error) {
	body, _ := json.Marshal(map[string]string{"authorization_code": strings.TrimSpace(code)})
	req, err := http.NewRequest(http.MethodPost, AuthAPIBase+"/login_with_authorization_code", bytes.NewReader(body))
	if err != nil {
		return "", "", "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range BrowserHeaders {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", "", fmt.Errorf("连接小浣熊换票接口失败: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Details string `json:"details"`
		Data    struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			User         struct {
				ID    string `json:"id"`
				Phone string `json:"phone"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", "", "", fmt.Errorf("换票响应无法解析（HTTP %d）: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	if payload.Code == 200035 {
		return "", "", "", "", errors.New("授权码已失效，请重新发起网页授权")
	}
	if payload.Data.AccessToken == "" || payload.Data.RefreshToken == "" {
		msg := payload.Message
		if msg == "" {
			msg = payload.Details
		}
		if msg == "" {
			msg = truncate(string(raw), 200)
		}
		return "", "", "", "", fmt.Errorf("换票失败（HTTP %d）: %s", resp.StatusCode, msg)
	}
	return payload.Data.AccessToken, payload.Data.RefreshToken, payload.Data.User.ID, payload.Data.User.Phone, nil
}

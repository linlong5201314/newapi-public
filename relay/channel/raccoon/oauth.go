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
// 关键点：redirect 指向我们自己的公网地址（生产 https://gw.ailong.love），
// 这样浏览器会把 code 直接送到服务端，用户不需要手工复制回调链接。
// 假 code 实测返回 400 {"code":200035,"message":"authorization_code_not_found_error"}。
//
// 会话状态不放在进程内：授权链路要经过 gw 前置负载均衡，start 与 callback
// 可能落在不同节点，所以状态由调用方（controller）落到共享库。

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
	// CallbackPath 我们的回调路由。
	CallbackPath = "/api/channel/self/oauth/raccoon/callback"
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

// CallbackURL 拼出完整回调地址。
func CallbackURL() string {
	return PublicBaseURL() + CallbackPath
}

// NewState 生成 64 位 hex 的 state。
func NewState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// AuthorizeURL 由 state 拼出授权页地址。
func AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("login_source", "desktop")
	q.Set("appname", AppName)
	q.Set("state", state)
	q.Set("desktop_callback", "loopback")
	q.Set("redirect", CallbackURL())
	return AuthorizeBase + "?" + q.Encode()
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

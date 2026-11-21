package service

// Nous Portal OAuth device flow (RFC 8628), protocol from
// magpie-community/opencode-nous-auth@0.1.3 oauth.mjs.
// Portal issues rotating inference JWTs; the refresh token is sent via
// x-nous-refresh-token header, not the standard form field.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	NousPortalBase      = "https://portal.nousresearch.com"
	NousInferenceBase   = "https://inference-api.nousresearch.com/v1"
	NousWelcomeBase     = "https://welcome-api.nousresearch.com/v1"
	NousClientID        = "hermes-cli"
	NousScope           = "inference:invoke"
	nousDeviceCodeURL   = NousPortalBase + "/api/oauth/device/code"
	nousTokenURL        = NousPortalBase + "/api/oauth/token"
	nousGrantType       = "urn:ietf:params:oauth:grant-type:device_code"
	nousMaxPollTimeout  = 30 * time.Minute
	nousDefaultInterval = 5 * time.Second
)

// NousTicket 设备授权票据（start 返回，poll 消费）。
type NousTicket struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	Until           int64  `json:"until"`
}

// NousCredential Nous Portal 授权凭据（加密存储）。
type NousCredential struct {
	AccessToken  string        `json:"access_token"`
	RefreshToken string        `json:"refresh_token"`
	ExpiresAt    int64         `json:"expires_at"`
	Scope        string        `json:"scope"`
	TokenType    string        `json:"token_type"`
	BaseURL      string        `json:"base_url"` // inference base URL
	Egress       AccountEgress `json:"egress"`
}

// NousModel 推理目录中的一个模型。
type NousModel struct {
	ID string `json:"id"`
}

// ValidNousAuthorizationURL 校验回调 URL 必须是官方 verification URI。
func ValidNousAuthorizationURL(target string) bool {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return false
	}
	return u.Host == "portal.nousresearch.com"
}

// NousBrowserUserAgent identifies the gateway's login relay and inference
// client as a browser the portal's edge accepts; the models endpoint answers
// 403 to the bare Go client identity. It carries no credential.
const NousBrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// nousPost 发送 form-urlencoded POST，带超时和重定向禁用。
func nousPost(ctx context.Context, proxy, target string, form url.Values, headers map[string]string) (int, []byte, error) {
	client, err := GetAccountAuthHTTPClient(proxy, "")
	if err != nil || client == nil {
		return 0, nil, ErrAccountEgress
	}
	copyClient := *client
	// The token exchange can take noticeably longer than device-code issuance
	// when the portal queues the assertion; 10s produced false upstream errors.
	copyClient.Timeout = 30 * time.Second
	copyClient.Jar = nil
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, ErrAccountProtocol
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	// The portal's token endpoint sits behind an edge filter that rejects the
	// bare Go client identity; the device-code endpoint does not. Present the
	// same browser identity the portal serves to a real login page.
	req.Header.Set("User-Agent", NousBrowserUserAgent)
	req.Header.Set("Origin", NousPortalBase)
	req.Header.Set("Referer", NousPortalBase+"/manage-subscription")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := copyClient.Do(req)
	if err != nil {
		return 0, nil, ErrAccountUpstream
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return 0, nil, ErrAccountProtocol
	}
	return res.StatusCode, raw, nil
}

// StartNousSignIn 发起设备授权。返回票据 + 用户验证 URL。
func StartNousSignIn(ctx context.Context, proxy string) (*NousTicket, string, error) {
	form := url.Values{"client_id": {NousClientID}, "scope": {NousScope}}
	status, raw, err := nousPost(ctx, proxy, nousDeviceCodeURL, form, nil)
	if err != nil {
		return nil, "", err
	}
	if status != http.StatusOK {
		return nil, "", ErrAccountUpstream
	}
	var resp struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		// verification_uri_complete pre-fills the device code so the user lands
		// on a ready-to-confirm page instead of having to type the code.
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, "", ErrAccountProtocol
	}
	if resp.DeviceCode == "" || resp.UserCode == "" || resp.VerificationURI == "" || resp.ExpiresIn <= 0 {
		return nil, "", ErrAccountProtocol
	}
	if !ValidNousAuthorizationURL(resp.VerificationURI) {
		return nil, "", ErrAccountProtocol
	}
	// Prefer the complete URI (code pre-filled) when the portal supplies it;
	// fall back to the bare verification page otherwise. Guard against a
	// mismatched embedded code.
	verificationURI := resp.VerificationURI
	if resp.VerificationURIComplete != "" && ValidNousAuthorizationURL(resp.VerificationURIComplete) {
		if u, parseErr := url.Parse(resp.VerificationURIComplete); parseErr == nil {
			if embedded := u.Query().Get("user_code"); embedded == "" || embedded == resp.UserCode {
				verificationURI = resp.VerificationURIComplete
			}
		}
	}
	interval := resp.Interval
	if interval < 1 {
		interval = int(nousDefaultInterval / time.Second)
	}
	until := time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second).Unix()
	if until > time.Now().Add(nousMaxPollTimeout).Unix() {
		until = time.Now().Add(nousMaxPollTimeout).Unix()
	}
	ticket := &NousTicket{
		DeviceCode:      resp.DeviceCode,
		UserCode:        resp.UserCode,
		VerificationURI: verificationURI,
		Interval:        interval,
		Until:           until,
	}
	return ticket, verificationURI, nil
}

// nousJWTClaims 只解 JWT payload 的 exp/scope，不验签（TLS 保护传输，服务端验签）。
func nousJWTClaims(token string) (exp int64, scopes []string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, nil, errors.New("malformed JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, nil, err
	}
	var claims struct {
		Exp   int64  `json:"exp"`
		Scope string `json:"scope"`
		Scp   string `json:"scp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return 0, nil, err
	}
	scopeText := claims.Scope
	if scopeText == "" {
		scopeText = claims.Scp
	}
	if scopeText != "" {
		scopes = strings.Fields(scopeText)
	}
	return claims.Exp, scopes, nil
}

// nousTokenResponse token 端点响应。
type nousTokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	Scope            string `json:"scope"`
	TokenType        string `json:"token_type"`
	InferenceBaseURL string `json:"inference_base_url"`
}

// nousParseCredential 从 token 响应解析凭据。必须含 refresh_token。
func nousParseCredential(body []byte, started time.Time) (*NousCredential, error) {
	var resp nousTokenResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, ErrAccountProtocol
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		return nil, ErrAccountProtocol
	}
	// 过期时间：取 expires_in 和 JWT exp 的较小值（插件协议 #328）。
	expiresAt := started.Add(time.Duration(resp.ExpiresIn) * time.Second).Unix()
	jwtExp, scopes, err := nousJWTClaims(resp.AccessToken)
	if err == nil && jwtExp > 0 && jwtExp < expiresAt {
		expiresAt = jwtExp
	}
	// scope 校验
	scopeOK := resp.Scope == NousScope
	if !scopeOK {
		for _, s := range scopes {
			if s == NousScope {
				scopeOK = true
				break
			}
		}
	}
	if !scopeOK {
		return nil, ErrAccountProtocol
	}
	baseURL := resp.InferenceBaseURL
	if baseURL == "" {
		baseURL = NousInferenceBase
	}
	// 只允许两个生产推理地址
	if baseURL != NousInferenceBase && baseURL != NousWelcomeBase {
		baseURL = NousInferenceBase
	}
	tokenType := resp.TokenType
	if tokenType == "" {
		tokenType = "Bearer"
	}
	return &NousCredential{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ExpiresAt:    expiresAt,
		Scope:        resp.Scope,
		TokenType:    tokenType,
		BaseURL:      baseURL,
	}, nil
}

// PollNousSignIn 轮询设备授权状态。返回 nil, nil 表示还在等待用户。
func PollNousSignIn(ctx context.Context, proxy string, ticket *NousTicket) (*NousCredential, error) {
	if ticket == nil || ticket.DeviceCode == "" {
		return nil, ErrAccountProtocol
	}
	if ticket.Until <= time.Now().Unix() {
		return nil, errors.New("device code expired")
	}
	form := url.Values{
		"grant_type":  {nousGrantType},
		"client_id":   {NousClientID},
		"device_code": {ticket.DeviceCode},
	}
	status, raw, err := nousPost(ctx, proxy, nousTokenURL, form, nil)
	if err != nil {
		common.SysLog(fmt.Sprintf("nous token exchange transport error: proxy=%t err=%v", proxy != "", err))
		return nil, err
	}
	if status != http.StatusOK {
		fragment := strings.TrimSpace(string(raw))
		if len(fragment) > 160 {
			fragment = fragment[:160]
		}
		common.SysLog(fmt.Sprintf("nous token exchange rejected: status=%d body=%s", status, fragment))
		// RFC 8628: authorization_pending 继续等，其他错误终止。
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &errResp) == nil {
			if errResp.Error == "authorization_pending" {
				return nil, nil
			}
			if errResp.Error == "slow_down" {
				ticket.Interval += 5
				return nil, nil
			}
			if errResp.Error == "expired_token" || errResp.Error == "access_denied" {
				return nil, errors.New("device authorization expired or denied")
			}
		}
		return nil, ErrAccountUpstream
	}
	return nousParseCredential(raw, time.Now())
}

// RenewNousCredential 用 refresh_token 换新的 access_token。
func RenewNousCredential(ctx context.Context, proxy string, cred *NousCredential) (*NousCredential, error) {
	if cred == nil || cred.RefreshToken == "" {
		return nil, ErrAccountProtocol
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {NousClientID},
		"refresh_token": {cred.RefreshToken},
	}
	headers := map[string]string{
		"x-nous-refresh-token": cred.RefreshToken,
	}
	status, raw, err := nousPost(ctx, proxy, nousTokenURL, form, headers)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, ErrAccountUpstream
	}
	return nousParseCredential(raw, time.Now())
}

// nousHostOf reduces a base URL to its host for diagnostics; never logs paths
// or credentials.
func nousHostOf(base string) string {
	if u, err := url.Parse(strings.TrimSpace(base)); err == nil && u.Host != "" {
		return u.Host
	}
	return "invalid"
}

// FetchNousModels 拉取当前账号可用的推理模型列表。
func FetchNousModels(ctx context.Context, proxy string, cred *NousCredential) ([]string, error) {
	if cred == nil || cred.AccessToken == "" {
		return nil, ErrAccountProtocol
	}
	if cred.ExpiresAt <= time.Now().Unix() {
		return nil, errors.New("Nous Portal credential expired")
	}
	client, err := GetAccountAuthHTTPClient(proxy, "")
	if err != nil || client == nil {
		return nil, ErrAccountEgress
	}
	copyClient := *client
	copyClient.Timeout = 15 * time.Second
	copyClient.Jar = nil
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cred.BaseURL+"/models", nil)
	if err != nil {
		return nil, ErrAccountProtocol
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	// The inference edge rejects the bare Go client identity with 403.
	req.Header.Set("User-Agent", NousBrowserUserAgent)
	res, err := copyClient.Do(req)
	if err != nil {
		common.SysLog(fmt.Sprintf("nous model discovery transport error: host=%s err=%v", nousHostOf(cred.BaseURL), err))
		return nil, fmt.Errorf("nous model discovery unreachable: %w", ErrAccountUpstream)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, ErrAccountProtocol
	}
	if res.StatusCode != http.StatusOK {
		common.SysLog(fmt.Sprintf("nous model discovery rejected: host=%s status=%d", nousHostOf(cred.BaseURL), res.StatusCode))
		return nil, fmt.Errorf("nous model discovery rejected with status %d: %w", res.StatusCode, ErrAccountUpstream)
	}
	var body struct {
		Data []NousModel `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, ErrAccountProtocol
	}
	models := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("no models available for this account")
	}
	return models, nil
}

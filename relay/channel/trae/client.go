// client.go Trae SOLO 上游客户端：llm_utils_chat / get_detail_param / ExchangeToken /
// checkin_credits / ide_user_ent_usage。移植自 _research/trae2api-web-main internal/upstream/client.go。
package trae

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client SOLO 上游 HTTP 客户端（按凭据构造；传输层全包共享）。
type Client struct {
	cred *Cred
}

// NewClient 以渠道凭据构造上游客户端。
func NewClient(cred *Cred) *Client {
	return &Client{cred: cred}
}

// Cred 返回客户端持有的凭据快照。
func (c *Client) Cred() *Cred { return c.cred }

var sharedTransport = &http.Transport{
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   20,
	IdleConnTimeout:       90 * time.Second,
	ResponseHeaderTimeout: 120 * time.Second,
}

var (
	jsonHTTP = &http.Client{Timeout: 120 * time.Second, Transport: sharedTransport}
	// streamHTTP SSE 流式专用：无总超时（首字节由 ResponseHeaderTimeout 兜底）。
	streamHTTP = &http.Client{Transport: sharedTransport}
)

// envBaseOverride 环境变量 TRAE_BASE_URL 全局兜底覆盖（dev/e2e 用，
// 与 OMNIROUTE_BASE_URL 同风格；生产留空）。
func envBaseOverride() string {
	return strings.TrimSuffix(strings.TrimSpace(os.Getenv("TRAE_BASE_URL")), "/")
}

func (c *Client) agentBase() string {
	if c.cred.BaseOverride != "" {
		return strings.TrimSuffix(c.cred.BaseOverride, "/")
	}
	if env := envBaseOverride(); env != "" {
		return env
	}
	return AgentHost
}

func (c *Client) ugBase() string {
	if c.cred.BaseOverride != "" {
		return strings.TrimSuffix(c.cred.BaseOverride, "/")
	}
	if env := envBaseOverride(); env != "" {
		return env
	}
	return UgHost
}

func (c *Client) oauthBase() string {
	if c.cred.BaseOverride != "" {
		return strings.TrimSuffix(c.cred.BaseOverride, "/")
	}
	if env := envBaseOverride(); env != "" {
		return env
	}
	if c.cred.ApiHost != "" {
		return c.cred.ApiHost
	}
	return OAuthHost
}

// doJSON 发请求并解 JSON；HTTP 非 2xx 时返回带分类的 *Error。
func (c *Client) doJSON(req *http.Request) (json.RawMessage, error) {
	resp, err := jsonHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		kind := Classify(resp.StatusCode, string(raw))
		return nil, &Error{Kind: kind, Status: resp.StatusCode, Msg: truncate(string(raw), 200)}
	}
	return raw, nil
}

// refreshTokenOnce 通过 ExchangeToken 轮换整族 token（注意：会踢同账号其他客户端下线）。
// 返回刷新后的新凭据（不改写入参）。
func (c *Client) refreshTokenOnce() (*Cred, error) {
	if strings.TrimSpace(c.cred.RefreshToken) == "" {
		return nil, fmt.Errorf("no refreshToken")
	}
	body := map[string]any{
		"ClientID":     ClientID,
		"RefreshToken": c.cred.RefreshToken,
		"ClientSecret": "-",
		"UserID":       "",
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, c.oauthBase()+EpExchange, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	OAuthHeaders(req)
	data, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result struct {
			Token               string `json:"Token"`
			TokenExpireAt       int64  `json:"TokenExpireAt"`
			TokenExpireDuration int64  `json:"TokenExpireDuration"`
			RefreshToken        string `json:"RefreshToken"`
			RefreshExpireAt     int64  `json:"RefreshExpireAt"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("exchange parse: %w", err)
	}
	if resp.Result.Token == "" {
		return nil, fmt.Errorf("refresh_failed: no token in response — re-login required")
	}
	next := *c.cred
	next.AccessToken = resp.Result.Token
	if resp.Result.RefreshToken != "" {
		next.RefreshToken = resp.Result.RefreshToken
	}
	if resp.Result.TokenExpireAt > 0 {
		next.ExpiresAt = NormalizeExpire(resp.Result.TokenExpireAt)
	} else if resp.Result.TokenExpireDuration > 0 {
		next.ExpiresAt = time.Now().Add(time.Duration(resp.Result.TokenExpireDuration) * time.Second).Unix()
	}
	return &next, nil
}

// NormalizeExpire TokenExpireAt 毫秒 → Unix 秒（>1e12 视为毫秒）。
func NormalizeExpire(v int64) int64 {
	if v > 1e12 {
		return v / 1000
	}
	return v
}

// ChatStream 发 llm_utils_chat 请求并返回原始 SSE body 流（调用方负责 Close）。
// ≥400 时返回已分类的 *Error + 原始 respBody。
func (c *Client) ChatStream(ctx context.Context, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.agentBase()+EpChat, bytes.NewReader(PrepareBody(body)))
	if err != nil {
		return nil, 0, nil, err
	}
	SOLOHeaders(req, c.cred, true)
	resp, err := streamHTTP.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		kind := Classify(resp.StatusCode, string(raw))
		return nil, resp.StatusCode, raw, &Error{Kind: kind, Status: resp.StatusCode, Msg: truncate(string(raw), 200)}
	}
	return resp.Body, resp.StatusCode, nil, nil
}

// ModelInfo 动态模型信息。
type ModelInfo struct {
	ID   string
	Name string
}

// FetchModels 拉 SOLO 模型表（get_detail_param）。
func (c *Client) FetchModels() ([]ModelInfo, error) {
	body := map[string]any{
		"function":            Function,
		"config_names":        nil,
		"need_prompt":         false,
		"current_config_info": nil,
		"poly_prompt":         true,
		"mode_type":           nil,
		"agent_type":          nil,
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, c.agentBase()+EpModels, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	SOLOHeaders(req, c.cred, false)
	data, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		ConfigInfoList []struct {
			ConfigName    string `json:"config_name"`
			DisplayConfig struct {
				DisplayName string `json:"display_name"`
			} `json:"display_config"`
		} `json:"config_info_list"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("models parse: %w", err)
	}
	out := make([]ModelInfo, 0, len(resp.ConfigInfoList))
	for _, cfg := range resp.ConfigInfoList {
		if cfg.ConfigName == "" {
			continue
		}
		out = append(out, ModelInfo{ID: cfg.ConfigName, Name: cfg.DisplayConfig.DisplayName})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("models api returned empty list")
	}
	return out, nil
}

// CheckinStatus 查询签到状态。
func (c *Client) CheckinStatus() (checkedIn bool, credits int64, enable bool, err error) {
	req, err := http.NewRequest(http.MethodPost, c.ugBase()+EpCheckinStatus, bytes.NewReader([]byte("{}")))
	if err != nil {
		return false, 0, false, err
	}
	UgHeaders(req, c.cred)
	data, err := c.doJSON(req)
	if err != nil {
		return false, 0, false, err
	}
	var resp struct {
		CheckedIn bool  `json:"checked_in"`
		Credits   int64 `json:"credits"`
		Enable    bool  `json:"enable"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return false, 0, false, fmt.Errorf("checkin status parse: %w", err)
	}
	return resp.CheckedIn, resp.Credits, resp.Enable, nil
}

// CheckinClaim 执行签到（每日 +200；高峰 9074 拥挤可重试）。
func (c *Client) CheckinClaim() error {
	req, err := http.NewRequest(http.MethodPost, c.ugBase()+EpCheckinClaim, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	UgHeaders(req, c.cred)
	_, err = c.doJSON(req)
	return err
}

// EntUsage 查询账号额度明细（remain/limit/used/packs）。
func (c *Client) EntUsage() (remain, limit, used int64, packs int, err error) {
	req, err := http.NewRequest(http.MethodPost, c.ugBase()+EpEntUsage, bytes.NewReader([]byte("{}")))
	if err != nil {
		return 0, 0, 0, 0, err
	}
	UgHeaders(req, c.cred)
	data, err := c.doJSON(req)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	var resp struct {
		UserEntitlementPackList []struct {
			EntitlementBaseInfo struct {
				Quota struct {
					CreditsLimit int64 `json:"credits_limit"`
				} `json:"quota"`
			} `json:"entitlement_base_info"`
			Usage struct {
				CreditsAmount float64 `json:"credits_amount"`
			} `json:"usage"`
		} `json:"user_entitlement_pack_list"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("ent usage parse: %w", err)
	}
	for _, p := range resp.UserEntitlementPackList {
		l := p.EntitlementBaseInfo.Quota.CreditsLimit
		if l <= 0 {
			continue
		}
		u := int64(p.Usage.CreditsAmount)
		limit += l
		used += u
		remain += l - u
		packs++
	}
	return remain, limit, used, packs, nil
}

// UserEntUsage 聚合剩余积分。
func (c *Client) UserEntUsage() (remain int64, err error) {
	remain, _, _, _, err = c.EntUsage()
	return remain, err
}

// GetUserInfo 查询账号信息（登录/导入补全用）。
func (c *Client) GetUserInfo() (uid, nickname, enterpriseID string, err error) {
	body := map[string]any{"ReqSource": "IDE", "IDEVersion": IdeVersion}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, c.oauthBase()+EpUserInfo, bytes.NewReader(raw))
	if err != nil {
		return "", "", "", err
	}
	OAuthHeaders(req)
	req.Header.Set("X-Cloudide-Token", c.cred.AccessToken)
	data, err := c.doJSON(req)
	if err != nil {
		return "", "", "", err
	}
	var resp struct {
		Result struct {
			UserID       string `json:"UserID"`
			ScreenName   string `json:"ScreenName"`
			EnterpriseID string `json:"EnterpriseID"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", "", "", fmt.Errorf("userinfo parse: %w", err)
	}
	return resp.Result.UserID, resp.Result.ScreenName, resp.Result.EnterpriseID, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

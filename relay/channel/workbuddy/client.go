// client.go WorkBuddy 四域上游 HTTP 客户端：
// chat(copilot.tencent.com) / billing(www.codebuddy.cn) / web(www.workbuddy.cn) /
// global(www.workbuddy.ai)。统一信封 {code,msg,data}，code!=0 即业务错误。
// 移植自 _research/workbuddy2api-panel-main internal/upstream/client.go（裁剪：
// 池化/文件持久化/global 探测目录/promotions 不搬，余额取聚合口径）。
package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// 四域默认 base。
const (
	defaultChatBaseCN    = "https://copilot.tencent.com"
	defaultBillingBaseCN = "https://www.codebuddy.cn"
	defaultWebBaseCN     = "https://www.workbuddy.cn"
	defaultGlobalBase    = "https://www.workbuddy.ai"
)

// 路径常量。
const (
	chatCompletionsPath = "/v2/chat/completions"
	refreshPath         = "/v2/plugin/auth/token/refresh"

	billingMeterPath   = "/billing/meter/get-user-resource"    // global 首选（无 /v2 前缀）
	dailyCheckinPath   = "/billing/meter/daily-checkin"        // global 首选
	billingMeterPathV2 = "/v2/billing/meter/get-user-resource" // CN 现状 / global fallback
	dailyCheckinPathV2 = "/v2/billing/meter/daily-checkin"
	reportPath         = "/v2/report"
)

// apiEnvelope 上游统一信封。
type apiEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// Client 上游 HTTP 客户端（按凭据构造；传输层全包共享）。
type Client struct {
	cred *Cred
}

// NewClient 以渠道凭据构造上游客户端。
func NewClient(cred *Cred) *Client {
	return &Client{cred: cred}
}

// Cred 返回客户端持有的凭据快照。
func (c *Client) Cred() *Cred { return c.cred }

// ---------------------------------------------------------------------------
// 传输层（共享连接池；真正禁 h2 / TLS 握手超时 / 短 keepalive 探测）。
// ---------------------------------------------------------------------------

var sharedTransport = newTransport()

var (
	jsonHTTP = &http.Client{Timeout: 120 * time.Second, Transport: sharedTransport}
	// chatHTTP 聊天 SSE 专用：无总时长上限，首字节由 ResponseHeaderTimeout 约束。
	chatHTTP = &http.Client{Timeout: 0, Transport: sharedTransport}
)

// chatIdleTimeout SSE 流中空闲监控阈值（活跃续命，静默掐流）。
const chatIdleTimeout = 90 * time.Second

// ---------------------------------------------------------------------------
// 域解析（BaseOverride 非空时四域同 host——mock 测试用）。
// 环境变量 WORKBUDDY_BASE_URL 为全局兜底覆盖（dev/e2e 用，与 OMNIROUTE_BASE_URL 同风格）。
// ---------------------------------------------------------------------------

func envBaseOverride() string {
	return strings.TrimSuffix(strings.TrimSpace(os.Getenv("WORKBUDDY_BASE_URL")), "/")
}

func (c *Client) resolveBase(prodCN, prodGlobal string) string {
	if c.cred.BaseOverride != "" {
		return strings.TrimSuffix(c.cred.BaseOverride, "/")
	}
	if env := envBaseOverride(); env != "" {
		return env
	}
	if c.cred.IsGlobal() {
		return prodGlobal
	}
	return prodCN
}

func (c *Client) chatBase() string {
	return c.resolveBase(defaultChatBaseCN, defaultGlobalBase)
}

func (c *Client) billingBase() string {
	return c.resolveBase(defaultBillingBaseCN, defaultGlobalBase)
}

func (c *Client) webBase() string {
	return c.resolveBase(defaultWebBaseCN, defaultGlobalBase)
}

// billingMeterPaths 按 realm 返回 billing/meter 域路径候选序列（global 404 时 fallback /v2）。
func (c *Client) billingMeterPaths() []string {
	if c.cred.IsGlobal() {
		return []string{billingMeterPath, billingMeterPathV2}
	}
	return []string{billingMeterPathV2}
}

// checkinMeterPaths 同上，针对 daily-checkin。
func (c *Client) checkinMeterPaths() []string {
	if c.cred.IsGlobal() {
		return []string{dailyCheckinPath, dailyCheckinPathV2}
	}
	return []string{dailyCheckinPathV2}
}

// ---------------------------------------------------------------------------
// 基础请求助手
// ---------------------------------------------------------------------------

// doJSON 发请求并解信封；HTTP 非 2xx 或业务 code != 0 时返回带分类的 *Error。
func (c *Client) doJSON(req *http.Request) (json.RawMessage, error) {
	resp, err := jsonHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		kind := Classify(resp.StatusCode, string(raw))
		return nil, &Error{Kind: kind, Status: resp.StatusCode, Msg: truncate(string(raw), 200)}
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse failed: %w (body: %s)", err, truncate(string(raw), 120))
	}
	if env.Code != 0 {
		kind := Classify(resp.StatusCode, env.Msg)
		if kind == ErrNone {
			kind = ErrClient
		}
		return nil, &Error{Kind: kind, Status: resp.StatusCode, Msg: fmt.Sprintf("code=%d msg=%s", env.Code, truncate(env.Msg, 160))}
	}
	return env.Data, nil
}

// billingJSON 发 billing 域请求并解信封。
func (c *Client) billingJSON(method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.billingBase()+path, rdr)
	if err != nil {
		return nil, err
	}
	BillingHeaders(req, c.cred)
	return c.doJSON(req)
}

// billingMeterJSON 仅对 /billing/meter 族端点按路径候选 fallback（仅 404 换路径）。
func (c *Client) billingMeterJSON(paths []string, method string, body any) (json.RawMessage, error) {
	var lastErr error
	for i, path := range paths {
		data, err := c.billingJSON(method, path, body)
		if err == nil {
			return data, nil
		}
		lastErr = err
		var ue *Error
		if !errors.As(err, &ue) || ue.Kind != ErrNotFound || i == len(paths)-1 {
			return nil, err
		}
	}
	return nil, lastErr
}

// growthJSON 发 growth 域请求（chatBase + BillingHeaders）并解信封。
func (c *Client) growthJSON(method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.chatBase()+path, rdr)
	if err != nil {
		return nil, err
	}
	BillingHeaders(req, c.cred)
	return c.doJSON(req)
}

// growthJSONMP 小程序口径 growth 请求（叠加 X-Client-Platform: miniprogram）。
func (c *Client) growthJSONMP(method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.chatBase()+path, rdr)
	if err != nil {
		return nil, err
	}
	BillingHeaders(req, c.cred)
	req.Header.Set("X-Client-Platform", "miniprogram")
	return c.doJSON(req)
}

// billingRetryDelay 维护类计费调用瞬时错误重试间隔基数。
var billingRetryDelay = 2 * time.Second

// isTransientBillingErr 报告 err 是否值得有界重试（5xx 或网络层错误）。
func isTransientBillingErr(err error) bool {
	if err == nil {
		return false
	}
	var ue *Error
	if errors.As(err, &ue) {
		return ue.Kind == ErrServer
	}
	return true
}

// retryBillingTransient 对签到/余额这类低频维护调用做瞬时错误有界重试（最多补 2 次）。
func (c *Client) retryBillingTransient(fn func() error) error {
	err := fn()
	if err == nil || !isTransientBillingErr(err) {
		return err
	}
	for i := 1; i <= 2; i++ {
		time.Sleep(time.Duration(i) * billingRetryDelay)
		if err = fn(); err == nil || !isTransientBillingErr(err) {
			return err
		}
	}
	return err
}

// ---------------------------------------------------------------------------
// Chat
// ---------------------------------------------------------------------------

// prepareBody 组装出站请求体（脱敏 + prompt_cache_key 注入）。
func (c *Client) prepareBody(body []byte, conversationID string) []byte {
	prepared := PrepareBody(body)
	if c.cred.IsGlobal() {
		prepared = ensureConsoleSystem(prepared)
	}
	return InjectPromptCacheKey(prepared, c.cred.UID, conversationID)
}

// ChatStream 发 chat 请求并返回原始 SSE body 流（调用方负责 Close）。
// 错误路径（≥400）返回已分类的 *Error（Kind 信封 + Retry-After 头解析）+ 原始 respBody。
func (c *Client) ChatStream(ctx context.Context, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prepared := c.prepareBody(body, "")
	endpoint := c.chatBase() + chatCompletionsPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(prepared))
	if err != nil {
		return nil, 0, nil, err
	}
	ChatHeaders(req, c.cred, ChatMeta{})
	resp, err := chatHTTP.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	if resp.StatusCode >= 400 {
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if rerr != nil {
			return nil, 0, nil, fmt.Errorf("read body: %w", rerr)
		}
		kind := Classify(resp.StatusCode, string(raw))
		if kind == ErrNone {
			return nil, resp.StatusCode, raw, nil
		}
		ue := &Error{Kind: kind, Status: resp.StatusCode, Msg: truncate(string(raw), 200)}
		if d, ok := ParseRetryAfter(resp.Header); ok {
			ue.RetryAfter = d
		}
		return nil, resp.StatusCode, raw, ue
	}
	return monitorBody(resp.Body, chatIdleTimeout, func() { resp.Body.Close() }), resp.StatusCode, nil, nil
}

// ---------------------------------------------------------------------------
// Token 刷新（两段式：锁内快照 → 锁外 I/O → 锁内校验写回；单飞见 refresh.go）
// ---------------------------------------------------------------------------

// refreshIOTimeout 刷新端点网络 I/O 上限。
const refreshIOTimeout = 30 * time.Second

// refreshTokenExpiresInMax refresh 响应 expiresIn 量级上限（10 年，脏值防御）。
const refreshTokenExpiresInMax = 10 * 365 * 24 * time.Hour

// refreshTokenOnce 单次刷新本体（调用方 RefreshCred 已保证同渠道串行）。
// 返回刷新后的新凭据（不改写入参）。
func (c *Client) refreshTokenOnce() (*Cred, error) {
	rtSnapshot := c.cred.RefreshToken
	if strings.TrimSpace(rtSnapshot) == "" {
		return nil, fmt.Errorf("no refreshToken")
	}
	endpoint := c.chatBase() + refreshPath
	ctx, cancel := context.WithTimeout(context.Background(), refreshIOTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	RefreshHeaders(req, c.cred)
	data, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var tok struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if err := json.Unmarshal(data, &tok); err != nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("refresh_failed: no accessToken in response — re-login required")
	}
	next := *c.cred
	next.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		next.RefreshToken = tok.RefreshToken
	}
	if tok.Domain != "" {
		next.Domain = tok.Domain
	}
	// preserveExpiry：响应缺 expiresIn / 超量级脏值时保留旧过期时间。
	if tok.ExpiresIn > 0 && time.Duration(tok.ExpiresIn)*time.Second < refreshTokenExpiresInMax {
		next.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix()
	}
	return &next, nil
}

// ---------------------------------------------------------------------------
// 余额 / 签到
// ---------------------------------------------------------------------------

// packageEndLayout 上游套餐到期时间墙钟格式（UTC+8）。
const packageEndLayout = "2006-01-02 15:04:05"

// UserResource 查询账号积分余额（remain 负值钳 0；企业版走企业口径端点）。
func (c *Client) UserResource() (remain, total int64, err error) {
	if c.cred.IsEnterprise() {
		return c.enterpriseResource()
	}
	now := time.Now()
	body := map[string]any{
		"PageNumber":               1,
		"PageSize":                 100,
		"ProductCode":              "p_tcaca",
		"Status":                   []int{0, 3},
		"PackageEndTimeRangeBegin": now.Format(packageEndLayout),
		"PackageEndTimeRangeEnd":   now.Add(365 * 101 * 24 * time.Hour).Format(packageEndLayout),
	}
	data, err := c.retryBillingTransientData(func() (json.RawMessage, error) {
		return c.billingMeterJSON(c.billingMeterPaths(), http.MethodPost, body)
	})
	if err != nil {
		return 0, 0, err
	}
	var resp struct {
		Response struct {
			Data struct {
				Accounts []struct {
					CapacitySize        int64 `json:"CapacitySize"`
					CapacityRemain      int64 `json:"CapacityRemain"`
					CycleCapacitySize   int64 `json:"CycleCapacitySize"`
					CycleCapacityRemain int64 `json:"CycleCapacityRemain"`
				} `json:"Accounts"`
			} `json:"Data"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, 0, fmt.Errorf("resource parse: %w", err)
	}
	for _, acct := range resp.Response.Data.Accounts {
		r, size := acct.CapacityRemain, acct.CapacitySize
		if acct.CycleCapacitySize > 0 {
			r, size = acct.CycleCapacityRemain, acct.CycleCapacitySize
		}
		if r < 0 {
			r = 0
		}
		if size < r {
			size = r
		}
		remain += r
		total += size
	}
	return remain, total, nil
}

// enterpriseResource 企业版分配额度（credit=已用，limitNum=额度；remain=limit-used）。
func (c *Client) enterpriseResource() (remain, total int64, err error) {
	data, err := c.retryBillingTransientData(func() (json.RawMessage, error) {
		return c.billingJSON(http.MethodPost, "/v2/billing/meter/get-enterprise-user-usage", map[string]any{})
	})
	if err != nil {
		return 0, 0, err
	}
	var resp struct {
		Credit        float64 `json:"credit"`
		LimitNum      int64   `json:"limitNum"`
		LimitNumSnake int64   `json:"limit_num"`
		UsedNum       float64 `json:"used_num"`
	}
	if uerr := json.Unmarshal(data, &resp); uerr != nil {
		return 0, 0, fmt.Errorf("enterprise resource parse: %w", uerr)
	}
	limit := resp.LimitNum
	if limit == 0 && resp.LimitNumSnake != 0 {
		limit = resp.LimitNumSnake
	}
	used := resp.Credit
	if used == 0 && resp.UsedNum != 0 {
		used = resp.UsedNum
	}
	if limit < 0 { // 不限量
		return 1 << 40, -1, nil
	}
	usedInt := int64(used)
	if used-float64(usedInt) >= 0.5 {
		usedInt++
	}
	remain = limit - usedInt
	if remain < 0 {
		remain = 0
	}
	return remain, limit, nil
}

// retryBillingTransientData retryBillingTransient 的带返回值版本。
func (c *Client) retryBillingTransientData(fn func() (json.RawMessage, error)) (json.RawMessage, error) {
	data, err := fn()
	if err == nil || !isTransientBillingErr(err) {
		return data, err
	}
	for i := 1; i <= 2; i++ {
		time.Sleep(time.Duration(i) * billingRetryDelay)
		if data, err = fn(); err == nil || !isTransientBillingErr(err) {
			return data, err
		}
	}
	return data, err
}

// DailyCheckin 执行每日签到。「已签到」（业务 code 非 0 + already 文案）按幂等成功处理。
func (c *Client) DailyCheckin() error {
	return c.retryBillingTransient(func() error {
		_, err := c.billingMeterJSON(c.checkinMeterPaths(), http.MethodPost, map[string]any{})
		return err
	})
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

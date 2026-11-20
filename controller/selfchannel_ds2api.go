package controller

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/raccoon"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const ds2LoginTTL = 5 * time.Minute
const ds2LoginCookie = "ds2api_login"

type ds2LoginOptions struct {
	Identity    model.AuthSessionIdentity `json:"identity"`
	CSRF        string                    `json:"csrf"`
	Proxy       string                    `json:"proxy"`
	ProxyPolicy *dto.ChannelProxyPolicy   `json:"proxy_policy,omitempty"`
	Models      []string                  `json:"models,omitempty"`
	Public      bool                      `json:"public"`
	Gateway     string                    `json:"gateway"`
	EgressKey   string                    `json:"egress_key"`
}

// The compatibility route name contains oauth, but the protocol is password login,
// not OAuth. The upstream project has no OAuth authorize/callback implementation.
func SelfDs2ApiOAuthStart(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req ds2LoginOptions
	if c.Request.ContentLength > 0 && c.ShouldBindJSON(&req) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	identity, authenticated := middleware.GetSessionAuthIdentity(c)
	_, isPAT := model.ParseAccessTokenSessionID(identity.SessionID)
	if !authenticated || isPAT || c.GetBool("use_access_token") || identity.UserID != c.GetInt("id") {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	req.Identity = model.AuthSessionIdentity{UserID: identity.UserID, SessionID: identity.SessionID, UserAuthVersion: identity.UserAuthVersion, SessionVersion: identity.SessionVersion}
	if err := model.DB.Transaction(func(tx *gorm.DB) error { return model.ValidateAuthSessionWithTx(tx, req.Identity) }); err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	origin, err := url.Parse(strings.TrimRight(system_setting.ServerAddress, "/"))
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || (origin.Path != "" && origin.Path != "/") || origin.Fragment != "" || (origin.Scheme != "https" && !(origin.Scheme == "http" && (origin.Hostname() == "localhost" || origin.Hostname() == "127.0.0.1"))) {
		common.ApiErrorMsg(c, "Configure an HTTPS server address before starting account login")
		return
	}
	gateway := strings.TrimRight(strings.TrimSpace(os.Getenv("DS2API_BASE_URL")), "/")
	upstream, err := url.Parse(gateway)
	if err != nil || upstream.Host == "" || upstream.User != nil || upstream.Scheme != "https" || upstream.RawQuery != "" || upstream.Fragment != "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "code": "DS2API_NOT_CONFIGURED", "message": "DS2API gateway is not configured"})
		return
	}
	if !checkSelfChannelLimit(c, 1) {
		return
	}
	if err := model.ValidateSelfChannelProxyPolicy(c.GetInt("id"), req.ProxyPolicy, req.Proxy); err != nil {
		common.ApiError(c, err)
		return
	}
	state, err := raccoon.NewState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	csrf, err := raccoon.NewState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	req.CSRF, req.Gateway, req.EgressKey = csrf, gateway, "ds2api:"+ds2SessionKey(state)
	raw, err := common.Marshal(req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	encrypted, err := common.EncryptSecret(string(raw))
	if err != nil || !common.IsEncryptedSecret(encrypted) {
		common.ApiErrorMsg(c, "Unable to securely store account login session")
		return
	}
	session := &model.SelfOAuthSession{State: ds2SessionKey(state), Provider: "ds2api", Status: "pending", Phone: strconv.Itoa(c.GetInt("id")), Refresh: encrypted}
	if err := model.DB.Create(session).Error; err != nil {
		common.ApiErrorMsg(c, "Unable to create account login session")
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(ds2LoginCookie, state, int(ds2LoginTTL.Seconds()), "/api/user/auth/ds2api", "", origin.Scheme == "https", true)
	common.ApiSuccess(c, gin.H{"sessionId": state, "authUrl": origin.Scheme + "://" + origin.Host + "/api/user/auth/ds2api/login?state=" + url.QueryEscape(state), "expiresIn": int(ds2LoginTTL.Seconds()), "flow": "account_login", "upstream_oauth": false})
}

func ds2SessionKey(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

func ds2BrowserSession(c *gin.Context) (*model.SelfOAuthSession, *ds2LoginOptions, bool) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	state := c.Query("state")
	if c.Request.Method == http.MethodPost {
		state = c.PostForm("state")
	}
	cookie, err := c.Cookie(ds2LoginCookie)
	if err != nil || len(state) != 64 || subtle.ConstantTimeCompare([]byte(cookie), []byte(state)) != 1 {
		c.String(http.StatusForbidden, "Invalid login session. Start again from the account binding page.")
		return nil, nil, false
	}
	var session model.SelfOAuthSession
	if err := model.DB.Where("state = ? AND provider = ? AND status = ? AND created_at > ?", ds2SessionKey(state), "ds2api", "pending", time.Now().Add(-ds2LoginTTL)).First(&session).Error; err != nil {
		c.String(http.StatusGone, "Account login session expired or already used. Start again.")
		return nil, nil, false
	}
	var options ds2LoginOptions
	if !common.IsEncryptedSecret(session.Refresh) || common.UnmarshalJsonStr(common.DecryptSecretOrLegacy(session.Refresh), &options) != nil {
		c.String(http.StatusGone, "Account login session is unavailable. Start again.")
		return nil, nil, false
	}
	refresh, cookieErr := c.Cookie(service.RefreshCookieName)
	if cookieErr != nil || strconv.Itoa(options.Identity.UserID) != session.Phone || service.ValidateDS2BrowserSession(refresh, options.Identity) != nil {
		c.String(http.StatusForbidden, "Account login requires the original dashboard session. Start again.")
		return nil, nil, false
	}
	return &session, &options, true
}

var ds2LoginPage = template.Must(template.New("ds2-login").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>连接 DeepSeek 账号</title><style>body{font-family:system-ui;max-width:38rem;margin:3rem auto;padding:1rem}.verify{background:#fff8e6;border:1px solid #f0c36d;border-radius:8px;padding:1rem;margin:1rem 0}.verify a{color:#0066cc;font-weight:600}button{padding:.5rem 1.5rem;font-size:1rem;cursor:pointer}input{width:100%;padding:.5rem;margin:.25rem 0;box-sizing:border-box}label{display:block;margin:.5rem 0 .25rem}</style></head><body><h1>连接 DeepSeek 账号</h1><p>这是本平台的账号登录页面，不是 DeepSeek OAuth 授权。平台将通过 DS2API 上游使用的登录接口验证你的账号，并安全保存返回的会话 token；不会要求 F12 或浏览器脚本。请仅使用你自己的账号。</p><div class="verify"><p><strong>如果上次登录提示"需要验证"：</strong></p><p>请先 <a href="https://chat.deepseek.com" target="_blank" rel="noopener noreferrer">打开 DeepSeek 官网</a> 手动登录一次完成验证（滑块/邮箱/手机验证码），验证成功后回到此页面重新提交。</p></div><p>只有你明确勾选公开，渠道才会进入公共池。上游若要求验证码或风控验证，本平台不能代为绕过。密码不落库。</p><form method="post" action="/api/user/auth/ds2api/credentials"><input type="hidden" name="state" value="{{.State}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><p><label>邮箱或手机号 <input name="account" type="text" autocomplete="username" maxlength="320" required></label></p><p><label>密码 <input name="password" type="password" autocomplete="current-password" maxlength="1024" required></label></p><button type="submit">登录并连接</button></form></body></html>`))

func SelfDs2ApiOAuthAuthorizePage(c *gin.Context) {
	_, options, ok := ds2BrowserSession(c)
	if !ok {
		return
	}
	// Native form POSTs use an opaque Origin under no-referrer. Preserve the
	// origin for CSRF validation without disclosing the path or state query.
	c.Header("Referrer-Policy", "strict-origin")
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := ds2LoginPage.Execute(c.Writer, map[string]string{"State": c.Query("state"), "CSRF": options.CSRF}); err != nil {
		c.Abort()
	}
}

func SelfDs2ApiOAuthCallback(c *gin.Context) {
	if c.Request.Method != http.MethodPost {
		c.Header("Allow", http.MethodPost)
		c.String(http.StatusMethodNotAllowed, "Use the account login form. Tokens are not accepted in URLs.")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.Request.ParseForm(); err != nil {
		c.String(http.StatusBadRequest, "Invalid login request")
		return
	}
	session, options, ok := ds2BrowserSession(c)
	if !ok {
		return
	}
	if subtle.ConstantTimeCompare([]byte(c.PostForm("csrf")), []byte(options.CSRF)) != 1 {
		c.String(http.StatusForbidden, "Invalid CSRF token")
		return
	}
	origin, err := url.Parse(system_setting.ServerAddress)
	if err != nil || c.GetHeader("Origin") != origin.Scheme+"://"+origin.Host {
		c.String(http.StatusForbidden, "Invalid login request origin")
		return
	}
	// Atomically claim before sending credentials; retries require a fresh session.
	claimed := model.DB.Model(&model.SelfOAuthSession{}).Where("state = ? AND provider = ? AND status = ? AND created_at > ?", session.State, "ds2api", "pending", time.Now().Add(-ds2LoginTTL)).Update("status", "exchanging")
	if claimed.Error != nil || claimed.RowsAffected != 1 {
		c.String(http.StatusConflict, "Account login session already used")
		return
	}
	uid, _ := strconv.Atoi(session.Phone)
	proxy, err := model.ResolveStableChannelProxy(uid, 0, options.EgressKey, dto.ChannelSettings{Proxy: options.Proxy, ProxyPolicy: options.ProxyPolicy})
	if err != nil {
		failDS2Session(c, session.State, "Configured proxy is unavailable")
		return
	}
	token, err := service.LoginDS2Account(c.Request.Context(), c.PostForm("account"), c.PostForm("password"), proxy)
	if err != nil {
		failDS2Session(c, session.State, err.Error())
		return
	}
	catalog, verified, _ := FetchLiveFreeModelsForUpstream(options.Gateway, "ds2api", token, proxy)
	if !verified || len(catalog) == 0 {
		failDS2Session(c, session.State, "DS2API gateway model discovery failed. No channel was created")
		return
	}
	selected := mergeModelCatalogs(options.Models)
	if len(selected) == 0 {
		selected = catalog
	}
	allowed := make(map[string]bool, len(catalog))
	for _, id := range catalog {
		allowed[id] = true
	}
	for _, id := range selected {
		if !allowed[id] {
			failDS2Session(c, session.State, "Selected model is not available from DS2API")
			return
		}
	}
	encrypted, err := common.EncryptSecret(token)
	if err != nil || !common.IsEncryptedSecret(encrypted) {
		failDS2Session(c, session.State, "Unable to securely store account credential")
		return
	}
	needsBinding := options.ProxyPolicy == nil && strings.TrimSpace(options.Proxy) == "" || options.Proxy == "userpool" || options.ProxyPolicy != nil && options.ProxyPolicy.Mode != "direct" && options.ProxyPolicy.Mode != "fixed"
	// Pool channels remain disabled until the post-commit shared alias is ready.
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Name: "DS2API", Key: encrypted, BaseURL: &options.Gateway, OwnerId: uid, Status: common.ChannelStatusEnabled, Group: "default", Models: strings.Join(selected, ","), CreatedTime: common.GetTimestamp(), Visibility: 0}
	if options.Public {
		channel.Visibility = 1
	}
	if needsBinding {
		channel.Status = common.ChannelStatusAutoDisabled
	}
	channel.SetSetting(dto.ChannelSettings{Proxy: options.Proxy, ProxyPolicy: options.ProxyPolicy})
	channel.SetOtherInfo(map[string]any{"templateId": "ds2api", "poolKey": "ds2api", "allModels": catalog})
	binding := &model.SelfAccountBinding{UserId: uid, Provider: "ds2api", Kind: "token", DisplayName: "DS2API", Status: "ready", Public: channel.Visibility, Proxy: options.Proxy, ProxyPolicy: options.ProxyPolicy}
	if needsBinding {
		binding.Status = "pending"
	}
	// OpenAI keys use the existing gateway key storage contract. The binding and
	// transient login credential are encrypted; the password is never persisted.
	if err := binding.EncodeAccounts([]model.SelfAccountCred{{Account: strings.TrimSpace(c.PostForm("account")), Password: token}}); err != nil {
		failDS2Session(c, session.State, "Unable to securely store account credential")
		return
	}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.ValidateAuthSessionWithTx(tx, options.Identity); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.Channel{}).Where("owner_id = ?", uid).Count(&count).Error; err != nil {
			return err
		}
		limit := operation_setting.SelfChannelMaxPerUser
		if limit <= 0 {
			limit = 20
		}
		if count >= int64(limit) {
			return fmt.Errorf("self channel limit reached")
		}
		if err := tx.Create(channel).Error; err != nil {
			return err
		}
		if err := channel.AddAbilities(tx); err != nil {
			return err
		}
		binding.ChannelId = channel.Id
		if err := tx.Create(binding).Error; err != nil {
			return err
		}
		status, refresh := "done", ""
		if needsBinding {
			status, refresh = "binding", session.Refresh
		}
		result := tx.Model(&model.SelfOAuthSession{}).Where("state = ? AND status = ? AND provider = ?", session.State, "exchanging", "ds2api").Updates(map[string]any{"status": status, "access": "", "account_id": strconv.Itoa(channel.Id), "refresh": refresh, "err": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("login completion conflict")
		}
		return nil
	})
	if err != nil {
		failDS2Session(c, session.State, "Unable to create account channel. Start again")
		return
	}
	if needsBinding {
		err = model.BindChannelProxyAccount(uid, channel.Id, options.EgressKey)
		if err == nil {
			err = model.DB.Transaction(func(tx *gorm.DB) error {
				if err := model.ValidateAuthSessionWithTx(tx, options.Identity); err != nil {
					return err
				}
				result := tx.Model(&model.SelfOAuthSession{}).Where("state = ? AND provider = ? AND status = ? AND created_at > ?", session.State, "ds2api", "binding", time.Now().Add(-ds2LoginTTL)).Updates(map[string]any{"status": "done", "refresh": ""})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf("login completion conflict")
				}
				activation := tx.Model(&model.Channel{}).Where("id = ? AND owner_id = ? AND status = ?", channel.Id, uid, common.ChannelStatusAutoDisabled).Update("status", common.ChannelStatusEnabled)
				if activation.Error != nil {
					return activation.Error
				}
				if activation.RowsAffected != 1 {
					return fmt.Errorf("channel activation conflict")
				}
				if err := tx.Model(binding).Update("status", "ready").Error; err != nil {
					return err
				}
				return tx.Model(&model.Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", true).Error
			})
		}
		if err != nil {
			failDS2Session(c, session.State, "Configured proxy is unavailable. The channel is disabled")
			model.InitChannelCache()
			return
		}
	}
	model.InitChannelCache()
	c.SetCookie(ds2LoginCookie, "", -1, "/api/user/auth/ds2api", "", origin.Scheme == "https", true)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=/my-channels"></head><body><h2>账号登录成功，渠道已连接。</h2><p><a href="/my-channels">返回我的渠道</a>。会话凭据不会显示在浏览器中。</p></body></html>`)
}

func failDS2Session(c *gin.Context, state, message string) {
	_ = model.DB.Model(&model.SelfOAuthSession{}).Where("state = ? AND provider = ? AND status IN ?", state, "ds2api", []string{"exchanging", "binding"}).Updates(map[string]any{"status": "failed", "err": message, "access": "", "refresh": ""}).Error
	// biz_code 11（需要官网验证）给用户一个验证后重试的引导页，而不是纯文本错误。
	if strings.Contains(message, "requires verification") || strings.Contains(message, "验证") {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusBadGateway, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>需要验证</title><style>body{font-family:system-ui;max-width:38rem;margin:3rem auto;padding:1rem}.warn{background:#fff8e6;border:1px solid #f0c36d;border-radius:8px;padding:1rem;margin:1rem 0}a{color:#0066cc}</style></head><body><h1>DeepSeek 账号需要验证</h1><div class="warn"><p>DeepSeek 要求此账号先在官网完成验证（滑块/邮箱/手机验证码）。</p><p><strong>步骤：</strong></p><ol><li><a href="https://chat.deepseek.com" target="_blank" rel="noopener noreferrer">打开 DeepSeek 官网</a>，用你的账号登录并完成验证</li><li>验证成功后，回到本平台重新开始授权（之前的会话已过期）</li></ol></div><p><a href="/my-channels">返回我的渠道</a></p></body></html>`)
		return
	}
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(http.StatusBadGateway, message)
}

func SelfDs2ApiOAuthPoll(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	state := c.Query("sessionId")
	if len(state) != 64 || c.GetInt("id") <= 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	var session model.SelfOAuthSession
	if err := model.DB.Where("state = ? AND provider = ? AND phone = ? AND created_at > ?", ds2SessionKey(state), "ds2api", strconv.Itoa(c.GetInt("id")), time.Now().Add(-ds2LoginTTL)).First(&session).Error; err != nil {
		common.ApiErrorMsg(c, "Account login session not found or expired")
		return
	}
	if session.Status == "cancelled" {
		common.ApiErrorMsg(c, "Account login was cancelled. Start again")
		return
	}
	if session.Status == "failed" {
		common.ApiErrorMsg(c, session.Err)
		return
	}
	if session.Status != "done" {
		common.ApiSuccess(c, gin.H{"done": false, "flow": "account_login"})
		return
	}
	id, _ := strconv.Atoi(session.AccountID)
	channel, ok := loadSelfOwnedChannel(c, id, false)
	if !ok {
		return
	}
	// Completion polling is idempotent. It cannot create duplicate channels.
	sanitizeSelfChannel(channel)
	common.ApiSuccess(c, gin.H{"done": true, "channel": channel, "models": strings.Split(channel.Models, ","), "flow": "account_login"})
}

// SelfDs2ApiOAuthCancel invalidates pending/exchanging state across all nodes. A
// racing completion must still match exchanging inside its channel transaction.
func SelfDs2ApiOAuthCancel(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	_, pat := model.ParseAccessTokenSessionID(identity.SessionID)
	if !ok || pat || c.GetBool("use_access_token") || identity.UserID != c.GetInt("id") {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	if err := http.NewCrossOriginProtection().Check(c.Request); err != nil {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if c.ShouldBindJSON(&req) != nil || len(req.SessionID) != 64 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.ValidateAuthSessionWithTx(tx, identity); err != nil {
			return err
		}
		var flow model.SelfOAuthSession
		if err := tx.Where("state = ? AND provider = ? AND phone = ?", ds2SessionKey(req.SessionID), "ds2api", strconv.Itoa(identity.UserID)).First(&flow).Error; err != nil {
			return err
		}
		if flow.Status == "cancelled" {
			return nil
		}
		var options ds2LoginOptions
		if common.UnmarshalJsonStr(common.DecryptSecretOrLegacy(flow.Refresh), &options) != nil || options.Identity != identity {
			return fmt.Errorf("invalid session")
		}
		updated := tx.Model(&model.SelfOAuthSession{}).Where("state = ? AND status IN ?", flow.State, []string{"pending", "exchanging", "binding"}).Updates(map[string]any{"status": "cancelled", "access": "", "refresh": "", "err": ""})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("invalid session")
		}
		return nil
	})
	if err != nil {
		common.ApiErrorMsg(c, "Account login session not found or expired")
		return
	}
	common.ApiSuccess(c, gin.H{"status": "cancelled"})
}

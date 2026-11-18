package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/trae"
	"github.com/QuantumNous/new-api/relay/channel/workbuddy"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func accountAuthorizationError(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, "ACCOUNT_AUTH_FAILED", "Account authorization could not be completed; retry or restart authorization"
	switch {
	case errors.Is(err, model.ErrAuthFlowInvalid):
		status, code, message = http.StatusBadRequest, "ACCOUNT_FLOW_INVALID", "Account authorization session is invalid"
	case errors.Is(err, model.ErrAuthFlowExpired):
		status, code, message = http.StatusGone, "ACCOUNT_FLOW_EXPIRED", "Account authorization expired; restart authorization"
	case errors.Is(err, model.ErrAuthFlowConsumed):
		status, code, message = http.StatusConflict, "ACCOUNT_FLOW_CONSUMED", "Account authorization was already consumed or cancelled"
	case errors.Is(err, model.ErrUserSessionInactive):
		status, code, message = http.StatusUnauthorized, "ACCOUNT_SESSION_REVOKED", "Dashboard login session is no longer active"
	case errors.Is(err, model.ErrWorkBuddyAuthLimit):
		status, code, message = http.StatusTooManyRequests, "ACCOUNT_AUTH_LIMIT", "Account authorization or channel limit reached"
	case errors.Is(err, model.ErrWorkBuddyEncryptionRequired):
		status, code, message = http.StatusServiceUnavailable, "ACCOUNT_ENCRYPTION_REQUIRED", "Account authorization requires encrypted credential storage"
	case errors.Is(err, service.ErrAccountPollEarly):
		status, code, message = http.StatusTooManyRequests, "ACCOUNT_POLL_EARLY", "Wait before polling authorization again"
		c.Header("Retry-After", "2")
	case errors.Is(err, service.ErrAccountEgress):
		status, code, message = http.StatusServiceUnavailable, "ACCOUNT_PROXY_UNAVAILABLE", "Account proxy changed or is unavailable; restart authorization"
	case errors.Is(err, service.ErrAccountUpstream), errors.Is(err, service.ErrAccountProtocol):
		status, code, message = http.StatusBadGateway, "ACCOUNT_UPSTREAM_FAILED", "Account provider request failed; retry or restart authorization"
	}
	// Static context only: never log transport errors, upstream state, or credentials.
	common.SysLog("account authorization rejected: " + code)
	c.JSON(status, gin.H{"success": false, "code": code, "message": common.NewMessage(message)})
}

func accountAuthorizationIdentity(c *gin.Context) (model.AuthSessionIdentity, bool) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Header("Referrer-Policy", "no-referrer")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	_, pat := model.ParseAccessTokenSessionID(identity.SessionID)
	if !ok || pat || c.GetBool("use_access_token") {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": common.NewMessage("A dashboard login session is required for account authorization")})
		return model.AuthSessionIdentity{}, false
	}
	guard := http.NewCrossOriginProtection()
	for _, origin := range common.SessionCookieTrustedURLs {
		if err := guard.AddTrustedOrigin(origin); err != nil {
			accountAuthorizationError(c, service.ErrAccountEgress)
			return model.AuthSessionIdentity{}, false
		}
	}
	// Poll is a legacy GET that can finalize a binding: explicitly guard it too.
	// Fetch metadata or a same-origin Origin is required, otherwise AJAX opt-in.
	fetchSite := c.GetHeader("Sec-Fetch-Site")
	rejected := fetchSite != "" && fetchSite != "same-origin"
	if c.GetHeader("Origin") == "" && fetchSite == "" && c.GetHeader("X-Account-Action") != "authorize" {
		rejected = true
	}
	probe := c.Request.Clone(c.Request.Context())
	probe.Method = http.MethodPost
	if rejected || guard.Check(probe) != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": common.NewMessage("Account authorization origin was rejected")})
		return model.AuthSessionIdentity{}, false
	}
	if c.Request.Method == http.MethodPost {
		media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || media != "application/json" {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": common.NewMessage("Account authorization requires a JSON request")})
			return model.AuthSessionIdentity{}, false
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	}
	if _, _, err := service.ValidateLoginSession(identity); err != nil {
		accountAuthorizationError(c, model.ErrUserSessionInactive)
		return model.AuthSessionIdentity{}, false
	}
	return model.AuthSessionIdentity{UserID: identity.UserID, SessionID: identity.SessionID, UserAuthVersion: identity.UserAuthVersion, SessionVersion: identity.SessionVersion}, true
}

func selfWorkBuddyOAuthStart(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req struct {
		Type        string                  `json:"type"`
		Realm       string                  `json:"realm"`
		DisplayName string                  `json:"displayName"`
		Public      bool                    `json:"public"`
		Proxy       string                  `json:"proxy"`
		ProxyPolicy *dto.ChannelProxyPolicy `json:"proxyPolicy"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	if strings.ToLower(strings.TrimSpace(req.Type)) != "workbuddy" {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	realm := strings.ToLower(strings.TrimSpace(req.Realm))
	if realm == "" {
		realm = "cn"
	}
	if len(req.DisplayName) > 128 {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	policy := req.ProxyPolicy
	if policy == nil {
		policy = &dto.ChannelProxyPolicy{Mode: "user_pool"}
		if req.Proxy != "" {
			policy.Mode = "fixed"
		}
	}
	token, authURL, err := StartWorkBuddyAuthorization(c.Request.Context(), identity, realm, strings.TrimSpace(req.DisplayName), req.Public, dto.ChannelSettings{Proxy: req.Proxy, ProxyPolicy: policy})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"sessionId": token, "authUrl": authURL, "realm": realm, "expiresIn": 600, "interval": 2}})
}

func selfWorkBuddyOAuthPoll(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	token := strings.TrimSpace(c.Query("sessionId"))
	channel, err := PollAccountAuthorization(c.Request.Context(), identity, token)
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if channel == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": false, "interval": 2}})
		return
	}
	sanitizeSelfChannel(channel)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": true, "channel": channel}})
}

// SelfOAuthCancel invalidates only the initiating owner's dashboard ceremony.
func SelfOAuthCancel(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	if err := CancelAccountAuthorization(identity, strings.TrimSpace(req.SessionID)); err != nil {
		accountAuthorizationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"cancelled": true}})
}

func selfTraeCredentialImport(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req struct {
		CallbackURL     string                  `json:"callbackUrl"`
		RefreshToken    string                  `json:"refreshToken"`
		UID             string                  `json:"uid"`
		MachineID       string                  `json:"machineId"`
		DeviceID        string                  `json:"deviceId"`
		Mode            string                  `json:"mode"`
		DisplayName     string                  `json:"displayName"`
		Public          bool                    `json:"public"`
		ConfirmRotation bool                    `json:"confirmRotation"`
		Proxy           string                  `json:"proxy"`
		ProxyPolicy     *dto.ChannelProxyPolicy `json:"proxyPolicy"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	if len(req.DisplayName) > 128 || req.Mode != "" && req.Mode != "preview" && req.Mode != "create" {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	if req.RefreshToken != "" && !req.ConfirmRotation || req.CallbackURL != "" && !req.ConfirmRotation {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "TRAE_ROTATION_CONFIRMATION_REQUIRED", "message": common.NewMessage("Trae token rotation may sign out other clients; confirm before importing")})
		return
	}
	policy := req.ProxyPolicy
	if policy == nil {
		policy = &dto.ChannelProxyPolicy{Mode: "user_pool"}
		if req.Proxy != "" {
			policy.Mode = "fixed"
		}
	}
	token, err := ImportTraeAuthorization(c.Request.Context(), identity, strings.TrimSpace(req.CallbackURL), strings.TrimSpace(req.RefreshToken), strings.TrimSpace(req.UID), req.MachineID, req.DeviceID, strings.TrimSpace(req.DisplayName), req.Public, req.ConfirmRotation, dto.ChannelSettings{Proxy: req.Proxy, ProxyPolicy: policy})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if req.Mode != "create" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": false, "ready": true, "sessionId": token, "expiresIn": 600, "browserLoginAvailable": false, "notice": "Trae credentials imported securely. Automatic browser callback requires an official local client bridge; tokens are never returned."}})
		return
	}
	channel, err := PollAccountAuthorization(c.Request.Context(), identity, token)
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	sanitizeSelfChannel(channel)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": true, "sessionId": token, "channel": channel}})
}

func workBuddyAuthRequest(ctx context.Context, state *model.WorkBuddyAuthState, method, path, bearer string) (map[string]any, error) {
	base, origin := "https://copilot.tencent.com", "https://www.codebuddy.cn"
	if state.Realm == "global" {
		base, origin = "https://www.workbuddy.ai", "https://www.workbuddy.ai"
	}
	var reader io.Reader
	if method == http.MethodPost {
		reader = strings.NewReader("{}")
	}
	request, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		return nil, service.ErrAccountProtocol
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	request.Header.Set("Origin", origin)
	request.Header.Set("Referer", origin+"/")
	request.Header.Set("User-Agent", "CLI/2.63.2 CodeBuddy/2.63.2")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	var response struct {
		Code *int           `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := service.AccountAuthorizationJSON(ctx, state.Proxy, request, &response); err != nil {
		return nil, err
	}
	if response.Code == nil {
		return nil, service.ErrAccountProtocol
	}
	if *response.Code != 0 {
		// The existing official device protocol identifies pending as "login ing".
		// No network, HTTP, malformed JSON or other business error is pending.
		if strings.HasPrefix(path, "/v2/plugin/auth/token?") && strings.EqualFold(strings.TrimSpace(response.Msg), "login ing") {
			return nil, nil
		}
		return nil, service.ErrAccountUpstream
	}
	if response.Data == nil {
		return nil, service.ErrAccountProtocol
	}
	return response.Data, nil
}

func newAccountFlow(identity model.AuthSessionIdentity, provider, realm, name string, public bool, settings dto.ChannelSettings, models []string) (string, error) {
	opaque := make([]byte, 32)
	_, err := rand.Read(opaque)
	if err != nil {
		return "", err
	}
	// Non-secret random egress identity; independent of upstream state/token.
	egress := provider + ":" + realm + ":" + common.GenerateHMACWithKey(opaque, "account-egress")
	proxy, err := model.ResolveStableChannelProxy(identity.UserID, 0, egress, settings)
	if err != nil {
		return "", service.ErrAccountEgress
	}
	endpointID, endpointFingerprint := 0, ""
	if settings.ProxyPolicy != nil && settings.ProxyPolicy.Mode != "direct" && settings.ProxyPolicy.Mode != "fixed" {
		endpointID, endpointFingerprint, err = model.ResolveProxyEndpointIdentity(identity.UserID, proxy)
		if err != nil {
			return "", service.ErrAccountEgress
		}
	}
	if name == "" {
		name = provider + " · " + time.Now().Format("0102")
	}
	return model.CreateWorkBuddyAuthFlow(model.WorkBuddyAuthState{Identity: identity, Provider: provider, Realm: realm, EgressKey: egress, Proxy: proxy, EndpointID: endpointID, EndpointFingerprint: endpointFingerprint, Settings: settings, DisplayName: name, Models: models, Public: public, Status: "starting"})
}

func StartWorkBuddyAuthorization(ctx context.Context, identity model.AuthSessionIdentity, realm, name string, public bool, settings dto.ChannelSettings) (string, string, error) {
	if realm != "cn" && realm != "global" {
		return "", "", model.ErrAuthFlowInvalid
	}
	token, err := newAccountFlow(identity, "workbuddy", realm, name, public, settings, (&workbuddy.Adaptor{}).GetModelList())
	if err != nil {
		return "", "", err
	}
	var authURL string
	var upstreamErr error
	err = model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		data, err := workBuddyAuthRequest(ctx, state, http.MethodPost, "/v2/plugin/auth/state?platform=CLI", "")
		if err != nil {
			state.Status = "failed"
			upstreamErr = err
			return nil
		}
		upstreamState, _ := data["state"].(string)
		authURL, _ = data["authUrl"].(string)
		u, parseErr := url.Parse(authURL)
		allowed := false
		if parseErr == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && u.Fragment == "" {
			if realm == "global" {
				allowed = u.Host == "www.workbuddy.ai"
			} else {
				allowed = u.Host == "www.codebuddy.cn" || u.Host == "copilot.tencent.com" || u.Host == "www.workbuddy.cn"
			}
		}
		if !allowed || upstreamState == "" || len(upstreamState) > 4096 {
			state.Status = "failed"
			upstreamErr = service.ErrAccountProtocol
			return nil
		}
		state.UpstreamState, state.Status = upstreamState, "pending"
		return nil
	})
	if err != nil {
		return "", "", err
	}
	if upstreamErr != nil {
		return "", "", upstreamErr
	}
	return token, authURL, nil
}

// PollAccountAuthorization retains the encrypted credential across account lookup
// and postcommit proxy-binding failures. Retrying never redeems a second token.
func PollAccountAuthorization(ctx context.Context, identity model.AuthSessionIdentity, token string) (*model.Channel, error) {
	// Resolve outside the flow transaction: proxy persistence uses its own DB
	// connection. A moved/deleted pool endpoint must never silently change the
	// egress of a device grant already in progress.
	var snapshot model.WorkBuddyAuthState
	if err := model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		snapshot = *state
		return nil
	}); err != nil {
		return nil, err
	}
	currentProxy := snapshot.Proxy
	if snapshot.Status != "done" {
		proxy, err := model.ResolveStableChannelProxy(identity.UserID, 0, snapshot.EgressKey, snapshot.Settings)
		if err != nil {
			return nil, service.ErrAccountEgress
		}
		if snapshot.EndpointID > 0 {
			id, fingerprint, err := model.ResolveProxyEndpointIdentity(identity.UserID, proxy)
			if err != nil || id != snapshot.EndpointID || fingerprint != snapshot.EndpointFingerprint {
				return nil, service.ErrAccountEgress
			}
		} else if proxy != snapshot.Proxy {
			// Explicit fixed/direct settings are not runtime node aliases.
			return nil, service.ErrAccountEgress
		}
		currentProxy = proxy
	}
	var stage model.WorkBuddyAuthState
	var providerErr error
	err := model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		state.Proxy = currentProxy
		if state.Status == "done" || state.Status == "binding" {
			stage = *state
			return nil
		}
		if state.Status == "failed" || state.Status == "cancelled" {
			return model.ErrAuthFlowConsumed
		}
		if state.NextPollAt > time.Now().Unix() {
			return service.ErrAccountPollEarly
		}
		if state.Provider != "workbuddy" && state.Provider != "trae" {
			return model.ErrAuthFlowInvalid
		}
		if state.Provider == "workbuddy" {
			var cred workbuddy.Cred
			if state.Credential == "" {
				data, err := workBuddyAuthRequest(ctx, state, http.MethodGet, "/v2/plugin/auth/token?state="+url.QueryEscape(state.UpstreamState), "")
				if err != nil {
					providerErr = err
					return nil
				}
				state.NextPollAt = time.Now().Add(2 * time.Second).Unix()
				if data == nil {
					return nil
				}
				raw, err := common.Marshal(data)
				if err != nil {
					return err
				}
				if err := common.Unmarshal(raw, &cred); err != nil || cred.AccessToken == "" {
					providerErr = service.ErrAccountProtocol
					return nil
				}
				cred.Realm = state.Realm
				if expires, ok := data["expiresIn"].(float64); ok && expires > 0 && expires < 31536000 {
					cred.ExpiresAt = time.Now().Add(time.Duration(expires) * time.Second).Unix()
				}
				raw, err = common.Marshal(cred)
				if err != nil {
					return err
				}
				state.Credential = string(raw)
			} else if err := common.Unmarshal([]byte(state.Credential), &cred); err != nil {
				return err
			}
			if cred.UID == "" {
				data, err := workBuddyAuthRequest(ctx, state, http.MethodGet, "/v2/plugin/login/account?state="+url.QueryEscape(state.UpstreamState), cred.AccessToken)
				if err != nil {
					providerErr = err
					return nil
				}
				cred.UID, _ = data["uid"].(string)
				cred.Nickname, _ = data["nickname"].(string)
				cred.EnterpriseID, _ = data["enterpriseId"].(string)
				if cred.UID == "" {
					providerErr = service.ErrAccountProtocol
					return nil
				}
				raw, err := common.Marshal(cred)
				if err != nil {
					return err
				}
				state.Credential = string(raw)
			}
		}
		channelType := constant.ChannelTypeWorkBuddy
		if state.Provider == "trae" {
			channelType = constant.ChannelTypeTrae
		}
		if err := model.StageWorkBuddyChannel(tx, state, channelType); err != nil {
			if errors.Is(err, model.ErrWorkBuddyAuthLimit) {
				// Persist a redeemed credential even when capacity has changed.
				// Retry must not redeem the one-shot upstream grant again.
				providerErr = err
				return nil
			}
			return err
		}
		stage = *state
		return nil
	})
	if err != nil {
		return nil, err
	}
	if providerErr != nil {
		return nil, providerErr
	}
	if stage.ChannelID == 0 {
		return nil, nil
	}
	if stage.Status != "done" && stage.Settings.ProxyPolicy != nil && stage.Settings.ProxyPolicy.Mode != "direct" && stage.Settings.ProxyPolicy.Mode != "fixed" {
		if err := model.BindChannelProxyAccount(identity.UserID, stage.ChannelID, stage.EgressKey); err != nil {
			return nil, service.ErrAccountEgress
		}
	}
	var channel *model.Channel
	err = model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		var err error
		channel, err = model.FinishWorkBuddyChannel(tx, state)
		return err
	})
	if err != nil {
		return nil, err
	}
	if stage.Status != "done" {
		model.InitChannelCache()
	}
	return channel, nil
}

func CancelAccountAuthorization(identity model.AuthSessionIdentity, token string) error {
	return model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Status == "done" {
			return model.ErrAuthFlowConsumed
		}
		if state.ChannelID > 0 {
			if err := tx.Where("channel_id = ?", state.ChannelID).Delete(&model.Ability{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id = ? AND owner_id = ? AND status = ?", state.ChannelID, identity.UserID, common.ChannelStatusManuallyDisabled).Delete(&model.Channel{}).Error; err != nil {
				return err
			}
		}
		state.Status = "cancelled"
		return nil
	})
}

// ImportTraeAuthorization accepts only the observed official local callback
// contract. It does NOT invent a gateway callback or advertise browser login.
func ImportTraeAuthorization(ctx context.Context, identity model.AuthSessionIdentity, callback, refresh, uid, machine, device, name string, public, confirmRotation bool, settings dto.ChannelSettings) (string, error) {
	info := &trae.CallbackInfo{RefreshToken: refresh, UID: uid}
	if callback != "" {
		if refresh != "" {
			return "", model.ErrAuthFlowInvalid
		}
		u, err := url.Parse(callback)
		if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "/authorize" || u.User != nil || u.Fragment != "" {
			return "", errors.New("Unsupported Trae callback URI; use the official local client callback")
		}
		info, err = trae.ParseCallback(callback)
		if err != nil {
			return "", model.ErrAuthFlowInvalid
		}
	}
	if info.RefreshToken == "" && info.AccessToken == "" {
		return "", model.ErrAuthFlowInvalid
	}
	if info.RefreshToken != "" && !confirmRotation {
		return "", errors.New("Trae token rotation confirmation is required; other clients may be signed out")
	}
	token, err := newAccountFlow(identity, "trae", "cn", name, public, settings, (&trae.Adaptor{}).GetModelList())
	if err != nil {
		return "", err
	}
	var upstreamErr error
	err = model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		assertion := info.RefreshToken
		if assertion == "" {
			assertion = info.AccessToken
		}
		if err := model.ClaimExternalAuthAssertionWithTx(tx, "trae_import", assertion, time.Now().Add(24*time.Hour)); err != nil {
			return err
		}
		if machine == "" || device == "" {
			machine, device = trae.NewDeviceIDs()
		}
		cred := trae.Cred{AccessToken: info.AccessToken, RefreshToken: info.RefreshToken, UID: info.UID, MachineID: machine, DeviceID: device, Domain: "trae.cn", ExpiresAt: info.ExpiresAt}
		if cred.RefreshToken != "" {
			body, _ := common.Marshal(map[string]any{"ClientID": trae.ClientID, "RefreshToken": cred.RefreshToken, "ClientSecret": "-", "UserID": ""})
			req, _ := http.NewRequest(http.MethodPost, trae.OAuthHost+trae.EpExchange, bytes.NewReader(body))
			trae.OAuthHeaders(req)
			var response struct {
				Result struct {
					Token               string
					RefreshToken        string
					TokenExpireAt       int64
					TokenExpireDuration int64
				}
			}
			if err := service.AccountAuthorizationJSON(ctx, state.Proxy, req, &response); err != nil {
				state.Status = "failed"
				upstreamErr = err
				return nil
			}
			if response.Result.Token == "" {
				state.Status = "failed"
				upstreamErr = service.ErrAccountProtocol
				return nil
			}
			cred.AccessToken = response.Result.Token
			if response.Result.RefreshToken != "" {
				cred.RefreshToken = response.Result.RefreshToken
			}
			cred.ExpiresAt = trae.NormalizeExpire(response.Result.TokenExpireAt)
			if cred.ExpiresAt <= 0 && response.Result.TokenExpireDuration > 0 && response.Result.TokenExpireDuration < 31536000 {
				cred.ExpiresAt = time.Now().Add(time.Duration(response.Result.TokenExpireDuration) * time.Second).Unix()
			}
		}
		body, _ := common.Marshal(map[string]any{"ReqSource": "IDE", "IDEVersion": trae.IdeVersion})
		req, _ := http.NewRequest(http.MethodPost, trae.OAuthHost+trae.EpUserInfo, bytes.NewReader(body))
		trae.OAuthHeaders(req)
		req.Header.Set("X-Cloudide-Token", cred.AccessToken)
		var response struct {
			Result struct {
				UserID       string
				ScreenName   string
				EnterpriseID string
			}
		}
		if err := service.AccountAuthorizationJSON(ctx, state.Proxy, req, &response); err != nil {
			state.Status = "failed"
			upstreamErr = err
			return nil
		}
		if response.Result.UserID == "" || cred.UID != "" && cred.UID != response.Result.UserID {
			state.Status = "failed"
			upstreamErr = service.ErrAccountProtocol
			return nil
		}
		cred.UID, cred.Nickname, cred.EnterpriseID = response.Result.UserID, response.Result.ScreenName, response.Result.EnterpriseID
		if cred.ExpiresAt <= time.Now().Unix() {
			state.Status = "failed"
			upstreamErr = service.ErrAccountProtocol
			return nil
		}
		raw, err := common.Marshal(cred)
		if err != nil {
			return err
		}
		state.Credential, state.Status = string(raw), "ready"
		return nil
	})
	if err != nil {
		return "", err
	}
	if upstreamErr != nil {
		return "", upstreamErr
	}
	return token, nil
}

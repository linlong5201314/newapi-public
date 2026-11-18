package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const freePiUpstream = "https://api.freepi.ai"
const freePiConsentVersion = "4" // Verified from freepi-gw v1.0.0; never auto-accept.

var freePiVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func freePiError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"success": false, "message": common.NewMessage(message)})
}

// These handlers require UserAuth at routing and independently require a live
// dashboard session, explicit JSON AJAX requests and same-origin protection. No
// OAuth callback is invented: FreePi owns the GitHub device grant end to end.
func freePiIdentity(c *gin.Context) (model.AuthSessionIdentity, bool) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	_, isPAT := model.ParseAccessTokenSessionID(identity.SessionID)
	if !ok || isPAT || c.GetBool("use_access_token") {
		freePiError(c, http.StatusUnauthorized, "A dashboard login session is required for FreePi authorization")
		return model.AuthSessionIdentity{}, false
	}
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if c.Request.Method != http.MethodPost || mediaType != "application/json" || err != nil || c.GetHeader("X-FreePi-Action") != "authorize" {
		freePiError(c, http.StatusForbidden, "FreePi authorization requires a protected JSON request")
		return model.AuthSessionIdentity{}, false
	}
	guard := http.NewCrossOriginProtection()
	for _, origin := range common.SessionCookieTrustedURLs {
		if err := guard.AddTrustedOrigin(origin); err != nil {
			freePiError(c, http.StatusServiceUnavailable, "FreePi authorization origin configuration is invalid")
			return model.AuthSessionIdentity{}, false
		}
	}
	if err := guard.Check(c.Request); err != nil {
		freePiError(c, http.StatusForbidden, "FreePi authorization origin was rejected")
		return model.AuthSessionIdentity{}, false
	}
	if _, _, err := service.ValidateLoginSession(identity); err != nil {
		freePiError(c, http.StatusUnauthorized, "FreePi authorization login session is no longer active")
		return model.AuthSessionIdentity{}, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
	return model.AuthSessionIdentity{UserID: identity.UserID, SessionID: identity.SessionID, UserAuthVersion: identity.UserAuthVersion, SessionVersion: identity.SessionVersion}, true
}

// freePiJSON has a fixed origin and path selected only by these handlers, never
// by request input. It neither follows redirects nor exposes upstream bodies or
// transport errors (which may contain credentials) in API errors or audit logs.
func freePiJSON(ctx context.Context, method, path, token string, body any, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := common.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, freePiUpstream+path, reader)
	if err != nil {
		return 0, errors.New("FreePi upstream request is invalid")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := *service.GetHttpClient()
	client.Timeout = 8 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return 0, errors.New("FreePi upstream request failed")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return resp.StatusCode, errors.New("FreePi upstream response is invalid")
	}
	if out == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, nil
	}
	if err := common.Unmarshal(raw, out); err != nil {
		return resp.StatusCode, errors.New("FreePi upstream response is invalid")
	}
	return resp.StatusCode, nil
}

// POST /api/channel/self/oauth/freepi/start
func StartFreePiOAuth(c *gin.Context) {
	identity, ok := freePiIdentity(c)
	if !ok {
		return
	}
	var req struct {
		DisplayName   string `json:"displayName"`
		Public        bool   `json:"public"`
		ConfirmPublic bool   `json:"confirmPublic"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len([]rune(req.DisplayName)) > 64 || (req.Public && !req.ConfirmPublic) {
		freePiError(c, http.StatusBadRequest, "Invalid FreePi authorization parameters or missing public sharing confirmation")
		return
	}
	var session model.UserSession
	if err := model.DB.Where("sid = ? AND user_id = ?", identity.SessionID, identity.UserID).First(&session).Error; err != nil || session.CreatedAt <= 0 || time.Since(time.Unix(session.CreatedAt, 0)) > 10*time.Minute {
		freePiError(c, http.StatusForbidden, "Sign in again before linking a FreePi account")
		return
	}
	if !common.CryptoSecretConfigured || strings.TrimSpace(common.CryptoSecret) == "" {
		freePiError(c, http.StatusServiceUnavailable, "FreePi authorization requires encrypted credential storage")
		return
	}
	var catalog struct {
		Latest string `json:"latest"`
		Models []struct {
			ID          string `json:"id"`
			Unavailable bool   `json:"unavailable"`
		} `json:"models"`
	}
	status, err := freePiJSON(c.Request.Context(), http.MethodGet, "/client-version", "", nil, &catalog)
	if err != nil || status != http.StatusOK || !freePiVersionPattern.MatchString(catalog.Latest) || len(catalog.Models) == 0 {
		freePiError(c, http.StatusBadGateway, "FreePi model catalog could not be verified")
		return
	}
	models := make([]string, 0, len(catalog.Models))
	for _, item := range catalog.Models {
		if !item.Unavailable && item.ID != "" && len(item.ID) <= 200 && !strings.ContainsAny(item.ID, ",\r\n") {
			models = append(models, item.ID)
		}
	}
	if len(models) == 0 {
		freePiError(c, http.StatusBadGateway, "FreePi model catalog could not be verified")
		return
	}
	var device struct {
		SessionID       string `json:"session_id"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Interval        int    `json:"interval"`
		ExpiresIn       int    `json:"expires_in"`
	}
	status, err = freePiJSON(c.Request.Context(), http.MethodPost, "/auth/github/device", "", struct{}{}, &device)
	verification, parseErr := url.Parse(device.VerificationURI)
	if err != nil || status != http.StatusOK || device.SessionID == "" || len(device.SessionID) > 512 || device.UserCode == "" || len(device.UserCode) > 64 || parseErr != nil || verification.Scheme != "https" || verification.Host != "github.com" || verification.Path != "/login/device" || verification.User != nil || verification.RawQuery != "" || verification.Fragment != "" || device.ExpiresIn <= 0 {
		freePiError(c, http.StatusBadGateway, "FreePi device authorization response could not be verified")
		return
	}
	if device.Interval > 600 {
		freePiError(c, http.StatusBadGateway, "FreePi device authorization response could not be verified")
		return
	}
	interval, expires := max(5, device.Interval), min(device.ExpiresIn, 600)
	name := strings.TrimSpace(req.DisplayName)
	if name == "" {
		name = "FreePi"
	}
	state, err := model.CreateFreePiAuthFlow(model.FreePiAuthState{Identity: identity, UpstreamSession: device.SessionID, DisplayName: name, Public: req.Public, Interval: interval, NextPollAt: time.Now().Add(time.Duration(interval) * time.Second).Unix(), Status: "pending", ClientVersion: catalog.Latest, Models: models}, time.Now().Add(time.Duration(expires)*time.Second))
	if err != nil {
		code := http.StatusServiceUnavailable
		if errors.Is(err, model.ErrFreePiAuthLimit) {
			code = http.StatusTooManyRequests
		}
		freePiError(c, code, "FreePi authorization could not be started. Try again later")
		return
	}
	model.RecordAuditLog(c, model.AuditLog{UserId: identity.UserID, ActorRole: c.GetInt("role"), Category: "auth", Action: "freepi.authorize.start", Success: true, Content: "FreePi device authorization started"})
	common.ApiSuccess(c, gin.H{"state": state, "userCode": device.UserCode, "verificationUri": verification.String(), "interval": interval, "expiresIn": expires, "flow": "device_authorization"})
}

// POST /api/channel/self/oauth/freepi/poll -- state stays in the JSON body.
func PollFreePiOAuth(c *gin.Context) { freePiAdvance(c, "poll") }

// POST /api/channel/self/oauth/freepi/consent -- explicit user acceptance only.
func ConsentFreePiOAuth(c *gin.Context) { freePiAdvance(c, "consent") }

// POST /api/channel/self/oauth/freepi/cancel
func CancelFreePiOAuth(c *gin.Context) { freePiAdvance(c, "cancel") }

func freePiAdvance(c *gin.Context, operation string) {
	identity, ok := freePiIdentity(c)
	if !ok {
		return
	}
	var req struct {
		State          string `json:"state"`
		Accepted       bool   `json:"accepted"`
		ConsentVersion string `json:"consentVersion"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.State) != 43 {
		freePiError(c, http.StatusBadRequest, "Invalid FreePi authorization state")
		return
	}
	result := gin.H{}
	created := false
	err := model.WithFreePiAuthFlow(req.State, identity, func(tx *gorm.DB, state *model.FreePiAuthState) error {
		if state.Status == "done" {
			result = gin.H{"status": "done", "done": true, "channel": gin.H{"id": state.ChannelID}, "public": state.Public}
			return nil
		}
		if operation == "cancel" {
			state.Status, state.UpstreamSession = "cancelled", ""
			result["status"] = "cancelled"
			return nil
		}
		if operation == "consent" {
			if state.Status != "consent_required" || !req.Accepted || req.ConsentVersion != freePiConsentVersion {
				return model.ErrAuthFlowInvalid
			}
			status, err := freePiJSON(c.Request.Context(), http.MethodPost, "/auth/consent", "", gin.H{"session_id": state.UpstreamSession, "consent_version": freePiConsentVersion}, nil)
			if err != nil || status < 200 || status >= 300 {
				return errors.New("FreePi consent request failed")
			}
			state.Status = "pending"
			state.NextPollAt = time.Now().Add(time.Duration(state.Interval) * time.Second).Unix()
			result = gin.H{"status": "pending", "interval": state.Interval}
			return nil
		}
		if state.Status == "consent_required" {
			result = gin.H{"status": "consent_required", "consentVersion": freePiConsentVersion}
			return nil
		}
		if time.Now().Unix() < state.NextPollAt {
			result = gin.H{"status": "pending", "interval": state.Interval}
			return nil
		}
		state.NextPollAt = time.Now().Add(time.Duration(state.Interval) * time.Second).Unix()
		var response struct {
			Token      string `json:"token"`
			Status     string `json:"status"`
			Code       string `json:"code"`
			Error      string `json:"error"`
			RetryAfter int    `json:"retry_after"`
		}
		status, err := freePiJSON(c.Request.Context(), http.MethodPost, "/auth/token", "", gin.H{"session_id": state.UpstreamSession, "timezone": "UTC", "locale": "en"}, &response)
		if err != nil {
			state.Status, state.UpstreamSession = "failed", ""
			result["status"] = "restart_required"
			return nil
		}
		if (status == http.StatusForbidden || status == http.StatusOK) && response.Code == "consent_required" {
			state.Status = "consent_required"
			result = gin.H{"status": "consent_required", "consentVersion": freePiConsentVersion}
			return nil
		}
		if response.Status == "slow_down" || response.Error == "slow_down" || response.Code == "slow_down" || response.RetryAfter > state.Interval || status == http.StatusTooManyRequests {
			state.Interval = max(state.Interval+5, response.RetryAfter)
			if state.Interval > 600 {
				state.Status, state.UpstreamSession = "failed", ""
				result["status"] = "restart_required"
				return nil
			}
			state.NextPollAt = time.Now().Add(time.Duration(state.Interval) * time.Second).Unix()
			result = gin.H{"status": "pending", "interval": state.Interval}
			return nil
		}
		if response.Status == "pending" || response.Error == "authorization_pending" || response.Code == "authorization_pending" {
			result = gin.H{"status": "pending", "interval": state.Interval}
			return nil
		}
		if status != http.StatusOK || response.Token == "" || len(response.Token) > 16384 || strings.ContainsAny(response.Token, "\r\n") {
			state.Status, state.UpstreamSession = "failed", ""
			result["status"] = "restart_required"
			return nil
		}
		var me struct {
			Handle string `json:"handle"`
		}
		status, err = freePiJSON(c.Request.Context(), http.MethodGet, "/me", response.Token, nil, &me)
		if err != nil || status != http.StatusOK || strings.TrimSpace(me.Handle) == "" {
			state.Status, state.UpstreamSession = "failed", ""
			result["status"] = "restart_required"
			return nil
		}
		if err := model.CompleteFreePiChannel(tx, state, response.Token, me.Handle, constant.ChannelTypeFreePi); err != nil {
			return err
		}
		created = true
		result = gin.H{"status": "done", "done": true, "channel": gin.H{"id": state.ChannelID}, "public": state.Public}
		return nil
	})
	if err != nil {
		code := http.StatusServiceUnavailable
		message := "FreePi authorization could not be completed. Start a new authorization"
		if errors.Is(err, model.ErrAuthFlowInvalid) || errors.Is(err, model.ErrAuthFlowExpired) || errors.Is(err, model.ErrAuthFlowConsumed) {
			code, message = http.StatusBadRequest, "FreePi authorization state is invalid, expired or already used"
		}
		model.RecordAuditLog(c, model.AuditLog{UserId: identity.UserID, ActorRole: c.GetInt("role"), Category: "auth", Action: "freepi.authorize.reject", Success: false, Content: "FreePi authorization request rejected"})
		freePiError(c, code, message)
		return
	}
	if created {
		model.InitChannelCache()
		model.RecordAuditLog(c, model.AuditLog{UserId: identity.UserID, ActorRole: c.GetInt("role"), Category: "auth", Action: "freepi.authorize.complete", Success: true, Content: "FreePi account linked to an owner-scoped channel"})
	}
	common.ApiSuccess(c, result)
}

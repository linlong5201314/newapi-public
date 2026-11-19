package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/trae"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const traeBridgePath = "/api/channel/self/oauth/trae/bridge"
const traeBridgeKind = "trae-cn-local-v1"

//go:embed assets/trae_login_helper.py
var traeLoginHelper string

// Stored only inside the existing encrypted, dashboard-SID-bound auth flow. The
// official callback requires an exact /authorize path and supports no state.
// Consequently a verified account MUST receive separate dashboard consent.
type traeBridgeState struct {
	Kind             string `json:"kind"`
	Origin           string `json:"origin"`
	ExpiresAt        int64  `json:"expires_at"`
	CapabilityDigest string `json:"capability_digest,omitempty"`
	UploadUsed       bool   `json:"upload_used"`
	Callback         string `json:"callback,omitempty"`
	MachineID        string `json:"machine_id,omitempty"`
	DeviceID         string `json:"device_id,omitempty"`
	ConsentToken     string `json:"consent_token,omitempty"`
	ConsentUID       string `json:"consent_uid,omitempty"`
	RedeemNonce      string `json:"redeem_nonce,omitempty"`
	RedeemUntil      int64  `json:"redeem_until,omitempty"`
}

type traeBridgeCapability struct {
	Kind      string                    `json:"kind"`
	Flow      string                    `json:"flow"`
	Identity  model.AuthSessionIdentity `json:"identity"`
	Origin    string                    `json:"origin"`
	ExpiresAt int64                     `json:"expires_at"`
	Nonce     string                    `json:"nonce"`
}

func traeBridgeOrigin() (string, error) {
	u, err := url.Parse(strings.TrimSpace(system_setting.ServerAddress))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.Opaque != "" {
		return "", service.ErrAccountEgress
	}
	return "https://" + strings.ToLower(u.Host), nil
}

func traeBridgeIdentity(c *gin.Context) (model.AuthSessionIdentity, string, bool) {
	origin, err := traeBridgeOrigin()
	if err != nil {
		accountAuthorizationError(c, err)
		return model.AuthSessionIdentity{}, "", false
	}
	// A trusted gateway may rewrite Host. Neither Host nor forwarded headers
	// select a public destination: configuration alone defines this origin.
	if c.GetHeader("Origin") != "" && c.GetHeader("Origin") != origin {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "code": "ACCOUNT_ORIGIN_REJECTED"})
		return model.AuthSessionIdentity{}, "", false
	}
	// Reuse the shared session/JSON/fetch-metadata guard with its logical public
	// Host, not the reverse proxy's internal node address. Preserve its body cap.
	actual := c.Request
	c.Request = actual.Clone(actual.Context())
	c.Request.Host = strings.TrimPrefix(origin, "https://")
	c.Request.URL.Scheme, c.Request.URL.Host = "https", c.Request.Host
	identity, ok := accountAuthorizationIdentity(c)
	actual.Body = c.Request.Body
	c.Request = actual
	return identity, origin, ok
}

func readTraeBridge(state *model.WorkBuddyAuthState, origin string) (*traeBridgeState, error) {
	var bridge traeBridgeState
	if state.Provider != "trae" || state.Realm != "cn" || common.UnmarshalJsonStr(state.UpstreamState, &bridge) != nil || bridge.Kind != traeBridgeKind || bridge.Origin != origin {
		return nil, model.ErrAuthFlowInvalid
	}
	if bridge.ExpiresAt <= time.Now().Unix() {
		return nil, model.ErrAuthFlowExpired
	}
	return &bridge, nil
}

func saveTraeBridge(state *model.WorkBuddyAuthState, bridge *traeBridgeState) error {
	raw, err := common.Marshal(bridge)
	if err == nil {
		state.UpstreamState = string(raw)
	}
	return err
}

// SelfTraeBridgeStart does not invent a hosted OAuth redirect. The user must
// explicitly download/run a local helper, log in at trae.cn, then confirm the
// provider-verified account in the original live dashboard session.
func SelfTraeBridgeStart(c *gin.Context) {
	identity, origin, ok := traeBridgeIdentity(c)
	if !ok {
		return
	}
	var request struct {
		Realm           string                  `json:"realm"`
		DisplayName     string                  `json:"displayName"`
		Public          bool                    `json:"public"`
		ConfirmRotation bool                    `json:"confirmRotation"`
		Proxy           string                  `json:"proxy"`
		ProxyPolicy     *dto.ChannelProxyPolicy `json:"proxyPolicy"`
	}
	if c.ShouldBindJSON(&request) != nil || request.Realm != "" && request.Realm != "cn" || len(request.DisplayName) > 128 || !request.ConfirmRotation {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	settings := accountLoginProxySettings("cn", request.Proxy, request.ProxyPolicy)
	flow, err := newAccountFlow(identity, "trae", "cn", strings.TrimSpace(request.DisplayName), request.Public, settings, (&trae.Adaptor{}).GetModelList())
	if err == nil {
		err = model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
			state.Status = "bridge_pending"
			return saveTraeBridge(state, &traeBridgeState{Kind: traeBridgeKind, Origin: origin, ExpiresAt: time.Now().Add(10 * time.Minute).Unix()})
		})
	}
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"sessionId": flow, "helperUrl": traeBridgePath + "/helper", "helperMethod": "POST", "helperFileName": "trae-login-helper.py", "helperCommand": "python -I trae-login-helper.py", "requiresLocalHelper": true, "requiresConsent": true, "expiresIn": 600, "interval": 2, "realm": "cn"}})
}

func SelfTraeBridgeHelper(c *gin.Context) {
	identity, origin, ok := traeBridgeIdentity(c)
	if !ok {
		return
	}
	var request struct {
		SessionID string `json:"sessionId"`
	}
	if c.ShouldBindJSON(&request) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	var capability string
	var expires int64
	err := model.WithWorkBuddyAuthFlow(request.SessionID, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		bridge, err := readTraeBridge(state, origin)
		if err != nil {
			return err
		}
		if state.Status != "bridge_pending" || bridge.UploadUsed {
			return model.ErrAuthFlowConsumed
		}
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		expires = bridge.ExpiresAt
		raw, err := common.Marshal(traeBridgeCapability{Kind: traeBridgeKind, Flow: request.SessionID, Identity: identity, Origin: origin, ExpiresAt: expires, Nonce: base64.RawURLEncoding.EncodeToString(nonce)})
		if err != nil {
			return err
		}
		capability, err = common.EncryptSecret(string(raw))
		if err != nil || !common.IsEncryptedSecret(capability) {
			return model.ErrWorkBuddyEncryptionRequired
		}
		// A second download revokes the first file's one-time capability.
		digest := sha256.Sum256([]byte(capability))
		bridge.CapabilityDigest = base64.RawURLEncoding.EncodeToString(digest[:])
		return saveTraeBridge(state, bridge)
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	config, err := common.Marshal(map[string]any{"origin": origin, "upload_url": origin + traeBridgePath + "/upload", "capability": capability, "expires_at": expires})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="trae-login-helper.py"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	source := strings.Replace(traeLoginHelper, "__TRAE_BRIDGE_CONFIG_BASE64__", base64.StdEncoding.EncodeToString(config), 1)
	c.Data(http.StatusOK, "text/x-python; charset=utf-8", []byte(source))
}

// SelfTraeBridgeUpload authenticates ONLY a short-lived one-use upload capability,
// never a dashboard JWT/cookie. Credential data is accepted in a bounded POST
// body, not a hosted URL. No channel is activated on this unauthenticated route.
func SelfTraeBridgeUpload(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	origin, err := traeBridgeOrigin()
	media, _, mediaErr := mime.ParseMediaType(c.GetHeader("Content-Type"))
	capability, bearer := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	if err != nil || mediaErr != nil || media != "application/json" || !bearer || len(capability) > 8192 || !common.IsEncryptedSecret(capability) || c.GetHeader("Origin") != "" || c.GetHeader("Sec-Fetch-Site") != "" || c.Request.URL.RawQuery != "" {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	raw, err := common.DecryptSecret(capability)
	var grant traeBridgeCapability
	if err != nil || common.UnmarshalJsonStr(raw, &grant) != nil || grant.Kind != traeBridgeKind || grant.Origin != origin || len(grant.Nonce) != 43 {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	if grant.ExpiresAt <= time.Now().Unix() {
		accountAuthorizationError(c, model.ErrAuthFlowExpired)
		return
	}
	var request struct {
		CallbackURL string `json:"callbackUrl"`
		MachineID   string `json:"machineId"`
		DeviceID    string `json:"deviceId"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	if c.ShouldBindJSON(&request) != nil || len(request.CallbackURL) > 49152 || len(request.MachineID) != 64 || len(request.DeviceID) != 36 {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	info, err := trae.ParseCallback(request.CallbackURL)
	if err != nil || info.UID == "" || info.ClientID != trae.IDEClientID {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	err = model.WithWorkBuddyAuthFlow(grant.Flow, grant.Identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		bridge, err := readTraeBridge(state, origin)
		if err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(capability))
		if bridge.UploadUsed || state.Status != "bridge_pending" {
			return model.ErrAuthFlowConsumed
		}
		if subtle.ConstantTimeCompare([]byte(bridge.CapabilityDigest), []byte(base64.RawURLEncoding.EncodeToString(digest[:]))) != 1 || grant.ExpiresAt != bridge.ExpiresAt {
			return model.ErrAuthFlowInvalid
		}
		bridge.UploadUsed, bridge.CapabilityDigest = true, ""
		bridge.Callback, bridge.MachineID, bridge.DeviceID = request.CallbackURL, request.MachineID, request.DeviceID
		state.Status = "bridge_uploaded"
		return saveTraeBridge(state, bridge)
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"uploaded": true, "requiresConsent": true}})
}

// pollTraeBridge verifies the provider account, but never stages a channel before
// explicit consent. ResolveStableChannelProxy is deliberately outside the flow
// transaction, matching PollAccountAuthorization's egress stability checks.
func pollTraeBridge(ctx context.Context, identity model.AuthSessionIdentity, token, origin string) (*traeBridgeState, *trae.Cred, string, error) {
	var snapshot model.WorkBuddyAuthState
	err := model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		snapshot = *state
		if state.Status == "done" {
			if state.Provider != "trae" || state.Realm != "cn" {
				return model.ErrAuthFlowInvalid
			}
			return nil
		}
		_, err := readTraeBridge(state, origin)
		return err
	})
	if err != nil {
		return nil, nil, "", err
	}
	if snapshot.Status == "done" || snapshot.Status == "ready" || snapshot.Status == "binding" {
		return nil, nil, snapshot.Status, nil
	}
	proxy, err := model.ResolveStableChannelProxy(identity.UserID, 0, snapshot.EgressKey, snapshot.Settings)
	if err != nil {
		return nil, nil, "", service.ErrAccountEgress
	}
	if snapshot.EndpointID > 0 {
		id, fingerprint, err := model.ResolveProxyEndpointIdentity(identity.UserID, proxy)
		if err != nil || id != snapshot.EndpointID || fingerprint != snapshot.EndpointFingerprint {
			return nil, nil, "", service.ErrAccountEgress
		}
	} else if proxy != snapshot.Proxy {
		return nil, nil, "", service.ErrAccountEgress
	}
	var bridge *traeBridgeState
	var credential *trae.Cred
	var info *trae.CallbackInfo
	var status, redeemNonce, machine, device string
	var providerErr error
	err = model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		var err error
		bridge, err = readTraeBridge(state, origin)
		if err != nil {
			return err
		}
		status = state.Status
		if status == "bridge_pending" {
			return nil
		}
		if status == "bridge_redeeming" {
			// A crashed worker must not redeem the same one-shot upstream pair
			// again. An expired lease fails closed; the user starts a fresh login.
			if bridge.RedeemUntil <= time.Now().Unix() {
				state.Status, providerErr = "failed", service.ErrAccountUpstream
			}
			return nil
		}
		if status == "bridge_consent" {
			credential = &trae.Cred{}
			return common.UnmarshalJsonStr(state.Credential, credential)
		}
		if status != "bridge_uploaded" {
			return model.ErrAuthFlowInvalid
		}
		info, err = trae.ParseCallback(bridge.Callback)
		if err != nil {
			return model.ErrAuthFlowInvalid
		}
		if err := claimTraeAuthorization(tx, info); err != nil {
			return err
		}
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		redeemNonce, machine, device = base64.RawURLEncoding.EncodeToString(nonce), bridge.MachineID, bridge.DeviceID
		bridge.RedeemNonce, bridge.RedeemUntil = redeemNonce, min(bridge.ExpiresAt, time.Now().Add(time.Minute).Unix())
		bridge.Callback, bridge.MachineID, bridge.DeviceID = "", "", ""
		state.Status, status = "bridge_redeeming", "bridge_redeeming"
		return saveTraeBridge(state, bridge)
	})
	if err != nil {
		return nil, nil, "", err
	}
	if providerErr != nil || redeemNonce == "" {
		return bridge, credential, status, providerErr
	}
	// Never hold a database transaction (particularly SQLite's global writer
	// lock) during provider HTTP. Cancellation and unrelated writes remain live.
	redeemContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	credential, providerErr = RedeemTraeAuthorization(redeemContext, proxy, info, machine, device)
	cancel()
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		var err error
		bridge, err = readTraeBridge(state, origin)
		if err != nil {
			return err
		}
		if state.Status != "bridge_redeeming" || bridge.RedeemUntil <= time.Now().Unix() || subtle.ConstantTimeCompare([]byte(bridge.RedeemNonce), []byte(redeemNonce)) != 1 {
			return model.ErrAuthFlowConsumed
		}
		bridge.RedeemNonce, bridge.RedeemUntil = "", 0
		if providerErr != nil {
			state.Status = "failed"
			return nil
		}
		consent := make([]byte, 32)
		if _, err := rand.Read(consent); err != nil {
			return err
		}
		bridge.ConsentToken, bridge.ConsentUID = base64.RawURLEncoding.EncodeToString(consent), credential.UID
		raw, err := common.Marshal(credential)
		if err != nil {
			return err
		}
		state.Credential, state.Status, status = string(raw), "bridge_consent", "bridge_consent"
		return saveTraeBridge(state, bridge)
	})
	if err != nil {
		return nil, nil, "", err
	}
	return bridge, credential, status, providerErr
}

func SelfTraeBridgePoll(c *gin.Context) {
	identity, origin, ok := traeBridgeIdentity(c)
	if !ok {
		return
	}
	token := c.Query("sessionId")
	bridge, credential, status, err := pollTraeBridge(c.Request.Context(), identity, token, origin)
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if status == "bridge_consent" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": false, "status": "awaiting_consent", "requiresConsent": true, "consentToken": bridge.ConsentToken, "account": gin.H{"uid": credential.UID, "nickname": credential.Nickname}, "interval": 2}})
		return
	}
	if status == "ready" || status == "binding" || status == "done" {
		channel, err := PollAccountAuthorization(c.Request.Context(), identity, token)
		if err != nil {
			accountAuthorizationError(c, err)
			return
		}
		if channel == nil {
			accountAuthorizationError(c, model.ErrAuthFlowInvalid)
			return
		}
		sanitizeSelfChannel(channel)
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": true, "channel": channel}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": false, "status": "waiting_for_helper", "interval": 2}})
}

func SelfTraeBridgeConsent(c *gin.Context) {
	identity, origin, ok := traeBridgeIdentity(c)
	if !ok {
		return
	}
	var request struct {
		SessionID    string `json:"sessionId"`
		ConsentToken string `json:"consentToken"`
		UID          string `json:"uid"`
	}
	if c.ShouldBindJSON(&request) != nil || len(request.ConsentToken) != 43 || request.UID == "" {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	err := model.WithWorkBuddyAuthFlow(request.SessionID, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		bridge, err := readTraeBridge(state, origin)
		if err != nil {
			return err
		}
		if state.Status != "bridge_consent" || subtle.ConstantTimeCompare([]byte(bridge.ConsentToken), []byte(request.ConsentToken)) != 1 || bridge.ConsentUID != request.UID {
			return model.ErrAuthFlowInvalid
		}
		bridge.ConsentToken = ""
		state.Status = "ready"
		return saveTraeBridge(state, bridge)
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	channel, err := PollAccountAuthorization(c.Request.Context(), identity, request.SessionID)
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if channel == nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	sanitizeSelfChannel(channel)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": true, "channel": channel}})
}

func SelfTraeBridgeCancel(c *gin.Context) {
	identity, origin, ok := traeBridgeIdentity(c)
	if !ok {
		return
	}
	var request struct {
		SessionID string `json:"sessionId"`
	}
	if c.ShouldBindJSON(&request) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	err := model.WithWorkBuddyAuthFlow(request.SessionID, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		if _, err := readTraeBridge(state, origin); err != nil {
			return err
		}
		if state.ChannelID > 0 || state.Status == "done" {
			return model.ErrAuthFlowConsumed
		}
		state.Status = "cancelled"
		return nil
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"cancelled": true}})
}

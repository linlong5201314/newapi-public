package controller

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/trae"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Verified against magpie-community/plugins@e6c457112ababf96d9415fe0a217ab0b7429ae98.
// Neither provider permits a hosted gateway redirect. The generated URLs keep
// the actual desktop loopback shape; there is deliberately no local listener,
// helper download, callback GET route, browser automation, or URL fetching here.
const raccoonURLKind = "raccoon-url-v1"

type accountURLStartRequest struct {
	Realm           string                  `json:"realm"`
	DisplayName     string                  `json:"displayName"`
	Public          bool                    `json:"public"`
	ConfirmRotation bool                    `json:"confirmRotation"`
	Proxy           string                  `json:"proxy"`
	ProxyPolicy     *dto.ChannelProxyPolicy `json:"proxyPolicy"`
}

type accountURLRequest struct {
	SessionID   string `json:"sessionId"`
	CallbackURL string `json:"callbackUrl"`
}

// Embedded bridge fields preserve readTraeBridge/pollTraeBridge and the existing
// explicit verified-identity consent contract. The extra URL metadata is needed
// only until authenticated paste; old helper flows cannot use this entry point.
type traeURLState struct {
	traeBridgeState
	URLMode          bool   `json:"url_mode"`
	ExpectedCallback string `json:"expected_callback"`
	LoginTrace       string `json:"login_trace"`
}

type raccoonURLState struct {
	Kind             string `json:"kind"`
	Origin           string `json:"origin"`
	ExpectedCallback string `json:"expected_callback"`
	State            string `json:"state"`
	ExpiresAt        int64  `json:"expires_at"`
	RedeemNonce      string `json:"redeem_nonce,omitempty"`
	RedeemUntil      int64  `json:"redeem_until,omitempty"`
	ConsentToken     string `json:"consent_token,omitempty"`
	ConsentUID       string `json:"consent_uid,omitempty"`
}

// accountURLIdentity adds a strict POST/no-query boundary to the existing
// dashboard SID, live UserVersion, configured-origin, Fetch Metadata and JSON
// guards. Returned credential URLs belong in the bounded body only, never a URL
// that request/access logging will record. Host/Forwarded never select origin.
func accountURLIdentity(c *gin.Context) (model.AuthSessionIdentity, string, bool) {
	if c.Request.Method != http.MethodPost || c.Request.URL.RawQuery != "" {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return model.AuthSessionIdentity{}, "", false
	}
	return traeBridgeIdentity(c)
}

func generatedLoopbackCallback(path string) (string, error) {
	port, err := rand.Int(rand.Reader, big.NewInt(16384))
	if err != nil {
		return "", err
	}
	return "http://127.0.0.1:" + strconv.Itoa(49152+int(port.Int64())) + path, nil
}

func saveAccountURLState(state *model.WorkBuddyAuthState, value any) error {
	raw, err := common.Marshal(value)
	if err == nil {
		state.UpstreamState = string(raw)
	}
	return err
}

func SelfTraeURLStart(c *gin.Context) {
	identity, origin, ok := accountURLIdentity(c)
	if !ok {
		return
	}
	var request accountURLStartRequest
	if c.ShouldBindJSON(&request) != nil || request.Realm != "" && request.Realm != "cn" || len(request.DisplayName) > 128 || !request.ConfirmRotation {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	callback, err := generatedLoopbackCallback("/authorize")
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	// The current CN client uses a 16-digit device ID and a 16-byte hex
	// machine ID, persisted unchanged through provider verification and relay.
	machineBytes := make([]byte, 16)
	if _, err := rand.Read(machineBytes); err != nil {
		accountAuthorizationError(c, err)
		return
	}
	deviceNumber, err := rand.Int(rand.Reader, big.NewInt(9000000000000000))
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	machine, device := hex.EncodeToString(machineBytes), strconv.FormatInt(1000000000000000+deviceNumber.Int64(), 10)
	trace := uuid.NewString()
	query := url.Values{
		"login_version": {"1"}, "auth_from": {"solo"}, "login_channel": {"native_ide"},
		"auth_type": {"local"}, "client_id": {trae.IDEClientID}, "redirect": {"0"},
		"login_trace_id": {trace}, "auth_callback_url": {callback},
		"machine_id": {machine}, "device_id": {device}, "x_device_id": {device}, "x_machine_id": {machine},
		"x_device_brand": {"ASUS TUF Gaming A15 FA507RM_FA507RM"}, "x_device_type": {"windows"},
		"x_os_version": {"Windows 10 Pro"}, "x_env": {""}, "x_app_version": {"3.3.65"},
		"x_app_type": {"stable"}, "hide_saas_login": {"false"},
	}
	settings := accountLoginProxySettings("cn", request.Proxy, request.ProxyPolicy)
	flow, err := newAccountFlow(identity, "trae", "cn", strings.TrimSpace(request.DisplayName), request.Public, settings, (&trae.Adaptor{}).GetModelList())
	if err == nil {
		err = model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
			state.Status = "bridge_pending"
			return saveAccountURLState(state, &traeURLState{traeBridgeState: traeBridgeState{Kind: traeBridgeKind, Origin: origin, ExpiresAt: time.Now().Add(10 * time.Minute).Unix(), MachineID: machine, DeviceID: device}, URLMode: true, ExpectedCallback: callback, LoginTrace: trace})
		})
	}
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"sessionId": flow, "authUrl": trae.ConsoleHost + "/authorization?" + query.Encode(), "expiresIn": 600, "interval": 2, "realm": "cn",
		"manualCallbackSupported": true, "automaticCallbackSupported": false, "requiresConsent": true, "expectedCallback": callback,
		"instructions": "Trae permits only a local 127.0.0.1 callback, not a gateway callback. Open this official authorization URL, finish login, then paste the complete returned local callback URL here. A local connection error is expected without a listener. If the page does not expose a returned URL, this manual flow cannot finish. No download is needed. Confirm the provider-verified account before linking.",
	}})
}

func SelfTraeURLComplete(c *gin.Context) {
	identity, origin, ok := accountURLIdentity(c)
	if !ok {
		return
	}
	var request accountURLRequest
	if c.ShouldBindJSON(&request) != nil || len(request.CallbackURL) > 49152 {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	info, err := trae.ParseCallback(request.CallbackURL)
	if err != nil || info.UID == "" || info.ClientID != trae.IDEClientID {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	err = model.WithWorkBuddyAuthFlow(request.SessionID, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		bridge, err := readTraeBridge(state, origin)
		if err != nil {
			return err
		}
		var generated traeURLState
		if common.UnmarshalJsonStr(state.UpstreamState, &generated) != nil || !generated.URLMode || generated.ExpectedCallback == "" {
			return model.ErrAuthFlowInvalid
		}
		u, err := url.Parse(strings.TrimSpace(request.CallbackURL))
		if err != nil {
			return model.ErrAuthFlowInvalid
		}
		u.RawQuery, u.ForceQuery = "", false
		if u.String() != generated.ExpectedCallback {
			return model.ErrAuthFlowInvalid
		}
		if bridge.UploadUsed || state.Status != "bridge_pending" {
			return model.ErrAuthFlowConsumed
		}
		// This endpoint is authenticated by the live initiating dashboard session;
		// no upload capability, callback nonce or fabricated provider state exists.
		bridge.UploadUsed, bridge.Callback = true, strings.TrimSpace(request.CallbackURL)
		bridge.CapabilityDigest = ""
		state.Status = "bridge_uploaded"
		return saveTraeBridge(state, bridge)
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	respondTraeURL(c, identity, request.SessionID, origin)
}

func respondTraeURL(c *gin.Context, identity model.AuthSessionIdentity, token, origin string) {
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
	status = "waiting_for_callback"
	if bridge != nil && bridge.RedeemNonce != "" {
		status = "pending"
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": false, "status": status, "interval": 2}})
}

func SelfTraeURLPoll(c *gin.Context) {
	identity, origin, ok := accountURLIdentity(c)
	if !ok {
		return
	}
	var request accountURLRequest
	if c.ShouldBindJSON(&request) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	respondTraeURL(c, identity, request.SessionID, origin)
}

// The existing cancellation/consent routines validate the same encrypted bridge
// record and original live dashboard session. No duplicate consent mechanism.
func SelfTraeURLCancel(c *gin.Context) {
	if _, _, ok := accountURLIdentity(c); !ok {
		return
	}
	SelfTraeBridgeCancel(c)
}

func readRaccoonURL(state *model.WorkBuddyAuthState, origin string) (*raccoonURLState, error) {
	var flow raccoonURLState
	if state.Provider != "raccoon" || state.Realm != "cn" || common.UnmarshalJsonStr(state.UpstreamState, &flow) != nil || flow.Kind != raccoonURLKind || flow.Origin != origin {
		return nil, model.ErrAuthFlowInvalid
	}
	if flow.ExpiresAt <= time.Now().Unix() {
		return nil, model.ErrAuthFlowExpired
	}
	return &flow, nil
}

func SelfRaccoonURLStart(c *gin.Context) {
	identity, origin, ok := accountURLIdentity(c)
	if !ok {
		return
	}
	var request accountURLStartRequest
	if c.ShouldBindJSON(&request) != nil || request.Realm != "" && request.Realm != "cn" || len(request.DisplayName) > 128 {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	callback, err := generatedLoopbackCallback("/auth/callback")
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		accountAuthorizationError(c, err)
		return
	}
	stateValue := hex.EncodeToString(nonce)
	query := url.Values{"login_source": {"desktop"}, "appname": {"办公小浣熊客户端"}, "state": {stateValue}, "desktop_callback": {"loopback"}, "redirect": {callback}}
	settings := accountLoginProxySettings("cn", request.Proxy, request.ProxyPolicy)
	flow, err := newAccountFlow(identity, "raccoon", "cn", strings.TrimSpace(request.DisplayName), request.Public, settings, nil)
	if err == nil {
		err = model.WithWorkBuddyAuthFlow(flow, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
			state.Status = "url_pending"
			return saveAccountURLState(state, &raccoonURLState{Kind: raccoonURLKind, Origin: origin, ExpectedCallback: callback, State: stateValue, ExpiresAt: time.Now().Add(10 * time.Minute).Unix()})
		})
	}
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"sessionId": flow, "authUrl": service.RaccoonOrigin + "/code/authorize?" + query.Encode(), "expiresIn": 600, "interval": 2, "realm": "cn", "manualCallbackSupported": true, "automaticCallbackSupported": false, "requiresConsent": true, "expectedCallback": callback, "instructions": "Raccoon permits only a desktop local callback, not a gateway callback. Open this official URL, finish authorization, then paste its complete returned 127.0.0.1 callback URL, including code and state. A local connection error is expected without a listener. No download is needed. A callback without the exact state cannot be accepted. Confirm the verified account before linking."}})
}

// parseRaccoonReturnedURL accepts only the generated loopback endpoint or the
// official desktop custom protocol. Both require an exact 64-hex provider state,
// a single code, and no credential fields. Never fetch any returned URL.
func parseRaccoonReturnedURL(raw string, flow *raccoonURLState) (string, error) {
	if flow == nil || len(raw) > 16384 || len(flow.State) != 64 {
		return "", model.ErrAuthFlowInvalid
	}
	if _, err := hex.DecodeString(flow.State); err != nil {
		return "", model.ErrAuthFlowInvalid
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || u.Opaque != "" || u.Fragment != "" || u.RawPath != "" {
		return "", model.ErrAuthFlowInvalid
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", model.ErrAuthFlowInvalid
	}
	u.RawQuery, u.ForceQuery = "", false
	if u.String() != flow.ExpectedCallback && (u.Scheme != "office-raccoon" || u.Host != "auth" || u.Path != "/callback") {
		return "", model.ErrAuthFlowInvalid
	}
	for key, values := range query {
		if len(values) != 1 || key != "code" && key != "state" {
			return "", model.ErrAuthFlowInvalid
		}
	}
	state := query.Get("state")
	code := query.Get("code")
	if len(state) != 64 || subtle.ConstantTimeCompare([]byte(flow.State), []byte(state)) != 1 || code == "" || len(code) > 4096 || strings.TrimSpace(code) != code || strings.ContainsFunc(code, unicode.IsControl) {
		return "", model.ErrAuthFlowInvalid
	}
	return code, nil
}

func SelfRaccoonURLComplete(c *gin.Context) {
	identity, origin, ok := accountURLIdentity(c)
	if !ok {
		return
	}
	var request accountURLRequest
	if c.ShouldBindJSON(&request) != nil || len(request.CallbackURL) > 16384 {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	var snapshot model.WorkBuddyAuthState
	var nonce, code string
	err := model.WithWorkBuddyAuthFlow(request.SessionID, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		flow, err := readRaccoonURL(state, origin)
		if err != nil {
			return err
		}
		if state.Status != "url_pending" {
			return model.ErrAuthFlowConsumed
		}
		code, err = parseRaccoonReturnedURL(request.CallbackURL, flow)
		if err != nil {
			return err
		}
		if err := model.ClaimExternalAuthAssertionWithTx(tx, "raccoon_url", code, time.Now().Add(24*time.Hour)); err != nil {
			return err
		}
		nonce = uuid.NewString()
		flow.RedeemNonce, flow.RedeemUntil = nonce, min(flow.ExpiresAt, time.Now().Add(45*time.Second).Unix())
		state.Status = "url_redeeming"
		if err := saveAccountURLState(state, flow); err != nil {
			return err
		}
		snapshot = *state
		return nil
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	// The only database claim is committed before resolving egress and making
	// provider HTTP. Cancellation, revocation and unrelated writes stay live.
	egress := service.AccountEgress{Key: snapshot.EgressKey, Proxy: snapshot.Proxy, EndpointID: snapshot.EndpointID, Fingerprint: snapshot.EndpointFingerprint}
	proxy, providerErr := egress.Resolve(identity.UserID, 0, snapshot.Settings)
	var credential *service.RaccoonCredential
	var models []string
	if providerErr == nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
		credential, models, providerErr = service.CompleteRaccoonAuthorization(ctx, proxy, code)
		cancel()
	}
	err = model.WithWorkBuddyAuthFlow(request.SessionID, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		flow, err := readRaccoonURL(state, origin)
		if err != nil {
			return err
		}
		if state.Status != "url_redeeming" || flow.RedeemUntil <= time.Now().Unix() || subtle.ConstantTimeCompare([]byte(flow.RedeemNonce), []byte(nonce)) != 1 {
			return model.ErrAuthFlowConsumed
		}
		flow.RedeemNonce, flow.RedeemUntil = "", 0
		if providerErr != nil {
			state.Status = "failed"
			return nil
		}
		credential.Egress = egress
		raw, err := common.Marshal(credential)
		if err != nil {
			return err
		}
		flow.ConsentToken, flow.ConsentUID = uuid.NewString(), credential.UserID
		state.Credential, state.Models, state.Status = string(raw), models, "url_consent"
		return saveAccountURLState(state, flow)
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if providerErr != nil {
		accountAuthorizationError(c, providerErr)
		return
	}
	respondRaccoonURL(c, identity, request.SessionID, origin)
}

func respondRaccoonURL(c *gin.Context, identity model.AuthSessionIdentity, token, origin string) {
	var snapshot model.WorkBuddyAuthState
	var flow *raccoonURLState
	var outcome error
	err := model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "raccoon" || state.Realm != "cn" {
			return model.ErrAuthFlowInvalid
		}
		if state.Status != "done" {
			var err error
			flow, err = readRaccoonURL(state, origin)
			if err != nil {
				return err
			}
			if state.Status == "url_redeeming" && flow.RedeemUntil <= time.Now().Unix() {
				state.Status, outcome = "failed", model.ErrAuthFlowConsumed
			}
		}
		snapshot = *state
		return nil
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if outcome != nil {
		accountAuthorizationError(c, outcome)
		return
	}
	if snapshot.Status == "url_consent" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": false, "status": "awaiting_consent", "requiresConsent": true, "consentToken": flow.ConsentToken, "account": gin.H{"uid": flow.ConsentUID, "nickname": ""}, "interval": 2}})
		return
	}
	if snapshot.Status == "binding" || snapshot.Status == "done" {
		channel, err := finishDesktopAccountChannel(identity, token, "raccoon")
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
	status := "pending"
	if snapshot.Status == "url_pending" {
		status = "waiting_for_callback"
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": false, "status": status, "interval": 2}})
}

func SelfRaccoonURLPoll(c *gin.Context) {
	identity, origin, ok := accountURLIdentity(c)
	if !ok {
		return
	}
	var request accountURLRequest
	if c.ShouldBindJSON(&request) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	respondRaccoonURL(c, identity, request.SessionID, origin)
}

func SelfRaccoonURLConsent(c *gin.Context) {
	identity, origin, ok := accountURLIdentity(c)
	if !ok {
		return
	}
	var request struct {
		SessionID    string `json:"sessionId"`
		ConsentToken string `json:"consentToken"`
		UID          string `json:"uid"`
	}
	if c.ShouldBindJSON(&request) != nil || len(request.ConsentToken) != 36 || request.UID == "" {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	var snapshot model.WorkBuddyAuthState
	err := model.WithWorkBuddyAuthFlow(request.SessionID, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		if _, err := readRaccoonURL(state, origin); err != nil {
			return err
		}
		if state.Status != "url_consent" {
			return model.ErrAuthFlowConsumed
		}
		snapshot = *state
		return nil
	})
	if err == nil {
		_, err = (service.AccountEgress{Key: snapshot.EgressKey, Proxy: snapshot.Proxy, EndpointID: snapshot.EndpointID, Fingerprint: snapshot.EndpointFingerprint}).Resolve(identity.UserID, 0, snapshot.Settings)
	}
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	err = model.WithWorkBuddyAuthFlow(request.SessionID, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		flow, err := readRaccoonURL(state, origin)
		if err != nil {
			return err
		}
		if state.Status != "url_consent" || subtle.ConstantTimeCompare([]byte(flow.ConsentToken), []byte(request.ConsentToken)) != 1 || flow.ConsentUID != request.UID {
			return model.ErrAuthFlowInvalid
		}
		flow.ConsentToken = ""
		if err := saveAccountURLState(state, flow); err != nil {
			return err
		}
		return model.StageWorkBuddyChannel(tx, state, constant.ChannelTypeRaccoon)
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	respondRaccoonURL(c, identity, request.SessionID, origin)
}

func SelfRaccoonURLCancel(c *gin.Context) {
	identity, origin, ok := accountURLIdentity(c)
	if !ok {
		return
	}
	var request accountURLRequest
	if c.ShouldBindJSON(&request) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	err := model.WithWorkBuddyAuthFlow(request.SessionID, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		if _, err := readRaccoonURL(state, origin); err != nil {
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

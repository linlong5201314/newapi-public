package controller

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/trae"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"gorm.io/gorm"
)

// ImportTraeAuthorization accepts only official CN local callback credentials or
// an explicitly confirmed legacy refresh token. It is not an OAuth code grant.
func ImportTraeAuthorization(ctx context.Context, identity model.AuthSessionIdentity, callback, refresh, uid, machine, device, name string, public, confirmRotation bool, settings dto.ChannelSettings) (string, error) {
	info := &trae.CallbackInfo{RefreshToken: strings.TrimSpace(refresh), UID: strings.TrimSpace(uid), ClientID: trae.ClientID, AuthHost: trae.DomesticAuthHost}
	if callback != "" {
		if info.RefreshToken != "" {
			return "", model.ErrAuthFlowInvalid
		}
		var err error
		info, err = trae.ParseCallback(callback)
		if err != nil || uid != "" && info.UID != "" && uid != info.UID {
			return "", model.ErrAuthFlowInvalid
		}
		if info.UID == "" {
			info.UID = strings.TrimSpace(uid)
		}
	}
	if info.RefreshToken == "" && info.AccessToken == "" || !confirmRotation || len(name) > 128 || len(machine) > 256 || len(device) > 256 {
		return "", model.ErrAuthFlowInvalid
	}
	token, err := newAccountFlow(identity, "trae", "cn", name, public, settings, (&trae.Adaptor{}).GetModelList())
	if err != nil {
		return "", err
	}
	var upstreamErr error
	err = model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		if err := claimTraeAuthorization(tx, info); err != nil {
			return err
		}
		credential, err := RedeemTraeAuthorization(ctx, state.Proxy, info, machine, device)
		if err != nil {
			state.Status, upstreamErr = "failed", err
			return nil
		}
		raw, err := common.Marshal(credential)
		if err != nil {
			return err
		}
		state.Credential, state.Status = string(raw), "ready"
		return nil
	})
	if err != nil {
		// Rejected assertions must not leave an unusable starting ceremony
		// occupying the owner's bounded pending authorization slots.
		_ = CancelAccountAuthorization(identity, token)
		return "", err
	}
	if upstreamErr != nil {
		return "", upstreamErr
	}
	return token, nil
}

// Claim both tokens, not just the refresh token: removing the refreshToken field
// from an already-imported callback must not permit a second channel binding.
// The caller owns the transaction so a replay cannot partially claim a pair.
func claimTraeAuthorization(tx *gorm.DB, info *trae.CallbackInfo) error {
	if info == nil || info.RefreshToken == "" && info.AccessToken == "" {
		return model.ErrAuthFlowInvalid
	}
	expires := time.Now().Add(24 * time.Hour)
	if expiry := time.Unix(info.ExpiresAt, 0); expiry.After(expires) {
		expires = expiry
	}
	for _, assertion := range []string{info.RefreshToken, info.AccessToken} {
		if assertion == "" {
			continue
		}
		if err := model.ClaimExternalAuthAssertionWithTx(tx, "trae_import", assertion, expires); err != nil {
			return err
		}
	}
	return nil
}

// RedeemTraeAuthorization verifies a parsed CN callback with the provider. It
// neither creates a flow nor persists/binds a channel: the caller must validate
// its owner/session, claim the assertion once, and keep proxy egress stable.
// A fresh token pair is preserved exactly as the current Magpie client does;
// only a legacy or expired pair needs ExchangeToken and explicit rotation consent.
func RedeemTraeAuthorization(ctx context.Context, proxy string, info *trae.CallbackInfo, machine, device string) (*trae.Cred, error) {
	if info == nil || info.RefreshToken == "" && info.AccessToken == "" || len(machine) > 256 || len(device) > 256 {
		return nil, model.ErrAuthFlowInvalid
	}
	if info.AuthHost != "" && info.AuthHost != trae.DomesticAuthHost || info.ClientID != "" && info.ClientID != trae.IDEClientID && info.ClientID != trae.ClientID {
		return nil, model.ErrAuthFlowInvalid
	}
	clientID := info.ClientID
	if clientID == "" {
		clientID = trae.ClientID
	}
	if machine == "" || device == "" {
		generatedMachine, generatedDevice := trae.NewDeviceIDs()
		if machine == "" {
			machine = generatedMachine
		}
		if device == "" {
			device = generatedDevice
		}
	}
		credential := &trae.Cred{AccessToken: info.AccessToken, RefreshToken: info.RefreshToken, UID: info.UID,
			MachineID: machine, DeviceID: device, Domain: "trae.cn", ApiHost: trae.DomesticAuthHost,
			ClientID: clientID, ExpiresAt: info.ExpiresAt}
		if credential.ExpiresAt <= 0 {
			credential.ExpiresAt = trae.CallbackTokenExpiry(credential.AccessToken)
		}
		// Imported desktop/Magpie sessions may already carry a fresh token pair.
		// Do not rotate that pair merely because its refresh token is present; the
		// provider treats refresh tokens as one-use and an unnecessary exchange can
		// sign out the still-active desktop client.
		if credential.AccessToken == "" || credential.ExpiresAt <= time.Now().Unix() {
		if credential.RefreshToken == "" {
			return nil, service.ErrAccountProtocol
		}
		body, err := common.Marshal(map[string]any{"ClientID": credential.ClientID, "RefreshToken": credential.RefreshToken, "ClientSecret": "-", "UserID": ""})
		if err != nil {
			return nil, service.ErrAccountProtocol
		}
		request, err := http.NewRequest(http.MethodPost, trae.DomesticAuthHost+trae.EpExchange, bytes.NewReader(body))
		if err != nil {
			return nil, service.ErrAccountProtocol
		}
		trae.OAuthHeaders(request)
		var response struct {
			Result struct {
				Token               string
				RefreshToken        string
				TokenExpireAt       int64
				TokenExpireDuration int64
				ClientID            string
			}
		}
		if err := service.AccountAuthorizationJSON(ctx, proxy, request, &response); err != nil {
			return nil, err
		}
		if response.Result.Token == "" || response.Result.ClientID != "" && response.Result.ClientID != credential.ClientID {
			return nil, service.ErrAccountProtocol
		}
		credential.AccessToken = response.Result.Token
		if response.Result.RefreshToken != "" {
			credential.RefreshToken = response.Result.RefreshToken
		}
		credential.ExpiresAt = trae.NormalizeExpire(response.Result.TokenExpireAt)
		if credential.ExpiresAt <= 0 && response.Result.TokenExpireDuration > 0 && response.Result.TokenExpireDuration < 31536000 {
			credential.ExpiresAt = time.Now().Add(time.Duration(response.Result.TokenExpireDuration) * time.Second).Unix()
		}
		if credential.ExpiresAt <= 0 {
			credential.ExpiresAt = trae.CallbackTokenExpiry(credential.AccessToken)
		}
	}
	if credential.ExpiresAt <= time.Now().Unix() {
		return nil, service.ErrAccountProtocol
	}
	body, err := common.Marshal(map[string]any{"ReqSource": "IDE", "IDEVersion": trae.IdeVersion})
	if err != nil {
		return nil, service.ErrAccountProtocol
	}
	request, err := http.NewRequest(http.MethodPost, trae.DomesticAuthHost+trae.EpUserInfo, bytes.NewReader(body))
	if err != nil {
		return nil, service.ErrAccountProtocol
	}
	trae.OAuthHeaders(request)
	request.Header.Set("X-Cloudide-Token", credential.AccessToken)
	request.Header.Set("Authorization", "Cloud-IDE-JWT "+credential.AccessToken)
	var response struct {
		Result struct {
			UserID       string
			ScreenName   string
			EnterpriseID string
			TenantID     string
		}
	}
	if err := service.AccountAuthorizationJSON(ctx, proxy, request, &response); err != nil {
		return nil, err
	}
	if response.Result.UserID == "" || credential.UID != "" && credential.UID != response.Result.UserID {
		return nil, service.ErrAccountProtocol
	}
	credential.UID, credential.Nickname, credential.EnterpriseID = response.Result.UserID, response.Result.ScreenName, response.Result.EnterpriseID
	if credential.EnterpriseID == "" {
		credential.EnterpriseID = response.Result.TenantID
	}
	return credential, nil
}

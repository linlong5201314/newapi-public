package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SelfImportRaccoon accepts one already-authorized desktop/Magpie credential.
// The browser only selects and submits a single JSON object; server code never
// reads arbitrary local paths. Provider verification remains mandatory.
func SelfImportRaccoon(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req struct {
		DisplayName string                  `json:"displayName"`
		Public      bool                    `json:"public"`
		Proxy       string                  `json:"proxy"`
		ProxyPolicy *dto.ChannelProxyPolicy `json:"proxyPolicy"`
		Credentials json.RawMessage         `json:"credentials"`
	}
	if c.ShouldBindJSON(&req) != nil || len(req.DisplayName) > 128 || len(req.Credentials) > 65536 {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	var raw struct {
		Token         string   `json:"token"`
		AccessToken   string   `json:"accessToken"`
		RefreshToken  string   `json:"refreshToken"`
		LocalVerified bool     `json:"localVerified"`
		Models        []string `json:"models"`
	}
	if common.Unmarshal(req.Credentials, &raw) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	access := strings.TrimSpace(raw.Token)
	if access == "" {
		access = strings.TrimSpace(raw.AccessToken)
	}
	refresh := strings.TrimSpace(raw.RefreshToken)
	if access == "" || refresh == "" || len(access) > 32768 || len(refresh) > 32768 || strings.ContainsFunc(access+refresh, unicode.IsControl) {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	settings := accountLoginProxySettings("cn", req.Proxy, req.ProxyPolicy)
	token, err := newAccountFlow(identity, "raccoon", "cn", strings.TrimSpace(req.DisplayName), req.Public, settings, nil)
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	var snapshot model.WorkBuddyAuthState
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		snapshot = *state
		return nil
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	credential := &service.RaccoonCredential{Token: access, RefreshToken: refresh}
	providerErr := error(nil)
	if raw.LocalVerified {
		providerErr = service.ValidateRaccoonLocalSession(credential)
	} else {
		providerErr = service.VerifyRaccoonImportedCredential(c.Request.Context(), snapshot.Proxy, credential)
	}
	var models []string
	if raw.LocalVerified && len(raw.Models) > 0 {
		models = raw.Models
	} else if providerErr == nil {
		models, providerErr = service.RaccoonLiveModels(c.Request.Context(), snapshot.Proxy, credential.Token)
	}
	if providerErr == nil {
		providerErr = model.ClaimExternalAuthAssertionWithTx(model.DB, "raccoon_import", access, time.Now().Add(24*time.Hour))
	}
	err = model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "raccoon" || state.Status != "starting" {
			return model.ErrAuthFlowConsumed
		}
		if providerErr != nil {
			state.Status = "failed"
			return nil
		}
		credential.Egress = service.AccountEgress{Key: state.EgressKey, Proxy: state.Proxy, EndpointID: state.EndpointID, Fingerprint: state.EndpointFingerprint}
		raw, err := common.Marshal(credential)
		if err != nil {
			return err
		}
		state.Credential, state.Models = string(raw), models
		return model.StageWorkBuddyChannel(tx, state, constant.ChannelTypeRaccoon)
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if providerErr != nil {
		accountAuthorizationError(c, providerErr)
		return
	}
	channel, err := finishDesktopAccountChannel(identity, token, "raccoon")
	if err != nil || channel == nil {
		if err == nil {
			err = service.ErrAccountProtocol
		}
		accountAuthorizationError(c, err)
		return
	}
	sanitizeSelfChannel(channel)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": true, "channel": channel}})
}

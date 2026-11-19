package controller

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type zcodeAuthorizationRequest struct {
	SessionID   string                  `json:"sessionId"`
	Realm       string                  `json:"realm"`
	DisplayName string                  `json:"displayName"`
	Public      bool                    `json:"public"`
	Proxy       string                  `json:"proxy"`
	ProxyPolicy *dto.ChannelProxyPolicy `json:"proxyPolicy"`
}

type zcodePollClaim struct {
	service.ZCodeTicket
	Nonce      string `json:"nonce,omitempty"`
	LeaseUntil int64  `json:"leaseUntil,omitempty"`
}

func zcodeAuthorizationError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrZCodePlanUnavailable) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "code": "ZCODE_PLAN_UNAVAILABLE", "message": common.NewMessage("ZCode Start Plan has no active supported model entitlement with available quota")})
		return
	}
	desktopAccountError(c, err)
}

func SelfZCodeOAuthStart(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req zcodeAuthorizationRequest
	if c.ShouldBindJSON(&req) != nil || len(req.DisplayName) > 128 || (req.Realm != "bigmodel" && req.Realm != "zai") {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	settings := accountLoginProxySettings("global", req.Proxy, req.ProxyPolicy)
	token, err := newAccountFlow(identity, "zcode", req.Realm, strings.TrimSpace(req.DisplayName), req.Public, settings, nil)
	if err != nil {
		zcodeAuthorizationError(c, err)
		return
	}
	var snapshot model.WorkBuddyAuthState
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error { snapshot = *state; return nil })
	if err != nil {
		zcodeAuthorizationError(c, err)
		return
	}
	ticket, authURL, providerErr := service.StartZCodeSignIn(c.Request.Context(), snapshot.Proxy, req.Realm)
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "zcode" || state.Status != "starting" {
			return model.ErrAuthFlowConsumed
		}
		if providerErr != nil {
			state.Status = "failed"
			return nil
		}
		raw, err := common.Marshal(zcodePollClaim{ZCodeTicket: *ticket})
		if err != nil {
			return err
		}
		state.UpstreamState = string(raw)
		state.Status = "pending"
		state.NextPollAt = time.Now().Add(time.Duration(ticket.Interval) * time.Second).Unix()
		return nil
	})
	if err != nil {
		zcodeAuthorizationError(c, err)
		return
	}
	if providerErr != nil {
		zcodeAuthorizationError(c, providerErr)
		return
	}
	common.ApiSuccess(c, gin.H{"sessionId": token, "authUrl": authURL, "expiresIn": max(1, int(ticket.Until-time.Now().Unix())), "interval": ticket.Interval})
}

func withZCodeClaim(token string, identity model.AuthSessionIdentity, nonce string, action func(*gorm.DB, *model.WorkBuddyAuthState, *zcodePollClaim) error) error {
	return model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "zcode" || (state.Status != "polling" && state.Status != "validating") {
			return model.ErrAuthFlowConsumed
		}
		var claim zcodePollClaim
		if common.Unmarshal([]byte(state.UpstreamState), &claim) != nil || claim.Nonce != nonce || claim.LeaseUntil <= time.Now().Unix() {
			return model.ErrAuthFlowConsumed
		}
		if err := action(tx, state, &claim); err != nil {
			return err
		}
		if state.Status == "pending" {
			claim.Nonce = ""
			claim.LeaseUntil = 0
		}
		raw, err := common.Marshal(claim)
		if err != nil {
			return err
		}
		state.UpstreamState = string(raw)
		return nil
	})
}

func SelfZCodeOAuthPoll(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req zcodeAuthorizationRequest
	if c.ShouldBindJSON(&req) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	token := strings.TrimSpace(req.SessionID)
	var snapshot model.WorkBuddyAuthState
	err := model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "zcode" {
			return model.ErrAuthFlowInvalid
		}
		snapshot = *state
		return nil
	})
	if err != nil {
		zcodeAuthorizationError(c, err)
		return
	}
	proxy := snapshot.Proxy
	if snapshot.Status != "done" {
		proxy, err = (service.AccountEgress{Key: snapshot.EgressKey, Proxy: snapshot.Proxy, EndpointID: snapshot.EndpointID, Fingerprint: snapshot.EndpointFingerprint}).Resolve(identity.UserID, 0, snapshot.Settings)
		if err != nil {
			zcodeAuthorizationError(c, err)
			return
		}
	}
	nonce := uuid.NewString()
	claimed := false
	var claim zcodePollClaim
	var outcome error
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "zcode" {
			return model.ErrAuthFlowInvalid
		}
		if state.Status == "done" || state.Status == "binding" {
			return nil
		}
		if state.Status == "failed" || state.Status == "cancelled" || state.Status == "starting" {
			return model.ErrAuthFlowConsumed
		}
		if common.Unmarshal([]byte(state.UpstreamState), &claim) != nil || claim.Provider != state.Realm {
			return service.ErrAccountProtocol
		}
		if state.Status != "pending" {
			if state.Status != "polling" && state.Status != "validating" {
				return model.ErrAuthFlowInvalid
			}
			if claim.LeaseUntil > time.Now().Unix() {
				return service.ErrAccountPollEarly
			}
			if state.Credential == "" {
				state.Status = "failed"
				outcome = model.ErrAuthFlowConsumed
				return nil
			}
		}
		if state.NextPollAt > time.Now().Unix() {
			return service.ErrAccountPollEarly
		}
		if state.Credential == "" && claim.Until <= time.Now().Unix() {
			state.Status = "failed"
			outcome = model.ErrAuthFlowExpired
			return nil
		}
		claim.Nonce = nonce
		claim.LeaseUntil = time.Now().Add(30 * time.Second).Unix()
		raw, err := common.Marshal(claim)
		if err != nil {
			return err
		}
		state.UpstreamState = string(raw)
		state.Proxy = proxy
		state.Status = "polling"
		if state.Credential != "" {
			state.Status = "validating"
		}
		snapshot = *state
		claimed = true
		return nil
	})
	if err != nil {
		zcodeAuthorizationError(c, err)
		return
	}
	if outcome != nil {
		zcodeAuthorizationError(c, outcome)
		return
	}
	if claimed {
		var credential *service.ZCodeCredential
		if snapshot.Credential == "" {
			credential, err = service.PollZCodeSignIn(c.Request.Context(), proxy, &claim.ZCodeTicket)
			providerErr := err
			err = withZCodeClaim(token, identity, nonce, func(_ *gorm.DB, state *model.WorkBuddyAuthState, stored *zcodePollClaim) error {
				if providerErr != nil {
					state.Status = "failed"
					return nil
				}
				if credential == nil {
					state.Status = "pending"
					state.NextPollAt = time.Now().Add(time.Duration(max(2, claim.Interval)) * time.Second).Unix()
					return nil
				}
				credential.Egress = service.AccountEgress{Key: state.EgressKey, Proxy: state.Proxy, EndpointID: state.EndpointID, Fingerprint: state.EndpointFingerprint}
				raw, err := common.Marshal(credential)
				if err != nil {
					return err
				}
				state.Credential = string(raw)
				state.Status = "validating"
				stored.FlowID = ""
				stored.PollToken = ""
				stored.LeaseUntil = time.Now().Add(30 * time.Second).Unix()
				return nil
			})
			if err != nil {
				zcodeAuthorizationError(c, err)
				return
			}
			if providerErr != nil {
				zcodeAuthorizationError(c, providerErr)
				return
			}
			if credential == nil {
				common.ApiSuccess(c, gin.H{"done": false, "interval": max(2, claim.Interval)})
				return
			}
		} else {
			credential = &service.ZCodeCredential{}
			if common.Unmarshal([]byte(snapshot.Credential), credential) != nil {
				zcodeAuthorizationError(c, service.ErrAccountProtocol)
				return
			}
		}
		models, providerErr := service.ValidateZCodeStartPlan(c.Request.Context(), proxy, credential)
		err = withZCodeClaim(token, identity, nonce, func(tx *gorm.DB, state *model.WorkBuddyAuthState, _ *zcodePollClaim) error {
			if providerErr != nil {
				state.Status = "pending"
				state.NextPollAt = time.Now().Add(3 * time.Second).Unix()
				if errors.Is(providerErr, service.ErrZCodePlanUnavailable) || errors.Is(providerErr, service.ErrDesktopLoginExpired) || errors.Is(providerErr, service.ErrAccountProtocol) {
					state.Status = "failed"
				}
				return nil
			}
			state.Models = models
			if stageErr := model.StageWorkBuddyChannel(tx, state, constant.ChannelTypeZCode); stageErr != nil {
				providerErr = stageErr
				state.Status = "pending"
				return nil
			}
			rawInfo, marshalErr := common.Marshal(map[string]any{"allModels": models, "templateId": "zcode-" + state.Realm})
			if marshalErr != nil {
				return marshalErr
			}
			return tx.Model(&model.Channel{}).Where("id = ? AND owner_id = ?", state.ChannelID, identity.UserID).Update("other_info", string(rawInfo)).Error
		})
		if err != nil {
			zcodeAuthorizationError(c, err)
			return
		}
		if providerErr != nil {
			zcodeAuthorizationError(c, providerErr)
			return
		}
	}
	channel, err := finishDesktopAccountChannel(identity, token, "zcode")
	if err != nil {
		zcodeAuthorizationError(c, err)
		return
	}
	if channel == nil {
		common.ApiSuccess(c, gin.H{"done": false, "interval": 2})
		return
	}
	sanitizeSelfChannel(channel)
	common.ApiSuccess(c, gin.H{"done": true, "channel": channel})
}

func SelfZCodeOAuthCancel(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req zcodeAuthorizationRequest
	if c.ShouldBindJSON(&req) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	err := model.WithWorkBuddyAuthFlow(strings.TrimSpace(req.SessionID), identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "zcode" {
			return model.ErrAuthFlowInvalid
		}
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
	if err != nil {
		zcodeAuthorizationError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

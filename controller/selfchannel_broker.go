package controller

import (
	"context"
	"net/url"
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

type brokerAccountRequest struct {
	SessionID   string                  `json:"sessionId"`
	Realm       string                  `json:"realm"`
	DisplayName string                  `json:"displayName"`
	Public      bool                    `json:"public"`
	Proxy       string                  `json:"proxy"`
	ProxyPolicy *dto.ChannelProxyPolicy `json:"proxyPolicy"`
	CallbackURL string                  `json:"callbackUrl"`
}
type brokerClaim struct {
	Ticket     common.RawMessage `json:"ticket,omitempty"`
	AuthURL    string            `json:"auth_url"`
	Until      int64             `json:"until"`
	Interval   int               `json:"interval"`
	Nonce      string            `json:"nonce,omitempty"`
	LeaseUntil int64             `json:"lease_until,omitempty"`
}

func brokerProvider(c *gin.Context) (string, int, bool) {
	switch c.Param("provider") {
	case "qoder":
		return "qoder", constant.ChannelTypeQoder, true
	case "cline":
		return "cline", constant.ChannelTypeCline, true
	case "nous":
		return "nous", constant.ChannelTypeNousPortal, true
	}
	return "", 0, false
}
func brokerStart(ctx context.Context, proxy, provider, realm string) (*brokerClaim, error) {
	var ticket any
	var target string
	var until int64
	var interval int
	var err error
	switch provider {
	case "qoder":
		var grant *service.QoderTicket
		grant, target, err = service.StartQoderSignIn(ctx, proxy, realm)
		if err == nil {
			ticket = grant
			until = grant.Until
			interval = grant.Interval
		}
	case "cline":
		var grant *service.ClineTicket
		grant, target, err = service.StartClineSignIn(ctx, proxy)
		if err == nil {
			ticket = grant
			until = grant.Until
			interval = grant.Interval
		}
	case "nous":
		var grant *service.NousTicket
		grant, target, err = service.StartNousSignIn(ctx, proxy)
		if err == nil {
			ticket = grant
			until = grant.Until
			interval = grant.Interval
		}
	default:
		return nil, model.ErrAuthFlowInvalid
	}
	if err != nil {
		return nil, err
	}
	raw, err := common.Marshal(ticket)
	if err != nil {
		return nil, err
	}
	return &brokerClaim{Ticket: raw, AuthURL: target, Until: until, Interval: max(2, interval)}, nil
}
func SelfBrokerAccountStart(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	provider, _, ok := brokerProvider(c)
	if !ok {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	var req brokerAccountRequest
	if c.ShouldBindJSON(&req) != nil || len(req.DisplayName) > 128 {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	realm := req.Realm
	if provider == "cline" || provider == "nous" {
		realm = "global"
	}
	if realm == "" {
		realm = "cn"
	}
	if realm != "cn" && realm != "global" {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	settings := accountLoginProxySettings("global", req.Proxy, req.ProxyPolicy)
	token, err := newAccountFlow(identity, provider, realm, strings.TrimSpace(req.DisplayName), req.Public, settings, nil)
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	var snapshot model.WorkBuddyAuthState
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error { snapshot = *s; return nil })
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	grant, upstreamErr := brokerStart(c.Request.Context(), snapshot.Proxy, provider, realm)
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
		if s.Provider != provider || s.Status != "starting" {
			return model.ErrAuthFlowConsumed
		}
		if upstreamErr != nil {
			s.Status = "failed"
			return nil
		}
		raw, err := common.Marshal(grant)
		if err != nil {
			return err
		}
		s.UpstreamState = string(raw)
		s.Status = "pending"
		s.NextPollAt = time.Now().Add(time.Duration(grant.Interval) * time.Second).Unix()
		return nil
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if upstreamErr != nil {
		accountAuthorizationError(c, upstreamErr)
		return
	}
	common.ApiSuccess(c, gin.H{"sessionId": token, "authUrl": grant.AuthURL, "expiresIn": max(1, int(grant.Until-time.Now().Unix())), "interval": grant.Interval, "automaticCallbackSupported": false, "manualCallbackSupported": false, "automaticPollingSupported": true, "completionMode": "device_poll"})
}

// Device grants are bound to the server-held verifier and original login. A
// pasted URL can request another status check, never supply an arbitrary token.
func brokerReturnedURL(raw, original string) bool {
	if len(raw) > 16384 {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	o, err := url.Parse(original)
	if err != nil {
		return false
	}
	if u.Host != o.Host || u.Path != o.Path {
		return false
	}
	bound := false
	for _, key := range []string{"nonce", "state", "user_code"} {
		if value := o.Query().Get(key); value != "" {
			bound = true
			if len(u.Query()[key]) != 1 || u.Query().Get(key) != value {
				return false
			}
		}
	}
	return bound
}
func brokerPollCredential(ctx context.Context, proxy, provider string, claim *brokerClaim, stored string, egress service.AccountEgress) (string, []string, error) {
	switch provider {
	case "qoder":
		var cred *service.QoderCredential
		var err error
		if stored == "" {
			var ticket service.QoderTicket
			if common.Unmarshal(claim.Ticket, &ticket) != nil {
				return "", nil, service.ErrAccountProtocol
			}
			cred, err = service.PollQoderSignIn(ctx, proxy, &ticket)
		} else {
			cred = &service.QoderCredential{}
			err = common.Unmarshal([]byte(stored), cred)
		}
		if err != nil || cred == nil {
			return "", nil, err
		}
		cred.Egress = egress
		raw, err := common.Marshal(cred)
		if err != nil {
			return "", nil, err
		}
		models, err := service.FetchQoderModels(ctx, proxy, cred)
		return string(raw), models, err
	case "cline":
		var cred *service.ClineCredential
		var err error
		if stored == "" {
			var ticket service.ClineTicket
			if common.Unmarshal(claim.Ticket, &ticket) != nil {
				return "", nil, service.ErrAccountProtocol
			}
			cred, err = service.PollClineSignIn(ctx, proxy, &ticket)
			claim.Interval = max(2, ticket.Interval)
			updated, _ := common.Marshal(ticket)
			claim.Ticket = updated
		} else {
			cred = &service.ClineCredential{}
			err = common.Unmarshal([]byte(stored), cred)
		}
		if err != nil || cred == nil {
			return "", nil, err
		}
		cred.Egress = egress
		raw, err := common.Marshal(cred)
		if err != nil {
			return "", nil, err
		}
		models, err := service.FetchClineModels(ctx, proxy, cred)
		return string(raw), models, err
	case "nous":
		var cred *service.NousCredential
		var err error
		if stored == "" {
			var ticket service.NousTicket
			if common.Unmarshal(claim.Ticket, &ticket) != nil {
				return "", nil, service.ErrAccountProtocol
			}
			cred, err = service.PollNousSignIn(ctx, proxy, &ticket)
		} else {
			cred = &service.NousCredential{}
			err = common.Unmarshal([]byte(stored), cred)
		}
		if err != nil || cred == nil {
			return "", nil, err
		}
		cred.Egress = egress
		raw, err := common.Marshal(cred)
		if err != nil {
			return "", nil, err
		}
		models, err := service.FetchNousModels(ctx, proxy, cred)
		return string(raw), models, err
	}
	return "", nil, model.ErrAuthFlowInvalid
}
func SelfBrokerAccountPoll(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	provider, channelType, ok := brokerProvider(c)
	if !ok {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	var req brokerAccountRequest
	if c.ShouldBindJSON(&req) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	token := strings.TrimSpace(req.SessionID)
	var snapshot model.WorkBuddyAuthState
	var claim brokerClaim
	err := model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
		if s.Provider != provider {
			return model.ErrAuthFlowInvalid
		}
		snapshot = *s
		return nil
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if snapshot.Status == "binding" || snapshot.Status == "done" {
		ch, err := finishDesktopAccountChannel(identity, token, provider)
		if err != nil {
			accountAuthorizationError(c, err)
			return
		}
		sanitizeSelfChannel(ch)
		common.ApiSuccess(c, gin.H{"done": true, "channel": ch})
		return
	}
	egress := service.AccountEgress{Key: snapshot.EgressKey, Proxy: snapshot.Proxy, EndpointID: snapshot.EndpointID, Fingerprint: snapshot.EndpointFingerprint}
	proxy, err := egress.Resolve(identity.UserID, 0, snapshot.Settings)
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	nonce := uuid.NewString()
	var expired bool
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
		if s.Provider != provider {
			return model.ErrAuthFlowInvalid
		}
		if s.Status != "pending" && s.Status != "polling" {
			return model.ErrAuthFlowConsumed
		}
		if common.Unmarshal([]byte(s.UpstreamState), &claim) != nil {
			return model.ErrAuthFlowInvalid
		}
		if req.CallbackURL != "" && !brokerReturnedURL(req.CallbackURL, claim.AuthURL) {
			return model.ErrAuthFlowInvalid
		}
		if s.Status == "polling" {
			if claim.LeaseUntil > time.Now().Unix() {
				return service.ErrAccountPollEarly
			}
			if s.Credential == "" {
				s.Status = "failed"
				expired = true
				return nil
			}
		}
		if s.Credential == "" && claim.Until <= time.Now().Unix() {
			s.Status = "failed"
			expired = true
			return nil
		}
		if s.NextPollAt > time.Now().Unix() {
			return service.ErrAccountPollEarly
		}
		claim.Nonce = nonce
		claim.LeaseUntil = time.Now().Add(45 * time.Second).Unix()
		raw, err := common.Marshal(claim)
		if err != nil {
			return err
		}
		s.UpstreamState = string(raw)
		s.Status = "polling"
		snapshot = *s
		return nil
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if expired {
		accountAuthorizationError(c, model.ErrAuthFlowExpired)
		return
	}
	credential, models, upstreamErr := brokerPollCredential(c.Request.Context(), proxy, provider, &claim, snapshot.Credential, egress)
	err = model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, s *model.WorkBuddyAuthState) error {
		if s.Provider != provider || s.Status != "polling" {
			return model.ErrAuthFlowConsumed
		}
		var live brokerClaim
		if common.Unmarshal([]byte(s.UpstreamState), &live) != nil || live.Nonce != nonce || live.LeaseUntil <= time.Now().Unix() {
			return model.ErrAuthFlowConsumed
		}
		live.Interval = claim.Interval
		if credential == "" && upstreamErr == nil {
			live.Ticket = claim.Ticket
		}
		if credential != "" {
			s.Credential = credential
			live.Ticket = nil
		}
		if upstreamErr != nil && credential == "" {
			s.Status = "failed"
			return nil
		}
		if upstreamErr == nil && credential != "" && len(models) > 0 {
			s.Models = models
			return model.StageWorkBuddyChannel(tx, s, channelType)
		}
		s.Status = "pending"
		s.NextPollAt = time.Now().Add(time.Duration(live.Interval) * time.Second).Unix()
		live.Nonce = ""
		live.LeaseUntil = 0
		raw, err := common.Marshal(live)
		s.UpstreamState = string(raw)
		return err
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if upstreamErr != nil {
		accountAuthorizationError(c, upstreamErr)
		return
	}
	ch, err := finishDesktopAccountChannel(identity, token, provider)
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	if ch == nil {
		common.ApiSuccess(c, gin.H{"done": false, "interval": claim.Interval})
		return
	}
	sanitizeSelfChannel(ch)
	common.ApiSuccess(c, gin.H{"done": true, "channel": ch})
}
func SelfBrokerAccountCancel(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	provider, _, ok := brokerProvider(c)
	if !ok {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	var req brokerAccountRequest
	if c.ShouldBindJSON(&req) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	err := model.WithWorkBuddyAuthFlow(req.SessionID, identity, func(tx *gorm.DB, s *model.WorkBuddyAuthState) error {
		if s.Provider != provider {
			return model.ErrAuthFlowInvalid
		}
		if s.Status == "done" {
			return model.ErrAuthFlowConsumed
		}
		if s.ChannelID > 0 {
			if err := tx.Where("channel_id = ?", s.ChannelID).Delete(&model.Ability{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id = ? AND owner_id = ? AND status = ?", s.ChannelID, identity.UserID, common.ChannelStatusManuallyDisabled).Delete(&model.Channel{}).Error; err != nil {
				return err
			}
		}
		s.Status = "cancelled"
		return nil
	})
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

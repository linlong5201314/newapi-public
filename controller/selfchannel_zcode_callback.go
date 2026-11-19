package controller

import (
	"net/http"
	"net/url"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func SelfZCodeURLComplete(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req struct {
		SessionID   string `json:"sessionId"`
		CallbackURL string `json:"callbackUrl"`
	}
	if c.ShouldBindJSON(&req) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	var snapshot model.WorkBuddyAuthState
	var ticket zcodePollClaim
	var outcome error
	err := model.WithWorkBuddyAuthFlow(req.SessionID, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
		if s.Provider != "zcode" || s.Status != "pending" {
			return model.ErrAuthFlowConsumed
		}
		if common.Unmarshal([]byte(s.UpstreamState), &ticket) != nil || ticket.BrowserReturnUsed || ticket.Provider != s.Realm {
			return model.ErrAuthFlowConsumed
		}
		if ticket.Until <= time.Now().Unix() {
			s.Status = "failed"
			outcome = model.ErrAuthFlowExpired
			return nil
		}
		if _, err := service.ValidateZCodeReturnedURL(ticket.AuthURL, req.CallbackURL, s.Realm); err != nil {
			return model.ErrAuthFlowInvalid
		}
		snapshot = *s
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
	proxy, err := (service.AccountEgress{Key: snapshot.EgressKey, Proxy: snapshot.Proxy, EndpointID: snapshot.EndpointID, Fingerprint: snapshot.EndpointFingerprint}).Resolve(identity.UserID, 0, snapshot.Settings)
	if err != nil {
		accountAuthorizationError(c, err)
		return
	}
	nonce := uuid.NewString()
	err = model.WithWorkBuddyAuthFlow(req.SessionID, identity, func(tx *gorm.DB, s *model.WorkBuddyAuthState) error {
		if s.Provider != "zcode" || s.Status != "pending" {
			return model.ErrAuthFlowConsumed
		}
		if common.Unmarshal([]byte(s.UpstreamState), &ticket) != nil || ticket.BrowserReturnUsed || ticket.Provider != s.Realm {
			return model.ErrAuthFlowConsumed
		}
		if ticket.Until <= time.Now().Unix() {
			s.Status = "failed"
			outcome = model.ErrAuthFlowExpired
			return nil
		}
		target, err := service.ValidateZCodeReturnedURL(ticket.AuthURL, req.CallbackURL, s.Realm)
		if err != nil {
			return model.ErrAuthFlowInvalid
		}
		// Replay identity is the validated assertion, not the caller's query ordering
		// or percent encoding. Never store the returned credential in plaintext.
		canonical, err := url.Parse(target)
		if err != nil {
			return model.ErrAuthFlowInvalid
		}
		canonical.RawQuery = canonical.Query().Encode()
		if err := model.ClaimExternalAuthAssertionWithTx(tx, "zcode_url_return", canonical.String(), time.Unix(ticket.Until, 0)); err != nil {
			return err
		}
		ticket.BrowserReturnUsed = true
		ticket.Nonce = nonce
		ticket.LeaseUntil = time.Now().Add(20 * time.Second).Unix()
		raw, err := common.Marshal(ticket)
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
	if outcome != nil {
		accountAuthorizationError(c, outcome)
		return
	}
	// Claim commits before HTTP; cancellation and session revocation can win while
	// the fixed provider callback is in flight. No dashboard credentials are sent.
	providerErr := service.CompleteZCodeBrowserReturn(c.Request.Context(), proxy, ticket.AuthURL, req.CallbackURL, snapshot.Realm)
	err = withZCodeClaim(req.SessionID, identity, nonce, func(_ *gorm.DB, s *model.WorkBuddyAuthState, claim *zcodePollClaim) error {
		if claim.Until <= time.Now().Unix() {
			s.Status = "failed"
			outcome = model.ErrAuthFlowExpired
		} else if providerErr != nil {
			s.Status = "failed"
		} else {
			s.Status = "pending"
			s.NextPollAt = time.Now().Add(2 * time.Second).Unix()
		}
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
	if providerErr != nil {
		accountAuthorizationError(c, providerErr)
		return
	}
	common.ApiSuccess(c, gin.H{"done": false, "status": "pending", "interval": 2, "message": http.StatusText(http.StatusAccepted)})
}

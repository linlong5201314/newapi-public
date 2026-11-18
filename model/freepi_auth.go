package model

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
)

const FreePiAuthPurpose = "freepi_device"

var ErrFreePiEncryptionRequired = errors.New("FreePi authorization requires encrypted credential storage")
var ErrFreePiAuthLimit = errors.New("Too many active FreePi authorization requests")

// FreePiAuthState never crosses the browser boundary. The whole payload is
// encrypted; the opaque state is HMACed by AuthFlow before persistence.
type FreePiAuthState struct {
	Proxy           string                  `json:"proxy,omitempty"`
	ProxyPolicy     *dto.ChannelProxyPolicy `json:"proxy_policy,omitempty"`
	EgressKey       string                  `json:"egress_key,omitempty"`
	Identity        AuthSessionIdentity     `json:"identity"`
	UpstreamSession string                  `json:"upstream_session,omitempty"`
	DisplayName     string                  `json:"display_name"`
	Public          bool                    `json:"public"`
	Interval        int                     `json:"interval"`
	NextPollAt      int64                   `json:"next_poll_at"`
	Status          string                  `json:"status"`
	ChannelID       int                     `json:"channel_id,omitempty"`
	ClientVersion   string                  `json:"client_version"`
	Models          []string                `json:"models"`
}

func CreateFreePiAuthFlow(state FreePiAuthState, expiresAt time.Time) (string, error) {
	if !common.CryptoSecretConfigured || strings.TrimSpace(common.CryptoSecret) == "" {
		return "", ErrFreePiEncryptionRequired
	}
	if state.UpstreamSession == "" || len(state.Models) == 0 || state.Interval < 1 {
		return "", ErrAuthFlowInvalid
	}
	raw, err := common.Marshal(state)
	if err != nil {
		return "", err
	}
	encrypted, err := common.EncryptSecret(string(raw))
	if err != nil {
		return "", err
	}
	if !common.IsEncryptedSecret(encrypted) {
		return "", ErrFreePiEncryptionRequired
	}
	var token string
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := ValidateAuthSessionWithTx(tx, state.Identity); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&AuthFlow{}).Where("purpose = ? AND user_id = ? AND consumed_at IS NULL AND expires_at > ?", FreePiAuthPurpose, state.Identity.UserID, time.Now()).Count(&count).Error; err != nil {
			return err
		}
		if count >= 3 {
			return ErrFreePiAuthLimit
		}
		var err error
		token, _, err = createAuthFlowWithTx(tx, AuthFlowCreate{Purpose: FreePiAuthPurpose, Provider: "freepi", Intent: AuthFlowIntentBind, UserId: state.Identity.UserID, SessionId: state.Identity.SessionID, Payload: encrypted, ExpiresAt: expiresAt})
		return err
	})
	return token, err
}

// WithFreePiAuthFlow serializes poll/consent/cancel across processes. Upstream
// calls use a bounded timeout while the transaction owns the ceremony; no other
// poll can redeem its one-shot upstream token concurrently. If upstream redeemed
// a token but committing fails, callers must restart (never report success).
func WithFreePiAuthFlow(token string, identity AuthSessionIdentity, action func(*gorm.DB, *FreePiAuthState) error) error {
	if len(token) != 43 || identity.UserID <= 0 || identity.SessionID == "" || action == nil {
		return ErrAuthFlowInvalid
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		// Take SQLite's write lock before reads. All paths lock user -> session
		// -> flow so polling and a concurrent restart cannot invert lock order.
		if err := tx.Model(&User{}).Where("id = ?", identity.UserID).UpdateColumn("auth_version", gorm.Expr("auth_version")).Error; err != nil {
			return err
		}
		if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
			return err
		}
		match := AuthFlowMatch{Purpose: FreePiAuthPurpose, Provider: "freepi", Intent: AuthFlowIntentBind, UserId: identity.UserID, SessionId: identity.SessionID}
		query := applyAuthFlowMatch(tx.Model(&AuthFlow{}), token, match)
		if err := query.UpdateColumn("payload", gorm.Expr("payload")).Error; err != nil {
			return err
		}
		var flow AuthFlow
		if err := applyAuthFlowMatch(lockForUpdate(tx), token, match).First(&flow).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAuthFlowInvalid
			}
			return err
		}
		if !flow.ExpiresAt.After(time.Now()) {
			return ErrAuthFlowExpired
		}
		if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
			return err
		}
		if !common.IsEncryptedSecret(flow.Payload) {
			return ErrFreePiEncryptionRequired
		}
		raw, err := common.DecryptSecret(flow.Payload)
		if err != nil {
			return err
		}
		var state FreePiAuthState
		if err := common.Unmarshal([]byte(raw), &state); err != nil {
			return err
		}
		if state.Identity != identity {
			return ErrAuthFlowInvalid
		}
		if flow.ConsumedAt != nil && state.Status != "done" {
			return ErrAuthFlowConsumed
		}
		if err := action(tx, &state); err != nil {
			return err
		}
		if !flow.ExpiresAt.After(time.Now()) {
			return ErrAuthFlowExpired
		}
		if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
			return err
		}
		rawBytes, err := common.Marshal(state)
		if err != nil {
			return err
		}
		encrypted, err := common.EncryptSecret(string(rawBytes))
		if err != nil {
			return err
		}
		if !common.IsEncryptedSecret(encrypted) {
			return ErrFreePiEncryptionRequired
		}
		updates := map[string]any{"payload": encrypted}
		if state.Status == "done" || state.Status == "cancelled" || state.Status == "failed" {
			updates["consumed_at"] = time.Now()
		}
		return tx.Model(&AuthFlow{}).Where("id = ?", flow.Id).Updates(updates).Error
	})
}

// CompleteFreePiChannel must run inside WithFreePiAuthFlow's transaction. Both
// the binding and channel (including abilities) commit with single-use state.
func CompleteFreePiChannel(tx *gorm.DB, state *FreePiAuthState, token, handle string, channelType int) error {
	if state.Status == "done" {
		return ErrAuthFlowConsumed
	}
	if strings.TrimSpace(token) == "" || strings.TrimSpace(handle) == "" || len(state.Models) == 0 || channelType <= 0 {
		return ErrAuthFlowInvalid
	}
	key, err := common.EncryptSecret(token)
	if err != nil {
		return err
	}
	if !common.IsEncryptedSecret(key) {
		return ErrFreePiEncryptionRequired
	}
	needsBinding := state.Proxy == "userpool" || state.ProxyPolicy != nil && state.ProxyPolicy.Mode != "direct" && state.ProxyPolicy.Mode != "fixed"
	base := "https://api.freepi.ai"
	channel := Channel{Type: channelType, Key: key, Name: state.DisplayName, BaseURL: &base, Models: strings.Join(state.Models, ","), Group: "default", OwnerId: state.Identity.UserID, Status: common.ChannelStatusEnabled, Tier: "api", CreatedTime: time.Now().Unix()}
	if state.Public {
		channel.Visibility = 1
	}
	if needsBinding {
		channel.Status = common.ChannelStatusAutoDisabled
	}
	channel.SetSetting(dto.ChannelSettings{Proxy: state.Proxy, ProxyPolicy: state.ProxyPolicy})
	channel.SetOtherInfo(map[string]any{"templateId": "freepi", "poolKey": "freepi", "allModels": state.Models, "freepiClientVersion": state.ClientVersion})
	if err := tx.Create(&channel).Error; err != nil {
		return err
	}
	if err := channel.AddAbilities(tx); err != nil {
		return err
	}
	binding := SelfAccountBinding{Proxy: state.Proxy, ProxyPolicy: state.ProxyPolicy, UserId: state.Identity.UserID, Provider: "freepi", Kind: "oauth", DisplayName: channel.Name, AccountMask: maskAccount(handle), AccountCount: 1, Public: channel.Visibility, Status: "ready", ChannelId: channel.Id}
	if needsBinding {
		binding.Status = "pending"
	}
	if err := binding.EncodeAccounts([]SelfAccountCred{{Account: handle, Password: token}}); err != nil {
		return err
	}
	if err := tx.Create(&binding).Error; err != nil {
		return err
	}
	state.ChannelID, state.Status, state.UpstreamSession = channel.Id, "done", ""
	if needsBinding {
		state.Status = "binding"
	}
	return nil
}

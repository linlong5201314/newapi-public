package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

const WorkBuddyAuthPurpose = "account_device"

var ErrWorkBuddyEncryptionRequired = errors.New("Account authorization requires encrypted credential storage")
var ErrWorkBuddyAuthLimit = errors.New("Account authorization limit reached")

// WorkBuddyAuthState is encrypted in AuthFlow.Payload, never returned to clients.
// Trae uses the same ceremony mechanics but never claims to implement browser OAuth.
type WorkBuddyAuthState struct {
	Identity            AuthSessionIdentity `json:"identity"`
	Provider            string              `json:"provider"`
	Realm               string              `json:"realm"`
	UpstreamState       string              `json:"upstream_state,omitempty"`
	Credential          string              `json:"credential,omitempty"`
	EgressKey           string              `json:"egress_key"`
	Proxy               string              `json:"proxy,omitempty"`
	EndpointID          int                 `json:"endpoint_id,omitempty"`
	EndpointFingerprint string              `json:"endpoint_fingerprint,omitempty"`
	Settings            dto.ChannelSettings `json:"settings"`
	DisplayName         string              `json:"display_name"`
	Models              []string            `json:"models"`
	Public              bool                `json:"public"`
	Status              string              `json:"status"`
	ChannelID           int                 `json:"channel_id,omitempty"`
	NextPollAt          int64               `json:"next_poll_at"`
}

func encryptWorkBuddyState(state *WorkBuddyAuthState) (string, error) {
	if !common.CryptoSecretConfigured || strings.TrimSpace(common.CryptoSecret) == "" {
		return "", ErrWorkBuddyEncryptionRequired
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
		return "", ErrWorkBuddyEncryptionRequired
	}
	return encrypted, nil
}

func CreateWorkBuddyAuthFlow(state WorkBuddyAuthState) (string, error) {
	providerAllowed := state.Provider == "workbuddy" || state.Provider == "trae" || state.Provider == "kimi-work" || state.Provider == "xiaomi-mimo" || state.Provider == "zcode" || state.Provider == "qoder" || state.Provider == "cline" || state.Provider == "raccoon"
	discoversModels := state.Provider == "zcode" || state.Provider == "qoder" || state.Provider == "cline" || state.Provider == "raccoon"
	if !providerAllowed || len(state.Models) == 0 && !discoversModels || state.EgressKey == "" {
		return "", ErrAuthFlowInvalid
	}
	payload, err := encryptWorkBuddyState(&state)
	if err != nil {
		return "", err
	}
	var token string
	err = DB.Transaction(func(tx *gorm.DB) error {
		// Write first for SQLite; serialize per owner for active-flow and channel limits.
		if err := tx.Model(&User{}).Where("id = ?", state.Identity.UserID).UpdateColumn("auth_version", gorm.Expr("auth_version")).Error; err != nil {
			return err
		}
		if err := ValidateAuthSessionWithTx(tx, state.Identity); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&AuthFlow{}).Where("purpose = ? AND user_id = ? AND consumed_at IS NULL AND expires_at > ?", WorkBuddyAuthPurpose, state.Identity.UserID, time.Now()).Count(&count).Error; err != nil {
			return err
		}
		if count >= 3 {
			return ErrWorkBuddyAuthLimit
		}
		if err := tx.Model(&Channel{}).Where("owner_id = ?", state.Identity.UserID).Count(&count).Error; err != nil {
			return err
		}
		if !operation_setting.SelfChannelEnabled || count >= int64(operation_setting.SelfChannelMaxPerUser) {
			return ErrWorkBuddyAuthLimit
		}
		var err error
		token, _, err = createAuthFlowWithTx(tx, AuthFlowCreate{Purpose: WorkBuddyAuthPurpose, Provider: state.Provider, Intent: AuthFlowIntentBind, UserId: state.Identity.UserID, SessionId: state.Identity.SessionID, Payload: payload, ExpiresAt: time.Now().Add(10 * time.Minute)})
		return err
	})
	return token, err
}

// WithWorkBuddyAuthFlow serializes one upstream redemption across processes.
// Its bounded upstream request is deliberate: a crashed transaction never commits
// a channel or reports success. Poll retries retain the encrypted credential.
func WithWorkBuddyAuthFlow(token string, identity AuthSessionIdentity, action func(*gorm.DB, *WorkBuddyAuthState) error) error {
	if len(token) != 43 || identity.UserID <= 0 || identity.SessionID == "" || action == nil {
		return ErrAuthFlowInvalid
	}
	var outcome error
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", identity.UserID).UpdateColumn("auth_version", gorm.Expr("auth_version")).Error; err != nil {
			return err
		}
		if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
			return err
		}
		match := AuthFlowMatch{Purpose: WorkBuddyAuthPurpose, Intent: AuthFlowIntentBind, UserId: identity.UserID, SessionId: identity.SessionID}
		if err := applyAuthFlowMatch(tx.Model(&AuthFlow{}), token, match).UpdateColumn("payload", gorm.Expr("payload")).Error; err != nil {
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
			outcome = ErrAuthFlowExpired
			return tx.Model(&flow).Updates(map[string]any{"payload": "", "consumed_at": time.Now()}).Error
		}
		if !common.IsEncryptedSecret(flow.Payload) {
			return ErrWorkBuddyEncryptionRequired
		}
		raw, err := common.DecryptSecret(flow.Payload)
		if err != nil {
			return err
		}
		var state WorkBuddyAuthState
		if err := common.Unmarshal([]byte(raw), &state); err != nil {
			return err
		}
		if state.Identity != identity || state.Provider != flow.Provider {
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
		updates := map[string]any{}
		if state.Status == "done" || state.Status == "cancelled" || state.Status == "failed" {
			state.UpstreamState, state.Credential = "", ""
			updates["consumed_at"] = time.Now()
		}
		encrypted, err := encryptWorkBuddyState(&state)
		if err != nil {
			return err
		}
		updates["payload"] = encrypted
		return tx.Model(&flow).Updates(updates).Error
	})
	if err != nil {
		return err
	}
	return outcome
}

// StageWorkBuddyChannel creates no abilities and remains disabled until proxy
// binding succeeds after commit. A failed binding is fail closed and retryable.
func StageWorkBuddyChannel(tx *gorm.DB, state *WorkBuddyAuthState, channelType int) error {
	if state.ChannelID != 0 {
		return nil
	}
	if state.Credential == "" || len(state.Models) == 0 {
		return ErrAuthFlowInvalid
	}
	var count int64
	if err := tx.Model(&Channel{}).Where("owner_id = ?", state.Identity.UserID).Count(&count).Error; err != nil {
		return err
	}
	if !operation_setting.SelfChannelEnabled || count >= int64(operation_setting.SelfChannelMaxPerUser) {
		return ErrWorkBuddyAuthLimit
	}
	encrypted, err := common.EncryptSecret(state.Credential)
	if err != nil {
		return err
	}
	if !common.IsEncryptedSecret(encrypted) {
		return ErrWorkBuddyEncryptionRequired
	}
	channel := Channel{Type: channelType, Key: encrypted, Name: state.DisplayName, Group: "default", Models: strings.Join(state.Models, ","), OwnerId: state.Identity.UserID, Status: common.ChannelStatusManuallyDisabled, Tier: "api", CreatedTime: time.Now().Unix()}
	if state.Public {
		channel.Visibility = 1
	}
	channel.SetSetting(state.Settings)
	channel.SetOtherInfo(map[string]any{"allModels": state.Models, "templateId": state.Provider})
	if err := tx.Create(&channel).Error; err != nil {
		return err
	}
	state.ChannelID, state.Status = channel.Id, "binding"
	return nil
}

func FinishWorkBuddyChannel(tx *gorm.DB, state *WorkBuddyAuthState) (*Channel, error) {
	var channel Channel
	if err := tx.Where("id = ? AND owner_id = ?", state.ChannelID, state.Identity.UserID).First(&channel).Error; err != nil {
		return nil, err
	}
	if state.Status == "done" {
		return &channel, nil
	}
	if state.Status != "binding" || channel.Status != common.ChannelStatusManuallyDisabled {
		return nil, ErrAuthFlowInvalid
	}
	channel.Status = common.ChannelStatusEnabled
	if err := tx.Model(&channel).Update("status", channel.Status).Error; err != nil {
		return nil, err
	}
	if err := channel.AddAbilities(tx); err != nil {
		return nil, err
	}
	state.Status = "done"
	return &channel, nil
}

// ResolveProxyEndpointIdentity returns a stable database identity and a
// configuration digest for a URL just issued by ResolveStableChannelProxy.
// Runtime listener ports and userinfo are not the identity. This never selects
// or mutates routing and never returns usable endpoint credentials.
func ResolveProxyEndpointIdentity(ownerID int, proxyURL string) (int, string, error) {
	invalid := errors.New("Account proxy endpoint identity is unavailable")
	if ownerID <= 0 || proxyURL == "" || DB == nil {
		return 0, "", invalid
	}
	value, ok := stableProxyEndpoints.Load(sha256.Sum256([]byte(proxyURL)))
	if !ok {
		return 0, "", invalid
	}
	selected := value.(stableProxyEndpoint)
	var endpoint ProxyEndpoint
	if err := DB.Where("id = ? AND (user_id = ? OR public = ?) AND enabled = ? AND fail_count = ?", selected.id, ownerID, true, true, 0).First(&endpoint).Error; err != nil {
		return 0, "", invalid
	}
	if sha256.Sum256([]byte(endpoint.Url+"\x00"+endpoint.RawUri)) != selected.fingerprint {
		return 0, "", invalid
	}
	current := endpoint.Url
	if current == "" && endpoint.RawUri != "" {
		var err error
		current, err = endpointProxyURLProvider(endpoint)
		if err != nil {
			return 0, "", invalid
		}
	}
	if current != proxyURL {
		return 0, "", invalid
	}
	digest := sha256.Sum256([]byte(endpoint.Kind + "\x00" + endpoint.Url + "\x00" + endpoint.RawUri))
	return endpoint.Id, fmt.Sprintf("%x", digest), nil
}

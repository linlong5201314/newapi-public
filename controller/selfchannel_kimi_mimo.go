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

type desktopAccountRequest struct {
	SessionID   string                     `json:"sessionId"`
	DisplayName string                     `json:"displayName"`
	Public      bool                       `json:"public"`
	Proxy       string                     `json:"proxy"`
	ProxyPolicy *dto.ChannelProxyPolicy    `json:"proxyPolicy"`
	Credentials service.KimiWorkCredential `json:"credentials"`
}

// Stored only in encrypted AuthFlow payload; never returned to clients.
type miMoPollClaim struct {
	service.MiMoTicket
	Nonce      string `json:"nonce,omitempty"`
	LeaseUntil int64  `json:"leaseUntil,omitempty"`
}

func desktopAccountError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrDesktopLoginExpired):
		body := accountAuthorizationBody("The provider login expired; sign in again")
		body["code"] = "DESKTOP_LOGIN_EXPIRED"
		c.JSON(http.StatusUnauthorized, body)
	case errors.Is(err, service.ErrMiMoRegion):
		body := accountAuthorizationBody("MiMo does not serve this Xiaomi account region")
		body["code"] = "MIMO_REGION_UNAVAILABLE"
		c.JSON(http.StatusForbidden, body)
	default:
		accountAuthorizationError(c, err)
	}
}

// SelfDesktopAccountCapabilities distinguishes local import from remote QR login.
// It never advertises a cloud OAuth flow for the desktop-local Kimi Work store.
func SelfDesktopAccountCapabilities(c *gin.Context) {
	if _, ok := accountAuthorizationIdentity(c); !ok {
		return
	}
	switch c.Query("provider") {
	case "cline-account", "qoder-cn", "qoder-global", "nous-portal":
		common.ApiSuccess(c, gin.H{"provider": c.Query("provider"), "method": "device_poll", "browserLoginAvailable": true, "importAvailable": false})
	case "raccoon", "trae-cn":
		common.ApiSuccess(c, gin.H{"provider": c.Query("provider"), "method": "callback_url", "browserLoginAvailable": true, "importAvailable": true, "importEndpoint": map[string]string{"trae-cn": "/api/channel/self/import_trae", "raccoon": "/api/channel/self/import_raccoon"}[c.Query("provider")], "credentialFormat": "single-desktop-or-magpie-account"})
	case "zcode-bigmodel", "zcode-zai":
		common.ApiSuccess(c, gin.H{"provider": c.Query("provider"), "method": "zcode_cli", "browserLoginAvailable": true, "importAvailable": false, "officialUrl": "https://zcode.z.ai"})
	case "kimi-work":
		common.ApiSuccess(c, gin.H{"provider": "kimi-work", "method": "local_import", "browserLoginAvailable": false, "importAvailable": true, "officialUrl": "https://www.kimi.com/", "helperUrl": "/api/channel/self/kimi_work/import_helper", "importEndpoint": "/api/channel/self/kimi_work/import", "credentialFormat": "kimi-work-desktop"})
	case "xiaomi-mimo":
		common.ApiSuccess(c, gin.H{"provider": "xiaomi-mimo", "method": "xiaomi_qr", "browserLoginAvailable": true, "importAvailable": false, "officialUrl": service.MiMoAccount, "startEndpoint": "/api/channel/self/oauth/mimo/start", "pollEndpoint": "/api/channel/self/oauth/mimo/poll", "cancelEndpoint": "/api/channel/self/oauth/mimo/cancel"})
	default:
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
	}
}

func SelfMiMoOAuthStart(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req desktopAccountRequest
	if c.ShouldBindJSON(&req) != nil || len(req.DisplayName) > 128 {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	settings := accountLoginProxySettings("global", req.Proxy, req.ProxyPolicy)
	token, err := newAccountFlow(identity, "xiaomi-mimo", "SGP", strings.TrimSpace(req.DisplayName), req.Public, settings, []string{"mimo-pro", "mimo-flash"})
	if err != nil {
		desktopAccountError(c, err)
		return
	}
	var snapshot model.WorkBuddyAuthState
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error { snapshot = *state; return nil })
	if err != nil {
		desktopAccountError(c, err)
		return
	}
	ticket, authURL, providerErr := service.StartMiMoSignIn(c.Request.Context(), snapshot.Proxy)
	expires := max(1, int(ticket.Until-time.Now().Unix()))
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "xiaomi-mimo" || state.Status != "starting" {
			return model.ErrAuthFlowConsumed
		}
		if providerErr != nil {
			state.Status = "failed"
			return nil
		}
		raw, err := common.Marshal(miMoPollClaim{MiMoTicket: ticket})
		if err != nil {
			return err
		}
		state.UpstreamState, state.Status = string(raw), "pending"
		return nil
	})
	if err != nil {
		desktopAccountError(c, err)
		return
	}
	if providerErr != nil {
		desktopAccountError(c, providerErr)
		return
	}
	common.ApiSuccess(c, gin.H{"sessionId": token, "authUrl": authURL, "expiresIn": expires, "interval": 2})
}

func SelfMiMoOAuthPoll(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req desktopAccountRequest
	if c.ShouldBindJSON(&req) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	token := strings.TrimSpace(req.SessionID)
	var snapshot model.WorkBuddyAuthState
	err := model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "xiaomi-mimo" {
			return model.ErrAuthFlowInvalid
		}
		snapshot = *state
		return nil
	})
	if err != nil {
		desktopAccountError(c, err)
		return
	}
	currentProxy := snapshot.Proxy
	if snapshot.Status != "done" {
		currentProxy, err = (service.AccountEgress{Key: snapshot.EgressKey, Proxy: snapshot.Proxy, EndpointID: snapshot.EndpointID, Fingerprint: snapshot.EndpointFingerprint}).Resolve(identity.UserID, 0, snapshot.Settings)
		if err != nil {
			desktopAccountError(c, err)
			return
		}
	}
	nonce := uuid.NewString()
	var claim miMoPollClaim
	var outcome error
	claimed := false
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "xiaomi-mimo" {
			return model.ErrAuthFlowInvalid
		}
		if state.Status == "done" || state.Status == "binding" {
			return nil
		}
		if state.Status == "cancelled" || state.Status == "failed" || state.Status == "starting" {
			return model.ErrAuthFlowConsumed
		}
		if common.Unmarshal([]byte(state.UpstreamState), &claim) != nil {
			return service.ErrAccountProtocol
		}
		if state.Status != "pending" {
			if state.Status != "polling" && state.Status != "exchanging" {
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
		claim.LeaseUntil = time.Now().Add(45 * time.Second).Unix()
		raw, err := common.Marshal(claim)
		if err != nil {
			return err
		}
		state.UpstreamState = string(raw)
		state.Proxy = currentProxy
		state.Status = "polling"
		if state.Credential != "" {
			state.Status = "exchanging"
		}
		snapshot = *state
		claimed = true
		return nil
	})
	if err != nil {
		desktopAccountError(c, err)
		return
	}
	if outcome != nil {
		desktopAccountError(c, outcome)
		return
	}
	if claimed {
		var credential *service.MiMoCredential
		if snapshot.Credential == "" {
			credential, err = service.PollMiMoSignIn(c.Request.Context(), currentProxy, claim.MiMoTicket)
			providerErr := err
			err = withMiMoClaim(token, identity, nonce, func(_ *gorm.DB, state *model.WorkBuddyAuthState, stored *miMoPollClaim) error {
				if providerErr != nil || credential == nil {
					state.Status = "pending"
					state.NextPollAt = time.Now().Add(2 * time.Second).Unix()
					if providerErr != nil {
						// A failed transport may have redeemed the one-shot result.
						// Timeout-with-no-result is already represented as nil by the service.
						state.Status = "failed"
					}
					return nil
				}
				credential.Egress = service.AccountEgress{Key: state.EgressKey, Proxy: state.Proxy, EndpointID: state.EndpointID, Fingerprint: state.EndpointFingerprint}
				raw, err := common.Marshal(credential)
				if err != nil {
					return err
				}
				state.Credential = string(raw)
				stored.PollURL = ""
				stored.LeaseUntil = time.Now().Add(45 * time.Second).Unix()
				state.Status = "exchanging"
				return nil
			})
			if err != nil {
				desktopAccountError(c, err)
				return
			}
			if providerErr != nil {
				desktopAccountError(c, providerErr)
				return
			}
			if credential == nil {
				common.ApiSuccess(c, gin.H{"done": false, "interval": 2})
				return
			}
		} else {
			credential = &service.MiMoCredential{}
			if common.Unmarshal([]byte(snapshot.Credential), credential) != nil {
				desktopAccountError(c, service.ErrAccountProtocol)
				return
			}
		}
		if len(credential.Cookies) == 0 {
			credential, err = service.RenewMiMoSession(c.Request.Context(), currentProxy, *credential)
		}
		providerErr := err
		err = withMiMoClaim(token, identity, nonce, func(tx *gorm.DB, state *model.WorkBuddyAuthState, _ *miMoPollClaim) error {
			if providerErr != nil {
				state.Status = "pending"
				state.NextPollAt = time.Now().Add(2 * time.Second).Unix()
				return nil
			}
			raw, err := common.Marshal(credential)
			if err != nil {
				return err
			}
			state.Credential = string(raw)
			if err := model.StageWorkBuddyChannel(tx, state, constant.ChannelTypeMiMo); err != nil {
				providerErr = err
				state.Status = "pending"
				return nil
			}
			return nil
		})
		if err != nil {
			desktopAccountError(c, err)
			return
		}
		if providerErr != nil {
			desktopAccountError(c, providerErr)
			return
		}
	}
	channel, err := finishDesktopAccountChannel(identity, token, "xiaomi-mimo")
	if err != nil {
		desktopAccountError(c, err)
		return
	}
	if channel == nil {
		common.ApiSuccess(c, gin.H{"done": false, "interval": 2})
		return
	}
	sanitizeSelfChannel(channel)
	common.ApiSuccess(c, gin.H{"done": true, "channel": channel})
}

// Network calls occur outside the write transaction. A short commit checks the
// live SID/version, expiry, cancellation, nonce and lease before applying results.
func withMiMoClaim(token string, identity model.AuthSessionIdentity, nonce string, action func(*gorm.DB, *model.WorkBuddyAuthState, *miMoPollClaim) error) error {
	return model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "xiaomi-mimo" || (state.Status != "polling" && state.Status != "exchanging") {
			return model.ErrAuthFlowConsumed
		}
		var claim miMoPollClaim
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

func SelfMiMoOAuthCancel(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req desktopAccountRequest
	if c.ShouldBindJSON(&req) != nil {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	err := model.WithWorkBuddyAuthFlow(strings.TrimSpace(req.SessionID), identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "xiaomi-mimo" {
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
		desktopAccountError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func finishDesktopAccountChannel(identity model.AuthSessionIdentity, token, provider string) (*model.Channel, error) {
	var state model.WorkBuddyAuthState
	if err := model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error {
		if s.Provider != provider {
			return model.ErrAuthFlowInvalid
		}
		state = *s
		return nil
	}); err != nil {
		return nil, err
	}
	if state.ChannelID == 0 {
		return nil, nil
	}
	if state.Status != "done" {
		if user, userErr := model.GetUserById(identity.UserID, false); userErr == nil && user.Role == common.RoleRootUser && (state.Settings.ProxyPolicy == nil || state.Settings.ProxyPolicy.Mode != "direct") {
			if _, err := model.ResolveRootChannelProxyPolicy(identity.UserID, state.EgressKey, state.Settings); err != nil {
				return nil, err
			}
		} else if _, err := (service.AccountEgress{Key: state.EgressKey, Proxy: state.Proxy, EndpointID: state.EndpointID, Fingerprint: state.EndpointFingerprint}).Resolve(identity.UserID, 0, state.Settings); err != nil {
			return nil, err
		}
	}
	if state.Status != "done" && state.Settings.ProxyPolicy != nil && state.Settings.ProxyPolicy.Mode != "direct" && state.Settings.ProxyPolicy.Mode != "fixed" {
		if err := model.BindChannelProxyAccount(identity.UserID, state.ChannelID, state.EgressKey); err != nil {
			return nil, service.ErrAccountEgress
		}
	}
	var channel *model.Channel
	err := model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, s *model.WorkBuddyAuthState) error {
		if s.Provider != provider {
			return model.ErrAuthFlowInvalid
		}
		var err error
		channel, err = model.FinishWorkBuddyChannel(tx, s)
		return err
	})
	if err == nil && state.Status != "done" {
		model.InitChannelCache()
	}
	return channel, err
}

func SelfImportKimiWork(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var req desktopAccountRequest
	if c.ShouldBindJSON(&req) != nil || len(req.DisplayName) > 128 {
		accountAuthorizationError(c, model.ErrAuthFlowInvalid)
		return
	}
	settings := accountLoginProxySettings("global", req.Proxy, req.ProxyPolicy)
	// The local app creates its Work key; this gateway never mints one or reads
	// arbitrary server-local paths. No Kimi Web refresh/session token is imported.
	token, err := newAccountFlow(identity, "kimi-work", "desktop", strings.TrimSpace(req.DisplayName), req.Public, settings, []string{"k3-agent"})
	if err != nil {
		desktopAccountError(c, err)
		return
	}
	var snapshot model.WorkBuddyAuthState
	err = model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, state *model.WorkBuddyAuthState) error { snapshot = *state; return nil })
	if err != nil {
		desktopAccountError(c, err)
		return
	}
	models, providerErr := service.ValidateKimiWork(c.Request.Context(), snapshot.Proxy, req.Credentials)
	err = model.WithWorkBuddyAuthFlow(token, identity, func(tx *gorm.DB, state *model.WorkBuddyAuthState) error {
		if state.Provider != "kimi-work" || state.Status != "starting" {
			return model.ErrAuthFlowConsumed
		}
		if providerErr != nil {
			state.Status = "failed"
			return nil
		}
		req.Credentials.Egress = service.AccountEgress{Key: state.EgressKey, Proxy: state.Proxy, EndpointID: state.EndpointID, Fingerprint: state.EndpointFingerprint}
		raw, err := common.Marshal(req.Credentials)
		if err != nil {
			return err
		}
		state.Credential = string(raw)
		state.Models = models
		return model.StageWorkBuddyChannel(tx, state, constant.ChannelTypeKimiWork)
	})
	if err != nil {
		desktopAccountError(c, err)
		return
	}
	if providerErr != nil {
		desktopAccountError(c, providerErr)
		return
	}
	channel, err := finishDesktopAccountChannel(identity, token, "kimi-work")
	if err != nil {
		desktopAccountError(c, err)
		return
	}
	if channel == nil {
		desktopAccountError(c, service.ErrAccountProtocol)
		return
	}
	sanitizeSelfChannel(channel)
	common.ApiSuccess(c, gin.H{"done": true, "channel": channel})
}

// The user runs this read-only exporter on their own Windows desktop, reviews
// the generated file and uploads it to their own authenticated HTTPS dashboard.
// It never makes requests, changes desktop files or prints usable credentials.
const kimiWorkExportPowerShell = `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
$data = Join-Path $env:APPDATA 'kimi-desktop'
$share = Join-Path $data 'daimon-share'
$pointer = Join-Path $data 'daimon-storage.json'
if (Test-Path -LiteralPath $pointer -PathType Leaf) {
  $p = Get-Content -LiteralPath $pointer -Raw -Encoding UTF8 | ConvertFrom-Json
  if ($p.shareDir -and [IO.Path]::IsPathRooted($p.shareDir)) { $share = $p.shareDir }
}
$file = Join-Path $share 'daimon\config.json'
if (!(Test-Path -LiteralPath $file -PathType Leaf)) { throw 'Sign in to Kimi desktop Work on this computer first.' }
if ((Get-Item -LiteralPath $file).Length -gt 8388608) { throw 'Unexpected Kimi configuration size.' }
$config = Get-Content -LiteralPath $file -Raw -Encoding UTF8 | ConvertFrom-Json
$work = $config.credentials.kimiCode
$user = $config.credentials.kimiWeb.userId
if (!$user -or !$work.apiKey -or $work.baseUrl.TrimEnd('/') -ne 'https://agent-gw.kimi.com/coding/v1') { throw 'No usable Kimi Work sign-in. Open Work in Kimi desktop first.' }
$export = @{ source='kimi-work-desktop'; workToken=$work.apiKey; userId=$user }
$device = Join-Path $share 'daimon\runtime\kimi-code\home\device_id'
if (Test-Path -LiteralPath $device -PathType Leaf) { $export.deviceId = (Get-Content -LiteralPath $device -Raw -Encoding UTF8).Trim() }
$dialog = New-Object System.Windows.Forms.SaveFileDialog
$dialog.Filter = 'JSON files (*.json)|*.json'
$dialog.FileName = 'kimi-work-import.json'
$dialog.Title = 'Save private Kimi Work export for your own HTTPS dashboard'
if ($dialog.ShowDialog() -ne [System.Windows.Forms.DialogResult]::OK) { exit }
[IO.File]::WriteAllText($dialog.FileName, ($export | ConvertTo-Json -Compress), (New-Object Text.UTF8Encoding $false))
Write-Host 'Private export saved. Upload it only to your own authenticated HTTPS dashboard, then delete the file.'
`

func SelfKimiWorkImportHelper(c *gin.Context) {
	if _, ok := accountAuthorizationIdentity(c); !ok {
		return
	}
	c.Header("Content-Disposition", `attachment; filename="export-kimi-work.ps1"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(kimiWorkExportPowerShell))
}

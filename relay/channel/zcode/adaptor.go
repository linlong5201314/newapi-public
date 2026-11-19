// Package zcode transports owner-authorized Start Plan requests. Anthropic
// conversion, streaming, usage and billing remain in the existing Claude adapter.
package zcode

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type Adaptor struct{ claude.Adaptor }

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info == nil || info.ChannelType != constant.ChannelTypeZCode {
		return "", service.ErrAccountProtocol
	}
	if info.RelayMode != relayconstant.RelayModeChatCompletions && info.RelayMode != relayconstant.RelayModeResponses && !(info.RelayFormat == types.RelayFormatClaude && info.RelayMode == relayconstant.RelayModeUnknown) {
		return "", errors.New("ZCode supports only chat and Messages requests")
	}
	if info.ChannelBaseUrl != "" && strings.TrimRight(info.ChannelBaseUrl, "/") != service.ZCodeStartBase {
		return "", service.ErrAccountProtocol
	}
	return service.ZCodeStartBase + "/v1/messages", nil
}

func storedCredential(info *relaycommon.RelayInfo) (*model.Channel, *service.ZCodeCredential, error) {
	if info == nil || info.ChannelId <= 0 || info.ChannelType != constant.ChannelTypeZCode || info.ApiKey == "" {
		return nil, nil, service.ErrAccountProtocol
	}
	stored, err := model.GetChannelById(info.ChannelId, true)
	if err != nil || stored.OwnerId <= 0 || stored.Type != constant.ChannelTypeZCode || !common.IsEncryptedSecret(stored.Key) {
		return nil, nil, service.ErrAccountProtocol
	}
	secret, err := common.DecryptSecret(stored.Key)
	if err != nil || secret != info.ApiKey {
		return nil, nil, service.ErrAccountProtocol
	}
	var cred service.ZCodeCredential
	if common.Unmarshal([]byte(secret), &cred) != nil || (cred.Provider != "bigmodel" && cred.Provider != "zai") || !service.HeaderCredential(cred.UserID, 256) || !service.HeaderCredential(cred.Token, 16384) {
		return nil, nil, service.ErrAccountProtocol
	}
	if cred.ExpiresAt <= time.Now().Unix() {
		return nil, nil, service.ErrDesktopLoginExpired
	}
	return stored, &cred, nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, h *http.Header, info *relaycommon.RelayInfo) error {
	if len(relaycommon.GetEffectiveHeaderOverride(info)) != 0 {
		return errors.New("ZCode account headers cannot be overridden")
	}
	_, cred, err := storedCredential(info)
	if err != nil {
		return err
	}
	channel.SetupApiRequestHeader(info, c, h)
	for _, name := range []string{"Authorization", "Cookie", "X-Api-Key", "Origin", "Referer", "Anthropic-Beta"} {
		h.Del(name)
	}
	h.Set("Authorization", "Bearer "+cred.Token)
	h.Set("X-Api-Key", cred.Token)
	h.Set("Content-Type", "application/json")
	h.Set("Anthropic-Version", "2023-06-01")
	// Honest gateway attribution; never invent desktop device or verification proof.
	h.Set("User-Agent", "NewAPI-ZCode-Bridge/1.0")
	h.Set("HTTP-Referer", "https://zcode.z.ai")
	h.Set("X-Title", "NewAPI ZCode account bridge")
	return nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	if len(relaycommon.GetEffectiveHeaderOverride(info)) != 0 {
		return nil, service.ErrAccountProtocol
	}
	stored, cred, err := storedCredential(info)
	if err != nil {
		return nil, err
	}
	// The routed model must remain in the authenticated account's discovered list.
	allowed := false
	for _, id := range strings.Split(stored.Models, ",") {
		if id == info.UpstreamModelName || (info.UpstreamModelName == "" && id == info.OriginModelName) {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, service.ErrZCodePlanUnavailable
	}
	settings := stored.GetSetting()
	if settings.TLSInsecureSkipVerify {
		return nil, service.ErrAccountProtocol
	}
	proxy, err := cred.Egress.Resolve(stored.OwnerId, stored.Id, settings)
	if err != nil {
		return nil, err
	}
	original := info.ChannelSetting
	defer func() { info.ChannelSetting = original }()
	info.ChannelSetting = settings
	info.ChannelSetting.Proxy = proxy
	mode := "fixed"
	if proxy == "" {
		info.ChannelSetting.Proxy = "direct"
		mode = "direct"
	}
	info.ChannelSetting.ProxyPolicy = &dto.ChannelProxyPolicy{Mode: mode}
	return channel.DoApiRequest(a, c, info, body)
}

// Models are discovered from authenticated balance capabilities, never a static
// global list. Reusing the Claude response handlers preserves native SSE/errors.
func (a *Adaptor) GetModelList() []string { return nil }
func (a *Adaptor) GetChannelName() string { return "zcode" }

// Package desktopaccount adds only verified Kimi Work and Xiaomi MiMo account
// transport. OpenAI response parsing, usage and billing are inherited unchanged.
package desktopaccount

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

var renewal singleflight.Group

type Adaptor struct{ openai.Adaptor }

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayMode != relayconstant.RelayModeChatCompletions {
		return "", errors.New("Desktop accounts support only chat completions on this gateway")
	}
	switch info.ChannelType {
	case constant.ChannelTypeKimiWork:
		if info.ChannelBaseUrl != "" && strings.TrimRight(info.ChannelBaseUrl, "/") != service.KimiWorkBase {
			return "", service.ErrAccountProtocol
		}
		return service.KimiWorkBase + "/chat/completions", nil
	case constant.ChannelTypeMiMo:
		var cred service.MiMoCredential
		if common.Unmarshal([]byte(info.ApiKey), &cred) != nil || service.MiMoBase(cred.Region) == "" {
			return "", service.ErrAccountProtocol
		}
		// Regional host is selected by an authenticated provider response, never a URL input.
		if info.ChannelBaseUrl != "" && strings.TrimRight(info.ChannelBaseUrl, "/") != service.MiMoBase("SGP") {
			return "", service.ErrAccountProtocol
		}
		return service.MiMoBase(cred.Region) + "/route/chat/completions", nil
	default:
		return "", service.ErrAccountProtocol
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, h *http.Header, info *relaycommon.RelayInfo) error {
	if len(relaycommon.GetEffectiveHeaderOverride(info)) > 0 {
		return errors.New("Desktop account authorization headers cannot be overridden")
	}
	if info.ChannelId <= 0 || info.ApiKey == "" {
		return service.ErrAccountProtocol
	}
	stored, err := model.GetChannelById(info.ChannelId, true)
	if err != nil || stored.OwnerId <= 0 || stored.Type != info.ChannelType || !common.IsEncryptedSecret(stored.Key) {
		return service.ErrAccountProtocol
	}
	secret, err := common.DecryptSecret(stored.Key)
	if err != nil || secret != info.ApiKey {
		return service.ErrAccountProtocol
	}
	// DoRequest pinned the transport to the same owner lease before creating headers.
	channel.SetupApiRequestHeader(info, c, h)
	h.Del("Authorization")
	h.Del("Cookie")
	h.Del("X-Api-Key")
	h.Del("Origin")
	h.Del("Referer")
	switch info.ChannelType {
	case constant.ChannelTypeKimiWork:
		var cred service.KimiWorkCredential
		if common.Unmarshal([]byte(secret), &cred) != nil || cred.Source != "kimi-work-desktop" || !service.HeaderCredential(cred.WorkToken, 8192) || (cred.DeviceID != "" && !service.HeaderCredential(cred.DeviceID, 128)) {
			return service.ErrAccountProtocol
		}
		for name, values := range service.KimiWorkHeaders(cred) {
			(*h)[name] = values
		}
	case constant.ChannelTypeMiMo:
		var cred service.MiMoCredential
		if common.Unmarshal([]byte(secret), &cred) != nil || cred.ExpiresAt <= time.Now().Unix() || len(cred.Cookies) == 0 {
			return service.ErrDesktopLoginExpired
		}
		request := &http.Request{Header: *h}
		for name, value := range cred.Cookies {
			if name == "passToken" || !service.HeaderCredential(name, 128) || !service.HeaderCredential(value, 8192) {
				return service.ErrAccountProtocol
			}
			request.AddCookie(&http.Cookie{Name: name, Value: value})
		}
		for name, values := range service.MiMoHeaders() {
			(*h)[name] = values
		}
		h.Set("X-Mimo-Source", "mimocode-cli-free")
	default:
		return service.ErrAccountProtocol
	}
	return nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	stored, err := model.GetChannelById(info.ChannelId, true)
	if err != nil || stored.OwnerId <= 0 || stored.Type != info.ChannelType || !common.IsEncryptedSecret(stored.Key) {
		return nil, service.ErrAccountProtocol
	}
	secret, err := common.DecryptSecret(stored.Key)
	if err != nil || secret != info.ApiKey {
		return nil, service.ErrAccountProtocol
	}
	settings := stored.GetSetting()
	var egress service.AccountEgress
	switch info.ChannelType {
	case constant.ChannelTypeKimiWork:
		var cred service.KimiWorkCredential
		if common.Unmarshal([]byte(secret), &cred) != nil {
			return nil, service.ErrAccountProtocol
		}
		egress = cred.Egress
	case constant.ChannelTypeMiMo:
		var cred service.MiMoCredential
		if common.Unmarshal([]byte(secret), &cred) != nil {
			return nil, service.ErrAccountProtocol
		}
		egress = cred.Egress
	default:
		return nil, service.ErrAccountProtocol
	}
	proxy, err := egress.Resolve(stored.OwnerId, stored.Id, settings)
	if err != nil {
		return nil, err
	}
	if info.ChannelType == constant.ChannelTypeMiMo {
		var cred service.MiMoCredential
		if common.Unmarshal([]byte(secret), &cred) != nil {
			return nil, service.ErrAccountProtocol
		}
		if cred.ExpiresAt <= time.Now().Add(10*time.Minute).Unix() {
			value, err, _ := renewal.Do(strconv.Itoa(stored.Id)+":"+common.GenerateHMACWithKey([]byte(secret), "mimo-renew"), func() (any, error) {
				latest, err := model.GetChannelById(stored.Id, true)
				if err != nil || latest.Key != stored.Key {
					return nil, service.ErrAccountProtocol
				}
				next, err := service.RenewMiMoSession(c.Request.Context(), proxy, cred)
				if err != nil {
					return nil, err
				}
				raw, err := common.Marshal(next)
				if err != nil {
					return nil, err
				}
				encrypted, err := common.EncryptSecret(string(raw))
				if err != nil || !common.IsEncryptedSecret(encrypted) {
					return nil, service.ErrAccountProtocol
				}
				result := model.DB.Model(&model.Channel{}).Where(map[string]any{"id": stored.Id, "owner_id": stored.OwnerId, "key": stored.Key}).Update("key", encrypted)
				if result.Error != nil {
					return nil, result.Error
				}
				if result.RowsAffected != 1 {
					return nil, service.ErrAccountProtocol
				}
				model.InitChannelCache()
				return string(raw), nil
			})
			if err != nil {
				return nil, err
			}
			info.ApiKey = value.(string)
		}
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

// Kimi models are discovered live at import; MiMo names are the app's names.
func (a *Adaptor) GetModelList() []string { return nil }
func (a *Adaptor) GetChannelName() string { return "desktopaccount" }

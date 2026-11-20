// Package nousportal implements the Nous Portal OAuth relay adapter.
// Nous Portal is an OpenAI-compatible inference API; the channel key is the
// encrypted NousCredential JSON from the device authorization flow.
package nousportal

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Adaptor reuses OpenAI's request/response handling; only the base URL and
// credential extraction differ.
type Adaptor struct {
	openai.Adaptor
}

func (a *Adaptor) GetChannelName() string { return "nous-portal" }
func (a *Adaptor) GetModelList() []string { return nil }

// GetRequestURL resolves the inference base from the stored credential, never
// from a caller-supplied ChannelBaseUrl.
func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info == nil || info.ChannelType != constant.ChannelTypeNousPortal {
		return "", service.ErrAccountProtocol
	}
	base := strings.TrimRight(strings.TrimSpace(info.ChannelBaseUrl), "/")
	if base == "" {
		base = service.NousInferenceBase
	}
	if base != service.NousInferenceBase && base != service.NousWelcomeBase {
		return "", service.ErrAccountProtocol
	}
	path := strings.TrimSpace(info.RequestURLPath)
	if path == "" {
		path = "/v1/chat/completions"
	}
	if strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	} else if path == "/v1" {
		path = "/chat/completions"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path, nil
}

// storedCredential decrypts the channel key into a NousCredential.
func storedCredential(info *relaycommon.RelayInfo) (*model.Channel, *service.NousCredential, error) {
	if info == nil || info.ChannelId <= 0 || info.ChannelType != constant.ChannelTypeNousPortal || info.ApiKey == "" {
		return nil, nil, service.ErrAccountProtocol
	}
	stored, err := model.GetChannelById(info.ChannelId, true)
	if err != nil || stored.OwnerId <= 0 || stored.Type != constant.ChannelTypeNousPortal || !common.IsEncryptedSecret(stored.Key) {
		return nil, nil, service.ErrAccountProtocol
	}
	secret, err := common.DecryptSecret(stored.Key)
	if err != nil || secret != info.ApiKey {
		return nil, nil, service.ErrAccountProtocol
	}
	var cred service.NousCredential
	if common.Unmarshal([]byte(secret), &cred) != nil || cred.AccessToken == "" {
		return nil, nil, service.ErrAccountProtocol
	}
	if cred.ExpiresAt > 0 && cred.ExpiresAt <= time.Now().Unix() {
		return nil, nil, service.ErrDesktopLoginExpired
	}
	return stored, &cred, nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, h *http.Header, info *relaycommon.RelayInfo) error {
	if len(relaycommon.GetEffectiveHeaderOverride(info)) != 0 {
		return service.ErrAccountProtocol
	}
	_, cred, err := storedCredential(info)
	if err != nil {
		return err
	}
	channel.SetupApiRequestHeader(info, c, h)
	for _, name := range []string{"Authorization", "Cookie", "X-Api-Key", "Origin", "Referer"} {
		h.Del(name)
	}
	h.Set("Authorization", "Bearer "+cred.AccessToken)
	h.Set("Content-Type", "application/json")
	return nil
}

// DoRequest dispatches through the OpenAI-compatible inference API.
// The channel key is JSON, not a plain API key, so the standard OpenAI
// adaptor cannot be used directly.
func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	if info == nil || info.ChannelType != constant.ChannelTypeNousPortal {
		return nil, service.ErrAccountProtocol
	}
	_, _, err := storedCredential(info)
	if err != nil {
		return nil, err
	}
	// Verify the requested model is in the channel's catalog.
	stored, _, err2 := storedCredential(info)
	if err2 != nil {
		return nil, err2
	}
	modelKey := info.UpstreamModelName
	if modelKey == "" {
		modelKey = info.OriginModelName
	}
	allowed := false
	for id := range strings.SplitSeq(stored.Models, ",") {
		if id == modelKey {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("model %s is not in the channel catalog", modelKey)
	}
	return channel.DoApiRequest(a, c, info, body)
}

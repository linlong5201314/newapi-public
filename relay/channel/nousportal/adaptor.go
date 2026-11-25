// Package nousportal implements the Nous Portal OAuth relay adapter.
// Nous Portal is an OpenAI-compatible inference API; the channel key is the
// encrypted NousCredential JSON from the device authorization flow.
package nousportal

import (
	"context"
	"fmt"
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
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

// Adaptor reuses OpenAI's request/response handling; only the base URL and
// credential extraction differ.
type Adaptor struct {
	openai.Adaptor
}

var nousRefresh singleflight.Group
var renewNousCredential = service.RenewNousCredential

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
func storedCredential(ctx context.Context, info *relaycommon.RelayInfo) (*model.Channel, *service.NousCredential, error) {
	if info == nil || info.ChannelId <= 0 || info.ChannelType != constant.ChannelTypeNousPortal || info.ApiKey == "" {
		return nil, nil, service.ErrAccountProtocol
	}
	stored, err := model.GetChannelById(info.ChannelId, true)
	if err != nil || stored.OwnerId <= 0 || stored.Type != constant.ChannelTypeNousPortal || !common.IsEncryptedSecret(stored.Key) {
		return nil, nil, service.ErrAccountProtocol
	}
	secret, err := common.DecryptSecret(stored.Key)
	if err != nil {
		return nil, nil, service.ErrAccountProtocol
	}
	var cred service.NousCredential
	if common.Unmarshal([]byte(secret), &cred) != nil || cred.AccessToken == "" {
		return nil, nil, service.ErrAccountProtocol
	}
	// Another request or node may have rotated this account already. Recover
	// that fresh credential instead of submitting the old refresh token again.
	if secret != info.ApiKey && (cred.ExpiresAt == 0 || cred.ExpiresAt > time.Now().Unix()+300) {
		return stored, &cred, nil
	}
	if secret != info.ApiKey || cred.ExpiresAt > 0 && cred.ExpiresAt <= time.Now().Unix()+300 {
		_, next, err := refreshStoredNousCredential(ctx, stored, cred)
		if err != nil {
			return nil, nil, err
		}
		cred = *next
	}
	return stored, &cred, nil
}

func credFromChannel(channel *model.Channel) (*service.NousCredential, error) {
	secret, err := common.DecryptSecret(channel.Key)
	if err != nil {
		return nil, err
	}
	var cred service.NousCredential
	if err := common.Unmarshal([]byte(secret), &cred); err != nil {
		return nil, err
	}
	return &cred, nil
}

func refreshStoredNousCredential(ctx context.Context, stored *model.Channel, cred service.NousCredential) (*model.Channel, *service.NousCredential, error) {
	resultCh := nousRefresh.DoChan(strconv.Itoa(stored.Id), func() (any, error) {
		latest, err := model.GetChannelById(stored.Id, true)
		if err != nil || latest.OwnerId != stored.OwnerId || latest.Type != constant.ChannelTypeNousPortal || latest.Key != stored.Key {
			return nil, service.ErrDesktopLoginExpired
		}
		proxy, err := cred.Egress.Resolve(stored.OwnerId, stored.Id, stored.GetSetting())
		if err != nil {
			return nil, err
		}
		next, err := renewNousCredential(ctx, proxy, &cred)
		if err != nil {
			return nil, err
		}
		next.Egress = cred.Egress
		raw, err := common.Marshal(next)
		if err != nil {
			return nil, err
		}
		encrypted, err := common.EncryptSecret(string(raw))
		if err != nil {
			return nil, err
		}
		result := model.DB.Model(&model.Channel{}).Where("id = ? AND type = ? AND owner_id = ? AND key = ?", stored.Id, constant.ChannelTypeNousPortal, stored.OwnerId, stored.Key).Update("key", encrypted)
		if result.Error != nil {
			return nil, service.ErrAccountUpstream
		}
		if result.RowsAffected != 1 {
			// Another node won the rotation race. Read its committed credential;
			// never submit our consumed refresh token or force a needless re-login.
			rotated, readErr := model.GetChannelById(stored.Id, true)
			if readErr != nil || rotated.OwnerId != stored.OwnerId || rotated.Type != constant.ChannelTypeNousPortal {
				return nil, service.ErrDesktopLoginExpired
			}
			next, readErr := credFromChannel(rotated)
			if readErr != nil || next.AccessToken == "" {
				return nil, service.ErrDesktopLoginExpired
			}
			return rotated, nil
		}
		latest.Key = encrypted
		model.InitChannelCache()
		return latest, nil
	})
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return nil, nil, result.Err
		}
		latest, ok := result.Val.(*model.Channel)
		if !ok || latest == nil {
			return nil, nil, service.ErrDesktopLoginExpired
		}
		next, err := credFromChannel(latest)
		return latest, next, err
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, h *http.Header, info *relaycommon.RelayInfo) error {
	if len(relaycommon.GetEffectiveHeaderOverride(info)) != 0 {
		return service.ErrAccountProtocol
	}
	_, cred, err := storedCredential(context.Background(), info)
	if err != nil {
		return err
	}
	channel.SetupApiRequestHeader(info, c, h)
	for _, name := range []string{"Authorization", "Cookie", "X-Api-Key", "Origin", "Referer"} {
		h.Del(name)
	}
	h.Set("Authorization", "Bearer "+cred.AccessToken)
	h.Set("Content-Type", "application/json")
	// The inference edge rejects the bare Go client identity with 403.
	h.Set("User-Agent", service.NousBrowserUserAgent)
	return nil
}

// DoRequest dispatches through the OpenAI-compatible inference API.
// The channel key is JSON, not a plain API key, so the standard OpenAI
// adaptor cannot be used directly.
func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	if info == nil || info.ChannelType != constant.ChannelTypeNousPortal {
		return nil, service.ErrAccountProtocol
	}
	stored, cred, err := storedCredential(c.Request.Context(), info)
	if err != nil {
		return nil, err
	}
	// Verify the requested model against the same credential snapshot used below.
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
	proxy, err := cred.Egress.Resolve(stored.OwnerId, stored.Id, stored.GetSetting())
	if err != nil {
		return nil, err
	}
	client, err := service.NewQoderHTTPClient(proxy)
	if err != nil {
		return nil, err
	}
	requestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, requestURL, body)
	if err != nil {
		return nil, err
	}
	channel.SetupApiRequestHeader(info, c, &req.Header)
	for _, name := range []string{"Authorization", "Cookie", "X-Api-Key", "Origin", "Referer"} {
		req.Header.Del(name)
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", service.NousBrowserUserAgent)
	client.Timeout = 0
	return client.Do(req)
}

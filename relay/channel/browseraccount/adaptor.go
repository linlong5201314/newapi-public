package browseraccount

import (
	"context"
	"crypto/sha256"
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

type Adaptor struct{ openai.Adaptor }

var refreshes singleflight.Group

func baseURL(kind int) string {
	switch kind {
	case constant.ChannelTypeCline:
		return service.ClineAPIBase
	case constant.ChannelTypeRaccoon:
		return service.RaccoonAPIBase
	default:
		return ""
	}
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info == nil || info.ChannelMeta == nil {
		return "", service.ErrAccountProtocol
	}
	base := baseURL(info.ChannelType)
	if base == "" || info.RelayMode != relayconstant.RelayModeChatCompletions || info.ChannelBaseUrl != "" && strings.TrimRight(info.ChannelBaseUrl, "/") != base {
		return "", service.ErrAccountProtocol
	}
	return base + "/chat/completions", nil
}

func rotatedAccountSecret(stored, latest *model.Channel, previousSecret string) (string, error) {
	if stored == nil || latest == nil || !common.IsEncryptedSecret(latest.Key) {
		return "", service.ErrAccountProtocol
	}
	expected := *stored
	expected.Key = latest.Key
	if identicalAccountSnapshot(stored, latest) == nil || unchangedAccount(&expected, latest) != nil {
		return "", service.ErrAccountProtocol
	}
	plain, err := common.DecryptSecret(latest.Key)
	if err != nil {
		return "", service.ErrAccountProtocol
	}
	var previousEgress, latestEgress service.AccountEgress
	var previousUID, latestUID string
	var until int64
	if stored.Type == constant.ChannelTypeCline {
		var previous, next service.ClineCredential
		if common.Unmarshal([]byte(previousSecret), &previous) != nil || common.Unmarshal([]byte(plain), &next) != nil {
			return "", service.ErrAccountProtocol
		}
		previousEgress, latestEgress = previous.Egress, next.Egress
		previousUID, latestUID, until = previous.UserID, next.UserID, next.ExpiresAt
	} else if stored.Type == constant.ChannelTypeRaccoon {
		var previous, next service.RaccoonCredential
		if common.Unmarshal([]byte(previousSecret), &previous) != nil || common.Unmarshal([]byte(plain), &next) != nil {
			return "", service.ErrAccountProtocol
		}
		previousEgress, latestEgress = previous.Egress, next.Egress
		previousUID, latestUID, until = previous.UserID, next.UserID, next.ExpiresAt
	} else {
		return "", service.ErrAccountProtocol
	}
	if previousUID == "" || previousUID != latestUID || previousEgress != latestEgress || until > 0 && until <= time.Now().Unix() {
		return "", service.ErrAccountProtocol
	}
	return plain, nil
}

func account(info *relaycommon.RelayInfo) (*model.Channel, service.AccountEgress, string, int64, error) {
	if info == nil || info.ChannelMeta == nil || info.ChannelId <= 0 || baseURL(info.ChannelType) == "" || info.ApiKey == "" {
		return nil, service.AccountEgress{}, "", 0, service.ErrAccountProtocol
	}
	stored, err := model.GetChannelById(info.ChannelId, true)
	if err != nil || stored == nil || stored.OwnerId <= 0 || stored.Type != info.ChannelType || !common.IsEncryptedSecret(stored.Key) {
		return nil, service.AccountEgress{}, "", 0, service.ErrAccountProtocol
	}
	plain, err := common.DecryptSecret(stored.Key)
	if err != nil {
		return nil, service.AccountEgress{}, "", 0, service.ErrAccountProtocol
	}
	if plain != info.ApiKey {
		previous := *stored
		previous.Key = info.ApiKey
		plain, err = rotatedAccountSecret(&previous, stored, info.ApiKey)
		if err != nil {
			return nil, service.AccountEgress{}, "", 0, service.ErrAccountProtocol
		}
		info.ApiKey = plain
	}
	var egress service.AccountEgress
	var token string
	var until int64
	if info.ChannelType == constant.ChannelTypeCline {
		var c service.ClineCredential
		if common.Unmarshal([]byte(plain), &c) != nil || !service.HeaderCredential(c.UserID, 256) {
			return nil, egress, "", 0, service.ErrAccountProtocol
		}
		egress, token, until = c.Egress, c.Token, c.ExpiresAt
		// Stored service credentials are raw tokens. Accept the historic single
		// workos prefix without ever emitting it twice; malformed nesting is rejected.
		token = strings.TrimPrefix(token, "workos:")
		if strings.HasPrefix(token, "workos:") {
			return nil, egress, "", 0, service.ErrAccountProtocol
		}
	} else {
		var c service.RaccoonCredential
		if common.Unmarshal([]byte(plain), &c) != nil || !service.HeaderCredential(c.UserID, 256) {
			return nil, egress, "", 0, service.ErrAccountProtocol
		}
		egress, token, until = c.Egress, c.Token, c.ExpiresAt
	}
	if !service.HeaderCredential(token, 16384) || until < 0 {
		return nil, egress, "", 0, service.ErrAccountProtocol
	}
	return stored, egress, token, until, nil
}

// channelSettings fails closed without GetSetting's database repair side effect.
func channelSettings(stored *model.Channel) (dto.ChannelSettings, error) {
	var settings dto.ChannelSettings
	if stored.Setting != nil && *stored.Setting != "" && common.Unmarshal([]byte(*stored.Setting), &settings) != nil {
		return settings, service.ErrAccountProtocol
	}
	if settings.TLSInsecureSkipVerify {
		return settings, service.ErrAccountProtocol
	}
	return settings, nil
}

// accountSnapshot binds refresh/dispatch to the configuration that selected the
// account, not just its token. JSON preserves nil versus empty setting values.
func accountSnapshot(stored *model.Channel) ([32]byte, error) {
	raw, err := common.Marshal(struct {
		Owner, Kind, Status              int
		Models                           string
		Settings, BaseURL, Header, Param *string
	}{stored.OwnerId, stored.Type, stored.Status, stored.Models, stored.Setting, stored.BaseURL, stored.HeaderOverride, stored.ParamOverride})
	if err != nil {
		return [32]byte{}, service.ErrAccountProtocol
	}
	return sha256.Sum256(raw), nil
}

func unchangedAccount(stored, latest *model.Channel) error {
	if stored == nil || latest == nil || stored.Id != latest.Id || latest.OwnerId != stored.OwnerId || latest.Type != stored.Type {
		return service.ErrAccountProtocol
	}
	before, err := accountSnapshot(stored)
	if err != nil {
		return err
	}
	after, err := accountSnapshot(latest)
	if err != nil || before != after {
		return service.ErrAccountProtocol
	}
	return nil
}

func identicalAccountSnapshot(stored, latest *model.Channel) error {
	if latest.Key != stored.Key {
		return service.ErrAccountProtocol
	}
	return unchangedAccount(stored, latest)
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, h *http.Header, info *relaycommon.RelayInfo) error {
	if c == nil || c.Request == nil || h == nil || info == nil || info.ChannelMeta == nil || len(relaycommon.GetEffectiveHeaderOverride(info)) > 0 {
		return service.ErrAccountProtocol
	}
	_, _, token, until, err := account(info)
	if err != nil {
		return err
	}
	if until > 0 && until <= time.Now().Unix() {
		return service.ErrDesktopLoginExpired
	}
	// Build an allowlist, never retain caller/provider credentials or cookies.
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	if info.IsStream {
		headers.Set("Accept", "text/event-stream")
	} else {
		headers.Set("Accept", "application/json")
	}
	if info.ChannelType == constant.ChannelTypeCline {
		headers.Set("Authorization", "Bearer workos:"+token)
		for name, values := range service.ClineHeaders() {
			headers[name] = append([]string(nil), values...)
		}
	} else {
		headers.Set("Authorization", "Bearer "+token)
		headers.Set("Origin", service.RaccoonOrigin)
		headers.Set("Referer", service.RaccoonOrigin+"/")
	}
	*h = headers
	return nil
}

func refreshKey(stored *model.Channel, secret string) string {
	return strconv.Itoa(stored.Id) + ":" + common.GenerateHMACWithKey([]byte(secret), "browser-account-refresh")
}

func refreshCredential(ctx context.Context, stored *model.Channel, egress service.AccountEgress, secret, proxy string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result := refreshes.DoChan(refreshKey(stored, secret), func() (any, error) {
		// A canceled duplicate waiter returns independently. The leader's provider
		// request is bounded and canceled with its originating request.
		refreshCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		if err := refreshCtx.Err(); err != nil {
			return nil, err
		}
			latest, err := model.GetChannelById(stored.Id, true)
			if err != nil || latest.OwnerId != stored.OwnerId || latest.Type != stored.Type {
				return nil, service.ErrAccountProtocol
			}
			if latest.Key != stored.Key {
				rotated, rotateErr := rotatedAccountSecret(stored, latest, secret)
				if rotateErr != nil {
					return nil, service.ErrAccountProtocol
				}
				return rotated, nil
			}
			if identicalAccountSnapshot(stored, latest) != nil {
				return nil, service.ErrAccountProtocol
			}
			settings, err := channelSettings(latest)
			if err != nil {
				return nil, err
			}
			if latest.Models != stored.Models || latest.OwnerId != stored.OwnerId || latest.Type != stored.Type {
				return nil, service.ErrAccountProtocol
			}
			pinned, err := egress.Resolve(latest.OwnerId, latest.Id, settings)
			if err != nil || pinned != proxy {
				return nil, service.ErrAccountEgress
			}
			var raw []byte
		if stored.Type == constant.ChannelTypeCline {
			var prior service.ClineCredential
			if common.Unmarshal([]byte(secret), &prior) != nil {
				return nil, service.ErrAccountProtocol
			}
			next, e := service.RenewClineCredential(refreshCtx, proxy, &prior)
			if e != nil {
				return nil, e
			}
			if next == nil || next.UserID != prior.UserID || next.Egress != prior.Egress {
				return nil, service.ErrAccountProtocol
			}
			raw, err = common.Marshal(next)
		} else if stored.Type == constant.ChannelTypeRaccoon {
			var prior service.RaccoonCredential
			if common.Unmarshal([]byte(secret), &prior) != nil {
				return nil, service.ErrAccountProtocol
			}
			next, e := service.RefreshRaccoonAccount(refreshCtx, proxy, &prior)
			if e != nil {
				return nil, e
			}
			if next == nil || next.UserID != prior.UserID || next.Egress != prior.Egress {
				return nil, service.ErrAccountProtocol
			}
			raw, err = common.Marshal(next)
		} else {
			return nil, service.ErrAccountProtocol
		}
		if err != nil {
			return nil, service.ErrAccountProtocol
		}
		encrypted, err := common.EncryptSecret(string(raw))
		if err != nil || !common.IsEncryptedSecret(encrypted) {
			return nil, service.ErrAccountProtocol
		}
		// Provider I/O completes before the short CAS write. Guard all identity and
		// transport fields, including concurrent settings edits during refresh.
		update := model.DB.WithContext(refreshCtx).Model(&model.Channel{}).Where(map[string]any{
			"id": stored.Id, "owner_id": stored.OwnerId, "type": stored.Type, "status": stored.Status, "key": stored.Key, "models": stored.Models,
			"setting": stored.Setting, "base_url": stored.BaseURL, "header_override": stored.HeaderOverride, "param_override": stored.ParamOverride,
		}).Update("key", encrypted)
		if update.Error != nil {
			if refreshCtx.Err() != nil {
				return nil, refreshCtx.Err()
			}
			return nil, service.ErrAccountProtocol
		}
			if update.RowsAffected != 1 {
				rotated, readErr := model.GetChannelById(stored.Id, true)
				if readErr != nil {
					return nil, service.ErrAccountProtocol
				}
				rotatedSecret, readErr := rotatedAccountSecret(stored, rotated, secret)
				if readErr != nil {
					return nil, service.ErrAccountProtocol
				}
				return rotatedSecret, nil
			}
		model.InitChannelCache()
		return string(raw), nil
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-result:
		if res.Err != nil {
			return "", res.Err
		}
		raw, ok := res.Val.(string)
		if !ok || raw == "" {
			return "", service.ErrAccountProtocol
		}
		return raw, nil
	}
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	if c == nil || c.Request == nil || body == nil {
		return nil, service.ErrAccountProtocol
	}
	target, err := a.GetRequestURL(info)
	if err != nil {
		return nil, err
	}
	if len(relaycommon.GetEffectiveHeaderOverride(info)) != 0 {
		return nil, service.ErrAccountProtocol
	}
	if err := c.Request.Context().Err(); err != nil {
		return nil, err
	}
	stored, egress, _, until, err := account(info)
	if err != nil {
		return nil, err
	}
	settings, err := channelSettings(stored)
	if err != nil {
		return nil, err
	}
	modelKey := info.UpstreamModelName
	if modelKey == "" {
		modelKey = info.OriginModelName
	}
	if !service.HeaderCredential(modelKey, 256) {
		return nil, service.ErrAccountProtocol
	}
	allowed := false
	for id := range strings.SplitSeq(stored.Models, ",") {
		if id == modelKey {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, service.ErrAccountProtocol
	}
	proxy, err := egress.Resolve(stored.OwnerId, stored.Id, settings)
	if err != nil {
		return nil, err
	}
	if until > 0 && until <= time.Now().Add(90*time.Second).Unix() {
		next, err := refreshCredential(c.Request.Context(), stored, egress, info.ApiKey, proxy)
		if err != nil {
			return nil, err
		}
		info.ApiKey = next
		// Bind the dispatch to the original snapshot despite the expected key change.
		latest, nextEgress, _, _, err := account(info)
		if err != nil {
			return nil, err
		}
		expected := *stored
		expected.Key = latest.Key
		if unchangedAccount(&expected, latest) != nil || nextEgress != egress {
			return nil, service.ErrAccountProtocol
		}
		settings, err = channelSettings(latest)
		if err != nil {
			return nil, err
		}
		pinned, err := nextEgress.Resolve(latest.OwnerId, latest.Id, settings)
		if err != nil || pinned != proxy {
			return nil, service.ErrAccountEgress
		}
		stored = latest
	}
	// Revalidate immediately before dispatch even if no refresh was needed.
	// A channel moved to another owner or changed while resolving its egress
	// must never reuse the earlier owner's pinned route.
	latest, latestEgress, _, _, err := account(info)
	if err != nil {
		return nil, err
	}
	if unchangedAccount(stored, latest) != nil || latestEgress != egress {
		return nil, service.ErrAccountProtocol
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target, body)
	if err != nil {
		return nil, service.ErrAccountProtocol
	}
	channel.ApplyUpstreamBodyMetadata(req, body)
	headers := req.Header
	if err := a.SetupRequestHeader(c, &headers, info); err != nil {
		return nil, err
	}
	req.Header = headers
	// Unlike generic dispatch this client has no ambient proxy, global insecure
	// TLS setting, cookie jar, redirects, account rotation, or automatic retry.
	client, err := service.NewQoderHTTPClient(proxy)
	if err != nil {
		return nil, err
	}
	copyClient := *client
	copyClient.Timeout = 0 // The relay request context controls chat/stream lifetime.
	resp, err := copyClient.Do(req)
	if err != nil {
		if c.Request.Context().Err() != nil {
			return nil, c.Request.Context().Err()
		}
		return nil, service.ErrAccountUpstream
	}
	if requestID := resp.Header.Get(common.RequestIdKey); requestID != "" {
		c.Set(common.UpstreamRequestIdKey, requestID)
	}
	return resp, nil
}

func (a *Adaptor) GetModelList() []string { return nil }
func (a *Adaptor) GetChannelName() string { return "browseraccount" }

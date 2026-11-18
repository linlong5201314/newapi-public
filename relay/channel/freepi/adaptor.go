// Package freepi adds only the verified FreePi transport contract. Response
// parsing and usage/settlement remain the existing OpenAI implementations. It
// never invents advertisement impressions, resets user sessions or rotates users.
package freepi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Adaptor struct{ openai.Adaptor }

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayMode != relayconstant.RelayModeChatCompletions {
		return "", errors.New("FreePi supports only chat completions on this gateway")
	}
	if strings.TrimRight(info.ChannelBaseUrl, "/") != "https://api.freepi.ai" {
		return "", errors.New("FreePi requires its verified upstream endpoint")
	}
	return "https://api.freepi.ai/v1/chat/completions", nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	if len(relaycommon.GetEffectiveHeaderOverride(info)) != 0 {
		return errors.New("FreePi authorization and session headers cannot be overridden")
	}
	if info.ChannelId <= 0 || info.ApiKey == "" || common.IsEncryptedSecret(info.ApiKey) || common.IsPlainPrefixedSecret(info.ApiKey) {
		return errors.New("FreePi channel credentials are unavailable")
	}
	ownerChannel, err := model.GetChannelById(info.ChannelId, true)
	if err != nil || ownerChannel.OwnerId <= 0 {
		return errors.New("FreePi requires an owner-scoped channel")
	}
	if ownerChannel.Type != info.ChannelType || !common.IsEncryptedSecret(ownerChannel.Key) {
		return errors.New("FreePi requires encrypted owner account credentials")
	}
	key, err := common.DecryptSecret(ownerChannel.Key)
	if err != nil || key == "" || key != info.ApiKey {
		return errors.New("FreePi owner credentials changed; retry authorization")
	}
	other := ownerChannel.GetOtherInfo()
	version, _ := other["freepiClientVersion"].(string)
	if version == "" || len(version) > 32 || strings.ContainsAny(version, "\r\n") {
		return errors.New("FreePi client version is unavailable; authorize the account again")
	}
	channel.SetupApiRequestHeader(info, c, header)
	header.Set("Authorization", "Bearer "+info.ApiKey)
	// Account JWT + owner scopes the stable lease without exposing either. A
	// public channel continues to use its consenting owner's lease, not caller ID.
	session := uuid.NewSHA1(uuid.NameSpaceOID, []byte("freepi:"+strconv.Itoa(ownerChannel.OwnerId)+":"+info.ApiKey))
	header.Set("x-session-id", session.String())
	header.Set("x-turn-id", uuid.NewString())
	header.Set("x-client-version", version)
	header.Set("x-client-locale", "en")
	header.Set("x-client-timezone", "UTC")
	return nil
}

// DoRequest must use the wrapper as the callback receiver, not the embedded
// adaptor's method, otherwise Go's promoted method calls bypass these headers.
func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, body)
}

func (a *Adaptor) GetModelList() []string { return nil } // Live /client-version only.
func (a *Adaptor) GetChannelName() string { return "freepi" }

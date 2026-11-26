package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type xiaoyiWorkFakeTransport struct{ t *testing.T }

func (f xiaoyiWorkFakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	require.NoError(f.t, err)
	reply := func(status int, text string) *http.Response {
		response, responseErr := selfContractResponse(status, text)
		require.NoError(f.t, responseErr)
		return response
	}
	switch request.URL.Host {
	case "oauth-login.cloud.huawei.com":
		assert.Equal(f.t, "/oauth2/v3/token", request.URL.Path)
		form, err := url.ParseQuery(string(body))
		require.NoError(f.t, err)
		assert.Equal(f.t, "authorization_code", form.Get("grant_type"))
		assert.NotEmpty(f.t, form.Get("code_verifier"))
		assert.Equal(f.t, "fixture-code", form.Get("code"))
		idToken := "fixture." + base64.RawURLEncoding.EncodeToString([]byte(`{"uid":"fixture-uid"}`)) + ".fixture"
		return reply(200, `{"access_token":"fixture-access-token","refresh_token":"fixture-refresh-token","id_token":"`+idToken+`"}`), nil
	case "hag-drcn.op.dbankcloud.com":
		switch request.URL.Path {
		case "/user-credential/v1/business-credential/create":
			assert.Equal(f.t, "Bearer fixture-access-token", request.Header.Get("Authorization"))
			assert.Equal(f.t, "fixture-uid", request.Header.Get("X-Uid"))
			return reply(200, `{"credentialResult":{"businessCredential":"fixture-business-credential","validTime":4102444800}}`), nil
		case "/user-ability-gallery/v1/agent/user/query":
			assert.Equal(f.t, "fixture-business-credential", request.Header.Get("Businesscredential"))
			assert.Equal(f.t, "fixture-uid", request.Header.Get("X-Uid"))
			assert.Equal(f.t, "com.huawei.hmos.vassistant", request.Header.Get("x-prd-pkg-name"))
			return reply(200, `{"contents":[{"modelList":[{"modelValue":"model-one","modelName":"Model One","aiPointConsumptionRatio":"消耗1x"},{"modelValue":"model-preview","modelName":"Preview","gray":true}]}]}`), nil
		case "/celia/v1/sse-api/chat/completions":
			assert.Equal(f.t, "fixture-business-credential", request.Header["businessCredential"][0])
			assert.Equal(f.t, "fixture-uid", request.Header["x-uid"][0])
			assert.NotEmpty(f.t, request.Header["x-device-id"][0])
			assert.Contains(f.t, string(body), `"model":"model-one"`)
			return reply(200, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n"), nil
		}
	}
	f.t.Errorf("unexpected upstream request: %s", request.URL)
	return reply(500, `{}`), nil
}

func xiaoyiWorkAuthClassRequest(identity model.AuthSessionIdentity, origin, requestURL string, handler gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", identity.UserID)
	c.Set("session_id", identity.SessionID)
	c.Set("auth_version", identity.UserAuthVersion)
	c.Set("session_version", identity.SessionVersion)
	c.Set("role", common.RoleCommonUser)
	c.Request = httptest.NewRequest(http.MethodPost, origin+requestURL, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Origin", origin)
	c.Request.Header.Set("X-Account-Action", "authorize")
	handler(c)
	return recorder
}

func TestXiaoyiWorkNativeAuthorizationAndModelCall(t *testing.T) {
	identity := authClassSession(t, "https://gateway.example")
	restore := service.SetXiaoyiWorkTransportForTest(xiaoyiWorkFakeTransport{t: t})
	t.Cleanup(restore)
	require.NoError(t, model.DB.AutoMigrate(&model.SelfOAuthSession{}))
	start := authClassRequest(identity, "https://gateway.example", SelfXiaoyiWorkOAuthStart, `{}`)
	require.Contains(t, start.Body.String(), `"success":true`, start.Body.String())
	var response struct {
		Data struct {
			SessionID string `json:"sessionId"`
			AuthURL   string `json:"authUrl"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(start.Body.Bytes(), &response))
	assert.Contains(t, response.Data.AuthURL, "code_challenge=")
	assert.Contains(t, response.Data.AuthURL, "redirect_uri=http%3A%2F%2F127.0.0.1%3A17355%2Fauth%2Fcallback")
	assert.NotContains(t, response.Data.AuthURL, "magpie")
	poll := xiaoyiWorkAuthClassRequest(identity, "https://gateway.example", "/api/channel/self/oauth/xiaoyiwork/poll?sessionId="+response.Data.SessionID, SelfXiaoyiWorkOAuthPoll, `{}`)
	require.Contains(t, poll.Body.String(), `"status":"pending"`, poll.Body.String())
	complete := authClassRequest(identity, "https://gateway.example", SelfXiaoyiWorkOAuthComplete, `{"sessionId":"`+response.Data.SessionID+`","code":"fixture-code"}`)
	require.Contains(t, complete.Body.String(), `"success":true`, complete.Body.String())
	donePoll := xiaoyiWorkAuthClassRequest(identity, "https://gateway.example", "/api/channel/self/oauth/xiaoyiwork/poll?sessionId="+response.Data.SessionID, SelfXiaoyiWorkOAuthPoll, `{}`)
	require.Contains(t, donePoll.Body.String(), `"done":true`, donePoll.Body.String())
	var channel model.Channel
	require.NoError(t, model.DB.Where("owner_id = ? AND name = ?", identity.UserID, "华为小艺 Work").First(&channel).Error)
	assert.Equal(t, 0, channel.Visibility)
	assert.Equal(t, service.XiaoyiWorkSSEEndpoint, channel.GetBaseURL())
	assert.Equal(t, "model-one,model-preview", channel.Models)
	assert.True(t, common.IsEncryptedSecret(channel.Key))
	assert.NotContains(t, channel.Key, "fixture-business-credential")
	session, err := xiaoyiWorkSessionForChannel(&channel)
	require.NoError(t, err)
	assert.Equal(t, "fixture-business-credential", session.Credential)
	assert.Equal(t, "fixture-uid", session.UID)
	assert.Contains(t, channel.OtherInfo, "model-preview")
	assert.NotContains(t, channel.OtherInfo, `"unavailableModels":["model-preview"]`)
	request := httptest.NewRequest(http.MethodPost, service.XiaoyiWorkSSEEndpoint, strings.NewReader(`{"model":"model-one","messages":[{"role":"user","content":"hello"}]}`))
	upstream, apiErr := service.DoXiaoyiWorkRelay(context.Background(), channel.Id, channel.Key, request, "model-one", false)
	require.Nil(t, apiErr, fmt.Sprintf("%+v", apiErr))
	require.Equal(t, http.StatusOK, upstream.StatusCode)
	body, err := io.ReadAll(upstream.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"content":"OK"`)
	assert.Contains(t, string(body), `"total_tokens":3`)
	assert.NotContains(t, string(body), "fixture-business-credential")

	require.NoError(t, model.DB.First(&channel, channel.Id).Error)
	streamRequest := httptest.NewRequest(http.MethodPost, service.XiaoyiWorkSSEEndpoint, strings.NewReader(`{"model":"model-one","stream":true,"messages":[]}`))
	streamResponse, apiErr := service.DoXiaoyiWorkRelay(context.Background(), channel.Id, channel.Key, streamRequest, "model-one", true)
	require.Nil(t, apiErr)
	streamBody, err := io.ReadAll(streamResponse.Body)
	require.NoError(t, err)
	assert.Contains(t, string(streamBody), `"content":"OK"`)
	assert.Contains(t, string(streamBody), "data: [DONE]")

	visibilityBody, err := common.Marshal(map[string]any{"channelId": channel.Id, "visibility": 1, "publicModels": []string{"model-one"}, "share_confirmed": true})
	require.NoError(t, model.DB.First(&channel, channel.Id).Error)
	t.Logf("saved channel type=%d visibility=%d models=%q otherInfo=%q keyPrefix=%t", channel.Type, channel.Visibility, channel.Models, channel.OtherInfo, common.IsEncryptedSecret(channel.Key))
	var original model.Channel
	require.NoError(t, model.DB.First(&original, channel.Id).Error)
	t.Logf("original snapshot type=%d visibility=%d models=%q otherInfo=%q keyMatch=%t", original.Type, original.Visibility, original.Models, original.OtherInfo, original.Key == channel.Key)
	visibility := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(visibility)
	ctx.Set("id", identity.UserID)
	ctx.Set("session_id", identity.SessionID)
	ctx.Set("auth_version", identity.UserAuthVersion)
	ctx.Set("session_version", identity.SessionVersion)
	ctx.Set("role", common.RoleCommonUser)
	ctx.Request = httptest.NewRequest(http.MethodPost, "https://gateway.example/visibility", bytes.NewReader(visibilityBody))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("Origin", "https://gateway.example")
	ctx.Request.Header.Set("X-Account-Action", "authorize")
	SetSelfChannelVisibility(ctx)
	require.Contains(t, visibility.Body.String(), `"success":true`, visibility.Body.String())
	require.NoError(t, model.DB.First(&channel, channel.Id).Error)
	assert.Equal(t, 1, channel.Visibility)
	assert.True(t, model.ChannelModelVisibleToOwner(&channel, "model-one", identity.UserID+1))
	assert.False(t, model.ChannelModelVisibleToOwner(&channel, "model-preview", identity.UserID+1))

	updateBody := `{"id":` + fmt.Sprint(channel.Id) + `,"name":"Xiaoyi Work updated","type":76,"base_url":"` + service.XiaoyiWorkSSEEndpoint + `","key":"","model_mapping":""}`
	updated := xiaoyiWorkAuthClassRequest(identity, "https://gateway.example", "/api/channel/self/", UpdateSelfChannel, updateBody)
	require.Contains(t, updated.Body.String(), `"success":true`, updated.Body.String())
	assert.NotContains(t, updated.Body.String(), "Reconnect Xiaoyi Work")
}

func TestXiaoyiWorkSharedRelayBillsTheCallingUser(t *testing.T) {
	require.NoError(t, i18n.Init())
	identity := authClassSession(t, "https://gateway.example")
	require.NoError(t, model.DB.AutoMigrate(&model.Token{}, &model.UserSubscription{}))
	restore := service.SetXiaoyiWorkTransportForTest(xiaoyiWorkFakeTransport{t: t})
	t.Cleanup(restore)
	oldPrice := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrice)) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"model-one":0.001}`))
	raw, err := common.Marshal(&service.XiaoyiWorkOAuthSession{AccessToken: "fixture-access-token", RefreshToken: "fixture-refresh-token", UID: "fixture-uid", DeviceID: "fixture-device", Credential: "fixture-business-credential", ValidTimeSec: 4102444800})
	require.NoError(t, err)
	secret, err := common.EncryptSecret(string(raw))
	require.NoError(t, err)
	channel := &model.Channel{Type: constant.ChannelTypeXiaoyiWork, Name: "native-shared-fixture", Key: secret, OwnerId: identity.UserID, Status: common.ChannelStatusEnabled, Group: "default", Models: "model-one", Tier: "api", Visibility: 1}
	channel.SetOtherInfo(map[string]any{"templateId": constant.XiaoyiWorkTemplateID, "publicModels": []string{"model-one"}, "allModels": []string{"model-one"}})
	base := service.XiaoyiWorkSSEEndpoint
	channel.BaseURL = &base
	require.NoError(t, model.DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	fixtureID := uuid.NewString()
	caller := &model.User{Username: "xiaoyi-shared-caller-" + fixtureID, Password: "fixture", Status: common.UserStatusEnabled, Group: "default", Quota: 1000, AffCode: "shared-" + fixtureID[:24]}
	require.NoError(t, model.DB.Create(caller).Error)
	apiToken := &model.Token{UserId: caller.Id, Key: "shared-fixture-key-" + fixtureID, Status: common.TokenStatusEnabled, Name: "shared fixture", RemainQuota: 1000, Group: "default"}
	require.NoError(t, model.DB.Create(apiToken).Error)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/chat/completions", strings.NewReader(`{"model":"model-one","messages":[{"role":"user","content":"hello"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	for key, value := range map[constant.ContextKey]any{
		constant.ContextKeyChannelId: channel.Id, constant.ContextKeyChannelType: channel.Type,
		constant.ContextKeyChannelKey:     model.DecryptChannelKey(channel.Type, channel.Key),
		constant.ContextKeyChannelBaseUrl: channel.GetBaseURL(), constant.ContextKeyChannelSetting: channel.GetSetting(),
		constant.ContextKeyChannelAllowedModels: []string{"model-one"}, constant.ContextKeyChannelTemplateID: constant.XiaoyiWorkTemplateID,
		constant.ContextKeyOriginalModel: "model-one", constant.ContextKeyUserId: caller.Id,
		constant.ContextKeyUserQuota: caller.Quota, constant.ContextKeyUserGroup: "default", constant.ContextKeyUsingGroup: "default",
		constant.ContextKeyTokenId: apiToken.Id, constant.ContextKeyTokenKey: apiToken.Key,
	} {
		common.SetContextKey(c, key, value)
	}
	c.Set("token_name", apiToken.Name)
	info, err := relaycommon.GenRelayInfo(c, kittypes.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{Model: "model-one", Messages: []dto.Message{{Role: "user", Content: "hello"}}}, nil)
	require.NoError(t, err)
	require.Nil(t, relay.PrepareRequestBilling(c, info))
	require.Nil(t, relay.TextHelper(c, info), recorder.Body.String())
	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"content":"OK"`)
	var chargedUser model.User
	require.NoError(t, model.DB.First(&chargedUser, caller.Id).Error)
	assert.Equal(t, 500, chargedUser.Quota)
	var owner model.User
	require.NoError(t, model.DB.First(&owner, identity.UserID).Error)
	assert.Zero(t, owner.UsedQuota)
	var chargedToken model.Token
	require.NoError(t, model.DB.First(&chargedToken, apiToken.Id).Error)
	assert.Equal(t, 500, chargedToken.RemainQuota)
	var chargedLog model.Log
	require.NoError(t, model.DB.Where("user_id = ? AND channel_id = ?", caller.Id, channel.Id).First(&chargedLog).Error)
	assert.Equal(t, 500, chargedLog.Quota)
}

// A pending session that already failed or was consumed is not "expired".
// Telling the user to wait out a window they are not in sends them in circles;
// the message has to name the state that actually blocks the retry.
func TestXiaoyiWorkReportsTheRealAuthorizationProblem(t *testing.T) {
	identity := authClassSession(t, "https://gateway.example")
	restore := service.SetXiaoyiWorkTransportForTest(xiaoyiWorkFakeTransport{t: t})
	t.Cleanup(restore)
	require.NoError(t, model.DB.AutoMigrate(&model.SelfOAuthSession{}))

	startSession := func() string {
		start := authClassRequest(identity, "https://gateway.example", SelfXiaoyiWorkOAuthStart, `{}`)
		require.Contains(t, start.Body.String(), `"success":true`, start.Body.String())
		var response struct {
			Data struct {
				SessionID string `json:"sessionId"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(start.Body.Bytes(), &response))
		require.NotEmpty(t, response.Data.SessionID)
		return response.Data.SessionID
	}

	failedState := startSession()
	require.NoError(t, model.UpdateSelfOAuthSession(xiaoyiWorkStateHash(failedState), map[string]any{"status": "failed"}))
	failed := authClassRequest(identity, "https://gateway.example", SelfXiaoyiWorkOAuthComplete, `{"sessionId":"`+failedState+`","code":"fixture-code"}`)
	assert.Contains(t, failed.Body.String(), "previous Xiaoyi Work authorization attempt did not finish", failed.Body.String())
	assert.NotContains(t, failed.Body.String(), "expired", failed.Body.String())

	usedState := startSession()
	complete := authClassRequest(identity, "https://gateway.example", SelfXiaoyiWorkOAuthComplete, `{"sessionId":"`+usedState+`","code":"fixture-code"}`)
	require.Contains(t, complete.Body.String(), `"success":true`, complete.Body.String())
	reused := authClassRequest(identity, "https://gateway.example", SelfXiaoyiWorkOAuthComplete, `{"sessionId":"`+usedState+`","code":"fixture-code"}`)
	assert.Contains(t, reused.Body.String(), "already used", reused.Body.String())
	assert.NotContains(t, reused.Body.String(), "expired", reused.Body.String())

	cancelledState := startSession()
	cancel := xiaoyiWorkAuthClassRequest(identity, "https://gateway.example", "/api/channel/self/oauth/xiaoyiwork/cancel", SelfXiaoyiWorkOAuthCancel, `{"sessionId":"`+cancelledState+`"}`)
	require.Contains(t, cancel.Body.String(), `"success":true`, cancel.Body.String())
	afterCancel := authClassRequest(identity, "https://gateway.example", SelfXiaoyiWorkOAuthComplete, `{"sessionId":"`+cancelledState+`","code":"fixture-code"}`)
	assert.Contains(t, afterCancel.Body.String(), "was cancelled", afterCancel.Body.String())
	assert.NotContains(t, afterCancel.Body.String(), "expired", afterCancel.Body.String())
}

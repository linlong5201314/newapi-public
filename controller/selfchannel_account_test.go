package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelfChannelFullEditorPayloadPreservesOwnerFields(t *testing.T) {
	uid := setupChannelCreateTest(t)
	channel := &model.Channel{OwnerId: uid, Name: "combined-editor", Type: constant.ChannelTypeOpenAI, Key: "old-key", Models: "model-one", Group: "default", Other: "keep-me"}
	channel.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeyMode: constant.MultiKeyMode("random")}
	require.NoError(t, channel.Insert())
	body := fmt.Sprintf(`{"id":%d,"type":1,"name":"combined-editor-renamed","models":"model-one,model-two","group":"default","openai_organization":"org-combined","weight":2,"key":"key-one\nkey-two","multi_key_mode":"random","key_mode":"replace","setting":"{\"proxy_policy\":{\"mode\":\"direct\"}}","header_override":"{\"X-Test\":\"kept\"}","param_override":"{\"temperature\":0.2}","other":"keep-me"}`, channel.Id)
	recorder := selfContractRequest(uid, http.MethodPut, "/", body, UpdateSelfChannel)
	require.Contains(t, recorder.Body.String(), `"success":true`, recorder.Body.String())
	var stored model.Channel
	require.NoError(t, model.DB.First(&stored, channel.Id).Error)
	assert.Equal(t, "combined-editor-renamed", stored.Name)
	assert.Equal(t, []string{"key-one", "key-two"}, stored.GetKeys())
	assert.True(t, stored.ChannelInfo.IsMultiKey)
	require.NotNil(t, stored.OpenAIOrganization)
	assert.Equal(t, "org-combined", *stored.OpenAIOrganization)
	assert.Equal(t, `{"X-Test":"kept"}`, *stored.HeaderOverride)
	assert.Equal(t, `{"temperature":0.2}`, *stored.ParamOverride)
	assert.Equal(t, "keep-me", stored.Other)
	assert.EqualValues(t, 2, stored.GetWeight())
	assert.Equal(t, "direct", stored.GetSetting().ProxyPolicy.Mode)
}

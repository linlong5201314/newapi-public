package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelfChannelSharedSubsetPersistsWithoutChangingOwnership(t *testing.T) {
	uid := setupChannelCreateTest(t)
	channel := &model.Channel{OwnerId: uid, Name: "shared-editor", Type: constant.ChannelTypeOpenAI, Key: "fixture-key", Models: "model-one,model-two", Group: "default"}
	channel.SetOtherInfo(map[string]any{"allModels": []string{"model-one", "model-two"}})
	require.NoError(t, channel.Insert())
	recorder := selfContractRequest(uid, http.MethodPost, "/visibility", `{"channelId":`+strconv.Itoa(channel.Id)+`,"visibility":1,"tier":"free","publicModels":["model-one"],"allModels":["model-one","model-two"],"share_confirmed":true}`, SetSelfChannelVisibility)
	require.Contains(t, recorder.Body.String(), `"success":true`, recorder.Body.String())
	var stored model.Channel
	require.NoError(t, model.DB.First(&stored, channel.Id).Error)
	assert.Equal(t, 1, stored.Visibility)
	assert.Equal(t, "free", stored.Tier)
	assert.Equal(t, uid, stored.OwnerId)
	assert.Equal(t, "model-one,model-two", stored.Models)
	info := stored.GetOtherInfo()
	assert.Equal(t, []any{"model-one"}, info["publicModels"])
	assert.Equal(t, []any{"model-one", "model-two"}, info["allModels"])
}

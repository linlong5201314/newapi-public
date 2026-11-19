package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func publicAdminRequest(t *testing.T, handler gin.HandlerFunc, method, target, body string, role, userID, channelID int) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(method, target, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", userID)
	c.Set("role", role)
	if channelID != 0 {
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(channelID)}}
	}
	handler(c)
	return response
}

func TestPublicPoolAdminRejectsNonRootBeforeDatabaseAccess(t *testing.T) {
	for _, handler := range []gin.HandlerFunc{ListPublicPoolAdminChannels, GetPublicPoolAdminChannel, AddPublicPoolAdminChannel, UpdatePublicPoolAdminChannel, DeletePublicPoolAdminChannel, CopyPublicPoolAdminChannel, SetPublicPoolAdminChannelStatus} {
		for _, role := range []int{0, common.RoleCommonUser, common.RoleAdminUser} {
			response := publicAdminRequest(t, handler, http.MethodPost, "/api/public_pool/admin/channels/1", `{}`, role, 1, 1)
			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.Contains(t, response.Body.String(), `"success":false`)
		}
	}
}

func TestPublicPoolAdminListsOnlyFreePlatformAndNeverReturnsProviderSecrets(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	secretURL := "https://fixture.invalid/?access_token=fixture-secret-url"
	settings := `{"proxy":"http://fixture-user:fixture-password@fixture.invalid:8080"}`
	header := `{"Authorization":"Bearer fixture-secret-header"}`
	for _, channel := range []*model.Channel{
		{Name: "platform-free", OwnerId: 0, Tier: "free", Key: "fixture-secret-key", Type: 1, Models: "model-a", Group: "default", BaseURL: &secretURL, Setting: &settings, HeaderOverride: &header},
		{Name: "platform-paid", OwnerId: 0, Tier: "api", Key: "paid-key", Type: 1, Models: "model-paid", Group: "default"},
		{Name: "private-owner", OwnerId: rootID, Tier: "free", Key: "private-key", Type: 1, Models: "private-model", Group: "default"},
		{Name: "shared-owner", OwnerId: 42, Tier: "free", Visibility: 1, Key: "shared-key", Type: 1, Models: "shared-model", Group: "default"},
	} {
		require.NoError(t, model.DB.Create(channel).Error)
	}
	response := publicAdminRequest(t, ListPublicPoolAdminChannels, http.MethodGet, "/api/public_pool/admin/channels?keyword=platform&page=1&page_size=10", "", common.RoleRootUser, rootID, 0)
	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Len(t, payload.Data.Items, 1)
	assert.Equal(t, 1, payload.Data.Total)
	assert.Equal(t, "platform-free", payload.Data.Items[0]["name"])
	for _, secret := range []string{"fixture-secret", "fixture-password", "private-owner", "shared-owner", "platform-paid", `"key"`, `"base_url"`, `"setting"`, `"header_override"`} {
		assert.NotContains(t, response.Body.String(), secret)
	}
	for _, query := range []string{"no-match", "private-model", "shared-model", "model-paid"} {
		response = publicAdminRequest(t, ListPublicPoolAdminChannels, http.MethodGet, "/api/public_pool/admin/channels?keyword="+query, "", common.RoleRootUser, rootID, 0)
		assert.Contains(t, response.Body.String(), `"items":[]`, "query=%s", query)
	}
}

func TestPublicPoolAdminRejectsOtherOwnersAndPaidChannelsWithoutMutation(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	for _, channel := range []*model.Channel{
		{Name: "private", OwnerId: rootID, Tier: "free", Type: 1, Key: "fixture-private", Models: "model-a", Group: "default"},
		{Name: "shared", OwnerId: 42, Visibility: 1, Tier: "free", Type: 1, Key: "fixture-shared", Models: "model-a", Group: "default"},
		{Name: "paid", OwnerId: 0, Tier: "api", Type: 1, Key: "fixture-paid", Models: "model-a", Group: "default"},
	} {
		require.NoError(t, model.DB.Create(channel).Error)
		for _, handler := range []gin.HandlerFunc{GetPublicPoolAdminChannel, UpdatePublicPoolAdminChannel, DeletePublicPoolAdminChannel, CopyPublicPoolAdminChannel, SetPublicPoolAdminChannelStatus} {
			response := publicAdminRequest(t, handler, http.MethodPost, "/api/public_pool/admin/channels/1", `{"name":"changed","type":1,"models":"model-b","group":"default","status":1}`, common.RoleRootUser, rootID, channel.Id)
			assert.Equal(t, http.StatusNotFound, response.Code)
		}
		unchanged, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		assert.Equal(t, channel.Name, unchanged.Name)
		assert.Equal(t, channel.OwnerId, unchanged.OwnerId)
		assert.Equal(t, channel.Tier, unchanged.Tier)
	}
}

func TestPublicPoolAdminCRUDKeepsPlatformFreeScopeAndKeyServerSide(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	response := publicAdminRequest(t, AddPublicPoolAdminChannel, http.MethodPost, "/api/public_pool/admin/channels", `{"name":"new-free","type":1,"models":"model-a","group":"default","key":"fixture-provider-key","base_url":"https://fixture.invalid/v1"}`, common.RoleRootUser, rootID, 0)
	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success, response.Body.String())
	require.Positive(t, payload.Data.ID)
	id := payload.Data.ID
	created, err := model.GetChannelById(id, true)
	require.NoError(t, err)
	assert.Zero(t, created.OwnerId)
	assert.Equal(t, "free", created.Tier)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, created.Status, "new public channels must not enable untested models")
	assert.Equal(t, "fixture-provider-key", created.Key)

	response = publicAdminRequest(t, UpdatePublicPoolAdminChannel, http.MethodPut, "/api/public_pool/admin/channels/1", `{"name":"renamed","type":1,"models":"model-a,model-b","group":"default","priority":0,"weight":0}`, common.RoleRootUser, rootID, id)
	require.Contains(t, response.Body.String(), `"success":true`)
	assert.NotContains(t, response.Body.String(), "fixture-provider-key")
	updated, err := model.GetChannelById(id, true)
	require.NoError(t, err)
	assert.Equal(t, "renamed", updated.Name)
	assert.Equal(t, "fixture-provider-key", updated.Key)
	assert.Equal(t, "model-a,model-b", updated.Models)

	response = publicAdminRequest(t, SetPublicPoolAdminChannelStatus, http.MethodPost, "/api/public_pool/admin/channels/1/status", `{"status":1}`, common.RoleRootUser, rootID, id)
	require.Contains(t, response.Body.String(), `"success":true`)
	enabled, err := model.GetChannelById(id, false)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, enabled.Status)
	var ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ? AND model = ?", id, "model-b").First(&ability).Error)
	assert.True(t, ability.Enabled)

	response = publicAdminRequest(t, CopyPublicPoolAdminChannel, http.MethodPost, "/api/public_pool/admin/channels/1/copy", "", common.RoleRootUser, rootID, id)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success, response.Body.String())
	clone, err := model.GetChannelById(payload.Data.ID, true)
	require.NoError(t, err)
	assert.Equal(t, "renamed_copy", clone.Name)
	assert.Equal(t, updated.Key, clone.Key)
	assert.Equal(t, 0, clone.OwnerId)
	assert.Equal(t, "free", clone.Tier)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, clone.Status)
	assert.NotContains(t, response.Body.String(), updated.Key)

	response = publicAdminRequest(t, GetPublicPoolAdminChannel, http.MethodGet, "/api/public_pool/admin/channels/1", "", common.RoleRootUser, rootID, id)
	assert.Contains(t, response.Body.String(), `"name":"renamed"`)
	assert.NotContains(t, response.Body.String(), `"key"`)
	response = publicAdminRequest(t, DeletePublicPoolAdminChannel, http.MethodDelete, "/api/public_pool/admin/channels/1", "", common.RoleRootUser, rootID, id)
	require.Contains(t, response.Body.String(), `"success":true`)
	_, err = model.GetChannelById(id, true)
	assert.Error(t, err)
	_, err = model.GetChannelById(clone.Id, true)
	assert.NoError(t, err)
}

func TestPublicPoolAdminRejectsInvalidRoutingInputs(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	for _, patch := range []map[string]any{
		{"type": 9999}, {"type": -1}, {"priority": -1}, {"weight": -1},
		{"name": strings.Repeat("x", 129)}, {"models": "model-a,,model-b"},
		{"models": strings.Repeat("x", 256)}, {"group": strings.Repeat("x", 65)},
		{"base_url": "file:///fixture"}, {"base_url": "https://"},
	} {
		fields := map[string]any{"name": "invalid-routing", "type": 1, "models": "model-a", "group": "default", "key": "fixture-input-key"}
		for key, value := range patch {
			fields[key] = value
		}
		raw, err := common.Marshal(fields)
		require.NoError(t, err)
		response := publicAdminRequest(t, AddPublicPoolAdminChannel, http.MethodPost, "/api/public_pool/admin/channels", string(raw), common.RoleRootUser, rootID, 0)
		assert.Contains(t, response.Body.String(), `"success":false`, "input=%v", patch)
		assert.NotContains(t, response.Body.String(), "fixture-input-key")
	}
}

func TestPublicPoolAdminRejectsScopeAndStatusInjection(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	for _, extra := range []string{`,"owner_id":42`, `,"tier":"api"`, `,"status":1`, `,"header_override":"secret"`} {
		body := `{"name":"invalid","type":1,"models":"model-a","group":"default","key":"fixture-key"` + extra + `}`
		response := publicAdminRequest(t, AddPublicPoolAdminChannel, http.MethodPost, "/api/public_pool/admin/channels", body, common.RoleRootUser, rootID, 0)
		assert.Contains(t, response.Body.String(), `"success":false`)
	}
	var count int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("name = ?", "invalid").Count(&count).Error)
	assert.Zero(t, count)
}

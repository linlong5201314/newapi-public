package controller

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/ai360"
	"github.com/QuantumNous/new-api/relay/channel/lingyiwanwu"
	"github.com/QuantumNous/new-api/relay/channel/minimax"
	"github.com/QuantumNous/new-api/relay/channel/moonshot"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// https://platform.openai.com/docs/api-reference/models/list

var openAIModels []dto.OpenAIModels
var openAIModelsMap map[string]dto.OpenAIModels
var channelId2Models map[int][]string

func init() {
	// https://platform.openai.com/docs/models/model-endpoint-compatibility
	for i := range constant.APITypeDummy {
		if i == constant.APITypeAIProxyLibrary {
			continue
		}
		adaptor := relay.GetAdaptor(i)
		channelName := adaptor.GetChannelName()
		modelNames := adaptor.GetModelList()
		for _, modelName := range modelNames {
			openAIModels = append(openAIModels, dto.OpenAIModels{
				Id:      modelName,
				Object:  "model",
				Created: 1626777600,
				OwnedBy: channelName,
			})
		}
	}
	for _, modelName := range ai360.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: ai360.ChannelName,
		})
	}
	for _, modelName := range moonshot.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: moonshot.ChannelName,
		})
	}
	for _, modelName := range lingyiwanwu.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: lingyiwanwu.ChannelName,
		})
	}
	for _, modelName := range minimax.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: minimax.ChannelName,
		})
	}
	for modelName, _ := range constant.MidjourneyModel2Action {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: "midjourney",
		})
	}
	openAIModelsMap = make(map[string]dto.OpenAIModels)
	for _, aiModel := range openAIModels {
		openAIModelsMap[aiModel.Id] = aiModel
	}
	channelId2Models = make(map[int][]string)
	for i := 1; i <= constant.ChannelTypeDummy; i++ {
		apiType, success := common.ChannelType2APIType(i)
		if !success || apiType == constant.APITypeAIProxyLibrary {
			if plugin, ok := jsplugin.DefaultRegistry.GetByChannelType(i); ok {
				channelId2Models[i] = append([]string(nil), plugin.Meta.Models...)
			}
			continue
		}
		meta := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: i,
		}}
		adaptor := relay.GetAdaptor(apiType)
		adaptor.Init(meta)
		channelId2Models[i] = adaptor.GetModelList()
		if len(channelId2Models[i]) == 0 {
			if plugin, ok := jsplugin.DefaultRegistry.GetByChannelType(i); ok {
				channelId2Models[i] = append([]string(nil), plugin.Meta.Models...)
			}
		}
	}
	openAIModels = lo.UniqBy(openAIModels, func(m dto.OpenAIModels) string {
		return m.Id
	})
}

func channelOwnerName(channelType int) string {
	apiType, success := common.ChannelType2APIType(channelType)
	if !success {
		return strings.ToLower(constant.GetChannelTypeName(channelType))
	}
	adaptor := relay.GetAdaptor(apiType)
	if adaptor == nil {
		return strings.ToLower(constant.GetChannelTypeName(channelType))
	}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: channelType,
	}})
	if name := strings.TrimSpace(adaptor.GetChannelName()); name != "" {
		return name
	}
	return strings.ToLower(constant.GetChannelTypeName(channelType))
}

// buildOpenAIModel builds static provider metadata for internal catalog callers.
// User-facing listing and details must use buildVisibleOpenAIModel instead.
func buildOpenAIModel(modelName string, ownerByModel map[string]string) dto.OpenAIModels {
	aiModel, ok := openAIModelsMap[modelName]
	if !ok {
		aiModel = dto.OpenAIModels{Id: modelName, Object: "model", Created: 1626777600, OwnedBy: "custom"}
	}
	if owner := ownerByModel[modelName]; owner != "" {
		aiModel.OwnedBy = owner
	}
	aiModel.SupportedEndpointTypes = model.GetModelSupportEndpointTypes(modelName)
	return aiModel
}

// buildVisibleOpenAIModel deliberately does not consult the global pricing/endpoint
// cache: another tenant's same-name private channel must not influence metadata.
func buildVisibleOpenAIModel(modelName string, channels []*model.Channel, overrides map[string]common.EndpointInfo) dto.OpenAIModels {
	aiModel := dto.OpenAIModels{Id: modelName, Object: "model", Created: 1626777600, OwnedBy: "custom"}
	if len(channels) > 0 {
		aiModel.OwnedBy = channelOwnerName(channels[0].Type)
	}
	aiModel.SupportedEndpointTypes = make([]constant.EndpointType, 0)
	seen := make(map[constant.EndpointType]bool)
	for _, channel := range channels {
		endpoints := common.GetEndpointTypesByChannelType(channel.Type, modelName)
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				endpoints = config.SupportedEndpointTypesForModel(modelName)
			}
		}
		for _, endpoint := range endpoints {
			if !seen[endpoint] {
				seen[endpoint] = true
				aiModel.SupportedEndpointTypes = append(aiModel.SupportedEndpointTypes, endpoint)
			}
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		endpoint := constant.EndpointType(key)
		if !seen[endpoint] {
			aiModel.SupportedEndpointTypes = append(aiModel.SupportedEndpointTypes, endpoint)
		}
	}
	return aiModel
}

type modelListGroups struct {
	userGroup   string
	tokenGroup  string
	ownerGroups []string
}

func getModelListGroups(c *gin.Context) (modelListGroups, error) {
	tokenGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	if userGroup == "" && (tokenGroup == "" || tokenGroup == "auto") {
		var err error
		userGroup, err = model.GetUserGroup(c.GetInt("id"), false)
		if err != nil {
			return modelListGroups{}, err
		}
	}

	if tokenGroup == "auto" {
		return modelListGroups{
			userGroup:   userGroup,
			tokenGroup:  tokenGroup,
			ownerGroups: service.GetRequestAutoGroups(c, userGroup),
		}, nil
	}

	group := userGroup
	if tokenGroup != "" {
		group = tokenGroup
	}
	return modelListGroups{
		userGroup:   userGroup,
		tokenGroup:  tokenGroup,
		ownerGroups: []string{group},
	}, nil
}

func ListModels(c *gin.Context, modelType int) {
	acceptUnsetRatioModel := operation_setting.SelfUseModeEnabled
	if !acceptUnsetRatioModel {
		userId := c.GetInt("id")
		if userId > 0 {
			userSettings, _ := model.GetUserSetting(userId, false)
			if userSettings.AcceptUnsetRatioModel {
				acceptUnsetRatioModel = true
			}
		}
	}

	userModelNames := make([]string, 0)
	groups, err := getModelListGroups(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "get user group failed",
		})
		return
	}
	ownerGroups := groups.ownerGroups
	modelLimitEnable := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
	var tokenModelLimit map[string]bool
	if modelLimitEnable {
		s, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		if ok {
			tokenModelLimit, _ = s.(map[string]bool)
		}
		if tokenModelLimit == nil {
			tokenModelLimit = map[string]bool{}
		}
	}
	viewerId := c.GetInt("id")
	if viewerId <= 0 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	models := service.GetGroupsEnabledModelsForOwner(ownerGroups, viewerId)
	// Resolve channel ownership before the billing-config filter: a model reachable
	// through the viewer's own channel or another user's explicitly shared channel
	// must stay listable for this viewer even without a platform billing entry. The
	// billing allowlist still gates models reachable only through platform-catalog
	// channels (owner_id=0), which carry no tenant entitlement.
	channelsByModel, err := model.GetModelChannelsForOwner(models, ownerGroups, viewerId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	for _, modelName := range models {
		if modelLimitEnable {
			matchingName := ratio_setting.RoutingMatchModelName(modelName)
			if !tokenModelLimit[modelName] && !tokenModelLimit[matchingName] {
				continue
			}
		}
		tenantOwned := model.HasTenantOwnedChannel(channelsByModel[modelName], viewerId)
		if !acceptUnsetRatioModel && !tenantOwned && !helper.HasModelBillingConfig(modelName) {
			continue
		}
		userModelNames = append(userModelNames, modelName)
	}

	if len(userModelNames) != len(models) {
		filtered := make(map[string][]*model.Channel, len(userModelNames))
		for _, modelName := range userModelNames {
			filtered[modelName] = channelsByModel[modelName]
		}
		channelsByModel = filtered
	}
	endpointOverrides, err := model.GetModelEndpointOverrides(userModelNames)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userOpenAiModels := make([]dto.OpenAIModels, 0, len(userModelNames))
	for _, modelName := range userModelNames {
		if channels := channelsByModel[modelName]; len(channels) > 0 {
			userOpenAiModels = append(userOpenAiModels, buildVisibleOpenAIModel(modelName, channels, endpointOverrides[modelName]))
		}
	}

	switch modelType {
	case constant.ChannelTypeAnthropic:
		useranthropicModels := make([]dto.AnthropicModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			useranthropicModels[i] = dto.AnthropicModel{
				ID:          model.Id,
				CreatedAt:   time.Unix(int64(model.Created), 0).UTC().Format(time.RFC3339),
				DisplayName: model.Id,
				Type:        "model",
			}
		}
		firstID := ""
		lastID := ""
		if len(useranthropicModels) > 0 {
			firstID = useranthropicModels[0].ID
			lastID = useranthropicModels[len(useranthropicModels)-1].ID
		}
		c.JSON(200, gin.H{
			"data":     useranthropicModels,
			"first_id": firstID,
			"has_more": false,
			"last_id":  lastID,
		})
	case constant.ChannelTypeGemini:
		userGeminiModels := make([]dto.GeminiModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			userGeminiModels[i] = dto.GeminiModel{
				Name:        model.Id,
				DisplayName: model.Id,
			}
		}
		c.JSON(200, gin.H{
			"models":        userGeminiModels,
			"nextPageToken": nil,
		})
	default:
		c.JSON(200, gin.H{
			"success": true,
			"data":    userOpenAiModels,
			"object":  "list",
		})
	}
}

func ChannelListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    openAIModels,
	})
}

func DashboardListModels(c *gin.Context) {
	modelsByChannel := make(map[int][]string, len(channelId2Models))
	for channelType, models := range channelId2Models {
		modelsByChannel[channelType] = append([]string(nil), models...)
	}
	for channelType := 1; channelType <= constant.ChannelTypeDummy; channelType++ {
		if plugin, ok := jsplugin.DefaultRegistry.GetByChannelType(channelType); ok {
			modelsByChannel[channelType] = append([]string(nil), plugin.Meta.Models...)
		}
	}
	c.JSON(200, gin.H{
		"success": true,
		"data":    modelsByChannel,
	})
}

func EnabledListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    model.GetEnabledModels(),
	})
}

func RetrieveModel(c *gin.Context, modelType int) {
	modelID := c.Param("model")
	// Reuse the exact list policy, including group, token allowlist and billing
	// availability. Merely appearing in an adaptor's static list is not authority.
	viewerID := c.GetInt("id")
	groups, err := getModelListGroups(c)
	if err == nil && viewerID > 0 {
		channels, lookupErr := model.GetModelChannelsForOwner([]string{modelID}, groups.ownerGroups, viewerID)
		allowed := lookupErr == nil && len(channels[modelID]) > 0
		if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
			value, _ := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
			limits, _ := value.(map[string]bool)
			allowed = allowed && (limits[modelID] || limits[ratio_setting.RoutingMatchModelName(modelID)])
		}
		acceptUnset := operation_setting.SelfUseModeEnabled
		if !acceptUnset {
			settings, _ := model.GetUserSetting(viewerID, false)
			acceptUnset = settings.AcceptUnsetRatioModel
		}
		// Same policy as ListModels: own/shared channels entitle the model without a
		// billing entry; platform-catalog-only models still need one.
		tenantOwned := model.HasTenantOwnedChannel(channels[modelID], viewerID)
		if allowed && (acceptUnset || tenantOwned || helper.HasModelBillingConfig(modelID)) {
			overrides, metadataErr := model.GetModelEndpointOverrides([]string{modelID})
			if metadataErr != nil {
				common.ApiError(c, metadataErr)
				return
			}
			aiModel := buildVisibleOpenAIModel(modelID, channels[modelID], overrides[modelID])
			if modelType == constant.ChannelTypeAnthropic {
				c.JSON(http.StatusOK, dto.AnthropicModel{ID: aiModel.Id, CreatedAt: time.Unix(int64(aiModel.Created), 0).UTC().Format(time.RFC3339), DisplayName: aiModel.Id, Type: "model"})
			} else {
				c.JSON(http.StatusOK, aiModel)
			}
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"error": types.OpenAIError{Message: fmt.Sprintf("The model '%s' does not exist", modelID), Type: "invalid_request_error", Param: "model", Code: "model_not_found"}})
}

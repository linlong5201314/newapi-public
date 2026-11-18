package controller

import (
	"maps"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		if common.StringsContains(item.EnableGroup, "all") {
			filtered = append(filtered, item)
			continue
		}
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

func GetPricing(c *gin.Context) {
	pricing := model.GetPricing()
	userID := c.GetInt("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	maps.Copy(groupRatio, ratio_setting.GetGroupRatioCopy())
	var group string
	if userID > 0 {
		user, err := model.GetUserCache(userID)
		if err == nil {
			group = user.Group
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
		}
	}

	usableGroup = service.GetUserUsableGroups(group)
	pricing = filterPricingByUsableGroups(pricing, usableGroup)
	// Pricing is globally cached for accounting, not an authorization boundary.
	// Copy/filter its response; never mutate cached slices or expose another
	// tenant's model names, enabled groups, providers or endpoint metadata.
	groups := make([]string, 0, len(usableGroup))
	visibleGroups := make(map[string][]string)
	for name := range usableGroup {
		if name == "auto" {
			continue
		}
		groups = append(groups, name)
		for _, modelName := range model.GetGroupEnabledModelsForOwner(name, userID) {
			visibleGroups[modelName] = append(visibleGroups[modelName], name)
		}
	}
	modelNames := make([]string, 0, len(visibleGroups))
	for name := range visibleGroups {
		modelNames = append(modelNames, name)
	}
	channelsByModel, err := model.GetModelChannelsForOwner(modelNames, groups, userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	endpointOverrides, err := model.GetModelEndpointOverrides(modelNames)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	filtered := make([]model.Pricing, 0, len(pricing))
	endpoints := make(map[string]common.EndpointInfo)
	visibleVendors := make(map[int]bool)
	for _, item := range pricing {
		channels := channelsByModel[item.ModelName]
		if len(channels) == 0 {
			continue
		}
		metadata := buildVisibleOpenAIModel(item.ModelName, channels, endpointOverrides[item.ModelName])
		item.OwnerBy = metadata.OwnedBy
		item.SupportedEndpointTypes = metadata.SupportedEndpointTypes
		item.EnableGroup = visibleGroups[item.ModelName]
		sort.Strings(item.EnableGroup)
		for _, endpoint := range metadata.SupportedEndpointTypes {
			if info, ok := common.GetDefaultEndpointInfo(endpoint); ok {
				endpoints[string(endpoint)] = info
			}
		}
		maps.Copy(endpoints, endpointOverrides[item.ModelName])
		visibleVendors[item.VendorID] = true
		filtered = append(filtered, item)
	}
	pricing = filtered
	vendors := make([]model.PricingVendor, 0)
	for _, vendor := range model.GetVendors() {
		if visibleVendors[vendor.ID] {
			vendors = append(vendors, vendor)
		}
	}
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}

	c.JSON(200, gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            vendors,
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": endpoints,
		"auto_groups":        service.GetUserAutoGroup(group),
		"pricing_version":    "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccessT(c, "Model ratios reset", nil)
}

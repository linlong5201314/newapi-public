package model

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// group2model2channels 内存索引：group -> model -> ownerId -> enabled channelIds。
// ownerId=0 为平台公共渠道桶，>0 为用户自建渠道桶（多租户路由隔离）。
var group2model2channels map[string]map[string]map[int][]int // enabled channel
var channelsIDM map[int]*Channel                             // all channels include disabled
// channel2advancedCustomConfig caches parsed Advanced Custom (type 58) configs so
// path-aware selection avoids re-parsing JSON per request. Refreshed on full sync.
var channel2advancedCustomConfig map[int]*kitdto.AdvancedCustomConfig
var channelSyncLock sync.RWMutex

func InitChannelCache() {
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		rebuildTaskAliasView()
		return
	}
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*kitdto.AdvancedCustomConfig)
	var channels []*Channel
	DB.Find(&channels)
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	var abilities []*Ability
	DB.Find(&abilities)
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string]map[int][]int)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string]map[int][]int)
	}
	for _, ability := range abilities {
		channel := newChannelId2channel[ability.ChannelId]
		if !ability.Enabled || channel == nil || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		if newGroup2model2channels[ability.Group] == nil {
			newGroup2model2channels[ability.Group] = make(map[string]map[int][]int)
		}
		if newGroup2model2channels[ability.Group][ability.Model] == nil {
			newGroup2model2channels[ability.Group][ability.Model] = make(map[int][]int)
		}
		newGroup2model2channels[ability.Group][ability.Model][channel.OwnerId] = append(newGroup2model2channels[ability.Group][ability.Model][channel.OwnerId], channel.Id)
	}

	// sort by priority (within each owner bucket)
	for group, model2channels := range newGroup2model2channels {
		for model, owner2channels := range model2channels {
			for ownerId, channels := range owner2channels {
				sort.Slice(channels, func(i, j int) bool {
					return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
				})
				newGroup2model2channels[group][model][ownerId] = channels
			}
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				if oldChannel, ok := channelsIDM[i]; ok {
					// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
					if oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
						channel.ChannelInfo.MultiKeyPollingIndex = oldChannel.ChannelInfo.MultiKeyPollingIndex
					}
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	channelSyncLock.Unlock()
	// Lock ordering: InvalidatePricingCache acquires updatePricingLock, and
	// GetPricing (holding updatePricingLock) nests channelSyncLock.RLock via
	// loadPricingAdvancedCustomConfigs. channelSyncLock MUST be released before
	// invalidating the pricing cache, otherwise the reversed order deadlocks.
	InvalidatePricingCache()
	rebuildTaskAliasView()
	common.SysLog(common.LogText("channels synced from database"))
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog(common.LogText("syncing channels from database"))
		InitChannelCache()
	}
}

// selectOwnerBucketIDs applies tenant visibility while the caller holds channelSyncLock.
func selectOwnerBucketIDs(owner2channels map[int][]int, ownerUserId int, modelName string) []int {
	var ids []int
	for _, list := range owner2channels {
		for _, id := range list {
			if ChannelModelVisibleToOwner(channelsIDM[id], modelName, ownerUserId) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func GetRandomSatisfiedChannel(
	group string,
	model string,
	retry int,
	filters []dto.ChannelFilter,
	ownerUserId int,
) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannel(group, model, retry, filters, ownerUserId)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// First, try to find channels with the exact model name.
	channels, _ := filterCandidateIDs(selectOwnerBucketIDs(group2model2channels[group][model], ownerUserId, model), model, filters)

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.RoutingMatchModelName(model)
		channels, _ = filterCandidateIDs(selectOwnerBucketIDs(group2model2channels[group][normalizedModel], ownerUserId, model), model, filters)
	}

	if len(channels) == 0 {
		return nil, nil
	}

	if len(channels) == 1 {
		if channel, ok := channelsIDM[channels[0]]; ok {
			return channel, nil
		}
		return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channels[0])
	}

	uniquePriorities := make(map[int]bool)
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			uniquePriorities[int(channel.GetPriority())] = true
		} else {
			return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channelId)
		}
	}
	var sortedUniquePriorities []int
	for priority := range uniquePriorities {
		sortedUniquePriorities = append(sortedUniquePriorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedUniquePriorities)))

	if retry >= len(uniquePriorities) {
		retry = len(uniquePriorities) - 1
	}
	targetPriority := int64(sortedUniquePriorities[retry])

	// get the priority for the given retry number
	var targetChannels []*Channel
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			if channel.GetPriority() == targetPriority {
				targetChannels = append(targetChannels, channel)
			}
		} else {
			return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channelId)
		}
	}

	if len(targetChannels) == 0 {
		return nil, errors.New(fmt.Sprintf("no channel found, group: %s, model: %s, priority: %d", group, model, targetPriority))
	}

	// 健康度加权：把「近几次成功率」乘进权重，快而稳的渠道多分流量，
	// 连续失败的渠道自动降权（Factor=0 时本轮不选）。
	weights := make([]float64, len(targetChannels))
	var sumWeight float64
	for i, channel := range targetChannels {
		weights[i] = float64(channel.GetWeight()) * ChannelHealthFactor(channel.Id)
		sumWeight += weights[i]
	}
	if sumWeight <= 0 {
		// 原始权重全为 0（或全部被健康度清零）时退化为等权，保持旧行为并避免打空。
		for i := range weights {
			weights[i] = 1
		}
		sumWeight = float64(len(weights))
	}

	randomWeight := rand.Float64() * sumWeight
	for i, channel := range targetChannels {
		randomWeight -= weights[i]
		if randomWeight < 0 {
			return channel, nil
		}
	}
	return targetChannels[len(targetChannels)-1], nil
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("channel #%d no longer exists", id)
	}
	return c, nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("channel #%d no longer exists", id)
	}
	return &c.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel, ok := channelsIDM[id]; ok {
		channel.Status = status
	}
	if status != common.ChannelStatusEnabled {
		// delete the channel from group2model2channels (all owner buckets)
		for group, model2channels := range group2model2channels {
			for model, owner2channels := range model2channels {
				for ownerId, channels := range owner2channels {
					for i, channelId := range channels {
						if channelId == id {
							// remove the channel from the slice
							group2model2channels[group][model][ownerId] = append(channels[:i], channels[i+1:]...)
							break
						}
					}
				}
			}
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	if channel == nil {
		channelSyncLock.Unlock()
		return
	}

	if channelsIDM == nil {
		channelsIDM = make(map[int]*Channel)
	}
	if oldChannel, ok := channelsIDM[channel.Id]; ok {
		logger.LogDebug(nil, "CacheUpdateChannel before: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, oldChannel.ChannelInfo.MultiKeyPollingIndex)
	}
	channelsIDM[channel.Id] = channel
	if channel2advancedCustomConfig == nil {
		channel2advancedCustomConfig = make(map[int]*kitdto.AdvancedCustomConfig)
	}
	delete(channel2advancedCustomConfig, channel.Id)
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			channel2advancedCustomConfig[channel.Id] = config
		}
	}
	logger.LogDebug(nil, "CacheUpdateChannel after: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)
	// Lock ordering: do NOT hold channelSyncLock while calling
	// InvalidatePricingCache. GetPricing acquires updatePricingLock first and then
	// channelSyncLock.RLock (via loadPricingAdvancedCustomConfigs); acquiring
	// updatePricingLock while holding channelSyncLock would be an AB-BA deadlock.
	channelSyncLock.Unlock()
	InvalidatePricingCache()
}

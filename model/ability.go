package model

import (
	"errors"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Ability struct {
	Group     string  `json:"group" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
	Model     string  `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	ChannelId int     `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool    `json:"enabled"`
	Priority  *int64  `json:"priority" gorm:"bigint;default:0;index"`
	Weight    uint    `json:"weight" gorm:"default:0;index"`
	Tag       *string `json:"tag" gorm:"index"`
}

type AbilityWithChannel struct {
	Ability
	ChannelType int `json:"channel_type"`
}

func GetAllEnableAbilityWithChannels() ([]AbilityWithChannel, error) {
	var abilities []AbilityWithChannel
	err := DB.Table("abilities").
		Select("abilities.*, channels.type as channel_type").
		Joins("left join channels on abilities.channel_id = channels.id").
		Where("abilities.enabled = ?", true).
		Scan(&abilities).Error
	return abilities, err
}

func GetGroupEnabledModels(group string) []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where(commonGroupCol+" = ? and enabled = ?", group, true).Distinct("model").Pluck("model", &models)
	return models
}

func GetEnabledModels() []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where("enabled = ?", true).Distinct("model").Pluck("model", &models)
	return models
}

// GetGroupEnabledModelsForOwner applies the same eligibility as relay selection.
func GetGroupEnabledModelsForOwner(group string, ownerUserId int) []string {
	var rows []struct {
		ModelName string
		Channel
	}
	query := DB.Table("abilities").
		Select("abilities.model AS model_name, channels.id, channels.owner_id, channels.visibility, channels.status, channels.models, channels.other_info").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where("abilities."+commonGroupCol+" = ? AND abilities.enabled = ? AND channels.status = ?", group, true, common.ChannelStatusEnabled).
		Where("channels.owner_id = ? OR channels.owner_id = 0 OR channels.visibility = 1", ownerUserId).
		Order("abilities.model ASC")
	if err := query.Scan(&rows).Error; err != nil {
		common.SysError("list visible models: " + err.Error())
		return []string{}
	}
	models := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if !seen[row.ModelName] && ChannelModelVisibleToOwner(&row.Channel, row.ModelName, ownerUserId) && ChannelSelectionTier(&row.Channel, ownerUserId) >= 0 {
			seen[row.ModelName] = true
			models = append(models, row.ModelName)
		}
	}
	return models
}

func GetAllEnableAbilities() []Ability {
	var abilities []Ability
	DB.Find(&abilities, "enabled = ?", true)
	return abilities
}

func getPriority(group string, model string, retry int) (int, error) {

	var priorities []int
	err := DB.Model(&Ability{}).
		Select("DISTINCT(priority)").
		Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true).
		Order("priority DESC").              // 按优先级降序排序
		Pluck("priority", &priorities).Error // Pluck用于将查询的结果直接扫描到一个切片中

	if err != nil {
		// 处理错误
		return 0, err
	}

	if len(priorities) == 0 {
		// 如果没有查询到优先级，则返回错误
		return 0, errors.New("database consistency is broken")
	}

	// 确定要使用的优先级
	var priorityToUse int
	if retry >= len(priorities) {
		// 如果重试次数大于优先级数，则使用最小的优先级
		priorityToUse = priorities[len(priorities)-1]
	} else {
		priorityToUse = priorities[retry]
	}
	return priorityToUse, nil
}

func getChannelQuery(group string, model string, retry int) (*gorm.DB, error) {
	maxPrioritySubQuery := DB.Model(&Ability{}).Select("MAX(priority)").Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true)
	channelQuery := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and priority = (?)", group, model, true, maxPrioritySubQuery)
	if retry != 0 {
		priority, err := getPriority(group, model, retry)
		if err != nil {
			return nil, err
		} else {
			channelQuery = DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and priority = ?", group, model, true, priority)
		}
	}

	return channelQuery, nil
}

// GetChannel selects enabled owner, shared or platform channels in the requested group.
func GetChannel(group string, model string, retry int, filters []dto.ChannelFilter, ownerUserId int, excluded ...map[int]bool) (*Channel, error) {
	var exclude map[int]bool
	if len(excluded) > 0 {
		exclude = excluded[0]
	}
	return getChannelByOwners(group, model, retry, filters, []int{ownerUserId}, exclude)
}

func getChannelByOwners(
	group string,
	model string,
	retry int,
	filters []dto.ChannelFilter,
	ownerIds []int,
	exclude map[int]bool,
) (*Channel, error) {
	ownerChannelsSubQuery := DB.Model(&Channel{}).Select("id").
		Where("status = ?", common.ChannelStatusEnabled).
		Where("owner_id IN ? OR owner_id = 0 OR visibility = 1", ownerIds)
	if ownerIds[0] > 0 && !operation_setting.SelfChannelUsePublicFallback {
		ownerChannelsSubQuery = ownerChannelsSubQuery.Where("owner_id <> 0")
	}
	var abilities []Ability
	err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and channel_id in (?)", group, model, true, ownerChannelsSubQuery).Order("priority DESC, weight DESC").Find(&abilities).Error
	if err != nil {
		return nil, err
	}
	abilities = filterAbilitiesByConstraints(abilities, model, filters, ownerIds[0], exclude)
	// Like the memory cache, retry with the routing name (for example without
	// @ modifiers) when no channel serves the exact name.
	if normalized := ratio_setting.RoutingMatchModelName(model); len(abilities) == 0 && normalized != "" && normalized != model {
		err = DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and channel_id in (?)", group, normalized, true, ownerChannelsSubQuery).Order("priority DESC, weight DESC").Find(&abilities).Error
		if err != nil {
			return nil, err
		}
		abilities = filterAbilitiesByConstraints(abilities, model, filters, ownerIds[0], exclude)
	}
	if len(abilities) > 0 {
		priorities := make([]int64, 0)
		seen := make(map[int64]bool)
		for _, ability := range abilities {
			priority := int64(0)
			if ability.Priority != nil {
				priority = *ability.Priority
			}
			if !seen[priority] {
				seen[priority] = true
				priorities = append(priorities, priority)
			}
		}
		sort.Slice(priorities, func(i, j int) bool { return priorities[i] > priorities[j] })
		if retry >= len(priorities) {
			retry = len(priorities) - 1
		}
		targetPriority := priorities[retry]
		abilities = lo.Filter(abilities, func(ability Ability, _ int) bool {
			return ability.Priority == nil && targetPriority == 0 || ability.Priority != nil && *ability.Priority == targetPriority
		})
	}
	channel := Channel{}
	if len(abilities) > 0 {
		eligible := make([]int, 0, len(abilities))
		for _, ability := range abilities {
			if ChannelHealthFactor(ability.ChannelId) > 0 {
				eligible = append(eligible, ability.ChannelId)
			}
		}
		if len(abilities) > 1 && len(eligible) > 0 {
			abilities = lo.Filter(abilities, func(ability Ability, _ int) bool {
				return slices.Contains(eligible, ability.ChannelId)
			})
		}
		// 按「权重 × 健康度」加权随机；健康度把近几次成功率算进去，
		// 快而稳的渠道多分流量，连续失败的自然降权。
		weights := make([]float64, len(abilities))
		var weightSum float64
		for i, ability := range abilities {
			weights[i] = float64(ability.Weight+10) * ChannelHealthFactorNoReservation(ability.ChannelId)
			weightSum += weights[i]
		}
		if weightSum <= 0 {
			for i := range weights {
				weights[i] = 1
			}
			weightSum = float64(len(weights))
		}
		randomWeight := rand.Float64() * weightSum
		for i, ability := range abilities {
			randomWeight -= weights[i]
			if randomWeight < 0 {
				channel.Id = ability.ChannelId
				break
			}
		}
		if channel.Id == 0 {
			channel.Id = abilities[len(abilities)-1].ChannelId
		}
	} else {
		return nil, nil
	}
	err = DB.First(&channel, "id = ?", channel.Id).Error
	return &channel, err
}

// filterAbilitiesByConstraints applies the same ChannelSatisfiesFilters
// predicate used by the memory-cache path. A failed channel lookup fails
// closed when a task-plugin identity is required and fails open otherwise.
func filterAbilitiesByConstraints(abilities []Ability, modelName string, filters []dto.ChannelFilter, owners ...any) []Ability {
	ownerUserId := -1
	var exclude map[int]bool
	for _, owner := range owners {
		switch value := owner.(type) {
		case int:
			ownerUserId = value
		case map[int]bool:
			exclude = value
		}
	}
	if len(abilities) == 0 {
		return nil
	}

	channelIds := make([]int, 0, len(abilities))
	seen := make(map[int]struct{}, len(abilities))
	for _, ability := range abilities {
		if _, ok := seen[ability.ChannelId]; ok {
			continue
		}
		seen[ability.ChannelId] = struct{}{}
		channelIds = append(channelIds, ability.ChannelId)
	}

	var channels []*Channel
	if err := DB.Where("id IN ?", channelIds).Find(&channels).Error; err != nil {
		if ownerUserId >= 0 || identityFilterRequiresKey(filters) {
			return nil
		}
		return abilities
	}

	if ownerUserId >= 0 {
		tiers := make(map[int]bool)
		for _, channel := range channels {
			tier := ChannelSelectionTier(channel, ownerUserId)
			if tier >= 0 && ChannelModelVisibleToOwner(channel, modelName, ownerUserId) {
				tiers[tier] = true
			}
		}
		for _, tier := range []int{1, 0, 2} {
			if tiers[tier] {
				allowed := make(map[int]bool)
				for _, channel := range channels {
					if ChannelSelectionTier(channel, ownerUserId) == tier {
						allowed[channel.Id] = true
					}
				}
				abilities = lo.Filter(abilities, func(ability Ability, _ int) bool { return allowed[ability.ChannelId] && !exclude[ability.ChannelId] })
				break
			}
		}
	}

	channelsByID := make(map[int]*Channel, len(channels))
	for _, channel := range channels {
		channelsByID[channel.Id] = channel
	}

	filtered := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		channel := channelsByID[ability.ChannelId]
		if ownerUserId >= 0 && !ChannelModelVisibleToOwner(channel, modelName, ownerUserId) {
			continue
		}
		if ok, _ := ChannelSatisfiesFilters(channel, modelName, filters); ok {
			filtered = append(filtered, ability)
		}
	}
	return filtered
}

func identityFilterRequiresKey(filters []dto.ChannelFilter) bool {
	for _, filter := range filters {
		if filter.Kind == dto.FilterTaskPluginIdentity && filter.TaskPluginKey != "" {
			return true
		}
	}
	return false
}

func (channel *Channel) AddAbilities(tx *gorm.DB) error {
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}
	if len(abilities) == 0 {
		return nil
	}
	// choose DB or provided tx
	useDB := DB
	if tx != nil {
		useDB = tx
	}
	for _, chunk := range lo.Chunk(abilities, 50) {
		err := useDB.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) DeleteAbilities() error {
	return DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
func (channel *Channel) UpdateAbilities(tx *gorm.DB) error {
	isNewTx := false
	// 如果没有传入事务，创建新的事务
	if tx == nil {
		tx = DB.Begin()
		if tx.Error != nil {
			return tx.Error
		}
		isNewTx = true
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()
	}

	// First delete all abilities of this channel
	err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
	if err != nil {
		if isNewTx {
			tx.Rollback()
		}
		return err
	}

	// Then add new abilities
	models_ := channel.GetModels()
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}

	if len(abilities) > 0 {
		for _, chunk := range lo.Chunk(abilities, 50) {
			err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
			if err != nil {
				if isNewTx {
					tx.Rollback()
				}
				return err
			}
		}
	}

	// 如果是新创建的事务，需要提交
	if isNewTx {
		return tx.Commit().Error
	}

	return nil
}

func UpdateAbilityStatus(channelId int, status bool) error {
	return DB.Model(&Ability{}).Where("channel_id = ?", channelId).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityStatusByTag(tag string, status bool) error {
	return DB.Model(&Ability{}).Where("tag = ?", tag).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityByTag(tag string, newTag *string, priority *int64, weight *uint) error {
	ability := Ability{}
	if newTag != nil {
		ability.Tag = newTag
	}
	if priority != nil {
		ability.Priority = priority
	}
	if weight != nil {
		ability.Weight = *weight
	}
	return DB.Model(&Ability{}).Where("tag = ?", tag).Updates(ability).Error
}

var fixLock = sync.Mutex{}

func FixAbility() (int, int, error) {
	lock := fixLock.TryLock()
	if !lock {
		return 0, 0, common.NewMessage("A repair task is already running. Please try again later")
	}
	defer fixLock.Unlock()

	// truncate abilities table
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		err := DB.Exec("DELETE FROM abilities").Error
		if err != nil {
			common.SysLog(common.LogText("Delete abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	} else {
		err := DB.Exec("TRUNCATE TABLE abilities").Error
		if err != nil {
			common.SysLog(common.LogText("Truncate abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	}
	var channels []*Channel
	// Find all channels
	err := DB.Model(&Channel{}).Find(&channels).Error
	if err != nil {
		return 0, 0, err
	}
	if len(channels) == 0 {
		return 0, 0, nil
	}
	successCount := 0
	failCount := 0
	for _, chunk := range lo.Chunk(channels, 50) {
		ids := lo.Map(chunk, func(c *Channel, _ int) int { return c.Id })
		// Delete all abilities of this channel
		err = DB.Where("channel_id IN ?", ids).Delete(&Ability{}).Error
		if err != nil {
			common.SysLog(common.LogText("Delete abilities failed: %s", err.Error()))
			failCount += len(chunk)
			continue
		}
		// Then add new abilities
		for _, channel := range chunk {
			err = channel.AddAbilities(nil)
			if err != nil {
				common.SysLog(common.LogText("Add abilities for channel %d failed: %s", channel.Id, err.Error()))
				failCount++
			} else {
				successCount++
			}
		}
	}
	InitChannelCache()
	return successCount, failCount, nil
}

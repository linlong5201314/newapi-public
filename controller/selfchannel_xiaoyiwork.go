package controller

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

var (
	errXiaoyiWorkModels = errors.New("Select only models available to your Xiaoyi Work account")
	errXiaoyiWorkShare  = errors.New("Confirm Xiaoyi Work sharing and select the models to share")
)

type xiaoyiWorkCatalog struct {
	available   []string
	previews    []string
	modelLabels map[string]string
}

func (catalog xiaoyiWorkCatalog) labels() map[string]string { return catalog.modelLabels }

func isXiaoyiWorkChannel(channel *model.Channel) bool {
	template, _ := channel.GetOtherInfo()["templateId"].(string)
	return template == constant.XiaoyiWorkTemplateID
}

func saveXiaoyiWorkModelSelection(c *gin.Context, id, visibility int, models, shared []string, confirmed bool) {
	channel, ok := loadSelfOwnedChannel(c, id, true)
	if !ok {
		return
	}
	if !isXiaoyiWorkChannel(channel) {
		common.ApiErrorMsg(c, model.ErrSelfChannelChanged.Error())
		return
	}
	info := channel.GetOtherInfo()
	selected := channel.GetModels()
	if models != nil {
		selected = mergeModelCatalogs(models)
	}
	published := mergeModelCatalogs(shared)
	if visibility == 1 && (len(published) == 0 || !confirmed || !modelSubset(published, selected)) {
		common.ApiErrorMsg(c, errXiaoyiWorkShare.Error())
		return
	}
	if visibility == 0 {
		published = []string{}
	}
	originalKey := channel.Key
	if visibility == 1 || (models != nil && !modelSubset(selected, channel.GetModels())) {
		if _, ok := accountAuthorizationIdentity(c); !ok {
			common.ApiErrorMsg(c, "A dashboard login session is required for account authorization")
			return
		}
		session, err := xiaoyiWorkSessionForChannel(channel)
		if err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		catalog, err := fetchXiaoyiWorkCatalog(c.Request.Context(), "", session)
		if err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		if !modelSubset(selected, mergeModelCatalogs(append(append([]string{}, catalog.available...), catalog.previews...))) {
			common.ApiErrorMsg(c, errXiaoyiWorkModels.Error())
			return
		}
		if err := saveXiaoyiWorkSession(channel, session); err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		info["allModels"] = mergeModelCatalogs(append(append([]string{}, catalog.available...), catalog.previews...))
		info["unavailableModels"] = []string{}
		info["previewOnlyModels"] = catalog.previews
		info["modelLabels"] = catalog.labels()
	}
	info["publicModels"] = published
	metadata, err := common.Marshal(info)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	updates := map[string]any{"visibility": visibility, "tier": "api", "other_info": string(metadata)}
	originalForSave := *channel
	originalForSave.Key = originalKey
	if models != nil {
		updates["models"] = strings.Join(selected, ",")
	}
	if channel.Key != originalKey {
		updates["key"] = channel.Key
	}
	if err := model.SaveVerifiedSelfChannel(&originalForSave, updates); err != nil {
		common.ApiErrorMsg(c, model.ErrSelfChannelChanged.Error())
		return
	}
	model.InitChannelCache()
	common.ApiSuccess(c, gin.H{"channelId": id, "visibility": visibility, "tier": "api"})
}

func saveXiaoyiWorkSession(channel *model.Channel, session *service.XiaoyiWorkOAuthSession) error {
	raw, err := common.Marshal(session)
	if err != nil {
		return err
	}
	stored, err := common.EncryptSecret(string(raw))
	if err != nil || !common.IsEncryptedSecret(stored) {
		return errors.New("Unable to securely store the Xiaoyi Work credential")
	}
	channel.Key = stored
	return nil
}

func prepareXiaoyiWorkUpdate(original *model.Channel, fields map[string]any) error {
	for key, value := range fields {
		switch strings.ToLower(key) {
		case "id", "name", "type":
			if strings.ToLower(key) == "type" && value != float64(constant.ChannelTypeXiaoyiWork) {
				return errors.New("Use the dedicated Xiaoyi Work account connection flow")
			}
		case "visibility", "other_info", "header_override", "param_override", "channel_info", "setting", "settings", "tier", "models":
			delete(fields, key)
		case "model_mapping", "key":
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" && text != original.Key {
				return errors.New("Reconnect Xiaoyi Work to change its account. Model mapping and external gateways are not supported")
			}
		case "base_url":
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" && strings.TrimSpace(text) != original.GetBaseURL() {
				return errors.New("Reconnect Xiaoyi Work to change its account. External gateways are not supported")
			}
			delete(fields, key)
		}
	}
	return nil
}

func updateXiaoyiWorkChannel(c *gin.Context, original *model.Channel, id int, fields map[string]any) error {
	if err := prepareXiaoyiWorkUpdate(original, fields); err != nil {
		return err
	}
	if err := model.SaveVerifiedSelfChannel(original, fields); err != nil {
		return err
	}
	updated, err := model.GetChannelById(id, true)
	if err != nil {
		return err
	}
	*original = *updated
	return nil
}

func modelSubset(selected, catalog []string) bool {
	allowed := make(map[string]bool, len(catalog))
	for _, id := range catalog {
		allowed[id] = true
	}
	for _, id := range selected {
		if !allowed[id] {
			return false
		}
	}
	return true
}

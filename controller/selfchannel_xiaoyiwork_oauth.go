package controller

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Huawei sign-in plus SMS verification routinely takes longer than five
// minutes, so a short window made the callback look expired even though the
// user was still on the official page.
const xiaoyiWorkLoginTTL = 15 * time.Minute

const xiaoyiWorkExpiredMessage = "Xiaoyi Work authorization expired (valid for 15 minutes). Click Authorize Huawei account again and finish sign-in within 15 minutes"

// xiaoyiWorkSessionProblem explains why a pending session could not be resumed.
// Reporting every case as "expired" sent users to redo a sign-in that had
// already failed or been consumed, which is the wrong fix for those cases.
func xiaoyiWorkSessionProblem(state string) string {
	prior, err := model.GetSelfOAuthSession(xiaoyiWorkStateHash(state), 0)
	if err != nil || prior == nil || prior.Provider != constant.XiaoyiWorkTemplateID {
		return xiaoyiWorkExpiredMessage
	}
	switch prior.Status {
	case "failed":
		return "The previous Xiaoyi Work authorization attempt did not finish. Click Authorize Huawei account again"
	case "done":
		return "This Xiaoyi Work authorization was already used. Click Authorize Huawei account again"
	case "exchanging":
		return "Xiaoyi Work authorization is already being completed"
	case "cancelled":
		return "Xiaoyi Work authorization was cancelled. Click Authorize Huawei account again"
	}
	return xiaoyiWorkExpiredMessage
}

type xiaoyiWorkLoginState struct {
	Identity       model.AuthSessionIdentity `json:"identity"`
	Verifier       string                    `json:"verifier"`
	DisplayName    string                    `json:"display_name"`
	PublicModels   []string                  `json:"public_models"`
	ShareConfirmed bool                      `json:"share_confirmed"`
}

func xiaoyiWorkStateHash(state string) string { return ds2SessionKey(state) }

func SelfXiaoyiWorkOAuthStart(c *gin.Context) {
	identity, ok := accountAuthorizationIdentity(c)
	if !ok {
		return
	}
	var request struct {
		DisplayName string `json:"displayName"`
	}
	_ = c.ShouldBindJSON(&request)
	verifier, err := service.XiaoyiWorkRandom(32)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	state, err := service.XiaoyiWorkRandom(24)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	authURL, err := service.XiaoyiWorkAuthorizeURL(state, verifier)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	payload, err := common.Marshal(xiaoyiWorkLoginState{Identity: identity, Verifier: verifier, DisplayName: strings.TrimSpace(request.DisplayName)})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	encrypted, err := common.EncryptSecret(string(payload))
	if err != nil || !common.IsEncryptedSecret(encrypted) {
		common.ApiErrorMsg(c, "Unable to securely store Xiaoyi Work authorization")
		return
	}
	if err := model.CreateSelfOAuthSession(&model.SelfOAuthSession{State: xiaoyiWorkStateHash(state), Provider: constant.XiaoyiWorkTemplateID, Status: "pending", Phone: strconv.Itoa(identity.UserID), Refresh: encrypted}); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"sessionId": state, "authUrl": authURL, "callbackPort": service.XiaoyiWorkCallbackPort, "callbackPath": service.XiaoyiWorkCallbackPath, "expiresIn": int(xiaoyiWorkLoginTTL.Seconds()), "interval": 2, "requiresLocalCallback": true, "upstream_oauth": true})
}

func xiaoyiWorkLoginSession(state string) (*model.SelfOAuthSession, *xiaoyiWorkLoginState, error) {
	return xiaoyiWorkLoginSessionWithTerminal(state, false)
}

func xiaoyiWorkLoginSessionWithTerminal(state string, includeTerminal bool) (*model.SelfOAuthSession, *xiaoyiWorkLoginState, error) {
	if len(state) < 20 || len(state) > 128 {
		return nil, nil, model.ErrAuthFlowInvalid
	}
	flow, err := model.GetSelfOAuthSession(xiaoyiWorkStateHash(state), xiaoyiWorkLoginTTL)
	if err != nil || flow == nil || flow.Provider != constant.XiaoyiWorkTemplateID || (!includeTerminal && flow.Status != "pending") {
		return nil, nil, model.ErrAuthFlowInvalid
	}
	if includeTerminal && (flow.Status == "done" || flow.Status == "failed") {
		return flow, nil, nil
	}
	if !common.IsEncryptedSecret(flow.Refresh) {
		return nil, nil, model.ErrAuthFlowInvalid
	}
	var options xiaoyiWorkLoginState
	if common.UnmarshalJsonStr(common.DecryptSecretOrLegacy(flow.Refresh), &options) != nil || options.Identity.UserID <= 0 || options.Verifier == "" {
		return nil, nil, model.ErrAuthFlowInvalid
	}
	if err := model.ValidateAuthSessionWithTx(model.DB, options.Identity); err != nil {
		return nil, nil, err
	}
	return flow, &options, nil
}

func SelfXiaoyiWorkOAuthComplete(c *gin.Context) {
	var request struct {
		SessionID      string   `json:"sessionId"`
		Code           string   `json:"code"`
		CallbackURL    string   `json:"callbackUrl"`
		Models         []string `json:"models"`
		PublicModels   []string `json:"public_models"`
		ShareConfirmed bool     `json:"share_confirmed"`
		DisplayName    string   `json:"displayName"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if _, ok := accountAuthorizationIdentity(c); !ok {
		return
	}
	state := strings.TrimSpace(request.SessionID)
	code := strings.TrimSpace(request.Code)
	if code == "" && request.CallbackURL != "" {
		callback, err := url.Parse(request.CallbackURL)
		if err != nil || callback.Scheme != "http" || callback.Hostname() != "127.0.0.1" || callback.Port() != strconv.Itoa(service.XiaoyiWorkCallbackPort) || callback.Path != service.XiaoyiWorkCallbackPath || callback.Query().Get("state") != state || callback.User != nil || callback.Fragment != "" {
			common.ApiErrorMsg(c, "Paste the complete Xiaoyi Work callback URL from this browser")
			return
		}
		code = strings.TrimSpace(callback.Query().Get("code"))
	}
	if code == "" || len(code) > 4096 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	flow, options, err := xiaoyiWorkLoginSession(state)
	if err != nil || flow == nil {
		common.ApiErrorMsg(c, xiaoyiWorkSessionProblem(state))
		return
	}
	if options.Identity.UserID != c.GetInt("id") {
		common.ApiErrorMsg(c, "Xiaoyi Work authorization belongs to another dashboard session")
		return
	}
	claimed := model.DB.Model(&model.SelfOAuthSession{}).Where("state = ? AND provider = ? AND status = ? AND created_at > ?", flow.State, constant.XiaoyiWorkTemplateID, "pending", time.Now().Add(-xiaoyiWorkLoginTTL)).Update("status", "exchanging")
	if claimed.Error != nil || claimed.RowsAffected != 1 {
		common.ApiErrorMsg(c, "Xiaoyi Work authorization was already used")
		return
	}
	fail := func(err error) {
		_ = model.UpdateSelfOAuthSession(flow.State, map[string]any{"status": "failed", "refresh": "", "err": "Xiaoyi Work authorization could not be completed"})
		common.ApiErrorMsg(c, err.Error())
	}
	session, err := service.XiaoyiWorkExchange(c.Request.Context(), "", code, options.Verifier)
	if err != nil {
		fail(fmt.Errorf("Xiaoyi Work sign-in or device credential issuance failed: %w", err))
		return
	}
	catalog, err := fetchXiaoyiWorkCatalog(c.Request.Context(), "", session)
	if err != nil {
		fail(fmt.Errorf("Xiaoyi Work model synchronization failed: %w", err))
		return
	}
	if len(catalog.available) == 0 {
		fail(errors.New("No Xiaoyi Work models are available to this account"))
		return
	}
	options.PublicModels = mergeModelCatalogs(request.PublicModels)
	selected := mergeModelCatalogs(append(append([]string{}, catalog.available...), catalog.previews...))
	if request.Models != nil {
		selected = mergeModelCatalogs(request.Models)
	}
	if len(selected) == 0 || !modelSubset(selected, mergeModelCatalogs(append(append([]string{}, catalog.available...), catalog.previews...))) {
		fail(errXiaoyiWorkModels)
		return
	}
	if len(options.PublicModels) > 0 && (!request.ShareConfirmed || !modelSubset(options.PublicModels, selected)) {
		fail(errXiaoyiWorkShare)
		return
	}
	stored, err := common.Marshal(session)
	if err != nil {
		fail(err)
		return
	}
	encrypted, err := common.EncryptSecret(string(stored))
	if err != nil || !common.IsEncryptedSecret(encrypted) {
		fail(errors.New("Unable to securely store the Xiaoyi Work credential"))
		return
	}
	name := options.DisplayName
	if request.DisplayName != "" {
		name = strings.TrimSpace(request.DisplayName)
	}
	if name == "" {
		name = "华为小艺 Work"
	}
	channel := &model.Channel{Type: constant.ChannelTypeXiaoyiWork, Name: name, Key: encrypted, OwnerId: options.Identity.UserID, Status: common.ChannelStatusEnabled, Group: "default", Models: strings.Join(selected, ","), Tier: "api", CreatedTime: common.GetTimestamp()}
	allModels := mergeModelCatalogs(append(append([]string{}, catalog.available...), catalog.previews...))
	channel.SetOtherInfo(map[string]any{"templateId": constant.XiaoyiWorkTemplateID, "poolKey": constant.XiaoyiWorkTemplateID, "allModels": allModels, "unavailableModels": []string{}, "previewOnlyModels": catalog.previews, "publicModels": options.PublicModels, "modelLabels": catalog.labels(), "accountUid": session.UID})
	channel.SetSetting(dto.ChannelSettings{Proxy: "direct", ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "direct"}})
	base := service.XiaoyiWorkSSEEndpoint
	channel.BaseURL = &base
	if len(options.PublicModels) > 0 {
		channel.Visibility = 1
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.ValidateAuthSessionWithTx(tx, options.Identity); err != nil {
			return err
		}
		if err := tx.Create(channel).Error; err != nil {
			return err
		}
		if err := channel.AddAbilities(tx); err != nil {
			return err
		}
		var persisted model.Channel
		if err := tx.Where("id = ? AND owner_id = ? AND type = ?", channel.Id, options.Identity.UserID, constant.ChannelTypeXiaoyiWork).First(&persisted).Error; err != nil {
			return fmt.Errorf("Xiaoyi Work channel was not persisted: %w", err)
		}
		var abilityCount int64
		if err := tx.Model(&model.Ability{}).Where("channel_id = ? AND enabled = ?", channel.Id, true).Count(&abilityCount).Error; err != nil {
			return err
		}
		if abilityCount != int64(len(selected)) {
			return fmt.Errorf("Xiaoyi Work channel abilities were not persisted")
		}
		result := tx.Model(&model.SelfOAuthSession{}).Where("state = ? AND status = ? AND provider = ?", flow.State, "exchanging", constant.XiaoyiWorkTemplateID).Updates(map[string]any{"status": "done", "account_id": strconv.Itoa(channel.Id), "refresh": "", "err": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("Xiaoyi Work authorization completion conflict")
		}
		return nil
	}); err != nil {
		fail(errors.New("Unable to create the Xiaoyi Work channel. Start again"))
		return
	}
	model.InitChannelCache()
	common.ApiSuccess(c, gin.H{"channelId": channel.Id, "models": selected, "unavailable_models": catalog.previews, "public_models": options.PublicModels, "upstream_oauth": true, "native": true})
}

func SelfXiaoyiWorkOAuthPoll(c *gin.Context) {
	if _, ok := accountAuthorizationIdentity(c); !ok {
		return
	}
	state := strings.TrimSpace(c.Query("sessionId"))
	flow, _, err := xiaoyiWorkLoginSessionWithTerminal(state, true)
	if err != nil || flow == nil || flow.Phone != strconv.Itoa(c.GetInt("id")) {
		common.ApiErrorMsg(c, xiaoyiWorkSessionProblem(state))
		return
	}
	channelID, _ := strconv.Atoi(flow.AccountID)
	if flow.Status == "done" {
		var count int64
		if err := model.DB.Model(&model.Channel{}).Where("id = ? AND owner_id = ? AND type = ?", channelID, c.GetInt("id"), constant.ChannelTypeXiaoyiWork).Count(&count).Error; err != nil || count != 1 {
			_ = model.UpdateSelfOAuthSession(flow.State, map[string]any{"status": "failed", "account_id": "", "err": "Xiaoyi Work channel was not created. Start authorization again"})
			common.ApiErrorMsg(c, "Xiaoyi Work channel was not created. Start authorization again")
			return
		}
	}
	common.ApiSuccess(c, gin.H{"done": flow.Status == "done", "status": flow.Status, "channelId": channelID, "message": flow.Err, "requiresLocalCallback": true})
}

func SelfXiaoyiWorkOAuthCancel(c *gin.Context) {
	if _, ok := accountAuthorizationIdentity(c); !ok {
		return
	}
	var request struct {
		SessionID string `json:"sessionId"`
	}
	if c.ShouldBindJSON(&request) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	flow, options, err := xiaoyiWorkLoginSession(request.SessionID)
	if err != nil || flow == nil || options.Identity.UserID != c.GetInt("id") {
		common.ApiErrorMsg(c, xiaoyiWorkSessionProblem(request.SessionID))
		return
	}
	result := model.DB.Model(&model.SelfOAuthSession{}).Where("state = ? AND provider = ? AND status = ?", flow.State, constant.XiaoyiWorkTemplateID, "pending").Updates(map[string]any{"status": "cancelled", "refresh": ""})
	if result.Error != nil || result.RowsAffected != 1 {
		common.ApiErrorMsg(c, "Xiaoyi Work authorization is already being completed")
		return
	}
	common.ApiSuccess(c, gin.H{"cancelled": true})
}

func xiaoyiWorkSessionForChannel(channel *model.Channel) (*service.XiaoyiWorkOAuthSession, error) {
	if !isXiaoyiWorkChannel(channel) {
		return nil, errors.New("Not a Xiaoyi Work channel")
	}
	raw := model.DecryptChannelKey(channel.Type, channel.Key)
	if raw == "" {
		return nil, errors.New("Xiaoyi Work authorization expired. Reconnect the account")
	}
	var session service.XiaoyiWorkOAuthSession
	if common.UnmarshalJsonStr(raw, &session) != nil || session.Credential == "" || session.UID == "" || session.DeviceID == "" {
		return nil, errors.New("Xiaoyi Work authorization is invalid. Reconnect the account")
	}
	return &session, nil
}

func fetchXiaoyiWorkCatalog(ctx context.Context, proxy string, session *service.XiaoyiWorkOAuthSession) (xiaoyiWorkCatalog, error) {
	if err := session.Fresh(ctx, proxy); err != nil {
		return xiaoyiWorkCatalog{}, errors.New("Xiaoyi Work credential refresh failed. Reconnect the account")
	}
	entries, err := service.FetchXiaoyiWorkCatalog(ctx, proxy, session)
	if err != nil {
		return xiaoyiWorkCatalog{}, errors.New("Unable to load the Xiaoyi Work model catalog. Retry or reconnect the account")
	}
	catalog := xiaoyiWorkCatalog{available: []string{}, previews: []string{}, modelLabels: map[string]string{}}
	for _, entry := range entries {
		if entry.Gray {
			catalog.previews = append(catalog.previews, entry.ID)
			continue
		}
		catalog.available = append(catalog.available, entry.ID)
		catalog.modelLabels[entry.ID] = entry.Name
	}
	return catalog, nil
}

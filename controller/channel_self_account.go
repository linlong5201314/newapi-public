// channel_self_account.go M3 账号型渠道（workbuddy/trae）的登录/导入端点，
// 挂 /api/channel/self 组（UserAuth + owner 强制）。
//
//   - POST /api/channel/self/oauth/start   {type:"workbuddy", realm?} → 设备流 {sessionId, authUrl, expiresIn}
//   - GET  /api/channel/self/oauth/poll    ?sessionId= → 完成返回已建渠道（或凭据预览 mode=preview）
//   - POST /api/channel/self/import_trae   {callbackUrl | refreshToken, uid, machineId?, deviceId?}
//     → ExchangeToken + GetUserInfo → 凭据预览（脱敏）供确认建渠道
package controller

import (
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/cline"
	"github.com/QuantumNous/new-api/relay/channel/raccoon"
	"github.com/QuantumNous/new-api/relay/channel/trae"
	"github.com/QuantumNous/new-api/relay/channel/workbuddy"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// SelfOAuthStart 发起账号渠道 OAuth 登录（目前支持 workbuddy 设备授权流）。
// body: {"type":"workbuddy", "realm":"cn|global"}；返回 {sessionId, authUrl, expiresIn}。
func SelfOAuthStart(c *gin.Context) { selfWorkBuddyOAuthStart(c) }

// SelfOAuthPoll 轮询 OAuth 登录态。
// mode=preview（默认）：成功后返回脱敏凭据预览，由前端确认后走常规建渠道流程；
// mode=create：成功后直接建渠道（name/group/models 可用 query 传入）。
func SelfOAuthPoll(c *gin.Context) { selfWorkBuddyOAuthPoll(c) }

// SelfImportTrae 导入 Trae 凭据：callbackUrl 或 refreshToken(+uid) 二选一。
// 返回脱敏凭据预览 + 可一键建渠道的 key（前端确认后走 POST /api/channel/self 建渠道，
// 或 mode=create 直接建）。
func SelfImportTrae(c *gin.Context) { selfTraeCredentialImport(c) }

// The official desktop protocol only accepts http://127.0.0.1:<port>/auth/callback
// with a 64-hex state. An HTTPS gateway callback is rejected before login. Do not
// synthesize OAuth or accept legacy bearer/code callbacks without a real client.
func SelfRaccoonOAuthStart(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "code": "RACCOON_AUTH_UNAVAILABLE", "message": common.NewMessage("Raccoon browser authorization only supports an official local client. Gateway callbacks are not supported"), "data": gin.H{"available": false, "flow": "unavailable", "reason": "loopback_only_protocol", "officialUrl": "https://xiaohuanxiong.com"}})
}

func SelfRaccoonOAuthCallback(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	c.String(http.StatusGone, "Raccoon gateway authorization is unavailable. Use the official local client; no account has been linked.")
}

func SelfRaccoonOAuthPoll(c *gin.Context) { SelfRaccoonOAuthStart(c) }

// maskTail 把手机号/token 脱敏成 前3****后4 形式。
func maskTail(s string) string {
	if len(s) <= 7 {
		if len(s) <= 2 {
			return "**"
		}
		return s[:1] + "***"
	}
	return s[:3] + "****" + s[len(s)-4:]
}

// createAccountChannelWithModels 同 createAccountChannel，但模型列表由调用方给出。
func createAccountChannelWithModels(c *gin.Context, channelType int, name, keyJSON string, models []string) (*model.Channel, error) {
	userId := c.GetInt("id")
	if !checkSelfChannelLimit(c, 1) {
		return nil, common.NewMessage("self channel count limit reached")
	}
	if name == "" {
		name = constant.GetChannelTypeName(channelType) + " · " + time.Now().Format("0102")
	}
	weight := uint(0)
	autoBan := 1
	channel := &model.Channel{
		Type:        channelType,
		Name:        name,
		Key:         keyJSON,
		Group:       "default",
		Models:      strings.Join(models, ","),
		OwnerId:     userId,
		Status:      common.ChannelStatusEnabled,
		Weight:      &weight,
		AutoBan:     &autoBan,
		CreatedTime: common.GetTimestamp(),
	}
	channel.SetOtherInfo(map[string]any{
		"allModels": models,
	})
	if err := channel.Insert(); err != nil {
		return nil, err
	}
	if err := channel.AddAbilities(nil); err != nil {
		common.SysLog("account channel add abilities failed: " + err.Error())
	}
	model.InitChannelCache()
	channel.Key = ""
	return channel, nil
}

// createAccountChannel 用导入的凭据直接建渠道（owner=当前用户，复用自服务限额）。
func createAccountChannel(c *gin.Context, channelType int, nickname, keyJSON string) (*model.Channel, error) {
	userId := c.GetInt("id")
	if !checkSelfChannelLimit(c, 1) {
		return nil, common.NewMessage("self channel count limit reached")
	}
	name := constant.GetChannelTypeName(channelType)
	if nickname != "" {
		name = name + " · " + nickname
	} else {
		name = name + " · " + time.Now().Format("0102")
	}
	var models []string
	switch channelType {
	case constant.ChannelTypeWorkBuddy:
		models = (&workbuddy.Adaptor{}).GetModelList()
	case constant.ChannelTypeTrae:
		models = (&trae.Adaptor{}).GetModelList()
	case constant.ChannelTypeCline:
		models = (&cline.Adaptor{}).GetModelList()
	}
	weight := uint(0)
	autoBan := 1
	channel := &model.Channel{
		Type:        channelType,
		Name:        name,
		Key:         keyJSON,
		Group:       "default",
		Models:      strings.Join(models, ","),
		OwnerId:     userId,
		Status:      common.ChannelStatusEnabled,
		Weight:      &weight,
		AutoBan:     &autoBan,
		CreatedTime: common.GetTimestamp(),
	}
	channel.SetOtherInfo(map[string]any{
		"allModels": models,
	})
	if err := channel.Insert(); err != nil {
		return nil, err
	}
	if err := channel.AddAbilities(nil); err != nil {
		common.SysLog("account channel add abilities failed: " + err.Error())
	}
	// 凭据已加密落库；响应对象不回传 key（避免密文/明文进入前端）。
	channel.Key = ""
	return channel, nil
}

// DefaultModelsForChannel 返回某渠道根据类型和模板推导出的完整内置候选模型列表。
func DefaultModelsForChannel(channel *model.Channel) []string {
	if channel == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(list []string) {
		for _, item := range list {
			v := strings.TrimSpace(item)
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}

	info := channel.GetOtherInfo()
	if rawAll, ok := info["allModels"].([]any); ok {
		for _, item := range rawAll {
			if s, sok := item.(string); sok {
				add([]string{s})
			}
		}
	}
	tplID, _ := info["templateId"].(string)
	if liveFreeCatalogProvider(channel.GetBaseURL(), tplID) != "" {
		return out
	}
	if tplId, _ := info["templateId"].(string); tplId != "" {
		if tpl := constant.GetFreeSourceTemplate(tplId); tpl != nil {
			add(tpl.Models)
		}
	}

	switch channel.Type {
	case constant.ChannelTypeWorkBuddy:
		add((&workbuddy.Adaptor{}).GetModelList())
	case constant.ChannelTypeTrae:
		add((&trae.Adaptor{}).GetModelList())
		if tpl := constant.GetFreeSourceTemplate("trae-cn"); tpl != nil {
			add(tpl.Models)
		}
	case constant.ChannelTypeCline:
		add((&cline.Adaptor{}).GetModelList())
		if tpl := constant.GetFreeSourceTemplate("cline-account"); tpl != nil {
			add(tpl.Models)
		}
	case constant.ChannelTypeRaccoon:
		add(raccoon.ModelList)
	}

	// 根据渠道名称兜底匹配模板模型（防止早先创建的渠道没有写 templateId）
	lowerName := strings.ToLower(channel.Name)
	for _, tpl := range constant.GetFreeSourceTemplates() {
		if strings.Contains(lowerName, strings.ToLower(tpl.Id)) ||
			(tpl.Name != "" && strings.Contains(lowerName, strings.ToLower(tpl.Name))) {
			add(tpl.Models)
		}
	}
	add(strings.Split(channel.Models, ","))
	return out
}

// SelfFetchAccountModels 拉取账号型渠道的实时模型目录（fetch_models 扩展：
// workbuddy/trae/cline/raccoon 走各自上游模型端点，若上游不可达则回退合并内置完整模型表，绝不报错丢失）。
func SelfFetchAccountModels(c *gin.Context, channel *model.Channel) ([]string, bool) {
	if channel.Type == constant.ChannelTypeZCode {
		// Never merge a global/default catalog into account-specific entitlements.
		if channel.OwnerId != c.GetInt("id") {
			writeSelfChannelNotFound(c)
			return nil, true
		}
		var credential service.ZCodeCredential
		secret := model.DecryptChannelKey(channel.Type, channel.Key)
		if common.Unmarshal([]byte(secret), &credential) != nil {
			zcodeAuthorizationError(c, service.ErrAccountProtocol)
			return nil, true
		}
		proxy, err := credential.Egress.Resolve(channel.OwnerId, channel.Id, channel.GetSetting())
		if err != nil {
			zcodeAuthorizationError(c, err)
			return nil, true
		}
		models, err := service.ValidateZCodeStartPlan(c.Request.Context(), proxy, &credential)
		if err != nil {
			zcodeAuthorizationError(c, err)
			return nil, true
		}
		info := channel.GetOtherInfo()
		if info == nil {
			info = map[string]any{}
		}
		info["allModels"] = models
		channel.SetOtherInfo(info)
		result := model.DB.Model(&model.Channel{}).Where(map[string]any{"id": channel.Id, "owner_id": channel.OwnerId, "key": channel.Key}).Update("other_info", channel.OtherInfo)
		if result.Error != nil {
			common.ApiError(c, result.Error)
			return nil, true
		}
		if result.RowsAffected != 1 {
			// MySQL reports zero changed rows for an unchanged catalog. A
			// second owner/key match distinguishes this from a stale secret.
			var matching int64
			check := model.DB.Model(&model.Channel{}).Where(map[string]any{"id": channel.Id, "owner_id": channel.OwnerId, "key": channel.Key}).Count(&matching)
			if check.Error != nil {
				common.ApiError(c, check.Error)
				return nil, true
			}
			if matching != 1 {
				zcodeAuthorizationError(c, model.ErrAuthFlowConsumed)
				return nil, true
			}
		}
		common.ApiSuccess(c, models)
		return models, true
	}

	key := model.DecryptChannelKey(channel.Type, channel.Key)
	if channel.ChannelInfo.IsMultiKey {
		keys := channel.GetKeys()
		if len(keys) > 0 {
			key = keys[0]
		}
	}
	var (
		models []string
		err    error
	)
	switch channel.Type {
	case constant.ChannelTypeWorkBuddy:
		models, err = workbuddy.FetchUpstreamModels(key)
	case constant.ChannelTypeTrae:
		models, err = trae.FetchUpstreamModels(key)
	case constant.ChannelTypeCline:
		base := channel.GetBaseURL()
		models, err = cline.FetchUpstreamModels(base, key)
	case constant.ChannelTypeRaccoon:
		models, err = raccoon.FetchUpstreamModels("", key)
	default:
		return nil, false
	}
	// 合并上游返回模型与完整默认模型池，确保即使之前只保存了 1 个模型，也能重新找回全部模型
	merged := make([]string, 0, len(models)+16)
	seen := map[string]bool{}
	for _, m := range models {
		v := strings.TrimSpace(m)
		if v != "" && !seen[v] {
			seen[v] = true
			merged = append(merged, v)
		}
	}
	for _, m := range DefaultModelsForChannel(channel) {
		if !seen[m] {
			seen[m] = true
			merged = append(merged, m)
		}
	}
	if len(merged) == 0 && err != nil {
		common.ApiError(c, err)
		return nil, true
	}
	// 持久化全量候选模型池到 other_info.allModels
	info := channel.GetOtherInfo()
	if info == nil {
		info = map[string]any{}
	}
	info["allModels"] = merged
	channel.SetOtherInfo(info)
	_ = model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("other_info", channel.OtherInfo).Error

	c.JSON(http.StatusOK, gin.H{"success": true, "data": merged})
	return merged, true
}

var _ = operation_setting.SelfChannelEnabled // 组开关在 router 层；引用防未用告警

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
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/trae"
	"github.com/QuantumNous/new-api/relay/channel/workbuddy"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// SelfOAuthStart 发起账号渠道 OAuth 登录（目前支持 workbuddy 设备授权流）。
// body: {"type":"workbuddy", "realm":"cn|global"}；返回 {sessionId, authUrl, expiresIn}。
func SelfOAuthStart(c *gin.Context) {
	var req struct {
		Type  string `json:"type"`
		Realm string `json:"realm"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	switch strings.ToLower(strings.TrimSpace(req.Type)) {
	case "workbuddy":
		realm := strings.ToLower(strings.TrimSpace(req.Realm))
		// WORKBUDDY_BASE_URL env 为 dev/e2e 全局上游覆盖（生产留空）。
		sess, err := workbuddy.OAuthStart(realm, os.Getenv("WORKBUDDY_BASE_URL"))
		if err != nil {
			common.ApiError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"sessionId": sess.State,
				"authUrl":   sess.AuthURL,
				"realm":     sess.Realm,
				"expiresIn": sess.ExpiresIn,
			},
		})
	default:
		common.ApiErrorMsg(c, "unsupported oauth type (expect workbuddy)")
	}
}

// SelfOAuthPoll 轮询 OAuth 登录态。
// mode=preview（默认）：成功后返回脱敏凭据预览，由前端确认后走常规建渠道流程；
// mode=create：成功后直接建渠道（name/group/models 可用 query 传入）。
func SelfOAuthPoll(c *gin.Context) {
	sessionId := strings.TrimSpace(c.Query("sessionId"))
	if sessionId == "" {
		common.ApiErrorMsg(c, "missing sessionId")
		return
	}
	done, cred, err := workbuddy.OAuthPoll(sessionId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !done {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": false}})
		return
	}
	if c.DefaultQuery("mode", "preview") == "create" {
		channel, cerr := createAccountChannel(c, constant.ChannelTypeWorkBuddy, cred.Nickname, cred.Marshal())
		if cerr != nil {
			common.ApiError(c, cerr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": true, "channel": channel}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"done":       true,
		"credential": cred.Masked(),
		"key":        cred.Marshal(),
	}})
}

// SelfImportTrae 导入 Trae 凭据：callbackUrl 或 refreshToken(+uid) 二选一。
// 返回脱敏凭据预览 + 可一键建渠道的 key（前端确认后走 POST /api/channel/self 建渠道，
// 或 mode=create 直接建）。
func SelfImportTrae(c *gin.Context) {
	var req struct {
		CallbackURL  string `json:"callbackUrl"`
		RefreshToken string `json:"refreshToken"`
		UID          string `json:"uid"`
		MachineID    string `json:"machineId"`
		DeviceID     string `json:"deviceId"`
		Mode         string `json:"mode"` // preview（默认）| create
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	var cred *trae.Cred
	var err error
	if strings.TrimSpace(req.CallbackURL) != "" {
		info, perr := trae.ParseCallback(req.CallbackURL)
		if perr != nil {
			common.ApiError(c, perr)
			return
		}
		cred, err = trae.ImportCredential(info, req.MachineID, req.DeviceID, "")
	} else if strings.TrimSpace(req.RefreshToken) != "" {
		cred, err = trae.ImportFromRefreshToken(req.RefreshToken, req.UID, req.MachineID, req.DeviceID, "")
	} else {
		common.ApiErrorMsg(c, "callbackUrl or refreshToken is required")
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Mode == "create" {
		channel, cerr := createAccountChannel(c, constant.ChannelTypeTrae, cred.Nickname, cred.Marshal())
		if cerr != nil {
			common.ApiError(c, cerr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": true, "channel": channel}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"done":       true,
		"credential": cred.Masked(),
		"key":        cred.Marshal(),
		// 刷新会踢同账号其他客户端下线（ExchangeToken 整族轮换）——前端必须明示。
		"notice": "Trae 刷新令牌会轮换整族 token 并踢下同账号其他客户端；建议专号专用。",
	}})
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

// SelfFetchAccountModels 拉取账号型渠道的实时模型目录（fetch_models 扩展：
// workbuddy/trae 走各自上游模型端点，其余类型回落既有流程）。
// 该函数供 router 在 fetch_models 分支前分流调用。
func SelfFetchAccountModels(c *gin.Context, channel *model.Channel) ([]string, bool) {
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
	default:
		return nil, false
	}
	if err != nil {
		common.ApiError(c, err)
		return nil, true
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": models})
	return nil, true
}

var _ = operation_setting.SelfChannelEnabled // 组开关在 router 层；引用防未用告警

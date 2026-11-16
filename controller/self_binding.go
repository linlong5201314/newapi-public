package controller

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// CreateSelfBinding POST /api/channel/self/bindings
// body: {"provider":"ds2api","accounts":"a@x.com----pwd\nb@y.com----pwd","public":false,"displayName":"我的号"}
// 用户只需提交上游账号密码，平台在后台为其单独开通代理实例，用户端看不到任何部署地址。
func CreateSelfBinding(c *gin.Context) {
	uid := c.GetInt("id")
	var req struct {
		Provider    string `json:"provider"`
		Accounts    string `json:"accounts"`
		DisplayName string `json:"displayName"`
		Public      bool   `json:"public"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	provider := strings.TrimSpace(req.Provider)
	tpl := constant.GetFreeSourceTemplate(provider)
	if tpl == nil {
		common.ApiErrorMsg(c, "未知的提供方")
		return
	}
	if tpl.AuthMode != "account" && tpl.AuthMode != "oauth" && tpl.AuthMode != "gateway" && tpl.AuthMode != "bridge" {
		common.ApiErrorMsg(c, "该来源不是账号授权类型，请直接用模板添加渠道")
		return
	}
	kind := "password"
	var creds []model.SelfAccountCred
	var err error
	switch {
	case tpl.AuthMode == "gateway" || tpl.Id == "magpie":
		kind = "gateway"
		creds, err = model.ParseGatewayKeys(req.Accounts)
	case tpl.AuthMode == "bridge":
		// 客户端登录态反代（小浣熊等）：access_token----refresh_token 每行一个
		kind = "bridge"
		creds, err = model.ParseAccountLines(req.Accounts)
	case tpl.AuthMode == "oauth":
		kind = "oauth"
		creds, err = model.ParseOAuthToken(req.Accounts)
	default:
		creds, err = model.ParseAccountLines(req.Accounts)
	}
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	binding := &model.SelfAccountBinding{
		UserId:      uid,
		Provider:    tpl.Id,
		Kind:        kind,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Status:      "pending",
	}
	if binding.DisplayName == "" {
		binding.DisplayName = tpl.Name
	}
	if req.Public {
		binding.Public = 1
	}
	if err := binding.EncodeAccounts(creds); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.CreateSelfBinding(binding); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"binding": binding})
}

// GetSelfBindings GET /api/channel/self/bindings
func GetSelfBindings(c *gin.Context) {
	uid := c.GetInt("id")
	rows, err := model.ListSelfBindings(uid)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows, "total": len(rows)})
}

// DeleteSelfBinding DELETE /api/channel/self/bindings/:id
func DeleteSelfBinding(c *gin.Context) {
	uid := c.GetInt("id")
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的绑定 ID")
		return
	}
	channelId, err := model.DeleteSelfBinding(uid, id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if channelId > 0 {
		if err := model.DisableSelfChannel(uid, channelId); err != nil {
			common.ApiError(c, err)
			return
		}
		model.InitChannelCache()
	}
	common.ApiSuccess(c, nil)
}

// SetSelfBindingPublic POST /api/channel/self/bindings/:id/public  body:{"public":true}
func SetSelfBindingPublic(c *gin.Context) {
	uid := c.GetInt("id")
	id, _ := strconv.Atoi(c.Param("id"))
	var req struct {
		Public bool `json:"public"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	public := 0
	if req.Public {
		public = 1
	}
	channelId, err := model.SetSelfBindingPublic(uid, id, public)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if channelId > 0 {
		model.InitChannelCache()
	}
	common.ApiSuccess(c, gin.H{"public": public})
}

// AdminListBindings GET /api/channel/admin/bindings —— 仅管理员，可见 service_ref（部署实例）与错误。
func AdminListBindings(c *gin.Context) {
	rows, err := model.ListAllBindings(200)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows, "total": len(rows)})
}

package controller

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

// CreateSelfBinding POST /api/channel/self/bindings
// body: {"provider":"ds2api","accounts":"a@x.com----pwd\nb@y.com----pwd","public":false,"displayName":"我的号"}
// 用户只需提交上游账号密码，平台在后台为其单独开通代理实例，用户端看不到任何部署地址。
func CreateSelfBinding(c *gin.Context) {
	uid := c.GetInt("id")
	var req struct {
		Provider    string                  `json:"provider"`
		Accounts    string                  `json:"accounts"`
		DisplayName string                  `json:"displayName"`
		Public      bool                    `json:"public"`
		Proxy       string                  `json:"proxy"`
		ProxyPolicy *dto.ChannelProxyPolicy `json:"proxy_policy"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	proxy, err := normalizeBindingProxy(req.Proxy)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := model.ValidateSelfChannelProxyPolicy(uid, req.ProxyPolicy, proxy); err != nil {
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
	if provider == "freepi" {
		common.ApiErrorMsg(c, "Use GitHub device authorization to connect FreePi")
		return
	}
	kind := "password"
	var creds []model.SelfAccountCred
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
		// 账号型：支持「账号----密码」「账号----会话Token」「裸 Token」三种写法，
		// 因为 ds2api 这类网页反代对数据中心 IP 的密码登录会被上游设备风控拦掉，
		// 用户改用浏览器登录后的会话 token 才能通。
		creds, err = model.ParseAccountOrTokenLines(req.Accounts)
		if err == nil && len(creds) > 0 {
			tokenCount := 0
			for _, cred := range creds {
				if model.IsLikelySessionToken(cred.Password) {
					tokenCount++
				}
			}
			if tokenCount > 0 && tokenCount < len(creds) {
				kind = "mixed"
			} else if tokenCount == len(creds) {
				kind = "token"
			}
		}
	}
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	binding := &model.SelfAccountBinding{
		ProxyPolicy: req.ProxyPolicy,
		UserId:      uid,
		Provider:    tpl.Id,
		Kind:        kind,
		Proxy:       proxy,
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
	binding.ProxyConfigured = binding.Proxy != ""
	binding.Proxy = ""
	binding.ServiceRef = ""
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
	for i := range rows {
		if creds, err := rows[i].DecodeAccounts(); err == nil && len(creds) > 0 {
			rows[i].ProxyPolicy = creds[0].ProxyPolicy
		}
		rows[i].ProxyConfigured = rows[i].Proxy != ""
		rows[i].Proxy = ""
		rows[i].ServiceRef = ""
		if rows[i].LastError != "" {
			rows[i].LastError = "Upstream provisioning failed. Please retry or contact an administrator"
		}
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

// normalizeBindingProxy 规范化用户提交的出口代理：
//
//	空字符串   跟随平台默认（用户出口池；池为空则直连）
//	direct     强制直连，忽略出口池
//	其它       http:// / https:// / socks5:// 开头的固定代理
func normalizeBindingProxy(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	if strings.EqualFold(v, "direct") || v == "直连" {
		return "direct", nil
	}
	l := strings.ToLower(v)
	if !strings.HasPrefix(l, "http://") && !strings.HasPrefix(l, "https://") && !strings.HasPrefix(l, "socks5://") {
		return "", fmt.Errorf("代理格式不对：需 http:// / https:// / socks5:// 开头，或填 direct 表示直连")
	}
	if _, err := url.ParseRequestURI(v); err != nil {
		return "", fmt.Errorf("代理地址解析失败: %v", err)
	}
	return v, nil
}

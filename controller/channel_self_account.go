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
	"github.com/QuantumNous/new-api/relay/channel/cline"
	"github.com/QuantumNous/new-api/relay/channel/raccoon"
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

// SelfRaccoonOAuthStart 发起小浣熊浏览器授权。
// 返回 {sessionId(=state), authUrl, callbackUrl, expiresIn}；用户打开 authUrl 登录后
// 浏览器会跳回我们的回调路由，服务端自动换票，前端轮询 poll 即可拿到渠道。
//
// 会话落库（self_oauth_sessions）而不是放进程内：授权链路要经过 gw.ailong.love
// 前置负载均衡，start 与 callback 可能落在不同节点。
func SelfRaccoonOAuthStart(c *gin.Context) {
	state, err := raccoon.NewState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.CreateSelfOAuthSession(&model.SelfOAuthSession{
		State:    state,
		Provider: "raccoon",
		Status:   "pending",
	}); err != nil {
		common.ApiError(c, err)
		return
	}
	model.PurgeExpiredSelfOAuthSessions(raccoon.SessionTTL * 2)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"sessionId":   state,
			"authUrl":     raccoon.AuthorizeURL(state),
			"callbackUrl": raccoon.CallbackURL(),
			"expiresIn":   int(raccoon.SessionTTL.Seconds()),
		},
	})
}

// SelfRaccoonOAuthCallback 浏览器授权回跳。
// 这个端点由浏览器直接访问（没有 JWT），所以只做 state 校验 + 换票，
// 然后回一段 HTML 让用户关掉标签页。
func SelfRaccoonOAuthCallback(c *gin.Context) {
	state := strings.TrimSpace(c.Query("state"))
	code := strings.TrimSpace(c.Query("code"))
	c.Header("Content-Type", "text/html; charset=utf-8")
	if state == "" || code == "" {
		c.String(http.StatusBadRequest, "<html><body style=\"font-family:system-ui;padding:32px\"><h3>授权回调缺少参数</h3><p>请回到平台页面重新点击「小浣熊授权」。</p></body></html>")
		return
	}
	sess, err := model.GetSelfOAuthSession(state, raccoon.SessionTTL)
	if err != nil || sess == nil {
		c.String(http.StatusOK, "<html><body style=\"font-family:system-ui;padding:32px\"><h3>授权会话不存在或已过期</h3><p>请回到平台页面重新点击授权。</p></body></html>")
		return
	}
	access, refresh, accountID, phone, xerr := raccoon.Exchange(code)
	if xerr != nil {
		common.SysLog("raccoon oauth callback failed: " + xerr.Error())
		_ = model.UpdateSelfOAuthSession(state, map[string]interface{}{"status": "failed", "err": xerr.Error()})
		c.String(http.StatusOK, "<html><body style=\"font-family:system-ui;padding:32px\"><h3>授权失败</h3><p>"+xerr.Error()+"</p><p>请回到平台页面重新点击授权。</p></body></html>")
		return
	}
	if err := model.UpdateSelfOAuthSession(state, map[string]interface{}{
		"status":     "done",
		"access":     access,
		"refresh":    refresh,
		"account_id": accountID,
		"phone":      phone,
		"err":        "",
	}); err != nil {
		common.SysLog("raccoon oauth session save failed: " + err.Error())
	}
	shown := phone
	if shown == "" {
		shown = accountID
	}
	c.String(http.StatusOK, "<html><body style=\"font-family:system-ui;padding:32px\"><h3>授权成功</h3><p>账号："+shown+"</p><p>可以关闭此页面，回到平台即可看到已开通的渠道。</p></body></html>")
}

// SelfRaccoonOAuthPoll 轮询小浣熊授权结果。
// mode=create（默认）成功后直接建 type 68 渠道；mode=preview 只返回脱敏凭据。
func SelfRaccoonOAuthPoll(c *gin.Context) {
	state := strings.TrimSpace(c.Query("sessionId"))
	if state == "" {
		common.ApiErrorMsg(c, "missing sessionId")
		return
	}
	sess, err := model.GetSelfOAuthSession(state, raccoon.SessionTTL)
	if err != nil || sess == nil {
		common.ApiErrorMsg(c, "授权会话不存在或已过期，请重新发起授权")
		return
	}
	if sess.Status == "failed" {
		common.ApiErrorMsg(c, sess.Err)
		return
	}
	if sess.Status != "done" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": false}})
		return
	}
	keyJSON, _ := common.Marshal(map[string]string{
		"access":  sess.Access,
		"refresh": sess.Refresh,
	})
	if c.DefaultQuery("mode", "create") == "preview" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"done": true,
			"credential": gin.H{
				"account": maskTail(sess.Phone),
				"access":  maskTail(sess.Access),
			},
			"key": string(keyJSON),
		}})
		return
	}
	// 同一会话只允许开通一次：先原子地把 status 改成 consumed，抢到的那次才建渠道。
	affected := model.DB.Model(&model.SelfOAuthSession{}).
		Where("state = ? and status = ?", state, "done").
		Update("status", "consumed").RowsAffected
	if affected == 0 {
		common.ApiErrorMsg(c, "该授权已开通渠道，如需再开请重新授权")
		return
	}
	// 模型目录用真实 token 现拉，拉不到回退内置列表。
	models := raccoon.ModelList
	if got, ferr := raccoon.FetchUpstreamModels("", sess.Access); ferr == nil && len(got) > 0 {
		models = got
	}
	channel, cerr := createAccountChannelWithModels(c, constant.ChannelTypeRaccoon, "小浣熊 "+maskTail(sess.Phone), string(keyJSON), models)
	if cerr != nil {
		_ = model.UpdateSelfOAuthSession(state, map[string]interface{}{"status": "done"})
		common.ApiError(c, cerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": true, "channel": channel, "models": models}})
}

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

// ---------------------------------------------------------------------------
// DS2API（DeepSeek 网页端）一键生成授权链接 → 复制去浏览器登录/确认 → 自动返回建渠道
// ---------------------------------------------------------------------------

// SelfDs2ApiOAuthStart POST /api/channel/self/oauth/ds2api/start
func SelfDs2ApiOAuthStart(c *gin.Context) {
	state, err := raccoon.NewState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.CreateSelfOAuthSession(&model.SelfOAuthSession{
		State:    state,
		Provider: "ds2api",
		Status:   "pending",
	}); err != nil {
		common.ApiError(c, err)
		return
	}
	scheme := "https"
	if c.Request != nil && strings.HasPrefix(c.Request.Host, "localhost") || strings.HasPrefix(c.Request.Host, "127.0.0.1") {
		if c.Request.TLS == nil {
			scheme = "http"
		}
	}
	if proto := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); proto != "" {
		scheme = proto
	}
	host := c.Request.Host
	authUrl := scheme + "://" + host + "/api/channel/self/oauth/ds2api/authorize?state=" + state
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"sessionId": state,
			"authUrl":   authUrl,
			"expiresIn": int(raccoon.SessionTTL.Seconds()),
		},
	})
}

// SelfDs2ApiOAuthAuthorizePage GET /api/channel/self/oauth/ds2api/authorize?state=xxx
// 用户复制链接到已登录 chat.deepseek.com 的浏览器打开，支持一键回传或粘贴登录态，授权后原窗口自动开通渠道。
func SelfDs2ApiOAuthAuthorizePage(c *gin.Context) {
	state := strings.TrimSpace(c.Query("state"))
	c.Header("Content-Type", "text/html; charset=utf-8")
	if state == "" {
		c.String(http.StatusBadRequest, "<h3>缺少授权参数 state</h3>")
		return
	}
	scheme := "https"
	if proto := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); proto != "" {
		scheme = proto
	} else if c.Request.TLS == nil && (strings.HasPrefix(c.Request.Host, "localhost") || strings.HasPrefix(c.Request.Host, "127.0.0.1")) {
		scheme = "http"
	}
	callbackURL := scheme + "://" + c.Request.Host + "/api/channel/self/oauth/ds2api/callback"
	bookmarklet := "javascript:(function(){try{var raw=localStorage.getItem('userToken')||'';var t=raw;try{var o=JSON.parse(raw);if(o&&o.value)t=o.value;}catch(e){}if(!t){t=prompt('未检测到本地 userToken，请粘贴 DeepSeek Token：')||'';}if(t){location.href='" + callbackURL + "?state=" + state + "&token='+encodeURIComponent(t);}}catch(e){alert(e);}})();"

	html := `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>DeepSeek (DS2API) 账号授权连接</title>
<style>
body{font-family:system-ui,-apple-system,sans-serif;background:#0f1117;color:#f3f5f9;margin:0;padding:24px;display:flex;justify-content:center}
.card{max-width:560px;width:100%;background:#171b26;border:1px solid #262c3d;border-radius:16px;padding:24px;box-sizing:border-box}
h2{margin:0 0 8px;font-size:18px;color:#fff}
p{color:#9ca3af;font-size:13px;line-height:1.6;margin:6px 0}
.step{background:#10131c;border:1px solid #262c3d;border-radius:12px;padding:14px;margin-top:14px}
.btn{display:inline-block;background:#4d6bfe;color:#fff;text-decoration:none;border:none;border-radius:8px;padding:9px 16px;font-size:13px;font-weight:600;cursor:pointer;margin-top:8px}
.btn-sec{background:#262c3d;color:#e5e7eb}
textarea,input{width:100%;background:#0f1117;color:#f3f5f9;border:1px solid #262c3d;border-radius:8px;padding:10px;font-family:monospace;font-size:12px;box-sizing:border-box;margin-top:8px}
</style></head><body><div class="card">
<h2>🔗 DeepSeek 网页账号快速授权 (DS2API)</h2>
<p>完成下方任意一种方式后，平台原页面将<strong>自动收到授权、自动为该号生成独立虚拟设备指纹并开通渠道</strong>：</p>
<div class="step">
  <strong>方式一：去 DeepSeek 官网登录并一键回传（推荐）</strong>
  <p>1. 点击下方按钮打开 <code>chat.deepseek.com</code> 登录你的账号；<br>
     2. 登录后在地址栏粘贴或点击复制的「一键回传命令」，自动读取 Token 并返回：</p>
  <div style="display:flex;gap:8px;flex-wrap:wrap">
    <a class="btn" href="https://chat.deepseek.com" target="_blank" rel="noopener">① 打开 chat.deepseek.com 登录</a>
    <button class="btn btn-sec" onclick="navigator.clipboard.writeText(document.getElementById('bm').value);this.textContent='✅ 已复制回传脚本（在 DeepSeek 页按 F12 控制台粘贴回车）'">② 复制一键自动回传脚本</button>
  </div>
  <input id="bm" readonly value="` + strings.ReplaceAll(bookmarklet, "javascript:", "") + `" onclick="this.select()" />
</div>
<div class="step">
  <strong>方式二：直接在此提交登录凭据 / Token / 抓包头（立即返回）</strong>
  <form method="POST" action="` + callbackURL + `">
    <input type="hidden" name="state" value="` + state + `" />
    <textarea name="token" rows="4" placeholder="粘贴 DeepSeek Token（如 SlCmRF5j+...）&#10;或整段 F12 Request Headers（含 authorization: Bearer ...）&#10;或 账号----Token / 账号----密码"></textarea>
    <button type="submit" class="btn" style="width:100%">✅ 确认授权并自动返回开通渠道</button>
  </form>
</div>
</div></body></html>`
	c.String(http.StatusOK, html)
}

// SelfDs2ApiOAuthCallback GET/POST /api/channel/self/oauth/ds2api/callback
func SelfDs2ApiOAuthCallback(c *gin.Context) {
	state := strings.TrimSpace(c.Query("state"))
	tokenRaw := strings.TrimSpace(c.Query("token"))
	if state == "" {
		state = strings.TrimSpace(c.PostForm("state"))
	}
	if tokenRaw == "" {
		tokenRaw = strings.TrimSpace(c.PostForm("token"))
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	if state == "" || tokenRaw == "" {
		c.String(http.StatusBadRequest, "<html><body style=\"font-family:system-ui;background:#0f1117;color:#fff;padding:32px\"><h3>缺少授权参数</h3><p>请返回授权页填写 Token 后提交。</p></body></html>")
		return
	}
	sess, err := model.GetSelfOAuthSession(state, raccoon.SessionTTL)
	if err != nil || sess == nil {
		c.String(http.StatusOK, "<html><body style=\"font-family:system-ui;background:#0f1117;color:#fff;padding:32px\"><h3>授权链接已过期</h3><p>请回到平台重新点击「生成 DS2API 授权链接」。</p></body></html>")
		return
	}
	creds, perr := model.ParseAccountOrTokenLines(tokenRaw)
	if perr != nil || len(creds) == 0 {
		// 单行裸 token 兜底
		creds = []model.SelfAccountCred{{
			Account:  "",
			Password: tokenRaw,
			Device:   model.BuildDeepSeekDeviceProfile(tokenRaw),
		}}
	}
	first := creds[0]
	devId := ""
	if first.Device != nil {
		devId = first.Device.DeviceId
	}
	_ = model.UpdateSelfOAuthSession(state, map[string]interface{}{
		"status":     "done",
		"access":     first.Password,
		"refresh":    tokenRaw,
		"account_id": devId,
		"phone":      first.Account,
		"err":        "",
	})
	c.String(http.StatusOK, "<html><body style=\"font-family:system-ui;background:#0f1117;color:#fff;padding:32px\"><h3>✅ DeepSeek (DS2API) 授权成功！</h3><p>已分配专属虚拟设备指纹：<code>"+devId+"</code></p><p>原平台窗口将自动完成渠道创建，你可以直接关闭此标签页。</p><script>setTimeout(function(){try{window.close();}catch(e){}},1200);</script></body></html>")
}

// SelfDs2ApiOAuthPoll GET /api/channel/self/oauth/ds2api/poll?sessionId=xxx
func SelfDs2ApiOAuthPoll(c *gin.Context) {
	uid := c.GetInt("id")
	state := strings.TrimSpace(c.Query("sessionId"))
	if state == "" {
		common.ApiErrorMsg(c, "missing sessionId")
		return
	}
	sess, err := model.GetSelfOAuthSession(state, raccoon.SessionTTL)
	if err != nil || sess == nil {
		common.ApiErrorMsg(c, "授权会话不存在或已过期")
		return
	}
	if sess.Status == "failed" {
		common.ApiErrorMsg(c, sess.Err)
		return
	}
	if sess.Status != "done" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"done": false}})
		return
	}
	affected := model.DB.Model(&model.SelfOAuthSession{}).
		Where("state = ? and status = ?", state, "done").
		Update("status", "consumed").RowsAffected
	if affected == 0 {
		common.ApiErrorMsg(c, "该授权会话已开通渠道")
		return
	}

	rawInput := strings.TrimSpace(sess.Refresh)
	if rawInput == "" {
		rawInput = sess.Access
	}
	creds, perr := model.ParseAccountOrTokenLines(rawInput)
	if perr != nil || len(creds) == 0 {
		creds = []model.SelfAccountCred{{
			Account:  sess.Phone,
			Password: sess.Access,
			Device:   model.BuildDeepSeekDeviceProfile(sess.Access),
		}}
	}

	// 1. 写入账号绑定表（带独立虚拟设备指纹，供后台 ds2api 实例同步）
	binding := &model.SelfAccountBinding{
		UserId:      uid,
		Provider:    "ds2api",
		Kind:        "token",
		DisplayName: "DS2API 授权账号",
		Status:      "pending",
	}
	_ = binding.EncodeAccounts(creds)
	_ = model.CreateSelfBinding(binding)

	// 2. 立即为用户创建一条 DS2API 渠道（用户在「我的渠道」和「我的模型」立即可见可用）
	tpl := constant.GetFreeSourceTemplate("ds2api")
	models := []string{"deepseek-v4.1-flash", "deepseek-chat", "deepseek-reasoner"}
	baseURL := "https://ds2api-production.up.railway.app"
	if tpl != nil {
		if len(tpl.Models) > 0 {
			models = append(tpl.Models, "deepseek-chat", "deepseek-reasoner")
		}
		if defBase := strings.TrimSpace(tpl.DefaultExtra["base"]); defBase != "" {
			baseURL = defBase
		}
	}
	weight := uint(0)
	autoBan := 1
	chName := "DS2API · " + maskTail(sess.Access)
	channel := &model.Channel{
		Type:        constant.ChannelTypeOpenAI,
		Name:        chName,
		Key:         sess.Access,
		BaseURL:     &baseURL,
		Group:       "default",
		Models:      strings.Join(models, ","),
		OwnerId:     uid,
		Status:      common.ChannelStatusEnabled,
		Weight:      &weight,
		AutoBan:     &autoBan,
		CreatedTime: common.GetTimestamp(),
	}
	devProfile := creds[0].Device
	channel.SetOtherInfo(map[string]any{
		"templateId":    "ds2api",
		"poolKey":       "ds2api",
		"allModels":     models,
		"deviceProfile": devProfile,
	})
	if err := channel.Insert(); err != nil {
		common.ApiError(c, err)
		return
	}
	_ = channel.AddAbilities(nil)
	_ = model.MarkBindingStatus(binding.Id, "ready", channel.Id, "auto-oauth", "")
	model.InitChannelCache()
	channel.Key = ""

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"done":           true,
			"channel":        channel,
			"models":         models,
			"device_profile": devProfile,
		},
	})
}

var _ = operation_setting.SelfChannelEnabled // 组开关在 router 层；引用防未用告警

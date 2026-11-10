// checkin_center.go M3 签到中心 API（/api/checkin 组，UserAuth，全强制 owner）。
package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/checkin"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// checkinSiteMetas GET /api/checkin/sites 的站点模板元数据（名称/mode/凭据表单 schema/引导文案）。
var checkinSiteMetas = []gin.H{
	{
		"site":  "workbuddy",
		"name":  "WorkBuddy/CodeBuddy",
		"mode":  "api",
		"icon":  "workbuddy",
		"cred":  gin.H{"kind": "channel_ref", "channelType": 65, "desc": "引用「我的渠道」中的 WorkBuddy 渠道凭据；或粘贴独立凭据 JSON"},
		"guide": "每日签到 + 连登管家闭环（补签卡/礼包/补偿/streak 兑换/抽奖）+ 任务中心幂等清扫。行为链任务（expert/template/canvas 等）暂不支持。",
	},
	{
		"site":  "trae",
		"name":  "Trae SOLO",
		"mode":  "api",
		"icon":  "trae",
		"cred":  gin.H{"kind": "channel_ref", "channelType": 66, "desc": "引用「我的渠道」中的 Trae 渠道凭据；或粘贴独立凭据 JSON"},
		"guide": "每日签到 status→claim（+200/日）+ EntUsage 刷新渠道余额。刷新令牌会踢同账号其他客户端下线。",
	},
	{
		"site":  "apinex",
		"name":  "APInex",
		"mode":  "agent",
		"icon":  "apinex",
		"cred":  gin.H{"kind": "cookie", "key": "cookie", "desc": "apinex_user_session（HttpOnly cookie 值）"},
		"guide": "Turnstile 强制，纯 API 无法签到：配置 agent 执行器地址（云机 apinex-auto HTTP 端点）远程执行。",
	},
	{
		"site":  "custom",
		"name":  "自定义站点",
		"mode":  "api|agent",
		"icon":  "custom",
		"cred":  gin.H{"kind": "json", "desc": `{"name":"名称","endpoint":"https://…","method":"POST","body":"{}","headers":{"Cookie":"…"},"success_path":"code","expect":"0"}`},
		"guide": "通用模板：endpoint 经 SSRF 校验；success_path 为响应 JSON 的 dot 路径（如 code / data.checked_in）。",
	},
}

// GetCheckinSites GET /api/checkin/sites — 站点模板元数据。
func GetCheckinSites(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"sites": checkinSiteMetas,
			"options": gin.H{
				"enabled":            operation_setting.CheckinEnabled,
				"maxAccountsPerUser": operation_setting.CheckinMaxAccountsPerUser,
				"allowAgentUrl":      operation_setting.CheckinAllowAgentUrl,
			},
		},
	})
}

// GetCheckinAccounts GET /api/checkin/accounts — 当前用户全部签到账号。
func GetCheckinAccounts(c *gin.Context) {
	list, err := model.GetCheckinAccounts(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// CredJson 不回传（凭据字段仅服务端可见）。
	out := make([]gin.H, 0, len(list))
	for _, a := range list {
		out = append(out, gin.H{
			"id": a.Id, "site": a.Site, "label": a.Label, "channel_id": a.ChannelId,
			"mode": a.Mode, "agent_url": a.AgentUrl, "enabled": a.Enabled,
			"last_status": a.LastStatus, "last_result": a.LastResult,
			"streak": a.Streak, "total_reward": a.TotalReward,
			"last_checkin_day": a.LastCheckinDay, "fail_count": a.FailCount,
			"created_time": a.CreatedTime, "updated_time": a.UpdatedTime,
			"has_cred": strings.TrimSpace(a.CredJson) != "",
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// AddCheckinAccount POST /api/checkin/accounts — 新建签到账号。
func AddCheckinAccount(c *gin.Context) {
	var req struct {
		Site      string `json:"site"`
		Label     string `json:"label"`
		ChannelId int    `json:"channel_id"`
		CredJson  string `json:"cred_json"`
		Mode      string `json:"mode"`
		AgentUrl  string `json:"agent_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	uid := c.GetInt("id")
	site := strings.ToLower(strings.TrimSpace(req.Site))
	switch site {
	case "workbuddy", "trae", "apinex", "custom":
	default:
		common.ApiErrorMsg(c, "site must be workbuddy|trae|apinex|custom")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		if site == "apinex" {
			mode = "agent"
		} else {
			mode = "api"
		}
	}
	if mode != "api" && mode != "agent" {
		common.ApiErrorMsg(c, "mode must be api|agent")
		return
	}
	if mode == "agent" {
		if !operation_setting.CheckinAllowAgentUrl {
			common.ApiErrorMsg(c, "agent mode disabled by platform (CheckinAllowAgentUrl=false)")
			return
		}
		if strings.TrimSpace(req.AgentUrl) == "" {
			common.ApiErrorMsg(c, "agent mode requires agent_url")
			return
		}
	}
	// 数量上限
	if count, err := model.CountCheckinAccounts(uid); err == nil && count >= int64(operation_setting.CheckinMaxAccountsPerUser) {
		common.ApiErrorMsg(c, "checkin account count limit reached")
		return
	}
	// 渠道引用校验（owner 强制 + 类型匹配）。
	if req.ChannelId > 0 {
		ch, err := model.GetChannelById(req.ChannelId, true)
		if err != nil || ch == nil || ch.OwnerId != uid {
			common.ApiErrorMsg(c, "referenced channel not found")
			return
		}
		wantType := 0
		if site == "workbuddy" {
			wantType = 65 // constant.ChannelTypeWorkBuddy
		} else if site == "trae" {
			wantType = 66 // constant.ChannelTypeTrae
		}
		if wantType != 0 && ch.Type != wantType {
			common.ApiErrorMsg(c, "referenced channel type mismatch")
			return
		}
	}
	acc := &model.CheckinAccount{
		UserId:    uid,
		Site:      site,
		Label:     strings.TrimSpace(req.Label),
		ChannelId: req.ChannelId,
		CredJson:  model.EncryptCheckinCred(strings.TrimSpace(req.CredJson)),
		Mode:      mode,
		AgentUrl:  strings.TrimSpace(req.AgentUrl),
		Enabled:   true,
	}
	if acc.Label == "" {
		acc.Label = site
	}
	if err := model.CreateCheckinAccount(acc); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"id": acc.Id}})
}

// UpdateCheckinAccount PUT /api/checkin/accounts/:id — 更新（label/cred/mode/agent_url/enabled）。
func UpdateCheckinAccount(c *gin.Context) {
	uid := c.GetInt("id")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	acc, err := model.GetCheckinAccount(uid, id)
	if err != nil {
		writeCheckinNotFound(c)
		return
	}
	var req struct {
		Label     *string `json:"label"`
		ChannelId *int    `json:"channel_id"`
		CredJson  *string `json:"cred_json"`
		Mode      *string `json:"mode"`
		AgentUrl  *string `json:"agent_url"`
		Enabled   *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	if req.Label != nil {
		acc.Label = strings.TrimSpace(*req.Label)
	}
	if req.ChannelId != nil {
		if *req.ChannelId > 0 {
			ch, cerr := model.GetChannelById(*req.ChannelId, true)
			if cerr != nil || ch == nil || ch.OwnerId != uid {
				common.ApiErrorMsg(c, "referenced channel not found")
				return
			}
		}
		acc.ChannelId = *req.ChannelId
	}
	if req.CredJson != nil {
		acc.CredJson = model.EncryptCheckinCred(strings.TrimSpace(*req.CredJson))
	}
	if req.Mode != nil {
		mode := strings.ToLower(strings.TrimSpace(*req.Mode))
		if mode != "api" && mode != "agent" {
			common.ApiErrorMsg(c, "mode must be api|agent")
			return
		}
		if mode == "agent" && !operation_setting.CheckinAllowAgentUrl {
			common.ApiErrorMsg(c, "agent mode disabled by platform")
			return
		}
		acc.Mode = mode
	}
	if req.AgentUrl != nil {
		acc.AgentUrl = strings.TrimSpace(*req.AgentUrl)
	}
	if req.Enabled != nil {
		acc.Enabled = *req.Enabled
	}
	if err := model.UpdateCheckinAccount(acc); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteCheckinAccount DELETE /api/checkin/accounts/:id。
func DeleteCheckinAccount(c *gin.Context) {
	uid := c.GetInt("id")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	if err := model.DeleteCheckinAccount(uid, id); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RunCheckinAccount POST /api/checkin/accounts/:id/run — 手动触发（判重幂等）。
func RunCheckinAccount(c *gin.Context) {
	uid := c.GetInt("id")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	acc, err := model.GetCheckinAccount(uid, id)
	if err != nil {
		writeCheckinNotFound(c)
		return
	}
	res := checkin.RunAccountManual(acc)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"ok": res.Success, "skipped": res.Skipped, "message": res.Message,
		"reward": res.Reward, "balance": res.Balance,
	}})
}

// GetCheckinAccountLogs GET /api/checkin/accounts/:id/logs?page=&page_size=。
func GetCheckinAccountLogs(c *gin.Context) {
	uid := c.GetInt("id")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	if _, err := model.GetCheckinAccount(uid, id); err != nil {
		writeCheckinNotFound(c)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	list, total, err := model.GetCheckinLogs(uid, id, (page-1)*pageSize, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"items": list, "total": total, "page": page, "page_size": pageSize,
	}})
}

func writeCheckinNotFound(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": false, "message": "checkin account not found"})
}

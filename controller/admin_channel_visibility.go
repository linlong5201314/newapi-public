package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// AdminListPlatformChannels 列出平台渠道（owner_id=0），供管理员决定哪些对外公开。
func AdminListPlatformChannels(c *gin.Context) {
	var rows []model.Channel
	if err := model.DB.Where("owner_id = ?", 0).Order("id asc").Find(&rows).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, ch := range rows {
		items = append(items, gin.H{
			"id":         ch.Id,
			"name":       ch.Name,
			"models":     ch.Models,
			"group":      ch.Group,
			"status":     ch.Status,
			"visibility": ch.Visibility,
			"tier":       ch.Tier,
			"allow_ips":  ch.AllowIps,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items, "total": len(items)}})
}

// AdminSetChannelVisibility 管理员调整任意渠道（含平台渠道）的公开状态与层级。
func AdminSetChannelVisibility(c *gin.Context) {
	var req struct {
		ChannelId  int    `json:"channelId"`
		Visibility int    `json:"visibility"`
		Tier       string `json:"tier"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ChannelId <= 0 {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	updates := map[string]any{}
	if req.Visibility == 0 || req.Visibility == 1 {
		updates["visibility"] = req.Visibility
	}
	if tier := strings.TrimSpace(req.Tier); tier == "free" || tier == "api" {
		updates["tier"] = tier
	}
	if len(updates) == 0 {
		common.ApiErrorMsg(c, "nothing to update")
		return
	}
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", req.ChannelId).Updates(updates).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"updated": true}})
}

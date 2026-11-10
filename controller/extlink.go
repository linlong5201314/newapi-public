// extlink.go M4 外链模块管理 API（/api/extlink 组）。
//
// 蓝图 docs/M4-外链模块设计.md §2：写操作 RootAuth + 审计；GET /api/extlink/enabled
// 对登录用户公开（仅返回 enabled 的）。URL 保存时过 SSRF 校验（service/extlink）。
package controller

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	extlinksvc "github.com/QuantumNous/new-api/service/extlink"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// extLinkSlugPattern slug 形态：小写字母数字开头，可含中划线（映射 /app/<slug>）。
var extLinkSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// extLinkJSON 管理端/用户端共用的出参形态。
func extLinkJSON(link *model.ExtLink) gin.H {
	return gin.H{
		"id":           link.Id,
		"slug":         link.Slug,
		"name":         link.Name,
		"url":          link.Url,
		"icon":         link.Icon,
		"mode":         link.Mode,
		"position":     link.Position,
		"enabled":      link.Enabled,
		"sort_order":   link.SortOrder,
		"created_time": link.CreatedTime,
		"updated_time": link.UpdatedTime,
	}
}

// GetExtLinks GET /api/extlink/ — RootAuth 全量列表 + 平台开关回显。
func GetExtLinks(c *gin.Context) {
	links, err := model.GetAllExtLinks()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	out := make([]gin.H, 0, len(links))
	for _, link := range links {
		out = append(out, extLinkJSON(link))
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"links": out,
			"options": gin.H{
				"enabled":     operation_setting.ExtLinkEnabled,
				"max_count":   operation_setting.ExtLinkMaxCount,
				"allow_hosts": operation_setting.ExtLinkAllowHosts,
			},
		},
	})
}

// GetEnabledExtLinks GET /api/extlink/enabled — UserAuth 启用列表（首页卡片/侧栏/站内页数据源）。
func GetEnabledExtLinks(c *gin.Context) {
	links, err := model.GetEnabledExtLinks()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	out := make([]gin.H, 0, len(links))
	for _, link := range links {
		out = append(out, extLinkJSON(link))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// GetExtLink GET /api/extlink/:id — RootAuth 单条。
func GetExtLink(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	link, err := model.GetExtLinkById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": extLinkJSON(link)})
}

// extLinkPayload 新增/更新共用入参。
type extLinkPayload struct {
	Id        int    `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Url       string `json:"url"`
	Icon      string `json:"icon"`
	Mode      string `json:"mode"`
	Position  string `json:"position"`
	Enabled   *bool  `json:"enabled"`
	SortOrder int    `json:"sort_order"`
}

// validateExtLinkPayload 归一化 + 校验（slug 形态、mode/position 枚举、SSRF）。
func validateExtLinkPayload(p *extLinkPayload) error {
	p.Slug = model.NormalizeExtLinkSlug(p.Slug)
	p.Name = strings.TrimSpace(p.Name)
	p.Url = strings.TrimSpace(p.Url)
	p.Icon = strings.TrimSpace(p.Icon)
	if p.Mode == "" {
		p.Mode = model.ExtLinkModeProxy
	}
	if p.Position == "" {
		p.Position = model.ExtLinkPositionHomeCard
	}
	if !extLinkSlugPattern.MatchString(p.Slug) {
		return common.NewMessage("invalid slug: lowercase letters/digits/dashes, 1-64 chars")
	}
	if p.Name == "" {
		return common.NewMessage("name is required")
	}
	if !model.IsValidExtLinkMode(p.Mode) {
		return common.NewMessage("invalid mode: proxy | iframe | newtab")
	}
	if !model.IsValidExtLinkPosition(p.Position) {
		return common.NewMessage("invalid position: home_card | sidebar | both")
	}
	if err := extlinksvc.ValidateExtLinkURL(p.Url); err != nil {
		return err
	}
	return nil
}

// AddExtLink POST /api/extlink/ — RootAuth 新建。
func AddExtLink(c *gin.Context) {
	var req extLinkPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateExtLinkPayload(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	count, err := model.CountExtLinks()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if count >= int64(operation_setting.ExtLinkMaxCount) {
		common.ApiErrorMsg(c, "ext link count limit reached")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	link := &model.ExtLink{
		Slug:      req.Slug,
		Name:      req.Name,
		Url:       req.Url,
		Icon:      req.Icon,
		Mode:      req.Mode,
		Position:  req.Position,
		Enabled:   enabled,
		SortOrder: req.SortOrder,
	}
	if err := model.CreateExtLink(link); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "extlink.create", map[string]any{
		"name": link.Name, "slug": link.Slug, "mode": link.Mode, "id": link.Id,
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": extLinkJSON(link)})
}

// UpdateExtLink PUT /api/extlink/ — RootAuth 更新（body 带 id；slug 变更双失效缓存）。
func UpdateExtLink(c *gin.Context) {
	var req extLinkPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Id <= 0 {
		common.ApiErrorMsg(c, "id is required")
		return
	}
	link, err := model.GetExtLinkById(req.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	oldSlug := link.Slug
	if err := validateExtLinkPayload(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	link.Slug = req.Slug
	link.Name = req.Name
	link.Url = req.Url
	link.Icon = req.Icon
	link.Mode = req.Mode
	link.Position = req.Position
	link.SortOrder = req.SortOrder
	if req.Enabled != nil {
		link.Enabled = *req.Enabled
	}
	if err := model.UpdateExtLink(link); err != nil {
		common.ApiError(c, err)
		return
	}
	if oldSlug != link.Slug {
		model.InvalidateExtLinkCache(oldSlug)
	}
	recordManageAudit(c, "extlink.update", map[string]any{
		"name": link.Name, "slug": link.Slug, "mode": link.Mode, "id": link.Id,
		"enabled": link.Enabled,
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": extLinkJSON(link)})
}

// DeleteExtLink DELETE /api/extlink/:id — RootAuth 删除。
func DeleteExtLink(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	link, err := model.GetExtLinkById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DeleteExtLink(id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "extlink.delete", map[string]any{
		"name": link.Name, "slug": link.Slug, "id": id,
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

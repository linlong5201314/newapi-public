// free_source_pool.go 公共免费模型提交池 API。
package controller

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetFreeSourcePools GET /api/free_source_pools?sort=rating|newest — 公共池列表（评分排序）。
func GetFreeSourcePools(c *gin.Context) {
	sort := c.DefaultQuery("sort", "rating")
	list, err := model.GetFreeSourcePools(sort)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, p := range list {
		out = append(out, gin.H{
			"id":           p.Id,
			"name":         p.Name,
			"url":          p.Url,
			"signup_url":   p.SignupUrl,
			"homepage_url": p.HomepageUrl,
			"models":       p.Models,
			"description":  p.Description,
			"tags":         p.Tags,
			"rating":       p.Rating,
			"rating_count": p.RatingCount,
			"recommended":  p.Recommended,
			"submitter":    gin.H{"id": p.SubmitterId, "name": p.SubmitterName},
			"created_at":   p.CreatedAt.Format(time.RFC3339),
			"updated_at":   p.UpdatedAt.Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// GetFreeSourcePoolDetail GET /api/free_source_pools/:id — 单条详情。
func GetFreeSourcePoolDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	p, err := model.GetFreeSourcePoolById(id)
	if err != nil || p == nil {
		common.ApiErrorMsg(c, "pool not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"id":           p.Id,
		"name":         p.Name,
		"url":          p.Url,
		"signup_url":   p.SignupUrl,
		"homepage_url": p.HomepageUrl,
		"models":       p.Models,
		"description":  p.Description,
		"tags":         p.Tags,
		"rating":       p.Rating,
		"rating_count": p.RatingCount,
		"recommended":  p.Recommended,
		"submitter":    gin.H{"id": p.SubmitterId, "name": p.SubmitterName},
		"created_at":   p.CreatedAt.Format(time.RFC3339),
		"updated_at":   p.UpdatedAt.Format(time.RFC3339),
	}})
}

// SubmitFreeSourcePool POST /api/free_source_pools — 提交新条目（所有用户可用）。
func SubmitFreeSourcePool(c *gin.Context) {
	var req struct {
		Name        string   `json:"name"`
		Url         string   `json:"url"`
		SignupUrl   string   `json:"signup_url"`
		HomepageUrl string   `json:"homepage_url"`
		Models      []string `json:"models"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Url) == "" {
		common.ApiErrorMsg(c, "name and url are required")
		return
	}
	if !strings.HasPrefix(req.Url, "http://") && !strings.HasPrefix(req.Url, "https://") {
		common.ApiErrorMsg(c, "url must start with http:// or https://")
		return
	}

	userId := c.GetInt("id")
	username, _ := model.GetUsernameById(userId, true)

	pool := &model.FreeSourcePool{
		Name:          strings.TrimSpace(req.Name),
		Url:           strings.TrimSpace(req.Url),
		SignupUrl:     strings.TrimSpace(req.SignupUrl),
		HomepageUrl:   strings.TrimSpace(req.HomepageUrl),
		Models:        req.Models,
		Description:   strings.TrimSpace(req.Description),
		Tags:          req.Tags,
		SubmitterId:   userId,
		SubmitterName: username,
	}
	if err := model.CreateFreeSourcePool(pool); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"id": pool.Id}})
}

// RateFreeSourcePool POST /api/free_source_pools/:id/rate — 评分（1-5星）。
func RateFreeSourcePool(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	var req struct {
		Rating  int    `json:"rating"`
		Comment string `json:"comment"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	if req.Rating < 1 || req.Rating > 5 {
		common.ApiErrorMsg(c, "rating must be 1-5")
		return
	}

	pool, err := model.GetFreeSourcePoolById(id)
	if err != nil || pool == nil {
		common.ApiErrorMsg(c, "pool not found")
		return
	}

	userId := c.GetInt("id")
	rating := &model.FreeSourcePoolRating{
		PoolId:  id,
		UserId:  userId,
		Rating:  req.Rating,
		Comment: strings.TrimSpace(req.Comment),
	}
	if err := model.CreateOrUpdateRating(rating); err != nil {
		common.ApiError(c, err)
		return
	}

	// 重新计算平均评分
	newRating, newCount, err := model.RecalculatePoolRating(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateFreeSourcePoolRating(id, newRating, newCount); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"rating": newRating, "rating_count": newCount}})
}

// ReviewFreeSourcePool POST /api/free_source_pools/:id/review — 评论（仅评论，不评分）。
func ReviewFreeSourcePool(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	var req struct {
		Comment string `json:"comment"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Comment) == "" {
		common.ApiErrorMsg(c, "comment is required")
		return
	}

	pool, err := model.GetFreeSourcePoolById(id)
	if err != nil || pool == nil {
		common.ApiErrorMsg(c, "pool not found")
		return
	}

	userId := c.GetInt("id")
	rating := &model.FreeSourcePoolRating{
		PoolId:  id,
		UserId:  userId,
		Rating:  0, // 仅评论，不评分
		Comment: strings.TrimSpace(req.Comment),
	}
	// 仅评论不评分，rating=0 不参与平均计算
	// 直接创建，不更新 pool.rating（只有 rating>0 才更新）
	if err := model.DB.Create(rating).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetFreeSourcePoolReviews GET /api/free_source_pools/:id/reviews — 评论列表。
func GetFreeSourcePoolReviews(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	var reviews []model.FreeSourcePoolRating
	err = model.DB.Where("pool_id = ? AND comment != ''", id).Order("created_at desc").Find(&reviews).Error
	if err != nil {
		common.ApiError(c, err)
		return
	}
	out := make([]gin.H, 0, len(reviews))
	for _, r := range reviews {
		username, _ := model.GetUsernameById(r.UserId, true)
		out = append(out, gin.H{
			"id":         r.Id,
			"user":       gin.H{"id": r.UserId, "name": username},
			"rating":     r.Rating,
			"comment":    r.Comment,
			"created_at": r.CreatedAt.Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// DeleteFreeSourcePool DELETE /api/free_source_pools/:id — 删除（仅管理员或提交者）。
func DeleteFreeSourcePool(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	pool, err := model.GetFreeSourcePoolById(id)
	if err != nil || pool == nil {
		common.ApiErrorMsg(c, "pool not found")
		return
	}
	userId := c.GetInt("id")
	role := c.GetInt("role")
	if pool.SubmitterId != userId && role < 100 { // 100=admin
		common.ApiErrorMsg(c, "permission denied")
		return
	}
	if err := model.DeleteFreeSourcePool(id); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ToggleFreeSourcePoolRecommended POST /api/free_source_pools/:id/recommended — 管理员推荐/取消推荐。
func ToggleFreeSourcePoolRecommended(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	role := c.GetInt("role")
	if role < 100 { // 仅管理员
		common.ApiErrorMsg(c, "admin only")
		return
	}
	pool, err := model.GetFreeSourcePoolById(id)
	if err != nil || pool == nil {
		common.ApiErrorMsg(c, "pool not found")
		return
	}
	newVal := !pool.Recommended
	if err := model.DB.Model(&model.FreeSourcePool{}).Where("id = ?", id).Update("recommended", newVal).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"recommended": newVal}})
}

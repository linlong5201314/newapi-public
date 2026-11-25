// free_source_pool.go 公共免费模型提交池 API。
package controller

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func validateFreeSourceInput(name, rawURL, signupURL, homepageURL, description, quota, proxy, apiBase string, models, tags []string) error {
	if len([]rune(name)) > 200 || len([]rune(description)) > 4000 || len([]rune(quota)) > 200 || len(models) > 100 || len(tags) > 20 {
		return errors.New("free source fields exceed the allowed size")
	}
	for _, item := range []string{rawURL, signupURL, homepageURL, apiBase} {
		if item == "" {
			continue
		}
		parsed, err := url.Parse(item)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || len(item) > 500 {
			return errors.New("free source URLs must be valid HTTP(S) URLs")
		}
	}
	if len(proxy) > 300 {
		return errors.New("proxy value is too long")
	}
	return nil
}

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
			"quota":        p.Quota,
			"proxy":        maskProxyUrl(p.Proxy),
			"api_base":     p.ApiBase,
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
		"quota":        p.Quota,
		"proxy":        maskProxyUrl(p.Proxy),
		"api_base":     p.ApiBase,
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
		Quota       string   `json:"quota"`
		Proxy       string   `json:"proxy"`
		ApiBase     string   `json:"api_base"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Url) == "" {
		common.ApiErrorMsg(c, "name and url are required")
		return
	}
	if err := validateFreeSourceInput(strings.TrimSpace(req.Name), strings.TrimSpace(req.Url), strings.TrimSpace(req.SignupUrl), strings.TrimSpace(req.HomepageUrl), strings.TrimSpace(req.Description), strings.TrimSpace(req.Quota), strings.TrimSpace(req.Proxy), strings.TrimSpace(req.ApiBase), req.Models, req.Tags); err != nil {
		common.ApiErrorMsg(c, "Invalid free source fields")
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
		Quota:         strings.TrimSpace(req.Quota),
		Proxy:         strings.TrimSpace(req.Proxy),
		ApiBase:       strings.TrimSpace(req.ApiBase),
		SubmitterId:   userId,
		SubmitterName: username,
	}
	if err := model.CreateFreeSourcePool(pool); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"id": pool.Id}})
}

// UpdateFreeSourcePool PATCH /api/free_source_pools/:id — 编辑条目。
// 权限：提交者本人或管理员；只更新请求里出现的字段（额度/代理/接入地址也可改）。
func UpdateFreeSourcePool(c *gin.Context) {
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
	if pool.SubmitterId != userId && role < common.RoleAdminUser {
		common.ApiErrorMsg(c, "permission denied")
		return
	}

	var req struct {
		Name        *string   `json:"name"`
		Url         *string   `json:"url"`
		SignupUrl   *string   `json:"signup_url"`
		HomepageUrl *string   `json:"homepage_url"`
		Models      *[]string `json:"models"`
		Description *string   `json:"description"`
		Tags        *[]string `json:"tags"`
		Quota       *string   `json:"quota"`
		Proxy       *string   `json:"proxy"`
		ApiBase     *string   `json:"api_base"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}

	updates := map[string]any{}
	name := pool.Name
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	rawURL := pool.Url
	if req.Url != nil {
		rawURL = strings.TrimSpace(*req.Url)
	}
	signupURL := pool.SignupUrl
	if req.SignupUrl != nil {
		signupURL = strings.TrimSpace(*req.SignupUrl)
	}
	homepageURL := pool.HomepageUrl
	if req.HomepageUrl != nil {
		homepageURL = strings.TrimSpace(*req.HomepageUrl)
	}
	description := pool.Description
	if req.Description != nil {
		description = strings.TrimSpace(*req.Description)
	}
	quota := pool.Quota
	if req.Quota != nil {
		quota = strings.TrimSpace(*req.Quota)
	}
	proxy := pool.Proxy
	if req.Proxy != nil {
		proxy = strings.TrimSpace(*req.Proxy)
	}
	apiBase := pool.ApiBase
	if req.ApiBase != nil {
		apiBase = strings.TrimSpace(*req.ApiBase)
	}
	models := pool.Models
	if req.Models != nil {
		models = *req.Models
	}
	tags := pool.Tags
	if req.Tags != nil {
		tags = *req.Tags
	}
	if err := validateFreeSourceInput(name, rawURL, signupURL, homepageURL, description, quota, proxy, apiBase, models, tags); err != nil || name == "" || rawURL == "" {
		common.ApiErrorMsg(c, "Invalid free source fields")
		return
	}
	if req.Name != nil {
		updates["name"] = name
	}
	if req.Url != nil {
		updates["url"] = rawURL
	}
	if req.SignupUrl != nil {
		updates["signup_url"] = signupURL
	}
	if req.HomepageUrl != nil {
		updates["homepage_url"] = homepageURL
	}
	if req.Models != nil {
		updates["models"] = models
	}
	if req.Description != nil {
		updates["description"] = description
	}
	if req.Tags != nil {
		updates["tags"] = tags
	}
	if req.Quota != nil {
		updates["quota"] = quota
	}
	if req.Proxy != nil {
		updates["proxy"] = proxy
	}
	if req.ApiBase != nil {
		updates["api_base"] = apiBase
	}
	if len(updates) == 0 {
		common.ApiErrorMsg(c, "nothing to update")
		return
	}
	if err := model.UpdateFreeSourcePool(id, updates); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
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
	if len([]rune(req.Comment)) > 2000 {
		common.ApiErrorMsg(c, "comment is too long")
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
	page, pageErr := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, sizeErr := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageErr != nil || sizeErr != nil || page < 1 || pageSize < 1 || pageSize > 100 {
		common.ApiErrorMsg(c, "invalid review pagination")
		return
	}
	var reviews []model.FreeSourcePoolRating
	err = model.DB.Where("pool_id = ? AND comment != ''", id).Order("created_at desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&reviews).Error
	if err != nil {
		common.ApiError(c, err)
		return
	}
	out := make([]gin.H, 0, len(reviews))
	userIDs := make([]int, 0, len(reviews))
	for _, review := range reviews {
		userIDs = append(userIDs, review.UserId)
	}
	var users []model.User
	if len(userIDs) > 0 {
		if err := model.DB.Select("id,username").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	}
	usernames := make(map[int]string, len(users))
	for _, user := range users {
		usernames[user.Id] = user.Username
	}
	for _, r := range reviews {
		username := usernames[r.UserId]
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
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("pool_id = ?", id).Delete(&model.FreeSourcePoolRating{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.FreeSourcePool{}, id).Error
	}); err != nil {
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

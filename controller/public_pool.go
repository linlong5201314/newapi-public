package controller

import (
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// 公共池（公开出来的自建渠道）
//
// 蓝图：自建渠道默认私有；owner 勾选"公开到公共池"后，其他用户可路由，
// 并且能看到公开者署名（昵称+头像）、实测延迟与评论。
//   GET  /api/public_pool/models            按模型聚合的公共池清单
//   GET  /api/public_pool/comments?model=   某模型的评论
//   POST /api/public_pool/comments          发表评论（需登录）
// ---------------------------------------------------------------------------

type publicOwner struct {
	UserId int    `json:"user_id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

type publicModelAgg struct {
	Model     string              `json:"model"`
	Providers int                 `json:"providers"`
	FreeCount int                 `json:"free_count"`
	FastestMs int                 `json:"fastest_ms"`
	Rating    float64             `json:"rating"`
	Comments  int                 `json:"comments"`
	Owners    []publicOwner       `json:"owners"`
	Channels  []publicChannelView `json:"channels"`
}

type publicChannelView struct {
	ChannelId    int    `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	OwnerName    string `json:"owner_name"`
	OwnerAvatar  string `json:"owner_avatar"`
	Tier         string `json:"tier"`
	ResponseTime int    `json:"response_time"`
}

// GetPublicPoolModels GET /api/public_pool/models
func GetPublicPoolModels(c *gin.Context) {
	briefs, err := model.ListPublicChannels()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	byModel := make(map[string]*publicModelAgg)
	ownerSeen := make(map[string]map[int]bool)
	for _, b := range briefs {
		agg, ok := byModel[b.Model]
		if !ok {
			agg = &publicModelAgg{Model: b.Model, FastestMs: -1}
			byModel[b.Model] = agg
			ownerSeen[b.Model] = make(map[int]bool)
		}
		agg.Providers++
		if strings.EqualFold(b.Tier, "free") {
			agg.FreeCount++
		}
		if b.ResponseTime > 0 && (agg.FastestMs < 0 || b.ResponseTime < agg.FastestMs) {
			agg.FastestMs = b.ResponseTime
		}
		agg.Rating = b.Rating
		agg.Comments = b.Comments
		if !ownerSeen[b.Model][b.OwnerId] {
			ownerSeen[b.Model][b.OwnerId] = true
			agg.Owners = append(agg.Owners, publicOwner{UserId: b.OwnerId, Name: b.OwnerName, Avatar: b.OwnerAvatar})
		}
		agg.Channels = append(agg.Channels, publicChannelView{
			ChannelId:    b.ChannelId,
			ChannelName:  b.ChannelName,
			OwnerName:    b.OwnerName,
			OwnerAvatar:  b.OwnerAvatar,
			Tier:         b.Tier,
			ResponseTime: b.ResponseTime,
		})
	}
	items := make([]*publicModelAgg, 0, len(byModel))
	for _, agg := range byModel {
		// 多人公开同一模型时，快的那台排前面（前端直接按这个顺序展示，不用再排）
		sort.Slice(agg.Channels, func(i, j int) bool {
			ai, aj := agg.Channels[i].ResponseTime, agg.Channels[j].ResponseTime
			if ai <= 0 {
				return false
			}
			if aj <= 0 {
				return true
			}
			return ai < aj
		})
		items = append(items, agg)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Providers != items[j].Providers {
			return items[i].Providers > items[j].Providers
		}
		return items[i].Model < items[j].Model
	})
	common.ApiSuccess(c, gin.H{"items": items, "total": len(items)})
}

// GetPublicPoolComments GET /api/public_pool/comments?model=&limit=
func GetPublicPoolComments(c *gin.Context) {
	rows, err := model.ListPublicComments(c.Query("model"), parseLimitOrDefault(c.Query("limit"), 50))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows, "total": len(rows)})
}

// AddPublicPoolComment POST /api/public_pool/comments
// body: {"model":"gpt-4o","channel_id":12,"content":"…","rating":5}
func AddPublicPoolComment(c *gin.Context) {
	uid := c.GetInt("id")
	req := struct {
		Model     string `json:"model"`
		ChannelId int    `json:"channel_id"`
		Content   string `json:"content"`
		Rating    int    `json:"rating"`
	}{}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "Invalid parameters")
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	req.Content = strings.TrimSpace(req.Content)
	if req.Model == "" || req.Content == "" {
		common.ApiErrorMsg(c, "model and content are required")
		return
	}
	if len([]rune(req.Content)) > 500 {
		common.ApiErrorMsg(c, "content too long (max 500)")
		return
	}
	if req.Rating < 0 || req.Rating > 5 {
		req.Rating = 0
	}
	// 评论必须挂在真实存在的公开渠道上，避免伪造 channel_id 蹭署名
	channel, err := model.GetChannelById(req.ChannelId, true)
	if err != nil || channel == nil || channel.Visibility != 1 {
		common.ApiErrorMsg(c, "channel is not public")
		return
	}
	user, err := model.GetUserById(uid, false)
	if err != nil || user == nil {
		common.ApiErrorMsg(c, "user not found")
		return
	}
	row := &model.PublicPoolComment{
		Model:       req.Model,
		ChannelId:   req.ChannelId,
		ChannelName: channel.Name,
		UserId:      uid,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Avatar:      user.Avatar,
		Content:     req.Content,
		Rating:      req.Rating,
	}
	if err := model.AddPublicComment(row); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, row)
}

// parseLimitOrDefault 评论分页参数：非法值一律回落到默认，不让前端把 limit 传成天文数字。
func parseLimitOrDefault(raw string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
		return n
	}
	return def
}

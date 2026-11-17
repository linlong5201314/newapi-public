package model

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"
)

// PublicPoolComment 公共池模型评论。
// 存的是评论时的昵称/头像快照：用户后来改名或换头像，历史评论的署名不会跟着变，
// 也避免每次列表都多一次 users 表联查。
type PublicPoolComment struct {
	Id          int    `json:"id" gorm:"primaryKey"`
	Model       string `json:"model" gorm:"type:varchar(128);index:idx_ppc_model"`
	ChannelId   int    `json:"channel_id" gorm:"index"`
	ChannelName string `json:"channel_name" gorm:"type:varchar(128)"`
	UserId      int    `json:"user_id" gorm:"index"`
	Username    string `json:"username" gorm:"type:varchar(64)"`
	DisplayName string `json:"display_name" gorm:"type:varchar(64)"`
	Avatar      string `json:"avatar" gorm:"type:varchar(255)"`
	Content     string `json:"content" gorm:"type:text"`
	Rating      int    `json:"rating" gorm:"default:0"`
	CreatedAt   int64  `json:"created_at" gorm:"index"`
}

func (PublicPoolComment) TableName() string { return "public_pool_comments" }

// PublicChannelBrief 公共池里的单个渠道摘要，按模型展开后给前端聚合。
type PublicChannelBrief struct {
	ChannelId    int     `json:"channel_id"`
	ChannelName  string  `json:"channel_name"`
	OwnerId      int     `json:"owner_id"`
	OwnerName    string  `json:"owner_name"`
	OwnerAvatar  string  `json:"owner_avatar"`
	Tier         string  `json:"tier"`
	ResponseTime int     `json:"response_time"`
	Model        string  `json:"model"`
	Rating       float64 `json:"rating"`
	Comments     int     `json:"comments"`
}

// ListPublicChannels 列出所有公开到公共池的自建渠道，并按渠道的 models（或 other_info.publicModels 子集）展开。
func ListPublicChannels() ([]PublicChannelBrief, error) {
	type row struct {
		ChannelId    int
		ChannelName  string
		OwnerId      int
		Tier         string
		ResponseTime int
		Models       string
		OtherInfo    string
		Username     string
		DisplayName  string
		Avatar       string
	}
	var rows []row
	err := DB.Raw(`
		select c.id as channel_id, c.name as channel_name, c.owner_id, c.tier,
		       c.response_time, c.models, coalesce(c.other_info, '') as other_info,
		       coalesce(u.username, '') as username,
		       coalesce(u.display_name, '') as display_name,
		       coalesce(u.avatar, '') as avatar
		  from channels c
		  left join users u on u.id = c.owner_id
		 where c.visibility = 1 and c.status = 1
		 order by c.response_time asc, c.id asc`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	ratingMap, countMap := publicCommentStats()
	out := make([]PublicChannelBrief, 0, len(rows))
	for _, r := range rows {
		ownerName := strings.TrimSpace(r.DisplayName)
		if ownerName == "" {
			ownerName = r.Username
		}
		publicSubset := parsePublicModelsSubset(r.OtherInfo)
		for _, m := range strings.Split(r.Models, ",") {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			if len(publicSubset) > 0 && !publicSubset[m] {
				continue
			}
			brief := PublicChannelBrief{
				ChannelId:    r.ChannelId,
				ChannelName:  r.ChannelName,
				OwnerId:      r.OwnerId,
				OwnerName:    ownerName,
				OwnerAvatar:  r.Avatar,
				Tier:         r.Tier,
				ResponseTime: r.ResponseTime,
				Model:        m,
			}
			if v, ok := ratingMap[m]; ok {
				brief.Rating = v
			}
			brief.Comments = countMap[m]
			out = append(out, brief)
		}
	}
	return out, nil
}

func parsePublicModelsSubset(otherInfoRaw string) map[string]bool {
	trimmed := strings.TrimSpace(otherInfoRaw)
	if trimmed == "" || !strings.Contains(trimmed, "publicModels") {
		return nil
	}
	var info struct {
		PublicModels []string `json:"publicModels"`
	}
	if err := json.Unmarshal([]byte(trimmed), &info); err != nil || len(info.PublicModels) == 0 {
		return nil
	}
	set := make(map[string]bool, len(info.PublicModels))
	for _, item := range info.PublicModels {
		if v := strings.TrimSpace(item); v != "" {
			set[v] = true
		}
	}
	return set
}

// publicCommentStats 按模型统计平均评分与评论数（一次查询，避免 N+1）。
func publicCommentStats() (map[string]float64, map[string]int) {
	type stat struct {
		Model string
		Avg   float64
		N     int
	}
	var stats []stat
	DB.Raw(`select model, coalesce(avg(rating), 0) as avg, count(*) as n
	          from public_pool_comments group by model`).Scan(&stats)
	rating := make(map[string]float64, len(stats))
	count := make(map[string]int, len(stats))
	for _, s := range stats {
		rating[s.Model] = s.Avg
		count[s.Model] = s.N
	}
	return rating, count
}

// ListPublicComments 拉某个模型的评论，新的在前。
func ListPublicComments(modelName string, limit int) ([]PublicPoolComment, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []PublicPoolComment
	q := DB.Order("id desc").Limit(limit)
	if strings.TrimSpace(modelName) != "" {
		q = q.Where("model = ?", modelName)
	}
	err := q.Find(&rows).Error
	return rows, err
}

// AddPublicComment 落一条评论。
func AddPublicComment(row *PublicPoolComment) error {
	row.CreatedAt = time.Now().Unix()
	return DB.Create(row).Error
}

// SetSelfChannelVisibility 更新自建渠道的可见性与层级。
// 只允许 owner 自己改：where 带上 owner_id，别人的渠道改不动（也改不到平台渠道）。
func SetSelfChannelVisibility(ownerId int, channelId int, visibility int, tier string) error {
	if ownerId <= 0 || channelId <= 0 {
		return errors.New("invalid owner or channel")
	}
	res := DB.Model(&Channel{}).
		Where("id = ? AND owner_id = ?", channelId, ownerId).
		Updates(map[string]any{"visibility": visibility, "tier": tier})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("channel not found or not owned by you")
	}
	return nil
}

// IpAllowedByRules 判断来源 IP 是否命中白名单规则。
// 规则支持逗号/分号/空白/换行分隔，可混用精确 IP 与 CIDR；空规则表示不限制来源。
func IpAllowedByRules(rules string, ip string) bool {
	rules = strings.TrimSpace(rules)
	if rules == "" {
		return true
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	fields := strings.FieldsFunc(rules, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	for _, raw := range fields {
		rule := strings.TrimSpace(raw)
		if rule == "" {
			continue
		}
		if strings.Contains(rule, "/") {
			if _, ipnet, err := net.ParseCIDR(rule); err == nil && ipnet.Contains(parsed) {
				return true
			}
			continue
		}
		if target := net.ParseIP(rule); target != nil && target.Equal(parsed) {
			return true
		}
	}
	return false
}

// SetSelfChannelAllowIps 更新自建渠道的免费层订阅 IP 白名单（只能改自己的渠道）。
func SetSelfChannelAllowIps(ownerId int, channelId int, allowIps string) error {
	res := DB.Model(&Channel{}).
		Where("id = ? AND owner_id = ?", channelId, ownerId).
		Update("allow_ips", allowIps)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("channel not found or not owned by you")
	}
	return nil
}

// UpdateUserAvatar 只更新头像字段（公共池署名用）。
func UpdateUserAvatar(uid int, avatar string) error {
	return DB.Model(&User{}).Where("id = ?", uid).Update("avatar", avatar).Error
}

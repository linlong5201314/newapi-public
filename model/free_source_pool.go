// FreeSourcePool 公共免费模型提交池。
// 用户提交发现的免费模型/源，全体用户可见，支持评分/评论/推荐排序。
package model

import (
	"time"

	"gorm.io/gorm"
)

// FreeSourcePool 公共免费模型池条目。
type FreeSourcePool struct {
	Id            int       `json:"id" gorm:"primaryKey;autoIncrement"`
	Name          string    `json:"name" gorm:"type:varchar(200);not null"`    // 名称（如 "Groq 免费额度"）
	Url           string    `json:"url" gorm:"type:varchar(500);not null"`     // 来源 URL
	SignupUrl     string    `json:"signup_url" gorm:"type:varchar(500)"`       // 注册/申请 key 页面
	HomepageUrl   string    `json:"homepage_url" gorm:"type:varchar(500)"`     // 官网主页（可选）
	Models        []string  `json:"models" gorm:"serializer:json;type:text"`   // 可用模型列表（JSON 序列化，三库通用）
	Description   string    `json:"description" gorm:"type:text"`              // 描述
	Tags          []string  `json:"tags" gorm:"serializer:json;type:text"`     // 标签：完全免费/额度限制/签到/推荐/不推荐
	Rating        float64   `json:"rating" gorm:"type:decimal(3,2);default:0"` // 平均评分（0-5）
	RatingCount   int       `json:"rating_count" gorm:"default:0"`             // 评分人数
	Recommended   bool      `json:"recommended" gorm:"default:false"`          // 是否推荐（高评分/人工置顶）
	SubmitterId   int       `json:"submitter_user_id" gorm:"not null"`         // 提交者 user_id
	SubmitterName string    `json:"submitter_name" gorm:"type:varchar(64)"`    // 提交者 username（冗余，避免 join）
	CreatedAt     time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt     time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (FreeSourcePool) TableName() string { return "free_source_pools" }

// GetFreeSourcePools 查询全部公共免费模型池（按评分降序，可选按 rating_count 排序）。
func GetFreeSourcePools(sortBy string) ([]FreeSourcePool, error) {
	var list []FreeSourcePool
	query := DB.Order("rating desc").Order("rating_count desc")
	if sortBy == "rating" {
		query = DB.Order("rating desc")
	} else if sortBy == "newest" {
		query = DB.Order("created_at desc")
	}
	err := query.Find(&list).Error
	return list, err
}

// CreateFreeSourcePool 新建提交。
func CreateFreeSourcePool(p *FreeSourcePool) error {
	return DB.Create(p).Error
}

// GetFreeSourcePoolById 按 id 查询。
func GetFreeSourcePoolById(id int) (*FreeSourcePool, error) {
	var p FreeSourcePool
	err := DB.First(&p, id).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateFreeSourcePoolRating 更新评分（重新计算平均值）。
func UpdateFreeSourcePoolRating(id int, newRating float64, newCount int) error {
	return DB.Model(&FreeSourcePool{}).Where("id = ?", id).Updates(map[string]interface{}{
		"rating":       newRating,
		"rating_count": newCount,
		"updated_at":   time.Now(),
	}).Error
}

// DeleteFreeSourcePool 删除（仅管理员）。
func DeleteFreeSourcePool(id int) error {
	return DB.Delete(&FreeSourcePool{}, id).Error
}

// FreeSourcePoolRating 评分记录（一个用户只能对同一条目评分一次）。
type FreeSourcePoolRating struct {
	Id        int       `json:"id" gorm:"primaryKey;autoIncrement"`
	PoolId    int       `json:"pool_id" gorm:"not null;index:idx_pool_user"`
	UserId    int       `json:"user_id" gorm:"not null;index:idx_pool_user"`
	Rating    int       `json:"rating" gorm:"not null"`   // 1-5 星
	Comment   string    `json:"comment" gorm:"type:text"` // 评论内容（可选）
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (FreeSourcePoolRating) TableName() string { return "free_source_pool_ratings" }

// GetUserRatingForPool 查询某用户对某条目的评分（判断重复评分）。
func GetUserRatingForPool(poolId int, userId int) (*FreeSourcePoolRating, error) {
	var r FreeSourcePoolRating
	err := DB.Where("pool_id = ? AND user_id = ?", poolId, userId).First(&r).Error
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// CreateOrUpdateRating 创建或更新评分。
func CreateOrUpdateRating(r *FreeSourcePoolRating) error {
	existing, err := GetUserRatingForPool(r.PoolId, r.UserId)
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	if existing != nil {
		// 更新
		return DB.Model(&FreeSourcePoolRating{}).Where("id = ?", existing.Id).Updates(map[string]interface{}{
			"rating":  r.Rating,
			"comment": r.Comment,
		}).Error
	}
	// 创建
	return DB.Create(r).Error
}

// RecalculatePoolRating 重新计算某条目的平均评分和总数。
func RecalculatePoolRating(poolId int) (float64, int, error) {
	var result struct {
		AvgRating float64 `gorm:"column:avg_rating"`
		Count     int     `gorm:"column:count"`
	}
	err := DB.Model(&FreeSourcePoolRating{}).
		Select("COALESCE(AVG(rating), 0) as avg_rating, COUNT(*) as count").
		Where("pool_id = ?", poolId).
		Scan(&result).Error
	if err != nil {
		return 0, 0, err
	}
	return result.AvgRating, result.Count, nil
}

package model

import (
	"time"
)

// SelfOAuthSession 浏览器授权会话（小浣熊等走「授权页 → 回调」流程的 provider 共用）。
//
// 为什么要落库而不是放内存：授权链路经过 gw.ailong.love 前置负载均衡，
// start 可能落在 A 节点、浏览器回调落在 B 节点。进程内 map 会让 B 节点
// 找不到 state 而报「授权会话不存在」。落到共享库后任意节点都能接住。
type SelfOAuthSession struct {
	State     string    `json:"state" gorm:"primaryKey;type:varchar(80)"`
	Provider  string    `json:"provider" gorm:"type:varchar(32);not null;default:''"`
	Status    string    `json:"status" gorm:"type:varchar(16);not null;default:'pending'"` // pending / done / failed
	Access    string    `json:"-" gorm:"type:text"`
	Refresh   string    `json:"-" gorm:"type:text"`
	AccountID string    `json:"account_id" gorm:"type:varchar(120)"`
	Phone     string    `json:"phone" gorm:"type:varchar(64)"`
	Err       string    `json:"err" gorm:"type:text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (SelfOAuthSession) TableName() string {
	return "self_oauth_sessions"
}

// CreateSelfOAuthSession 新建会话（同 state 覆盖，避免残留）。
func CreateSelfOAuthSession(s *SelfOAuthSession) error {
	now := time.Now()
	s.CreatedAt = now
	s.UpdatedAt = now
	if s.Status == "" {
		s.Status = "pending"
	}
	return DB.Save(s).Error
}

// GetSelfOAuthSession 读会话，超过 ttl 视为不存在（顺手删掉）。
func GetSelfOAuthSession(state string, ttl time.Duration) (*SelfOAuthSession, error) {
	var s SelfOAuthSession
	if err := DB.Where("state = ?", state).First(&s).Error; err != nil {
		return nil, err
	}
	if ttl > 0 && time.Since(s.CreatedAt) > ttl {
		DB.Where("state = ?", state).Delete(&SelfOAuthSession{})
		return nil, nil
	}
	return &s, nil
}

// UpdateSelfOAuthSession 按 state 更新指定列。
func UpdateSelfOAuthSession(state string, updates map[string]interface{}) error {
	updates["updated_at"] = time.Now()
	return DB.Model(&SelfOAuthSession{}).Where("state = ?", state).Updates(updates).Error
}

// PurgeExpiredSelfOAuthSessions 清理过期会话，避免表无限增长。
func PurgeExpiredSelfOAuthSessions(ttl time.Duration) {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	DB.Where("created_at < ?", time.Now().Add(-ttl)).Delete(&SelfOAuthSession{})
}

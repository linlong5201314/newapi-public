// checkin_account.go M3 签到中心数据模型：CheckinAccount（签到账号）+ CheckinLog（执行日志）。
// 与既有用户额度签到（Checkin / checkins 表）是不同功能，独立建表。
package model

import (
	"github.com/QuantumNous/new-api/common"
)

// CheckinAccount 签到账号（每用户自助登记的外部站点签到凭据）。
type CheckinAccount struct {
	Id             int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId         int    `json:"user_id" gorm:"not null;index:idx_checkin_accounts_user"`
	Site           string `json:"site" gorm:"type:varchar(32);not null;index"` // workbuddy | trae | apinex | custom
	Label          string `json:"label" gorm:"type:varchar(128);default:''"`
	ChannelId      int    `json:"channel_id" gorm:"default:0"`                   // 引用自有渠道（workbuddy/trae），0=独立凭据
	CredJson       string `json:"cred_json" gorm:"type:text"`                    // 独立凭据（apinex cookie / custom 配置），M5 统一加密
	Mode           string `json:"mode" gorm:"type:varchar(16);default:'api'"`    // api | agent
	AgentUrl       string `json:"agent_url" gorm:"type:varchar(512);default:''"` // mode=agent 时执行器地址
	Enabled        bool   `json:"enabled" gorm:"default:true"`
	LastStatus     string `json:"last_status" gorm:"type:varchar(32);default:''"`      // ok | failed | skipped
	LastResult     string `json:"last_result" gorm:"type:varchar(512);default:''"`     // 最近一次结果摘要
	Streak         int    `json:"streak" gorm:"default:0"`                             // 连续签到天数（站点支持时更新）
	TotalReward    int64  `json:"total_reward" gorm:"bigint;default:0"`                // 累计积分/额度
	LastCheckinDay string `json:"last_checkin_day" gorm:"type:varchar(10);default:''"` // CST 日期，判重
	FailCount      int    `json:"fail_count" gorm:"default:0"`                         // 当日失败次数（补跑 ≤2 次判据）
	CreatedTime    int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime    int64  `json:"updated_time" gorm:"bigint"`
}

func (CheckinAccount) TableName() string {
	return "checkin_accounts"
}

// CheckinLog 签到执行日志（每次动作一条）。
type CheckinLog struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId      int    `json:"user_id" gorm:"not null;index"`
	AccountId   int    `json:"account_id" gorm:"not null;index:idx_checkin_logs_account"`
	Site        string `json:"site" gorm:"type:varchar(32);not null"`
	Action      string `json:"action" gorm:"type:varchar(64);default:''"` // checkin | claim | task | balance | agent ...
	Success     bool   `json:"success"`
	Detail      string `json:"detail" gorm:"type:text"`
	CreatedTime int64  `json:"created_time" gorm:"bigint"`
}

func (CheckinLog) TableName() string {
	return "checkin_logs"
}

// ---------------------------------------------------------------------------
// CRUD（全部强制 owner）
// ---------------------------------------------------------------------------

// CreateCheckinAccount 新建签到账号。
func CreateCheckinAccount(acc *CheckinAccount) error {
	acc.CreatedTime = common.GetTimestamp()
	acc.UpdatedTime = acc.CreatedTime
	return DB.Create(acc).Error
}

// GetCheckinAccounts 列出用户全部签到账号。
func GetCheckinAccounts(userId int) ([]CheckinAccount, error) {
	var list []CheckinAccount
	err := DB.Where("user_id = ?", userId).Order("id desc").Find(&list).Error
	return list, err
}

// GetCheckinAccount 取单个账号（强制 owner）。
func GetCheckinAccount(userId, id int) (*CheckinAccount, error) {
	var acc CheckinAccount
	err := DB.Where("id = ? AND user_id = ?", id, userId).First(&acc).Error
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

// CountCheckinAccounts 统计用户签到账号数（上限校验用）。
func CountCheckinAccounts(userId int) (int64, error) {
	var count int64
	err := DB.Model(&CheckinAccount{}).Where("user_id = ?", userId).Count(&count).Error
	return count, err
}

// UpdateCheckinAccount 更新签到账号（仅允许业务字段；owner 不可变）。
func UpdateCheckinAccount(acc *CheckinAccount) error {
	acc.UpdatedTime = common.GetTimestamp()
	return DB.Model(acc).Select(
		"label", "channel_id", "cred_json", "mode", "agent_url", "enabled", "updated_time",
	).Updates(acc).Error
}

// SaveCheckinRunResult 保存一次执行结果（状态/摘要/连签/累计奖励/判重日期/失败计数）。
func SaveCheckinRunResult(acc *CheckinAccount) error {
	acc.UpdatedTime = common.GetTimestamp()
	return DB.Model(acc).Select(
		"last_status", "last_result", "streak", "total_reward", "last_checkin_day", "fail_count", "updated_time",
	).Updates(acc).Error
}

// UpdateCheckinAccountCred 单独更新凭据（agent 模式 cookie 续期回写用）。
// 入参为明文，落库前统一加密（M5）；强制 owner。
func UpdateCheckinAccountCred(userId, id int, plainCred string) error {
	return DB.Model(&CheckinAccount{}).
		Where("id = ? AND user_id = ?", id, userId).
		Updates(map[string]any{
			"cred_json":    EncryptCheckinCred(plainCred),
			"updated_time": common.GetTimestamp(),
		}).Error
}

// DeleteCheckinAccount 删除签到账号（强制 owner）。
func DeleteCheckinAccount(userId, id int) error {
	return DB.Where("id = ? AND user_id = ?", id, userId).Delete(&CheckinAccount{}).Error
}

// GetEnabledCheckinAccounts 调度器 sweep 用：全部 enabled 账号（分页防止大表一次拉爆）。
func GetEnabledCheckinAccounts(offset, limit int) ([]CheckinAccount, error) {
	var list []CheckinAccount
	err := DB.Where("enabled = ?", true).Order("id").Offset(offset).Limit(limit).Find(&list).Error
	return list, err
}

// InsertCheckinLog 写一条执行日志。
func InsertCheckinLog(log *CheckinLog) error {
	log.CreatedTime = common.GetTimestamp()
	return DB.Create(log).Error
}

// GetCheckinLogs 取账号的执行日志（强制 owner，倒序分页）。
func GetCheckinLogs(userId, accountId, offset, limit int) ([]CheckinLog, int64, error) {
	var (
		list  []CheckinLog
		total int64
	)
	q := DB.Model(&CheckinLog{}).Where("account_id = ? AND user_id = ?", accountId, userId)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Order("id desc").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

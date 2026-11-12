// ProxyPool 用户出口 IP 池。
// 每个用户可配置多个出口节点（机场订阅 / 住宅 IP / vless 等），
// 上游请求按节点轮换，从而在按 IP 限流的免费层上放大可用额度。
package model

import (
	"time"

	"gorm.io/gorm"
)

const (
	// ProxyEndpointMaxFailCount 连续失败达到该次数后节点退出可用列表。
	ProxyEndpointMaxFailCount = 5
	// proxyLastErrorMaxLen 与 last_error 字段的 varchar(500) 对齐。
	proxyLastErrorMaxLen = 500
)

// ProxyEndpoint 用户出口节点。
type ProxyEndpoint struct {
	Id             int        `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId         int        `json:"user_id" gorm:"index;not null"`
	Name           string     `json:"name" gorm:"type:varchar(120);not null"`
	Kind           string     `json:"kind" gorm:"type:varchar(20);not null"` // http|https|socks5|socks5h|vless|vmess|trojan|hysteria2|ss
	Url            string     `json:"url" gorm:"type:varchar(1000)"`         // 可直接使用的代理 URL（仅 http/https/socks5/socks5h 有值）
	RawUri         string     `json:"raw_uri" gorm:"type:text"`              // 客户端型节点的原始 URI（vless://... 等）
	Region         string     `json:"region" gorm:"type:varchar(60)"`
	SubscriptionId int        `json:"subscription_id" gorm:"index;default:0"` // 0=手动添加
	Enabled        bool       `json:"enabled" gorm:"default:true"`
	Weight         int        `json:"weight" gorm:"default:1"`
	FailCount      int        `json:"fail_count" gorm:"default:0"`
	LastOkAt       *time.Time `json:"last_ok_at"`
	LastError      string     `json:"last_error" gorm:"type:varchar(500)"`
	CreatedAt      time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (ProxyEndpoint) TableName() string { return "proxy_endpoints" }

// ProxySubscription 用户添加的机场订阅。
type ProxySubscription struct {
	Id         int        `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId     int        `json:"user_id" gorm:"index;not null"`
	Name       string     `json:"name" gorm:"type:varchar(120)"`
	Url        string     `json:"url" gorm:"type:text"`           // 订阅链接（返回给前端时脱敏，只回 host+前缀）
	Format     string     `json:"format" gorm:"type:varchar(20)"` // clash|base64|uri-list|auto
	NodeCount  int        `json:"node_count" gorm:"default:0"`
	LastSyncAt *time.Time `json:"last_sync_at"`
	LastError  string     `json:"last_error" gorm:"type:varchar(500)"`
	Enabled    bool       `json:"enabled" gorm:"default:true"`
	CreatedAt  time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt  time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (ProxySubscription) TableName() string { return "proxy_subscriptions" }

// ListProxyEndpoints 列出某用户的全部出口节点（按 id 升序）。
func ListProxyEndpoints(userId int) ([]ProxyEndpoint, error) {
	var list []ProxyEndpoint
	err := DB.Where("user_id = ?", userId).Order("id").Find(&list).Error
	return list, err
}

// GetProxyEndpoint 按用户隔离读取单个出口节点。
func GetProxyEndpoint(userId, id int) (*ProxyEndpoint, error) {
	var endpoint ProxyEndpoint
	err := DB.Where("user_id = ? AND id = ?", userId, id).First(&endpoint).Error
	if err != nil {
		return nil, err
	}
	return &endpoint, nil
}

// CreateProxyEndpoint 新建出口节点。
func CreateProxyEndpoint(e *ProxyEndpoint) error {
	if err := DB.Create(e).Error; err != nil {
		return err
	}
	// 出口集合变了，轮换缓存必须立刻失效。
	InvalidateProxyPoolCache(e.UserId)
	return nil
}

// CreateProxyEndpoints 批量新建出口节点（订阅同步时避免逐条往返）。
func CreateProxyEndpoints(list []ProxyEndpoint) error {
	if len(list) == 0 {
		return nil
	}
	if err := DB.CreateInBatches(&list, 100).Error; err != nil {
		return err
	}
	InvalidateProxyPoolCache(list[0].UserId)
	return nil
}

// UpdateProxyEndpoint 按用户隔离更新出口节点的指定字段。
func UpdateProxyEndpoint(userId, id int, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	result := DB.Model(&ProxyEndpoint{}).Where("user_id = ? AND id = ?", userId, id).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	InvalidateProxyPoolCache(userId)
	return nil
}

// DeleteProxyEndpoint 按用户隔离删除出口节点。
func DeleteProxyEndpoint(userId, id int) error {
	result := DB.Where("user_id = ? AND id = ?", userId, id).Delete(&ProxyEndpoint{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	InvalidateProxyPoolCache(userId)
	return nil
}

// DeleteProxyEndpointsBySubscription 删除某订阅派生的全部节点（重建订阅节点前调用）。
func DeleteProxyEndpointsBySubscription(userId, subId int) error {
	if err := DB.Where("user_id = ? AND subscription_id = ?", userId, subId).Delete(&ProxyEndpoint{}).Error; err != nil {
		return err
	}
	InvalidateProxyPoolCache(userId)
	return nil
}

// ListProxySubscriptions 列出某用户的全部订阅（按 id 升序）。
func ListProxySubscriptions(userId int) ([]ProxySubscription, error) {
	var list []ProxySubscription
	err := DB.Where("user_id = ?", userId).Order("id").Find(&list).Error
	return list, err
}

// GetProxySubscription 按用户隔离读取单个订阅。
func GetProxySubscription(userId, id int) (*ProxySubscription, error) {
	var sub ProxySubscription
	err := DB.Where("user_id = ? AND id = ?", userId, id).First(&sub).Error
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

// CreateProxySubscription 新建订阅。
func CreateProxySubscription(s *ProxySubscription) error {
	return DB.Create(s).Error
}

// UpdateProxySubscription 按用户隔离更新订阅的指定字段。
func UpdateProxySubscription(userId, id int, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	result := DB.Model(&ProxySubscription{}).Where("user_id = ? AND id = ?", userId, id).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteProxySubscription 按用户隔离删除订阅（其节点由调用方另行清理）。
func DeleteProxySubscription(userId, id int) error {
	result := DB.Where("user_id = ? AND id = ?", userId, id).Delete(&ProxySubscription{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// MarkProxyEndpointResult 记录一次节点可用性探测结果。
// 成功清零失败计数并刷新 last_ok_at；失败累加 fail_count 并记录截断后的错误。
func MarkProxyEndpointResult(id int, ok bool, errMsg string) error {
	now := time.Now()
	if ok {
		return DB.Model(&ProxyEndpoint{}).Where("id = ?", id).Updates(map[string]any{
			"fail_count": 0,
			"last_ok_at": now,
			"last_error": "",
		}).Error
	}
	if len([]rune(errMsg)) > proxyLastErrorMaxLen {
		errMsg = string([]rune(errMsg)[:proxyLastErrorMaxLen])
	}
	return DB.Model(&ProxyEndpoint{}).Where("id = ?", id).Updates(map[string]any{
		"fail_count": gorm.Expr("fail_count + 1"),
		"last_error": errMsg,
	}).Error
}

// CountProxyEndpoints 统计某用户的出口节点总数（用于配额校验）。
func CountProxyEndpoints(userId int) (int, error) {
	var total int64
	if err := DB.Model(&ProxyEndpoint{}).Where("user_id = ?", userId).Count(&total).Error; err != nil {
		return 0, err
	}
	return int(total), nil
}

// CountProxySubscriptions 统计某用户的订阅总数（用于配额校验）。
func CountProxySubscriptions(userId int) (int, error) {
	var total int64
	if err := DB.Model(&ProxySubscription{}).Where("user_id = ?", userId).Count(&total).Error; err != nil {
		return 0, err
	}
	return int(total), nil
}

// ListUsableProxyUrls 返回某用户当前可用于上游轮换的代理 URL。
// 条件：enabled、url 非空、连续失败次数未达上限。
func ListUsableProxyUrls(userId int) ([]string, error) {
	var urls []string
	err := DB.Model(&ProxyEndpoint{}).
		Where("user_id = ? AND enabled = ? AND url <> '' AND fail_count < ?", userId, true, ProxyEndpointMaxFailCount).
		Order("id").
		Pluck("url", &urls).Error
	return urls, err
}

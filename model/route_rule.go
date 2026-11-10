package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 用户级路由规则（Route Rules, M2）
//
// 规则名 = 该用户 API 可调用的虚拟模型名。请求解析 model 后先查该用户
// enabled 规则表，命中则进入策略路由路径（见 service/route_rule.go），
// 未命中走 M1 现有 owner 桶路径。
// 蓝图：docs/M2-路由与免费源设计.md §1。
// ---------------------------------------------------------------------------

// RouteRuleTarget 是 Targets JSON 数组的元素。
// Weight 仅 weighted 策略使用（缺省 1）；ModelOverride 命中后改写上游模型名；
// FallbackOnlyOnQuotaExhaustion=true 的目标只在前一目标报配额耗尽类错误
// （402/429）时才参与选择。
type RouteRuleTarget struct {
	ChannelId                       int    `json:"channelId"`
	Weight                          int    `json:"weight,omitempty"`
	ModelOverride                   string `json:"modelOverride,omitempty"`
	FallbackOnlyOnQuotaExhaustion   bool   `json:"fallbackOnlyOnQuotaExhaustion,omitempty"`
}

// RouteRuleConfig 是 Config JSON 的内容；零值字段取默认值。
type RouteRuleConfig struct {
	MaxRetries      int `json:"maxRetries,omitempty"`      // 同目标瞬时错误重试次数（默认 1）
	RetryDelayMs    int `json:"retryDelayMs,omitempty"`    // 目标间退避（默认 1000ms，0=不等）
	TargetTimeoutMs int `json:"targetTimeoutMs,omitempty"` // 每目标独立超时（默认 30000ms）
}

const (
	RouteRuleDefaultMaxRetries      = 1
	RouteRuleDefaultRetryDelayMs    = 1000
	RouteRuleDefaultTargetTimeoutMs = 30000
	RouteRuleMaxTargets             = 20
)

type RouteRule struct {
	Id          int    `json:"id" gorm:"primaryKey"`
	UserId      int    `json:"user_id" gorm:"not null;uniqueIndex:idx_route_rules_user_name,priority:1;index:idx_route_rules_user"`
	Name        string `json:"name" gorm:"size:64;not null;uniqueIndex:idx_route_rules_user_name,priority:2"`
	Strategy    string `json:"strategy" gorm:"size:32;not null;default:'priority'"`
	Targets     string `json:"targets" gorm:"type:text;not null"`
	Config      string `json:"config" gorm:"type:text"`
	Enabled     bool   `json:"enabled" gorm:"not null;default:true"`
	CreatedTime int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime int64  `json:"updated_time" gorm:"bigint"`
}

func (r *RouteRule) GetTargets() ([]RouteRuleTarget, error) {
	var targets []RouteRuleTarget
	if strings.TrimSpace(r.Targets) == "" {
		return nil, errors.New("route rule targets is empty")
	}
	if err := common.Unmarshal([]byte(r.Targets), &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (r *RouteRule) SetTargets(targets []RouteRuleTarget) error {
	data, err := common.Marshal(targets)
	if err != nil {
		return err
	}
	r.Targets = string(data)
	return nil
}

func (r *RouteRule) GetConfig() RouteRuleConfig {
	cfg := RouteRuleConfig{
		MaxRetries:      RouteRuleDefaultMaxRetries,
		RetryDelayMs:    RouteRuleDefaultRetryDelayMs,
		TargetTimeoutMs: RouteRuleDefaultTargetTimeoutMs,
	}
	if strings.TrimSpace(r.Config) == "" {
		return cfg
	}
	_ = common.Unmarshal([]byte(r.Config), &cfg)
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.RetryDelayMs < 0 {
		cfg.RetryDelayMs = 0
	}
	if cfg.TargetTimeoutMs <= 0 {
		cfg.TargetTimeoutMs = RouteRuleDefaultTargetTimeoutMs
	}
	return cfg
}

// ---------------------------------------------------------------------------
// CRUD
// ---------------------------------------------------------------------------

func GetRouteRulesByUserId(userId int) ([]*RouteRule, error) {
	var rules []*RouteRule
	err := DB.Where("user_id = ?", userId).Order("id asc").Find(&rules).Error
	return rules, err
}

// GetEnabledRouteRulesByUserId 路由热路径使用（service 层再包 60s 缓存）。
func GetEnabledRouteRulesByUserId(userId int) ([]*RouteRule, error) {
	var rules []*RouteRule
	err := DB.Where("user_id = ? AND enabled = ?", userId, true).Order("id asc").Find(&rules).Error
	return rules, err
}

func GetRouteRuleById(id int) (*RouteRule, error) {
	var rule RouteRule
	err := DB.First(&rule, id).Error
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func CountRouteRulesByUserId(userId int) (int64, error) {
	var count int64
	err := DB.Model(&RouteRule{}).Where("user_id = ?", userId).Count(&count).Error
	return count, err
}

func (r *RouteRule) Insert() error {
	now := common.GetTimestamp()
	r.CreatedTime = now
	r.UpdatedTime = now
	return DB.Create(r).Error
}

// Update 全字段更新（不含 Id/UserId，归属禁止漂移）。
func (r *RouteRule) Update() error {
	r.UpdatedTime = common.GetTimestamp()
	return DB.Model(&RouteRule{}).Where("id = ? AND user_id = ?", r.Id, r.UserId).
		Select("name", "strategy", "targets", "config", "enabled", "updated_time").
		Updates(r).Error
}

func (r *RouteRule) Delete() error {
	return DB.Where("id = ? AND user_id = ?", r.Id, r.UserId).Delete(&RouteRule{}).Error
}

func (r *RouteRule) SetEnabled(enabled bool) error {
	r.Enabled = enabled
	r.UpdatedTime = common.GetTimestamp()
	return DB.Model(&RouteRule{}).Where("id = ? AND user_id = ?", r.Id, r.UserId).
		Updates(map[string]any{"enabled": enabled, "updated_time": r.UpdatedTime}).Error
}

// IsRouteRuleDuplicateNameErr 判断唯一索引 (user_id,name) 冲突。
func IsRouteRuleDuplicateNameErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "idx_route_rules_user_name") ||
		(strings.Contains(msg, "unique") && strings.Contains(msg, "route_rules"))
}

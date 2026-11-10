// extlink.go M4 外链模块数据模型（蓝图 docs/M4-外链模块设计.md §1）。
package model

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ExtLink 外部应用/链接。slug 唯一，映射站内二级路径 /app/<slug>。
type ExtLink struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Slug        string `json:"slug" gorm:"uniqueIndex;size:64;not null"`
	Name        string `json:"name" gorm:"size:128;not null"`
	Url         string `json:"url" gorm:"size:1024;not null"`
	Icon        string `json:"icon" gorm:"size:64;default:''"`
	Mode        string `json:"mode" gorm:"size:16;not null;default:'proxy'"` // proxy | iframe | newtab
	Position    string `json:"position" gorm:"size:16;not null;default:'home_card'"` // home_card | sidebar | both
	Enabled     bool   `json:"enabled" gorm:"not null;default:true"`
	SortOrder   int    `json:"sort_order" gorm:"not null;default:0"`
	CreatedTime int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime int64  `json:"updated_time" gorm:"bigint"`
}

func (ExtLink) TableName() string {
	return "ext_links"
}

// ExtLink 合法取值集合。
const (
	ExtLinkModeProxy  = "proxy"
	ExtLinkModeIframe = "iframe"
	ExtLinkModeNewtab = "newtab"

	ExtLinkPositionHomeCard = "home_card"
	ExtLinkPositionSidebar  = "sidebar"
	ExtLinkPositionBoth     = "both"
)

// IsValidExtLinkMode / IsValidExtLinkPosition 供 controller 入参校验。
func IsValidExtLinkMode(mode string) bool {
	switch mode {
	case ExtLinkModeProxy, ExtLinkModeIframe, ExtLinkModeNewtab:
		return true
	}
	return false
}

func IsValidExtLinkPosition(position string) bool {
	switch position {
	case ExtLinkPositionHomeCard, ExtLinkPositionSidebar, ExtLinkPositionBoth:
		return true
	}
	return false
}

// NormalizeExtLinkSlug 统一 slug 形态（小写、去空白、去前后斜杠）。
func NormalizeExtLinkSlug(slug string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(slug)), "/")
}

// CountExtLinks 当前外链总数（配合 ExtLinkMaxCount 上限校验）。
func CountExtLinks() (int64, error) {
	var count int64
	err := DB.Model(&ExtLink{}).Count(&count).Error
	return count, err
}

// GetAllExtLinks 管理端全量列表（按 sort_order, id 升序）。
func GetAllExtLinks() ([]*ExtLink, error) {
	var links []*ExtLink
	err := DB.Order("sort_order asc, id asc").Find(&links).Error
	return links, err
}

// GetEnabledExtLinks 用户端启用列表（仅 enabled=true）。
func GetEnabledExtLinks() ([]*ExtLink, error) {
	var links []*ExtLink
	err := DB.Where("enabled = ?", true).Order("sort_order asc, id asc").Find(&links).Error
	return links, err
}

// GetExtLinkById 主键查询；不存在返回 gorm.ErrRecordNotFound。
func GetExtLinkById(id int) (*ExtLink, error) {
	var link ExtLink
	err := DB.First(&link, id).Error
	if err != nil {
		return nil, err
	}
	return &link, nil
}

// CreateExtLink 落库并刷新 slug 缓存。
func CreateExtLink(link *ExtLink) error {
	now := common.GetTimestamp()
	link.CreatedTime = now
	link.UpdatedTime = now
	if err := DB.Create(link).Error; err != nil {
		return err
	}
	InvalidateExtLinkCache(link.Slug)
	return nil
}

// UpdateExtLink 更新可变字段（slug 变更时新旧 slug 缓存都失效）。
func UpdateExtLink(link *ExtLink) error {
	link.UpdatedTime = common.GetTimestamp()
	if err := DB.Save(link).Error; err != nil {
		return err
	}
	InvalidateExtLinkCache(link.Slug)
	return nil
}

// DeleteExtLink 删除并失效缓存。
func DeleteExtLink(id int) error {
	link, err := GetExtLinkById(id)
	if err != nil {
		return err
	}
	if err := DB.Delete(&ExtLink{}, id).Error; err != nil {
		return err
	}
	InvalidateExtLinkCache(link.Slug)
	return nil
}

// -------------------- 60s slug 缓存（反代热路径） --------------------

type extLinkCacheEntry struct {
	link      *ExtLink // nil 表示"不存在"的负缓存
	expiresAt time.Time
}

var (
	extLinkCache   = map[string]*extLinkCacheEntry{}
	extLinkCacheMu sync.RWMutex
)

const extLinkCacheTTL = 60 * time.Second

// GetExtLinkBySlugCached 反代热路径查询：60s 缓存（含负缓存）。
// 返回 (nil, nil) 表示 slug 不存在或已禁用——调用方按 404 处理。
func GetExtLinkBySlugCached(slug string) (*ExtLink, error) {
	slug = NormalizeExtLinkSlug(slug)
	now := time.Now()

	extLinkCacheMu.RLock()
	entry, ok := extLinkCache[slug]
	extLinkCacheMu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		return entry.link, nil
	}

	extLinkCacheMu.Lock()
	defer extLinkCacheMu.Unlock()
	// double-check
	if entry, ok := extLinkCache[slug]; ok && now.Before(entry.expiresAt) {
		return entry.link, nil
	}

	var link ExtLink
	err := DB.Where("slug = ?", slug).First(&link).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			extLinkCache[slug] = &extLinkCacheEntry{link: nil, expiresAt: now.Add(extLinkCacheTTL)}
			return nil, nil
		}
		return nil, err
	}
	if !link.Enabled {
		// 禁用的也走负缓存语义（60s 内启用变更允许延迟生效，与蓝图 60s 缓存一致）。
		extLinkCache[slug] = &extLinkCacheEntry{link: nil, expiresAt: now.Add(extLinkCacheTTL)}
		return nil, nil
	}
	copied := link
	extLinkCache[slug] = &extLinkCacheEntry{link: &copied, expiresAt: now.Add(extLinkCacheTTL)}
	return &copied, nil
}

// InvalidateExtLinkCache 写操作后主动失效，避免管理端改动要等满 TTL。
// slug 变更场景由 controller 对新旧 slug 各调一次。
func InvalidateExtLinkCache(slug string) {
	slug = NormalizeExtLinkSlug(slug)
	extLinkCacheMu.Lock()
	delete(extLinkCache, slug)
	extLinkCacheMu.Unlock()
}

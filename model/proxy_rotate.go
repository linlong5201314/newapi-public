// ProxyRotate 出口轮换调度。
//
// 目标：让同一条上游免费额度（通常按 IP 限流，例如 30 RPM/IP、150K tokens/day/IP）
// 分散到 N 个出口上，从而把可用额度放大到 N 倍。
//
// 设计要点：
//  1. 逐请求轮询（round-robin），不是按 token/渠道挑一个固定出口 —— 只有一个出口
//     在跑的话额度不会变多，轮询才能把 N 个 IP 的配额都用上。
//  2. URL 列表必须走内存缓存。生产库是跨区 Supabase（Railway iad ↔ eu-west-1），
//     单次查询约 200ms；如果每个请求都查一次 DB，relay 会被自己的元数据查询拖死。
//     缓存 TTL 30s，节点增删改时显式失效。
//  3. 失败降级：某出口连续失败达 ProxyEndpointMaxFailCount 次后临时退出可用列表
//     （fail_count 落库，重启后依然有效），成功一次即清零复活。
package model

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

const proxyPoolCacheTTL = 30 * time.Second

type proxyPoolCacheEntry struct {
	urls []string
	at   time.Time
}

var (
	proxyPoolCache    sync.Map // userId(int) -> *proxyPoolCacheEntry
	proxyRotateCursor sync.Map // userId(int) -> *uint64
)

// InvalidateProxyPoolCache 丢弃某用户的出口列表缓存。
// 节点/订阅发生增删改时必须调用，否则最坏情况要等 TTL 过期才生效。
func InvalidateProxyPoolCache(userId int) {
	if userId <= 0 {
		return
	}
	proxyPoolCache.Delete(userId)
}

// InvalidateAllProxyPoolCache 全量失效（订阅批量重建节点后使用）。
func InvalidateAllProxyPoolCache() {
	proxyPoolCache.Range(func(key, _ any) bool {
		proxyPoolCache.Delete(key)
		return true
	})
}

// listUsableProxyUrlsCached 带 TTL 缓存的可用出口 URL 列表。
func listUsableProxyUrlsCached(userId int) ([]string, error) {
	if cached, ok := proxyPoolCache.Load(userId); ok {
		entry := cached.(*proxyPoolCacheEntry)
		if time.Since(entry.at) < proxyPoolCacheTTL {
			return entry.urls, nil
		}
	}
	urls, err := ListUsableProxyUrls(userId)
	if err != nil {
		return nil, err
	}
	proxyPoolCache.Store(userId, &proxyPoolCacheEntry{urls: urls, at: time.Now()})
	return urls, nil
}

// PickUserProxyUrl 为该用户挑下一个出口 URL；没有可用节点时返回空串。
//
// 返回空串是正常状态（用户没配出口 / 全部进入失败冷却），调用方应回退到
// 直连或渠道自带的 proxy，而不是把它当成错误。
func PickUserProxyUrl(userId int) string {
	if userId <= 0 {
		return ""
	}
	urls, err := listUsableProxyUrlsCached(userId)
	if err != nil || len(urls) == 0 {
		return ""
	}
	if len(urls) == 1 {
		return urls[0]
	}
	counterAny, _ := proxyRotateCursor.LoadOrStore(userId, new(uint64))
	counter := counterAny.(*uint64)
	next := atomic.AddUint64(counter, 1) - 1
	return urls[int(next%uint64(len(urls)))]
}

// RecordProxyOutcome 记录一次真实上游调用的结果，用于失败降级。
// proxyUrl 是本次实际使用的出口；不在池里的 URL 直接忽略。
func RecordProxyOutcome(userId int, proxyUrl string, ok bool) {
	if userId <= 0 || proxyUrl == "" {
		return
	}
	endpoint, err := GetUsableProxyEndpointByUrl(userId, proxyUrl)
	if err != nil || endpoint == nil {
		return
	}
	_ = MarkProxyEndpointResult(endpoint.Id, ok, "upstream request failed through this exit")
	// 失败计数变化会影响可用集合，立刻让缓存失效；成功清零同理。
	InvalidateProxyPoolCache(userId)
}

// GetUsableProxyEndpointByUrl 按 URL 找到该用户某个仍可用的节点。
func GetUsableProxyEndpointByUrl(userId int, proxyUrl string) (*ProxyEndpoint, error) {
	if userId <= 0 || proxyUrl == "" {
		return nil, nil
	}
	var endpoint ProxyEndpoint
	err := DB.Where("user_id = ? AND url = ? AND enabled = ?", userId, proxyUrl, true).
		First(&endpoint).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &endpoint, nil
}

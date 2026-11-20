// ProxyPool 用户出口 IP 池。
// 每个用户可配置多个出口节点（机场订阅 / 住宅 IP / vless 等），
// 上游请求按节点轮换，从而在按 IP 限流的免费层上放大可用额度。
package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"gorm.io/gorm"
)

var proxySchemaMu sync.Mutex
var proxySchemaReadyDB *gorm.DB

const (
	// ProxyEndpointMaxFailCount 连续失败达到该次数后节点退出可用列表。
	ProxyEndpointMaxFailCount = 5
	// proxyLastErrorMaxLen 与 last_error 字段的 varchar(500) 对齐。
	proxyLastErrorMaxLen = 500
)

// ProxyEndpoint 用户出口节点。
type ProxyEndpoint struct {
	Id             int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId         int    `json:"user_id" gorm:"index;not null;default:0"`
	Name           string `json:"name" gorm:"type:varchar(120);not null;default:''"`
	Kind           string `json:"kind" gorm:"type:varchar(20);not null;default:''"` // http|https|socks5|socks5h|vless|vmess|trojan|hysteria2|ss
	Url            string `json:"url" gorm:"type:varchar(1000)"`                    // 可直接使用的代理 URL（仅 http/https/socks5/socks5h 有值）
	RawUri         string `json:"raw_uri" gorm:"type:text"`                         // 客户端型节点的原始 URI（vless://... 等）
	Region         string `json:"region" gorm:"type:varchar(60)"`
	SubscriptionId int    `json:"subscription_id" gorm:"index;default:0"` // 0=手动添加
	Source         string `json:"source" gorm:"type:varchar(40);index;default:'manual'"`
	Enabled        bool   `json:"enabled" gorm:"default:true"`
	// Public=true 时该节点进入公共池：所有用户可见可选；false 时仅归属用户可见可用。
	Public    bool       `json:"public" gorm:"index;default:false"`
	Weight    int        `json:"weight" gorm:"default:1"`
	LatencyMs int        `json:"latency_ms" gorm:"column:latency_ms;default:0"`
	FailCount int        `json:"fail_count" gorm:"default:0"`
	LastOkAt  *time.Time `json:"last_ok_at"`
	LastError string     `json:"last_error" gorm:"type:varchar(500)"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (ProxyEndpoint) TableName() string { return "proxy_endpoints" }

// ProxySubscription 用户添加的机场订阅。
type ProxySubscription struct {
	Id           int        `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId       int        `json:"user_id" gorm:"index;not null;default:0"`
	Name         string     `json:"name" gorm:"type:varchar(120)"`
	Url          string     `json:"url" gorm:"type:text"`           // 订阅链接（返回给前端时脱敏，只回 host+前缀）
	Format       string     `json:"format" gorm:"type:varchar(20)"` // clash|base64|uri-list|auto
	NodeCount    int        `json:"node_count" gorm:"default:0"`
	TrafficUsed  int64      `json:"traffic_used" gorm:"default:0"`  // 已用流量（字节，订阅头/手填）
	TrafficTotal int64      `json:"traffic_total" gorm:"default:0"` // 总流量（字节，0=未知）
	ExpireAt     *time.Time `json:"expire_at"`                      // 到期时间（可选）
	LastSyncAt   *time.Time `json:"last_sync_at"`
	LastError    string     `json:"last_error" gorm:"type:varchar(500)"`
	Enabled      bool       `json:"enabled" gorm:"default:true"`
	Public       bool       `json:"public" gorm:"index;default:false"`
	CreatedAt    time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (ProxySubscription) TableName() string { return "proxy_subscriptions" }

// EnsureProxyPoolSchema 在 SKIP_DB_MIGRATION=true 时也补齐代理池表的全部缺列。
// 只增补缺失结构，不用 AutoMigrate 重写已有列/默认值，避免跨库重复 ALTER。
func EnsureProxyPoolSchema() error {
	proxySchemaMu.Lock()
	defer proxySchemaMu.Unlock()
	if DB == nil {
		return fmt.Errorf("proxy pool database is not initialized")
	}
	if proxySchemaReadyDB == DB {
		return nil
	}
	migrator := DB.Migrator()
	for _, target := range []any{&ProxyEndpoint{}, &ProxySubscription{}} {
		stmt := &gorm.Statement{DB: DB}
		if err := stmt.Parse(target); err != nil {
			return fmt.Errorf("parse proxy pool schema: %w", err)
		}
		if !migrator.HasTable(target) {
			if err := migrator.CreateTable(target); err != nil {
				// A second application node may be initializing the same table.
				if !migrator.HasTable(target) {
					return fmt.Errorf("create proxy pool table %s: %w", stmt.Table, err)
				}
			}
		}
		for _, field := range stmt.Schema.Fields {
			if field.DBName == "" || migrator.HasColumn(target, field.DBName) {
				continue
			}
			if err := migrator.AddColumn(target, field.Name); err != nil {
				if !migrator.HasColumn(target, field.DBName) {
					return fmt.Errorf("add proxy pool field %s.%s: %w", stmt.Table, field.DBName, err)
				}
			}
		}
		for name := range stmt.Schema.ParseIndexes() {
			if migrator.HasIndex(target, name) {
				continue
			}
			if err := migrator.CreateIndex(target, name); err != nil {
				if !migrator.HasIndex(target, name) {
					return fmt.Errorf("create proxy pool index %s: %w", name, err)
				}
			}
		}
	}
	proxySchemaReadyDB = DB
	return nil
}

const ProxyEndpointListLimit = 600

// ListProxyEndpoints preserves the bounded legacy list contract.
func ListProxyEndpoints(userId int) ([]ProxyEndpoint, error) {
	list, _, err := ListProxyEndpointsPage(userId, 0, ProxyEndpointListLimit)
	return list, err
}

// ListProxyEndpointsPage bounds response size, not inventory or scheduling.
// One look-ahead row reports hasMore without an extra full inventory count.
func ListProxyEndpointsPage(userId, offset, limit int) ([]ProxyEndpoint, bool, error) {
	if offset < 0 || limit <= 0 || limit > ProxyEndpointListLimit {
		return nil, false, fmt.Errorf("invalid proxy endpoint pagination")
	}
	if err := EnsureProxyPoolSchema(); err != nil {
		return nil, false, err
	}
	var list []ProxyEndpoint
	err := DB.Select("id, user_id, name, kind, url, CASE WHEN raw_uri <> '' THEN substr(raw_uri, 1, 20) ELSE '' END AS raw_uri, region, subscription_id, source, enabled, public, weight, latency_ms, fail_count, last_ok_at, last_error, created_at, updated_at").
		Where("user_id = ? OR public = ?", userId, true).
		Order("CASE WHEN user_id = " + itoaSafe(userId) + " AND source <> 'haizhu' THEN 0 ELSE 1 END ASC, fail_count ASC, CASE WHEN latency_ms > 0 THEN latency_ms ELSE 99999 END ASC, weight DESC, id ASC").
		Offset(offset).Limit(limit + 1).
		Find(&list).Error
	hasMore := len(list) > limit
	if hasMore {
		list = list[:limit]
	}
	return list, hasMore, err
}

// ProxyPoolStats 代理池聚合统计。
type ProxyPoolStats struct {
	Total        int `gorm:"column:total"`
	Enabled      int `gorm:"column:enabled"`
	ClientNodes  int `gorm:"column:client_nodes"`
	PublicNodes  int `gorm:"column:public_nodes"`
	HaizhuNodes  int `gorm:"column:haizhu_nodes"`
	DirectNodes  int `gorm:"column:direct_nodes"`
	HealthyNodes int `gorm:"column:healthy_nodes"`
	UnknownNodes int `gorm:"column:unknown_nodes"`
	FailedNodes  int `gorm:"column:failed_nodes"`
}

// GetProxyPoolStatsForUser 单次 SQL 聚合统计该用户可见的代理池规模（跨区库 ~220ms）。
func GetProxyPoolStatsForUser(userId int) (ProxyPoolStats, error) {
	if err := EnsureProxyPoolSchema(); err != nil {
		return ProxyPoolStats{}, err
	}
	var st ProxyPoolStats
	err := DB.Model(&ProxyEndpoint{}).
		Select(`COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN enabled = true THEN 1 ELSE 0 END), 0) AS enabled,
			COALESCE(SUM(CASE WHEN raw_uri <> '' THEN 1 ELSE 0 END), 0) AS client_nodes,
			COALESCE(SUM(CASE WHEN public = true THEN 1 ELSE 0 END), 0) AS public_nodes,
			COALESCE(SUM(CASE WHEN source = 'haizhu' THEN 1 ELSE 0 END), 0) AS haizhu_nodes,
 COALESCE(SUM(CASE WHEN url <> '' THEN 1 ELSE 0 END), 0) AS direct_nodes,
 COALESCE(SUM(CASE WHEN enabled = true AND fail_count = 0 AND last_ok_at IS NOT NULL THEN 1 ELSE 0 END), 0) AS healthy_nodes,
 COALESCE(SUM(CASE WHEN enabled = true AND fail_count = 0 AND last_ok_at IS NULL THEN 1 ELSE 0 END), 0) AS unknown_nodes,
 COALESCE(SUM(CASE WHEN enabled = true AND fail_count > 0 THEN 1 ELSE 0 END), 0) AS failed_nodes`).
		Where("user_id = ? OR public = ?", userId, true).
		Scan(&st).Error
	return st, err
}

func itoaSafe(v int) string {
	if v <= 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// GetProxyEndpointVisible 读取当前用户可见的单个出口节点（自己的或公开的）。
func GetProxyEndpointVisible(userId, id int) (*ProxyEndpoint, error) {
	var endpoint ProxyEndpoint
	err := DB.Where("id = ? AND (user_id = ? OR public = ?)", id, userId, true).First(&endpoint).Error
	if err != nil {
		return nil, err
	}
	return &endpoint, nil
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
	if err := DB.CreateInBatches(&list, 800).Error; err != nil {
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
	return MarkProxyEndpointLatency(id, ok, 0, errMsg)
}

// MarkProxyEndpointLatency 记录一次节点探测结果并回写实测延迟（ms）。
func MarkProxyEndpointLatency(id int, ok bool, latencyMs int, errMsg string) error {
	now := time.Now()
	if ok {
		updates := map[string]any{
			"fail_count": 0,
			"last_ok_at": now,
			"last_error": "",
		}
		if latencyMs > 0 {
			updates["latency_ms"] = latencyMs
		}
		return DB.Model(&ProxyEndpoint{}).Where("id = ?", id).Updates(updates).Error
	}
	if len([]rune(errMsg)) > proxyLastErrorMaxLen {
		errMsg = string([]rune(errMsg)[:proxyLastErrorMaxLen])
	}
	return DB.Model(&ProxyEndpoint{}).Where("id = ?", id).Updates(map[string]any{
		"fail_count": gorm.Expr("fail_count + 1"),
		"last_error": errMsg,
	}).Error
}

// CountProxyEndpoints 统计某用户的手动/订阅出口节点总数（不含内置 haizhu 池，用于配额校验）。
func CountProxyEndpoints(userId int) (int, error) {
	var total int64
	if err := DB.Model(&ProxyEndpoint{}).Where("user_id = ? AND source <> 'haizhu'", userId).Count(&total).Error; err != nil {
		return 0, err
	}
	return int(total), nil
}

// CountHaizhuEndpoints 统计已入库的 Haizhu 内置公共节点数。
func CountHaizhuEndpoints() (int, error) {
	var total int64
	if err := DB.Model(&ProxyEndpoint{}).Where("source = ?", "haizhu").Count(&total).Error; err != nil {
		return 0, err
	}
	return int(total), nil
}

// DeleteHaizhuEndpoints 清理旧的 Haizhu 内置节点（重新全量导入前调用）。
func DeleteHaizhuEndpoints() error {
	if err := DB.Where("source = ?", "haizhu").Delete(&ProxyEndpoint{}).Error; err != nil {
		return err
	}
	InvalidateAllProxyPoolCache()
	return nil
}

// CountProxySubscriptions 统计某用户的订阅总数（用于配额校验）。
func CountProxySubscriptions(userId int) (int, error) {
	var total int64
	if err := DB.Model(&ProxySubscription{}).Where("user_id = ?", userId).Count(&total).Error; err != nil {
		return 0, err
	}
	return int(total), nil
}

// ListUsableProxyUrls 返回某用户当前可用于上游轮换的代理 URL（自己的 + 公共池公开的）。
// 条件：enabled、url 非空、连续失败次数未达上限；优先使用未失败且延迟最低的节点；
// 末尾追加内核（mihomo）的本地出口，让只有客户端协议（vless/ss/anytls…）的节点也能真正发请求。
func ListUsableProxyUrls(userId int) ([]string, error) {
	if err := EnsureProxyPoolSchema(); err != nil {
		return nil, err
	}
	var endpoints []ProxyEndpoint
	err := DB.Where("(user_id = ? OR public = ?) AND enabled = ? AND fail_count < ?", userId, true, true, ProxyEndpointMaxFailCount).
		Order("fail_count ASC, CASE WHEN latency_ms > 0 THEN latency_ms ELSE 99999 END ASC, weight DESC, id ASC").Find(&endpoints).Error
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint.Url != "" {
			urls = append(urls, endpoint.Url)
		} else if endpoint.RawUri != "" {
			if proxyURL, err := endpointProxyURLProvider(endpoint); err == nil {
				urls = append(urls, proxyURL)
			}
		}
	}
	return urls, nil
}

// ValidateSelfChannelProxyPolicy checks visibility without contacting proxy nodes.
func ValidateSelfChannelProxyPolicy(userID int, policy *dto.ChannelProxyPolicy, fixedProxy string) error {
	if policy == nil {
		return nil
	}
	if policy.SubscriptionID < 0 || policy.EndpointID < 0 {
		return fmt.Errorf("proxy policy IDs must not be negative")
	}
	if policy.Mode != "subscription" && policy.SubscriptionID != 0 || policy.Mode != "endpoint" && policy.EndpointID != 0 {
		return fmt.Errorf("proxy policy IDs do not match its mode")
	}
	switch policy.Mode {
	case "direct", "user_pool", "public_pool":
		return nil
	case "fixed":
		parsed, err := common.ParseProxyURLStrict(strings.TrimSpace(fixedProxy))
		if err != nil || parsed == nil {
			return fmt.Errorf("fixed proxy policy requires a valid proxy URL")
		}
		return nil
	case "endpoint", "subscription":
		if userID <= 0 {
			return fmt.Errorf("proxy policy requires an authenticated owner")
		}
		if err := EnsureProxyPoolSchema(); err != nil {
			return err
		}
		if policy.Mode == "endpoint" {
			if policy.EndpointID <= 0 {
				return fmt.Errorf("proxy endpoint ID is required")
			}
			ep, err := GetProxyEndpointVisible(userID, policy.EndpointID)
			if err != nil {
				return fmt.Errorf("proxy endpoint unavailable or not visible: %w", err)
			}
			if !ep.Enabled {
				return fmt.Errorf("proxy endpoint is disabled")
			}
			return nil
		}
		if policy.SubscriptionID <= 0 {
			return fmt.Errorf("proxy subscription ID is required")
		}
		var sub ProxySubscription
		if err := DB.Where("id = ? AND (user_id = ? OR public = ?)", policy.SubscriptionID, userID, true).First(&sub).Error; err != nil {
			return fmt.Errorf("proxy subscription unavailable or not visible: %w", err)
		}
		if !sub.Enabled {
			return fmt.Errorf("proxy subscription is disabled")
		}
		return nil
	default:
		return fmt.Errorf("invalid proxy policy mode")
	}
}

// ResolveChannelProxyPolicy preserves the legacy entry point; callers with a
// channel/account identity should use ResolveStableChannelProxy instead.
func ResolveChannelProxyPolicy(ownerID int, setting dto.ChannelSettings) (string, error) {
	return ResolveStableChannelProxy(ownerID, 0, "legacy", setting)
}

const proxyStickyTTL = 30 * 24 * time.Hour

type proxyStickyEntry struct {
	value string
	until time.Time
}

var proxyStickyState = struct {
	sync.Mutex
	entries map[string]proxyStickyEntry
}{entries: make(map[string]proxyStickyEntry)}

// proxyStickyValue atomically compares and replaces a binding. Redis errors
// fail closed rather than creating divergent per-instance assignments. Values
// contain only endpoint IDs or identity hashes, never credentials or proxy URLs.
func proxyStickyValue(key string, expected *string, next string) (string, error) {
	if common.RedisEnabled && common.RDB == nil && strings.TrimSpace(os.Getenv("REDIS_CONN_STRING")) != "" {
		return "", fmt.Errorf("proxy account binding store unavailable")
	}
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		script := `local v=redis.call('GET',KEYS[1]) or '';
if ARGV[1]=='1' and v==ARGV[2] then v=ARGV[3]; redis.call('SET',KEYS[1],v,'EX',ARGV[4])
elseif v~='' then redis.call('EXPIRE',KEYS[1],ARGV[4]) end; return v`
		write, old := "0", ""
		if expected != nil {
			write, old = "1", *expected
		}
		value, err := common.RDB.Eval(ctx, script, []string{key}, write, old, next, int64(proxyStickyTTL/time.Second)).Text()
		if err != nil && !errors.Is(err, redis.Nil) {
			return "", fmt.Errorf("proxy account binding store unavailable")
		}
		return value, nil
	}
	proxyStickyState.Lock()
	defer proxyStickyState.Unlock()
	now := time.Now()
	entry := proxyStickyState.entries[key]
	if !entry.until.After(now) {
		entry.value = ""
	}
	if expected != nil && entry.value == *expected {
		entry.value = next
	}
	if entry.value != "" {
		entry.until = now.Add(proxyStickyTTL)
		proxyStickyState.entries[key] = entry
	}
	// Bound local state and remove expired identities on the write path.
	if len(proxyStickyState.entries) > 10000 {
		for k, v := range proxyStickyState.entries {
			if !v.until.After(now) {
				delete(proxyStickyState.entries, k)
			}
		}
	}
	return entry.value, nil
}

func proxyAccountIdentity(accountKey string) string {
	preference := "any:"
	provider, _, _ := strings.Cut(strings.ToLower(accountKey), ":")
	if provider == "workbuddy" || provider == "trae" {
		preference = "hk-tw:"
	}
	return fmt.Sprintf("%s%x", preference, sha256.Sum256([]byte(accountKey)))
}

// BindChannelProxyAccount must be called by the authorized creator after saving
// the channel, using the SAME opaque accountKey used before login. It preserves
// the pool policy so an unusable endpoint can be replaced without direct fallback.
func BindChannelProxyAccount(ownerID, channelID int, accountKey string) error {
	if ownerID < 0 || channelID <= 0 || strings.TrimSpace(accountKey) == "" || len(accountKey) > 1024 {
		return fmt.Errorf("invalid proxy account binding identity")
	}
	if DB == nil {
		return fmt.Errorf("proxy channel owner unavailable")
	}
	var channel Channel
	if err := DB.Select("id, owner_id").Where("id = ? AND owner_id = ?", channelID, ownerID).First(&channel).Error; err != nil {
		return fmt.Errorf("proxy channel owner unavailable")
	}
	key := fmt.Sprintf("proxy-pool:sticky:v1:owner:%d:channel:%d", ownerID, channelID)
	identity := proxyAccountIdentity(accountKey)
	empty := ""
	value, err := proxyStickyValue(key, &empty, identity)
	if err != nil {
		return err
	}
	if value != identity {
		return fmt.Errorf("proxy channel already bound to another account")
	}
	return nil
}

// Egress selection failures are surfaced to account authorization as actionable
// 503 responses, distinct from a mid-flow proxy change/unavailability. The
// controller maps them with errors.Is; messages stay user-facing English keys.
var (
	ErrProxyPoolEmpty        = errors.New("no usable proxy endpoint in the selected proxy scope")
	ErrProxyEndpointNotReady = errors.New("selected proxy endpoint has no usable listener in this process")
)

// ResolveStableChannelProxy selects a fixed-node URL, never the shared selector.
// ownerID is the channel OWNER (0 for an internal platform public pool), not
// the requesting end user. accountKey is a stable provider:opaque identity for
// pre-login flows; channel callers use channelID and the post-login binding.
// Unknown nodes may be selected but are never represented as probed healthy.
func ResolveStableChannelProxy(ownerID, channelID int, accountKey string, setting dto.ChannelSettings) (string, error) {
	if ownerID < 0 || channelID < 0 || len(accountKey) > 1024 {
		return "", fmt.Errorf("invalid proxy account identity")
	}
	policy := setting.ProxyPolicy
	legacyScope := false
	if policy == nil {
		switch legacy := strings.TrimSpace(setting.Proxy); legacy {
		case "direct":
			return "", nil
		case "", "userpool":
			if legacy == "" && ownerID == 0 {
				// Preserve unset legacy platform channels; an explicit pool
				// policy is required to opt these into internal public egress.
				return "", nil
			}
			legacyScope = true
			policy = &dto.ChannelProxyPolicy{Mode: "user_pool"}
		default:
			policy = &dto.ChannelProxyPolicy{Mode: "fixed"}
		}
	}
	if err := ValidateSelfChannelProxyPolicy(ownerID, policy, setting.Proxy); err != nil {
		return "", err
	}
	switch policy.Mode {
	case "direct":
		return "", nil
	case "fixed":
		return strings.TrimSpace(setting.Proxy), nil
	}
	if ownerID == 0 && policy.Mode != "public_pool" {
		return "", fmt.Errorf("proxy policy requires an authenticated owner")
	}
	if err := EnsureProxyPoolSchema(); err != nil {
		return "", err
	}
	identity := proxyAccountIdentity(accountKey)
	if channelID > 0 {
		aliasKey := fmt.Sprintf("proxy-pool:sticky:v1:owner:%d:channel:%d", ownerID, channelID)
		alias, err := proxyStickyValue(aliasKey, nil, "")
		if err != nil {
			return "", err
		}
		if alias != "" {
			identity = alias
		} else {
			identity = proxyAccountIdentity(fmt.Sprintf("channel:%d", channelID))
		}
	} else if strings.TrimSpace(accountKey) == "" {
		return "", fmt.Errorf("proxy account identity is required before login")
	}
	key := fmt.Sprintf("proxy-pool:sticky:v1:owner:%d:account:%s:scope:%s:%d:%d:%t", ownerID, identity, policy.Mode, policy.EndpointID, policy.SubscriptionID, legacyScope)
	bound, err := proxyStickyValue(key, nil, "")
	if err != nil {
		return "", err
	}
	// A failed endpoint stays out until an explicit successful probe resets it.
	query := DB.Model(&ProxyEndpoint{}).Where("enabled = ? AND fail_count = ?", true, 0)
	switch policy.Mode {
	case "user_pool":
		if legacyScope {
			query = query.Where("user_id = ? OR public = ?", ownerID, true)
		} else {
			query = query.Where("user_id = ?", ownerID)
		}
	case "public_pool":
		query = query.Where("public = ?", true)
	case "endpoint":
		query = query.Where("id = ? AND (user_id = ? OR public = ?)", policy.EndpointID, ownerID, true)
	case "subscription":
		query = query.Where("subscription_id = ? AND (user_id = ? OR public = ?)", policy.SubscriptionID, ownerID, true)
	}
	var endpoints []ProxyEndpoint
	if err := query.Order("id ASC").Find(&endpoints).Error; err != nil {
		return "", fmt.Errorf("resolve proxy policy: %w", err)
	}
	// Rank inventory IDs before resolving a listener. Eager resolution would
	// either exclude everything outside preload or load thousands of listeners.
	candidates := make(map[string]ProxyEndpoint, len(endpoints))
	best, bestRank := "", -1
	var bestScore [32]byte
	for _, endpoint := range endpoints {
		if endpoint.Url == "" && endpoint.RawUri == "" {
			continue
		}
		if endpoint.Url != "" {
			if parsed, parseErr := common.ParseProxyURLStrict(endpoint.Url); parseErr != nil || parsed == nil {
				continue
			}
		}
		id := strconv.Itoa(endpoint.Id)
		candidates[id] = endpoint
		rank := 0
		region := strings.ToUpper(endpoint.Region + " " + endpoint.Name)
		if strings.HasPrefix(identity, "hk-tw:") && (strings.Contains(region, "HK") || strings.Contains(region, "TW") || strings.Contains(region, "香港") || strings.Contains(region, "台湾") || strings.Contains(region, "台灣")) {
			rank = 2
		}
		if endpoint.LastOkAt != nil {
			rank++
		}
		score := sha256.Sum256([]byte(key + ":" + id))
		if best == "" || rank > bestRank || rank == bestRank && strings.Compare(string(score[:]), string(bestScore[:])) > 0 {
			best, bestRank, bestScore = id, rank, score
		}
	}
	if best == "" {
		return "", ErrProxyPoolEmpty
	}
	selected := bound
	if _, ok := candidates[selected]; !ok {
		selected, err = proxyStickyValue(key, &bound, best)
		if err != nil {
			return "", err
		}
	}
	endpoint, ok := candidates[selected]
	if !ok {
		return "", fmt.Errorf("proxy binding changed concurrently; retry the request")
	}
	proxyURL := endpoint.Url
	if proxyURL == "" {
		proxyURL, err = selectedEndpointProxyURLProvider(endpoint)
		if err != nil {
			// Runtime capacity/outages must not rotate an otherwise valid binding.
			return "", fmt.Errorf("%w: %v", ErrProxyEndpointNotReady, err)
		}
	}
	if parsed, parseErr := common.ParseProxyURLStrict(proxyURL); parseErr != nil || parsed == nil {
		return "", fmt.Errorf("account proxy endpoint returned an invalid listener URL")
	}
	stableProxyEndpoints.Store(sha256.Sum256([]byte(proxyURL)), stableProxyEndpoint{id: endpoint.Id, fingerprint: sha256.Sum256([]byte(endpoint.Url + "\x00" + endpoint.RawUri))})
	return proxyURL, nil
}

type stableProxyEndpoint struct {
	id          int
	fingerprint [32]byte
}

var stableProxyEndpoints sync.Map

// InvalidateStableProxyURL records failure of a URL issued by this resolver.
// The caller already has the scoped, authenticated endpoint URL; no shared
// selector is mutated. Returning true prevents the legacy outcome path from
// attributing a channel owner's private endpoint to the requesting end user.
func InvalidateStableProxyURL(proxyURL string) bool {
	value, ok := stableProxyEndpoints.Load(sha256.Sum256([]byte(proxyURL)))
	if !ok || DB == nil {
		return false
	}
	selected := value.(stableProxyEndpoint)
	var endpoint ProxyEndpoint
	if err := DB.Where("id = ?", selected.id).First(&endpoint).Error; err != nil {
		return true
	}
	if sha256.Sum256([]byte(endpoint.Url+"\x00"+endpoint.RawUri)) != selected.fingerprint {
		return true
	}
	if err := MarkProxyEndpointResult(endpoint.Id, false, "upstream request failed through this exit"); err != nil {
		common.SysError("proxy endpoint failure update failed")
	}
	return true
}

var endpointProxyURLProvider = func(ProxyEndpoint) (string, error) {
	return "", fmt.Errorf("mihomo endpoint listener is unavailable; reload the kernel")
}

// selectedEndpointProxyURLProvider may load ONE already authorized endpoint.
// Passive pool enumeration must keep using endpointProxyURLProvider.
var selectedEndpointProxyURLProvider = func(endpoint ProxyEndpoint) (string, error) {
	return endpointProxyURLProvider(endpoint)
}

// SetEndpointProxyURLProvider registers the loaded, fixed-node listener resolver at startup.
func SetEndpointProxyURLProvider(fn func(ProxyEndpoint) (string, error)) {
	if fn != nil {
		endpointProxyURLProvider = fn
	}
}

// SetSelectedEndpointProxyURLProvider registers bounded, on-demand resolution.
func SetSelectedEndpointProxyURLProvider(fn func(ProxyEndpoint) (string, error)) {
	if fn != nil {
		selectedEndpointProxyURLProvider = fn
	}
}

// extraProxyURLProvider 由内核管理器注册（避免 model → service 的循环依赖）。
var extraProxyURLProvider = func(int) []string { return nil }

// kernelFailoverProvider 当走本地 mihomo 端口失败时触发内核切到下一个可用节点。
var kernelFailoverProvider = func(userId int, proxyURL string) bool { return false }

// SetExtraProxyURLProvider 注册内核出口提供者。
func SetExtraProxyURLProvider(fn func(userId int) []string) {
	if fn == nil {
		extraProxyURLProvider = func(int) []string { return nil }
		return
	}
	extraProxyURLProvider = fn
}

// SetKernelFailoverProvider 注册内核故障切换回调。
func SetKernelFailoverProvider(fn func(userId int, proxyURL string) bool) {
	if fn == nil {
		kernelFailoverProvider = func(int, string) bool { return false }
		return
	}
	kernelFailoverProvider = fn
}

// ProxyRuntimePreloadLimit bounds initial per-process listeners, not inventory.
// Selected endpoints can load on demand up to the separate runtime hard bound.
func ProxyRuntimePreloadLimit() int {
	return min(max(common.GetEnvOrDefault("PROXY_POOL_PRELOAD_LIMIT", 300), 1), 1500)
}

// ListAllEnabledProxyEndpoints returns only the bounded preload inventory.
func ListAllEnabledProxyEndpoints() ([]ProxyEndpoint, error) {
	if err := EnsureProxyPoolSchema(); err != nil {
		return nil, err
	}
	var list []ProxyEndpoint
	err := DB.Where("enabled = ? AND fail_count < ?", true, ProxyEndpointMaxFailCount).
		Order("CASE WHEN source <> 'haizhu' THEN 0 ELSE 1 END ASC, fail_count ASC, CASE WHEN latency_ms > 0 THEN latency_ms ELSE 99999 END ASC, weight DESC, id ASC").
		Limit(ProxyRuntimePreloadLimit()).
		Find(&list).Error
	return list, err
}

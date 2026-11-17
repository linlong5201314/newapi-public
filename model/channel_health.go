package model

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// ---------------------------------------------------------------------------
// 渠道健康度（近 N 次成功率 + 平均延迟）
//
// 目的：公共池里多人公开同一个模型时，路由按「实测延迟 + 近几次成功率」加权，
// 快的多分流量，连续失败的自动降权，避免请求反复打到已经挂掉的上游。
//
// 实现：内存环形缓冲记录最近 ChannelHealthSampleSize 次结果，同时异步写 Redis
// hash（chstat:<id>），多节点共享同一份观测；Redis 不可用时退化为单节点内存统计。
// 读写都不落 DB，避免每个请求一次 UPDATE。
// ---------------------------------------------------------------------------

const (
	// ChannelHealthSampleSize 是参与成功率计算的最近结果条数。
	ChannelHealthSampleSize = 5
	// channelHealthRedisTTL 是 Redis 中单渠道统计的存活时间。
	channelHealthRedisTTL = time.Hour
	// channelHealthCacheTTL 是本地读缓存的存活时间（多节点场景下避免每请求打 Redis）。
	channelHealthCacheTTL = 15 * time.Second
	// channelHealthMinSamples 是降权判定所需的最小样本数，样本不足时不惩罚。
	channelHealthMinSamples = 3
)

// ChannelHealth 是对外暴露的健康度快照。
type ChannelHealth struct {
	ChannelId    int     `json:"channel_id"`
	Samples      int     `json:"samples"`
	RecentOK     int     `json:"recent_ok"`
	RecentFail   int     `json:"recent_fail"`
	SuccessRate  float64 `json:"success_rate"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
	// Factor 是路由权重系数：1.0 正常，0.5 降权，0 表示本次不选（样本足够且几乎全失败）。
	Factor float64 `json:"factor"`
	// State 取值 healthy / degraded / down / unknown，前端直接展示。
	State string `json:"state"`
}

type channelHealthEntry struct {
	mu        sync.Mutex
	recent    [ChannelHealthSampleSize]bool
	count     int
	next      int
	latSum    int64
	latCount  int
	loadedAt  time.Time
	hydrated  bool
	redisOK   int64
	redisFail int64
}

var (
	channelHealthMu    sync.RWMutex
	channelHealthStore = make(map[int]*channelHealthEntry)
)

func channelHealthEntryOf(id int) *channelHealthEntry {
	channelHealthMu.RLock()
	entry, ok := channelHealthStore[id]
	channelHealthMu.RUnlock()
	if ok {
		return entry
	}
	channelHealthMu.Lock()
	defer channelHealthMu.Unlock()
	if entry, ok = channelHealthStore[id]; ok {
		return entry
	}
	entry = &channelHealthEntry{}
	channelHealthStore[id] = entry
	return entry
}

// RecordChannelOutcome 记录一次渠道调用结果（成功/失败 + 端到端延迟）。
// 热路径调用，只做内存更新 + 异步 Redis 累加。
func RecordChannelOutcome(channelId int, success bool, latencyMs int64) {
	if channelId <= 0 {
		return
	}
	entry := channelHealthEntryOf(channelId)
	entry.mu.Lock()
	entry.recent[entry.next] = success
	entry.next = (entry.next + 1) % ChannelHealthSampleSize
	if entry.count < ChannelHealthSampleSize {
		entry.count++
	}
	if success && latencyMs > 0 {
		entry.latSum += latencyMs
		entry.latCount++
	}
	if success {
		entry.redisOK++
	} else {
		entry.redisFail++
	}
	okDelta := entry.redisOK
	failDelta := entry.redisFail
	latDelta := int64(0)
	if success && latencyMs > 0 {
		latDelta = latencyMs
	}
	entry.redisOK = 0
	entry.redisFail = 0
	entry.mu.Unlock()

	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	go flushChannelHealthToRedis(channelId, okDelta, failDelta, latDelta)
}

func flushChannelHealthToRedis(channelId int, okDelta, failDelta, latDelta int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	key := channelHealthRedisKey(channelId)
	pipe := common.RDB.TxPipeline()
	if okDelta > 0 {
		pipe.HIncrBy(ctx, key, "ok", okDelta)
	}
	if failDelta > 0 {
		pipe.HIncrBy(ctx, key, "fail", failDelta)
	}
	if latDelta > 0 {
		pipe.HIncrBy(ctx, key, "lat", latDelta)
		pipe.HIncrBy(ctx, key, "lat_n", 1)
	}
	pipe.Expire(ctx, key, channelHealthRedisTTL)
	_, _ = pipe.Exec(ctx)
}

func channelHealthRedisKey(channelId int) string {
	return "chstat:" + strconv.Itoa(channelId)
}

// hydrateFromRedis 在本地样本不足时从 Redis 补齐累计值（多节点共享观测）。
func (entry *channelHealthEntry) hydrateFromRedis(channelId int) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	entry.mu.Lock()
	fresh := entry.hydrated && time.Since(entry.loadedAt) < channelHealthCacheTTL
	entry.mu.Unlock()
	if fresh {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	values, err := common.RDB.HGetAll(ctx, channelHealthRedisKey(channelId)).Result()
	entry.mu.Lock()
	entry.hydrated = true
	entry.loadedAt = time.Now()
	if err == nil && len(values) > 0 {
		ok, _ := strconv.ParseInt(values["ok"], 10, 64)
		fail, _ := strconv.ParseInt(values["fail"], 10, 64)
		lat, _ := strconv.ParseInt(values["lat"], 10, 64)
		latN, _ := strconv.ParseInt(values["lat_n"], 10, 64)
		entry.redisOK = ok
		entry.redisFail = fail
		if lat > 0 && latN > 0 {
			entry.latSum = lat
			entry.latCount = int(latN)
		}
	}
	entry.mu.Unlock()
}

// ChannelHealthOf 返回单渠道健康度快照。
func ChannelHealthOf(channelId int) ChannelHealth {
	if channelId <= 0 {
		return ChannelHealth{State: "unknown", Factor: 1}
	}
	entry := channelHealthEntryOf(channelId)
	entry.hydrateFromRedis(channelId)

	entry.mu.Lock()
	count := entry.count
	okCount := 0
	for i := 0; i < count; i++ {
		if entry.recent[i] {
			okCount++
		}
	}
	failCount := count - okCount
	latSum, latCount := entry.latSum, entry.latCount
	entry.mu.Unlock()

	health := ChannelHealth{
		ChannelId:  channelId,
		Samples:    count,
		RecentOK:   okCount,
		RecentFail: failCount,
		State:      "unknown",
		Factor:     1,
	}
	if count > 0 {
		health.SuccessRate = float64(okCount) / float64(count) * 100
	}
	if latCount > 0 {
		health.AvgLatencyMs = latSum / int64(latCount)
	}

	switch {
	case count < channelHealthMinSamples:
		// 样本不足：不奖励也不惩罚，保持默认权重。
		health.State = "unknown"
		health.Factor = 1
	case failCount >= channelHealthMinSamples && okCount == 0:
		// 最近连续失败：本次不选（除非它是唯一候选，调用方兜底）。
		health.State = "down"
		health.Factor = 0
	case health.SuccessRate < 60:
		health.State = "degraded"
		health.Factor = 0.35
	case health.SuccessRate < 90:
		health.State = "degraded"
		health.Factor = 0.7
	default:
		health.State = "healthy"
		health.Factor = 1
	}
	return health
}

// ChannelHealthBatch 批量取健康度（一次加锁 + 一次 Redis pipeline 语义的多次 HGetAll）。
func ChannelHealthBatch(channelIds []int) map[int]ChannelHealth {
	result := make(map[int]ChannelHealth, len(channelIds))
	for _, id := range channelIds {
		if id <= 0 {
			continue
		}
		result[id] = ChannelHealthOf(id)
	}
	return result
}

// ChannelHealthFactor 只取权重系数，供路由热路径使用（不构造完整快照）。
func ChannelHealthFactor(channelId int) float64 {
	if channelId <= 0 {
		return 1
	}
	entry := channelHealthEntryOf(channelId)
	entry.hydrateFromRedis(channelId)
	entry.mu.Lock()
	count := entry.count
	okCount := 0
	for i := 0; i < count; i++ {
		if entry.recent[i] {
			okCount++
		}
	}
	entry.mu.Unlock()
	if count < channelHealthMinSamples {
		return 1
	}
	failCount := count - okCount
	if failCount >= channelHealthMinSamples && okCount == 0 {
		return 0
	}
	rate := float64(okCount) / float64(count) * 100
	switch {
	case rate < 60:
		return 0.35
	case rate < 90:
		return 0.7
	default:
		return 1
	}
}

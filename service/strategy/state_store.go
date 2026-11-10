package strategy

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// ---------------------------------------------------------------------------
// round-robin / lkgp 外部状态存储。
// 优先 Redis（多节点精确）；无 Redis 退化为进程内 map（单节点精确，多节点允许
// 不精确，与文档 §1.2 说明一致）。
// ---------------------------------------------------------------------------

const (
	roundRobinKeyPrefix = "m2:rr:"
	lkgpKeyPrefix       = "m2:lkgp:"
	lkgpTTL             = 24 * time.Hour
)

var (
	memMu        sync.Mutex
	memCounters  = map[int]int64{}
	memLkgp      = map[int]int{}
	memLkgpStamp = map[int]time.Time{}
)

func redisReady() bool {
	return common.RedisEnabled && common.RDB != nil
}

// NextRoundRobin 返回 per-rule 单调递增计数（首次调用返回 0）。
func NextRoundRobin(ruleId int) int64 {
	if redisReady() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		key := roundRobinKeyPrefix + strconv.Itoa(ruleId)
		n, err := common.RDB.Incr(ctx, key).Result()
		if err == nil {
			return n - 1
		}
		// Redis 故障时退化进程内，不阻塞请求
	}
	memMu.Lock()
	defer memMu.Unlock()
	n := memCounters[ruleId]
	memCounters[ruleId] = n + 1
	return n
}

// SetLastKnownGood 记录规则最近一次成功的渠道。
func SetLastKnownGood(ruleId, channelId int) {
	if ruleId <= 0 || channelId <= 0 {
		return
	}
	if redisReady() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		key := lkgpKeyPrefix + strconv.Itoa(ruleId)
		if err := common.RDB.Set(ctx, key, channelId, lkgpTTL).Err(); err == nil {
			return
		}
	}
	memMu.Lock()
	defer memMu.Unlock()
	memLkgp[ruleId] = channelId
	memLkgpStamp[ruleId] = time.Now()
}

// GetLastKnownGood 读规则最近一次成功的渠道。
func GetLastKnownGood(ruleId int) (int, bool) {
	if ruleId <= 0 {
		return 0, false
	}
	if redisReady() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		key := lkgpKeyPrefix + strconv.Itoa(ruleId)
		val, err := common.RDB.Get(ctx, key).Result()
		if err == nil {
			if id, convErr := strconv.Atoi(val); convErr == nil && id > 0 {
				return id, true
			}
		}
	}
	memMu.Lock()
	defer memMu.Unlock()
	id, ok := memLkgp[ruleId]
	if !ok {
		return 0, false
	}
	if time.Since(memLkgpStamp[ruleId]) > lkgpTTL {
		delete(memLkgp, ruleId)
		delete(memLkgpStamp, ruleId)
		return 0, false
	}
	return id, true
}

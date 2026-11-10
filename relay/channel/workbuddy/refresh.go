// refresh.go WorkBuddy token 刷新单飞（红线：上游 RT 反重放，并发刷新会作废整条 RT 链）。
//
// 并发模型（移植自 workbuddy2api-panel，文件凭证 → DB 凭证适配）：
//   - 进程内单飞：同 channelId 同一时刻只允许一个 refresh 在途，其余等待并复用结果。
//   - Redis 分布式锁兜底（多节点部署）：SETNX wb:refresh:<channelId>，拿不到锁的节点
//     轮询 DB 等持锁节点写回新凭据；超时仍过期才兜底自行刷新。
//   - 两段式：锁内（进程单飞）快照 → 锁外 I/O（30s）→ 写回 channels.key + 缓存失效。
package workbuddy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// refreshSkew 请求前刷新判定窗口（提前 10 分钟视为将过期）。
const refreshSkew = 10 * time.Minute

// refreshFlight 同渠道一次 refresh 的单飞票据。
type refreshFlight struct {
	done chan struct{}
	cred *Cred
	err  error
}

var (
	refreshMu       sync.Mutex
	refreshInFlight = map[int]*refreshFlight{}
)

// RefreshIfNeeded 请求前调用：凭据将在 10min 内过期（或已过期/无 expiry）时刷新并写回。
// 返回可用于出站的凭据（可能原样返回）。channelId<=0 时（单元测试/mock 无渠道上下文）
// 跳过持久化，仅做进程内单飞刷新并返回新凭据。
func RefreshIfNeeded(channelId int, cred *Cred) (*Cred, error) {
	if !cred.NeedsRefresh(refreshSkew) {
		return cred, nil
	}
	return refreshSingleflight(channelId, cred)
}

// refreshSingleflight 进程内单飞：首飞执行，其余等待复用结果。
func refreshSingleflight(channelId int, cred *Cred) (*Cred, error) {
	key := channelId
	if key <= 0 {
		// 无渠道上下文：按凭据 uid 哈希负键，仍保证同账号串行。
		key = -int(hashString(cred.UID + "|" + cred.RefreshToken[:minInt(len(cred.RefreshToken), 8)]))
		if key == 0 {
			key = -1
		}
	}
	refreshMu.Lock()
	if f, ok := refreshInFlight[key]; ok {
		refreshMu.Unlock()
		<-f.done
		return f.cred, f.err
	}
	f := &refreshFlight{done: make(chan struct{})}
	refreshInFlight[key] = f
	refreshMu.Unlock()

	newCred, err := refreshWithDistributedLock(channelId, cred)

	f.cred, f.err = newCred, err
	close(f.done)
	refreshMu.Lock()
	delete(refreshInFlight, key)
	refreshMu.Unlock()
	return newCred, err
}

// refreshWithDistributedLock 多节点互斥刷新本体。
func refreshWithDistributedLock(channelId int, cred *Cred) (*Cred, error) {
	if channelId > 0 {
		// 进程内渠道锁：串行化 DB 写回（与 SaveCred 约定）。
		lock := ChannelLock(channelId)
		lock.Lock()
		defer lock.Unlock()
		// 重读 DB：另一 goroutine/节点可能刚刷新完。
		if latest, err := LoadChannelCred(channelId); err == nil && !latest.NeedsRefresh(refreshSkew) {
			return latest, nil
		} else if err == nil {
			cred = latest // 用 DB 里的最新 RT（避免拿陈旧快照撞反重放）
		}
	}
	if common.RedisEnabled && channelId > 0 {
		return refreshWithRedisLock(channelId, cred)
	}
	return doRefreshAndSave(channelId, cred)
}

// redisRefreshLockTTL 分布式锁持有上限（刷新 I/O 30s + 余量）。
const redisRefreshLockTTL = 45 * time.Second

// refreshWithRedisLock SETNX 分布式锁版本：拿不到锁的节点轮询 DB 等写回。
func refreshWithRedisLock(channelId int, cred *Cred) (*Cred, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	lockKey := fmt.Sprintf("wb:refresh:%d", channelId)
	acquired, err := common.RDB.SetNX(ctx, lockKey, 1, redisRefreshLockTTL).Result()
	if err == nil && acquired {
		defer common.RDB.Del(ctx, lockKey)
		return doRefreshAndSave(channelId, cred)
	}
	// 未拿到锁：轮询 DB 等持锁节点写回（最长 ~12s）。
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(800 * time.Millisecond)
		if latest, lerr := LoadChannelCred(channelId); lerr == nil && !latest.NeedsRefresh(refreshSkew) {
			return latest, nil
		}
	}
	// 兜底：锁可能已失效/持锁节点崩溃，自行刷新（反重放风险由 doJSON 的 12153 分类暴露）。
	return doRefreshAndSave(channelId, cred)
}

// doRefreshAndSave 执行刷新并写回 channels.key（调用方已保证串行）。
func doRefreshAndSave(channelId int, cred *Cred) (*Cred, error) {
	client := NewClient(cred)
	newCred, err := client.refreshTokenOnce()
	if err != nil {
		return nil, err
	}
	if channelId > 0 {
		if err := SaveCred(channelId, newCred); err != nil {
			common.SysLog("workbuddy refresh persist failed: " + err.Error())
			return nil, err
		}
	}
	return newCred, nil
}

func hashString(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

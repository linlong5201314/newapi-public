// scheduler.go 签到中心平台级调度器：
// 每日 09:05 / 21:05 CST sweep 全部 enabled 账号（±15min 确定性抖动防整点风暴）；
// 多节点互斥 Redis SETNX checkin:sweep:<day>:<slot>（无 Redis 仅 master 节点跑）；
// 判重 LastCheckinDay == today 跳过；失败当日补跑 ≤2 次（距上次 ≥30min）。
package checkin

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// sweepSlots 每日 sweep 槽位（CST 时:分）。
var sweepSlots = []struct{ hour, min int }{{9, 5}, {21, 5}}

// sweepLockTTL sweep 槽位锁持有时间（槽位窗口）。
const sweepLockTTL = 2 * time.Hour

// retryMinInterval 失败补跑最小间隔。
const retryMinInterval = 30 * time.Minute

// maxDailyRetries 失败当日补跑上限（FailCount > 2 当日放弃）。
const maxDailyRetries = 2

// sweepPageSize sweep 分页大小。
const sweepPageSize = 200

// StartScheduler 启动调度器（阻塞循环；main.go goroutine 调用）。
func StartScheduler(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	common.SysLog("checkin scheduler started (slots 09:05/21:05 CST, jitter ±15min)")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			common.SysLog("checkin scheduler stopped")
			return
		case <-ticker.C:
			if !operation_setting.CheckinEnabled {
				continue
			}
			tick(ctx)
		}
	}
}

// tick 每个 30s 节拍：对落在窗口内的槽位尝试取锁并 sweep。
func tick(ctx context.Context) {
	now := time.Now().In(cst)
	for _, slot := range sweepSlots {
		fire := time.Date(now.Year(), now.Month(), now.Day(), slot.hour, slot.min, 0, 0, cst)
		if now.Before(fire) || now.After(fire.Add(sweepLockTTL)) {
			continue // 未到槽位 / 已过窗口
		}
		day := now.Format("2006-01-02")
		slotKey := fmt.Sprintf("%02d%02d", slot.hour, slot.min)
		if !acquireSweepLock(ctx, day, slotKey) {
			continue
		}
		sweep(ctx, day, slotKey, fire, now)
	}
}

// acquireSweepLock 多节点互斥：Redis SETNX（无 Redis 则仅 master 节点）。
// 锁按 (day, slot) 一次性：拿到锁的节点负责整个槽位的 sweep；锁 TTL 覆盖窗口。
var sweptMarks = struct {
	mu sync.Mutex
	m  map[string]bool
}{m: map[string]bool{}}

func acquireSweepLock(ctx context.Context, day, slot string) bool {
	key := fmt.Sprintf("checkin:sweep:%s:%s", day, slot)
	if common.RedisEnabled {
		ok, err := common.RDB.SetNX(ctx, key, 1, sweepLockTTL).Result()
		if err != nil {
			common.SysLog("checkin sweep lock error: " + err.Error())
			return false
		}
		return ok
	}
	if !common.IsMasterNode {
		return false
	}
	// 无 Redis 单 master：进程内标记防重复 sweep（重启重置是可接受的——判重有 DB 兜底）。
	sweptMarks.mu.Lock()
	defer sweptMarks.mu.Unlock()
	if sweptMarks.m[key] {
		return false
	}
	sweptMarks.m[key] = true
	return true
}

// sweep 执行一个槽位的全量账号扫描。
func sweep(ctx context.Context, day, slot string, slotFire, now time.Time) {
	count := 0
	for offset := 0; ; offset += sweepPageSize {
		accounts, err := model.GetEnabledCheckinAccounts(offset, sweepPageSize)
		if err != nil {
			common.SysLog("checkin sweep query failed: " + err.Error())
			return
		}
		if len(accounts) == 0 {
			break
		}
		for i := range accounts {
			acc := &accounts[i]
			if !shouldRun(acc, day, slotFire, now) {
				continue
			}
			runAccount(acc, "cron")
			count++
		}
		if len(accounts) < sweepPageSize {
			break
		}
	}
	if count > 0 {
		common.SysLog(fmt.Sprintf("checkin sweep %s:%s done, executed %d accounts", day, slot, count))
	}
}

// shouldRun 判定账号本轮是否应执行：
//   - 确定性抖动：slotFire + jitter(accountId, day, slot) ≤ now（±15min）；
//   - 判重：LastCheckinDay == today 且 LastStatus != failed → 跳过；
//   - 失败补跑：FailCount ≤ 2 且距上次更新 ≥30min。
func shouldRun(acc *model.CheckinAccount, day string, slotFire, now time.Time) bool {
	today := now.Format("2006-01-02")
	if acc.LastCheckinDay == today && acc.LastStatus != "failed" {
		return false
	}
	if acc.LastStatus == "failed" {
		if acc.FailCount > maxDailyRetries {
			return false
		}
		if time.Since(time.Unix(acc.UpdatedTime, 0)) < retryMinInterval {
			return false
		}
		// 失败补跑不看抖动窗口（尽快补跑，仍在槽位窗口内）。
		return true
	}
	jitter := jitterMinutes(acc.Id, day, slotFire)
	return !now.Before(slotFire.Add(jitter))
}

// jitterMinutes 确定性抖动（同一账号同一槽位同一天恒定，±15min）。
func jitterMinutes(accountId int, day string, slotFire time.Time) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(fmt.Sprintf("%d|%s|%02d%02d", accountId, day, slotFire.Hour(), slotFire.Minute())))
	offset := int(h.Sum32()%31) - 15 // [-15, +15] 分钟
	return time.Duration(offset) * time.Minute
}

// SweepAllNow 立即对全部 enabled 账号做一轮 sweep（判重仍生效；运维/e2e 用，
// 不走槽位锁与抖动窗口）。返回执行账号数。
func SweepAllNow() int {
	now := time.Now().In(cst)
	day := now.Format("2006-01-02")
	count := 0
	for offset := 0; ; offset += sweepPageSize {
		accounts, err := model.GetEnabledCheckinAccounts(offset, sweepPageSize)
		if err != nil {
			common.SysLog("checkin sweep-now query failed: " + err.Error())
			return count
		}
		if len(accounts) == 0 {
			break
		}
		for i := range accounts {
			acc := &accounts[i]
			if acc.LastCheckinDay == day && acc.LastStatus != "failed" {
				continue
			}
			if acc.LastStatus == "failed" && acc.FailCount > maxDailyRetries {
				continue
			}
			runAccount(acc, "cron")
			count++
		}
		if len(accounts) < sweepPageSize {
			break
		}
	}
	return count
}

// Package checkin M3 签到中心：调度器 + 站点实现（workbuddy/trae/apinex/custom）。
//
// 平台级 cron：每日 09:05 / 21:05 CST sweep 全部 enabled 账号（±15min 抖动防整点风暴）。
// 多节点互斥：Redis SETNX checkin:sweep:<day>:<slot>（无 Redis 则仅 master 节点跑）。
// 判重：LastCheckinDay == today(CST) 跳过；失败当日补跑 ≤2 次。
package checkin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// cst CST 时区（UTC+8 无夏令时，FixedZone 免依赖系统 tzdata）。
var cst = time.FixedZone("CST", 8*3600)

// todayCST 返回当前 CST 日期（YYYY-MM-DD）。
func todayCST() string {
	return time.Now().In(cst).Format("2006-01-02")
}

// RunResult 一次账号执行的归一化结果。
type RunResult struct {
	Success    bool
	Skipped    bool   // 判重跳过（非失败）
	Message    string // 摘要（进 LastResult / CheckinLog.Detail）
	Reward     int64  // 本次累计到账积分/额度
	Streak     int    // 最新连签天数（0=站点不提供）
	Balance    int64  // 查询到的最新余额（<0 表示未查询）
	RetryAfter time.Duration
}

// runAccount 执行单个签到账号：解析站点实现 → 运行 → 落库（状态/日志/判重/失败计数）。
// trigger 仅用于日志（"cron" | "manual"）。
func runAccount(acc *model.CheckinAccount, trigger string) *RunResult {
	return runAccountWithContext(context.Background(), acc, trigger)
}

func runAccountWithContext(ctx context.Context, acc *model.CheckinAccount, trigger string) *RunResult {
	select {
	case accountRunSlots <- struct{}{}:
		defer func() { <-accountRunSlots }()
	case <-ctx.Done():
		return &RunResult{Message: "签到任务已取消", Balance: -1, RetryAfter: time.Minute}
	}
	if acc == nil {
		return &RunResult{Message: "签到账号不存在", Balance: -1, RetryAfter: time.Minute}
	}
	token := common.GetUUID()
	originalCred := acc.CredJson
	if err := model.ClaimCheckinAccount(acc, token, time.Now(), 2*time.Minute); err != nil {
		return &RunResult{Message: "签到任务正在其他节点执行", Balance: -1, RetryAfter: time.Minute}
	}
	leaseCtx, stopLease := context.WithCancel(ctx)
	defer stopLease()
	leaseLost := make(chan struct{})
	var leaseLostOnce sync.Once
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				if err := model.RenewCheckinAccountLease(acc, time.Now(), 2*time.Minute); err != nil {
					leaseLostOnce.Do(func() { close(leaseLost) })
					return
				}
			}
		}
	}()
	var res *RunResult
	defer func() {
		stopLease()
		if recovered := recover(); recovered != nil {
			res = &RunResult{Success: false, Message: "签到任务执行失败", Balance: -1}
		}
		if res == nil {
			res = &RunResult{Success: false, Message: "签到任务执行失败", Balance: -1}
		}
		if res.RetryAfter > 0 && !res.Success && !res.Skipped {
			// The result writer uses the same account fence; delayed retries are
			// also visible to other nodes through UpdatedTime.
		}
		persistCheckinResult(acc, trigger, res, originalCred)
		if errors.Is(model.ReleaseCheckinAccountLease(acc), model.ErrCheckinLeaseLost) {
			common.SysLog("checkin: stale execution could not release a renewed lease")
		}
	}()
	select {
	case <-leaseLost:
		res = &RunResult{Success: false, Message: "签到任务租约已失效", Balance: -1, RetryAfter: time.Minute}
		return res
	default:
	}
	res = execute(acc)
	return res
}

func persistCheckinResult(acc *model.CheckinAccount, trigger string, res *RunResult, originalCred string) {
	now := todayCST()
	renewedCred := ""
	if acc.CredJson != originalCred {
		renewedCred = acc.CredJson
	}
	// Credential renewal shares the result transaction and lease fence. A stale
	// worker cannot persist its cookie after another node has claimed the account.
	acc.LastResult = truncate(res.Message, 500)
	if res.Reward > 0 {
		acc.TotalReward += res.Reward
	}
	if res.Streak > 0 {
		acc.Streak = res.Streak
	}
	switch {
	case res.Skipped:
		acc.LastStatus = "skipped"
		acc.LastCheckinDay = now
		acc.FailCount = 0
	case res.Success:
		acc.LastStatus = "ok"
		acc.LastCheckinDay = now
		acc.FailCount = 0
	default:
		acc.LastStatus = "failed"
		acc.RetryAfterUntil = 0
		if res.RetryAfter > 0 {
			acc.RetryAfterUntil = time.Now().Add(res.RetryAfter).Unix()
		}
		if acc.LastCheckinDay != now {
			acc.FailCount = 0
			acc.LastCheckinDay = now
		}
		acc.FailCount++
		// 失败也记判重日期？不记——允许当日补跑（≤2 次）。
	}
	detail := fmt.Sprintf("[%s] %s", trigger, truncate(res.Message, 800))
	if err := model.SaveCheckinRunResult(acc, detail, renewedCred); err != nil {
		common.SysLog("checkin: save run result failed: " + err.Error())
	}
}

// execute 按站点分发执行。
func execute(acc *model.CheckinAccount) *RunResult {
	site := strings.ToLower(strings.TrimSpace(acc.Site))
	mode := strings.ToLower(strings.TrimSpace(acc.Mode))
	if mode == "agent" {
		return runAgent(acc)
	}
	switch site {
	case "workbuddy":
		return runWorkbuddy(acc)
	case "trae":
		return runTrae(acc)
	case "qoder":
		return runQoder(acc)
	case "custom":
		return runCustom(acc)
	case "apinex":
		return &RunResult{Success: false, Message: "apinex 站点仅支持 agent 模式（云机执行器过 Turnstile）"}
	default:
		return &RunResult{Success: false, Message: "unknown site: " + acc.Site}
	}
}

var manualUserRuns sync.Map

// RunAccountManual 手动触发入口（controller 用）：判重豁免——手动触发视为「当日补跑」，
// 但同日已成功/跳过的账号直接返回 skipped（幂等）。
func RunAccountManual(acc *model.CheckinAccount) *RunResult {
	if acc.LastCheckinDay == todayCST() && acc.LastStatus != "failed" {
		return &RunResult{Success: true, Skipped: true, Message: "今日已签到（判重跳过）", Balance: -1}
	}
	if _, loaded := manualUserRuns.LoadOrStore(acc.UserId, struct{}{}); loaded {
		return &RunResult{Message: "已有手动签到任务正在执行", Balance: -1, RetryAfter: 5 * time.Second}
	}
	defer manualUserRuns.Delete(acc.UserId)
	return runAccount(acc, "manual")
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

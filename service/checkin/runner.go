// Package checkin M3 签到中心：调度器 + 站点实现（workbuddy/trae/apinex/custom）。
//
// 平台级 cron：每日 09:05 / 21:05 CST sweep 全部 enabled 账号（±15min 抖动防整点风暴）。
// 多节点互斥：Redis SETNX checkin:sweep:<day>:<slot>（无 Redis 则仅 master 节点跑）。
// 判重：LastCheckinDay == today(CST) 跳过；失败当日补跑 ≤2 次。
package checkin

import (
	"fmt"
	"strings"
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
	originalCred := acc.CredJson
	res := execute(acc)
	now := todayCST()
	// 站点执行器可能回写续期凭据（agent cookie）；凭据变更单独持久化，
	// SaveCheckinRunResult 只写运行状态字段，避免每次 sweep 重写凭据。
	if acc.CredJson != originalCred {
		if err := model.UpdateCheckinAccountCred(acc.UserId, acc.Id, acc.CredJson); err != nil {
			common.SysLog("checkin: persist renewed credential failed: " + err.Error())
		}
	}

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
		acc.FailCount++
		// 失败也记判重日期？不记——允许当日补跑（≤2 次）。
	}
	if err := model.SaveCheckinRunResult(acc); err != nil {
		common.SysLog("checkin: save run result failed: " + err.Error())
	}
	_ = model.InsertCheckinLog(&model.CheckinLog{
		UserId:    acc.UserId,
		AccountId: acc.Id,
		Site:      acc.Site,
		Action:    "checkin",
		Success:   res.Success || res.Skipped,
		Detail:    fmt.Sprintf("[%s] %s", trigger, truncate(res.Message, 800)),
	})
	return res
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
	case "custom":
		return runCustom(acc)
	case "apinex":
		return &RunResult{Success: false, Message: "apinex 站点仅支持 agent 模式（云机执行器过 Turnstile）"}
	default:
		return &RunResult{Success: false, Message: "unknown site: " + acc.Site}
	}
}

// RunAccountManual 手动触发入口（controller 用）：判重豁免——手动触发视为「当日补跑」，
// 但同日已成功/跳过的账号直接返回 skipped（幂等）。
func RunAccountManual(acc *model.CheckinAccount) *RunResult {
	if acc.LastCheckinDay == todayCST() && acc.LastStatus != "failed" {
		return &RunResult{Success: true, Skipped: true, Message: "今日已签到（判重跳过）", Balance: -1}
	}
	return runAccount(acc, "manual")
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

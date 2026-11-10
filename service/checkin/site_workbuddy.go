// site_workbuddy.go WorkBuddy 站点签到（api 模式）：
// 每日签到 + 连登管家闭环（补签卡/礼包/补偿/streak 兑换/抽奖）+
// 任务 accept+claim 幂等清扫（25 个行为链任务一期只做无行为类，行为链类标记跳过）。
package checkin

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/workbuddy"
)

// behaviorChainTaskPrefixes 行为链类任务前缀（expert/template/canvas 等需要真实客户端
// 行为轨迹的任务；一期标记「暂不支持」，展示但不执行）。
var behaviorChainTaskMarkers = []string{
	"expert", "template", "canvas", "Sequential_Tasks", "school_season",
	"wb_wechat", "black_cat", "travel", "buddy", "RichMeow",
}

// isBehaviorChainTask 判定任务是否行为链类（需要客户端行为轨迹，网关在签到上下文不执行）。
// 纯上报类（report 活跃）与模型体验类不在此列。
func isBehaviorChainTask(t workbuddy.Task) bool {
	code := strings.ToLower(t.TaskCode)
	for _, m := range behaviorChainTaskMarkers {
		if strings.Contains(code, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// runWorkbuddy 执行 workbuddy 站点签到全流程。
func runWorkbuddy(acc *model.CheckinAccount) *RunResult {
	cred, err := resolveWorkbuddyCred(acc)
	if err != nil {
		return &RunResult{Success: false, Message: err.Error(), Balance: -1}
	}
	client := workbuddy.NewClient(cred)
	var (
		reward   int64
		streak   int
		notes    []string
		critical error // 致命错误（签到本体失败/凭据失效）
	)
	addNote := func(format string, args ...any) {
		notes = append(notes, fmt.Sprintf(format, args...))
	}

	// 0. 企业版/企业切换会话：成长体系不可用——只做余额刷新后返回。
	if cred.IsEnterprise() || cred.IsEnterpriseSession() {
		remain, _, balErr := workbuddy.RefreshBalance(acc.ChannelId, cred)
		if balErr != nil {
			return &RunResult{Success: false, Message: "企业账号余额刷新失败: " + balErr.Error(), Balance: -1}
		}
		ReenableIfCredits(acc, remain)
		return &RunResult{Success: true, Message: fmt.Sprintf("企业账号跳过成长体系，余额 %d", remain), Balance: remain}
	}

	// 1. 每日签到（「已签到」幂等成功）。
	if err := client.DailyCheckin(); err != nil {
		if workbuddy.IsAlreadyCheckin(err) {
			addNote("今日已签到")
		} else {
			critical = fmt.Errorf("每日签到失败: %w", err)
		}
	} else {
		addNote("签到成功")
	}

	// 2. 连登管家闭环（best-effort：单项失败不致命，进摘要）。
	// 2a. 补签卡：昨日漏签且有卡 → 补签保住连登。
	if missed, err := client.HeatmapYesterdayMissed(); err == nil && missed {
		if sf, serr := client.GrowthStreakFull(); serr == nil && sf.MakeupCards.Balance > 0 {
			yesterday := yesterdayCST()
			if uerr := client.UseMakeupCard(yesterday); uerr == nil {
				addNote("补签卡补签昨日")
			}
		}
	}
	// 2b. 新手礼包（每号一次；已领业务错误静默）。
	if credit, err := client.ClaimGift(); err == nil && credit > 0 {
		reward += credit
		addNote("礼包 +%d", credit)
	}
	// 2c. 官方补偿（先查状态，可领且未领才领）。
	if st, err := client.GetCompensationStatus(); err == nil && st.Pending() {
		if credit, cerr := client.ClaimCompensation(); cerr == nil && credit > 0 {
			reward += credit
			addNote("补偿 +%d", credit)
		}
	}
	// 2d. streak 档位兑换（7d/14d/28d；未解锁上游 403 静默跳过）。
	if sf, err := client.GrowthStreakFull(); err == nil {
		streak = sf.Streak.Days
		for _, t := range []struct {
			tier   string
			status string
		}{
			{"7d", sf.RedemptionStatus.Tier7dStatus},
			{"14d", sf.RedemptionStatus.Tier14dStatus},
			{"28d", sf.RedemptionStatus.Tier28dStatus},
		} {
			if t.status == "available" || t.status == "unlocked" || t.status == "claimable" {
				if rerr := client.GrowthRedeemTier(t.tier); rerr == nil {
					addNote("连登%s 已兑换", t.tier)
				}
			}
		}
	}
	// 2e. 抽奖（有 chances 全抽）。
	if chances, err := client.LotteryChances(); err == nil && chances > 0 {
		drawn := 0
		for i := 0; i < chances && i < 10; i++ {
			if _, derr := client.LotteryDraw(); derr != nil {
				break
			}
			drawn++
		}
		if drawn > 0 {
			addNote("抽奖 ×%d", drawn)
		}
	}

	// 3. 活跃上报（点亮连登 + 解锁 first_buddy；每号每天 1 次）。
	if err := client.ReportChatActivity(fmt.Sprintf("checkin-%d", common.GetTimestamp()), ""); err == nil {
		addNote("活跃上报")
	}

	// 4. 任务 accept + claim 幂等清扫（无行为类）。
	tasks, terr := client.ListTasks()
	if terr != nil {
		addNote("任务列表失败: %v", terr)
	} else {
		tasks = dedupTasks(tasks)
		var acceptCodes []string
		for _, t := range tasks {
			if t.Locked || t.Claimed || isBehaviorChainTask(t) {
				continue
			}
			if t.AcceptStatus == "not_accepted" || t.AcceptStatus == "" {
				acceptCodes = append(acceptCodes, t.TaskCode)
			}
		}
		if len(acceptCodes) > 0 {
			if err := client.AcceptTasks(acceptCodes); err == nil {
				addNote("接受任务 ×%d", len(acceptCodes))
			}
		}
		// 回读（accept 后进度由服务端行为事件点亮；这里只领已达标的）。
		claimedCount := 0
		for _, t := range tasks {
			if t.Claimed || !t.Claimable || isBehaviorChainTask(t) {
				continue
			}
			credit, energy, cerr := client.ClaimReward(t.TaskCode)
			if cerr != nil {
				continue // 单个失败不阻塞（幂等设计，下轮再领）
			}
			if credit > 0 || energy > 0 {
				reward += credit
				claimedCount++
			}
		}
		if claimedCount > 0 {
			addNote("任务领奖 ×%d", claimedCount)
		}
		// 行为链任务统计（展示「暂不支持」）。
		behavior := 0
		for _, t := range tasks {
			if isBehaviorChainTask(t) && !t.Claimed {
				behavior++
			}
		}
		if behavior > 0 {
			addNote("行为链任务 ×%d（暂不支持，已跳过）", behavior)
		}
	}

	// 5. 余额刷新 + 解冻（签到后紧邻查询）。
	remain, _, balErr := workbuddy.RefreshBalance(acc.ChannelId, cred)
	balance := int64(-1)
	if balErr == nil {
		balance = remain
		ReenableIfCredits(acc, remain)
		addNote("余额 %d", remain)
	} else {
		addNote("余额查询失败: %v", balErr)
	}

	if critical != nil {
		return &RunResult{Success: false, Message: critical.Error() + "；" + strings.Join(notes, "；"), Reward: reward, Streak: streak, Balance: balance}
	}
	msg := strings.Join(notes, "；")
	if msg == "" {
		msg = "完成（无可执行项）"
	}
	return &RunResult{Success: true, Message: msg, Reward: reward, Streak: streak, Balance: balance}
}

// resolveWorkbuddyCred 解析账号凭据：ChannelId>0 引用渠道（校验 owner）→ 渠道 key；
// 否则 CredJson 独立凭据。渠道引用时顺带做临期刷新（写回渠道）。
func resolveWorkbuddyCred(acc *model.CheckinAccount) (*workbuddy.Cred, error) {
	if acc.ChannelId > 0 {
		ch, err := model.GetChannelById(acc.ChannelId, true)
		if err != nil || ch == nil || ch.OwnerId != acc.UserId {
			return nil, fmt.Errorf("引用的渠道不存在或不属于当前用户")
		}
		key := model.DecryptChannelKey(ch.Type, ch.Key)
		if ch.ChannelInfo.IsMultiKey {
			keys := ch.GetKeys()
			if len(keys) > 0 {
				key = keys[0]
			}
		}
		cred, err := workbuddy.ParseCred(key)
		if err != nil {
			return nil, fmt.Errorf("渠道凭据解析失败: %w", err)
		}
		refreshed, err := workbuddy.RefreshIfNeeded(acc.ChannelId, cred)
		if err != nil {
			return nil, fmt.Errorf("token 刷新失败: %w", err)
		}
		return refreshed, nil
	}
	credJson := model.DecryptCheckinCred(acc.CredJson)
	if strings.TrimSpace(credJson) == "" {
		return nil, fmt.Errorf("未配置凭据（引用渠道或独立凭据二选一）")
	}
	return workbuddy.ParseCred(credJson)
}

// dedupTasks 按 task_code 去重（mp 超集合并场景）。
func dedupTasks(tasks []workbuddy.Task) []workbuddy.Task {
	seen := map[string]bool{}
	out := make([]workbuddy.Task, 0, len(tasks))
	for _, t := range tasks {
		if t.TaskCode == "" || seen[t.TaskCode] {
			continue
		}
		seen[t.TaskCode] = true
		out = append(out, t)
	}
	return out
}

// yesterdayCST 昨日 CST 日期。
func yesterdayCST() string {
	return time.Now().AddDate(0, 0, -1).In(cst).Format("2006-01-02")
}

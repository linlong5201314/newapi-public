// site_trae.go Trae 站点签到（api 模式）：checkin status→claim（+200/日）+
// EntUsage 刷新渠道 Balance + 有分解冻。
// 移植自 _research/trae2api-web-main internal/scheduler（池 → 单账号）。
package checkin

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/trae"
)

// runTrae 执行 trae 站点签到全流程。
func runTrae(acc *model.CheckinAccount) *RunResult {
	cred, err := resolveTraeCred(acc)
	if err != nil {
		return &RunResult{Success: false, Message: err.Error(), Balance: -1}
	}
	client := trae.NewClient(cred)
	var notes []string
	addNote := func(format string, args ...any) {
		notes = append(notes, fmt.Sprintf(format, args...))
	}

	// 1. status → 未签且 enable → claim（9074 拥挤做一次有界重试）。
	checkedIn, credits, enable, err := client.CheckinStatus()
	if err != nil {
		return &RunResult{Success: false, Message: "签到状态查询失败: " + err.Error(), Balance: -1}
	}
	if checkedIn {
		addNote("今日已签到")
	} else if !enable {
		addNote("签到活动未开启")
	} else {
		claimErr := client.CheckinClaim()
		if claimErr != nil && isBusyErr(claimErr) {
			claimErr = client.CheckinClaim() // 9074 拥挤：立即重试一次
		}
		if claimErr != nil {
			return &RunResult{Success: false, Message: "签到领取失败: " + claimErr.Error(), Balance: -1}
		}
		addNote("签到 +200")
	}

	// 2. EntUsage 刷新渠道 Balance + 有分解冻。
	remain, balErr := trae.RefreshBalance(acc.ChannelId, cred)
	if balErr != nil {
		return &RunResult{Success: false, Message: "积分查询失败: " + balErr.Error(), Balance: -1}
	}
	ReenableIfCredits(acc, remain)
	addNote("余额 %d", remain)
	if credits > 0 && credits != remain {
		// status 帧的 credits 与 EntUsage 口径可能不同（ide 包 vs 聚合），以 EntUsage 为准。
		addNote("（status 口径 %d）", credits)
	}
	return &RunResult{Success: true, Message: strings.Join(notes, "；"), Reward: 0, Balance: remain}
}

// isBusyErr 判定是否签到拥挤（9074）错误。
func isBusyErr(err error) bool {
	return strings.Contains(err.Error(), "9074")
}

// resolveTraeCred 解析账号凭据（同 workbuddy 双源：引用渠道 | 独立凭据）。
func resolveTraeCred(acc *model.CheckinAccount) (*trae.Cred, error) {
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
		cred, err := trae.ParseCred(key)
		if err != nil {
			return nil, fmt.Errorf("渠道凭据解析失败: %w", err)
		}
		refreshed, err := trae.RefreshIfNeeded(acc.ChannelId, cred)
		if err != nil {
			return nil, fmt.Errorf("token 刷新失败: %w", err)
		}
		return refreshed, nil
	}
	credJson := model.DecryptCheckinCred(acc.CredJson)
	if strings.TrimSpace(credJson) == "" {
		return nil, fmt.Errorf("未配置凭据（引用渠道或独立凭据二选一）")
	}
	return trae.ParseCred(credJson)
}

// ReenableIfCredits 余额 > 0 时解冻「本用户自动禁用」的引用渠道
// （签到/余额刷新后调用；手动禁用的渠道不动）。
func ReenableIfCredits(acc *model.CheckinAccount, balance int64) {
	if acc.ChannelId <= 0 || balance <= 0 {
		return
	}
	ch, err := model.GetChannelById(acc.ChannelId, true)
	if err != nil || ch == nil || ch.OwnerId != acc.UserId {
		return
	}
	if ch.Status != common.ChannelStatusAutoDisabled {
		return
	}
	if model.UpdateChannelStatus(acc.ChannelId, "", common.ChannelStatusEnabled, "") {
		_ = model.InsertCheckinLog(&model.CheckinLog{
			UserId:    acc.UserId,
			AccountId: acc.Id,
			Site:      acc.Site,
			Action:    "reenable",
			Success:   true,
			Detail:    fmt.Sprintf("余额 %d > 0，渠道 #%d 自动解冻", balance, acc.ChannelId),
		})
	}
}

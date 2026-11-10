// tasks.go WorkBuddy 成长体系：每日签到外的任务中心/连登管家闭环/活跃上报。
// 移植自 _research/workbuddy2api-panel-main internal/upstream/{tasks,streak,blackcat,compensation,report}.go。
package workbuddy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// growth 域任务路径。
const (
	tasksListPath   = "/v2/activity/growth/tasks"
	tasksAcceptPath = "/v2/activity/growth/tasks/accept"
	streakPath      = "/activity/growth/streak"
)

// 连登管家闭环路径。
const (
	streakRedeemPath   = "/activity/growth/redeem"
	lotterySummaryPath = "/activity/growth/lottery/summary"
	lotteryDrawPath    = "/activity/growth/lottery/draw"
	heatmapPath        = "/activity/growth/heatmap"
	makeupCardUsePath  = "/activity/growth/makeup-cards/use"
)

// Task 单个任务的对外视图。
type Task struct {
	TaskCode     string `json:"task_code"`
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	TaskDesc     string `json:"task_desc,omitempty"`
	Credit       int64  `json:"credit,omitempty"`
	Energy       int64  `json:"energy,omitempty"`
	HasReward    bool   `json:"has_reward,omitempty"`
	TaskType     string `json:"task_type,omitempty"`
	Tag          string `json:"tag,omitempty"`
	JumpURL      string `json:"jump_url,omitempty"`
	Locked       bool   `json:"locked,omitempty"`
	Target       int64  `json:"target"`
	Current      int64  `json:"current"`
	AcceptStatus string `json:"accept_status,omitempty"`
	Status       string `json:"status,omitempty"`
	Claimable    bool   `json:"claimable,omitempty"`
	Claimed      bool   `json:"claimed,omitempty"`
}

// ListTasks 拉取全量任务列表（默认口径）。
func (c *Client) ListTasks() ([]Task, error) {
	data, err := c.growthJSON(http.MethodGet, tasksListPath, nil)
	if err != nil {
		return nil, err
	}
	return parseGrowthTasks(data)
}

// ListTasksMP 小程序口径任务列表（实测为默认口径超集；合并按 task_code 去重）。
func (c *Client) ListTasksMP() ([]Task, error) {
	data, err := c.growthJSONMP(http.MethodGet, tasksListPath, nil)
	if err != nil {
		return nil, err
	}
	return parseGrowthTasks(data)
}

// parseGrowthTasks 解析 growth 任务列表 data.tasks[]（宽松解析两种 progress 形态）。
func parseGrowthTasks(data json.RawMessage) ([]Task, error) {
	var resp struct {
		Tasks []struct {
			TaskCode     string          `json:"task_code"`
			Title        string          `json:"title"`
			Description  string          `json:"description"`
			TaskDesc     string          `json:"task_desc"`
			RewardCredit int64           `json:"reward_credit"`
			RewardEnergy int64           `json:"reward_energy"`
			HasReward    bool            `json:"has_reward"`
			TaskType     string          `json:"task_type"`
			Tag          string          `json:"tag"`
			JumpURL      string          `json:"jump_url"`
			Locked       bool            `json:"locked"`
			AcceptStatus string          `json:"accept_status"`
			Status       string          `json:"status"`
			Target       int64           `json:"target"`
			Current      int64           `json:"current"`
			Progress     json.RawMessage `json:"progress"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	out := make([]Task, 0, len(resp.Tasks))
	for _, t := range resp.Tasks {
		cur, tgt := t.Current, t.Target
		if len(t.Progress) > 0 && string(t.Progress) != "null" {
			var pr struct {
				Current int64 `json:"current"`
				Target  int64 `json:"target"`
			}
			if json.Unmarshal(t.Progress, &pr) == nil && (pr.Target > 0 || pr.Current > 0) {
				cur, tgt = pr.Current, pr.Target
			}
		}
		claimed := t.AcceptStatus == "claimed"
		out = append(out, Task{
			TaskCode:     t.TaskCode,
			Title:        t.Title,
			Description:  t.Description,
			TaskDesc:     t.TaskDesc,
			Credit:       t.RewardCredit,
			Energy:       t.RewardEnergy,
			HasReward:    t.HasReward,
			TaskType:     t.TaskType,
			Tag:          t.Tag,
			JumpURL:      t.JumpURL,
			Locked:       t.Locked,
			Target:       tgt,
			Current:      cur,
			AcceptStatus: t.AcceptStatus,
			Status:       t.Status,
			Claimable:    !claimed && tgt > 0 && cur >= tgt,
			Claimed:      claimed,
		})
	}
	return out, nil
}

// AcceptTasks 接受任务（幂等）。
func (c *Client) AcceptTasks(taskCodes []string) error {
	_, err := c.growthJSON(http.MethodPost, tasksAcceptPath, map[string]any{"task_codes": taskCodes})
	return err
}

// AcceptTasksMP 接受小程序限定任务（mp 头；幂等）。
func (c *Client) AcceptTasksMP(taskCodes []string) error {
	_, err := c.growthJSONMP(http.MethodPost, tasksAcceptPath, map[string]any{"task_codes": taskCodes})
	return err
}

// ClaimReward 领取单个任务奖励：Web 成长中心领奖端点
// POST {webBase}/activity/growth/tasks/<code>/claim（x-client-platform: web）。
// 返回 (credit, energy, err)；已领取（already_claimed）幂等返回 (0,0,nil)。
func (c *Client) ClaimReward(taskCode string) (credit, energy int64, err error) {
	req, err := http.NewRequest(http.MethodPost,
		c.webBase()+"/activity/growth/tasks/"+url.PathEscape(taskCode)+"/claim", nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cred.AccessToken)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://www.workbuddy.cn")
	req.Header.Set("Referer", "https://www.workbuddy.cn/profile/growth-center")
	req.Header.Set("x-client-platform", "web")
	req.Header.Set("User-Agent", defaultWorkBuddyUAFor(c.cred))
	if c.cred.UID != "" {
		req.Header.Set("X-User-Id", c.cred.UID)
	}
	if c.cred.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", c.cred.EnterpriseID)
		req.Header.Set("X-Tenant-Id", c.cred.EnterpriseID)
	}
	if c.cred.Domain != "" {
		req.Header.Set("X-Domain", c.cred.Domain)
	}
	data, err := c.doJSON(req)
	if err != nil {
		return 0, 0, err
	}
	return parseClaimReward(data)
}

// ClaimRewardMP 领取小程序限定任务奖励：chat 域 + mp 头，400 时降级 Web 域。
func (c *Client) ClaimRewardMP(taskCode string) (credit, energy int64, err error) {
	req, err := http.NewRequest(http.MethodPost,
		c.chatBase()+"/activity/growth/tasks/"+url.PathEscape(taskCode)+"/claim", nil)
	if err != nil {
		return 0, 0, err
	}
	BillingHeaders(req, c.cred)
	req.Header.Set("X-Client-Platform", "miniprogram")
	data, err := c.doJSON(req)
	if err != nil {
		if ue, ok := err.(*Error); ok && ue.Status == http.StatusBadRequest {
			return c.ClaimReward(taskCode)
		}
		return 0, 0, err
	}
	return parseClaimReward(data)
}

// parseClaimReward 解析领奖响应 {"already_claimed":bool,"credit":n,"energy":n}。
func parseClaimReward(data json.RawMessage) (credit, energy int64, err error) {
	var resp struct {
		AlreadyClaimed bool  `json:"already_claimed"`
		Credit         int64 `json:"credit"`
		Energy         int64 `json:"energy"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, 0, err
	}
	if resp.AlreadyClaimed {
		return 0, 0, nil
	}
	return resp.Credit, resp.Energy, nil
}

// ---------------------------------------------------------------------------
// 连登管家闭环（streak 兑换 / 抽奖 / 补签卡 / 礼包 / 补偿）
// ---------------------------------------------------------------------------

// StreakFull 连登完整状态（GET /activity/growth/streak）。
type StreakFull struct {
	Streak struct {
		Days              int    `json:"days"`
		MonthTotalDays    int    `json:"month_total_days"`
		NextTier          string `json:"next_tier"`
		NextTierRemaining int    `json:"next_tier_remaining"`
	} `json:"streak"`
	MakeupCards struct {
		Balance int `json:"balance"`
		Max     int `json:"max"`
	} `json:"makeup_cards"`
	RedemptionStatus struct {
		Tier7dStatus  string `json:"tier_7d_status"`
		Tier14dStatus string `json:"tier_14d_status"`
		Tier28dStatus string `json:"tier_28d_status"`
		RemainingDays int    `json:"remaining_days"`
		Tiers         []struct {
			Tier    string `json:"tier"`
			Days    int    `json:"days"`
			Credit  int    `json:"credit"`
			Energy  int    `json:"energy"`
			Cards   int    `json:"cards"`
			Chances int    `json:"chances"`
		} `json:"tiers"`
	} `json:"redemption_status"`
}

// GrowthStreakFull 拉取连登完整状态。
func (c *Client) GrowthStreakFull() (*StreakFull, error) {
	data, err := c.growthJSON(http.MethodGet, streakPath, nil)
	if err != nil {
		return nil, err
	}
	out := &StreakFull{}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, err
	}
	return out, nil
}

// GrowthRedeemTier 兑换连登档位（tier: "7d"|"14d"|"28d"；未解锁上游 403，调用方跳过）。
func (c *Client) GrowthRedeemTier(tier string) error {
	_, err := c.growthJSON(http.MethodPost, streakRedeemPath,
		map[string]any{"tier": tier, "client_token": newMessageID()})
	return err
}

// LotteryChances 当前抽奖次数。
func (c *Client) LotteryChances() (int, error) {
	data, err := c.growthJSON(http.MethodGet, lotterySummaryPath, nil)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Chances int `json:"chances"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	return resp.Chances, nil
}

// LotteryDraw 抽奖一次（每次新 client_token 幂等）。
func (c *Client) LotteryDraw() (json.RawMessage, error) {
	return c.growthJSON(http.MethodPost, lotteryDrawPath, map[string]any{"client_token": newMessageID()})
}

// HeatmapYesterdayMissed 检查昨日是否漏签（heatmap cell score==0）。
func (c *Client) HeatmapYesterdayMissed() (bool, error) {
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	data, err := c.growthJSON(http.MethodGet, heatmapPath, nil)
	if err != nil {
		return false, err
	}
	var resp struct {
		Cells []struct {
			Date  string `json:"date"`
			Score int    `json:"score"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return false, err
	}
	for _, cell := range resp.Cells {
		if len(cell.Date) >= 10 && cell.Date[:10] == yesterday {
			return cell.Score == 0, nil
		}
	}
	return false, nil
}

// UseMakeupCard 对指定日期使用补签卡（保住连登连续天数；无卡返回业务错误）。
func (c *Client) UseMakeupCard(date string) error {
	_, err := c.growthJSON(http.MethodPost, makeupCardUsePath, map[string]any{"target_date": date})
	return err
}

// ClaimGift 领取新手礼包（每号一次，已领返回业务错误）。
func (c *Client) ClaimGift() (int64, error) {
	data, err := c.billingJSON(http.MethodPost, "/billing/meter/claim-gift", map[string]any{})
	if err != nil {
		return 0, err
	}
	var resp struct {
		Credit int64 `json:"credit"`
	}
	_ = json.Unmarshal(data, &resp)
	return resp.Credit, nil
}

// CompensationStatus 补偿活动状态。
type CompensationStatus struct {
	Active         bool   `json:"active"`
	Claimed        bool   `json:"claimed"`
	CreditNum      int64  `json:"credit_num"`
	ValidityPeriod int64  `json:"validity_period"`
	EndTime        string `json:"end_time"`
}

// Pending 是否存在「可领且未领」的补偿。
func (s CompensationStatus) Pending() bool {
	return s.Active && !s.Claimed && s.CreditNum > 0
}

// GetCompensationStatus 查补偿活动状态（active=false 是无活动的正常返回）。
func (c *Client) GetCompensationStatus() (CompensationStatus, error) {
	data, err := c.billingJSON(http.MethodPost, "/billing/meter/compensation-status", map[string]any{})
	if err != nil {
		return CompensationStatus{}, err
	}
	var st CompensationStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return CompensationStatus{}, err
	}
	return st, nil
}

// ClaimCompensation 领取活动补偿（有则领；已领/无返回业务错误）。
func (c *Client) ClaimCompensation() (int64, error) {
	data, err := c.billingJSON(http.MethodPost, "/billing/meter/claim-compensation", map[string]any{})
	if err != nil {
		return 0, err
	}
	var resp struct {
		Credit int64 `json:"credit"`
	}
	_ = json.Unmarshal(data, &resp)
	return resp.Credit, nil
}

// ---------------------------------------------------------------------------
// 活跃上报（点亮连登 + 解锁 first_buddy 任务；每号每天 1 次即可）
// ---------------------------------------------------------------------------

// ReportChatActivity 向上游发送一条对话活跃上报（chat_request_send，默认模型）。
func (c *Client) ReportChatActivity(conversationID, requestID string) error {
	return c.ReportChatActivityModel(conversationID, requestID, "deepseek-v4-flash", "DeepSeek V4 Flash")
}

// ReportChatActivityModel 同上，可指定上报携带的模型（对齐「体验某模型」类任务）。
func (c *Client) ReportChatActivityModel(conversationID, requestID, modelID, modelName string) error {
	if requestID == "" {
		requestID = conversationID
	}
	if modelID == "" {
		modelID = "deepseek-v4-flash"
	}
	if modelName == "" {
		modelName = modelID
	}
	now := time.Now().UnixMilli()
	ev := map[string]any{
		"eventCode":             "chat_request_send",
		"timestamp":             now,
		"reportDelay":           0,
		"mode":                  "craft",
		"conversationId":        conversationID,
		"requestId":             requestID,
		"inputLength":           12,
		"requestModelId":        modelID,
		"requestModelName":      modelName,
		"isPlan":                false,
		"isAutoExecuteTerminal": false,
		"isAutoModify":          false,
		"codebaseEnable":        false,
		"maxToken":              0,
		"maxSteps":              0,
		"temperature":           0,
		"maxRetries":            0,
		"mentionContexts":       []any{},
		"knowledgeId":           []any{},
		"knowledgeName":         []any{},
		"codebaseId":            "",
		"mentionContextCount":   0,
		"command":               "",
		"expertId":              "",
		"recommendId":           "",
		"skillId":               "",
		"skillCount":            0,
		"totalCount":            0,
		"fileUri":               "",
		"presentAt":             now,
		"traceId":               "",
		"rootRequestId":         requestID,
		"parentConversationId":  conversationID,
		"agentName":             "default",
		"agentType":             "conversation",
		"userId":                c.cred.UID,
	}
	raw, err := json.Marshal([]map[string]any{ev})
	if err != nil {
		return err
	}
	_, err = c.billingJSON(http.MethodPost, reportPath, json.RawMessage(raw))
	return err
}

// InNightWindow 夜猫子计数窗口（23:00–08:00 本地时区）判定。
func InNightWindow(now time.Time) bool {
	h := now.Hour()
	return h >= 23 || h < 8
}

// BlackcatNeed 查 black_cat 任务剩余差额（需要再完成几次对话；0 = 无可做）。
func (c *Client) BlackcatNeed() (int64, error) {
	tasks, err := c.ListTasks()
	if err != nil {
		return 0, err
	}
	for _, t := range tasks {
		if t.TaskCode == "black_cat" {
			if t.Claimed || t.Current >= t.Target {
				return 0, nil
			}
			return t.Target - t.Current, nil
		}
	}
	return 0, nil
}

// ErrTaskBehaviorUnsupported 行为链类任务一期不执行的标记错误。
var ErrTaskBehaviorUnsupported = fmt.Errorf("behavior-chain task unsupported in M3")

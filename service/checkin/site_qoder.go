// site_qoder.go Qoder 站点签到（api 模式）：领取「每日 100 Credits」活动奖励。
//
// 接口契约来自官方桌面端行为（逆向记录：88lin/workbuddy-auto-signin qoder_signin.py）：
//
//	GET  {base}/sash/api/v1/me/campaigns
//	POST {base}/sash/api/v1/me/campaigns/{campaignId}/claim   // 幂等
//
// 必需头：Accept=application/json、User-Agent=Qoder/claim、Cosy-ClientType=10、
// Authorization=Bearer <桌面端登录态 token>；可选风控头 Cosy-MachineId /
// Cosy-MachineToken / Cosy-MachineCode / Cosy-MachineType / Cosy-MachineOS /
// Cosy-MachineHostname / Cosy-Version（带设备标识时服务端更容易下发活动）。
//
// 活动按 UTC+8 每日 10:00 刷新，无补领；发放判定 = HTTP 200 且 body.data.status
// == "CLAIMED"（replayed=true 表示本窗口已领过，按成功处理）。
package checkin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// qoderDefaultBase Qoder CN 开放接口基址（国际版可覆盖为 openapi.qoder.com）。
const qoderDefaultBase = "https://openapi.qoder.com.cn"

// qoderCred Qoder 账号凭据（存 CheckinAccount.CredJson，加密落盘）。
type qoderCred struct {
	Token           string `json:"token"`                     // 桌面端登录态 token（必填）
	BaseURL         string `json:"baseUrl,omitempty"`         // 缺省 qoderDefaultBase
	MachineID       string `json:"machineId,omitempty"`       // Cosy-MachineId
	MachineToken    string `json:"machineToken,omitempty"`    // Cosy-MachineToken
	MachineCode     string `json:"machineCode,omitempty"`     // Cosy-MachineCode
	MachineType     string `json:"machineType,omitempty"`     // Cosy-MachineType
	MachineOS       string `json:"machineOs,omitempty"`       // Cosy-MachineOS
	MachineHostname string `json:"machineHostname,omitempty"` // Cosy-MachineHostname
	ClientVersion   string `json:"clientVersion,omitempty"`   // Cosy-Version
}

// qoderHTTP Qoder 专用 client（活动接口响应快，30s 足够）。
var qoderHTTP = &http.Client{Timeout: 30 * time.Second}

// qoderCampaign 活动项（仅取用到的字段）。
type qoderCampaign struct {
	CampaignID    string  `json:"campaignId"`
	CampaignKey   string  `json:"campaignKey"`
	ActionType    string  `json:"actionType"`
	ClaimStatus   string  `json:"claimStatus"`
	StartAt       float64 `json:"startAt"`
	EndAt         float64 `json:"endAt"`
	BenefitAmount float64 `json:"-"`
}

// qoderCampaignsResp campaigns 列表响应（宽容解析 benefit.amount）。
type qoderCampaignsResp struct {
	Campaigns []map[string]any `json:"campaigns"`
}

// runQoder 执行 Qoder 每日 Credits 领取。
func runQoder(acc *model.CheckinAccount) *RunResult {
	cred, err := parseQoderCred(model.DecryptCheckinCred(acc.CredJson))
	if err != nil {
		return &RunResult{Success: false, Message: err.Error(), Balance: -1}
	}

	campaigns, msg := qoderListCampaigns(cred)
	if msg != "" {
		return &RunResult{Success: false, Message: msg, Balance: -1}
	}

	now := float64(time.Now().Unix())
	var todo, claimed []map[string]any
	for _, c := range campaigns {
		if actionType, _ := c["actionType"].(string); actionType != "CLAIM_BENEFIT" {
			continue
		}
		if !qoderCampaignActive(c, now) {
			continue
		}
		status, _ := c["claimStatus"].(string)
		switch status {
		case "CLAIMABLE":
			todo = append(todo, c)
		case "CLAIMED":
			claimed = append(claimed, c)
		}
	}

	if len(todo) == 0 {
		if len(claimed) > 0 {
			amount := qoderSumAmount(claimed)
			return &RunResult{
				Success: true,
				Skipped: true,
				Message: fmt.Sprintf("本周期已领取 %s Credits（%s）", formatQoderCredits(amount), qoderKeys(claimed)),
				Reward:  0,
				Balance: -1,
			}
		}
		return &RunResult{Success: false, Message: "当前没有可领取的 Credits 活动（活动可能已结束或尚未开始）", Balance: -1}
	}

	var total float64
	var keys []string
	var failures []string
	for _, c := range todo {
		id, _ := c["campaignId"].(string)
		key, _ := c["campaignKey"].(string)
		if key == "" {
			key = id
		}
		amount, replayed, err := qoderClaim(cred, id)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %s", key, err.Error()))
			continue
		}
		if amount < 0 {
			amount = qoderAmount(c)
		}
		total += amount
		keys = append(keys, key)
		if replayed {
			failures = append(failures, fmt.Sprintf("%s: 本窗口已领过（replayed）", key))
		}
	}

	if len(keys) == 0 {
		return &RunResult{Success: false, Message: "领取失败：" + strings.Join(failures, "；"), Balance: -1}
	}
	note := fmt.Sprintf("领取 %s Credits（%s）", formatQoderCredits(total), strings.Join(keys, "、"))
	if len(failures) > 0 {
		note += "；部分失败：" + strings.Join(failures, "；")
	}
	return &RunResult{Success: true, Message: note, Reward: int64(total), Balance: -1}
}

// parseQoderCred 解析凭据 JSON；兼容「裸 token 字符串」写法（含 token=xxx 形式）。
func parseQoderCred(raw string) (*qoderCred, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, fmt.Errorf("未配置凭据：需要 {\"token\":\"<Qoder 桌面端登录态 token>\"}")
	}
	var cred qoderCred
	if strings.HasPrefix(text, "{") {
		if err := json.Unmarshal([]byte(text), &cred); err != nil {
			return nil, fmt.Errorf("凭据 JSON 解析失败: %w", err)
		}
	} else {
		// 兼容 token=xxx / 裸 token 两种粘贴形式。
		value := text
		if idx := strings.IndexByte(text, '='); idx > 0 && strings.EqualFold(strings.TrimSpace(text[:idx]), "token") {
			value = strings.TrimSpace(text[idx+1:])
		}
		cred.Token = strings.Trim(value, `"'`)
	}
	cred.Token = strings.TrimSpace(cred.Token)
	if cred.Token == "" {
		return nil, fmt.Errorf("凭据缺 token（Qoder CN 桌面端 → 登录态 auth.v1.dat 中的 token 字段）")
	}
	if strings.TrimSpace(cred.BaseURL) == "" {
		cred.BaseURL = qoderDefaultBase
	}
	cred.BaseURL = strings.TrimRight(strings.TrimSpace(cred.BaseURL), "/")
	if err := ValidateExternalURL(cred.BaseURL); err != nil {
		return nil, fmt.Errorf("baseUrl 不合法（SSRF 防护）: %w", err)
	}
	return &cred, nil
}

// qoderListCampaigns 拉取活动列表；返回错误摘要（空串=成功）。
func qoderListCampaigns(cred *qoderCred) ([]map[string]any, string) {
	code, raw, err := qoderDo(cred, http.MethodGet, "/sash/api/v1/me/campaigns")
	if err != nil {
		return nil, "查询活动失败（网络）: " + err.Error()
	}
	switch code {
	case http.StatusUnauthorized:
		return nil, "登录态被拒绝（HTTP 401）：Qoder 桌面端 token 已失效，重新登录后更新凭据"
	case http.StatusForbidden:
		return nil, "查询被拒绝（HTTP 403）：账号资格不足或风控拦截（可补 machineId/machineToken 等设备头）"
	}
	if code != http.StatusOK {
		return nil, fmt.Sprintf("查询活动失败: HTTP %d %s", code, truncate(string(raw), 200))
	}
	var resp qoderCampaignsResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, "活动响应非 JSON: " + truncate(string(raw), 200)
	}
	return resp.Campaigns, ""
}

// qoderClaim 领取单个活动；返回到账 Credits、是否 replayed、错误。
// amount < 0 表示响应未带额度（调用方回退到活动定义里的额度）。
func qoderClaim(cred *qoderCred, campaignID string) (float64, bool, error) {
	if strings.TrimSpace(campaignID) == "" {
		return -1, false, fmt.Errorf("campaignId 为空")
	}
	code, raw, err := qoderDo(cred, http.MethodPost, "/sash/api/v1/me/campaigns/"+campaignID+"/claim")
	if err != nil {
		return -1, false, fmt.Errorf("网络错误: %w", err)
	}
	switch code {
	case http.StatusUnauthorized:
		return -1, false, fmt.Errorf("HTTP 401 登录态被拒绝，请重新登录客户端")
	case http.StatusForbidden:
		return -1, false, fmt.Errorf("HTTP 403 领取被拒绝（账号资格/风控）")
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return -1, false, fmt.Errorf("HTTP %d 响应非 JSON: %s", code, truncate(string(raw), 160))
	}
	data := doc
	if nested, ok := doc["data"].(map[string]any); ok {
		data = nested
	}
	status, _ := data["status"].(string)
	if code != http.StatusOK || status != "CLAIMED" {
		return -1, false, fmt.Errorf("HTTP %d status=%s %s", code, status, truncate(string(raw), 160))
	}
	replayed, _ := data["replayed"].(bool)
	amount := qoderAmount(data)
	return amount, replayed, nil
}

// qoderDo 发一次带签名的请求（含 429/5xx 有界重试）。
func qoderDo(cred *qoderCred, method, path string) (int, []byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		code, raw, err := qoderDoOnce(cred, method, path)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
			continue
		}
		if code == http.StatusTooManyRequests || (code >= 500 && code < 600) {
			lastErr = fmt.Errorf("HTTP %d", code)
			time.Sleep(time.Duration(attempt+1) * 3 * time.Second)
			continue
		}
		return code, raw, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("请求重试耗尽")
	}
	return 0, nil, lastErr
}

// qoderDoOnce 单次请求。
func qoderDoOnce(cred *qoderCred, method, path string) (int, []byte, error) {
	var body io.Reader
	if method == http.MethodPost {
		body = bytes.NewReader([]byte{})
	}
	req, err := http.NewRequest(method, cred.BaseURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Qoder/claim")
	req.Header.Set("Cosy-ClientType", "10")
	req.Header.Set("Authorization", "Bearer "+cred.Token)
	if cred.MachineID != "" {
		req.Header.Set("Cosy-MachineId", cred.MachineID)
	}
	if cred.MachineToken != "" {
		req.Header.Set("Cosy-MachineToken", cred.MachineToken)
	}
	if cred.MachineCode != "" {
		req.Header.Set("Cosy-MachineCode", cred.MachineCode)
	}
	if cred.MachineType != "" {
		req.Header.Set("Cosy-MachineType", cred.MachineType)
	}
	if cred.MachineOS != "" {
		req.Header.Set("Cosy-MachineOS", cred.MachineOS)
	}
	if cred.MachineHostname != "" {
		req.Header.Set("Cosy-MachineHostname", cred.MachineHostname)
	}
	if cred.ClientVersion != "" {
		req.Header.Set("Cosy-Version", cred.ClientVersion)
	}
	resp, err := qoderHTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw, nil
}

// qoderCampaignActive 判定活动时间窗（startAt/endAt 为 unix 秒，缺省视为不限制）。
func qoderCampaignActive(c map[string]any, now float64) bool {
	if start, ok := c["startAt"].(float64); ok && start > 0 && now < start {
		return false
	}
	if end, ok := c["endAt"].(float64); ok && end > 0 && now >= end {
		return false
	}
	return true
}

// qoderAmount 从任意层级取 benefit.amount（活动定义或领取响应）。
func qoderAmount(node map[string]any) float64 {
	benefit, ok := node["benefit"].(map[string]any)
	if !ok {
		return -1
	}
	if amount, ok := benefit["amount"].(float64); ok {
		return amount
	}
	return -1
}

func qoderSumAmount(list []map[string]any) float64 {
	var total float64
	for _, c := range list {
		if amount := qoderAmount(c); amount > 0 {
			total += amount
		}
	}
	return total
}

func qoderKeys(list []map[string]any) string {
	keys := make([]string, 0, len(list))
	for _, c := range list {
		key, _ := c["campaignKey"].(string)
		if key == "" {
			key, _ = c["campaignId"].(string)
		}
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "活动"
	}
	return strings.Join(keys, "、")
}

// formatQoderCredits 额度展示（整数不带小数）。
func formatQoderCredits(v float64) string {
	if v < 0 {
		return "未知数量"
	}
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.1f", v)
}

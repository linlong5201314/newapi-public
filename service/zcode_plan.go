package service

// Balance contract pinned to HaizhuAI/Zkali@0770d34c1561018ebba3a780ec3c4896faecffe0.
// Account availability comes only from this authenticated response, never the catalog.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const zcodeStartPlanBalanceURL = ZCodeAPIBase + "/zcode-plan/billing/balance?app_version=" + ZCodeAppVersion

// ErrZCodePlanUnavailable means the balance response establishes no usable,
// supported Start Plan capability. Transport/authentication failures stay distinct.
var ErrZCodePlanUnavailable = errors.New("ZCode Start Plan has no active supported model entitlement with available quota")

// Raw numeric fields distinguish missing quota from an explicit zero and reject
// malformed values rather than coercing them to an available balance.
type zcodePlanEntitlement struct {
	ID          string          `json:"entitlement_id"`
	EffectiveAt json.RawMessage `json:"effective_at"`
}

type zcodeStartPlan struct {
	UserPlanID   string                 `json:"user_plan_id"`
	PlanID       string                 `json:"plan_id"`
	Name         string                 `json:"name"`
	Status       string                 `json:"status"`
	StartsAt     json.RawMessage        `json:"starts_at"`
	EndsAt       json.RawMessage        `json:"ends_at"`
	Entitlements []zcodePlanEntitlement `json:"entitlements"`
}

type zcodePlanBucket struct {
	UserPlanID    string          `json:"user_plan_id"`
	PlanID        string          `json:"plan_id"`
	EntitlementID string          `json:"entitlement_id"`
	Capabilities  []string        `json:"capabilities"`
	Total         json.RawMessage `json:"total_units"`
	Used          json.RawMessage `json:"used_units"`
	Reserved      json.RawMessage `json:"reserved_units"`
	Remaining     json.RawMessage `json:"remaining_units"`
	Available     json.RawMessage `json:"available_units"`
	PeriodStart   json.RawMessage `json:"period_start"`
	PeriodEnd     json.RawMessage `json:"period_end"`
	ExpiresAt     json.RawMessage `json:"expires_at"`
}

// ValidateZCodeStartPlan performs one read-only balance request using the ZCode
// login token, not the provider business token. It never claims quota, logs in,
// refreshes credentials, creates keys, or tests availability with inference.
// The caller resolves the credential's stable egress before passing proxy.
func ValidateZCodeStartPlan(ctx context.Context, proxy string, credential *ZCodeCredential) ([]string, error) {
	if credential == nil || (credential.Provider != "zai" && credential.Provider != "bigmodel") ||
		!HeaderCredential(credential.UserID, 256) || !HeaderCredential(credential.Token, 16384) || credential.ExpiresAt <= 0 {
		return nil, ErrAccountProtocol
	}
	if credential.ExpiresAt <= time.Now().Unix() {
		return nil, ErrDesktopLoginExpired
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+credential.Token)
	// X-Device-Mid 是 zcode-plan/billing/balance 的必传头（缺了返回 400
	// {"code":3001}）。服务端用确定性 UUID，不以任何真实设备指纹为输入。
	headers.Set("X-Device-Mid", zcodeDeviceMid(credential.UserID))
	status, responseHeaders, raw, err := desktopAccountRequest(ctx, proxy, zcodeStartPlanBalanceURL, headers)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, ErrDesktopLoginExpired
	}
	if status != http.StatusOK {
		return nil, ErrAccountUpstream
	}
	var result struct {
		Code    *int  `json:"code"`
		Success *bool `json:"success"`
		Data    *struct {
			ServerTime json.RawMessage   `json:"server_time"`
			Plans      []zcodeStartPlan  `json:"plans"`
			Balances   []zcodePlanBucket `json:"balances"`
		} `json:"data"`
	}
	if common.Unmarshal(raw, &result) != nil {
		return nil, ErrAccountProtocol
	}
	if result.Code != nil && (*result.Code == 401 || *result.Code == 403) {
		return nil, ErrDesktopLoginExpired
	}
	if (result.Success != nil && !*result.Success) || (result.Code != nil && *result.Code != 0 && *result.Code != 200) {
		return nil, ErrAccountUpstream
	}
	if result.Data == nil {
		return nil, fmt.Errorf("%w: missing balance data", ErrZCodePlanUnavailable)
	}

	// HTTP Date belongs to this response and supersedes potentially stale JSON
	// server_time, matching the pinned upstream expiry normalization.
	now := float64(time.Now().Unix())
	if date, err := http.ParseTime(responseHeaders.Get("Date")); err == nil {
		now = float64(date.Unix())
	} else if zcodePlanFieldPresent(result.Data.ServerTime) {
		serverTime, valid := zcodePlanNumber(result.Data.ServerTime)
		if !valid || serverTime > 253402300799 {
			return nil, ErrAccountProtocol
		}
		now = serverTime
	}
	if float64(credential.ExpiresAt) <= now || credential.ExpiresAt <= time.Now().Unix() {
		return nil, ErrDesktopLoginExpired
	}

	supported := [...]string{"GLM-5.3-Flash", "GLM-5.2", "GLM-5-Turbo"}
	found := make(map[string]bool, len(supported))
	for _, bucket := range result.Data.Balances {
		if !zcodePlanWindowActive(bucket.PeriodStart, bucket.PeriodEnd, now) ||
			!zcodePlanWindowActive(nil, bucket.ExpiresAt, now) || !zcodePlanQuotaAvailable(bucket) {
			continue
		}
		// Match before filtering active plans: a product ID shared by expired
		// and live instances must not assign the expired instance's quota to
		// the live one. Explicit instance/entitlement IDs disambiguate it.
		owner := -1
		for i, plan := range result.Data.Plans {
			if zcodePlanOwnsBucket(plan, bucket) {
				if owner != -1 {
					owner = -2
					break
				}
				owner = i
			}
		}
		if owner < 0 {
			continue
		}
		plan := result.Data.Plans[owner]
		identity := strings.ToLower(plan.PlanID) + " \n" + strings.ToLower(plan.Name)
		if !strings.EqualFold(strings.TrimSpace(plan.Status), "active") ||
			(!strings.Contains(identity, "start-plan") && !strings.Contains(identity, "start plan")) ||
			!zcodePlanWindowActive(plan.StartsAt, plan.EndsAt, now) {
			continue
		}
		entitled := true
		for _, entitlement := range plan.Entitlements {
			if bucket.EntitlementID == "" || bucket.EntitlementID == entitlement.ID {
				if !zcodePlanWindowActive(entitlement.EffectiveAt, nil, now) {
					entitled = false
				}
			}
		}
		if !entitled {
			continue
		}
		for _, capability := range bucket.Capabilities {
			kind, id, ok := strings.Cut(strings.TrimSpace(capability), ":")
			if !ok || !strings.EqualFold(kind, "model") {
				continue
			}
			for _, model := range supported {
				if strings.EqualFold(strings.TrimSpace(id), model) {
					found[model] = true
				}
			}
		}
	}
	models := make([]string, 0, len(found))
	for _, model := range supported {
		if found[model] {
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		return nil, ErrZCodePlanUnavailable
	}
	return models, nil
}

// zcodeDeviceMid 生成确定性设备标识：同一用户始终得到同一个 UUID，避免每次
// 余额查询产生新设备记录。输入只有用户 ID（已认证身份），不采集任何真实设备指纹。
func zcodeDeviceMid(userID string) string {
	h := sha256.Sum256([]byte("zcode-device-mid-v1:" + userID))
	// RFC 4122 UUID v5 格式（版本位置 0101，变体位 10）。
	raw := h[:16]
	raw[6] = (raw[6] & 0x0f) | 0x50
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

// zcodePlanOwnsBucket requires an explicit link, never just a model/show_name.
func zcodePlanOwnsBucket(plan zcodeStartPlan, bucket zcodePlanBucket) bool {
	if bucket.UserPlanID != "" {
		if plan.UserPlanID != bucket.UserPlanID {
			return false
		}
	} else if bucket.PlanID != "" {
		if plan.PlanID != bucket.PlanID {
			return false
		}
	} else if bucket.EntitlementID == "" {
		return false
	}
	if bucket.PlanID != "" && plan.PlanID != "" && bucket.PlanID != plan.PlanID {
		return false
	}
	if bucket.EntitlementID != "" {
		matches := 0
		for _, entitlement := range plan.Entitlements {
			if entitlement.ID == bucket.EntitlementID {
				matches++
			}
		}
		return matches == 1
	}
	return true
}

func zcodePlanFieldPresent(raw json.RawMessage) bool {
	return len(raw) != 0 && strings.TrimSpace(string(raw)) != "null"
}

func zcodePlanNumber(raw json.RawMessage) (float64, bool) {
	text := strings.TrimSpace(string(raw))
	if len(text) > 0 && text[0] == '"' {
		if common.Unmarshal(raw, &text) != nil {
			return 0, false
		}
	}
	// Numeric strings are decimal JSON numbers, not Go hex floats, whitespace,
	// booleans, infinity or NaN. The JSON wrapper also checks number syntax.
	var number json.Number
	if len(text) == 0 || common.Unmarshal([]byte(text), &number) != nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(string(number), 64)
	return value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func zcodePlanWindowActive(start, end json.RawMessage, now float64) bool {
	if zcodePlanFieldPresent(start) {
		value, valid := zcodePlanNumber(start)
		if !valid || value > 253402300799 || value > now {
			return false
		}
	}
	if zcodePlanFieldPresent(end) {
		value, valid := zcodePlanNumber(end)
		if !valid || value > 253402300799 || value <= now {
			return false
		}
	}
	return true
}

func zcodePlanQuotaAvailable(bucket zcodePlanBucket) bool {
	// No total-minus-used-only inference: an explicit remaining or available
	// balance is required. When present, available is authoritative (zero wins).
	values := make([]float64, 5)
	present := make([]bool, 5)
	for i, raw := range []json.RawMessage{bucket.Total, bucket.Used, bucket.Reserved, bucket.Remaining, bucket.Available} {
		if !zcodePlanFieldPresent(raw) {
			continue
		}
		value, valid := zcodePlanNumber(raw)
		if !valid {
			return false
		}
		values[i], present[i] = value, true
	}
	total, used, reserved, remaining, available := values[0], values[1], values[2], values[3], values[4]
	if (!present[3] && !present[4]) || (present[3] && remaining <= 0) || (present[4] && available <= 0) {
		return false
	}
	if present[0] && (used > total || reserved > total-used || (present[3] && remaining > total-used) || (present[4] && available > total-used-reserved)) {
		return false
	}
	if present[3] && (reserved >= remaining || (present[4] && available > remaining-reserved)) {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Start Plan 套餐预览与领取（Zkali zai-start-plan-provider 协议）
// ---------------------------------------------------------------------------

// ZCodePlanPreview 一个可领取的套餐。
type ZCodePlanPreview struct {
	PlanID    string `json:"plan_id"`
	Name      string `json:"name"`
	Priority  int    `json:"priority"`
	Claimable bool   `json:"claimable"`
}

// ZCodePlanPreviewResult 预览响应的归一化结果。
type ZCodePlanPreviewResult struct {
	Plans      []ZCodePlanPreview `json:"plans"`
	ServerTime float64            `json:"server_time"`
}

// ZCodePlanClaimResult 领取响应。
type ZCodePlanClaimResult struct {
	Claimed bool   `json:"claimed"`
	PlanID  string `json:"plan_id"`
	Message string `json:"message,omitempty"`
}

const zcodePlanPreviewURL = ZCodeAPIBase + "/zcode-plan/billing/preview?app_version=" + ZCodeAppVersion
const zcodePlanClaimURL = ZCodeAPIBase + "/zcode-plan/billing/claim"

// PreviewZCodePlans 查询可领取的 Start Plan 套餐。JWT 可选（未登录也能看公开预览）。
// 返回 nil, nil 表示没有可领取的套餐。
func PreviewZCodePlans(ctx context.Context, proxy string, credential *ZCodeCredential) (*ZCodePlanPreviewResult, error) {
	headers := make(http.Header)
	if credential != nil && HeaderCredential(credential.Token, 16384) {
		headers.Set("Authorization", "Bearer "+credential.Token)
		headers.Set("X-Device-Mid", zcodeDeviceMid(credential.UserID))
	}
	status, _, raw, err := desktopAccountRequest(ctx, proxy, zcodePlanPreviewURL, headers)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, ErrAccountUpstream
	}
	var envelope struct {
		Code *int `json:"code"`
		Data *struct {
			ServerTime json.RawMessage `json:"server_time"`
			Plans      []struct {
				PlanID    string `json:"plan_id"`
				Name      string `json:"name"`
				Priority  int    `json:"priority"`
				Claimable *bool  `json:"claimable"`
			} `json:"plans"`
		} `json:"data"`
	}
	if common.Unmarshal(raw, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 || envelope.Data == nil {
		return nil, ErrAccountProtocol
	}
	result := &ZCodePlanPreviewResult{Plans: []ZCodePlanPreview{}}
	if t, ok := zcodePlanNumber(envelope.Data.ServerTime); ok {
		result.ServerTime = t
	}
	for _, plan := range envelope.Data.Plans {
		claimable := plan.Claimable == nil || *plan.Claimable
		if plan.PlanID != "" && claimable {
			result.Plans = append(result.Plans, ZCodePlanPreview{
				PlanID:    plan.PlanID,
				Name:      plan.Name,
				Priority:  plan.Priority,
				Claimable: true,
			})
		}
	}
	// 按 priority 降序、plan_id 升序排列（Zkali 协议：服务端已排序，客户端不重排）。
	return result, nil
}

// ClaimZCodePlan 用当前 ZCode JWT 领取指定套餐。必须已登录。
func ClaimZCodePlan(ctx context.Context, proxy string, credential *ZCodeCredential, planID string) (*ZCodePlanClaimResult, error) {
	if credential == nil || (credential.Provider != "zai" && credential.Provider != "bigmodel") ||
		!HeaderCredential(credential.UserID, 256) || !HeaderCredential(credential.Token, 16384) || credential.ExpiresAt <= 0 {
		return nil, ErrAccountProtocol
	}
	if credential.ExpiresAt <= time.Now().Unix() {
		return nil, ErrDesktopLoginExpired
	}
	if strings.TrimSpace(planID) == "" || len(planID) > 128 {
		return nil, ErrAccountProtocol
	}
	body, err := common.Marshal(map[string]string{"plan_id": planID})
	if err != nil {
		return nil, ErrAccountProtocol
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+credential.Token)
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Device-Mid", zcodeDeviceMid(credential.UserID))
	status, _, raw, err := desktopAccountRequestWithBody(ctx, proxy, zcodePlanClaimURL, headers, body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, ErrDesktopLoginExpired
	}
	var envelope struct {
		Code *int   `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			Claimed *bool `json:"claimed"`
		} `json:"data"`
	}
	if common.Unmarshal(raw, &envelope) != nil {
		return nil, ErrAccountProtocol
	}
	result := &ZCodePlanClaimResult{PlanID: planID, Message: envelope.Msg}
	if envelope.Code != nil && *envelope.Code == 0 && envelope.Data != nil && envelope.Data.Claimed != nil && *envelope.Data.Claimed {
		result.Claimed = true
	}
	if status != http.StatusOK {
		return result, ErrAccountUpstream
	}
	return result, nil
}

// desktopAccountRequestWithBody 是 desktopAccountRequest 的 POST 变体。
// 只用于套餐领取等需要 JSON body 的请求；URL 与凭据安全约束与 GET 版本相同。
func desktopAccountRequestWithBody(ctx context.Context, proxy, target string, headers http.Header, body []byte) (int, http.Header, []byte, error) {
	client, err := GetAccountAuthHTTPClient(proxy, "")
	if err != nil || client == nil {
		return 0, nil, nil, ErrAccountEgress
	}
	copyClient := *client
	copyClient.Timeout = 8 * time.Second
	copyClient.Jar = nil
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, ErrAccountProtocol
	}
	req.Header = headers.Clone()
	res, err := copyClient.Do(req)
	if err != nil {
		return 0, nil, nil, ErrAccountUpstream
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return 0, nil, nil, ErrAccountProtocol
	}
	return res.StatusCode, res.Header, raw, nil
}

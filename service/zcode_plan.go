package service

// Balance contract pinned to HaizhuAI/Zkali@0770d34c1561018ebba3a780ec3c4896faecffe0.
// Account availability comes only from this authenticated response, never the catalog.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

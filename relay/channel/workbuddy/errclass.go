// errclass.go WorkBuddy 上游错误 12 层分类（判定顺序自「严」到「宽」，不可换）。
// 移植自 _research/workbuddy2api-panel-main internal/upstream/client.go，
// 语义逐层保留；分类结果由 adaptor 映射到 new-api 渠道禁用/冷却语义。
package workbuddy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ErrKind 错误分类，adaptor 据此决定渠道健康处置。
type ErrKind int

const (
	ErrNone           ErrKind = iota // 成功
	ErrHardCredit                    // 余额不足（402 或 body 关键词）→ 硬冷却（签到后解冻）
	ErrSoftRate                      // 429 软限流 → 短冷却（重试换路）
	ErrSessionDead                   // 401 + 12153 offline session 失效 → 禁用渠道（需重新登录）
	ErrNotFound                      // 404 上游偶发 → 短冷却，不累计错误计数
	ErrServer                        // 5xx 上游故障
	ErrContentBlocked                // 内容策略拦截（400 + 审核文案）→ 不罚渠道
	ErrBadParams                     // 请求体解析失败（11101）→ 请求级错误：不罚渠道
	ErrAccountFault                  // 账号级授权/配额故障（11140 request illegal / 14017）→ 禁用渠道
	ErrChannelRefused                // 11128 通道校验失败 → 不罚渠道
	ErrModelBlocked                  // 11102/11158 模型不可用 → 模型级避让（不罚渠道）
	ErrWafBlock                      // 403 + 非业务信封体（APISIX WAF 拦截页/空体）
	ErrPromptTooLong                 // 11115 prompt is too long → 请求级错误
	ErrImageInvalid                  // 图片请求格式/数据无效 → 请求级错误
	ErrUpstreamBusy                  // 6021 queue.full 渠道级容量满载 → 不罚渠道
	ErrClient                        // 其他 4xx / 业务错误
)

func (k ErrKind) String() string {
	switch k {
	case ErrHardCredit:
		return "hard_credit"
	case ErrSoftRate:
		return "soft_rate"
	case ErrSessionDead:
		return "session_dead"
	case ErrNotFound:
		return "not_found"
	case ErrServer:
		return "server"
	case ErrContentBlocked:
		return "content_blocked"
	case ErrBadParams:
		return "bad_params"
	case ErrModelBlocked:
		return "model_blocked"
	case ErrWafBlock:
		return "waf_block"
	case ErrPromptTooLong:
		return "prompt_too_long"
	case ErrImageInvalid:
		return "image_invalid"
	case ErrUpstreamBusy:
		return "upstream_busy"
	case ErrAccountFault:
		return "account_fault"
	case ErrChannelRefused:
		return "channel_refused"
	case ErrClient:
		return "client"
	default:
		return "none"
	}
}

// Error 带分类的上游错误。
type Error struct {
	Kind   ErrKind
	Status int
	Msg    string
	// RetryAfter 上游明示的等待时长（Retry-After 秒 / retry-after-ms / x-ratelimit-reset）。
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	return fmt.Sprintf("upstream %s (http %d): %s", e.Kind, e.Status, e.Msg)
}

// hardMarkers 余额不足关键词（小写比较 + 中文原文比较双通道）。
var hardMarkers = []string{
	"insufficient credit", "no credit", "credit exhausted", "credits exhausted", "out of credit",
	"quota exceeded", "quota exhaust", "payment required", "credit not enough",
	"not enough credit",
	"积分不足", "额度不足", "余额不足", "积分用完", "额度用尽", "没有积分",
}

// softRateMarkers 限流/节流关键词（子串匹配，宁缺毋滥）。
var softRateMarkers = []string{
	"rate limit",
	"rate-limiting",
	"rate-limited",
	"too many requests",
	"too many",
	"usage limit",
	"请求过于频繁", "限流",
}

var sessionDeadMarkers = []string{"Offline user session not found", "12153"}

// isQueueFullBody 判定渠道级容量满载文案（6021 queue.full.title 家族）。
func isQueueFullBody(lower string) bool {
	return strings.Contains(lower, "queue.full") || strings.Contains(lower, "queue full")
}

// accountFaultMarkers 账号级授权/配额故障关键词。
// 注意 "request illegal" 不能作为通用 marker：11128 通道校验失败与 11140 账号限制
// 文案完全相同，必须 code + 文案双条件分流（IsAccountRestricted / isChannelRefused）。
var accountFaultMarkers = []string{
	"trial not activated",
	"trial version is not yet activated",
}

// accountRestrictedMsg WorkBuddy 11140 账号限制的正文关键词。
const accountRestrictedMsg = "request illegal"

// IsAccountRestricted 报告上游 body 是否为 11140 账号级限制（code + 文案双条件）。
func IsAccountRestricted(body string) bool {
	if !strings.Contains(strings.ToLower(body), accountRestrictedMsg) {
		return false
	}
	return hasBusinessCode(body, "11140")
}

// isChannelRefused 报告上游 body 是否为 11128 通道校验失败（code + 文案双条件）。
func isChannelRefused(body string) bool {
	if !strings.Contains(strings.ToLower(body), accountRestrictedMsg) {
		return false
	}
	return hasBusinessCode(body, "11-128")
}

// contentBlockedMarkers 内容策略拦截关键词（误报，不罚渠道）。
var contentBlockedMarkers = []string{
	"blocked by security policy",
	"unapproved channel",
	"illegal api invocation",
}

// badParamsMarkerMsg 请求体解析失败关键词（HTTP 400 + code 11101）。
var badParamsMarkerMsg = "Unmarshal chat params failed"

// invalidImageMarkers 图片请求格式/数据无效文案形态。
var invalidImageMarkers = []string{
	"invalid image_url content",
	"invalid_image_data",
	"replace the image",
}

// promptTooLongMarkers 上下文超限 marker（code 11115 / msg 文案）。
var promptTooLongMarkers = []string{
	`"code":11115`,
	`"code": 11115`,
	`"code":"11115"`,
	"prompt is too long",
}

// isPromptTooLongStatus 11115 只在请求级 4xx 上判。
func isPromptTooLongStatus(status int) bool {
	return status == http.StatusBadRequest || status == http.StatusNotFound ||
		status == http.StatusRequestEntityTooLarge
}

// alreadyCheckinMarkers "今天已签到"关键词（幂等成功判定）。
var alreadyCheckinMarkers = []string{"已签到", "already"}
var badParamsMarkerCode = `"code":11101`

// softRateResetLoc 上游 429 6004 文案中的重置时间固定按 UTC+8 解释。
var softRateResetLoc = time.FixedZone("UTC+8", 8*60*60)

// SoftRateResetLoc 暴露重置时间的固定时区（测试/断言同口径）。
func SoftRateResetLoc() *time.Location { return softRateResetLoc }

// modelRateLimitCode 模型级 429 限流业务码（账号健康，切模型即可）。
const modelRateLimitCode = "6004"

const softRateResetPatternCN = `将在 (.+?) 重置`
const softRateResetPatternEN = `(?i)reset at (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`

var (
	reModelRateLimit  = regexp.MustCompile(`"code"\s*:\s*"?` + modelRateLimitCode + `"?`)
	reSoftRateResetCN = regexp.MustCompile(softRateResetPatternCN)
	reSoftRateResetEN = regexp.MustCompile(softRateResetPatternEN)
)

const softRateTimeLayout = "2006-01-02 15:04:05"

// IsModelRateLimit 报告 429 body 是否明确指向模型级限流（code 6004）。
func IsModelRateLimit(body string) bool {
	return reModelRateLimit.MatchString(body)
}

// modelBlockCode 「该后端无此模型」业务码；modelForbiddenCode 「该账号类型无权使用该模型」。
const modelBlockCode = "11102"
const modelForbiddenCode = "11158"

const modelBlockMsgMarker = "service info not found"
const modelForbiddenMsgMarker = "is not allowed for user type"

// IsModelBlocked 报告 body 是否是 11102/11158 的确定性模型拒绝。
// 只比对 code/msg 独立字段（绝不做整段文本子串匹配），只看 400/404。
func IsModelBlocked(status int, body string) bool {
	if (status != http.StatusBadRequest && status != http.StatusNotFound) || body == "" {
		return false
	}
	lower := strings.ToLower(body)
	if !strings.Contains(body, modelBlockCode) && !strings.Contains(body, modelForbiddenCode) &&
		!strings.Contains(lower, modelBlockMsgMarker) && !strings.Contains(lower, modelForbiddenMsgMarker) {
		return false
	}
	code, msg := modelBlockFields(body)
	if code == modelBlockCode || code == modelForbiddenCode {
		return true
	}
	lowerMsg := strings.ToLower(msg)
	return strings.Contains(lowerMsg, modelBlockMsgMarker) || strings.Contains(lowerMsg, modelForbiddenMsgMarker)
}

// modelBlockFields 提取顶层或 error 子对象中的业务 code 与消息。
func modelBlockFields(body string) (code, msg string) {
	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return "", ""
	}
	nodes := []map[string]any{root}
	if inner, ok := root["error"].(map[string]any); ok {
		nodes = append(nodes, inner)
	}
	for _, node := range nodes {
		for _, key := range []string{"code", "errCode", "error_code"} {
			if v, ok := node[key]; ok && v != nil && code == "" {
				code = strings.TrimSpace(fmt.Sprint(v))
			}
		}
		for _, key := range []string{"msg", "message"} {
			if v, ok := node[key].(string); ok && v != "" && msg == "" {
				msg = strings.TrimSpace(v)
			}
		}
	}
	return code, msg
}

// hasBusinessCode 报告 JSON 错误信封是否含精确业务 code（顶层/嵌套递归遍历）。
func hasBusinessCode(body, want string) bool {
	var root any
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return false
	}
	var walk func(any) bool
	walk = func(value any) bool {
		switch node := value.(type) {
		case map[string]any:
			if code, ok := node["code"]; ok && strings.TrimSpace(fmt.Sprint(code)) == want {
				return true
			}
			for _, child := range node {
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range node {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(root)
}

// hasBusinessEnvelope 报告错误 body 是否携带上游业务信封形态（含 "code":/"msg": 字段）。
func hasBusinessEnvelope(body string) bool {
	return strings.Contains(body, `"code":`) || strings.Contains(body, `"msg":`)
}

// IsWafBlocked 报告 403 响应是否为 WAF 拦截形态（无业务信封的 HTML/空体/纯文本）。
func IsWafBlocked(status int, body string) bool {
	return status == http.StatusForbidden && !hasBusinessEnvelope(body)
}

// retryAfterHeaderCandidates 冷却时长优先解析的响应头候选序列。
var retryAfterHeaderCandidates = []string{"Retry-After", "Retry-After-Ms", "X-Ratelimit-Reset"}

// retryAfterSanity 解析结果上限（超过视为上游异常值丢弃）。
const retryAfterSanity = 2 * time.Hour

// ParseRetryAfter 从限流/拦截响应头解析上游明示的等待时长；全部不可用返回 false。
func ParseRetryAfter(h http.Header) (time.Duration, bool) {
	for _, name := range retryAfterHeaderCandidates {
		v := strings.TrimSpace(h.Get(name))
		if v == "" {
			continue
		}
		if !isAllDigits(v) {
			continue
		}
		n, ok := parseRetryNumber(v, name)
		if !ok {
			continue
		}
		if n <= 0 || n > retryAfterSanity {
			continue
		}
		return n, true
	}
	return 0, false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseRetryNumber 按头名口径把纯数字串折算成时长。
func parseRetryNumber(v, headerName string) (time.Duration, bool) {
	if len(v) > 16 {
		return 0, false
	}
	var n int64
	for _, r := range v {
		n = n*10 + int64(r-'0')
	}
	switch headerName {
	case "Retry-After":
		if n > int64(retryAfterSanity/time.Second) {
			return 0, false
		}
		return time.Duration(n) * time.Second, true
	case "Retry-After-Ms":
		if n > int64(retryAfterSanity/time.Millisecond) {
			return 0, false
		}
		return time.Duration(n) * time.Millisecond, true
	default: // X-Ratelimit-Reset：epoch → 剩余量
		sec := n
		if len(v) >= 12 {
			sec = n / 1000
		}
		remain := time.Until(time.Unix(sec, 0))
		return remain, true
	}
}

// ParseRateReset 从限流响应 body 解析「将在 … 重置」时间（上游 UTC+8 文案）。
func ParseRateReset(body string) (time.Time, bool) {
	m := reSoftRateResetCN.FindStringSubmatch(body)
	if len(m) < 2 {
		m = reSoftRateResetEN.FindStringSubmatch(body)
	}
	if len(m) < 2 {
		return time.Time{}, false
	}
	ts := strings.TrimSpace(m[1])
	ts = strings.TrimSuffix(ts, " UTC+8")
	t, err := time.ParseInLocation(softRateTimeLayout, ts, softRateResetLoc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Classify 按 HTTP 状态码 + body 判定错误类别（12 层判定序，顺序不可换）：
//  0. 11102/11158 模型负缓存 → 1. 402 余额耗尽 → 2. 12153 session 失效 →
//  3. 11128/11140 通道/账号限制 → 4. 429+14018 积分耗尽 → 5. 429+6021 满载 →
//  6. 429 软限流 → 7. 余额词 → 8. 限流词 → 9. 11115 过长 → 10. 404/5xx →
//  11. WAF 403 → 12. 内容/参数/其他 4xx。
func Classify(status int, body string) ErrKind {
	if IsModelBlocked(status, body) {
		return ErrModelBlocked
	}
	if status == http.StatusPaymentRequired {
		return ErrHardCredit
	}
	lower := strings.ToLower(body)
	for _, m := range sessionDeadMarkers {
		if strings.Contains(body, m) {
			return ErrSessionDead
		}
	}
	if isChannelRefused(body) {
		return ErrChannelRefused
	}
	if IsAccountRestricted(body) {
		return ErrAccountFault
	}
	for _, m := range accountFaultMarkers {
		if strings.Contains(lower, strings.ToLower(m)) || strings.Contains(body, m) {
			return ErrAccountFault
		}
	}
	if status == http.StatusTooManyRequests && hasBusinessCode(body, "14018") {
		return ErrHardCredit
	}
	if (status == http.StatusTooManyRequests && hasBusinessCode(body, "6021")) || isQueueFullBody(lower) {
		return ErrUpstreamBusy
	}
	if status == http.StatusTooManyRequests {
		return ErrSoftRate
	}
	for _, m := range hardMarkers {
		if strings.Contains(lower, strings.ToLower(m)) || strings.Contains(body, m) {
			return ErrHardCredit
		}
	}
	for _, m := range softRateMarkers {
		if strings.Contains(lower, strings.ToLower(m)) || strings.Contains(body, m) {
			return ErrSoftRate
		}
	}
	if isPromptTooLongStatus(status) {
		for _, m := range promptTooLongMarkers {
			if strings.Contains(body, m) || (m != strings.ToLower(m) && strings.Contains(lower, strings.ToLower(m))) {
				return ErrPromptTooLong
			}
		}
	}
	if status == http.StatusNotFound {
		return ErrNotFound
	}
	if status >= 500 {
		return ErrServer
	}
	if IsWafBlocked(status, body) {
		return ErrWafBlock
	}
	if status == http.StatusBadRequest && codeMarker(lower, "11135") {
		return ErrImageInvalid
	}
	if status == http.StatusBadRequest {
		for _, m := range invalidImageMarkers {
			if strings.Contains(lower, m) {
				return ErrImageInvalid
			}
		}
	}
	if status >= 400 {
		for _, m := range contentBlockedMarkers {
			if strings.Contains(lower, m) {
				return ErrContentBlocked
			}
		}
		if strings.Contains(body, badParamsMarkerMsg) || strings.Contains(body, badParamsMarkerCode) {
			return ErrBadParams
		}
		return ErrClient
	}
	return ErrNone
}

// codeMarker 判定 body 是否含指定业务 code（JSON 空白容差的字面匹配）。
func codeMarker(lowerBody, code string) bool {
	for _, form := range []string{`"code":` + code, `"code": ` + code, `"code":"` + code + `"`} {
		if strings.Contains(lowerBody, form) {
			return true
		}
	}
	return false
}

// IsAlreadyCheckin 报告 err 是否表示"今天已签到"（上游幂等拒绝重复签到）。
// 只认带分类的 *Error；网络层/解析层错误不得当作幂等成功。
func IsAlreadyCheckin(err error) bool {
	var ue *Error
	if !asError(err, &ue) {
		return false
	}
	for _, m := range alreadyCheckinMarkers {
		if strings.Contains(ue.Msg, m) || strings.Contains(strings.ToLower(ue.Msg), strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// asError errors.As 的本地封装（避免在判断处重复引 errors 包名冲突）。
func asError(err error, target **Error) bool {
	for err != nil {
		if ue, ok := err.(*Error); ok {
			*target = ue
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

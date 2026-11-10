// errclass.go Trae SOLO 上游错误分类（SPEC §4.3）+ 签到错误码语义。
// 移植自 _research/trae2api-web-main internal/upstream/client.go。
package trae

import (
	"fmt"
	"net/http"
	"strings"
)

// ErrKind 错误分类，adaptor/签到调度据此决定渠道健康处置。
type ErrKind int

const (
	ErrNone        ErrKind = iota // 成功
	ErrPlanLimit                  // 1005 权益不足 → 硬冷却（签到补积分后解冻）
	ErrSoftRate                   // 429 → 短冷却（重试换路）
	ErrSessionDead                // 401 + Cloud-IDE-JWT 失效 → 禁用渠道（需重新登录）
	ErrNotFound                   // 404
	ErrServer                     // 5xx
	ErrQuotaExhausted             // 4008 配额耗尽 → 硬冷却（签到补积分后解冻）
	ErrClient                     // 其他 4xx
)

func (k ErrKind) String() string {
	switch k {
	case ErrPlanLimit:
		return "plan_limit"
	case ErrSoftRate:
		return "soft_rate"
	case ErrSessionDead:
		return "session_dead"
	case ErrNotFound:
		return "not_found"
	case ErrServer:
		return "server"
	case ErrQuotaExhausted:
		return "quota_exhausted"
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
}

func (e *Error) Error() string {
	return fmt.Sprintf("upstream %s (http %d): %s", e.Kind, e.Status, e.Msg)
}

var sessionDeadMarkers = []string{"login", "token 失效", "token invalid", "session", "unauthorized", "401"}

// Classify 按 HTTP 状态码 + body 判定错误类别。
func Classify(status int, body string) ErrKind {
	lower := strings.ToLower(body)
	// 4008 配额耗尽（结构化业务码）
	if strings.Contains(body, `"code":4008`) || strings.Contains(body, `"code": 4008`) {
		return ErrQuotaExhausted
	}
	// 1005 plan 权益不足
	if strings.Contains(body, `"code":1005`) || strings.Contains(body, `"code": 1005`) ||
		(strings.Contains(body, "1005") && strings.Contains(lower, "plan")) {
		return ErrPlanLimit
	}
	// 1001 认证失效（结构化业务码，任意状态码）
	if strings.Contains(body, `"code":1001`) || strings.Contains(body, `"code": 1001`) {
		return ErrSessionDead
	}
	// session 失效
	if status == http.StatusUnauthorized {
		for _, m := range sessionDeadMarkers {
			if strings.Contains(lower, strings.ToLower(m)) {
				return ErrSessionDead
			}
		}
		return ErrSessionDead
	}
	if status == http.StatusTooManyRequests {
		return ErrSoftRate
	}
	if status == http.StatusNotFound {
		return ErrNotFound
	}
	if status >= 500 {
		return ErrServer
	}
	if status >= 400 {
		return ErrClient
	}
	return ErrNone
}

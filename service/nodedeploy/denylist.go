// denylist.go 影响面保护：denylist 硬拒绝 + allowlist 校验。
package nodedeploy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/model"
)

var (
	// ErrDenylisted 目标命中内置 denylist（硬拒绝，任何操作都不得继续）。
	ErrDenylisted = errors.New("target is in the node denylist")
	// ErrNotAllowed 目标不在 allowlist 内。
	ErrNotAllowed = errors.New("target is not in the node allowlist")
	// ErrNodeNotFound 节点不存在。
	ErrNodeNotFound = errors.New("node not found")
	// ErrPlanExpired plan 不存在或已过期（10 分钟有效）。
	ErrPlanExpired = errors.New("deploy plan not found or expired")
	// ErrPlanNodeChanged plan 生成后节点配置已变更，必须重新 plan。
	ErrPlanNodeChanged = errors.New("node configuration changed since plan generation; regenerate the plan")
	// ErrConfirmRequired 写操作必须 confirm=true。
	ErrConfirmRequired = errors.New("confirm must be true to apply a plan")
)

// NodeTargets 返回需要做 denylist/allowlist 校验的目标标识（非空去重）。
func NodeTargets(node *model.Node) []string {
	if node == nil {
		return nil
	}
	candidates := []string{
		strings.TrimSpace(node.ProjectId),
		strings.TrimSpace(node.ServiceId),
		strings.TrimSpace(node.EnvironmentId),
		strings.TrimSpace(node.WorkspaceId),
		strings.TrimSpace(node.Domain),
		strings.TrimSpace(node.ServiceDomain),
	}
	targets := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, value := range candidates {
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, value)
	}
	return targets
}

// IsDenylisted 判断值是否命中 denylist（大小写不敏感；域名支持后缀匹配）。
func IsDenylisted(value string, denylist []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, item := range denylist {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if value == item {
			return true
		}
		// 域名前缀匹配：denylist 填 ailong.love 时 gate.ailong.love 一并保护。
		if strings.Contains(value, ".") && strings.HasSuffix(value, "."+item) {
			return true
		}
	}
	return false
}

// ValidateTargets 校验节点全部目标：denylist 命中硬拒绝；allowlist 非空时每个目标必须在其中。
func ValidateTargets(node *model.Node, opts Options) error {
	targets := NodeTargets(node)
	for _, target := range targets {
		if IsDenylisted(target, opts.Denylist) {
			return fmt.Errorf("%w: %s", ErrDenylisted, target)
		}
	}
	if len(opts.Allowlist) > 0 {
		allowed := make(map[string]bool, len(opts.Allowlist))
		for _, item := range opts.Allowlist {
			allowed[strings.ToLower(strings.TrimSpace(item))] = true
		}
		for _, target := range targets {
			if !allowed[strings.ToLower(target)] {
				return fmt.Errorf("%w: %s", ErrNotAllowed, target)
			}
		}
	}
	return nil
}

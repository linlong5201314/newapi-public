// health.go 节点健康检查：GET {node base}/api/status，记录健康/延迟/错误。
package nodemonitor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// CheckResult 单次巡检结果。
type CheckResult struct {
	NodeId    int    `json:"node_id"`
	Healthy   bool   `json:"healthy"`
	LatencyMs int    `json:"latency_ms"`
	Status    int    `json:"status"`
	Error     string `json:"error,omitempty"`
	CheckedAt int64  `json:"checked_at"`
}

// httpClient 巡检专用（不跟随过多重定向，限制总耗时）。
var httpClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

// NodeBaseURL 返回节点访问根地址：ServiceUrl 优先，其次自定义域名/平台域名。
func NodeBaseURL(node *model.Node) string {
	for _, candidate := range []string{node.ServiceUrl, node.Domain, node.ServiceDomain} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if !strings.Contains(candidate, "://") {
			candidate = "https://" + candidate
		}
		return strings.TrimSuffix(candidate, "/")
	}
	return ""
}

// CheckNode 立即检查单个节点并把结果写回 nodes 表。
func CheckNode(ctx context.Context, node *model.Node) CheckResult {
	result := CheckResult{NodeId: node.Id, CheckedAt: common.GetTimestamp()}
	base := NodeBaseURL(node)
	if base == "" {
		result.Error = "node has no ServiceUrl/Domain"
		_ = model.UpdateNodeHeartbeat(node.Id, false, 0, result.Error)
		return result
	}
	target := base + "/api/status"
	if parsed, err := url.Parse(target); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		result.Error = "invalid service URL: " + target
		_ = model.UpdateNodeHeartbeat(node.Id, false, 0, result.Error)
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		result.Error = err.Error()
		_ = model.UpdateNodeHeartbeat(node.Id, false, 0, result.Error)
		return result
	}
	req.Header.Set("Accept", "application/json")
	started := time.Now()
	resp, err := httpClient.Do(req)
	result.LatencyMs = int(time.Since(started).Milliseconds())
	if err != nil {
		result.Error = err.Error()
		_ = model.UpdateNodeHeartbeat(node.Id, false, result.LatencyMs, truncate(result.Error, 500))
		return result
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	result.Status = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Healthy = true
	} else {
		result.Error = fmt.Sprintf("unexpected status %d from %s", resp.StatusCode, target)
	}
	_ = model.UpdateNodeHeartbeat(node.Id, result.Healthy, result.LatencyMs, truncate(result.Error, 500))
	return result
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

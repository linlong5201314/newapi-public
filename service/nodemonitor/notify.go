// notify.go 节点告警：webhook（Option NodeAlertWebhookUrl）+ SMTP 邮件（Option NodeAlertEmail）。
// 30 分钟冷却防止巡检周期内重复轰炸；Redis 可用时跨节点共享冷却。
package nodemonitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const alertCooldown = 30 * time.Minute

var (
	alertMu       sync.Mutex
	alertLastSent = map[string]int64{}
)

// AlertPayload webhook 请求体。
type AlertPayload struct {
	Type      string  `json:"type"`
	Kind      string  `json:"kind"`
	NodeId    int     `json:"node_id"`
	NodeName  string  `json:"node_name"`
	Message   string  `json:"message"`
	Remaining float64 `json:"remaining,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
	Timestamp int64   `json:"timestamp"`
}

// SendAlert 发送节点告警（webhook + 邮件），并写 NodeAudit。
func SendAlert(ctx context.Context, node *model.Node, kind, message string, remaining float64) {
	key := fmt.Sprintf("%d:%s", node.Id, kind)
	if !alertAllowed(key) {
		return
	}
	payload := AlertPayload{
		Type:      "node_alert",
		Kind:      kind,
		NodeId:    node.Id,
		NodeName:  node.Name,
		Message:   message,
		Remaining: remaining,
		Threshold: operation_setting.NodeBalanceAlertThreshold,
		Timestamp: common.GetTimestamp(),
	}
	if webhook := strings.TrimSpace(operation_setting.NodeAlertWebhookUrl); webhook != "" {
		if err := postWebhook(ctx, webhook, payload); err != nil {
			common.SysLog("node alert webhook failed: " + err.Error())
		}
	}
	if receiver := strings.TrimSpace(operation_setting.NodeAlertEmail); receiver != "" {
		subject := fmt.Sprintf("[new-api] node alert: %s (%s)", node.Name, kind)
		if err := common.SendEmail(subject, receiver, message); err != nil {
			common.SysLog("node alert email failed: " + err.Error())
		}
	}
	_ = model.InsertNodeAudit(&model.NodeAudit{
		NodeId:       node.Id,
		Action:       "alert",
		Target:       kind,
		Result:       "success",
		OperatorId:   0,
		OperatorName: "system",
		Detail:       message,
		CreatedTime:  common.GetTimestamp(),
	})
}

func alertAllowed(key string) bool {
	if common.RedisEnabled && common.RDB != nil {
		ok, err := common.RDB.SetNX(context.Background(), "node:alert:"+key, 1, alertCooldown).Result()
		if err == nil {
			return ok
		}
	}
	alertMu.Lock()
	defer alertMu.Unlock()
	now := time.Now().Unix()
	if last, exists := alertLastSent[key]; exists && now-last < int64(alertCooldown.Seconds()) {
		return false
	}
	alertLastSent[key] = now
	return true
}

func postWebhook(ctx context.Context, target string, payload AlertPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// site_agent.go agent 模式执行器：POST AgentUrl（云机执行器 HTTP 端点，如 yucheng
// apinex-auto）带 label；响应可回写 cookie 续期。SSRF 防护 + CheckinAllowAgentUrl 开关。
package checkin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// agentHTTP agent 执行器专用 client（执行器跑 headed Chrome 过 Turnstile，耗时较长）。
var agentHTTP = &http.Client{Timeout: 180 * time.Second}

// agentPayload agent 执行器请求体。
type agentPayload struct {
	Label  string `json:"label"`
	Site   string `json:"site"`
	Cookie string `json:"cookie,omitempty"`
}

// agentResponse agent 执行器响应（宽容解析：ok/success 双键 + cookie 续期）。
type agentResponse struct {
	OK      bool   `json:"ok"`
	Success bool   `json:"success"`
	Message string `json:"message"`
	Reward  int64  `json:"reward"`
	Cookie  string `json:"cookie"` // 执行器回写的新 cookie（续期）
}

// runAgent agent 模式签到：POST AgentUrl；apinex 的 Turnstile 由执行器（headed Chrome）完成。
func runAgent(acc *model.CheckinAccount) *RunResult {
	if !operation_setting.CheckinAllowAgentUrl {
		return &RunResult{Success: false, Message: "平台已禁用 agent 模式（CheckinAllowAgentUrl=false）", Balance: -1}
	}
	agentURL := strings.TrimSpace(acc.AgentUrl)
	if agentURL == "" {
		return &RunResult{Success: false, Message: "agent 模式未配置 AgentUrl", Balance: -1}
	}
	if err := ValidateExternalURL(agentURL); err != nil {
		return &RunResult{Success: false, Message: "AgentUrl 不合法（SSRF 防护）: " + err.Error(), Balance: -1}
	}
	cookie := ""
	credJson := model.DecryptCheckinCred(acc.CredJson)
	if strings.TrimSpace(credJson) != "" {
		var cred struct {
			Cookie string `json:"cookie"`
		}
		if err := json.Unmarshal([]byte(credJson), &cred); err == nil {
			cookie = cred.Cookie
		} else {
			// 兼容纯文本 cookie 直填。
			cookie = strings.TrimSpace(credJson)
		}
	}
	payload, _ := json.Marshal(agentPayload{Label: acc.Label, Site: acc.Site, Cookie: cookie})
	req, err := http.NewRequest(http.MethodPost, agentURL, bytes.NewReader(payload))
	if err != nil {
		return &RunResult{Success: false, Message: err.Error(), Balance: -1}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := agentHTTP.Do(req)
	if err != nil {
		return &RunResult{Success: false, Message: "agent 调用失败: " + err.Error(), Balance: -1}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return &RunResult{Success: false, Message: fmt.Sprintf("agent 返回 %d: %s", resp.StatusCode, truncate(string(raw), 200)), Balance: -1}
	}
	var ar agentResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return &RunResult{Success: false, Message: "agent 响应解析失败: " + err.Error(), Balance: -1}
	}
	// cookie 续期：执行器回写的新 cookie 落 CredJson（加密存储由持久化层处理）。
	if ar.Cookie != "" && ar.Cookie != cookie {
		newCred, _ := json.Marshal(map[string]string{"cookie": ar.Cookie})
		acc.CredJson = model.EncryptCheckinCred(string(newCred))
	}
	ok := ar.OK || ar.Success
	msg := ar.Message
	if msg == "" {
		if ok {
			msg = "agent 执行成功"
		} else {
			msg = "agent 执行失败"
		}
	}
	return &RunResult{Success: ok, Message: msg, Reward: ar.Reward, Balance: -1}
}

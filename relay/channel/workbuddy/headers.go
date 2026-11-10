// headers.go WorkBuddy 上游三类请求头（common / chat / billing / refresh）。
// 移植自 _research/workbuddy2api-panel-main internal/upstream/headers.go，
// 凭据对象从 auth.Auth（文件+锁）适配为本包 Cred（DB JSON，单请求内只读快照）。
package workbuddy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

const (
	// defaultClientVersion 出站 WorkBuddy 客户端版本段（对齐官方 5.5.4 分发包）。
	defaultClientVersion = "5.5.4"
	// defaultCliVersion 出站 UA 中 CLI/<ver> 段版本（官方内置 CLI 2.137.1）。
	defaultCliVersion = "2.137.1"

	originRefererCN     = "https://www.codebuddy.cn"
	originRefererGlobal = "https://www.workbuddy.ai"
)

// originRefererFor 按账号 realm 返回 Origin/Referer 基础域。
func originRefererFor(c *Cred) string {
	if c != nil && c.IsGlobal() {
		return originRefererGlobal
	}
	return originRefererCN
}

// defaultWorkBuddyUAFor 组装默认客户端出站 UA（官方桌面端形态）：
// `WorkBuddy/<ver> <platform>/<ver> CLI/<cliVer>`；global 平台段用 `WorkBuddy AI`。
func defaultWorkBuddyUAFor(c *Cred) string {
	platform := "WorkBuddy"
	if c != nil && c.IsGlobal() {
		platform = "WorkBuddy AI"
	}
	return "WorkBuddy/" + defaultClientVersion + " " + platform + "/" + defaultClientVersion + " CLI/" + defaultCliVersion
}

// billingUA 白名单类（billing/checkin/banner）出站 UA：单段 `WorkBuddy/<ver>`。
func billingUA() string {
	return "WorkBuddy/" + defaultClientVersion
}

// deriveAccountStableID 按 uid + 用途盐稳定派生 36 hex 设备/会话标识
// （跨重启稳定、账号间互异——防多号被上游按设备指纹缺失/漂移关联风控）。
func deriveAccountStableID(uid, purpose string) string {
	sum := sha256.Sum256([]byte("wb2a:" + purpose + ":" + uid))
	return hex.EncodeToString(sum[:18])
}

// injectAccountStableHeaders 注入 X-Machine-ID / X-Session-ID（uid 为空不注入）。
func injectAccountStableHeaders(req *http.Request, c *Cred) {
	if c == nil || c.UID == "" {
		return
	}
	req.Header.Set("X-Machine-ID", deriveAccountStableID(c.UID, "machine"))
	req.Header.Set("X-Session-ID", deriveAccountStableID(c.UID, "session"))
}

// acceptLanguageFor 按账号 realm 返回 Accept-Language。
func acceptLanguageFor(c *Cred) string {
	if c != nil && c.IsGlobal() {
		return "en-US"
	}
	return "zh-CN"
}

// injectDeviceToken 注入 X-Device-Token 头（凭据带 deviceToken 时）。
func injectDeviceToken(req *http.Request, c *Cred) {
	if c != nil && c.DeviceToken != "" {
		req.Header.Set("X-Device-Token", c.DeviceToken)
	}
}

// CommonHeaders 设置所有 API 共享的请求头。
func CommonHeaders(req *http.Request, c *Cred) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	origin := originRefererFor(c)
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("User-Agent", defaultWorkBuddyUAFor(c))
	// X-CodeBuddy-Request: 1（官方客户端风控闸门头，所有 API 请求必带）。
	req.Header.Set("X-CodeBuddy-Request", "1")
	req.Header.Set("Accept-Language", acceptLanguageFor(c))
	injectAccountStableHeaders(req, c)
}

// ChatMeta 一次 chat 出站的会话头族元数据（官方客户端一次 user send 内所有
// tool call/重试复用同一个 ConversationRequestID）。
type ChatMeta struct {
	ConversationID        string // X-Conversation-ID：空则自动生成
	ConversationRequestID string // 聚合主键，空则自动生成
	TraceID               string // X-Trace-ID：空则回落 ConversationRequestID
}

// newMessageID 32 hex 消息级 ID（官方客户端 UUID 去横线形态）。
func newMessageID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ChatHeaders 在 common 之上加 chat 专属的账号头。
// 安全红线：绝不在 chat 请求里携带 X-Refresh-Token。
func ChatHeaders(req *http.Request, c *Cred, meta ChatMeta) {
	CommonHeaders(req, c)
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	} else {
		req.Header.Set("X-No-Authorization", "1")
	}
	if c.UID != "" {
		req.Header.Set("X-User-Id", c.UID)
	} else {
		req.Header.Set("X-No-User-Id", "1")
	}
	if !c.IsGlobal() {
		if c.EnterpriseID != "" {
			req.Header.Set("X-Enterprise-Id", c.EnterpriseID)
		} else {
			req.Header.Set("X-No-Enterprise-Id", "1")
		}
		if c.Domain != "" {
			req.Header.Set("X-Domain", c.Domain)
		} else {
			req.Header.Set("X-No-Department-Info", "1")
		}
	} else {
		// global 账号：显式声明无企业 + 国际版域（对齐 intl 客户端形态）。
		req.Header.Set("X-No-Enterprise-Id", "1")
		req.Header.Set("X-Domain", "www.workbuddy.ai")
	}
	// 用量归属头：对齐官方 WorkBuddy 桌面端指纹。
	req.Header.Set("X-Agent-Purpose", "conversation")
	req.Header.Set("X-IDE-Name", "WorkBuddy")
	req.Header.Set("X-IDE-Type", "WorkBuddy")
	req.Header.Set("X-IDE-Version", defaultClientVersion)
	req.Header.Set("X-Product", "WorkBuddy")
	injectDeviceToken(req, c)
	injectConversationHeaders(req, meta)
}

// injectConversationHeaders 注入官方客户端会话头族（对话/请求/消息/B3 链路）。
func injectConversationHeaders(req *http.Request, meta ChatMeta) {
	convReqID := meta.ConversationRequestID
	if convReqID == "" {
		convReqID = newMessageID()
	}
	messageID := newMessageID()
	conversationID := meta.ConversationID
	if conversationID == "" {
		conversationID = newMessageID()
	}
	req.Header.Set("X-Conversation-ID", conversationID)
	req.Header.Set("X-Conversation-Request-ID", convReqID)
	req.Header.Set("X-Conversation-Message-ID", messageID)
	req.Header.Set("X-Request-ID", messageID)
	req.Header.Set("X-Root-Request-ID", convReqID)
	traceID := meta.TraceID
	if traceID == "" {
		traceID = convReqID
	}
	req.Header.Set("X-Trace-ID", traceID)
	b3Trace := convReqID
	if !validTraceID(b3Trace) {
		b3Trace = messageID
	}
	req.Header.Set("X-B3-TraceId", b3Trace)
	req.Header.Set("X-B3-SpanId", messageID[:16])
	req.Header.Set("X-B3-Sampled", "1")
}

// validTraceID 判断 B3 TraceId 是否合法：16 或 32 位 hex。
func validTraceID(s string) bool {
	if len(s) != 16 && len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

// BillingHeaders billing 接口请求头（codebuddy.cn 域）。
func BillingHeaders(req *http.Request, c *Cred) {
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CodeBuddy-Request", "1")
	req.Header.Set("Accept-Language", acceptLanguageFor(c))
	req.Header.Set("User-Agent", billingUA())
	if c.UID != "" {
		req.Header.Set("X-User-Id", c.UID)
	}
	if c.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", c.EnterpriseID)
		req.Header.Set("X-Tenant-Id", c.EnterpriseID)
	}
	if c.Domain != "" {
		req.Header.Set("X-Domain", c.Domain)
	}
	injectDeviceToken(req, c)
}

// RefreshHeaders refresh 端点专属头（X-Refresh-Token 只允许出现在这里）。
func RefreshHeaders(req *http.Request, c *Cred) {
	CommonHeaders(req, c)
	req.Header.Set("X-Refresh-Token", c.RefreshToken)
	if c.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", c.EnterpriseID)
	}
	// X-Auth-Refresh-Source 对齐官方客户端 refresh 渠道标识 "plugin"。
	req.Header.Set("X-Auth-Refresh-Source", "plugin")
}

// cache_key.go 注入上游 prompt_cache_key 字段（P0 费用优化）。
// 移植自 _research/workbuddy2api-panel-main internal/upstream/cache_key.go。
//
// 逆向实测：上游服务端支持 prompt_cache_key，同一段 8k token 前缀命中后
// credit≈0.02（费用降 ~17×）。按账号隔离生成稳定 cache key。
package workbuddy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// InjectPromptCacheKey 在已改写的出站 body 上注入 prompt_cache_key 字段。
// 优先级：body 已带 → 原值保留；body 带 conversation_id/conversationId → 做哈希源；
// 否则用入站 conversationID 参数。键格式 `wb2a-<uid8>-<convHex>`（uid 硬隔离）。
func InjectPromptCacheKey(body []byte, uid, conversationID string) []byte {
	if len(body) == 0 {
		return body
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	if existing, ok := obj["prompt_cache_key"].(string); ok && existing != "" {
		return body
	}
	conv := conversationID
	if v := strField(obj, "conversation_id"); v != "" {
		conv = v
	} else if v := strField(obj, "conversationId"); v != "" {
		conv = v
	}
	obj["prompt_cache_key"] = buildCacheKey(uid, conv)
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// buildCacheKey 生成 `wb2a-<uid8>-<convHex>` 稳定 cache key。
func buildCacheKey(uid, conversation string) string {
	uid8 := uid
	if len(uid8) > 8 {
		uid8 = uid8[:8]
	}
	if uid8 == "" {
		uid8 = "-"
	}
	sum := sha256.Sum256([]byte(uid + "|" + conversation))
	convHex := hex.EncodeToString(sum[:16])
	return "wb2a-" + uid8 + "-" + convHex
}

// strField 从 map 取 string 字段。
func strField(obj map[string]any, key string) string {
	v, ok := obj[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// tool_pairing.go 出站请求体的孤儿 tool_call↔tool 配对清理 + tool 结果块重排。
// 移植自 _research/workbuddy2api-panel-main internal/upstream/tool_pairing.go。
//
// 背景：OpenAI 兼容协议要求带 tool_calls 的 assistant 消息，其每一个 tool_call id
// 都必须有对应的一条 role:tool 结果消息；反之亦然。缺任一侧上游都 400。
package workbuddy

import "strings"

// repackToolResultBlocks 把插在 assistant.tool_calls 与其 tool 结果之间的非 tool 消息
// 挪到整组之后，保证同一批 tool_call 的结果在 wire 上连续。只调顺序、不改内容。
func repackToolResultBlocks(messages []any) ([]any, bool) {
	if len(messages) < 3 {
		return messages, false
	}
	out := make([]any, 0, len(messages))
	changed := false
	i := 0
	for i < len(messages) {
		m, ok := messages[i].(map[string]any)
		if !ok || m["role"] != "assistant" {
			out = append(out, messages[i])
			i++
			continue
		}
		tcs, hasCalls := m["tool_calls"].([]any)
		if !hasCalls || len(tcs) == 0 {
			out = append(out, messages[i])
			i++
			continue
		}
		want := map[string]bool{}
		for _, tci := range tcs {
			if tc, ok := tci.(map[string]any); ok {
				if id, _ := tc["id"].(string); id != "" {
					want[id] = true
				}
			}
		}
		out = append(out, messages[i])
		i++
		var results []any
		var between []any
		sawNonTool := false
		for i < len(messages) {
			mm, ok := messages[i].(map[string]any)
			if !ok {
				break
			}
			role, _ := mm["role"].(string)
			if role == "tool" {
				id, _ := mm["tool_call_id"].(string)
				if !want[id] {
					break
				}
				results = append(results, messages[i])
				if sawNonTool {
					changed = true
				}
				i++
				continue
			}
			if len(results) == 0 {
				break
			}
			// 下一组 assistant.tool_calls 是新的组头，绝不能当插入物吞掉。
			if role == "assistant" {
				if next, _ := mm["tool_calls"].([]any); len(next) > 0 {
					break
				}
			}
			between = append(between, messages[i])
			sawNonTool = true
			i++
		}
		out = append(out, results...)
		out = append(out, between...)
	}
	return out, changed
}

// mergeAdjacentToolCalls 合并背靠背的两条 assistant.tool_calls 为一条
// （部分 agent 客户端回放并行调用的报文形状；上游 deepseek 系对此 11148）。
func mergeAdjacentToolCalls(messages []any) ([]any, bool) {
	if len(messages) < 2 {
		return messages, false
	}
	out := make([]any, 0, len(messages))
	changed := false
	for _, rawMsg := range messages {
		m, ok := rawMsg.(map[string]any)
		if !ok || m["role"] != "assistant" {
			out = append(out, rawMsg)
			continue
		}
		tcs, hasCalls := m["tool_calls"].([]any)
		if !hasCalls || len(tcs) == 0 {
			out = append(out, rawMsg)
			continue
		}
		// 上一条也是带 tool_calls 的 assistant 且本消息无正文 → 合并进上一条。
		if len(out) > 0 {
			if prev, ok2 := out[len(out)-1].(map[string]any); ok2 && prev["role"] == "assistant" {
				if prevCalls, ok3 := prev["tool_calls"].([]any); ok3 && len(prevCalls) > 0 {
					content, _ := m["content"].(string)
					if strings.TrimSpace(content) == "" {
						prev["tool_calls"] = append(prevCalls, tcs...)
						changed = true
						continue
					}
				}
			}
		}
		out = append(out, rawMsg)
	}
	return out, changed
}

// cleanupOrphanToolCalls 剔除无法配对的 tool_call 与孤儿 tool 结果消息：
//   - assistant.tool_calls 中没有任何对应 role:tool 结果的调用 → 从 tool_calls 移除；
//     清空后无 content 的 assistant 整条删除。
//   - role:tool 但 tool_call_id 在前面任何 assistant.tool_calls 里都找不到 → 删除该消息。
func cleanupOrphanToolCalls(messages []any) ([]any, bool) {
	changed := false
	out := make([]any, 0, len(messages))
	answered := map[string]bool{} // 已有结果的 call id
	for _, rawMsg := range messages {
		m, ok := rawMsg.(map[string]any)
		if !ok {
			out = append(out, rawMsg)
			continue
		}
		role, _ := m["role"].(string)
		switch role {
		case "assistant":
			tcs, hasCalls := m["tool_calls"].([]any)
			if !hasCalls || len(tcs) == 0 {
				out = append(out, rawMsg)
				continue
			}
			kept := make([]any, 0, len(tcs))
			for _, tci := range tcs {
				tc, ok := tci.(map[string]any)
				if !ok {
					changed = true
					continue
				}
				id, _ := tc["id"].(string)
				if id == "" {
					kept = append(kept, tc)
					continue
				}
				if answered[id] {
					kept = append(kept, tc)
				} else {
					changed = true
				}
			}
			if len(kept) == 0 {
				delete(m, "tool_calls")
				content, _ := m["content"].(string)
				if strings.TrimSpace(content) == "" {
					changed = true
					continue // 整条删除
				}
			} else if len(kept) != len(tcs) {
				m["tool_calls"] = kept
			}
			out = append(out, m)
		case "tool":
			id, _ := m["tool_call_id"].(string)
			if id != "" {
				// 结果必须能配对到前面某个 assistant 的 call；否则删除。
				if !hasPendingCall(out, id) {
					changed = true
					continue
				}
				answered[id] = true
			}
			out = append(out, m)
		default:
			out = append(out, rawMsg)
		}
	}
	return out, changed
}

// hasPendingCall 报告已收集消息里是否存在 id 对应的 assistant.tool_calls。
func hasPendingCall(msgs []any, id string) bool {
	for _, rawMsg := range msgs {
		m, ok := rawMsg.(map[string]any)
		if !ok || m["role"] != "assistant" {
			continue
		}
		tcs, _ := m["tool_calls"].([]any)
		for _, tci := range tcs {
			if tc, ok := tci.(map[string]any); ok {
				if cid, _ := tc["id"].(string); cid == id {
					return true
				}
			}
		}
	}
	return false
}

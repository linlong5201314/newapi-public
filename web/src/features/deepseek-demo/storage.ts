/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { DemoConversation } from './types'

/**
 * DeepSeek 风格对话页的本地会话存储。
 *
 * 按用户分桶（dsdemo:<uid>），换账号不会互相看到历史；容量上限 50 条，
 * 写入失败（隐私模式 / 配额满）时静默降级为纯内存会话，不阻断对话。
 */
const STORAGE_PREFIX = 'dsdemo'
const MAX_CONVERSATIONS = 50

function storageKey(userId: number | undefined): string {
  return `${STORAGE_PREFIX}:${userId ?? 'anon'}`
}

export function loadConversations(userId?: number): DemoConversation[] {
  if (typeof window === 'undefined') return []
  try {
    const raw = window.localStorage.getItem(storageKey(userId))
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    return parsed.filter(isConversation).slice(0, MAX_CONVERSATIONS)
  } catch {
    return []
  }
}

export function saveConversations(
  userId: number | undefined,
  conversations: DemoConversation[]
): void {
  if (typeof window === 'undefined') return
  try {
    const trimmed = [...conversations]
      .sort((a, b) => b.updatedAt - a.updatedAt)
      .slice(0, MAX_CONVERSATIONS)
    window.localStorage.setItem(
      storageKey(userId),
      JSON.stringify(trimmed)
    )
  } catch {
    // 配额满 / 隐私模式：保留内存态即可。
  }
}

function isConversation(value: unknown): value is DemoConversation {
  if (!value || typeof value !== 'object') return false
  const record = value as Partial<DemoConversation>
  return (
    typeof record.id === 'string' &&
    typeof record.title === 'string' &&
    Array.isArray(record.messages)
  )
}

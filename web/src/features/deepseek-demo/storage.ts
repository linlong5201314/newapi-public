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
import type {
  DemoAttachment,
  DemoConversation,
  DemoImage,
  DemoMessage,
} from './types'

const MAX_CONVERSATIONS = 50

export function loadConversations(userId?: number): DemoConversation[] {
  if (typeof window === 'undefined' || userId === undefined) return []
  try {
    const raw = window.localStorage.getItem(`dsdemo:${userId}`)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed
      .filter(isConversation)
      .sort((a, b) => b.updatedAt - a.updatedAt)
      .slice(0, MAX_CONVERSATIONS)
      .map((conversation) => ({
        ...conversation,
        messages: conversation.messages.map((message) =>
          message.status === 'streaming'
            ? { ...message, status: 'stopped' }
            : message
        ),
      }))
  } catch {
    return []
  }
}

export function saveConversations(
  userId: number | undefined,
  conversations: DemoConversation[]
): void {
  if (typeof window === 'undefined' || userId === undefined) return
  try {
    const trimmed = [...conversations]
      .sort((a, b) => b.updatedAt - a.updatedAt)
      .slice(0, MAX_CONVERSATIONS)
    window.localStorage.setItem(`dsdemo:${userId}`, JSON.stringify(trimmed))
  } catch {
    // Storage denied or full: retain the in-memory conversation.
  }
}

function isAttachment(value: unknown): value is DemoAttachment {
  if (!value || typeof value !== 'object') return false
  const record = value as Partial<DemoAttachment>
  return (
    typeof record.id === 'string' &&
    typeof record.name === 'string' &&
    (record.kind === 'image' || record.kind === 'text') &&
    typeof record.dataUrl === 'string' &&
    (record.textContent === undefined ||
      typeof record.textContent === 'string')
  )
}

function isImage(value: unknown): value is DemoImage {
  if (!value || typeof value !== 'object') return false
  const record = value as Partial<DemoImage>
  return (
    typeof record.url === 'string' &&
    (record.kind === 'url' || record.kind === 'b64')
  )
}

function isMessage(value: unknown): value is DemoMessage {
  if (!value || typeof value !== 'object') return false
  const record = value as Partial<DemoMessage>
  return (
    typeof record.id === 'string' &&
    (record.role === 'user' || record.role === 'assistant') &&
    typeof record.content === 'string' &&
    typeof record.createdAt === 'number' &&
    Number.isFinite(record.createdAt) &&
    ['streaming', 'complete', 'error', 'stopped'].includes(
      record.status ?? ''
    ) &&
    (record.reasoning === undefined || typeof record.reasoning === 'string') &&
    (record.error === undefined || typeof record.error === 'string') &&
    (record.latencyMs === undefined || typeof record.latencyMs === 'number') &&
    (record.image === undefined || isImage(record.image)) &&
    (record.attachments === undefined ||
      (Array.isArray(record.attachments) &&
        record.attachments.every(isAttachment)))
  )
}

function isConversation(value: unknown): value is DemoConversation {
  if (!value || typeof value !== 'object') return false
  const record = value as Partial<DemoConversation>
  return (
    typeof record.id === 'string' &&
    typeof record.title === 'string' &&
    typeof record.model === 'string' &&
    typeof record.group === 'string' &&
    typeof record.createdAt === 'number' &&
    Number.isFinite(record.createdAt) &&
    typeof record.updatedAt === 'number' &&
    Number.isFinite(record.updatedAt) &&
    Array.isArray(record.messages) &&
    record.messages.every(isMessage)
  )
}

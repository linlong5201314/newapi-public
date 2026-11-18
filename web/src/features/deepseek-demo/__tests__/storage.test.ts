import { afterEach, describe, expect, it, vi } from 'vitest'

import { loadConversations, saveConversations } from '../storage'
import type { DemoConversation } from '../types'

const conversation: DemoConversation = {
  id: 'chat-1',
  title: 'Hello',
  model: 'deepseek-chat',
  group: 'default',
  createdAt: 1,
  updatedAt: 2,
  messages: [
    {
      id: 'reply',
      role: 'assistant',
      content: 'Partial',
      status: 'streaming',
      createdAt: 2,
    },
  ],
}

afterEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
})

describe('demo history storage', () => {
  it('restores an interrupted reply as stopped rather than generating forever', () => {
    localStorage.setItem('dsdemo:1', JSON.stringify([conversation]))
    expect(loadConversations(1)[0].messages[0]).toMatchObject({
      content: 'Partial',
      status: 'stopped',
    })
  })

  it('rejects malformed nested messages without breaking valid history', () => {
    localStorage.setItem(
      'dsdemo:1',
      JSON.stringify([{ ...conversation, messages: [null] }, conversation])
    )
    expect(loadConversations(1)).toHaveLength(1)
  })

  it('keeps histories isolated by account and never stores anonymous history', () => {
    saveConversations(1, [conversation])
    expect(loadConversations(2)).toEqual([])
    saveConversations(undefined, [conversation])
    expect(localStorage.getItem('dsdemo:anon')).toBeNull()
    expect(loadConversations()).toEqual([])
  })

  it('keeps the most recent 50 conversations first and tolerates denied storage', () => {
    saveConversations(
      1,
      Array.from({ length: 52 }, (_, n) => ({
        ...conversation,
        id: String(n),
        updatedAt: n,
      }))
    )
    expect(loadConversations(1)).toHaveLength(50)
    expect(loadConversations(1)[0].id).toBe('51')
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    expect(loadConversations(1)).toEqual([])
  })
})

import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { useDemoChat } from '../use-demo-chat'

const transport = vi.hoisted(() => ({
  sources: [] as Array<{
    payload: string
    closed: boolean
    emit: (type: string, data: string) => void
  }>,
}))
vi.mock('@/lib/api', () => ({
  getFreshAuthHeaders: async () => ({ 'X-Test-Session': 'fixture' }),
}))
vi.mock('sse.js', () => ({
  SSE: class {
    readyState = 1
    closed = false
    payload: string
    listeners = new Map<string, (event: { data: string }) => void>()
    constructor(_url: string, options: { payload: string }) {
      this.payload = options.payload
      transport.sources.push(this)
    }
    addEventListener(
      type: string,
      listener: (event: { data: string }) => void
    ) {
      this.listeners.set(type, listener)
    }
    stream() {}
    close() {
      this.closed = true
    }
    emit(type: string, data: string) {
      this.listeners.get(type)?.({ data })
    }
  },
}))

afterEach(() => {
  localStorage.clear()
  transport.sources.length = 0
})

describe('demo authenticated conversation flow', () => {
  it('streams reasoning and content to one reply and persists completion', async () => {
    const { result } = renderHook(() => useDemoChat(7))
    act(() => {
      result.current.send('Hello', 'deepseek-chat', 'team')
    })
    await waitFor(() => expect(transport.sources).toHaveLength(1))
    const source = transport.sources[0]
    expect(JSON.parse(source.payload)).toMatchObject({
      model: 'deepseek-chat',
      group: 'team',
      messages: [{ role: 'user', content: 'Hello' }],
    })
    act(() => {
      source.emit(
        'message',
        JSON.stringify({
          choices: [
            { delta: { reasoning_content: 'Thinking', content: 'Hi' } },
          ],
        })
      )
      source.emit('message', '[DONE]')
    })
    expect(result.current.pending).toBe(false)
    expect(result.current.activeConversation?.messages.at(-1)).toMatchObject({
      content: 'Hi',
      reasoning: 'Thinking',
      status: 'complete',
    })
    expect(
      JSON.parse(localStorage.getItem('dsdemo:7') ?? '[]')[0].messages
    ).toHaveLength(2)
  })

  it('shows a failed empty reply and regenerates using current model and group', async () => {
    const { result } = renderHook(() => useDemoChat(7))
    act(() => {
      result.current.send('Hello', 'old-model', 'old-group')
    })
    await waitFor(() => expect(transport.sources).toHaveLength(1))
    act(() =>
      transport.sources[0].emit(
        'error',
        JSON.stringify({ error: { message: 'Network unavailable' } })
      )
    )
    expect(result.current.activeConversation?.messages.at(-1)).toMatchObject({
      status: 'error',
      content: '',
    })
    expect(result.current.pending).toBe(false)
    act(() => result.current.regenerate('new-model', 'new-group'))
    await waitFor(() => expect(transport.sources).toHaveLength(2))
    expect(JSON.parse(transport.sources[1].payload)).toMatchObject({
      model: 'new-model',
      group: 'new-group',
      messages: [{ role: 'user', content: 'Hello' }],
    })
  })

  it('rejects double send while headers are pending and ignores late chunks after stop', async () => {
    const { result } = renderHook(() => useDemoChat(7))
    act(() => {
      expect(result.current.send('First', 'model', 'default')).toBe(true)
      expect(result.current.send('Duplicate', 'model', 'default')).toBe(false)
    })
    await waitFor(() => expect(transport.sources).toHaveLength(1))
    act(() => {
      result.current.stop()
      transport.sources[0].emit(
        'message',
        JSON.stringify({ choices: [{ delta: { content: 'late' } }] })
      )
    })
    expect(transport.sources[0].closed).toBe(true)
    expect(result.current.activeConversation?.messages.at(-1)).toMatchObject({
      content: '',
      status: 'stopped',
    })
  })

  it('stops a backgrounded stream and reloads partial history without replaying requests', async () => {
    const first = renderHook(() => useDemoChat(7))
    act(() => {
      first.result.current.send('First', 'model', 'default')
    })
    await waitFor(() => expect(transport.sources).toHaveLength(1))
    act(() => {
      transport.sources[0].emit(
        'message',
        JSON.stringify({ choices: [{ delta: { content: 'Partial' } }] })
      )
      window.dispatchEvent(new Event('pagehide'))
    })
    expect(first.result.current.pending).toBe(false)
    first.unmount()
    const restored = renderHook(() => useDemoChat(7))
    expect(
      restored.result.current.activeConversation?.messages.at(-1)
    ).toMatchObject({ content: 'Partial', status: 'stopped' })
    expect(transport.sources).toHaveLength(1)
  })

  it('adds reasoning_effort to the payload only when Deep thinking is enabled', async () => {
    const { result } = renderHook(() => useDemoChat(7))
    act(() => {
      result.current.setThinkingEnabled(true)
      result.current.send('Hello', 'deepseek-chat', 'default')
    })
    await waitFor(() => expect(transport.sources).toHaveLength(1))
    expect(JSON.parse(transport.sources[0].payload)).toMatchObject({
      reasoning_effort: 'medium',
    })
    act(() => {
      transport.sources[0].emit('message', '[DONE]')
      result.current.setThinkingEnabled(false)
      result.current.send('Second', 'deepseek-chat', 'default')
    })
    await waitFor(() => expect(transport.sources).toHaveLength(2))
    expect(JSON.parse(transport.sources[1].payload)).not.toHaveProperty(
      'reasoning_effort'
    )
  })

  it('sends image attachments as multimodal content parts in the stream payload', async () => {
    const { result } = renderHook(() => useDemoChat(7))
    const png = new File(['fake-png-bytes'], 'pic.png', { type: 'image/png' })
    await act(async () => {
      await result.current.addAttachmentFiles([png])
    })
    expect(result.current.attachments).toHaveLength(1)
    act(() => {
      result.current.send(
        'What is in this image?',
        'gpt-4o',
        'default',
        result.current.attachments
      )
    })
    await waitFor(() => expect(transport.sources).toHaveLength(1))
    const payload = JSON.parse(transport.sources[0].payload)
    const content = payload.messages[0].content
    expect(Array.isArray(content)).toBe(true)
    expect(content[0]).toEqual({
      type: 'text',
      text: 'What is in this image?',
    })
    expect(content[1].type).toBe('image_url')
    expect(content[1].image_url.url.startsWith('data:image/png;base64,')).toBe(
      true
    )
  })

  it('keeps legacy string content when no attachments are present', async () => {
    const { result } = renderHook(() => useDemoChat(7))
    act(() => {
      result.current.send('plain', 'deepseek-chat', 'default')
    })
    await waitFor(() => expect(transport.sources).toHaveLength(1))
    const payload = JSON.parse(transport.sources[0].payload)
    expect(payload.messages[0].content).toBe('plain')
  })

  it('deleting the streaming conversation closes its request and leaves no empty ghost chat', async () => {
    const { result } = renderHook(() => useDemoChat(7))
    act(() => {
      result.current.send('First', 'model', 'default')
    })
    await waitFor(() => expect(transport.sources).toHaveLength(1))
    act(() => result.current.remove(result.current.activeId))
    expect(result.current.conversations).toEqual([])
    expect(result.current.activeConversation).toBeUndefined()
    expect(transport.sources[0].closed).toBe(true)
  })
})

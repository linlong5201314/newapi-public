import { nanoid } from 'nanoid'
import { useCallback, useEffect, useRef, useState } from 'react'

import { useStreamRequest } from '@/features/playground/hooks/use-stream-request'
import type { ChatCompletionRequest } from '@/features/playground/types'

import { loadConversations, saveConversations } from './storage'
import type { DemoConversation, DemoMessage } from './types'

export function useDemoChat(userId: number) {
  const [conversations, setConversations] = useState(() =>
    loadConversations(userId)
  )
  const [activeId, setActiveId] = useState(conversations[0]?.id ?? '')
  const [pending, setPending] = useState(false)
  const requestRef = useRef<{
    conversationId: string
    messageId: string
  } | null>(null)
  const { sendStreamRequest, stopStream } = useStreamRequest()

  useEffect(() => {
    saveConversations(userId, conversations)
  }, [conversations, userId])
  const patchConversation = useCallback(
    (
      id: string,
      updater: (conversation: DemoConversation) => DemoConversation
    ) => {
      setConversations((prev) =>
        prev.map((item) => (item.id === id ? updater(item) : item))
      )
    },
    []
  )
  const patchMessage = useCallback(
    (
      conversationId: string,
      messageId: string,
      updater: (message: DemoMessage) => DemoMessage
    ) => {
      patchConversation(conversationId, (conversation) => ({
        ...conversation,
        messages: conversation.messages.map((message) =>
          message.id === messageId ? updater(message) : message
        ),
        updatedAt: Date.now(),
      }))
    },
    [patchConversation]
  )

  const stop = useCallback(() => {
    const request = requestRef.current
    requestRef.current = null
    stopStream()
    setPending(false)
    if (request) {
      patchMessage(request.conversationId, request.messageId, (message) => ({
        ...message,
        status: 'stopped',
      }))
    }
  }, [patchMessage, stopStream])

  useEffect(() => {
    const onVisibilityChange = () => {
      if (document.visibilityState === 'hidden') stop()
    }
    window.addEventListener('offline', stop)
    window.addEventListener('pagehide', stop)
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => {
      requestRef.current = null
      window.removeEventListener('offline', stop)
      window.removeEventListener('pagehide', stop)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [stop])

  const runCompletion = (
    conversation: DemoConversation,
    history: DemoMessage[],
    assistantId: string
  ) => {
    const request = { conversationId: conversation.id, messageId: assistantId }
    requestRef.current = request
    setPending(true)
    const startedAt = Date.now()
    let firstChunkAt = 0
    const payload: ChatCompletionRequest = {
      model: conversation.model,
      group: conversation.group,
      stream: true,
      messages: history
        .filter(
          (message) =>
            message.role === 'user' ||
            (message.status === 'complete' && message.content.trim())
        )
        .map((message) => ({ role: message.role, content: message.content })),
    }
    const finish = (status: 'complete' | 'error', error?: string) => {
      if (requestRef.current !== request) return
      requestRef.current = null
      setPending(false)
      patchMessage(conversation.id, assistantId, (message) => ({
        ...message,
        status,
        error,
        latencyMs: (firstChunkAt || Date.now()) - startedAt,
      }))
    }
    void Promise.resolve(
      sendStreamRequest(
        payload,
        (type, chunk) => {
          if (requestRef.current !== request) return
          if (!firstChunkAt) firstChunkAt = Date.now()
          patchMessage(conversation.id, assistantId, (message) =>
            type === 'reasoning'
              ? { ...message, reasoning: `${message.reasoning ?? ''}${chunk}` }
              : { ...message, content: `${message.content}${chunk}` }
          )
        },
        () => finish('complete'),
        (error) => finish('error', error)
      )
    ).catch(() =>
      finish('error', 'Request failed. Check your connection and try again.')
    )
  }

  const activeConversation = conversations.find((item) => item.id === activeId)
  const send = (text: string, model: string, group: string): boolean => {
    if (!text.trim() || !model || requestRef.current) return false
    const now = Date.now()
    const userMessage: DemoMessage = {
      id: nanoid(),
      role: 'user',
      content: text.trim(),
      status: 'complete',
      createdAt: now,
    }
    const assistantMessage: DemoMessage = {
      id: nanoid(),
      role: 'assistant',
      content: '',
      status: 'streaming',
      createdAt: now,
    }
    const history = [...(activeConversation?.messages ?? []), userMessage]
    const conversation: DemoConversation = {
      id: activeConversation?.id ?? nanoid(),
      title: activeConversation?.title ?? text.trim().slice(0, 50),
      model,
      group,
      messages: [...history, assistantMessage],
      createdAt: activeConversation?.createdAt ?? now,
      updatedAt: now,
    }
    setConversations((prev) => [
      conversation,
      ...prev.filter((item) => item.id !== conversation.id),
    ])
    setActiveId(conversation.id)
    runCompletion(conversation, history, assistantMessage.id)
    return true
  }

  const regenerate = (model: string, group: string) => {
    if (!activeConversation || requestRef.current || !model) return
    const lastUserIndex = activeConversation.messages
      .map((message) => message.role)
      .lastIndexOf('user')
    if (lastUserIndex < 0) return
    const history = activeConversation.messages.slice(0, lastUserIndex + 1)
    const assistantId = nanoid()
    const conversation: DemoConversation = {
      ...activeConversation,
      model,
      group,
      updatedAt: Date.now(),
      messages: [
        ...history,
        {
          id: assistantId,
          role: 'assistant',
          content: '',
          status: 'streaming',
          createdAt: Date.now(),
        },
      ],
    }
    patchConversation(conversation.id, () => conversation)
    runCompletion(conversation, history, assistantId)
  }
  const remove = (id: string) => {
    if (requestRef.current?.conversationId === id) stop()
    setConversations((prev) => prev.filter((item) => item.id !== id))
    setActiveId((prev) => (prev === id ? '' : prev))
  }
  const select = (id: string) => {
    stop()
    setActiveId(id)
  }
  return {
    conversations,
    activeId,
    activeConversation,
    pending,
    stop,
    send,
    regenerate,
    remove,
    select,
  }
}

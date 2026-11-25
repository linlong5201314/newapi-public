import { nanoid } from 'nanoid'
import { useCallback, useEffect, useRef, useState } from 'react'

import { useStreamRequest } from '@/features/playground/hooks/use-stream-request'
import type {
  ChatCompletionMessage,
  ChatCompletionRequest,
} from '@/features/playground/types'

import {
  buildAttachmentContentParts,
  MAX_ATTACHMENTS,
  readDemoAttachments,
  type AttachmentRejectReason,
} from './attachments'
import { generateDemoImage } from './image-generation'
import { loadConversations, saveConversations } from './storage'
import type { DemoAttachment, DemoConversation, DemoMessage } from './types'

export type { AttachmentRejectReason }

interface DemoChatCompletionRequest extends ChatCompletionRequest {
  /**
   * Passed through to the gateway verbatim; DeepSeek/OpenAI-compatible
   * reasoning channels map it to their native effort control. Only set
   * when the user enables the Deep thinking chip.
   */
  reasoning_effort?: string
}

export function useDemoChat(userId: number) {
  const [conversations, setConversations] = useState(() =>
    loadConversations(userId)
  )
  const [activeId, setActiveId] = useState(conversations[0]?.id ?? '')
  const [pending, setPending] = useState(false)
  const [thinkingEnabled, setThinkingEnabledState] = useState(false)
  const [attachments, setAttachments] = useState<DemoAttachment[]>([])
  const [attachmentRejections, setAttachmentRejections] = useState<
    { name: string; reason: AttachmentRejectReason }[]
  >([])
  const requestRef = useRef<{
    conversationId: string
    messageId: string
  } | null>(null)
  const imageAbortRef = useRef<AbortController | null>(null)
  const thinkingRef = useRef(false)
  const { sendStreamRequest, stopStream } = useStreamRequest()

  const setThinkingEnabled = useCallback((enabled: boolean) => {
    thinkingRef.current = enabled
    setThinkingEnabledState(enabled)
  }, [])

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
    imageAbortRef.current?.abort()
    imageAbortRef.current = null
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
      stop()
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
    const payload: DemoChatCompletionRequest = {
      model: conversation.model,
      group: conversation.group,
      stream: true,
      messages: history
        .filter(
          (message) =>
            message.role === 'user' ||
            (message.status === 'complete' && message.content.trim())
        )
        .map((message): ChatCompletionMessage => ({
          role: message.role,
          content:
            message.role === 'user'
              ? buildAttachmentContentParts(
                  message.content,
                  message.attachments ?? []
                )
              : message.content,
        })),
    }
    if (thinkingRef.current) {
      payload.reasoning_effort = 'medium'
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
  const send = (
    text: string,
    model: string,
    group: string,
    attachments: DemoAttachment[] = []
  ): boolean => {
    if (!text.trim() || !model || requestRef.current) return false
    const now = Date.now()
    const trimmed = text.trim()
    const userMessage: DemoMessage = {
      id: nanoid(),
      role: 'user',
      content: trimmed,
      status: 'complete',
      createdAt: now,
      ...(attachments.length ? { attachments } : {}),
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
      title: activeConversation?.title ?? trimmed.slice(0, 50),
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

  const clearAttachments = useCallback(() => {
    setAttachments([])
    setAttachmentRejections([])
  }, [])

  const addAttachmentFiles = useCallback(
    async (files: Iterable<File>) => {
      const result = await readDemoAttachments(files, attachments)
      if (result.added.length) {
        setAttachments((prev) =>
          [...prev, ...result.added].slice(0, MAX_ATTACHMENTS)
        )
      }
      setAttachmentRejections(result.rejected)
      return result
    },
    [attachments]
  )

  const removeAttachment = useCallback((id: string) => {
    setAttachments((prev) => prev.filter((item) => item.id !== id))
  }, [])

  const generateImage = async (
    prompt: string,
    model: string,
    group: string
  ): Promise<boolean> => {
    const trimmed = prompt.trim()
    if (!trimmed || !model || requestRef.current) return false
    const now = Date.now()
    const userMessage: DemoMessage = {
      id: nanoid(),
      role: 'user',
      content: trimmed,
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
      title: activeConversation?.title ?? trimmed.slice(0, 50),
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

    const request = {
      conversationId: conversation.id,
      messageId: assistantMessage.id,
    }
    requestRef.current = request
    const abortController = new AbortController()
    imageAbortRef.current = abortController
    setPending(true)
    const startedAt = Date.now()
    try {
      const image = await generateDemoImage({
        model,
        group,
        prompt: trimmed,
        signal: abortController.signal,
      })
      if (requestRef.current !== request) return false
      requestRef.current = null
      if (imageAbortRef.current === abortController) {
        imageAbortRef.current = null
      }
      setPending(false)
      patchMessage(conversation.id, assistantMessage.id, (message) => ({
        ...message,
        status: 'complete',
        image,
        latencyMs: Date.now() - startedAt,
      }))
      return true
    } catch (error) {
      if (requestRef.current !== request) return false
      if (requestRef.current === request) {
        requestRef.current = null
        setPending(false)
      }
      patchMessage(conversation.id, assistantMessage.id, (message) => ({
        ...message,
        status: 'error',
        error:
          error instanceof Error
            ? error.message
            : 'Image generation failed. Check your connection and try again.',
        latencyMs: Date.now() - startedAt,
      }))
      return false
    }
  }

  return {
    conversations,
    activeId,
    activeConversation,
    pending,
    thinkingEnabled,
    setThinkingEnabled,
    stop,
    send,
    regenerate,
    remove,
    select,
    attachments,
    attachmentRejections,
    addAttachmentFiles,
    removeAttachment,
    clearAttachments,
    generateImage,
  }
}

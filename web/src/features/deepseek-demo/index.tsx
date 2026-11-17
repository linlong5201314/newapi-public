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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Boxes, FlaskConical, Smartphone, SquarePen } from 'lucide-react'
import { nanoid } from 'nanoid'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { getUserGroups, getUserModels } from '@/features/playground/api'
import { useStreamRequest } from '@/features/playground/hooks/use-stream-request'
import type { ChatCompletionRequest } from '@/features/playground/types'
import { useAuthStore } from '@/stores/auth-store'

import { Composer } from './components/composer'
import { DemoSidebar } from './components/demo-sidebar'
import { DemoMessageList } from './components/message-list'
import { ModelPicker } from './components/model-picker'
import { loadConversations, saveConversations } from './storage'
import type {
  DemoConversation,
  DemoGroupOption,
  DemoMessage,
  DemoModelOption,
} from './types'

/**
 * DeepSeek 官方 demo 风格的对话首页。
 *
 * 与 `/playground` 的区别：这里是一个完整的「聊天产品」外壳——左侧会话列表、
 * 顶部模型胶囊、空态引导卡片；游乐场保持原样不动。
 * 会话只存 localStorage（按用户分桶），消息实时流式渲染。
 */
export function DeepSeekDemo() {
  const user = useAuthStore((state) => state.auth.user)
  const userId = user?.id

  const [conversations, setConversations] = useState<DemoConversation[]>([])
  const [activeId, setActiveId] = useState('')
  const [collapsed, setCollapsed] = useState(false)
  const [input, setInput] = useState('')
  const [model, setModel] = useState('')
  const [group, setGroup] = useState('')
  const hydratedRef = useRef(false)

  const { sendStreamRequest, stopStream, isStreaming } = useStreamRequest()

  // 会话按用户分桶载入；切换账号时整体重置，避免串号。
  useEffect(() => {
    if (hydratedRef.current && userId === undefined) return
    const stored = loadConversations(userId)
    hydratedRef.current = true
    setConversations(stored)
    setActiveId(stored[0]?.id ?? '')
    setModel(stored[0]?.model ?? '')
    setGroup(stored[0]?.group ?? '')
  }, [userId])

  useEffect(() => {
    if (!hydratedRef.current) return
    saveConversations(userId, conversations)
  }, [conversations, userId])

  const { data: groups } = useQuery({
    queryKey: ['deepseek-demo-groups'],
    queryFn: getUserGroups,
    staleTime: 5 * 60 * 1000,
  })

  const effectiveGroup = group || groups?.[0]?.value || 'default'

  const { data: models, isLoading: isLoadingModels } = useQuery({
    queryKey: ['deepseek-demo-models', effectiveGroup],
    queryFn: () => getUserModels(effectiveGroup),
    staleTime: 60 * 1000,
  })

  useEffect(() => {
    if (group || !groups?.length) return
    setGroup(groups[0].value)
  }, [group, groups])

  useEffect(() => {
    try {
      const fromMyModels = window.localStorage.getItem('ds-demo:preferred-model')
      if (fromMyModels) {
        window.localStorage.removeItem('ds-demo:preferred-model')
        setModel(fromMyModels)
        return
      }
    } catch {
      // ignore
    }
    if (!models?.length) return
    if (model && models.some((item) => item.value === model)) return
    const preferred =
      models.find((item) =>
        /deepseek|r1|v3/i.test(item.value)
      ) ?? models[0]
    setModel(preferred.value)
  }, [model, models])

  const groupOptions: DemoGroupOption[] = useMemo(
    () =>
      (groups ?? []).map((item) => ({
        label: item.label,
        value: item.value,
        desc: item.desc,
      })),
    [groups]
  )

  const modelOptions: DemoModelOption[] = useMemo(
    () =>
      (models ?? []).map((item) => ({
        label: item.label,
        value: item.value,
      })),
    [models]
  )

  const activeConversation = useMemo(
    () => conversations.find((item) => item.id === activeId) ?? null,
    [activeId, conversations]
  )

  const patchConversation = useCallback(
    (id: string, updater: (conversation: DemoConversation) => DemoConversation) => {
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

  const handleNew = useCallback(() => {
    stopStream()
    setActiveId('')
    setInput('')
  }, [stopStream])

  const handleDelete = useCallback(
    (id: string) => {
      setConversations((prev) => prev.filter((item) => item.id !== id))
      setActiveId((prev) => (prev === id ? '' : prev))
    },
    []
  )

  /** 真正发请求：把历史消息按 OpenAI 格式拼好，追加一条空的 assistant 占位。 */
  const runCompletion = useCallback(
    (conversation: DemoConversation, history: DemoMessage[], assistantId: string) => {
      const startedAt = Date.now()
      const payload: ChatCompletionRequest = {
        model: conversation.model || model,
        group: conversation.group || effectiveGroup,
        messages: history.map((message) => ({
          role: message.role,
          content: message.content,
        })),
        stream: true,
        temperature: 0.7,
      }

      let firstChunkAt = 0

      void sendStreamRequest(
        payload,
        (type, chunk) => {
          if (!firstChunkAt) firstChunkAt = Date.now()
          patchMessage(conversation.id, assistantId, (message) =>
            type === 'reasoning'
              ? { ...message, reasoning: `${message.reasoning ?? ''}${chunk}` }
              : { ...message, content: `${message.content}${chunk}` }
          )
        },
        () => {
          const latency = (firstChunkAt || Date.now()) - startedAt
          patchMessage(conversation.id, assistantId, (message) => ({
            ...message,
            status: 'complete',
            latencyMs: latency,
          }))
        },
        (error) => {
          patchMessage(conversation.id, assistantId, (message) => ({
            ...message,
            status: 'error',
            error,
          }))
        }
      )
    },
    [effectiveGroup, model, patchMessage, sendStreamRequest]
  )

  const handleSend = useCallback(() => {
    const text = input.trim()
    if (!text || isStreaming) return
    if (!model) return

    setInput('')

    const userMessage: DemoMessage = {
      id: nanoid(),
      role: 'user',
      content: text,
      status: 'complete',
      createdAt: Date.now(),
    }
    const assistantMessage: DemoMessage = {
      id: nanoid(),
      role: 'assistant',
      content: '',
      status: 'streaming',
      createdAt: Date.now(),
    }

    const existing = conversations.find((item) => item.id === activeId)
    if (existing) {
      const history = [...existing.messages, userMessage]
      patchConversation(existing.id, (conversation) => ({
        ...conversation,
        messages: [...history, assistantMessage],
        updatedAt: Date.now(),
      }))
      runCompletion(existing, history, assistantMessage.id)
      return
    }

    const conversation: DemoConversation = {
      id: nanoid(),
      title: text.slice(0, 30),
      model,
      group: effectiveGroup,
      messages: [userMessage, assistantMessage],
      createdAt: Date.now(),
      updatedAt: Date.now(),
    }
    setConversations((prev) => [conversation, ...prev])
    setActiveId(conversation.id)
    runCompletion(conversation, [userMessage], assistantMessage.id)
  }, [
    activeId,
    conversations,
    effectiveGroup,
    input,
    isStreaming,
    model,
    patchConversation,
    runCompletion,
  ])

  const handleStop = useCallback(() => {
    stopStream()
    if (!activeConversation) return
    const last = activeConversation.messages.at(-1)
    if (!last || last.status !== 'streaming') return
    patchMessage(activeConversation.id, last.id, (message) => ({
      ...message,
      status: 'complete',
    }))
  }, [activeConversation, patchMessage, stopStream])

  const handleRegenerate = useCallback(() => {
    if (!activeConversation || isStreaming) return
    const messages = activeConversation.messages
    const lastUserIndex = [...messages]
      .map((message, index) => ({ message, index }))
      .reverse()
      .find((item) => item.message.role === 'user')?.index
    if (lastUserIndex === undefined) return

    const history = messages.slice(0, lastUserIndex + 1)
    const assistantId = nanoid()
    patchConversation(activeConversation.id, (conversation) => ({
      ...conversation,
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
      updatedAt: Date.now(),
    }))
    runCompletion(activeConversation, history, assistantId)
  }, [activeConversation, isStreaming, patchConversation, runCompletion])

  const displayName =
    user?.display_name?.trim() || user?.username || '未登录'

  return (
    <div className='flex h-full min-h-0 w-full overflow-hidden bg-background'>
      <DemoSidebar
        conversations={conversations}
        activeId={activeId}
        userName={displayName}
        collapsed={collapsed}
        onSelect={setActiveId}
        onNew={handleNew}
        onDelete={handleDelete}
        onToggleCollapse={() => setCollapsed((prev) => !prev)}
      />

      <div className='flex min-w-0 flex-1 flex-col'>
        <header className='flex shrink-0 items-center justify-between gap-3 border-b border-border/50 px-4 py-2.5'>
          <div className='flex min-w-0 items-center gap-2'>
            <ModelPicker
              groups={groupOptions}
              models={modelOptions}
              value={model}
              group={effectiveGroup}
              isLoading={isLoadingModels}
              disabled={isStreaming}
              onGroupChange={(next) => {
                setGroup(next)
                setModel('')
              }}
              onChange={(next) => {
                setModel(next)
                if (activeConversation) {
                  patchConversation(activeConversation.id, (conversation) => ({
                    ...conversation,
                    model: next,
                  }))
                }
              }}
            />
            {activeConversation && (
              <span className='hidden truncate text-xs text-muted-foreground sm:inline'>
                {activeConversation.title}
              </span>
            )}
          </div>
          <div className='flex items-center gap-1'>
            <a
              href='/deepseek-newapi.apk'
              download='DeepSeek-NewAPI-Client.apk'
              className='flex h-8 items-center gap-1.5 rounded-lg border border-primary/30 bg-primary/10 px-2.5 text-xs font-medium text-primary transition-colors hover:bg-primary/20'
              title='下载 DeepSeek 官方风格 Android 客户端 APK（支持注册登录本站账号密码，或自定义 URL 与 API Key）'
            >
              <Smartphone className='size-3.5' />
              下载 Android APK
            </a>
            <Link
              to='/my-models'
              className='flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground'
            >
              <Boxes className='size-3.5' />
              我的模型
            </Link>
            <Link
              to='/playground'
              className='flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground'
            >
              <FlaskConical className='size-3.5' />
              游乐场
            </Link>
            <button
              type='button'
              onClick={handleNew}
              aria-label='新对话'
              className='flex size-8 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground'
            >
              <SquarePen className='size-4' />
            </button>
          </div>
        </header>

        <DemoMessageList
          messages={activeConversation?.messages ?? []}
          isStreaming={isStreaming}
          onSuggestion={(text) => setInput(text)}
          onRegenerate={handleRegenerate}
        />

        <Composer
          value={input}
          isStreaming={isStreaming}
          disabled={!model}
          placeholder={
            model
              ? `给 ${model} 发送消息，Enter 发送，Shift + Enter 换行`
              : '正在加载可用模型…'
          }
          onChange={setInput}
          onSubmit={handleSend}
          onStop={handleStop}
        />
      </div>
    </div>
  )
}

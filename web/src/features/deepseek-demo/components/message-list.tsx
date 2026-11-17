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
import {
  ChevronDown,
  Copy,
  RotateCcw,
  Sparkles,
  ThumbsDown,
  ThumbsUp,
} from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'

import { Markdown } from '@/components/ui/markdown'
import { cn } from '@/lib/utils'

import type { DemoMessage } from '../types'

const SUGGESTIONS = [
  {
    title: '写一段自我介绍',
    desc: '帮我给新同事写一段简洁的自我介绍',
  },
  {
    title: '解释代码',
    desc: '解释这段代码在做什么，并指出潜在问题',
  },
  {
    title: '整理长文',
    desc: '把下面这段内容整理成条理清晰的要点',
  },
  {
    title: '翻译润色',
    desc: '把这句中文翻译成地道英文，并说明用词选择',
  },
]

interface MessageListProps {
  messages: DemoMessage[]
  isStreaming: boolean
  onSuggestion: (text: string) => void
  onRegenerate: () => void
}

export function DemoMessageList({
  messages,
  isStreaming,
  onSuggestion,
  onRegenerate,
}: MessageListProps) {
  const bottomRef = useRef<HTMLDivElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const [stickToBottom, setStickToBottom] = useState(true)

  const lastContent = messages.at(-1)?.content ?? ''

  useEffect(() => {
    if (!stickToBottom) return
    bottomRef.current?.scrollIntoView({ block: 'end' })
  }, [lastContent, messages.length, stickToBottom])

  const handleScroll = () => {
    const el = containerRef.current
    if (!el) return
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight
    setStickToBottom(distance < 80)
  }

  if (messages.length === 0) {
    return (
      <div className='flex min-h-0 flex-1 flex-col items-center justify-center px-6'>
        <div className='mb-8 flex flex-col items-center gap-3'>
          <span className='flex size-12 items-center justify-center rounded-2xl bg-[#4d6bfe] text-xl font-bold text-white'>
            D
          </span>
          <h1 className='text-2xl font-semibold'>我是 DeepSeek，很高兴见到你！</h1>
          <p className='text-sm text-muted-foreground'>
            选好模型，直接开始聊。所有请求都走你自己的渠道。
          </p>
        </div>
        <div className='grid w-full max-w-2xl grid-cols-1 gap-3 sm:grid-cols-2'>
          {SUGGESTIONS.map((item) => (
            <button
              key={item.title}
              type='button'
              onClick={() => onSuggestion(`${item.desc}\n`)}
              className='rounded-2xl border border-border/70 bg-background p-4 text-left transition-colors hover:border-[#4d6bfe]/50 hover:bg-[#4d6bfe]/5'
            >
              <p className='text-sm font-medium'>{item.title}</p>
              <p className='mt-1 line-clamp-2 text-xs text-muted-foreground'>
                {item.desc}
              </p>
            </button>
          ))}
        </div>
      </div>
    )
  }

  return (
    <div
      ref={containerRef}
      onScroll={handleScroll}
      className='min-h-0 flex-1 overflow-y-auto'
    >
      <div className='mx-auto flex w-full max-w-3xl flex-col gap-5 px-4 py-6'>
        {messages.map((message, index) => (
          <MessageBubble
            key={message.id}
            message={message}
            isLast={index === messages.length - 1}
            isStreaming={isStreaming}
            onRegenerate={onRegenerate}
          />
        ))}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}

function MessageBubble({
  message,
  isLast,
  isStreaming,
  onRegenerate,
}: {
  message: DemoMessage
  isLast: boolean
  isStreaming: boolean
  onRegenerate: () => void
}) {
  const [reasoningOpen, setReasoningOpen] = useState(false)
  const [copied, setCopied] = useState(false)

  const isUser = message.role === 'user'
  const streaming = message.status === 'streaming'

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(message.content)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      // 剪贴板不可用时静默失败，不打断阅读。
    }
  }

  const reasoningText = useMemo(() => message.reasoning?.trim(), [message.reasoning])

  if (isUser) {
    return (
      <div className='flex justify-end'>
        <div className='max-w-[85%] rounded-2xl rounded-tr-md bg-[#eef2ff] px-4 py-2.5 text-sm leading-relaxed whitespace-pre-wrap text-foreground dark:bg-[#2a2f4a]'>
          {message.content}
        </div>
      </div>
    )
  }

  return (
    <div className='group flex gap-3'>
      <span className='mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg bg-[#4d6bfe] text-xs font-bold text-white'>
        D
      </span>
      <div className='min-w-0 flex-1'>
        {reasoningText && (
          <div className='mb-2 rounded-xl border border-border/60 bg-muted/40'>
            <button
              type='button'
              onClick={() => setReasoningOpen((prev) => !prev)}
              className='flex w-full items-center gap-1.5 px-3 py-2 text-xs font-medium text-muted-foreground'
            >
              <Sparkles className='size-3.5' />
              深度思考
              <ChevronDown
                className={cn(
                  'size-3.5 transition-transform',
                  reasoningOpen && 'rotate-180'
                )}
              />
            </button>
            {reasoningOpen && (
              <p className='max-h-64 overflow-y-auto px-3 pb-3 text-xs leading-relaxed whitespace-pre-wrap text-muted-foreground'>
                {reasoningText}
              </p>
            )}
          </div>
        )}

        {message.content ? (
          <Markdown breaks className='text-sm leading-relaxed'>
            {message.content}
          </Markdown>
        ) : (
          streaming && (
            <span className='inline-flex gap-1 py-2'>
              <Dot delay='0ms' />
              <Dot delay='150ms' />
              <Dot delay='300ms' />
            </span>
          )
        )}

        {streaming && message.content && (
          <span className='ml-0.5 inline-block h-4 w-[2px] animate-pulse bg-foreground align-middle' />
        )}

        {message.status === 'error' && (
          <p className='mt-2 rounded-lg bg-destructive/10 px-3 py-2 text-xs text-destructive'>
            {message.error || '请求失败，请检查渠道与模型是否可用'}
          </p>
        )}

        {!streaming && message.content && (
          <div className='mt-1.5 flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100'>
            <IconAction label={copied ? '已复制' : '复制'} onClick={copy}>
              <Copy className='size-3.5' />
            </IconAction>
            {isLast && !isStreaming && (
              <IconAction label='重新生成' onClick={onRegenerate}>
                <RotateCcw className='size-3.5' />
              </IconAction>
            )}
            <IconAction label='有帮助'>
              <ThumbsUp className='size-3.5' />
            </IconAction>
            <IconAction label='没帮助'>
              <ThumbsDown className='size-3.5' />
            </IconAction>
            {message.latencyMs ? (
              <span className='ml-1 text-[11px] text-muted-foreground/70'>
                {(message.latencyMs / 1000).toFixed(1)}s
              </span>
            ) : null}
          </div>
        )}
      </div>
    </div>
  )
}

function Dot({ delay }: { delay: string }) {
  return (
    <span
      style={{ animationDelay: delay }}
      className='size-1.5 animate-bounce rounded-full bg-muted-foreground/60'
    />
  )
}

function IconAction({
  label,
  onClick,
  children,
}: {
  label: string
  onClick?: () => void
  children: ReactNode
}) {
  return (
    <button
      type='button'
      aria-label={label}
      title={label}
      onClick={onClick}
      className='flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground'
    >
      {children}
    </button>
  )
}

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
import { ArrowUp, Square } from 'lucide-react'
import { useCallback, useEffect, useRef } from 'react'

import { cn } from '@/lib/utils'

interface ComposerProps {
  value: string
  disabled?: boolean
  isStreaming?: boolean
  placeholder?: string
  onChange: (value: string) => void
  onSubmit: () => void
  onStop: () => void
}

/**
 * DeepSeek 风格的输入框：圆角大盒子，Enter 发送 / Shift+Enter 换行，
 * 生成中按钮变成停止。高度随内容自动增长（上限 ~200px）。
 */
export function Composer({
  value,
  disabled,
  isStreaming,
  placeholder,
  onChange,
  onSubmit,
  onStop,
}: ComposerProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const resize = useCallback(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`
  }, [])

  useEffect(() => {
    resize()
  }, [value, resize])

  const canSubmit = !disabled && !isStreaming && value.trim().length > 0

  return (
    <div className='mx-auto w-full max-w-3xl px-4 pb-4'>
      <div
        className={cn(
          'relative flex items-end gap-2 rounded-3xl border border-border/80 bg-background px-4 py-3 shadow-[0_2px_16px_-8px_rgba(0,0,0,0.25)] transition-colors',
          'focus-within:border-[#4d6bfe]/60'
        )}
      >
        <textarea
          ref={textareaRef}
          rows={1}
          value={value}
          disabled={disabled}
          placeholder={placeholder ?? '给 AI 发送消息，Enter 发送，Shift + Enter 换行'}
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && !event.shiftKey) {
              event.preventDefault()
              if (canSubmit) onSubmit()
            }
          }}
          className='max-h-[200px] min-h-7 w-full resize-none bg-transparent py-1 text-sm leading-6 outline-none placeholder:text-muted-foreground/70 disabled:opacity-60'
        />
        <button
          type='button'
          aria-label={isStreaming ? '停止生成' : '发送'}
          disabled={isStreaming ? false : !canSubmit}
          onClick={isStreaming ? onStop : onSubmit}
          className={cn(
            'mb-0.5 flex size-8 shrink-0 items-center justify-center rounded-full transition-colors',
            isStreaming
              ? 'bg-muted text-foreground hover:bg-muted/70'
              : canSubmit
                ? 'bg-[#4d6bfe] text-white hover:bg-[#3f5bea]'
                : 'bg-muted text-muted-foreground'
          )}
        >
          {isStreaming ? (
            <Square className='size-3.5 fill-current' />
          ) : (
            <ArrowUp className='size-4' />
          )}
        </button>
      </div>
      <p className='mt-2 text-center text-[11px] text-muted-foreground'>
        内容由 AI 生成，请自行判断准确性
      </p>
    </div>
  )
}

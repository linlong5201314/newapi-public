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
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'

interface ComposerProps {
  value: string
  disabled?: boolean
  isStreaming?: boolean
  placeholder?: string
  onChange: (value: string) => void
  onSubmit: () => void
  onStop: () => void
}

export function Composer(props: ComposerProps) {
  const { t } = useTranslation()
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const composingRef = useRef(false)

  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.max(48, Math.min(el.scrollHeight, 200))}px`
  }, [props.value])

  const canSubmit =
    !props.disabled && !props.isStreaming && props.value.trim().length > 0

  return (
    <div className='mx-auto w-full max-w-3xl shrink-0 px-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:px-5'>
      <div className='border-border/70 bg-muted/40 focus-within:border-primary/60 rounded-3xl border px-4 pt-2 pb-3 transition-colors'>
        <Textarea
          ref={textareaRef}
          rows={1}
          value={props.value}
          disabled={props.disabled && !props.isStreaming}
          aria-label={t('Message')}
          placeholder={props.placeholder ?? t('Message DeepSeek')}
          onChange={(event) => props.onChange(event.target.value)}
          onCompositionStart={() => {
            composingRef.current = true
          }}
          onCompositionEnd={() => {
            composingRef.current = false
          }}
          onKeyDown={(event) => {
            if (
              event.key !== 'Enter' ||
              event.shiftKey ||
              event.nativeEvent.isComposing ||
              composingRef.current ||
              event.keyCode === 229
            ) {
              return
            }
            event.preventDefault()
            if (canSubmit) props.onSubmit()
          }}
          className='max-h-[200px] min-h-12 resize-none rounded-none border-0 bg-transparent px-0 text-base shadow-none focus-visible:ring-0 sm:text-sm dark:bg-transparent'
        />
        <div className='flex items-center justify-between gap-2'>
          <span className='text-muted-foreground text-xs'>
            {t('Shift + Enter for a new line')}
          </span>
          <Button
            type='button'
            size='icon'
            aria-label={props.isStreaming ? t('Stop generating') : t('Send')}
            disabled={!props.isStreaming && !canSubmit}
            onClick={props.isStreaming ? props.onStop : props.onSubmit}
            className='size-10 rounded-full bg-[#4d6bfe] text-white hover:bg-[#3f5bea] sm:size-9'
          >
            {props.isStreaming ? (
              <Square className='size-3.5 fill-current' aria-hidden='true' />
            ) : (
              <ArrowUp className='size-5' aria-hidden='true' />
            )}
          </Button>
        </div>
      </div>
      <p className='text-muted-foreground mt-2 text-center text-[11px]'>
        {t('AI-generated content may be inaccurate.')}
      </p>
    </div>
  )
}

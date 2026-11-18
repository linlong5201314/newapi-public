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
import { useEffect, useRef, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'

import { IconSendOutline16, IconStopFill16 } from './official-icons'

interface ComposerProps {
  value: string
  controls?: ReactNode
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
    el.style.height = `${Math.min(el.scrollHeight, 336)}px`
  }, [props.value])

  const canSubmit =
    !props.disabled && !props.isStreaming && props.value.trim().length > 0

  return (
    <div className='ds-demo-composer-container'>
      <div className='ds-demo-composer'>
        <Textarea
          ref={textareaRef}
          rows={2}
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
          className='ds-demo-textarea'
        />
        <div className='ds-demo-function-row'>
          <div className='ds-demo-input-controls'>{props.controls}</div>
          <Button
            type='button'
            size='icon'
            aria-label={props.isStreaming ? t('Stop generating') : t('Send')}
            disabled={!props.isStreaming && !canSubmit}
            onClick={props.isStreaming ? props.onStop : props.onSubmit}
            className='ds-demo-send'
          >
            {props.isStreaming ? <IconStopFill16 /> : <IconSendOutline16 />}
          </Button>
        </div>
      </div>
      <p className='ds-demo-caveat'>
        {t('AI-generated content may be inaccurate.')}
      </p>
    </div>
  )
}

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
import { Brain, Globe, ImagePlus, Paperclip, X } from 'lucide-react'
import { useEffect, useRef, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import { ATTACHMENT_ACCEPT } from '../attachments'
import type { DemoAttachment } from '../types'
import { IconSendOutline16, IconStopFill16 } from './official-icons'

interface ComposerProps {
  value: string
  controls?: ReactNode
  disabled?: boolean
  isStreaming?: boolean
  placeholder?: string
  thinkingEnabled?: boolean
  /** Web search is only rendered when a real backend supports it. */
  searchAvailable?: boolean
  /** Image generation chip is only rendered for image-capable models. */
  canGenerateImage?: boolean
  /** Attachments are only enabled for vision-capable models. */
  attachmentsEnabled?: boolean
  attachments?: DemoAttachment[]
  onThinkingChange?: (enabled: boolean) => void
  onChange: (value: string) => void
  onSubmit: () => void
  onStop: () => void
  onGenerateImage?: () => void
  onAttach?: (files: File[]) => void
  onRemoveAttachment?: (id: string) => void
}

export function Composer(props: ComposerProps) {
  const { t } = useTranslation()
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const composingRef = useRef(false)

  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 336)}px`
  }, [props.value])

  const canSubmit =
    !props.disabled && !props.isStreaming && props.value.trim().length > 0
  const attachmentsEnabled = props.attachmentsEnabled ?? false

  const thinkingChip = (
    <Button
      type='button'
      variant='ghost'
      size='sm'
      aria-pressed={props.thinkingEnabled ?? false}
      aria-label={t('Deep thinking')}
      disabled={props.disabled && !props.isStreaming}
      onClick={() => props.onThinkingChange?.(!props.thinkingEnabled)}
      className='ds-demo-chip'
      data-active={props.thinkingEnabled ? 'true' : 'false'}
    >
      <Brain className='size-4' aria-hidden='true' />
      {t('Deep thinking')}
    </Button>
  )

  const searchChip = (
    <Button
      type='button'
      variant='ghost'
      size='sm'
      aria-label={t('Smart search')}
      disabled
      className='ds-demo-chip'
      data-disabled-reason='unavailable'
    >
      <Globe className='size-4' aria-hidden='true' />
      {t('Smart search')}
    </Button>
  )

  const attachTooltip = attachmentsEnabled
    ? t('Attach images or text files')
    : t('The current model cannot read attachments.')

  const attachmentButton = (
    <Button
      type='button'
      variant='ghost'
      size='icon'
      aria-label={t('Attach a file')}
      disabled={!attachmentsEnabled || props.disabled}
      data-disabled-reason={attachmentsEnabled ? undefined : 'model'}
      onClick={() => fileInputRef.current?.click()}
      className='ds-demo-attach'
    >
      <Paperclip className='size-4' aria-hidden='true' />
    </Button>
  )

  const imageChip = (
    <Button
      type='button'
      variant='ghost'
      size='sm'
      aria-label={t('Generate image')}
      disabled={!canSubmit}
      onClick={props.onGenerateImage}
      className='ds-demo-chip'
    >
      <ImagePlus className='size-4' aria-hidden='true' />
      {t('Generate image')}
    </Button>
  )

  return (
    <div className='ds-demo-composer-container'>
      <TooltipProvider>
        <div className='ds-demo-composer'>
          <input
            ref={fileInputRef}
            type='file'
            hidden
            multiple
            accept={ATTACHMENT_ACCEPT}
            aria-hidden='true'
            tabIndex={-1}
            onChange={(event) => {
              const files = event.target.files
              if (files?.length) props.onAttach?.([...files])
              event.target.value = ''
            }}
          />
          {props.attachments?.length ? (
            <ul
              className='ds-demo-attachment-list'
              aria-label={t('Attachments')}
            >
              {props.attachments.map((attachment) => (
                <li key={attachment.id} className='ds-demo-attachment-chip'>
                  <span className='ds-demo-attachment-name'>
                    {attachment.name}
                  </span>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon'
                    aria-label={t('Remove {{name}}', { name: attachment.name })}
                    onClick={() => props.onRemoveAttachment?.(attachment.id)}
                    className='ds-demo-attachment-remove'
                  >
                    <X className='size-3.5' aria-hidden='true' />
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
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
            <div className='ds-demo-input-controls'>
              {props.controls}
              <div className='ds-demo-chip-row'>
                {thinkingChip}
                {props.canGenerateImage ? imageChip : null}
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <span
                        className='ds-demo-chip-anchor'
                        aria-disabled='true'
                      />
                    }
                  >
                    {searchChip}
                  </TooltipTrigger>
                  <TooltipContent>
                    <p>{t('Search is not connected for this channel.')}</p>
                  </TooltipContent>
                </Tooltip>
              </div>
            </div>
            <Tooltip>
              <TooltipTrigger
                render={
                  <span
                    className='ds-demo-attach-anchor'
                    aria-disabled={attachmentsEnabled ? undefined : 'true'}
                  />
                }
              >
                {attachmentButton}
              </TooltipTrigger>
              <TooltipContent>
                <p>{attachTooltip}</p>
              </TooltipContent>
            </Tooltip>
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
      </TooltipProvider>
      <p className='ds-demo-caveat'>
        {t('AI-generated content may be inaccurate.')}
      </p>
    </div>
  )
}

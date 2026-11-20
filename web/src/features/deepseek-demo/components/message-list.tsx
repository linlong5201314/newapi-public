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
import { Download, ExternalLink, Paperclip, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  Conversation,
  ConversationContent,
  ConversationEmptyState,
  ConversationScrollButton,
} from '@/components/ai-elements/conversation'
import {
  Reasoning,
  ReasoningContent,
  ReasoningTrigger,
} from '@/components/ai-elements/reasoning'
import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Markdown } from '@/components/ui/markdown'

import type { DemoImage, DemoMessage } from '../types'

interface MessageListProps {
  messages: DemoMessage[]
  isStreaming: boolean
  onSuggestion: (text: string) => void
  onRegenerate: () => void
}

export function DemoMessageList(props: MessageListProps) {
  const { t } = useTranslation()
  if (props.messages.length === 0) {
    return (
      <ConversationEmptyState className='ds-demo-greeting'>
        <h1>{t('How can I help you today?')}</h1>
      </ConversationEmptyState>
    )
  }

  return (
    <Conversation
      className='ds-demo-messages'
      aria-label={t('Conversation messages')}
    >
      <ConversationContent className='ds-demo-message-column'>
        {props.messages.map((message, index) => (
          <MessageBubble
            key={message.id}
            message={message}
            isLast={index === props.messages.length - 1}
            isStreaming={props.isStreaming}
            onRegenerate={props.onRegenerate}
          />
        ))}
      </ConversationContent>
      <ConversationScrollButton aria-label={t('Scroll to bottom')} />
    </Conversation>
  )
}

function GeneratedImageBlock(props: { image: DemoImage }) {
  const { t } = useTranslation()
  const fileName = `generated-image-${Date.now()}.png`
  return (
    <figure className='ds-demo-generated-image'>
      <img
        src={props.image.url}
        alt={t('Generated image')}
        className='ds-demo-generated-image-img'
      />
      <figcaption className='ds-demo-generated-image-actions'>
        <a
          href={props.image.url}
          target='_blank'
          rel='noopener noreferrer'
          aria-label={t('Open image in new tab')}
          className='ds-demo-generated-image-action'
        >
          <ExternalLink className='size-4' aria-hidden='true' />
          {t('Open')}
        </a>
        <a
          href={props.image.url}
          download={fileName}
          aria-label={t('Download image')}
          className='ds-demo-generated-image-action'
        >
          <Download className='size-4' aria-hidden='true' />
          {t('Download')}
        </a>
      </figcaption>
    </figure>
  )
}

function MessageBubble(props: {
  message: DemoMessage
  isLast: boolean
  isStreaming: boolean
  onRegenerate: () => void
}) {
  const { t } = useTranslation()
  const message = props.message
  if (message.role === 'user') {
    return (
      <div className='ds-demo-user-message'>
        <div className='ds-demo-user-bubble'>
          {message.attachments?.length ? (
            <ul className='ds-demo-message-attachments'>
              {message.attachments.map((attachment) =>
                attachment.kind === 'image' ? (
                  <li key={attachment.id}>
                    <img
                      src={attachment.dataUrl}
                      alt={attachment.name}
                      className='ds-demo-message-attachment-image'
                    />
                  </li>
                ) : (
                  <li key={attachment.id} className='ds-demo-message-attachment'>
                    <Paperclip className='size-3.5' aria-hidden='true' />
                    <span>{attachment.name}</span>
                  </li>
                )
              )}
            </ul>
          ) : null}
          {message.content}
        </div>
      </div>
    )
  }
  const streaming = message.status === 'streaming'
  return (
    <div className='group ds-demo-assistant-message' aria-busy={streaming}>
      <div className='min-w-0 flex-1'>
        {message.reasoning?.trim() && (
          <Reasoning
            defaultOpen={streaming}
            isStreaming={streaming && !message.content}
          >
            <ReasoningTrigger>{t('Deep thinking')}</ReasoningTrigger>
            <ReasoningContent>{message.reasoning}</ReasoningContent>
          </Reasoning>
        )}
        {message.content && (
          <Markdown breaks className='ds-demo-markdown'>
            {message.content}
          </Markdown>
        )}
        {message.image && <GeneratedImageBlock image={message.image} />}
        {streaming && (
          <span
            role='status'
            className='text-muted-foreground inline-flex items-center gap-2 py-2 text-xs'
          >
            <span
              aria-hidden='true'
              className='size-2 animate-pulse rounded-full bg-[#4d6bfe]'
            />
            {t('Generating...')}
          </span>
        )}
        {message.status === 'error' && (
          <p
            role='alert'
            className='bg-destructive/10 text-destructive mt-2 rounded-xl px-3 py-2 text-sm'
          >
            {t(
              message.error ||
                'Request failed. Check your connection and try again.'
            )}
          </p>
        )}
        {message.status === 'stopped' && (
          <p role='status' className='text-muted-foreground mt-2 text-xs'>
            {t('Generation stopped')}
          </p>
        )}
        {!streaming && (
          <div className='ds-demo-response-actions'>
            {message.content && (
              <CopyButton
                value={message.content}
                aria-label={t('Copy response')}
                className='size-9'
              />
            )}
            {message.latencyMs !== undefined && message.status !== 'error' && (
              <span className='text-muted-foreground self-center text-xs'>
                {message.latencyMs < 1000
                  ? t('{{value}}ms', {
                      value: Math.max(1, Math.round(message.latencyMs)),
                    })
                  : t('{{value}}s', {
                      value: (message.latencyMs / 1000).toFixed(2),
                    })}
              </span>
            )}
            {props.isLast && (
              <Button
                variant='ghost'
                size='icon'
                aria-label={t('Regenerate response')}
                disabled={props.isStreaming}
                onClick={props.onRegenerate}
                className='text-muted-foreground size-9'
              >
                <RotateCcw className='size-4' aria-hidden='true' />
              </Button>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

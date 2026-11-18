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
import { RotateCcw } from 'lucide-react'
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

import type { DemoMessage } from '../types'

const SUGGESTIONS = [
  {
    title: 'Introduce yourself',
    prompt: 'Help me write a short introduction for my new colleagues.',
  },
  {
    title: 'Explain code',
    prompt: 'Explain this code and point out potential problems.',
  },
  {
    title: 'Summarize an article',
    prompt: 'Organize the following text into clear key points.',
  },
  {
    title: 'Translate and polish',
    prompt:
      'Translate the following text into natural English and explain your word choices.',
  },
]

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
      <div className='flex min-h-0 flex-1 overflow-y-auto'>
        <ConversationEmptyState className='m-auto h-auto px-5 py-8'>
          <span
            aria-hidden='true'
            className='mb-2 text-3xl font-semibold tracking-tight text-[#4d6bfe]'
          >
            DeepSeek
          </span>
          <h1 className='text-xl font-semibold sm:text-2xl'>
            {t('How can I help you today?')}
          </h1>
          <p className='text-muted-foreground max-w-md text-sm'>
            {t('Use your signed-in account. No API key required.')}
          </p>
          <div className='mt-5 grid w-full max-w-xl grid-cols-1 gap-2 sm:grid-cols-2'>
            {SUGGESTIONS.map((item) => (
              <Button
                key={item.title}
                variant='outline'
                onClick={() => props.onSuggestion(t(item.prompt))}
                className='h-auto min-h-11 justify-start rounded-2xl px-4 py-3 text-left text-sm font-normal whitespace-normal'
              >
                {t(item.title)}
              </Button>
            ))}
          </div>
        </ConversationEmptyState>
      </div>
    )
  }

  return (
    <Conversation aria-label={t('Conversation messages')}>
      <ConversationContent className='mx-auto flex w-full max-w-3xl flex-col gap-7 px-4 py-6 sm:px-5'>
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
      <div className='flex justify-end'>
        <div className='bg-muted max-w-[90%] rounded-2xl px-4 py-3 text-sm leading-relaxed break-words whitespace-pre-wrap sm:max-w-[85%]'>
          {message.content}
        </div>
      </div>
    )
  }
  const streaming = message.status === 'streaming'
  return (
    <div className='group flex min-w-0 gap-3' aria-busy={streaming}>
      <span
        aria-hidden='true'
        className='mt-1 flex size-7 shrink-0 items-center justify-center rounded-full bg-[#4d6bfe]/10 text-xs font-semibold text-[#4d6bfe]'
      >
        D
      </span>
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
          <Markdown breaks className='text-sm leading-7 break-words'>
            {message.content}
          </Markdown>
        )}
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
          <div className='mt-2 flex items-center gap-1 sm:opacity-0 sm:group-focus-within:opacity-100 sm:group-hover:opacity-100'>
            {message.content && (
              <CopyButton
                value={message.content}
                aria-label={t('Copy response')}
                className='size-9'
              />
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

import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { DemoMessageList } from '../components/message-list'
import type { DemoMessage } from '../types'

const reply: DemoMessage = {
  id: 'reply',
  role: 'assistant',
  content: '',
  status: 'error',
  error: 'Connection failed',
  createdAt: 1,
}

describe('demo response actions', () => {
  it('offers a real retry for an empty failed response instead of hiding all actions', () => {
    const onRegenerate = vi.fn()
    render(
      <DemoMessageList
        messages={[reply]}
        isStreaming={false}
        onSuggestion={vi.fn()}
        onRegenerate={onRegenerate}
      />
    )
    expect(screen.getByRole('alert')).toHaveTextContent('Connection failed')
    fireEvent.click(screen.getByRole('button', { name: 'Regenerate response' }))
    expect(onRegenerate).toHaveBeenCalledOnce()
  })

  it('exposes stopped generation status and does not show inactive feedback controls', () => {
    render(
      <DemoMessageList
        messages={[{ ...reply, content: 'Partial answer', status: 'stopped' }]}
        isStreaming={false}
        onSuggestion={vi.fn()}
        onRegenerate={vi.fn()}
      />
    )
    expect(screen.getByRole('status')).toHaveTextContent('Generation stopped')
    expect(screen.getByRole('button', { name: 'Copy response' })).toBeEnabled()
    expect(
      screen.queryByRole('button', { name: 'Helpful' })
    ).not.toBeInTheDocument()
  })

  it('keeps reasoning collapsed for a completed reply and exposes expanded state when opened', () => {
    render(
      <DemoMessageList
        messages={[
          {
            ...reply,
            content: 'Answer',
            reasoning: 'Reasoning detail',
            status: 'complete',
          },
        ]}
        isStreaming={false}
        onSuggestion={vi.fn()}
        onRegenerate={vi.fn()}
      />
    )
    const reasoning = screen.getByRole('button', { name: 'Deep thinking' })
    expect(reasoning).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(reasoning)
    expect(reasoning).toHaveAttribute('aria-expanded', 'true')
  })

  it('shows only the official greeting for a new chat, without invented suggestion cards', () => {
    render(
      <DemoMessageList
        messages={[]}
        isStreaming={false}
        onSuggestion={vi.fn()}
        onRegenerate={vi.fn()}
      />
    )
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      'How can I help you today?'
    )
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.queryByText('DeepSeek')).not.toBeInTheDocument()
  })
})

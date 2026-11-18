import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { Composer } from '../components/composer'

describe('demo composer', () => {
  it('does not send Enter while choosing an IME candidate', () => {
    const onSubmit = vi.fn()
    render(
      <Composer
        value='你好'
        onChange={vi.fn()}
        onSubmit={onSubmit}
        onStop={vi.fn()}
      />
    )
    fireEvent.keyDown(screen.getByRole('textbox'), {
      key: 'Enter',
      isComposing: true,
      keyCode: 229,
    })
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('sends plain Enter but preserves Shift+Enter for a newline', () => {
    const onSubmit = vi.fn()
    render(
      <Composer
        value='Hello'
        onChange={vi.fn()}
        onSubmit={onSubmit}
        onStop={vi.fn()}
      />
    )
    const textbox = screen.getByRole('textbox')
    fireEvent.keyDown(textbox, { key: 'Enter', shiftKey: true })
    expect(onSubmit).not.toHaveBeenCalled()
    fireEvent.keyDown(textbox, { key: 'Enter' })
    expect(onSubmit).toHaveBeenCalledOnce()
  })

  it('keeps Stop available while sending and prevents a second submit', () => {
    const onSubmit = vi.fn()
    const onStop = vi.fn()
    render(
      <Composer
        value='next draft'
        disabled
        isStreaming
        onChange={vi.fn()}
        onSubmit={onSubmit}
        onStop={onStop}
      />
    )
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
    fireEvent.click(screen.getByRole('button', { name: 'Stop generating' }))
    expect(onSubmit).not.toHaveBeenCalled()
    expect(onStop).toHaveBeenCalledOnce()
  })

  it('disables Send for whitespace and labels the message input', () => {
    render(
      <Composer
        value='  '
        onChange={vi.fn()}
        onSubmit={vi.fn()}
        onStop={vi.fn()}
      />
    )
    expect(screen.getByRole('textbox', { name: 'Message' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled()
  })
})

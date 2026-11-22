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

  it('toggles the Deep thinking chip as real pressed state', () => {
    const onThinkingChange = vi.fn()
    render(
      <Composer
        value='draft'
        thinkingEnabled={false}
        onThinkingChange={onThinkingChange}
        onChange={vi.fn()}
        onSubmit={vi.fn()}
        onStop={vi.fn()}
      />
    )
    const chip = screen.getByRole('button', { name: 'Deep thinking' })
    expect(chip).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(chip)
    expect(onThinkingChange).toHaveBeenCalledWith(true)
  })

  it('keeps Smart search disabled with an explicit unavailability marker', () => {
    render(
      <Composer
        value='draft'
        onChange={vi.fn()}
        onSubmit={vi.fn()}
        onStop={vi.fn()}
      />
    )
    const chip = screen.getByRole('button', { name: 'Smart search' })
    expect(chip).toBeDisabled()
    expect(chip).toHaveAttribute('data-disabled-reason', 'unavailable')
  })

  it('disables the attachment control for text-only models with an explicit marker', () => {
    render(
      <Composer
        value='draft'
        onChange={vi.fn()}
        onSubmit={vi.fn()}
        onStop={vi.fn()}
      />
    )
    const attach = screen.getByRole('button', { name: 'Attach a file' })
    expect(attach).toBeDisabled()
    expect(attach).toHaveAttribute('data-disabled-reason', 'model')
  })

  it('opens the file picker for vision-capable models', () => {
    render(
      <Composer
        value='draft'
        attachmentsEnabled
        onChange={vi.fn()}
        onSubmit={vi.fn()}
        onStop={vi.fn()}
        onAttach={vi.fn()}
      />
    )
    const attach = screen.getByRole('button', { name: 'Attach a file' })
    expect(attach).toBeEnabled()
    const input = document.querySelector<HTMLInputElement>('input[type="file"]')
    expect(input).not.toBeNull()
    expect(input?.getAttribute('accept')).toContain('image/*')
    fireEvent.click(attach)
  })

  it('disables the attachment button with a model-capability reason for text models', () => {
    render(
      <Composer
        value='draft'
        attachmentsEnabled={false}
        onChange={vi.fn()}
        onSubmit={vi.fn()}
        onStop={vi.fn()}
      />
    )
    const attach = screen.getByRole('button', { name: 'Attach a file' })
    expect(attach).toBeDisabled()
    expect(attach).toHaveAttribute('data-disabled-reason', 'model')
  })

  it('shows a real Generate image action only for image models', () => {
    const { rerender } = render(
      <Composer
        value='a cat'
        canGenerateImage
        onChange={vi.fn()}
        onSubmit={vi.fn()}
        onStop={vi.fn()}
        onGenerateImage={vi.fn()}
      />
    )
    expect(
      screen.getByRole('button', { name: 'Generate image' })
    ).toBeInTheDocument()
    rerender(
      <Composer
        value='a cat'
        onChange={vi.fn()}
        onSubmit={vi.fn()}
        onStop={vi.fn()}
      />
    )
    expect(
      screen.queryByRole('button', { name: 'Generate image' })
    ).not.toBeInTheDocument()
  })

  it('calls onGenerateImage with the current draft instead of chat submit', () => {
    const onGenerateImage = vi.fn()
    const onSubmit = vi.fn()
    render(
      <Composer
        value='a cat'
        canGenerateImage
        onGenerateImage={onGenerateImage}
        onChange={vi.fn()}
        onSubmit={onSubmit}
        onStop={vi.fn()}
      />
    )
    fireEvent.click(screen.getByRole('button', { name: 'Generate image' }))
    expect(onGenerateImage).toHaveBeenCalledOnce()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('renders attachment chips with a remove control', () => {
    const onRemoveAttachment = vi.fn()
    render(
      <Composer
        value='draft'
        attachmentsEnabled
        attachments={[
          {
            id: 'a1',
            name: 'notes.md',
            kind: 'text',
            dataUrl: 'data:text/plain;base64,eA==',
            textContent: 'x',
          },
        ]}
        onRemoveAttachment={onRemoveAttachment}
        onChange={vi.fn()}
        onSubmit={vi.fn()}
        onStop={vi.fn()}
        onAttach={vi.fn()}
      />
    )
    expect(screen.getByText('notes.md')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Remove notes.md' }))
    expect(onRemoveAttachment).toHaveBeenCalledWith('a1')
  })
})

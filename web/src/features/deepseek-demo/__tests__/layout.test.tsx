import { readFileSync } from 'node:fs'

import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { Composer } from '../components/composer'
import { DemoMessageList } from '../components/message-list'

const styles = readFileSync(
  'src/features/deepseek-demo/deepseek-demo.css',
  'utf8'
)

let sheet: HTMLStyleElement
beforeEach(() => {
  sheet = document.createElement('style')
  sheet.textContent = styles
  document.head.append(sheet)
})
afterEach(() => sheet.remove())

describe('official chat layout contract', () => {
  it('keeps the desktop sidebar at 310px with a 40px pill new-chat control', () => {
    render(
      <div className='ds-demo'>
        <aside className='ds-demo-sidebar'>
          <button type='button' className='ds-demo-new-chat'>
            New chat
          </button>
        </aside>
      </div>
    )
    expect(screen.getByRole('complementary')).toHaveStyle({
      width: '310px',
    })
    expect(screen.getByRole('button', { name: 'New chat' })).toHaveStyle({
      height: '40px',
      'border-radius': '100px',
    })
  })

  it('caps the composer container at 910px and keeps the whale brand row styles scoped', () => {
    render(
      <div className='ds-demo'>
        <div className='ds-demo-composer-container' data-testid='composer' />
        <span className='ds-demo-brand' data-testid='brand'>
          <span className='ds-demo-app-name'>DeepSeek · NewAPI</span>
        </span>
      </div>
    )
    expect(screen.getByTestId('composer')).toHaveStyle({
      'max-width': 'min(910px, calc(100% - 32px))',
    })
    expect(screen.getByTestId('brand')).toHaveStyle({ display: 'flex' })
    expect(styles).toContain('--message-list-max-width: 910px')
    expect(styles).toContain('.ds-demo .ds-demo-sidebar-search-input')
    expect(styles).toContain(".ds-demo .ds-demo-chip[data-active='true']")
  })

  it('keeps the official two-line input, 24px wrapper radius, and 34px send circle', () => {
    render(
      <div className='ds-demo'>
        <Composer
          value='Hello'
          onChange={vi.fn()}
          onSubmit={vi.fn()}
          onStop={vi.fn()}
        />
      </div>
    )
    expect(screen.getByRole('textbox', { name: 'Message' })).toHaveStyle({
      'min-height': '84px',
      'line-height': '24px',
    })
    expect(screen.getByRole('button', { name: 'Send' })).toHaveStyle({
      width: '34px',
      height: '34px',
      'border-radius': '50%',
    })
    expect(screen.getByRole('textbox').parentElement).toHaveStyle({
      'border-radius': '24px',
    })
  })

  it('shows the centered official greeting with no extra suggestion cards or brand avatar', () => {
    render(
      <div className='ds-demo' data-empty='true'>
        <div className='ds-demo-content'>
          <DemoMessageList
            messages={[]}
            isStreaming={false}
            onSuggestion={vi.fn()}
            onRegenerate={vi.fn()}
          />
        </div>
      </div>
    )
    const heading = screen.getByRole('heading', {
      name: 'How can I help you today?',
    })
    expect(heading).toHaveStyle({
      'font-size': '24px',
      'line-height': '32px',
      'text-align': 'center',
    })
    expect(heading.parentElement).toHaveStyle({ height: 'auto' })
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.queryByText('D')).not.toBeInTheDocument()
  })

  it('keeps assistant responses full width without a D avatar or unsupported feedback buttons', () => {
    render(
      <div className='ds-demo'>
        <DemoMessageList
          messages={[
            {
              id: 'answer',
              role: 'assistant',
              content: 'A useful answer',
              status: 'complete',
              createdAt: 1,
            },
          ]}
          isStreaming={false}
          onSuggestion={vi.fn()}
          onRegenerate={vi.fn()}
        />
      </div>
    )
    expect(screen.getByText('A useful answer')).toBeVisible()
    expect(screen.queryByText('D')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Helpful' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Regenerate response' })
    ).toBeEnabled()
  })

  it('bounds mobile drawer and greeting sizes without viewport-fixed overlays outside the feature', () => {
    render(
      <div
        role='dialog'
        aria-label='Chat history'
        className='ds-demo ds-demo-mobile-sheet'
      />
    )
    expect(screen.getByRole('dialog', { name: 'Chat history' })).toHaveStyle({
      position: 'fixed',
      'flex-direction': 'column',
    })
    const mediaRules = [...(sheet.sheet?.cssRules ?? [])].filter(
      (rule): rule is CSSMediaRule => rule instanceof CSSMediaRule
    )
    const mobile = mediaRules.find((rule) =>
      rule.conditionText.includes('767px')
    )
    const small = mediaRules.find((rule) =>
      rule.conditionText.includes('439px')
    )
    expect(mobile?.cssText).toContain('max-width: calc(100vw - 48px)')
    expect(styles.slice(styles.indexOf('@media (max-width: 767px)'))).toContain(
      'env(safe-area-inset-bottom)'
    )
    expect(small?.cssText).toContain('font-size: 20px')
    expect(styles).not.toContain('body {')
    expect(styles).not.toContain(':root {')
  })
})

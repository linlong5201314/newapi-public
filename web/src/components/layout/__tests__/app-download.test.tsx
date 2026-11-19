import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { AppDownload } from '../components/app-download'

describe('App download action', () => {
  it('exposes an accessible same-origin APK download without tokens or external navigation', () => {
    render(<AppDownload />)
    const link = screen.getByRole('link', { name: 'Download App' })
    expect(link).toHaveAttribute('href', '/deepseek-newapi.apk')
    expect(link).toHaveAttribute('download', 'deepseek-newapi.apk')
    expect(link).not.toHaveAttribute('target')
    expect(link).toBeVisible()
    expect(link).toHaveTextContent('App')
  })

  it('keeps the action present for mobile and desktop without a breakpoint-hidden container', () => {
    render(<AppDownload />)
    const link = screen.getByRole('link', { name: 'Download App' })
    expect(
      link.className
        .split(/\s+/)
        .some((name) => /^(?:[a-z]+:)?hidden$/.test(name))
    ).toBe(false)
    expect(link).toHaveAttribute('title', 'Download App')
  })
})

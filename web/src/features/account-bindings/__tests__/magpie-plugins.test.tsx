import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { MagpiePluginsCard } from '../magpie-plugins'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
vi.mock('@/lib/api', () => ({ api: { get: vi.fn() } }))

const plugin = {
  id: 'marvis',
  name: 'Marvis',
  repo: 'MYCF711/magpie-marvis',
  desc: 'Provider',
  authMode: 'account',
  stars: 20,
  url: 'https://github.com/MYCF711/magpie-marvis',
  install: 'magpie plugin add github:MYCF711/magpie-marvis',
  importSource: 'https://github.com/MYCF711/magpie-marvis',
}

function renderCard() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  render(
    <QueryClientProvider client={client}>
      <MagpiePluginsCard />
    </QueryClientProvider>
  )
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('Magpie plugin operations', () => {
  it('copies a real import source and explains that copying does not install a provider', async () => {
    const user = userEvent.setup()
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: { items: [plugin], total: 1 } },
    })
    renderCard()
    await user.click(
      await screen.findByRole('button', { name: 'Copy import source' })
    )
    expect(await navigator.clipboard.readText()).toBe(plugin.importSource)
    expect(
      screen.getByText(
        'Copying does not install or authorize a plugin. Run commands on the machine hosting your own Magpie gateway, not in this website.'
      )
    ).toBeVisible()
    expect(
      screen.getByRole('link', { name: 'Open plugin documentation' })
    ).toHaveAttribute('href', plugin.url)
  })

  it('copies the complete executable CLI command, separately from the import source', async () => {
    const user = userEvent.setup()
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: { items: [plugin], total: 1 } },
    })
    renderCard()
    await user.click(
      await screen.findByRole('button', { name: 'Copy CLI command' })
    )
    expect(await navigator.clipboard.readText()).toBe(plugin.install)
  })

  it('uses the shared fallback when the browser clipboard is denied', async () => {
    const user = userEvent.setup()
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: { items: [plugin], total: 1 } },
    })
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(
      new Error('denied')
    )
    let selected = ''
    const previous = Object.getOwnPropertyDescriptor(document, 'execCommand')
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: () => {
        selected = (document.activeElement as HTMLTextAreaElement).value
        return true
      },
    })
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    try {
      renderCard()
      await user.click(
        await screen.findByRole('button', { name: 'Copy CLI command' })
      )
      expect(selected).toBe(plugin.install)
      expect(
        await screen.findByRole('button', { name: 'Copied' })
      ).toBeVisible()
    } finally {
      if (previous) Object.defineProperty(document, 'execCommand', previous)
      else Reflect.deleteProperty(document, 'execCommand')
    }
  })

  it('does not offer copy or install actions for an unresolved source', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [{ ...plugin, url: '', importSource: '', install: '' }],
          total: 1,
        },
      },
    })
    renderCard()
    expect(
      await screen.findByText(
        'Installation source is unverified. No install command is offered.'
      )
    ).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Copy CLI command' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Copy import source' })
    ).not.toBeInTheDocument()
  })

  it('shows a failed API response as an error and allows retry instead of reporting no plugins', async () => {
    const user = userEvent.setup()
    vi.mocked(api.get)
      .mockResolvedValueOnce({
        data: { success: false, message: 'Catalog unavailable' },
      })
      .mockResolvedValueOnce({
        data: { success: true, data: { items: [plugin], total: 1 } },
      })
    renderCard()
    expect(
      await screen.findByText('Could not load Magpie plugins')
    ).toBeVisible()
    expect(screen.queryByText('No matching plugins')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Marvis')).toBeVisible()
  })
})

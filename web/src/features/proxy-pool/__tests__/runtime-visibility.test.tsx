import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { ProxyPoolPage } from '../index'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

afterEach(() => {
  useAuthStore.getState().auth.setUser(null)
  vi.resetAllMocks()
})

function renderPool(role: number) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'pool-user', role })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (String(url).endsWith('/runtime')) {
      return {
        data: {
          success: true,
          data: { running: true, nodes: [], proxies: 2, port: 23456 },
        },
      }
    }
    if (String(url).endsWith('/summary')) {
      return {
        data: {
          success: true,
          data: {
            endpoint_total: 0,
            endpoint_enabled: 0,
            usable_url_count: 0,
            subscription_total: 0,
            client_node_count: 0,
          },
        },
      }
    }
    return { data: { success: true, data: [] } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ProxyPoolPage />
    </QueryClientProvider>
  )
}

describe('shared kernel visibility', () => {
  it.each([1, 10])(
    'does not expose or request kernel controls for role %s',
    async (role) => {
      renderPool(role)
      await screen.findByText('No egress nodes yet')
      expect(
        screen.queryByRole('button', { name: 'Reload kernel configuration' })
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: '重载内核配置' })
      ).not.toBeInTheDocument()
      expect(
        vi
          .mocked(api.get)
          .mock.calls.some(([url]) => String(url).endsWith('/runtime'))
      ).toBe(false)
      expect(screen.queryByText('Export mihomo config')).not.toBeInTheDocument()
    }
  )

  it('shows and requests kernel controls for the super administrator', async () => {
    renderPool(100)
    await waitFor(() =>
      expect(
        vi
          .mocked(api.get)
          .mock.calls.some(([url]) => String(url).endsWith('/runtime'))
      ).toBe(true)
    )
    expect(
      screen.getByRole('button', {
        name: /Reload kernel configuration|重载内核配置/,
      })
    ).toBeInTheDocument()
  })
})

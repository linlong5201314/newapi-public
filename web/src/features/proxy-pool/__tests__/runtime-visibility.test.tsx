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

function renderPool(role: number, inventory = false) {
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
            imported_total: inventory ? 7114 : 0,
            enabled: inventory ? 7000 : 0,
            loaded_this_process: inventory ? 300 : 0,
            probed_healthy: inventory ? 12 : 0,
            unknown: inventory ? 6980 : 0,
            failed: inventory ? 8 : 0,
            inventory_remaining: inventory ? 6814 : 0,
            list_limit: 600,
            runtime_preload_limit: 300,
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
  it('keeps database inventory, enabled nodes and process-local load distinct', async () => {
    renderPool(1, true)
    await screen.findByText('7,114')
    expect(
      screen.getByText('Imported inventory (database, visible to you)')
    ).toBeInTheDocument()
    expect(
      screen.getByText('Loaded in this process (visible to you)')
    ).toBeInTheDocument()
    expect(screen.getByText('300')).toBeInTheDocument()
    expect(
      screen.getByText('Probed healthy (database, enabled nodes)')
    ).toBeInTheDocument()
    expect(screen.getByText('12')).toBeInTheDocument()
    expect(
      screen.getByText(
        'Enabled: 7,000. Enabled does not mean verified healthy.'
      )
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        'Not summed across instances. Unloaded inventory: 6,814.'
      )
    ).toBeInTheDocument()
    expect(screen.queryByText('可见可用节点总数')).not.toBeInTheDocument()
  })

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

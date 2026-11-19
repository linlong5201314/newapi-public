import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { ProxyPoolPage } from '../index'
import type { ProxyEndpoint } from '../types'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

const clients: QueryClient[] = []

afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
  useAuthStore.getState().auth.setUser(null)
  vi.resetAllMocks()
})

function endpoint(id: number): ProxyEndpoint {
  return {
    id,
    name: `Inventory node ${id}`,
    kind: 'vless',
    region: 'HK',
    subscription_id: 0,
    enabled: true,
    public: true,
    is_owner: false,
    weight: 1,
    fail_count: 0,
    last_error: '',
    dialable: true,
    created_at: '',
    updated_at: '',
  }
}

function page(data: ProxyEndpoint[], offset: number, hasMore: boolean) {
  return {
    data: {
      success: true,
      data,
      pagination: {
        offset,
        limit: 600,
        returned: data.length,
        has_more: hasMore,
      },
    },
  }
}

function renderInventory(nextPage: (offset: number) => unknown) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'pool-user', role: 1 })
  vi.mocked(api.get).mockImplementation(async (url, config) => {
    if (String(url).endsWith('/endpoints')) {
      const params = config?.params as { offset?: number } | undefined
      return await nextPage(params?.offset ?? 0)
    }
    if (String(url).endsWith('/summary')) {
      return {
        data: {
          success: true,
          data: {
            imported_total: 7114,
            endpoint_total: 7114,
            endpoint_enabled: 7114,
            loaded_this_process: 300,
            probed_healthy: null,
            unknown: null,
            failed: null,
            runtime_preload_limit: 300,
            runtime_listener_limit: 1500,
            on_demand_loading: true,
            usable_url_count: 0,
            subscription_total: 0,
            client_node_count: 7114,
          },
        },
      }
    }
    return { data: { success: true, data: [] } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>
      <ProxyPoolPage />
    </QueryClientProvider>
  )
}

const firstPage = Array.from({ length: 600 }, (_, i) => endpoint(i + 1))

describe('incremental proxy inventory', () => {
  it('fetches only the initial 600 records and labels search as fetched-only', async () => {
    const requests = vi.fn((offset: number) => page(firstPage, offset, true))
    renderInventory(requests)

    await screen.findByText('Fetched 600 of 7,114 visible inventory records.')
    expect(requests).toHaveBeenCalledTimes(1)
    expect(requests).toHaveBeenCalledWith(0)
    expect(screen.getByLabelText('Search fetched nodes')).toBeInTheDocument()
    expect(
      screen.getByText(
        'Search and visibility filters apply only to fetched records. Load more inventory to include the rest.'
      )
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Load next inventory page' })
    ).toBeEnabled()
    expect(screen.getAllByRole('row')).toHaveLength(101)
    expect(screen.getByText(/Preload limit: 300/)).toHaveTextContent(
      'up to 1,500 listeners per process'
    )
    expect(screen.getByText(/Preload limit: 300/)).toHaveTextContent(
      'Active listeners are not evicted'
    )
    fireEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
    await screen.findByText('Inventory node 101')
    expect(screen.queryByText('Inventory node 1')).not.toBeInTheDocument()
    expect(screen.getAllByRole('row')).toHaveLength(101)
    expect(requests).toHaveBeenCalledTimes(1)
  })

  it('loads the next server page only on request and finds nodes beyond 600', async () => {
    const requests = vi.fn((offset: number) =>
      page(
        offset === 0
          ? firstPage
          : Array.from({ length: 600 }, (_, i) => endpoint(i + 601)),
        offset,
        true
      )
    )
    renderInventory(requests)
    await screen.findByText('Fetched 600 of 7,114 visible inventory records.')
    const search = screen.getByLabelText('Search fetched nodes')
    fireEvent.change(search, { target: { value: 'Inventory node 711' } })
    expect(screen.getByText('No matching fetched nodes')).toBeInTheDocument()

    const user = userEvent.setup()
    screen.getByRole('button', { name: 'Load next inventory page' }).focus()
    await user.keyboard('{Enter}')

    await screen.findByText('Fetched 1,200 of 7,114 visible inventory records.')
    expect(screen.getByText('Inventory node 711')).toBeInTheDocument()
    expect(requests.mock.calls.map(([offset]) => offset)).toEqual([0, 600])
    expect(search).toHaveValue('Inventory node 711')
  })

  it('deduplicates endpoint IDs across pages and stops at has_more false', async () => {
    renderInventory((offset) =>
      page(
        offset === 0 ? firstPage : [endpoint(600), endpoint(601)],
        offset,
        offset === 0
      )
    )
    await screen.findByText('Fetched 600 of 7,114 visible inventory records.')
    fireEvent.click(
      screen.getByRole('button', { name: 'Load next inventory page' })
    )
    await screen.findByText('Fetched 601 of 7,114 visible inventory records.')
    fireEvent.change(screen.getByLabelText('Search fetched nodes'), {
      target: { value: 'Inventory node 600' },
    })
    expect(screen.getAllByText('Inventory node 600')).toHaveLength(1)
    expect(
      screen.queryByRole('button', { name: 'Load next inventory page' })
    ).not.toBeInTheDocument()
  })

  it('keeps the fetched list visible while the next page loads', async () => {
    let finishPage: ((value: ReturnType<typeof page>) => void) | undefined
    const nextPage = new Promise<ReturnType<typeof page>>((resolve) => {
      finishPage = resolve
    })
    renderInventory((offset) =>
      offset === 0 ? page(firstPage, 0, true) : nextPage
    )
    await screen.findByText('Inventory node 1')
    fireEvent.click(
      screen.getByRole('button', { name: 'Load next inventory page' })
    )
    await screen.findByRole('button', { name: 'Loading inventory...' })
    expect(
      screen.getByRole('button', { name: 'Loading inventory...' })
    ).toBeDisabled()
    expect(screen.getByText('Inventory node 1')).toBeInTheDocument()
    finishPage?.(page([endpoint(601)], 600, false))
    await screen.findByText('Fetched 601 of 7,114 visible inventory records.')
  })

  it('preserves previous pages after next-page failure and retries the same offset', async () => {
    let failed = false
    const requests = vi.fn((offset: number) => {
      if (offset === 0) return page(firstPage, 0, true)
      if (!failed) {
        failed = true
        throw new Error('Page temporarily unavailable')
      }
      return page([endpoint(601)], 600, false)
    })
    renderInventory(requests)
    await screen.findByText('Inventory node 1')
    fireEvent.click(
      screen.getByRole('button', { name: 'Load next inventory page' })
    )
    await screen.findByText(
      'Could not load more inventory. Fetched nodes are still available.'
    )
    expect(screen.getByText('Inventory node 1')).toBeInTheDocument()
    expect(
      screen.getByText('Fetched 600 of 7,114 visible inventory records.')
    ).toBeInTheDocument()
    fireEvent.click(
      screen.getByRole('button', { name: 'Retry loading inventory' })
    )
    await screen.findByText('Fetched 601 of 7,114 visible inventory records.')
    expect(requests.mock.calls.map(([offset]) => offset)).toEqual([0, 600, 600])
  })

  it('keeps missing healthy metrics unknown rather than equating inventory with healthy nodes', async () => {
    renderInventory(() => page([], 0, false))
    await screen.findByText('Fetched 0 of 7,114 visible inventory records.')
    const healthyLabel = screen.getByText(
      'Probed healthy (database, enabled nodes)'
    )
    expect(
      healthyLabel.closest('[data-slot="card-content"]')
    ).toHaveTextContent(
      'Probed healthy (database, enabled nodes)-Unknown: - · Failed: -'
    )
    expect(screen.getByText('Unknown: - · Failed: -')).toBeInTheDocument()
    await waitFor(() => expect(api.get).toHaveBeenCalled())
  })
})

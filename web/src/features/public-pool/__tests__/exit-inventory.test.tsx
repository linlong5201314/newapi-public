import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { PublicExitPool } from '../components/public-exit-pool'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

const clients: QueryClient[] = []

afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
  vi.resetAllMocks()
})

async function renderPreview(publicNodes: boolean) {
  const data = Array.from({ length: 600 }, (_, index) => ({
    id: index + 1,
    name: `Preview node ${index + 1}`,
    kind: 'vless',
    region: 'HK',
    public: publicNodes,
    latency_ms: null,
  }))
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data,
      pagination: {
        offset: 0,
        limit: 600,
        returned: 600,
        has_more: true,
      },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const router = createRouter({
    routeTree: createRootRoute({ component: PublicExitPool }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

describe('public exit inventory preview', () => {
  it('labels ninety public cards as a preview of fetched records and links to incremental full inventory', async () => {
    await renderPreview(true)
    await screen.findByText(
      'Previewing 90 public nodes from 600 fetched inventory records. This is not the complete pool or a health count.'
    )
    expect(screen.getByText('Preview node 90')).toBeVisible()
    expect(screen.queryByText('Preview node 91')).not.toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: 'Browse all proxy inventory' })
    ).toHaveAttribute('href', '/proxy-pool')
    expect(
      screen.getByText(
        'More inventory is available. Open proxy management to load additional pages.'
      )
    ).toBeVisible()
    expect(api.get).toHaveBeenCalledTimes(1)
    expect(api.get).toHaveBeenCalledWith('/api/proxy-pool/endpoints', {
      params: { offset: 0, limit: 600 },
    })
    expect(
      screen.queryByText('600 published proxy exits')
    ).not.toBeInTheDocument()
  })

  it('does not declare the whole public pool empty when the first page only contains private records', async () => {
    await renderPreview(false)
    await screen.findByText('No public nodes in the fetched preview.')
    expect(
      screen.queryByText('No public proxy exits are published.')
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: 'Browse all proxy inventory' })
    ).toHaveAttribute('href', '/proxy-pool')
    expect(
      screen.getByText(
        'More inventory is available. Open proxy management to load additional pages.'
      )
    ).toBeVisible()
  })
})

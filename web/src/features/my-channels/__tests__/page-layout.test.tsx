import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { MyChannels } from '../index'
import { channelSchema } from '../types'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
}))
let client: QueryClient
beforeEach(() => {
  window.localStorage.clear()
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/channel/self/') {
      return { data: { success: true, data: { items: [], total: 0 } } }
    }
    if (url === '/api/channel/self/free_usage') {
      return { data: { success: true, data: [] } }
    }
    if (String(url).includes('/magpie/plugins')) {
      return { data: { success: true, data: { items: [], total: 0 } } }
    }
    return { data: { success: true, data: [] } }
  })
})
afterEach(() => {
  client.clear()
  window.localStorage.clear()
})

async function renderPage() {
  const root = createRootRoute({ component: Outlet })
  const authenticated = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
    component: Outlet,
  })
  const page = createRoute({
    getParentRoute: () => authenticated,
    path: 'my-channels/',
    component: MyChannels,
  })
  const router = createRouter({
    routeTree: root.addChildren([authenticated.addChildren([page])]),
    history: createMemoryHistory({ initialEntries: ['/my-channels/'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

it('shows a newly connected Xiaoyi Work channel with its real type instead of Unknown', async () => {
  const channel = channelSchema.parse({
    id: 103,
    type: 76,
    key: '',
    name: '华为小艺 Work',
    status: 1,
    models: 'DeepSeek_V4.1_Flash_VE',
    group: 'default',
    created_time: 1,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
  })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/channel/self/') {
      return { data: { success: true, data: { items: [channel], total: 1 } } }
    }
    if (url === '/api/channel/self/free_usage') {
      return { data: { success: true, data: [] } }
    }
    return { data: { success: true, data: [] } }
  })
  await renderPage()
  expect(await screen.findByRole('button', { name: '华为小艺 Work' })).toBeVisible()
  expect(screen.getAllByText('华为小艺 Work').length).toBeGreaterThan(1)
  expect(screen.queryByText('Unknown')).not.toBeInTheDocument()
  expect(screen.getByText('1 models')).toBeVisible()
})

it('shows the primary channel list before quota and keeps optional Magpie collapsed below it', async () => {
  await renderPage()
  const table = await screen.findByRole('table')
  const quota = await screen.findByText(
    'Add a channel from a free source and its monthly quota shows up here.'
  )
  expect(
    table.compareDocumentPosition(quota) & Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy()
  const toggle = screen.getByRole('button', {
    name: 'Magpie plugins (optional)',
  })
  expect(
    quota.compareDocumentPosition(toggle) & Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy()
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(
    screen.queryByRole('textbox', { name: 'Search Magpie plugins' })
  ).not.toBeInTheDocument()
  const user = userEvent.setup()
  toggle.focus()
  await user.keyboard('{Enter}')
  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  expect(
    await screen.findByRole('textbox', { name: 'Search Magpie plugins' })
  ).toBeVisible()
  toggle.focus()
  await user.keyboard(' ')
  await waitFor(() => expect(toggle).toHaveAttribute('aria-expanded', 'false'))
  expect(toggle).toHaveFocus()
  expect(screen.getByRole('table')).toBeVisible()
})

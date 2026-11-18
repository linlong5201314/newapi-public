/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
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
import { Route as NodesRoute } from '@/routes/_authenticated/nodes'
import { useAuthStore } from '@/stores/auth-store'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
}))

let client: QueryClient

beforeEach(() => {
  window.localStorage.clear()
  useAuthStore
    .getState()
    .auth.setUser({ id: 2, username: 'local-root', role: 100 })
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/node') return { data: { success: true, data: [] } }
    throw new Error(`Unexpected GET ${url}`)
  })
})

afterEach(() => {
  client.clear()
  window.localStorage.clear()
  useAuthStore.getState().auth.reset()
})

async function renderPage(path = '/nodes') {
  const root = createRootRoute({ component: Outlet })
  const authenticated = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
    component: Outlet,
  })
  const nodes = createRoute({
    getParentRoute: () => authenticated,
    path: 'nodes/',
    // The production guard reads the auth store and takes no route context.
    beforeLoad: NodesRoute.options.beforeLoad as () => void,
    validateSearch: NodesRoute.options.validateSearch,
    component: NodesRoute.options.component,
  })
  const forbidden = createRoute({
    getParentRoute: () => root,
    path: '/403',
    component: () => <h1>Forbidden</h1>,
  })
  const router = createRouter({
    routeTree: root.addChildren([
      authenticated.addChildren([nodes]),
      forbidden,
    ]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return router
}

it.each(['/nodes', '/nodes/'])(
  'shows the root empty list at %s and opens the real add dialog',
  async (path) => {
    await renderPage(path)
    expect(await screen.findByText('No nodes yet')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Check all' })).toBeDisabled()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Add Node' }))
    expect(await screen.findByRole('dialog')).toBeVisible()
    expect(api.post).not.toHaveBeenCalled()
  }
)

it.each([1, 10])(
  'denies role %s without requesting the root node API',
  async (role) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 3, username: 'local-non-root', role })
    await renderPage()
    expect(
      await screen.findByRole('heading', { name: 'Forbidden' })
    ).toBeVisible()
    expect(api.get).not.toHaveBeenCalled()
  }
)

it('filters loaded nodes from the URL and restores rows when filters reset', async () => {
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: [
        { id: 1, name: 'Local Alpha', provider: 'render', enabled: true },
        { id: 2, name: 'Local Beta', provider: 'render', enabled: true },
      ],
    },
  })
  const router = await renderPage('/nodes?filter=Alpha')
  expect(await screen.findByText('Local Alpha')).toBeVisible()
  expect(screen.queryByText('Local Beta')).not.toBeInTheDocument()
  await userEvent.setup().click(screen.getByRole('button', { name: 'Reset' }))
  expect(await screen.findByText('Local Beta')).toBeVisible()
  await waitFor(() =>
    expect(router.state.location.search.filter).toBeUndefined()
  )
})

it('shows a retry after list failure and recovers to the empty state', async () => {
  vi.mocked(api.get).mockRejectedValue(new Error('Local node API unavailable'))
  await renderPage()
  const retry = await screen.findByRole('button', { name: /retry/i })
  expect(retry).toBeVisible()
  expect(screen.queryByText('No nodes yet')).not.toBeInTheDocument()
  vi.mocked(api.get).mockResolvedValue({ data: { success: true, data: [] } })
  await userEvent.setup().click(retry)
  expect(await screen.findByText('No nodes yet')).toBeVisible()
})

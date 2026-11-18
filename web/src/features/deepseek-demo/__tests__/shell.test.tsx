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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { DirectionProvider } from '@/context/direction-provider'
import { api } from '@/lib/api'
import { Route as DemoRoute } from '@/routes/_authenticated/demo'
import { Route as AuthenticatedRoute } from '@/routes/_authenticated/route'
import { useAuthStore } from '@/stores/auth-store'

let client: QueryClient
beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  useAuthStore.setState((state) => ({
    auth: {
      ...state.auth,
      user: { id: 8, username: 'Alice', role: 100 },
      accessToken: 'shell-test-session',
      accessExpiresAt: Math.floor(Date.now() / 1000) + 3600,
      bootstrapState: 'complete',
    },
  }))
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/user/self/groups') {
      return { data: { success: true, data: { default: { desc: 'Default' } } } }
    }
    if (url === '/api/user/models') {
      return { data: { success: true, data: ['deepseek-chat'] } }
    }
    return { data: { success: true, data: [], announcements: [], notices: [] } }
  })
})
afterEach(() => {
  client.clear()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
  vi.restoreAllMocks()
})

async function renderShell(path: string) {
  const root = createRootRoute({ component: Outlet })
  const authenticated = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
    beforeLoad: AuthenticatedRoute.options.beforeLoad as () => Promise<void>,
    component: AuthenticatedRoute.options.component,
  })
  const demo = createRoute({
    getParentRoute: () => authenticated,
    path: 'demo/',
    component: DemoRoute.options.component,
  })
  const nodes = createRoute({
    getParentRoute: () => authenticated,
    path: 'nodes/',
    component: () => (
      <main>
        <h1>Nodes route content</h1>
      </main>
    ),
  })
  const pool = createRoute({
    getParentRoute: () => authenticated,
    path: 'public-pool/',
    component: () => (
      <main>
        <h1>Public pool route content</h1>
      </main>
    ),
  })
  const router = createRouter({
    routeTree: root.addChildren([
      authenticated.addChildren([demo, nodes, pool]),
    ]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <DirectionProvider>
        <RouterProvider router={router} />
      </DirectionProvider>
    </QueryClientProvider>
  )
  return router
}

describe('demo-only authenticated shell', () => {
  it.each(['/demo', '/demo/'])(
    'renders %s with only chat navigation and no console header or sidebar',
    async (path) => {
      await renderShell(path)
      expect(
        await screen.findByRole('heading', {
          name: 'How can I help you today?',
        })
      ).toBeVisible()
      expect(screen.getAllByRole('banner')).toHaveLength(1)
      expect(screen.getByRole('banner')).toHaveClass('ds-demo-header')
      expect(
        document.querySelector('[data-slot=sidebar]')
      ).not.toBeInTheDocument()
      expect(
        document.querySelector('[data-slot=sidebar-inset]')
      ).not.toBeInTheDocument()
      expect(
        screen.getByRole('complementary', { name: 'Chat history' })
      ).toBeInTheDocument()
      expect(
        screen.getByRole('link', { name: 'Back to console' })
      ).toHaveAttribute('href', '/dashboard')
      expect(screen.getByText('NewAPI · QuantumNous')).toBeInTheDocument()
    }
  )

  it.each([
    ['/nodes', 'Nodes route content'],
    ['/public-pool', 'Public pool route content'],
  ])(
    'keeps the original console header and sidebar for %s',
    async (path, title) => {
      await renderShell(path)
      expect(await screen.findByRole('heading', { name: title })).toBeVisible()
      expect(screen.getByRole('banner')).toBeInTheDocument()
      expect(document.querySelector('[data-slot=sidebar]')).toBeInTheDocument()
      expect(
        document.querySelector('[data-slot=sidebar-inset]')
      ).toBeInTheDocument()
    }
  )

  it('unmounts the full-screen demo immediately on sign-out', async () => {
    await renderShell('/demo')
    await screen.findByRole('heading', { name: 'How can I help you today?' })
    useAuthStore.getState().auth.reset()
    await waitFor(() =>
      expect(
        screen.queryByRole('heading', { name: 'How can I help you today?' })
      ).not.toBeInTheDocument()
    )
  })
})

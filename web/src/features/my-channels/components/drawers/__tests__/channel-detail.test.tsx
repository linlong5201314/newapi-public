import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { channelSchema } from '../../../types'
import { ChannelDetailDrawer } from '../channel-detail-drawer'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
}))
const channel = channelSchema.parse({
  id: 7,
  name: 'Long channel',
  type: 1,
  key: '',
  models: 'deepseek-chat',
  status: 1,
  created_time: 0,
  test_time: 0,
  response_time: 0,
  balance_updated_time: 0,
})
let client: QueryClient
let fixedProxy = false
let legacyProxy: 'direct' | 'public_pool' | undefined
beforeEach(() => {
  vi.resetAllMocks()
  fixedProxy = false
  legacyProxy = undefined
  vi.mocked(api.put).mockResolvedValue({ data: { success: true } })
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/channel/self/7/detail') {
      return {
        data: {
          success: true,
          data: {
            channel: {
              ...channel,
              setting: JSON.stringify({ proxy: '', extra_setting: true }),
            },
            proxy_configured: fixedProxy,
            proxy_policy: {
              mode: legacyProxy ?? (fixedProxy ? 'fixed' : 'user_pool'),
            },
            models: ['deepseek-chat'],
            all_models: ['deepseek-chat', 'qwen-coder'],
            public_models: [],
            health: { state: 'unknown', samples: 0 },
            proxy: fixedProxy
              ? {
                  mode: 'fixed',
                  label: 'Fixed proxy',
                  value: 'http://***@proxy.example:8080',
                }
              : { mode: 'pool', label: 'User pool', value: '' },
            template: { name: 'Long template', ip_free: true },
            token_estimate: { hidden: true },
            proxy_pool: {
              endpoint_total: 2,
              endpoint_enabled: 2,
              subscription_nodes: 1,
              subscription_total: 1,
              regions: {},
              selectable_nodes: [],
            },
          },
        },
      }
    }
    if (
      url === '/api/proxy-pool/endpoints' ||
      url === '/api/proxy-pool/subscriptions'
    ) {
      return { data: { success: true, data: [] } }
    }
    throw new Error(`Unexpected GET: ${url}`)
  })
})
afterEach(() => client.clear())
async function renderDrawer() {
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => (
        <ChannelDetailDrawer
          channel={channel}
          open
          onOpenChange={() => undefined}
        />
      ),
    }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

it('keeps a viewport-bounded drawer with a keyboard focusable vertical scroll region', async () => {
  await renderDrawer()
  await screen.findByText('qwen-coder')
  const region = screen.getByRole('region', { name: 'Channel detail content' })
  expect(region).toHaveAttribute('tabindex', '0')
  expect(region).toHaveClass(
    'min-h-0',
    'flex-1',
    'overflow-y-auto',
    'overscroll-contain'
  )
  expect(screen.getByRole('dialog')).toHaveClass('h-dvh', 'overflow-hidden')
  region.focus()
  fireEvent.keyDown(region, { key: 'PageDown' })
  expect(region).toHaveFocus()
  expect(screen.getByRole('heading', { name: /Long channel/ })).toBeVisible()
  expect(
    within(region).getByRole('button', {
      name: /Save model configuration|保存模型配置/,
    })
  ).toBeVisible()
})

it('enables only explicitly chosen passing models and retains untested catalog entries', async () => {
  const user = userEvent.setup()
  const get = vi.mocked(api.get).getMockImplementation()
  if (!get) throw new Error('Channel detail GET fixture is required')
  vi.mocked(api.get).mockImplementation(async (url, config) => {
    if (url === '/api/channel/self/model_probe/7') {
      return {
        data: {
          success: true,
          data: { channel_id: 7, endpoints: [], results: [] },
        },
      }
    }
    return get(url, config)
  })
  const now = Math.floor(Date.now() / 1000)
  vi.mocked(api.post).mockImplementation(async (url) => {
    if (url === '/api/channel/self/model_probe/7') {
      return {
        data: {
          success: true,
          data: {
            channel_id: 7,
            passed_models: ['qwen-coder'],
            results: [
              {
                model: 'qwen-coder',
                endpoint_id: 0,
                status: 'available',
                reason: 'passed',
                checked_at: now,
                expires_at: now + 600,
                latency_ms: 12,
                recent_passes: 1,
                recent_sample_count: 1,
              },
              {
                model: 'deepseek-chat',
                endpoint_id: 0,
                status: 'unavailable',
                reason: 'upstream_rejected',
                checked_at: now,
                expires_at: now + 600,
                latency_ms: 12,
                recent_passes: 0,
                recent_sample_count: 1,
              },
            ],
          },
        },
      }
    }
    return { data: { success: true } }
  })
  await renderDrawer()
  await screen.findByText('qwen-coder')
  await user.click(screen.getByRole('button', { name: 'Test models' }))
  expect(api.post).not.toHaveBeenCalled()
  const runTests = await screen.findByRole('button', {
    name: 'Run selected tests',
  })
  await waitFor(() => expect(runTests).toBeEnabled())
  await user.click(runTests)
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/model_probe/7',
      expect.objectContaining({ models: ['deepseek-chat', 'qwen-coder'] }),
      expect.anything()
    )
  )
  const useModel = await screen.findByRole('checkbox', {
    name: /^Use qwen-coder/,
  })
  expect(
    screen.queryByRole('checkbox', { name: /^Use deepseek-chat/ })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Use selected passing models' })
  ).toBeDisabled()
  await user.click(useModel)
  await user.click(
    screen.getByRole('button', { name: 'Use selected passing models' })
  )
  expect(
    screen.getByRole('checkbox', { name: 'Enable qwen-coder in my channels' })
  ).toBeChecked()
  expect(
    screen.getByRole('checkbox', {
      name: 'Enable deepseek-chat in my channels',
    })
  ).toBeChecked()
  expect(
    screen.getByRole('checkbox', {
      name: 'Publish qwen-coder to the public pool',
    })
  ).not.toBeChecked()
  await user.click(
    screen.getByRole('button', { name: 'Save model configuration' })
  )
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith(
      '/api/channel/self/',
      expect.objectContaining({ id: 7, models: 'deepseek-chat,qwen-coder' }),
      expect.anything()
    )
  )
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/self/visibility',
    expect.objectContaining({
      channelId: 7,
      allModels: ['deepseek-chat', 'qwen-coder'],
      publicModels: [],
    }),
    expect.anything()
  )
})

it('asks before deleting a channel from the detail drawer', async () => {
  const user = userEvent.setup()
  await renderDrawer()
  await screen.findByText('qwen-coder')
  await user.click(
    screen.getByRole('button', { name: /Delete channel|删除渠道/ })
  )
  expect(api.delete).not.toHaveBeenCalled()
  expect(await screen.findByRole('alertdialog')).toBeVisible()
})

it.each(['direct', 'public_pool'] as const)(
  'requires an explicit owned replacement before saving legacy %s proxy',
  async (mode) => {
    legacyProxy = mode
    const user = userEvent.setup()
    await renderDrawer()
    await screen.findByText('qwen-coder')
    const save = screen.getByRole('button', { name: 'Save proxy settings' })
    expect(save).toBeDisabled()
    expect(api.put).not.toHaveBeenCalled()
    await user.selectOptions(
      screen.getByRole('combobox', { name: 'Outbound proxy mode' }),
      'user_pool'
    )
    expect(save).toBeEnabled()
    await user.click(save)
    await waitFor(() => expect(api.put).toHaveBeenCalled())
    const payload = vi.mocked(api.put).mock.calls[0][1] as { setting: string }
    expect(JSON.parse(payload.setting)).toMatchObject({
      proxy_policy: { mode: 'user_pool' },
      extra_setting: true,
    })
  }
)

it('keeps a fixed proxy secret on the server and preserves unrelated settings when saving', async () => {
  fixedProxy = true
  const user = userEvent.setup()
  await renderDrawer()
  await screen.findByText('qwen-coder')
  expect(screen.getByLabelText('Fixed proxy URL')).toHaveValue('')
  await user.click(screen.getByRole('button', { name: 'Save proxy settings' }))
  await waitFor(() => expect(api.put).toHaveBeenCalled())
  const payload = vi.mocked(api.put).mock.calls[0][1] as { setting: string }
  expect(JSON.parse(payload.setting)).toMatchObject({
    proxy: '',
    proxy_policy: { mode: 'fixed' },
    extra_setting: true,
  })
  expect(payload.setting).not.toContain('***')
  expect(
    vi
      .mocked(api.get)
      .mock.calls.some(([url]) => String(url).includes('/runtime'))
  ).toBe(false)
})

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { SelfChannelDetail } from '@/features/my-channels/api'
import { channelSchema } from '@/features/my-channels/types'
import { api } from '@/lib/api'

import { MyModelsPage } from '../index'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
}))

const savedChannel = channelSchema.parse({
  id: 7,
  type: 1,
  key: '',
  status: 1,
  visibility: 1,
  name: 'Saved channel',
  created_time: 0,
  test_time: 0,
  response_time: 0,
  balance_updated_time: 0,
  models: 'deepseek-chat',
})
let channel = savedChannel
let catalog: string[]
let published: string[]
let detailResponse: {
  success: boolean
  message?: string
  data?: Partial<SelfChannelDetail>
}
let client: QueryClient

beforeEach(() => {
  vi.resetAllMocks()
  channel = { ...savedChannel }
  catalog = ['deepseek-chat', 'qwen-coder', 'claude-sonnet']
  published = []
  detailResponse = { success: true }
  client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/channel/self/') {
      return { data: { success: true, data: { items: [channel], total: 1 } } }
    }
    if (url === '/api/channel/self/7/detail') {
      return {
        data: {
          ...detailResponse,
          data: detailResponse.success
            ? {
                channel,
                models: channel.models.split(',').filter(Boolean),
                all_models: [...catalog],
                public_models: published,
                template: { ip_free: false },
                proxy: { label: 'Direct' },
              }
            : undefined,
        },
      }
    }
    if (url === '/api/channel/self/fetch_models/7') {
      catalog = [...new Set([...catalog, 'new-model'])]
      return { data: { success: true, data: [...catalog] } }
    }
    throw new Error(`Unexpected GET: ${url}`)
  })
  vi.mocked(api.put).mockImplementation(async (_url, body) => {
    channel = { ...channel, ...(body as object) }
    return { data: { success: true } }
  })
  vi.mocked(api.post).mockImplementation(async (_url, body) => {
    const payload = body as { allModels: string[]; publicModels: string[] }
    catalog = payload.allModels
    published = payload.publicModels
    return { data: { success: true } }
  })
  vi.mocked(api.delete).mockResolvedValue({ data: { success: true } })
})

afterEach(() => client.clear())

async function renderPage() {
  const router = createRouter({
    routeTree: createRootRoute({ component: MyModelsPage }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

async function expandChannel(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    await screen.findByRole('button', { name: /Expand models|点击展开模型/ })
  )
  await screen.findByText('qwen-coder')
}

describe('saved channel model management', () => {
  it('uses the supported page parameter and retains public visibility', async () => {
    await renderPage()
    expect(await screen.findByText('Saved channel')).toBeVisible()
    expect(api.get).toHaveBeenCalledWith('/api/channel/self/', {
      params: { p: 1, page_size: 100 },
    })
    expect(screen.getByText(/Public|公开中/)).toBeVisible()
  })

  it('requires confirmation before deleting the entire channel and supports cancel', async () => {
    const user = userEvent.setup()
    await renderPage()
    await user.click(
      await screen.findByRole('button', { name: /Delete channel|删除渠道/ })
    )
    expect(api.delete).not.toHaveBeenCalled()
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(api.delete).not.toHaveBeenCalled()
    await user.click(
      screen.getByRole('button', { name: /Delete channel|删除渠道/ })
    )
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', {
        name: /Delete channel|删除渠道/,
      })
    )
    await waitFor(() =>
      expect(api.delete).toHaveBeenCalledWith(
        '/api/channel/self/7',
        expect.any(Object)
      )
    )
  })

  it('fetches by saved channel ID without enabling newly discovered models', async () => {
    const user = userEvent.setup()
    await renderPage()
    await expandChannel(user)
    await user.click(
      screen.getByRole('button', {
        name: /Fetch latest upstream models|获取上游最新模型/,
      })
    )
    expect(await screen.findByText('new-model')).toBeVisible()
    expect(api.get).toHaveBeenCalledWith(
      '/api/channel/self/fetch_models/7',
      expect.any(Object)
    )
    expect(
      screen.getByRole('checkbox', {
        name: 'Enable deepseek-chat in my channels',
      })
    ).toBeChecked()
    expect(
      screen.getByRole('checkbox', { name: 'Enable new-model in my channels' })
    ).not.toBeChecked()
  })

  it('persists the full catalog when only deepseek is selected and restores it after remount', async () => {
    const user = userEvent.setup()
    const view = await renderPage()
    await expandChannel(user)
    await user.click(
      screen.getByRole('button', { name: /Save channel models|保存该渠道模型/ })
    )
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/api/channel/self/visibility',
        expect.objectContaining({
          allModels: ['deepseek-chat', 'qwen-coder', 'claude-sonnet'],
          publicModels: [],
        }),
        expect.any(Object)
      )
    )
    expect(api.put).toHaveBeenCalledWith(
      '/api/channel/self/',
      { id: 7, models: 'deepseek-chat' },
      expect.any(Object)
    )
    view.unmount()
    client.clear()
    await renderPage()
    await expandChannel(user)
    expect(
      screen.getByRole('checkbox', {
        name: 'Enable deepseek-chat in my channels',
      })
    ).toBeChecked()
    expect(
      screen.getByRole('checkbox', { name: 'Enable qwen-coder in my channels' })
    ).not.toBeChecked()
  })

  it('retains selection and candidate models when upstream fetch fails', async () => {
    const user = userEvent.setup()
    await renderPage()
    await expandChannel(user)
    vi.mocked(api.get).mockRejectedValueOnce(new Error('Upstream unavailable'))
    await user.click(
      screen.getByRole('button', {
        name: /Fetch latest upstream models|获取上游最新模型/,
      })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', {
          name: /Fetch latest upstream models|获取上游最新模型/,
        })
      ).toBeEnabled()
    )
    expect(
      screen.getByRole('checkbox', {
        name: 'Enable deepseek-chat in my channels',
      })
    ).toBeChecked()
    expect(screen.getByText('qwen-coder')).toBeVisible()
  })

  it('does not publish or report success after the model update business response fails', async () => {
    const user = userEvent.setup()
    await renderPage()
    await expandChannel(user)
    vi.mocked(api.put).mockResolvedValueOnce({
      data: { success: false, message: 'Model update denied' },
    })
    await user.click(
      screen.getByRole('button', { name: /Save channel models|保存该渠道模型/ })
    )
    await waitFor(() => expect(api.put).toHaveBeenCalled())
    await waitFor(() =>
      expect(
        screen.getByRole('button', {
          name: /Save channel models|保存该渠道模型/,
        })
      ).toBeEnabled()
    )
    expect(api.post).not.toHaveBeenCalled()
  })

  it('does not allow saving an incomplete catalog after the detail request fails', async () => {
    const user = userEvent.setup()
    detailResponse = { success: false, message: 'Channel details unavailable' }
    await renderPage()
    await user.click(
      await screen.findByRole('button', { name: /Expand models|点击展开模型/ })
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Channel details unavailable'
    )
    expect(
      screen.queryByRole('button', {
        name: /Save channel models|保存该渠道模型/,
      })
    ).not.toBeInTheDocument()
  })
})

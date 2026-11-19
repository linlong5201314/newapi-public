import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterContextProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { channelSchema, type Channel } from '../../../types'
import { ChannelsProvider } from '../../channels-provider'
import { ChannelMutateDrawer } from '../channel-mutate-drawer'

let client: QueryClient
const channel = channelSchema.parse({
  id: 42,
  name: 'Saved model testing channel',
  type: 1,
  models: 'retained-model',
  group: 'default',
  key: '',
  status: 1,
  created_time: 1,
  test_time: 0,
  response_time: 0,
  balance_updated_time: 0,
})

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/channel/self/42') {
      return { data: { success: true, data: channel } }
    }
    if (url === '/api/channel/self/model_probe/42') {
      return {
        data: {
          success: true,
          data: { channel_id: 42, endpoints: [], results: [] },
        },
      }
    }
    if (url === '/api/channel/self/default_base_urls') {
      return { data: { success: true, data: {} } }
    }
    if (url === '/api/channel/self/models') {
      return {
        data: {
          success: true,
          data: [{ id: 'retained-model' }, { id: 'new-model' }],
        },
      }
    }
    return { data: { success: true, data: [] } }
  })
  vi.spyOn(api, 'post').mockResolvedValue({ data: { success: true } })
})
afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
})

function renderForm(currentRow?: Channel) {
  const router = createRouter({
    routeTree: createRootRoute(),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        <ChannelsProvider>
          <ChannelMutateDrawer
            open
            onOpenChange={() => undefined}
            currentRow={currentRow}
          />
        </ChannelsProvider>
      </RouterContextProvider>
    </QueryClientProvider>
  )
}

it('shows a disabled test action and save or connect hint before a channel exists', async () => {
  const user = userEvent.setup()
  renderForm()
  await user.click(await screen.findByRole('option', { name: /^DeepSeek / }))
  expect(
    await screen.findByRole('button', { name: 'Test models' })
  ).toBeDisabled()
  expect(
    screen.getByText('Save or connect the channel before testing models.')
  ).toBeVisible()
  expect(api.post).not.toHaveBeenCalled()
  expect(
    vi
      .mocked(api.get)
      .mock.calls.some(([url]) => String(url).includes('/model_probe/'))
  ).toBe(false)
})

it('uses the verified saved channel ID for tests without invoking inference on open', async () => {
  const user = userEvent.setup()
  renderForm(channel)
  await screen.findByDisplayValue('Saved model testing channel')
  const test = await screen.findByRole('button', { name: 'Test models' })
  expect(test).toBeEnabled()
  await user.click(test)
  await waitFor(() =>
    expect(api.get).toHaveBeenCalledWith(
      '/api/channel/self/model_probe/42',
      expect.objectContaining({
        params: expect.objectContaining({ models: 'retained-model' }),
      })
    )
  )
  expect(api.post).not.toHaveBeenCalled()
  expect(
    screen.queryByText('Save or connect the channel before testing models.')
  ).not.toBeInTheDocument()
})

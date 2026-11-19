import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { NodeModelAvailability } from '../node-model-availability'

function mount(disabled = false) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <NodeModelAvailability
        endpointId={17}
        subscriptionId={4}
        disabled={disabled}
      />
    </QueryClientProvider>
  )
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('Node model availability action', () => {
  it('loads only own-channel metadata on explicit open and passes selected node to explicit model testing', async () => {
    const user = userEvent.setup()
    const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (url === '/api/channel/self/') {
        return {
          data: {
            success: true,
            data: {
              items: [
                { id: 5, name: 'Own channel', models: 'model-a,model-b' },
              ],
              total: 1,
            },
          },
        }
      }
      return {
        data: {
          success: true,
          data: {
            channel_id: 5,
            endpoints: [
              { id: 17, subscription_id: 4, public: false, status: 'unknown' },
            ],
            results: [],
            max_models: 3,
            max_endpoints: 3,
            max_output_tokens: 8,
          },
        },
      }
    })
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: { channel_id: 5, results: [], passed_models: [] },
      },
    })
    mount()
    expect(get).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Model availability' }))
    const dialog = screen.getByRole('dialog', {
      name: 'Node model availability',
    })
    expect(
      await within(dialog).findByRole('option', { name: 'Own channel' })
    ).toBeInTheDocument()
    expect(within(dialog).getByText('Endpoint #17')).toBeVisible()
    expect(within(dialog).getByText('Subscription #4')).toBeVisible()
    await user.selectOptions(
      within(dialog).getByRole('combobox', { name: 'My channel' }),
      '5'
    )
    expect(post).not.toHaveBeenCalled()
    await user.click(
      within(dialog).getByRole('button', { name: 'Test models' })
    )
    const testDialog = screen.getByRole('dialog', { name: 'Test models' })
    expect(
      await within(testDialog).findByRole('checkbox', { name: /Endpoint #17/ })
    ).toBeChecked()
    expect(post).not.toHaveBeenCalled()
    await user.click(
      within(testDialog).getByRole('button', { name: 'Run selected tests' })
    )
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/api/channel/self/model_probe/5',
        expect.objectContaining({
          models: ['model-a', 'model-b'],
          endpoint_ids: [17],
        }),
        expect.any(Object)
      )
    )
    expect(
      get.mock.calls.some(
        ([url]) => typeof url === 'string' && url.includes('/key')
      )
    ).toBe(false)
  })

  it('shows an empty own-channel state without exposing public or admin channel requests', async () => {
    const user = userEvent.setup()
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    mount()
    await user.click(screen.getByRole('button', { name: 'Model availability' }))
    expect(
      await screen.findByText(
        'Add your own channel to test model availability on this node.'
      )
    ).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Test models' })
    ).not.toBeInTheDocument()
    expect(get).toHaveBeenCalledWith(
      '/api/channel/self/',
      expect.objectContaining({ params: { p: 1, page_size: 20 } })
    )
  })

  it('when own-channel metadata fails exposes retry without running model probes', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockRejectedValue(new Error('Fixture unavailable'))
    const post = vi.spyOn(api, 'post')
    mount()
    await user.click(screen.getByRole('button', { name: 'Model availability' }))
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
    expect(post).not.toHaveBeenCalled()
  })

  it('when row is disabled remains keyboard-disabled and performs no metadata request', () => {
    const get = vi.spyOn(api, 'get')
    mount(true)
    expect(
      screen.getByRole('button', { name: 'Model availability' })
    ).toBeDisabled()
    expect(get).not.toHaveBeenCalled()
  })
})

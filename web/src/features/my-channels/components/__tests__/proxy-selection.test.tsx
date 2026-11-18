import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import type { ChannelProxyPolicy } from '../../api'
import { ChannelProxySelector } from '../channel-proxy-selector'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn() } }))
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/proxy-pool/endpoints') {
      return {
        data: {
          success: true,
          data: [
            { id: 14, name: 'My endpoint', is_owner: true, enabled: true },
            {
              id: 15,
              name: 'Public endpoint',
              is_owner: false,
              enabled: true,
              public: true,
            },
          ],
        },
      }
    }
    if (url === '/api/proxy-pool/subscriptions') {
      return {
        data: {
          success: true,
          data: [{ id: 9, name: 'My subscription', enabled: true }],
        },
      }
    }
    throw new Error(`Unexpected GET: ${url}`)
  })
})

it('selects owner subscription and endpoint IDs without requesting global runtime endpoints', async () => {
  const change = vi.fn()
  function Fixture() {
    const [policy, setPolicy] = useState<ChannelProxyPolicy>({
      mode: 'user_pool',
    })
    return (
      <ChannelProxySelector
        enabled
        policy={policy}
        proxy=''
        onPolicyChange={(next) => {
          change(next)
          setPolicy(next)
        }}
        onProxyChange={() => undefined}
      />
    )
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const user = userEvent.setup()
  const view = render(
    <QueryClientProvider client={client}>
      <Fixture />
    </QueryClientProvider>
  )
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Outbound proxy mode' }),
    'subscription'
  )
  await user.selectOptions(
    await screen.findByRole('combobox', { name: 'My subscription' }),
    '9'
  )
  expect(change).toHaveBeenLastCalledWith({
    mode: 'subscription',
    subscription_id: 9,
  })
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Outbound proxy mode' }),
    'endpoint'
  )
  expect(
    screen.queryByRole('option', { name: 'Public endpoint' })
  ).not.toBeInTheDocument()
  await user.selectOptions(
    await screen.findByRole('combobox', { name: 'My proxy endpoint' }),
    '14'
  )
  expect(change).toHaveBeenLastCalledWith({ mode: 'endpoint', endpoint_id: 14 })
  expect(vi.mocked(api.get).mock.calls.map(([url]) => url)).toEqual(
    expect.arrayContaining([
      '/api/proxy-pool/endpoints',
      '/api/proxy-pool/subscriptions',
    ])
  )
  expect(
    vi
      .mocked(api.get)
      .mock.calls.some(([url]) => String(url).includes('/runtime'))
  ).toBe(false)
  view.unmount()
  client.clear()
})

it('offers only owned outbound strategies when creating a channel', () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <ChannelProxySelector
        enabled={false}
        policy={{ mode: 'user_pool' }}
        proxy=''
        onPolicyChange={vi.fn()}
        onProxyChange={vi.fn()}
      />
    </QueryClientProvider>
  )
  expect(
    screen.getAllByRole('option').map((option) => option.getAttribute('value'))
  ).toEqual(['user_pool', 'subscription', 'endpoint', 'fixed'])
  view.unmount()
  client.clear()
})

it.each(['direct', 'public_pool'] as const)(
  'keeps legacy %s read-only until the owner explicitly selects a replacement',
  async (mode) => {
    const change = vi.fn()
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    })
    function Fixture() {
      const [policy, setPolicy] = useState<ChannelProxyPolicy>({ mode })
      return (
        <ChannelProxySelector
          enabled={false}
          policy={policy}
          proxy=''
          onPolicyChange={(next) => {
            change(next)
            setPolicy(next)
          }}
          onProxyChange={vi.fn()}
        />
      )
    }
    const user = userEvent.setup()
    const view = render(
      <QueryClientProvider client={client}>
        <Fixture />
      </QueryClientProvider>
    )
    const select = screen.getByRole('combobox', { name: 'Outbound proxy mode' })
    expect(select).toHaveValue(mode)
    const legacy = screen
      .getAllByRole('option')
      .find((option) => option.getAttribute('value') === mode)
    expect(legacy).toBeDisabled()
    expect(select).toHaveAccessibleDescription(
      'This legacy proxy setting is read-only. Select one of your own proxy strategies to change it. Other channel settings keep the current route.'
    )
    expect(change).not.toHaveBeenCalled()
    await user.selectOptions(select, 'user_pool')
    expect(change).toHaveBeenLastCalledWith({ mode: 'user_pool' })
    expect(
      screen.queryByRole('option', { name: /Legacy/ })
    ).not.toBeInTheDocument()
    view.unmount()
    client.clear()
  }
)

it('never pre-fills a masked proxy URL when preserving a configured fixed proxy', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const change = vi.fn()
  const view = render(
    <QueryClientProvider client={client}>
      <ChannelProxySelector
        enabled
        policy={{ mode: 'fixed' }}
        proxy=''
        fixedConfigured
        onPolicyChange={change}
        onProxyChange={change}
      />
    </QueryClientProvider>
  )
  expect(screen.getByLabelText('Fixed proxy URL')).toHaveValue('')
  expect(
    screen.getByText('Leave blank to keep the configured proxy.')
  ).toBeVisible()
  expect(change).not.toHaveBeenCalled()
  view.unmount()
  client.clear()
})

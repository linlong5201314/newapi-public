import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { FromTemplateDialog } from '@/features/my-channels/components/dialogs/from-template-dialog'
import { api } from '@/lib/api'

import * as accountApi from '../api'
import { AccountBindingsCard } from '../index'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))
let client: QueryClient
beforeEach(() => {
  vi.resetAllMocks()
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/channel/self/free_templates') {
      return {
        data: {
          success: true,
          data: [
            {
              id: 'workbuddy',
              name: 'WorkBuddy fixture',
              authMode: 'oauth',
              models: [],
            },
            {
              id: 'trae-cn',
              name: 'Trae fixture',
              authMode: 'oauth',
              models: [],
            },
          ],
        },
      }
    }
    return { data: { success: true, data: [] } }
  })
  vi.mocked(api.post).mockResolvedValue({ data: { success: true } })
})
afterEach(() => client.clear())
function renderUI(bindings = false) {
  return render(
    <QueryClientProvider client={client}>
      {bindings ? (
        <AccountBindingsCard />
      ) : (
        <FromTemplateDialog open onOpenChange={() => undefined} />
      )}
    </QueryClientProvider>
  )
}

it('sends WorkBuddy owner egress in camelJSON with the account action header', async () => {
  await accountApi.startWorkbuddyOAuth('global', {
    proxy: '',
    proxy_policy: { mode: 'subscription', subscription_id: 9 },
    displayName: 'My account',
    public: false,
  })
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/self/oauth/start',
    {
      type: 'workbuddy',
      realm: 'global',
      proxy: '',
      proxyPolicy: { mode: 'subscription', subscription_id: 9 },
      displayName: 'My account',
      public: false,
    },
    expect.objectContaining({
      singleUseAuthorization: true,
      headers: { 'X-Account-Action': 'authorize' },
    })
  )
  await accountApi.pollWorkbuddyOAuth('private session')
  expect(api.get).toHaveBeenCalledWith(
    '/api/channel/self/oauth/poll?sessionId=private%20session&mode=create',
    expect.objectContaining({ headers: { 'X-Account-Action': 'authorize' } })
  )
})

it('cancels pending WorkBuddy authorization through the owner-scoped action endpoint', async () => {
  await accountApi.cancelWorkbuddyOAuth('pending')
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/self/oauth/cancel',
    { sessionId: 'pending' },
    expect.objectContaining({
      singleUseAuthorization: true,
      headers: { 'X-Account-Action': 'authorize' },
    })
  )
})

it('sends explicit Trae consent and owner egress without inventing an OAuth callback', async () => {
  await accountApi.importTrae({
    refreshToken: 'own-token',
    confirmRotation: true,
    proxy: '',
    proxy_policy: { mode: 'user_pool' },
    public: false,
  })
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/self/import_trae',
    {
      refreshToken: 'own-token',
      confirmRotation: true,
      proxy: '',
      proxyPolicy: { mode: 'user_pool' },
      public: false,
      mode: 'create',
    },
    expect.objectContaining({
      singleUseAuthorization: true,
      headers: { 'X-Account-Action': 'authorize' },
    })
  )
})

it.each([false, true])(
  'reserves WorkBuddy popup before network and cancels a late response after unmount (bindings=%s)',
  async (bindings) => {
    const popup = {
      opener: undefined,
      closed: false,
      location: { href: '' },
      close: vi.fn(),
    }
    const open = vi
      .spyOn(window, 'open')
      .mockReturnValue(popup as unknown as Window)
    let resolve!: (value: unknown) => void
    vi.mocked(api.post).mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done
        })
    )
    const user = userEvent.setup()
    const view = renderUI(bindings)
    if (!bindings) {
      await user.click(
        await screen.findByRole('button', { name: /WorkBuddy fixture/ })
      )
    }
    await user.click(
      screen.getByRole('button', {
        name: /开始授权|Start WorkBuddy authorization|开始 WorkBuddy OAuth/,
      })
    )
    expect(open).toHaveBeenCalledWith('about:blank', '_blank')
    expect(popup.opener).toBeNull()
    view.unmount()
    await act(async () =>
      resolve({
        data: {
          success: true,
          data: {
            sessionId: 'late',
            authUrl: 'https://www.codebuddy.cn/auth',
            expiresIn: 600,
          },
        },
      })
    )
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/api/channel/self/oauth/cancel',
        { sessionId: 'late' },
        expect.anything()
      )
    )
    expect(popup.location.href).toBe('')
    expect(popup.close).toHaveBeenCalled()
  }
)

it.each([false, true])(
  'requires explicit Trae rotation consent and rejects non-loopback callbacks (bindings=%s)',
  async (bindings) => {
    const user = userEvent.setup()
    renderUI(bindings)
    if (!bindings) {
      await user.click(
        await screen.findByRole('button', { name: /Trae fixture/ })
      )
    }
    const input = screen.getByRole('textbox', {
      name: 'Trae callback URL or refresh token',
    })
    await user.type(input, 'own-token')
    const submit = screen.getByRole('button', {
      name: bindings ? '导入并开通' : /提交 Token 授权并开通渠道/,
    })
    expect(submit).toBeDisabled()
    const consent = screen.getByRole('checkbox', {
      name: 'I understand that importing Trae credentials rotates tokens and may sign out other clients.',
    })
    expect(consent).not.toBeChecked()
    await user.click(consent)
    await user.clear(input)
    await user.type(input, 'https://evil.example/authorize?code=secret')
    await user.click(submit)
    expect(api.post).not.toHaveBeenCalledWith(
      '/api/channel/self/import_trae',
      expect.anything(),
      expect.anything()
    )
    expect(
      screen.getByText(
        'Use your own refresh token or the official http://127.0.0.1:port/authorize callback.'
      )
    ).toBeVisible()
    await user.clear(input)
    await user.type(input, 'own-token')
    await user.click(submit)
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/api/channel/self/import_trae',
        expect.objectContaining({
          refreshToken: 'own-token',
          confirmRotation: true,
          mode: 'create',
          public: false,
          proxyPolicy: { mode: 'user_pool' },
        }),
        expect.anything()
      )
    )
  }
)

it.each([false, true])(
  'closes the reserved popup and clears pending UI when WorkBuddy start fails (bindings=%s)',
  async (bindings) => {
    const popup = {
      opener: undefined,
      closed: false,
      location: { href: '' },
      close: vi.fn(),
    }
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    vi.mocked(api.post).mockRejectedValueOnce(
      new Error('authorization unavailable')
    )
    const user = userEvent.setup()
    renderUI(bindings)
    if (!bindings) {
      await user.click(
        await screen.findByRole('button', { name: /WorkBuddy fixture/ })
      )
    }
    await user.click(
      screen.getByRole('button', { name: 'Start WorkBuddy authorization' })
    )
    expect(
      await screen.findByText('Failed to start authorization')
    ).toBeVisible()
    expect(popup.close).toHaveBeenCalled()
    expect(
      screen.getByRole('button', { name: 'Start WorkBuddy authorization' })
    ).toBeEnabled()
    expect(
      screen.queryByRole('button', { name: 'Cancel authorization' })
    ).not.toBeInTheDocument()
  }
)

it.each([false, true])(
  'cancels a pending WorkBuddy session explicitly and leaves private defaults unchanged (bindings=%s)',
  async (bindings) => {
    const popup = {
      opener: undefined,
      closed: false,
      location: { href: '' },
      close: vi.fn(),
    }
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    vi.mocked(api.post).mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          sessionId: 'pending',
          authUrl: 'https://www.codebuddy.cn/auth',
          expiresIn: 600,
        },
      },
    })
    const user = userEvent.setup()
    renderUI(bindings)
    if (!bindings) {
      await user.click(
        await screen.findByRole('button', { name: /WorkBuddy fixture/ })
      )
    }
    await user.click(
      screen.getByRole('button', { name: 'Start WorkBuddy authorization' })
    )
    await waitFor(() =>
      expect(popup.location.href).toBe('https://www.codebuddy.cn/auth')
    )
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/start',
      expect.objectContaining({
        proxy: '',
        proxyPolicy: { mode: 'user_pool' },
        public: false,
      }),
      expect.anything()
    )
    await user.click(
      screen.getByRole('button', { name: 'Cancel authorization' })
    )
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/api/channel/self/oauth/cancel',
        { sessionId: 'pending' },
        expect.anything()
      )
    )
    expect(
      screen.queryByRole('button', { name: 'Cancel authorization' })
    ).not.toBeInTheDocument()
    expect(popup.close).toHaveBeenCalled()
  }
)

it('cancels a pending WorkBuddy session when the template dialog closes', async () => {
  const popup = {
    opener: undefined,
    closed: false,
    location: { href: '' },
    close: vi.fn(),
  }
  vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
  vi.mocked(api.post).mockResolvedValueOnce({
    data: {
      success: true,
      data: { sessionId: 'closing', authUrl: 'https://www.codebuddy.cn/auth' },
    },
  })
  const user = userEvent.setup()
  const view = renderUI()
  await user.click(
    await screen.findByRole('button', { name: /WorkBuddy fixture/ })
  )
  await user.click(
    screen.getByRole('button', { name: 'Start WorkBuddy authorization' })
  )
  await waitFor(() =>
    expect(popup.location.href).toBe('https://www.codebuddy.cn/auth')
  )
  view.rerender(
    <QueryClientProvider client={client}>
      <FromTemplateDialog open={false} onOpenChange={() => undefined} />
    </QueryClientProvider>
  )
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/cancel',
      { sessionId: 'closing' },
      expect.anything()
    )
  )
  expect(popup.close).toHaveBeenCalled()
})

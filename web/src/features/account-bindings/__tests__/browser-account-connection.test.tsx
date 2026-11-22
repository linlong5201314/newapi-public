import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  BrowserAccountConnection,
  type WebAccountProvider,
} from '../browser-account-connection'

vi.mock('@/lib/api', () => ({ api: { post: vi.fn(), get: vi.fn() } }))
let client: QueryClient
beforeEach(() => {
  vi.resetAllMocks()
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  vi.spyOn(window, 'open').mockReturnValue(null)
  vi.mocked(api.post).mockResolvedValue({
    data: { success: true, data: { done: false, interval: 2 } },
  })
})
afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
  vi.useRealTimers()
})
function card(provider: WebAccountProvider, onComplete = vi.fn()) {
  return {
    ...render(
      <QueryClientProvider client={client}>
        <BrowserAccountConnection
          provider={provider}
          onComplete={onComplete}
          options={{ public: false, proxyPolicy: { mode: 'direct' } }}
        />
      </QueryClientProvider>
    ),
    onComplete,
  }
}
it('always keeps an official generated URL when a popup is blocked', async () => {
  const authUrl = 'https://bigmodel.cn/login?appId=zcode&state=fixture'
  vi.mocked(api.post).mockResolvedValueOnce({
    data: {
      success: true,
      data: { sessionId: 'owner-flow', authUrl, expiresIn: 300, interval: 2 },
    },
  })
  const view = card('zcode-bigmodel')
  fireEvent.click(
    screen.getByRole('button', { name: 'Generate authorization URL' })
  )
  expect(
    await screen.findByRole('link', {
      name: 'Open official account login in your browser',
    })
  ).toHaveAttribute('href', authUrl)
  expect(
    screen.getByRole('button', { name: 'Copy authorization URL' })
  ).toBeInTheDocument()
  expect(screen.getByLabelText('Generated authorization URL')).toHaveValue(
    authUrl
  )
  expect(view.onComplete).not.toHaveBeenCalled()
})
it('submits the final returned URL only to the original protected flow and clears it', async () => {
  vi.mocked(api.post).mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url.endsWith('/start')
        ? {
            sessionId: 'owner-trae',
            authUrl: 'https://www.trae.cn/authorization?login_trace_id=fixture',
            expiresIn: 600,
            automaticCallbackSupported: false,
            manualCallbackSupported: true,
          }
        : {
            done: false,
            status: 'awaiting_consent',
            requiresConsent: true,
            account: { uid: 'verified-own-user' },
            consentToken: 'fixture-proof',
          },
    },
  }))
  const view = card('trae-cn')
  fireEvent.click(screen.getByRole('checkbox'))
  fireEvent.click(
    screen.getByRole('button', { name: 'Generate authorization URL' })
  )
  const returned = await screen.findByLabelText(
    'Final authorization callback URL (fallback)'
  )
  fireEvent.change(returned, {
    target: {
      value: 'http://127.0.0.1:64532/authorize?userJwt=private-fixture',
    },
  })
  fireEvent.click(
    screen.getByRole('button', { name: 'Validate returned URL and connect' })
  )
  await screen.findByText(/verified-own-user/)
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/self/oauth/trae/url/complete',
    {
      sessionId: 'owner-trae',
      callbackUrl: 'http://127.0.0.1:64532/authorize?userJwt=private-fixture',
    },
    expect.objectContaining({ singleUseAuthorization: true })
  )
  expect(returned).toHaveValue('')
  expect(view.onComplete).not.toHaveBeenCalled()
})
it.each([
  ['trae-cn', 'https://www.trae.cn/authorization?login_trace_id=fixture'],
  ['raccoon', 'https://xiaohuanxiong.com/code/authorize?state=fixture'],
  ['zcode-bigmodel', 'https://bigmodel.cn/login?state=fixture'],
] as const)(
  'keeps real returned URL fallback for %s',
  async (provider, authUrl) => {
    vi.mocked(api.post).mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          sessionId: 'callback-flow',
          authUrl,
          expiresIn: 300,
          interval: 2,
          automaticCallbackSupported: false,
          manualCallbackSupported: true,
        },
      },
    })
    card(provider)
    if (provider === 'trae-cn') fireEvent.click(screen.getByRole('checkbox'))
    fireEvent.click(
      screen.getByRole('button', { name: 'Generate authorization URL' })
    )
    expect(
      await screen.findByLabelText(
        'Final authorization callback URL (fallback)'
      )
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Validate returned URL and connect' })
    ).toBeDisabled()
    expect(
      screen.queryByRole('button', { name: 'Check authorization status' })
    ).not.toBeInTheDocument()
  }
)
it('cancels a late browser session without exposing its link after unmount', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(api.post).mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done
      })
  )
  const view = card('cline-account')
  fireEvent.click(
    screen.getByRole('button', { name: 'Generate authorization URL' })
  )
  view.unmount()
  await act(async () =>
    resolve({
      data: {
        success: true,
        data: {
          sessionId: 'late-owner-flow',
          authUrl: 'https://authkit.cline.bot/device?user_code=fixture',
          expiresIn: 300,
        },
      },
    })
  )
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/broker/cline/cancel',
      { sessionId: 'late-owner-flow' },
      expect.anything()
    )
  )
})
it.each([
  [
    'cline-account',
    'cline',
    'https://authkit.cline.bot/device?user_code=fixture',
  ],
  [
    'qoder-cn',
    'qoder',
    'https://qoder.com.cn/device/selectAccounts?nonce=fixture',
  ],
  [
    'qoder-global',
    'qoder',
    'https://qoder.com/device/selectAccounts?nonce=fixture',
  ],
  [
    'nous-portal',
    'nous',
    'https://portal.nousresearch.com/device?user_code=fixture',
  ],
] as const)(
  'uses original-grant status checks instead of a returned callback for %s',
  async (provider, endpoint, authUrl) => {
    vi.mocked(api.post).mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          sessionId: 'device-owner-flow',
          authUrl,
          expiresIn: 300,
          interval: 2,
          completionMode: 'device_poll',
          automaticCallbackSupported: false,
          manualCallbackSupported: false,
          automaticPollingSupported: true,
        },
      },
    })
    const view = card(provider)
    fireEvent.click(
      screen.getByRole('button', { name: 'Generate authorization URL' })
    )
    expect(
      await screen.findByRole('link', {
        name: 'Open official account login in your browser',
      })
    ).toHaveAttribute('href', authUrl)
    expect(screen.getByLabelText('Generated authorization URL')).toHaveValue(
      authUrl
    )
    expect(
      screen.queryByLabelText('Final authorization callback URL (fallback)')
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', {
        name: 'Validate returned URL and connect',
      })
    ).not.toBeInTheDocument()
    expect(
      screen.getByText(
        'Complete official sign-in in the opened page. This page automatically checks the original device authorization; no callback URL is required.'
      )
    ).toBeInTheDocument()
    fireEvent.click(
      screen.getByRole('button', { name: 'Check authorization status' })
    )
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        `/api/channel/self/oauth/broker/${endpoint}/poll`,
        { sessionId: 'device-owner-flow' },
        expect.objectContaining({ singleUseAuthorization: true })
      )
    )
    expect(view.onComplete).not.toHaveBeenCalled()
  }
)
it('ignores a late device status result after cancellation', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(api.post)
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          sessionId: 'device-owner-flow',
          authUrl: 'https://authkit.cline.bot/device?user_code=fixture',
          expiresIn: 300,
          interval: 2,
          completionMode: 'device_poll',
          automaticCallbackSupported: false,
          manualCallbackSupported: false,
        },
      },
    })
    .mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done
        })
    )
  const view = card('cline-account')
  fireEvent.click(
    screen.getByRole('button', { name: 'Generate authorization URL' })
  )
  fireEvent.click(
    await screen.findByRole('button', { name: 'Check authorization status' })
  )
  fireEvent.click(screen.getByRole('button', { name: 'Cancel authorization' }))
  await act(async () =>
    resolve({
      data: { success: true, data: { done: true, channel: { id: 99 } } },
    })
  )
  expect(view.onComplete).not.toHaveBeenCalled()
  expect(screen.queryByText(/channel #99/)).not.toBeInTheDocument()
})
it('honors the increased device poll interval returned by the server', async () => {
  vi.useFakeTimers()
  vi.mocked(api.post)
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          sessionId: 'device-owner-flow',
          authUrl: 'https://authkit.cline.bot/device?user_code=fixture',
          expiresIn: 300,
          interval: 2,
          completionMode: 'device_poll',
          automaticCallbackSupported: false,
          manualCallbackSupported: false,
        },
      },
    })
    .mockResolvedValueOnce({
      data: { success: true, data: { done: false, interval: 35 } },
    })
  card('cline-account')
  await act(async () =>
    fireEvent.click(
      screen.getByRole('button', { name: 'Generate authorization URL' })
    )
  )
  await act(async () => vi.advanceTimersByTimeAsync(2000))
  expect(api.post).toHaveBeenCalledTimes(2)
  await act(async () => vi.advanceTimersByTimeAsync(34000))
  expect(api.post).toHaveBeenCalledTimes(2)
  await act(async () => vi.advanceTimersByTimeAsync(1000))
  expect(api.post).toHaveBeenCalledTimes(3)
})
it.each([
  ['cline-account', 'https://authkit.cline.bot/device?user_code=a'],
  ['raccoon', 'https://xiaohuanxiong.com/code/authorize?state=a'],
  ['qoder-cn', 'https://qoder.com.cn/device/selectAccounts?nonce=a'],
] as const)(
  'allows pinned %s authorization host',
  async (provider, authUrl) => {
    vi.mocked(api.post).mockResolvedValueOnce({
      data: {
        success: true,
        data: { sessionId: 'owner-flow', authUrl, expiresIn: 300, interval: 2 },
      },
    })
    card(provider)
    fireEvent.click(
      screen.getByRole('button', { name: 'Generate authorization URL' })
    )
    expect(
      await screen.findByRole('link', {
        name: 'Open official account login in your browser',
      })
    ).toHaveAttribute('href', authUrl)
  }
)
it.each([
  'http://bigmodel.cn/login',
  'https://bigmodel.cn.evil.invalid/login',
  'https://bigmodel.cn:443/login',
  'https://secret@bigmodel.cn/login',
  'https://bigmodel.cn/login#token',
])('rejects unsafe generated login URL %s', async (authUrl) => {
  vi.mocked(api.post).mockResolvedValueOnce({
    data: {
      success: true,
      data: {
        sessionId: 'unsafe-owner-flow',
        authUrl,
        expiresIn: 300,
        interval: 2,
      },
    },
  })
  const view = card('zcode-bigmodel')
  fireEvent.click(
    screen.getByRole('button', { name: 'Generate authorization URL' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Invalid authorization session response'
  )
  expect(
    screen.queryByRole('link', {
      name: 'Open official account login in your browser',
    })
  ).not.toBeInTheDocument()
  expect(view.onComplete).not.toHaveBeenCalled()
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/self/oauth/zcode/cancel',
    { sessionId: 'unsafe-owner-flow' },
    expect.anything()
  )
})

it.each([
  ['raccoon', 'https://xiaohuanxiong.com/code/authorize?state=fixture'],
  ['trae-cn', 'https://www.trae.cn/authorization?login_trace_id=fixture'],
] as const)(
  'does not poll %s before the manual callback or overwrite its completion error',
  async (provider, authUrl) => {
    vi.useFakeTimers()
    vi.mocked(api.post).mockImplementation(async (url) => {
      if (url.endsWith('/start')) {
        return {
          data: {
            success: true,
            data: {
              sessionId: 'manual-owner-flow',
              authUrl,
              expiresIn: 600,
              interval: 2,
              automaticCallbackSupported: false,
              manualCallbackSupported: true,
            },
          },
        }
      }
      if (url.endsWith('/complete')) {
        throw {
          response: {
            status: 502,
            data: {
              success: false,
              code: 'ACCOUNT_UPSTREAM_FAILED',
              message: 'Original provider completion failed',
            },
          },
        }
      }
      if (url.endsWith('/poll')) {
        throw {
          response: {
            status: 409,
            data: {
              success: false,
              code: 'ACCOUNT_FLOW_CONSUMED',
              message: 'Later consumed response',
            },
          },
        }
      }
      return { data: { success: true, data: { done: false } } }
    })
    const view = card(provider)
    if (provider === 'trae-cn') fireEvent.click(screen.getByRole('checkbox'))
    await act(async () =>
      fireEvent.click(
        screen.getByRole('button', { name: 'Generate authorization URL' })
      )
    )
    await act(async () => vi.advanceTimersByTimeAsync(12000))
    expect(
      vi.mocked(api.post).mock.calls.filter(([url]) => url.endsWith('/poll'))
    ).toHaveLength(0)
    const input = screen.getByLabelText(
      'Final authorization callback URL (fallback)'
    )
    fireEvent.change(input, {
      target: {
        value:
          'http://127.0.0.1:54321/auth/callback?code=private-fixture&state=fixture',
      },
    })
    await act(async () =>
      fireEvent.click(
        screen.getByRole('button', {
          name: 'Validate returned URL and connect',
        })
      )
    )
    const original = screen.getByRole('alert').textContent
    expect(original).not.toContain('Later consumed response')
    expect(
      screen.queryByLabelText('Final authorization callback URL (fallback)')
    ).not.toBeInTheDocument()
    await act(async () => vi.advanceTimersByTimeAsync(16000))
    expect(screen.getByRole('alert')).toHaveTextContent(original ?? '')
    expect(
      vi.mocked(api.post).mock.calls.filter(([url]) => url.endsWith('/poll'))
    ).toHaveLength(0)
    expect(view.onComplete).not.toHaveBeenCalled()
  }
)

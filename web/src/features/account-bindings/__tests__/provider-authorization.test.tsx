import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ProviderAuthorizationCard } from '../provider-authorization'
import type { NativeAccountProvider } from '../provider-authorization-api'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))
let client: QueryClient
const mimoSession = {
  sessionId: 'owner-mimo-session',
  authUrl: 'https://sgp.account.xiaomi.com/pass/lp?ticket=synthetic',
  expiresIn: 600,
  interval: 2,
}
const traeSession = {
  sessionId: 'owner-trae-session',
  helperUrl: '/api/channel/self/oauth/trae/bridge/helper',
  requiresLocalHelper: true,
  expiresIn: 600,
  interval: 2,
}

beforeEach(() => {
  vi.resetAllMocks()
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  vi.mocked(api.get).mockImplementation(async (_url, config) => {
    const provider = (config?.params as { provider?: string } | undefined)
      ?.provider
    return {
      data: {
        success: true,
        data: {
          provider,
          method: provider === 'kimi-work' ? 'local_import' : 'xiaomi_qr',
          browserLoginAvailable: provider === 'xiaomi-mimo',
          importAvailable: provider === 'kimi-work',
        },
      },
    }
  })
  vi.mocked(api.post).mockResolvedValue({
    data: { success: true, data: { done: false, interval: 2 } },
  })
  vi.stubGlobal(
    'URL',
    Object.assign(URL, {
      createObjectURL: vi.fn(() => 'blob:helper'),
      revokeObjectURL: vi.fn(),
    })
  )
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(
    () => undefined
  )
})
afterEach(() => {
  cleanup()
  client.clear()
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function renderCard(provider: NativeAccountProvider, onComplete = vi.fn()) {
  return {
    ...render(
      <QueryClientProvider client={client}>
        <ProviderAuthorizationCard
          provider={provider}
          onComplete={onComplete}
          options={{ public: false, proxyPolicy: { mode: 'direct' } }}
        />
      </QueryClientProvider>
    ),
    onComplete,
  }
}

it('reserves a Xiaomi login popup before network and creates a channel only after the server completes', async () => {
  const popup = {
    opener: undefined,
    closed: false,
    location: { href: '' },
    close: vi.fn(),
  }
  vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
  vi.mocked(api.post).mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url.endsWith('/start')
        ? mimoSession
        : { done: true, channel: { id: 73 } },
    },
  }))
  const view = renderCard('xiaomi-mimo')
  await userEvent.setup().click(
    await screen.findByRole('button', {
      name: 'Authorize Xiaomi MiMo account',
    })
  )
  await waitFor(() => expect(popup.location.href).toBe(mimoSession.authUrl))
  expect(popup.opener).toBeNull()
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/self/oauth/mimo/start',
    { public: false, proxyPolicy: { mode: 'direct' } },
    expect.objectContaining({
      headers: { 'X-Account-Action': 'authorize' },
      singleUseAuthorization: true,
    })
  )
  expect(view.onComplete).not.toHaveBeenCalled()
  vi.useFakeTimers()
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000)
  })
  vi.useRealTimers()
  await waitFor(() => expect(view.onComplete).toHaveBeenCalledWith(73))
  expect(popup.close).toHaveBeenCalled()
})

it.each(['account.xiaomi.com', 'ak.account.xiaomi.com'])(
  'still opens the exact supported Xiaomi login host: %s',
  async (host) => {
    const popup = {
      opener: undefined,
      closed: false,
      location: { href: '' },
      close: vi.fn(),
    }
    const authUrl = `https://${host}/longPolling/login?ticket=synthetic`
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    vi.mocked(api.post).mockResolvedValueOnce({
      data: { success: true, data: { ...mimoSession, authUrl } },
    })
    renderCard('xiaomi-mimo')
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'Authorize Xiaomi MiMo account',
      })
    )
    await waitFor(() => expect(popup.location.href).toBe(authUrl))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  }
)

it.each([
  'https://attacker.invalid/leak',
  'https://sgp.account.xiaomi.com.attacker.invalid/leak',
  'https://sgp.lp.account.xiaomi.com/longPolling/listen',
  'https://ak.account.xiaomi.com.attacker.invalid/leak',
  'https://evil.account.xiaomi.com/leak',
  'https://ak.lp.account.xiaomi.com/longPolling/listen',
  'http://ak.account.xiaomi.com/pass/lp',
  'https://ak.account.xiaomi.com:8443/pass/lp',
  'https://fixture@ak.account.xiaomi.com/pass/lp',
  'https://fixture:secret@account.xiaomi.com/pass/lp',
  'https://ak.account.xiaomi.com/pass/lp#fragment',
])('rejects an unapproved Xiaomi popup URL: %s', async (authUrl) => {
  const popup = {
    opener: undefined,
    closed: false,
    location: { href: '' },
    close: vi.fn(),
  }
  vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
  vi.mocked(api.post).mockResolvedValueOnce({
    data: { success: true, data: { ...mimoSession, authUrl } },
  })
  const view = renderCard('xiaomi-mimo')
  fireEvent.click(
    await screen.findByRole('button', { name: 'Authorize Xiaomi MiMo account' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Invalid authorization session response'
  )
  expect(popup.location.href).toBe('')
  expect(popup.close).toHaveBeenCalled()
  expect(view.onComplete).not.toHaveBeenCalled()
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/mimo/cancel',
      { sessionId: mimoSession.sessionId },
      expect.objectContaining({ headers: { 'X-Account-Action': 'authorize' } })
    )
  )
})

it('cancels a late Xiaomi session after the card unmounts without navigating the reserved popup', async () => {
  const popup = {
    opener: undefined,
    closed: false,
    location: { href: '' },
    close: vi.fn(),
  }
  vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
  let resolve!: (result: unknown) => void
  vi.mocked(api.post).mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done
      })
  )
  const view = renderCard('xiaomi-mimo')
  await userEvent.setup().click(
    await screen.findByRole('button', {
      name: 'Authorize Xiaomi MiMo account',
    })
  )
  view.unmount()
  await act(async () => resolve({ data: { success: true, data: mimoSession } }))
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/mimo/cancel',
      { sessionId: mimoSession.sessionId },
      expect.objectContaining({ headers: { 'X-Account-Action': 'authorize' } })
    )
  )
  expect(popup.location.href).toBe('')
})

it('does not create a Xiaomi session when popup is blocked and leaves an actionable error', async () => {
  vi.spyOn(window, 'open').mockReturnValue(null)
  renderCard('xiaomi-mimo')
  await userEvent.setup().click(
    await screen.findByRole('button', {
      name: 'Authorize Xiaomi MiMo account',
    })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Your browser blocked the login window. Allow popups and try again.'
  )
  expect(api.post).not.toHaveBeenCalled()
})

it('requires explicit account identity consent after the Trae helper returns before creating a channel', async () => {
  vi.mocked(api.post).mockImplementation(async (url) => {
    if (url.endsWith('/helper')) {
      return {
        data: new Blob(['# synthetic helper'], { type: 'text/x-python' }),
      }
    }
    if (url.endsWith('/consent')) {
      return {
        data: { success: true, data: { done: true, channel: { id: 91 } } },
      }
    }
    return { data: { success: true, data: traeSession } }
  })
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: {
        done: false,
        status: 'awaiting_consent',
        requiresConsent: true,
        account: { uid: 'own-uid', nickname: 'My Trae' },
        consentToken: 'one-time-consent',
      },
    },
  })
  const view = renderCard('trae-cn')
  const user = userEvent.setup()
  const start = screen.getByRole('button', {
    name: 'Download helper and start Trae domestic login',
  })
  expect(start).toBeDisabled()
  await user.click(screen.getByRole('checkbox'))
  await user.click(start)
  await screen.findByRole('button', { name: 'Cancel authorization' })
  await waitFor(() => expect(start).not.toBeInTheDocument())
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/self/oauth/trae/bridge/start',
    expect.objectContaining({ realm: 'cn', confirmRotation: true }),
    expect.objectContaining({ headers: { 'X-Account-Action': 'authorize' } })
  )
  expect(screen.getByText('python -I trae-login-helper.py')).toBeVisible()
  vi.useFakeTimers()
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000)
  })
  vi.useRealTimers()
  expect(await screen.findByText(/My Trae.*own-uid/)).toBeVisible()
  expect(view.onComplete).not.toHaveBeenCalled()
  expect(api.post).not.toHaveBeenCalledWith(
    expect.stringContaining('/consent'),
    expect.anything(),
    expect.anything()
  )
  await user.click(
    screen.getByRole('button', { name: 'Confirm and connect this account' })
  )
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/self/oauth/trae/bridge/consent',
    {
      sessionId: traeSession.sessionId,
      consentToken: 'one-time-consent',
      uid: 'own-uid',
    },
    expect.objectContaining({ singleUseAuthorization: true })
  )
  await waitFor(() => expect(view.onComplete).toHaveBeenCalledWith(91))
})

it('shows only the supported local Kimi import and posts a minimal JSON object rather than a website OAuth token', async () => {
  const user = userEvent.setup()
  vi.mocked(api.post).mockResolvedValue({
    data: { success: true, data: { done: true, channel: { id: 64 } } },
  })
  const view = renderCard('kimi-work')
  expect(
    await screen.findByRole('button', {
      name: 'Download Kimi Work export helper',
    })
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /Authorize.*Kimi/ })
  ).not.toBeInTheDocument()
  const credentials = {
    workToken: 'synthetic-work-key',
    origin: 'https://agent-gw.kimi.com/coding/v1',
  }
  const file = new File([JSON.stringify(credentials)], 'minimal-export.json', {
    type: 'application/json',
  })
  Object.defineProperty(file, 'text', {
    value: async () => JSON.stringify(credentials),
  })
  await user.upload(
    screen.getByLabelText('Kimi Work credential export JSON'),
    file
  )
  await user.click(
    screen.getByRole('button', { name: 'Import Kimi Work and create channel' })
  )
  await waitFor(() => expect(view.onComplete).toHaveBeenCalledWith(64))
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/self/kimi_work/import',
    { credentials, public: false, proxyPolicy: { mode: 'direct' } },
    expect.objectContaining({
      singleUseAuthorization: true,
      headers: { 'X-Account-Action': 'authorize' },
    })
  )
  expect(JSON.stringify(view.container.textContent)).not.toContain(
    'synthetic-work-key'
  )
})

it('does not offer unsupported server login actions when capabilities disable them', async () => {
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: {
        provider: 'xiaomi-mimo',
        method: 'xiaomi_qr',
        browserLoginAvailable: false,
        importAvailable: false,
      },
    },
  })
  renderCard('xiaomi-mimo')
  expect(
    await screen.findByRole('button', { name: 'Authorize Xiaomi MiMo account' })
  ).toBeDisabled()
  expect(screen.getByRole('status')).toHaveTextContent(
    'Xiaomi account authorization is not available on this server. No browser login URL will be invented.'
  )
})

it('keeps a protected HTTP403 business error visible and closes a reserved popup', async () => {
  const popup = {
    opener: undefined,
    closed: false,
    location: { href: '' },
    close: vi.fn(),
  }
  vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
  const config = { headers: new AxiosHeaders() }
  vi.mocked(api.post).mockRejectedValue(
    new AxiosError(
      'Request failed with status code 403',
      'ERR_BAD_REQUEST',
      config,
      null,
      {
        data: {
          success: false,
          message: 'Confirm your dashboard session before authorizing Xiaomi',
        },
        status: 403,
        statusText: 'Forbidden',
        headers: new AxiosHeaders(),
        config,
      }
    )
  )
  renderCard('xiaomi-mimo')
  fireEvent.click(
    await screen.findByRole('button', { name: 'Authorize Xiaomi MiMo account' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Confirm your dashboard session before authorizing Xiaomi'
  )
  expect(popup.close).toHaveBeenCalled()
})

it.each(['zcode-bigmodel', 'zcode-zai'] as const)(
  'connects %s through official browser consent without manual tokens',
  async (provider) => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data: {
          provider,
          method: 'zcode_cli',
          browserLoginAvailable: true,
          importAvailable: false,
        },
      },
    })
    const popup = {
      opener: undefined,
      closed: false,
      location: { href: '' },
      close: vi.fn(),
    }
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    const authUrl =
      provider === 'zcode-bigmodel'
        ? 'https://bigmodel.cn/login?appId=zcode&state=fixture'
        : 'https://chat.z.ai/api/oauth/authorize?state=fixture'
    vi.mocked(api.post).mockImplementation(async (url) => ({
      data: {
        success: true,
        data: url.endsWith('/start')
          ? {
              sessionId: 'owner-zcode-session',
              authUrl,
              expiresIn: 300,
              interval: 2,
            }
          : { done: true, channel: { id: 94 } },
      },
    }))
    const view = renderCard(provider)
    fireEvent.click(
      await screen.findByRole('button', { name: 'Authorize ZCode account' })
    )
    await waitFor(() => expect(popup.location.href).toBe(authUrl))
    expect(popup.opener).toBeNull()
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/zcode/start',
      {
        public: false,
        proxyPolicy: { mode: 'direct' },
        realm: provider === 'zcode-bigmodel' ? 'bigmodel' : 'zai',
      },
      expect.objectContaining({ singleUseAuthorization: true })
    )
    expect(view.onComplete).not.toHaveBeenCalled()
    vi.useFakeTimers()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000)
    })
    vi.useRealTimers()
    await waitFor(() => expect(view.onComplete).toHaveBeenCalledWith(94))
  }
)
it.each([
  'https://bigmodel.cn.attacker.invalid/login',
  'http://bigmodel.cn/login',
  'https://bigmodel.cn:8443/login',
  'https://user:secret@bigmodel.cn/login',
  'https://bigmodel.cn/login#token',
  'https://bigmodel.cn/not-login',
  'https://chat.z.ai/api/oauth/authorize/extra',
])('rejects unapproved ZCode authorization destination %s', async (authUrl) => {
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: {
        method: 'zcode_cli',
        browserLoginAvailable: true,
        importAvailable: false,
      },
    },
  })
  const popup = {
    opener: undefined,
    closed: false,
    location: { href: '' },
    close: vi.fn(),
  }
  vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
  vi.mocked(api.post).mockResolvedValue({
    data: {
      success: true,
      data: {
        sessionId: 'owner-zcode-session',
        authUrl,
        expiresIn: 300,
        interval: 2,
      },
    },
  })
  renderCard('zcode-bigmodel')
  fireEvent.click(
    await screen.findByRole('button', { name: 'Authorize ZCode account' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Invalid authorization session response'
  )
  expect(popup.location.href).toBe('')
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/zcode/cancel',
      { sessionId: 'owner-zcode-session' },
      expect.anything()
    )
  )
})
it('does not create a ZCode flow when the login popup is blocked', async () => {
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: {
        method: 'zcode_cli',
        browserLoginAvailable: true,
        importAvailable: false,
      },
    },
  })
  vi.spyOn(window, 'open').mockReturnValue(null)
  renderCard('zcode-bigmodel')
  fireEvent.click(
    await screen.findByRole('button', { name: 'Authorize ZCode account' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Your browser blocked the login window'
  )
  expect(api.post).not.toHaveBeenCalled()
})

it('offers an explicit official login link in the Android client without enabling arbitrary popup windows', async () => {
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(
    'Android DeepSeekNewAPIClient/1.1.0'
  )
  const open = vi.spyOn(window, 'open').mockReturnValue(null)
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: {
        method: 'zcode_cli',
        browserLoginAvailable: true,
        importAvailable: false,
      },
    },
  })
  const authUrl = 'https://bigmodel.cn/login?appId=zcode&state=android-fixture'
  vi.mocked(api.post).mockResolvedValue({
    data: {
      success: true,
      data: {
        sessionId: 'android-own-flow',
        authUrl,
        expiresIn: 300,
        interval: 2,
      },
    },
  })
  const view = renderCard('zcode-bigmodel')
  fireEvent.click(
    await screen.findByRole('button', { name: 'Authorize ZCode account' })
  )
  const link = await screen.findByRole('link', {
    name: 'Open official account login in your browser',
  })
  expect(link).toHaveAttribute('href', authUrl)
  expect(link).toHaveAttribute('target', '_self')
  expect(link).toHaveAttribute('rel', 'noreferrer')
  expect(open).not.toHaveBeenCalled()
  expect(view.onComplete).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Cancel authorization' }))
  await waitFor(() =>
    expect(
      screen.queryByRole('link', {
        name: 'Open official account login in your browser',
      })
    ).not.toBeInTheDocument()
  )
})
it('does not expose unapproved Android login destinations', async () => {
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(
    'Android DeepSeekNewAPIClient/1.1.0'
  )
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: {
        method: 'zcode_cli',
        browserLoginAvailable: true,
        importAvailable: false,
      },
    },
  })
  vi.mocked(api.post).mockResolvedValue({
    data: {
      success: true,
      data: {
        sessionId: 'android-own-flow',
        authUrl: 'https://attacker.invalid/login',
        expiresIn: 300,
        interval: 2,
      },
    },
  })
  renderCard('zcode-bigmodel')
  fireEvent.click(
    await screen.findByRole('button', { name: 'Authorize ZCode account' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Invalid authorization session response'
  )
  expect(
    screen.queryByRole('link', {
      name: 'Open official account login in your browser',
    })
  ).not.toBeInTheDocument()
})

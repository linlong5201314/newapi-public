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
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { FreePiAuthorizationCard } from '../freepi-authorization'

const { translate } = vi.hoisted(() => ({ translate: (key: string) => key }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translate }) }))
vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))
vi.mock('@/lib/handle-server-error', () => ({ handleServerError: vi.fn() }))

const session = {
  state: 'test-private-state',
  userCode: 'ABCD-EFGH',
  verificationUri: 'https://github.com/login/device',
  interval: 5,
  expiresIn: 600,
  flow: 'device_authorization',
  method: 'device',
}

function renderCard(onComplete?: (id: number) => void) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  render(
    <QueryClientProvider client={client}>
      <FreePiAuthorizationCard onComplete={onComplete} />
    </QueryClientProvider>
  )
}

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.resetAllMocks()
})

function forbidden(message: string, code?: string): AxiosError {
  const config = { headers: new AxiosHeaders() }
  return new AxiosError(
    'Request failed with status code 403',
    'ERR_BAD_REQUEST',
    config,
    null,
    {
      data: { success: false, message, ...(code ? { code } : {}) },
      status: 403,
      statusText: 'Forbidden',
      headers: new AxiosHeaders(),
      config,
    }
  )
}

describe('FreePi browser Connect authorization', () => {
  it('starts the official browser connection with the confirmation code and automatic polling', async () => {
    const user = userEvent.setup()
    const onComplete = vi.fn()
    vi.mocked(api.post)
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: {
            state: 'opaque-connect-state',
            userCode: 'WXYZ-1234',
            verificationUri:
              'https://freepi.ai/connect?session=upstream-reference',
            interval: 5,
            expiresIn: 600,
            flow: 'browser_connect',
            method: 'connect',
          },
        },
      })
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: { status: 'done', done: true, channel: { id: 52 } },
        },
      })
    renderCard(onComplete)
    await user.click(
      screen.getByRole('button', { name: 'Start browser authorization' })
    )
    expect(await screen.findByText('WXYZ-1234')).toBeVisible()
    expect(
      screen.getByRole('link', { name: 'Open FreePi to sign in and Connect' })
    ).toHaveAttribute(
      'href',
      'https://freepi.ai/connect?session=upstream-reference'
    )
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/freepi/start',
      expect.objectContaining({ method: 'connect' }),
      expect.any(Object)
    )
    vi.useFakeTimers()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    vi.useRealTimers()
    expect(
      await screen.findByText(
        'FreePi authorization completed. Channel #{{id}} uses your account'
      )
    ).toBeVisible()
    await waitFor(() => expect(onComplete).toHaveBeenCalledWith(52))
  })

  it('rejects an untrusted Connect URL and offers the device-code fallback', async () => {
    const user = userEvent.setup()
    vi.mocked(api.post).mockResolvedValue({
      data: {
        success: true,
        data: {
          ...session,
          method: 'connect',
          flow: 'browser_connect',
          verificationUri: 'https://attacker.invalid/connect',
        },
      },
    })
    renderCard()
    await user.click(
      screen.getByRole('button', { name: 'Start browser authorization' })
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'FreePi device authorization response could not be verified'
    )
    await user.click(
      screen.getByRole('button', { name: 'Use GitHub device code instead' })
    )
    expect(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    ).toBeVisible()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('FreePi device authorization', () => {
  it('keeps the actual HTTP 403 business reason visible without requesting verification for unrelated failures', async () => {
    vi.mocked(api.post).mockRejectedValue(
      forbidden('FreePi authorization requires a same-origin browser request')
    )
    renderCard()
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'GitHub device code' }))
    await userEvent.setup().click(
      screen.getByRole('button', {
        name: 'Start GitHub device authorization',
      })
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'FreePi authorization requires a same-origin browser request'
    )
    expect(screen.getByRole('alert')).not.toHaveTextContent(
      'Request failed with status code 403'
    )
    expect(api.get).not.toHaveBeenCalled()
  })

  it('requests scoped verification only for FREEPI_REAUTH_REQUIRED and retries once with the proof header', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data: {
          scope: 'upstream.account.bind',
          methods: [{ method: 'password', available: true }],
          oauth_providers: [],
          password_encryption_enabled: false,
        },
      },
    })
    vi.mocked(api.post).mockImplementation(async (url) => {
      if (url === '/api/verify') {
        return {
          data: {
            success: true,
            data: {
              proof_token: 'one-use-freepi-proof',
              scope: 'upstream.account.bind',
              method: 'password',
              expires_at: Math.floor(Date.now() / 1000) + 120,
            },
          },
        }
      }
      const starts = vi
        .mocked(api.post)
        .mock.calls.filter(
          ([path]) => path === '/api/channel/self/oauth/freepi/start'
        )
      if (starts.length === 1) {
        throw forbidden(
          'Confirm your identity before linking FreePi',
          'FREEPI_REAUTH_REQUIRED'
        )
      }
      return { data: { success: true, data: session } }
    })
    const user = userEvent.setup()
    renderCard()
    await user.click(screen.getByRole('button', { name: 'GitHub device code' }))
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    await user.type(
      await screen.findByLabelText('Password', { selector: 'input' }),
      'synthetic-password'
    )
    await user.click(screen.getByRole('button', { name: 'Verify' }))
    expect(await screen.findByText(session.userCode)).toBeVisible()
    expect(api.get).toHaveBeenCalledWith(
      '/api/verify/methods',
      expect.objectContaining({ params: { scope: 'upstream.account.bind' } })
    )
    expect(api.post).toHaveBeenCalledWith(
      '/api/verify',
      expect.objectContaining({
        scope: 'upstream.account.bind',
        context: { provider: 'freepi' },
      }),
      expect.anything()
    )
    expect(api.post).toHaveBeenLastCalledWith(
      '/api/channel/self/oauth/freepi/start',
      {
        displayName: '',
        public: false,
        confirmPublic: false,
        method: 'device',
      },
      expect.objectContaining({
        singleUseAuthorization: true,
        headers: {
          'X-FreePi-Action': 'authorize',
          'X-Security-Proof': 'one-use-freepi-proof',
        },
      })
    )
    expect(api.post).toHaveBeenCalledTimes(3)
  })

  it('does not spend a FreePi proof or start GitHub authorization when security verification is cancelled', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data: {
          scope: 'upstream.account.bind',
          methods: [{ method: 'password', available: true }],
          oauth_providers: [],
          password_encryption_enabled: false,
        },
      },
    })
    vi.mocked(api.post).mockRejectedValue(
      forbidden(
        'Confirm your identity before linking FreePi',
        'FREEPI_REAUTH_REQUIRED'
      )
    )
    const user = userEvent.setup()
    renderCard()
    await user.click(screen.getByRole('button', { name: 'GitHub device code' }))
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    await screen.findByLabelText('Password', { selector: 'input' })
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(
      await screen.findByRole('button', {
        name: 'Start GitHub device authorization',
      })
    ).toBeEnabled()
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(screen.queryByText(session.userCode)).not.toBeInTheDocument()
  })
  it('uses an upstream complete verification link while keeping the manual code fallback', async () => {
    const user = userEvent.setup()
    const complete = 'https://github.com/login/device?user_code=ABCD-EFGH'
    vi.mocked(api.post).mockResolvedValue({
      data: {
        success: true,
        data: { ...session, verificationUriComplete: complete },
      },
    })
    renderCard()
    await user.click(screen.getByRole('button', { name: 'GitHub device code' }))
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    expect(await screen.findByText(session.userCode)).toBeVisible()
    expect(
      screen.getByRole('link', { name: 'Open GitHub to confirm authorization' })
    ).toHaveAttribute('href', complete)
  })

  it('ignores an untrusted complete link and preserves the official manual device page', async () => {
    const user = userEvent.setup()
    vi.mocked(api.post).mockResolvedValue({
      data: {
        success: true,
        data: {
          ...session,
          verificationUriComplete:
            'https://attacker.invalid/?user_code=ABCD-EFGH',
        },
      },
    })
    renderCard()
    await user.click(screen.getByRole('button', { name: 'GitHub device code' }))
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    expect(
      await screen.findByRole('link', {
        name: 'Open GitHub and enter this device code',
      })
    ).toHaveAttribute('href', session.verificationUri)
  })

  it('starts private by default and shows an official GitHub code without reporting a ready channel', async () => {
    const user = userEvent.setup()
    vi.mocked(api.post).mockResolvedValue({
      data: { success: true, data: session },
    })
    renderCard()
    expect(
      screen.getByRole('checkbox', {
        name: 'I explicitly allow other users to call my FreePi channel and consume my account credits',
      })
    ).not.toBeChecked()
    await user.click(screen.getByRole('button', { name: 'GitHub device code' }))
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    expect(await screen.findByText(session.userCode)).toBeVisible()
    expect(
      screen.getByRole('link', {
        name: 'Open GitHub and enter this device code',
      })
    ).toHaveAttribute('href', 'https://github.com/login/device')
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/freepi/start',
      {
        displayName: '',
        public: false,
        confirmPublic: false,
        method: 'device',
      },
      expect.objectContaining({
        singleUseAuthorization: true,
        headers: { 'X-FreePi-Action': 'authorize' },
      })
    )
    expect(
      screen.queryByText(
        'FreePi authorization completed. Channel #{{id}} uses your account'
      )
    ).not.toBeInTheDocument()
  })

  it('does not silently accept upstream consent and only completes after explicit acceptance and token redemption', async () => {
    const user = userEvent.setup()
    const onComplete = vi.fn()
    vi.mocked(api.post)
      .mockResolvedValueOnce({ data: { success: true, data: session } })
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: { status: 'consent_required', consentVersion: '4' },
        },
      })
      .mockResolvedValueOnce({
        data: { success: true, data: { status: 'pending', interval: 5 } },
      })
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: { status: 'done', done: true, channel: { id: 42 } },
        },
      })
    renderCard(onComplete)
    await user.click(screen.getByRole('button', { name: 'GitHub device code' }))
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    await screen.findByText(session.userCode)
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    vi.useRealTimers()
    const accept = await screen.findByRole('button', {
      name: 'Accept FreePi consent and continue',
    })
    expect(accept).toBeDisabled()
    expect(api.post).toHaveBeenCalledTimes(2)
    await user.click(
      screen.getByRole('checkbox', {
        name: 'I have reviewed and accept FreePi consent version {{version}}',
      })
    )
    await user.click(accept)
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/freepi/consent',
      { state: session.state, accepted: true, consentVersion: '4' },
      expect.any(Object)
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('button', {
          name: 'Accept FreePi consent and continue',
        })
      ).not.toBeInTheDocument()
    )
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    vi.useRealTimers()
    expect(
      await screen.findByText(
        'FreePi authorization completed. Channel #{{id}} uses your account'
      )
    ).toBeVisible()
    await waitFor(() => expect(onComplete).toHaveBeenCalledWith(42))
  })

  it('keeps public sharing off unless the user selects it and sends explicit confirmation', async () => {
    const user = userEvent.setup()
    vi.mocked(api.post).mockResolvedValue({
      data: { success: true, data: session },
    })
    renderCard()
    await user.click(screen.getByRole('button', { name: 'GitHub device code' }))
    await user.click(
      screen.getByRole('checkbox', {
        name: 'I explicitly allow other users to call my FreePi channel and consume my account credits',
      })
    )
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    await screen.findByText(session.userCode)
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/freepi/start',
      expect.objectContaining({ public: true, confirmPublic: true }),
      expect.any(Object)
    )
  })

  it('cancels server state without reporting an authorized channel', async () => {
    const user = userEvent.setup()
    vi.mocked(api.post)
      .mockResolvedValueOnce({ data: { success: true, data: session } })
      .mockResolvedValueOnce({
        data: { success: true, data: { status: 'cancelled' } },
      })
    renderCard()
    await user.click(screen.getByRole('button', { name: 'GitHub device code' }))
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    await screen.findByText(session.userCode)
    await user.click(
      screen.getByRole('button', { name: 'Cancel authorization' })
    )
    await waitFor(() =>
      expect(screen.queryByText(session.userCode)).not.toBeInTheDocument()
    )
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/freepi/cancel',
      { state: session.state },
      expect.any(Object)
    )
    expect(
      screen.queryByText(
        'FreePi authorization completed. Channel #{{id}} uses your account'
      )
    ).not.toBeInTheDocument()
  })

  it('expires while awaiting consent and asks the user to start again', async () => {
    vi.useFakeTimers()
    vi.mocked(api.post)
      .mockResolvedValueOnce({
        data: { success: true, data: { ...session, expiresIn: 10 } },
      })
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: { status: 'consent_required', consentVersion: '4' },
        },
      })
    renderCard()
    fireEvent.click(screen.getByRole('button', { name: 'GitHub device code' }))
    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', {
          name: 'Start GitHub device authorization',
        })
      )
      await vi.advanceTimersByTimeAsync(0)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(50)
    })
    expect(screen.getByText(session.userCode)).toBeVisible()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(50)
    })
    expect(
      screen.getByRole('button', { name: 'Accept FreePi consent and continue' })
    ).toBeDisabled()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000)
    })
    expect(screen.getByRole('alert')).toHaveTextContent(
      'FreePi authorization expired. Start again'
    )
    expect(
      screen.queryByRole('button', {
        name: 'Accept FreePi consent and continue',
      })
    ).not.toBeInTheDocument()
    vi.useRealTimers()
  })

  it('rejects a non-GitHub verification URL instead of navigating or reporting success', async () => {
    const user = userEvent.setup()
    vi.mocked(api.post).mockResolvedValue({
      data: {
        success: true,
        data: { ...session, verificationUri: 'https://attacker.invalid' },
      },
    })
    renderCard()
    await user.click(screen.getByRole('button', { name: 'GitHub device code' }))
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'FreePi device authorization response could not be verified'
    )
    expect(
      screen.queryByRole('link', {
        name: 'Open GitHub and enter this device code',
      })
    ).not.toBeInTheDocument()
  })

  it('shows an API failure rather than treating an unsuccessful start as an authorized account', async () => {
    const user = userEvent.setup()
    vi.mocked(api.post).mockResolvedValue({
      data: { success: false, message: 'Login required' },
    })
    renderCard()
    await user.click(screen.getByRole('button', { name: 'GitHub device code' }))
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    expect(await screen.findByRole('alert')).toHaveTextContent('Login required')
    expect(screen.queryByText(session.userCode)).not.toBeInTheDocument()
  })
})

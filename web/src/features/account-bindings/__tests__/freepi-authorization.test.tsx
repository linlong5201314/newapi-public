import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { act } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { FreePiAuthorizationCard } from '../freepi-authorization'

const { translate } = vi.hoisted(() => ({ translate: (key: string) => key }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translate }) }))
vi.mock('@/lib/api', () => ({ api: { post: vi.fn() } }))
vi.mock('@/lib/handle-server-error', () => ({ handleServerError: vi.fn() }))

const session = {
  state: 'test-private-state',
  userCode: 'ABCD-EFGH',
  verificationUri: 'https://github.com/login/device',
  interval: 5,
  expiresIn: 600,
  flow: 'device_authorization',
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

describe('FreePi device authorization', () => {
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
      { displayName: '', public: false, confirmPublic: false },
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
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    await screen.findByText(session.userCode)
    vi.useFakeTimers()
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
    expect(onComplete).toHaveBeenCalledWith(42)
  })

  it('keeps public sharing off unless the user selects it and sends explicit confirmation', async () => {
    const user = userEvent.setup()
    vi.mocked(api.post).mockResolvedValue({
      data: { success: true, data: session },
    })
    renderCard()
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
    const user = userEvent.setup()
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
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    await screen.findByText(session.userCode)
    vi.useFakeTimers()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    vi.useRealTimers()
    await screen.findByRole('button', {
      name: 'Accept FreePi consent and continue',
    })
    vi.useFakeTimers()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000)
    })
    vi.useRealTimers()
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'FreePi authorization expired. Start again'
    )
    expect(
      screen.queryByRole('button', {
        name: 'Accept FreePi consent and continue',
      })
    ).not.toBeInTheDocument()
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
    await user.click(
      screen.getByRole('button', { name: 'Start GitHub device authorization' })
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'FreePi authorization could not be started. Try again later'
    )
    expect(screen.queryByText(session.userCode)).not.toBeInTheDocument()
  })
})

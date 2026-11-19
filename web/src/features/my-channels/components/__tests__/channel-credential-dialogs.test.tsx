import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { TraeImportDialog } from '../channel-credential-dialogs'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))
let client: QueryClient
const session = {
  sessionId: 'drawer-owner-session',
  expiresIn: 600,
  interval: 2,
  requiresLocalHelper: true,
}

beforeEach(() => {
  vi.resetAllMocks()
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  vi.stubGlobal(
    'URL',
    Object.assign(URL, {
      createObjectURL: vi.fn(() => 'blob:drawer-helper'),
      revokeObjectURL: vi.fn(),
    })
  )
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(
    () => undefined
  )
  vi.mocked(api.post).mockImplementation(async (url) => {
    if (url.endsWith('/helper')) {
      return {
        data: new Blob(['# synthetic helper'], { type: 'text/x-python' }),
      }
    }
    if (url.endsWith('/consent')) {
      return {
        data: { success: true, data: { done: true, channel: { id: 88 } } },
      }
    }
    return { data: { success: true, data: session } }
  })
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: {
        done: false,
        status: 'awaiting_consent',
        requiresConsent: true,
        consentToken: 'drawer-one-time-consent',
        account: { uid: 'drawer-account', nickname: 'My account' },
      },
    },
  })
})
afterEach(() => {
  cleanup()
  client.clear()
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it('uses the server-owned Trae helper flow and closes with onChannelCreated only after verified identity consent', async () => {
  const onChannelCreated = vi.fn()
  const onOpenChange = vi.fn()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <TraeImportDialog
        open
        onOpenChange={onOpenChange}
        onChannelCreated={onChannelCreated}
      />
    </QueryClientProvider>
  )
  const start = await screen.findByRole('button', {
    name: 'Download helper and start Trae domestic login',
  })
  expect(start).toBeDisabled()
  expect(
    screen.queryByRole('textbox', { name: 'Callback URL' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Import and preview' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('checkbox'))
  await user.click(start)
  await waitFor(() => expect(start).not.toBeInTheDocument())
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/self/oauth/trae/bridge/start',
    expect.objectContaining({ realm: 'cn', confirmRotation: true }),
    expect.objectContaining({
      headers: { 'X-Account-Action': 'authorize' },
      singleUseAuthorization: true,
    })
  )
  vi.useFakeTimers()
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000)
  })
  vi.useRealTimers()
  expect(await screen.findByText(/My account.*drawer-account/)).toBeVisible()
  expect(onChannelCreated).not.toHaveBeenCalled()
  await user.click(
    screen.getByRole('button', { name: 'Confirm and connect this account' })
  )
  await waitFor(() => expect(onChannelCreated).toHaveBeenCalledOnce())
  expect(onOpenChange).toHaveBeenCalledWith(false)
  expect(api.post).not.toHaveBeenCalledWith(
    '/api/channel/self/import_trae',
    expect.anything(),
    expect.anything()
  )
})

it('cancels the pending server Trae helper session when the legacy dialog closes without a created-channel callback', async () => {
  const onChannelCreated = vi.fn()
  const onOpenChange = vi.fn()
  const user = userEvent.setup()
  const view = render(
    <QueryClientProvider client={client}>
      <TraeImportDialog
        open
        onOpenChange={onOpenChange}
        onChannelCreated={onChannelCreated}
      />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('checkbox'))
  await user.click(
    screen.getByRole('button', {
      name: 'Download helper and start Trae domestic login',
    })
  )
  await waitFor(() =>
    expect(
      screen.queryByRole('button', {
        name: 'Download helper and start Trae domestic login',
      })
    ).not.toBeInTheDocument()
  )
  view.rerender(
    <QueryClientProvider client={client}>
      <TraeImportDialog
        open={false}
        onOpenChange={onOpenChange}
        onChannelCreated={onChannelCreated}
      />
    </QueryClientProvider>
  )
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/oauth/trae/bridge/cancel',
      { sessionId: session.sessionId },
      expect.objectContaining({ headers: { 'X-Account-Action': 'authorize' } })
    )
  )
  expect(onChannelCreated).not.toHaveBeenCalled()
})

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
import { useAuthStore } from '@/stores/auth-store'

import { PublicPoolAdminChannels } from '../components/admin-channels'

const savedChannel = {
  id: 7,
  name: 'Platform fixture',
  type: 1,
  models: 'model-a,model-b',
  group: 'default',
  status: 2,
  owner_id: 0,
  tier: 'free',
  priority: 0,
  weight: 0,
  created_time: 0,
  response_time: 0,
}

function mount(role = 100) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'fixture', role })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <PublicPoolAdminChannels />
    </QueryClientProvider>
  )
}

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

describe('Platform free channel administration', () => {
  it.each([1, 10])(
    'when role is %s does not expose or query platform management',
    (role) => {
      const get = vi.spyOn(api, 'get')
      mount(role)
      expect(
        screen.queryByRole('button', { name: 'Add public free channel' })
      ).not.toBeInTheDocument()
      expect(get).not.toHaveBeenCalled()
    }
  )

  it('when root searches lists disabled public free channels through scoped API and exposes accessible actions', async () => {
    const user = userEvent.setup()
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [savedChannel], total: 1 } },
    })
    mount()
    const row = await screen.findByRole('article', { name: 'Platform fixture' })
    expect(within(row).getByText('Disabled')).toBeVisible()
    expect(within(row).getByRole('button', { name: 'Edit' })).toBeEnabled()
    expect(within(row).getByRole('button', { name: 'Copy' })).toBeEnabled()
    expect(within(row).getByRole('button', { name: 'Delete' })).toBeEnabled()
    await user.type(
      screen.getByRole('searchbox', { name: 'Search public free channels' }),
      'model-a'
    )
    await user.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith(
        '/api/public_pool/admin/channels',
        expect.objectContaining({
          params: expect.objectContaining({ keyword: 'model-a', page: 1 }),
          signal: expect.any(AbortSignal),
        })
      )
    )
  })

  it('when adding a channel requires valid inputs and sends no owner tier or enable override', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true, data: { id: 8 } } })
    mount()
    await user.click(
      screen.getByRole('button', { name: 'Add public free channel' })
    )
    const dialog = screen.getByRole('dialog', {
      name: 'Add public free channel',
    })
    expect(
      within(dialog).getByText(
        'New and copied channels stay disabled until you test and explicitly enable them.'
      )
    ).toBeVisible()
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(post).not.toHaveBeenCalled()
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Channel name' }),
      'New fixture'
    )
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Model IDs' }),
      'model-a'
    )
    await user.type(
      within(dialog).getByLabelText('Provider key (write-only)'),
      'fixture-new-key'
    )
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/api/public_pool/admin/channels',
        expect.objectContaining({
          name: 'New fixture',
          models: 'model-a',
          key: 'fixture-new-key',
          group: 'default',
        })
      )
    )
    const body = post.mock.calls[0][1] as Record<string, unknown>
    expect(body).not.toHaveProperty('owner_id')
    expect(body).not.toHaveProperty('tier')
    expect(body).not.toHaveProperty('status')
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })

  it('when editing preserves saved credential unless a write-only replacement is supplied', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [savedChannel], total: 1 } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true, data: { id: 7 } } })
    mount()
    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    const dialog = screen.getByRole('dialog', {
      name: 'Edit public free channel',
    })
    expect(
      within(dialog).getByLabelText('Provider key (write-only)')
    ).toHaveValue('')
    expect(within(dialog).getByLabelText('Base URL (write-only)')).toHaveValue(
      ''
    )
    const name = within(dialog).getByRole('textbox', { name: 'Channel name' })
    await user.clear(name)
    await user.type(name, 'Renamed fixture')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        '/api/public_pool/admin/channels/7',
        expect.objectContaining({ name: 'Renamed fixture' })
      )
    )
    expect(put.mock.calls[0][1]).not.toHaveProperty('key')
    expect(put.mock.calls[0][1]).not.toHaveProperty('base_url')
  })

  it('when copying requires confirmation and copies server-side without loading provider credentials', async () => {
    const user = userEvent.setup()
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [savedChannel], total: 1 } },
    })
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true, data: { id: 9 } } })
    mount()
    await user.click(await screen.findByRole('button', { name: 'Copy' }))
    expect(post).not.toHaveBeenCalled()
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Copy',
      })
    )
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/api/public_pool/admin/channels/7/copy'
      )
    )
    expect(
      get.mock.calls.every(([url]) => url === '/api/public_pool/admin/channels')
    ).toBe(true)
  })

  it('when deletion is cancelled does not delete and when confirmed deletes only the selected channel', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [savedChannel], total: 1 } },
    })
    const remove = vi
      .spyOn(api, 'delete')
      .mockResolvedValue({ data: { success: true } })
    mount()
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Cancel',
      })
    )
    expect(remove).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      })
    )
    await waitFor(() =>
      expect(remove).toHaveBeenCalledWith('/api/public_pool/admin/channels/7')
    )
  })

  it('when a mutation fails keeps the editor open and shows failure instead of success', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [savedChannel], total: 1 } },
    })
    vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: false, message: 'Fixture save denied' },
    })
    mount()
    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    await user.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Save' })
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Fixture save denied'
    )
    expect(screen.getByRole('dialog')).toBeVisible()
  })

  it('when query fails exposes retry and no fabricated empty list', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('Fixture unavailable'))
    mount()
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
    expect(
      screen.queryByText('No public free channels')
    ).not.toBeInTheDocument()
  })
})

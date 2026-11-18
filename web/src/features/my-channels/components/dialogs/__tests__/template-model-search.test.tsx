import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { FromTemplateDialog } from '../from-template-dialog'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))
let client: QueryClient
const templates = [
  {
    id: 'search-fixture',
    name: 'Search fixture',
    models: ['deepseek-chat', 'qwen-coder'],
    keyRequired: false,
    authType: 'none',
  },
  {
    id: 'openrouter-free',
    name: 'OpenRouter fixture',
    models: ['default-free'],
    keyRequired: false,
    authType: 'none',
  },
  {
    id: 'opencode-free',
    name: 'OpenCode fixture',
    models: ['opencode-free-model'],
    keyRequired: false,
    authType: 'none',
  },
]
beforeEach(() => {
  vi.resetAllMocks()
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/channel/self/free_templates') {
      return { data: { success: true, data: templates } }
    }
    if (url === '/api/proxy-pool/endpoints') {
      return { data: { success: true, data: [] } }
    }
    if (url === '/api/proxy-pool/runtime') {
      return { data: { success: true, data: null } }
    }
    throw new Error(`Unexpected GET: ${url}`)
  })
  vi.mocked(api.post).mockResolvedValue({
    data: { success: true, data: { id: 11 } },
  })
})
afterEach(() => client.clear())
function renderDialog() {
  return render(
    <QueryClientProvider client={client}>
      <FromTemplateDialog open onOpenChange={() => undefined} />
    </QueryClientProvider>
  )
}

it('filters model names without dropping hidden selections and preserves public subset rules', async () => {
  const user = userEvent.setup()
  renderDialog()
  await user.click(
    await screen.findByRole('button', { name: /Search fixture/ })
  )
  const search = screen.getByRole('searchbox', { name: 'Search models' })
  await user.type(search, '  DEEPSEEK  ')
  expect(screen.getByText('deepseek-chat')).toBeVisible()
  expect(screen.queryByText('qwen-coder')).not.toBeInTheDocument()
  await user.clear(search)
  expect(
    screen.getByRole('checkbox', { name: 'Enable qwen-coder in my channels' })
  ).toBeChecked()
  await user.click(
    screen.getByRole('checkbox', {
      name: 'Publish qwen-coder to the public pool',
    })
  )
  await user.click(
    screen.getByRole('checkbox', { name: 'Enable qwen-coder in my channels' })
  )
  expect(
    screen.getByRole('checkbox', {
      name: 'Publish qwen-coder to the public pool',
    })
  ).not.toBeChecked()
  await user.type(search, 'not-a-model')
  expect(screen.getByText('No model found.')).toBeVisible()
})

it('sends the complete discovered catalog while only selected models are enabled', async () => {
  const user = userEvent.setup()
  renderDialog()
  await user.click(
    await screen.findByRole('button', { name: /Search fixture/ })
  )
  vi.mocked(api.post).mockResolvedValueOnce({
    data: {
      success: true,
      data: {
        models: ['new-model', 'deepseek-chat'],
        probed_live: true,
        latency_ms: 2,
      },
    },
  })
  await user.click(
    screen.getByRole('button', {
      name: /Fetch models|获取模型（优先测速快节点）/,
    })
  )
  expect(await screen.findByText('new-model')).toBeVisible()
  expect(
    screen.getByRole('checkbox', { name: 'Enable new-model in my channels' })
  ).not.toBeChecked()
  await user.click(
    screen.getByRole('checkbox', { name: 'Enable qwen-coder in my channels' })
  )
  await user.click(
    screen.getByRole('button', { name: /Create channel|创建渠道/ })
  )
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/from_template',
      expect.objectContaining({
        models: ['deepseek-chat'],
        all_models: ['deepseek-chat', 'qwen-coder', 'new-model'],
        public_models: [],
      }),
      expect.any(Object)
    )
  )
})

it('ignores a pending automatic model response after switching to another template', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(api.post).mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done
      })
  )
  const user = userEvent.setup()
  renderDialog()
  await user.click(
    await screen.findByRole('button', { name: /OpenRouter fixture/ })
  )
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/probe_template_models',
      expect.any(Object),
      expect.any(Object)
    )
  )
  await user.click(screen.getByRole('button', { name: 'Back' }))
  await user.click(
    await screen.findByRole('button', { name: /Search fixture/ })
  )
  await act(async () =>
    resolve({ data: { success: true, data: { models: ['stale-model'] } } })
  )
  expect(screen.queryByText('stale-model')).not.toBeInTheDocument()
  expect(screen.getByText('qwen-coder')).toBeVisible()
})

it('disables channel creation when the catalog is restricted to the OpenCode client', async () => {
  vi.mocked(api.post).mockResolvedValueOnce({
    data: {
      success: true,
      data: {
        models: ['opencode-free-model'],
        callable: false,
        call_contract: 'opencode_client_only',
      },
    },
  })
  const user = userEvent.setup()
  renderDialog()
  await user.click(
    await screen.findByRole('button', { name: /OpenCode fixture/ })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'OpenCode free models are only callable from the OpenCode client, not through this gateway.'
  )
  expect(
    screen.getByRole('button', { name: /Create channel|创建渠道/ })
  ).toBeDisabled()
})

it('keeps manual model choices when the interface language changes', async () => {
  const user = userEvent.setup()
  renderDialog()
  await user.click(
    await screen.findByRole('button', { name: /Search fixture/ })
  )
  await user.click(
    screen.getByRole('checkbox', { name: 'Enable qwen-coder in my channels' })
  )
  try {
    await act(async () => {
      await i18next.changeLanguage('fr')
    })
    expect(
      screen.getByRole('checkbox', { name: 'Enable qwen-coder in my channels' })
    ).not.toBeChecked()
  } finally {
    await act(async () => {
      await i18next.changeLanguage('en')
    })
  }
})

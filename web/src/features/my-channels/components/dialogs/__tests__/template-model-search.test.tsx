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
    id: 'zcode-bigmodel',
    name: 'ZCode account fixture',
    models: [],
    channelType: 72,
    authMode: 'oauth',
    hiddenEndpoint: true,
  },
  {
    id: 'xiaohuanxiong',
    name: 'Raccoon auth fixture',
    models: [],
    authMode: 'bridge',
  },
  {
    id: 'ds2api',
    name: 'DeepSeek auth fixture',
    models: [],
    authMode: 'account',
  },
  {
    id: 'xiaoyiwork',
    name: 'Xiaoyi Work fixture',
    models: [],
    authMode: 'magpie',
    authType: 'key',
  },
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
    if (url === '/api/channel/self/account_capabilities') {
      return {
        data: {
          success: true,
          data: {
            method: 'zcode_cli',
            browserLoginAvailable: true,
            importAvailable: false,
          },
        },
      }
    }
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
afterEach(() => {
  client.clear()
  sessionStorage.removeItem('new-api:xiaoyiwork:authorization-draft')
})
// The dialog intentionally defaults to no models selected. Tests that assert
// creation payloads first opt in explicitly via the visible bulk action.
async function selectAllMyModels(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    screen.getByRole('button', { name: 'Select all for my channels' })
  )
}

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
  ).not.toBeChecked()
  await selectAllMyModels(user)
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
  await selectAllMyModels(user)
  await user.click(
    screen.getByRole('checkbox', { name: 'Enable qwen-coder in my channels' })
  )
  await user.click(
    screen.getByRole('checkbox', { name: 'Enable new-model in my channels' })
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

it('hides OpenCode client-only sources from new channel creation without deleting legacy data', async () => {
  renderDialog()
  expect(
    await screen.findByRole('button', { name: /Search fixture/ })
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /OpenCode fixture/ })
  ).not.toBeInTheDocument()
  expect(api.post).not.toHaveBeenCalled()
})

it('creates an IP free source privately using an owned outbound strategy', async () => {
  const user = userEvent.setup()
  renderDialog()
  await user.click(
    await screen.findByRole('button', { name: /Search fixture/ })
  )
  expect(
    screen.getAllByRole('option').map((option) => option.getAttribute('value'))
  ).not.toContain('public_pool')
  expect(
    screen.getAllByRole('option').map((option) => option.getAttribute('value'))
  ).not.toContain('direct')
  expect(
    screen.getByText(
      'Platform IP-based free models are available to everyone through Public models. Your sources stay private unless you choose to share them.'
    )
  ).toBeVisible()
  await selectAllMyModels(user)
  await user.click(
    screen.getByRole('button', { name: /Create channel|创建渠道/ })
  )
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/channel/self/from_template',
      expect.objectContaining({
        proxy_policy: { mode: 'user_pool' },
        visibility: 0,
        public_models: [],
      }),
      expect.any(Object)
    )
  )
})

it('keeps manual model choices when the interface language changes', async () => {
  const user = userEvent.setup()
  renderDialog()
  await user.click(
    await screen.findByRole('button', { name: /Search fixture/ })
  )
  await selectAllMyModels(user)
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

it('offers generated Raccoon browser authorization without helper downloads', async () => {
  const user = userEvent.setup()
  renderDialog()
  await user.click(
    await screen.findByRole('button', { name: /Raccoon auth fixture/ })
  )
  expect(
    screen.getByRole('button', { name: 'Generate authorization URL' })
  ).toBeInTheDocument()
  expect(screen.queryByText(/F12/)).not.toBeInTheDocument()
  expect(api.post).not.toHaveBeenCalled()
})

it('starts native Huawei authorization without requiring a Magpie deployment', async () => {
  const user = userEvent.setup()
  const open = vi.spyOn(window, 'open').mockReturnValue(null)
  vi.mocked(api.post).mockResolvedValueOnce({
    data: { success: true, data: { sessionId: 'fixture-session', authUrl: 'https://oauth-login.cloud.huawei.com/oauth2/v3/authorize?code_challenge=fixture', callbackPort: 17355, callbackPath: '/auth/callback' } },
  })
  renderDialog()
  await user.click(await screen.findByRole('button', { name: 'Account authorization' }))
  expect(screen.queryByRole('button', { name: /DeepSeek auth fixture/ })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: /Xiaoyi Work fixture/ }))
  expect(screen.queryByText(/Magpie gateway address|Magpie gateway key|Magpie browser link/)).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Authorize Huawei account' }))
  expect(await screen.findByRole('link', { name: 'Open Huawei authorization' })).toHaveAttribute('href', expect.stringContaining('code_challenge=fixture'))
  await user.type(screen.getByLabelText('Local callback URL (optional if you have the code)'), 'http://127.0.0.1:17355/auth/callback?code=fixture-code&state=fixture-session')
  expect(screen.getByRole('button', { name: 'Connect and sync models' })).toBeEnabled()
  expect(open).toHaveBeenCalledWith(expect.stringContaining('oauth-login.cloud.huawei.com'), '_blank', 'noopener,noreferrer')
  open.mockRestore()
})

it('enables only account models and requires confirmation to share a selected model', async () => {
  const user = userEvent.setup()
  vi.mocked(api.post)
    .mockResolvedValueOnce({ data: { success: true, data: { sessionId: 'fixture-session', authUrl: 'https://oauth-login.cloud.huawei.com/oauth2/v3/authorize?state=fixture', callbackPort: 17355, callbackPath: '/auth/callback' } } })
    .mockResolvedValueOnce({ data: { success: true, data: { channelId: 11, models: ['model-one', 'model-two'], unavailable_models: [], public_models: [] } } })
    .mockResolvedValueOnce({ data: { success: true, data: { channelId: 11, models: ['model-one'], public_models: ['model-one'] } } })
  renderDialog()
  await user.click(await screen.findByRole('button', { name: /Xiaoyi Work fixture/ }))
  await user.click(screen.getByRole('button', { name: 'Authorize Huawei account' }))
  await user.type(screen.getByLabelText('Authorization code'), 'fixture-code')
  await user.click(screen.getByRole('button', { name: 'Connect and sync models' }))
  expect(await screen.findByText('model-two')).toBeVisible()
  await user.click(screen.getByRole('checkbox', { name: 'Enable model-two in my channels' }))
  expect(screen.getByRole('checkbox', { name: 'Enable model-one in my channels' })).toBeChecked()
  expect(screen.getByRole('button', { name: 'Save selection and connect' })).toBeEnabled()
  await user.click(screen.getByRole('checkbox', { name: 'Publish model-one to the public pool' }))
  expect(screen.getByRole('checkbox', { name: 'Enable model-one in my channels' })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: 'Enable model-two in my channels' })).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Save selection and connect' })).toBeDisabled()
  await user.click(screen.getByRole('checkbox', { name: 'I explicitly allow other users to use this channel and spend my account credits' }))
  await user.click(screen.getByRole('button', { name: 'Save selection and connect' }))
  await waitFor(() => expect(api.post).toHaveBeenLastCalledWith('/api/channel/self/visibility', expect.objectContaining({ channelId: 11, models: ['model-one'], publicModels: ['model-one'], share_confirmed: true }), expect.objectContaining({ headers: { 'X-Account-Action': 'authorize' } })))
})

it('creates the native channel from a verified callback and shows grey previews', async () => {
  const user = userEvent.setup()
  vi.mocked(api.post)
    .mockResolvedValueOnce({ data: { success: true, data: { sessionId: 'fixture-session', authUrl: 'https://oauth-login.cloud.huawei.com/oauth2/v3/authorize?state=fixture', callbackPort: 17355, callbackPath: '/auth/callback' } } })
    .mockResolvedValueOnce({ data: { success: true, data: { channelId: 11, models: ['model-one'], unavailable_models: ['model-preview'], public_models: [] } } })
  renderDialog()
  await user.click(await screen.findByRole('button', { name: /Xiaoyi Work fixture/ }))
  await user.click(screen.getByRole('button', { name: 'Authorize Huawei account' }))
  await user.type(screen.getByLabelText('Local callback URL (optional if you have the code)'), 'http://127.0.0.1:17355/auth/callback?code=fixture-code&state=fixture-session')
  await user.click(screen.getByRole('button', { name: 'Connect and sync models' }))
  expect(await screen.findByText('model-preview')).toBeVisible()
  expect(screen.getByText('1 models are available to this account.')).toBeVisible()
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/api/channel/self/oauth/xiaoyiwork/complete', expect.objectContaining({ sessionId: 'fixture-session', callbackUrl: expect.stringContaining('code=fixture-code') }), expect.objectContaining({ headers: { 'X-Account-Action': 'authorize' } })))
  const initialRequest = vi.mocked(api.post).mock.calls.find(([url]) => url === '/api/channel/self/oauth/xiaoyiwork/complete')?.[1] as Record<string, unknown>
  expect(initialRequest).not.toHaveProperty('models')
})

it('restores an unfinished Huawei authorization when the dialog is reopened', async () => {
  sessionStorage.setItem('new-api:xiaoyiwork:authorization-draft', JSON.stringify({
    authorization: { sessionId: 'restored-session', authUrl: 'https://oauth-login.cloud.huawei.com/oauth2/v3/authorize?state=restored', callbackPort: 17355, callbackPath: '/auth/callback' },
    callbackUrl: '', code: 'restored-code', name: '', channelId: null,
  }))
  const user = userEvent.setup()
  vi.mocked(api.post).mockResolvedValueOnce({ data: { success: true, data: { channelId: 12, models: ['restored-model'], unavailable_models: [], public_models: [] } } })
  renderDialog()
  await user.click(await screen.findByRole('button', { name: /Xiaoyi Work fixture/ }))
  expect(screen.getByLabelText('Authorization code')).toHaveValue('restored-code')
  await user.click(screen.getByRole('button', { name: 'Connect and sync models' }))
  expect(await screen.findByText('restored-model')).toBeVisible()
  expect(screen.getByText('Xiaoyi Work channel created in My channels. You can close this window or adjust models below.')).toBeVisible()
  expect(screen.queryByRole('dialog')).toBeInTheDocument()
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/api/channel/self/oauth/xiaoyiwork/complete', expect.objectContaining({ sessionId: 'restored-session', code: 'restored-code' }), expect.anything()))
})

it('uses ZCode native consent without the generic token submit or unauthenticated model fetch', async () => {
  const user = userEvent.setup()
  renderDialog()
  await user.click(
    await screen.findByRole('button', { name: /ZCode account fixture/ })
  )
  expect(
    await screen.findByRole('button', { name: 'Generate authorization URL' })
  ).toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: '提交 Token 授权并开通渠道' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: '获取模型（优先测速快节点）' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('checkbox', {
      name: 'I explicitly allow other users to use this channel and spend my account credits',
    })
  ).not.toBeChecked()
})

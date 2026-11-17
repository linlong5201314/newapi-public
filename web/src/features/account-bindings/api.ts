import { api, type ApiRequestConfig } from '@/lib/api'

export type AccountBinding = {
  id: number
  provider: string
  display_name?: string
  account_mask?: string
  account_count?: number
  public?: number
  status?: string
  channel_id?: number
  service_ref?: string
  last_error?: string
  created_at?: string
}

const cfg = (config: ApiRequestConfig = {}): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
})

export async function listBindings(): Promise<AccountBinding[]> {
  const res = await api.get<{
    success: boolean
    data?: { items?: AccountBinding[] }
  }>('/api/channel/self/bindings', cfg())
  return res.data?.data?.items ?? []
}

export async function createBinding(payload: {
  provider: string
  accounts: string
  displayName?: string
  public?: boolean
  proxy?: string
}): Promise<{ ok: boolean; message?: string }> {
  const res = await api.post<{ success: boolean; message?: string }>(
    '/api/channel/self/bindings',
    payload,
    cfg()
  )
  return { ok: Boolean(res.data?.success), message: res.data?.message }
}

export async function deleteBinding(id: number): Promise<void> {
  await api.delete(`/api/channel/self/bindings/${id}`, cfg())
}

export async function setBindingPublic(
  id: number,
  value: boolean
): Promise<void> {
  await api.post(
    `/api/channel/self/bindings/${id}/public`,
    { public: value },
    cfg()
  )
}

export type OAuthSession = {
  sessionId: string
  authUrl: string
  realm?: string
  callbackUrl?: string
  expiresIn?: number
}

// 小浣熊（办公小浣熊 / Raccoon Work）走浏览器授权回跳：
// 点「开始授权」→ 打开 xiaohuanxiong.com 授权页 → 登录后浏览器跳回我们的
// /api/channel/self/oauth/raccoon/callback?code=… → 服务端自动换票建渠道。
export async function startRaccoonOAuth(): Promise<OAuthSession | null> {
  const res = await api.post<{
    success: boolean
    data?: OAuthSession
    message?: string
  }>('/api/channel/self/oauth/raccoon/start', {}, cfg())
  return res.data?.success ? (res.data.data ?? null) : null
}

export async function pollRaccoonOAuth(
  sessionId: string
): Promise<OAuthPollResult> {
  const res = await api.get<{
    success: boolean
    data?: OAuthPollResult & { models?: string[] }
    message?: string
  }>(
    `/api/channel/self/oauth/raccoon/poll?sessionId=${encodeURIComponent(sessionId)}&mode=create`,
    cfg()
  )
  if (!res.data?.success) {
    return { done: false, message: res.data?.message }
  }
  return res.data.data ?? { done: false }
}

// WorkBuddy 采用设备授权流：先拿 authUrl 让用户在自己浏览器里完成 Google/邮箱登录，
// 再由前端轮询，登录成功后由后端直接建渠道（mode=create）。
export async function startWorkbuddyOAuth(
  realm: 'cn' | 'global'
): Promise<OAuthSession | null> {
  const res = await api.post<{
    success: boolean
    data?: OAuthSession
    message?: string
  }>('/api/channel/self/oauth/start', { type: 'workbuddy', realm }, cfg())
  return res.data?.success ? (res.data.data ?? null) : null
}

export type OAuthPollResult = {
  done: boolean
  channel?: { id?: number; name?: string }
  credential?: string
  message?: string
}

export async function pollWorkbuddyOAuth(
  sessionId: string
): Promise<OAuthPollResult> {
  const res = await api.get<{
    success: boolean
    data?: OAuthPollResult
    message?: string
  }>(
    `/api/channel/self/oauth/poll?sessionId=${encodeURIComponent(sessionId)}&mode=create`,
    cfg()
  )
  if (!res.data?.success) {
    return { done: false, message: res.data?.message }
  }
  return res.data.data ?? { done: false }
}

// Trae：粘贴登录后的回调链接（含 code）或 refreshToken，后端换取凭据并建渠道。
export async function importTrae(payload: {
  callbackUrl?: string
  refreshToken?: string
  uid?: string
}): Promise<{ ok: boolean; message?: string; notice?: string }> {
  const res = await api.post<{
    success: boolean
    data?: { notice?: string; channel?: { id?: number } }
    message?: string
  }>('/api/channel/self/import_trae', { ...payload, mode: 'create' }, cfg())
  return {
    ok: Boolean(res.data?.success),
    message: res.data?.message,
    notice: res.data?.data?.notice,
  }
}

// DS2API（DeepSeek 网页端）：生成专属授权链接 → 复制/点击去浏览器登录确认 → 自动回传并开通渠道。
export async function startDs2ApiOAuth(): Promise<OAuthSession | null> {
  const res = await api.post<{
    success: boolean
    data?: OAuthSession
    message?: string
  }>('/api/channel/self/oauth/ds2api/start', {}, cfg())
  return res.data?.success ? (res.data.data ?? null) : null
}

export async function pollDs2ApiOAuth(
  sessionId: string
): Promise<OAuthPollResult> {
  const res = await api.get<{
    success: boolean
    data?: OAuthPollResult & { models?: string[] }
    message?: string
  }>(
    `/api/channel/self/oauth/ds2api/poll?sessionId=${encodeURIComponent(sessionId)}`,
    cfg()
  )
  if (!res.data?.success) {
    return { done: false, message: res.data?.message }
  }
  return res.data.data ?? { done: false }
}

export type MagpiePlugin = {
  id: string
  name: string
  repo: string
  desc: string
  authMode: string
  stars: number
  url: string
  install: string
}

export async function listMagpiePlugins(q?: string): Promise<{
  items: MagpiePlugin[]
  total: number
  upstream?: string
}> {
  const res = await api.get<{
    success: boolean
    data?: { items?: MagpiePlugin[]; total?: number; upstream?: string }
  }>(`/api/channel/self/magpie_plugins${q ? `?q=${encodeURIComponent(q)}` : ''}`, cfg())
  return {
    items: res.data?.data?.items ?? [],
    total: res.data?.data?.total ?? 0,
    upstream: res.data?.data?.upstream,
  }
}

import { api, type ApiRequestConfig } from '@/lib/api'
import {
  createServerError,
  requireServerSuccess,
} from '@/lib/server-error-message'

import type { BindingProxyPolicy } from './api'

export type NativeAccountProvider =
  | 'kimi-work'
  | 'xiaomi-mimo'
  | 'trae-cn'
  | 'zcode-bigmodel'
  | 'zcode-zai'
  | 'cline-account'
  | 'raccoon'
  | 'qoder-cn'
  | 'qoder-global'
  | 'nous-portal'
export type BrowserAccountProvider = Exclude<NativeAccountProvider, 'kimi-work'>
export type ProviderAuthorizationOptions = {
  displayName?: string
  public?: boolean
  proxy?: string
  proxyPolicy?: BindingProxyPolicy
}
export type ProviderCapabilities = {
  provider: string
  method:
    | 'local_import'
    | 'xiaomi_qr'
    | 'zcode_cli'
    | 'device_poll'
    | 'callback_url'
  browserLoginAvailable: boolean
  importAvailable: boolean
  officialUrl: string
  helperUrl?: string
}
export type NativeAuthorizationSession = {
  sessionId: string
  authUrl?: string
  helperUrl?: string
  requiresLocalHelper?: boolean
  expiresIn: number
  interval?: number
  automaticCallbackSupported?: boolean
  manualCallbackSupported?: boolean
  expectedCallback?: string
  completionMode?: string
}
export type NativeAuthorizationResult = {
  done: boolean
  status?: 'pending' | 'awaiting_consent' | 'done'
  requiresConsent?: boolean
  account?: { uid: string; nickname?: string }
  consentToken?: string
  channel?: { id?: number }
  interval?: number
}

export async function confirmTraeBridgeAccount(
  sessionId: string,
  consentToken: string,
  uid: string
): Promise<NativeAuthorizationResult> {
  const response = await api.post<Envelope<NativeAuthorizationResult>>(
    '/api/channel/self/oauth/trae/bridge/consent',
    { sessionId, consentToken, uid },
    accountConfig(true)
  )
  return requireServerSuccess(response.data).data
}
type Envelope<T> = { success: boolean; data: T; message?: string }

const accountConfig = (singleUseAuthorization = false): ApiRequestConfig => ({
  skipBusinessError: true,
  skipErrorHandler: true,
  singleUseAuthorization,
  headers: { 'X-Account-Action': 'authorize' },
})

export async function getProviderCapabilities(
  provider: NativeAccountProvider,
  signal?: AbortSignal
): Promise<ProviderCapabilities> {
  const response = await api.get<Envelope<ProviderCapabilities>>(
    '/api/channel/self/account_capabilities',
    {
      ...accountConfig(),
      params: { provider },
      signal,
      disableDuplicate: true,
    }
  )
  return requireServerSuccess(response.data).data
}

function browserAccountEndpoint(provider: BrowserAccountProvider) {
  if (provider === 'trae-cn') return 'trae/url'
  if (provider === 'raccoon') return 'raccoon/url'
  if (provider === 'cline-account') return 'broker/cline'
  if (provider === 'qoder-cn' || provider === 'qoder-global')
    return 'broker/qoder'
  if (provider === 'nous-portal') return 'broker/nous'
  if (provider === 'zcode-bigmodel' || provider === 'zcode-zai') return 'zcode'
  return 'mimo'
}

export async function startBrowserAuthorization(
  provider: BrowserAccountProvider,
  options: ProviderAuthorizationOptions
): Promise<NativeAuthorizationSession> {
  const endpoint = browserAccountEndpoint(provider)
  let payload: ProviderAuthorizationOptions & {
    realm?: string
    confirmRotation?: boolean
  } = options
  if (provider === 'trae-cn') {
    payload = { ...options, realm: 'cn', confirmRotation: true }
  }
  if (provider === 'zcode-bigmodel' || provider === 'zcode-zai') {
    payload = {
      ...options,
      realm: provider === 'zcode-bigmodel' ? 'bigmodel' : 'zai',
    }
  }
  if (provider === 'qoder-cn' || provider === 'qoder-global')
    payload = { ...options, realm: provider === 'qoder-cn' ? 'cn' : 'global' }
  const response = await api.post<Envelope<NativeAuthorizationSession>>(
    `/api/channel/self/oauth/${endpoint}/start`,
    payload,
    accountConfig(true)
  )
  return requireServerSuccess(response.data).data
}

export async function pollBrowserAuthorization(
  provider: BrowserAccountProvider,
  sessionId: string
): Promise<NativeAuthorizationResult> {
  const response = await api.post<Envelope<NativeAuthorizationResult>>(
    `/api/channel/self/oauth/${browserAccountEndpoint(provider)}/poll`,
    { sessionId },
    accountConfig(true)
  )
  return requireServerSuccess(response.data).data
}

export async function cancelBrowserAuthorization(
  provider: BrowserAccountProvider,
  sessionId: string
): Promise<void> {
  const endpoint = browserAccountEndpoint(provider)
  const response = await api.post<Envelope<unknown>>(
    `/api/channel/self/oauth/${endpoint}/cancel`,
    { sessionId },
    accountConfig(true)
  )
  requireServerSuccess(response.data)
}

export async function startNativeAuthorization(
  provider: BrowserAccountProvider,
  options: ProviderAuthorizationOptions
): Promise<NativeAuthorizationSession> {
  if (provider !== 'trae-cn')
    return startBrowserAuthorization(provider, options)
  const response = await api.post<Envelope<NativeAuthorizationSession>>(
    '/api/channel/self/oauth/trae/bridge/start',
    { ...options, realm: 'cn', confirmRotation: true },
    accountConfig(true)
  )
  return requireServerSuccess(response.data).data
}
export async function pollNativeAuthorization(
  provider: BrowserAccountProvider,
  sessionId: string
): Promise<NativeAuthorizationResult> {
  if (provider !== 'trae-cn')
    return pollBrowserAuthorization(provider, sessionId)
  const response = await api.get<Envelope<NativeAuthorizationResult>>(
    '/api/channel/self/oauth/trae/bridge/poll',
    { ...accountConfig(), params: { sessionId }, disableDuplicate: true }
  )
  return requireServerSuccess(response.data).data
}
export async function cancelNativeAuthorization(
  provider: BrowserAccountProvider,
  sessionId: string
): Promise<void> {
  if (provider !== 'trae-cn')
    return cancelBrowserAuthorization(provider, sessionId)
  const response = await api.post<Envelope<unknown>>(
    '/api/channel/self/oauth/trae/bridge/cancel',
    { sessionId },
    accountConfig(true)
  )
  requireServerSuccess(response.data)
}

export async function confirmBrowserAccount(
  provider: BrowserAccountProvider,
  sessionId: string,
  consentToken: string,
  uid: string
): Promise<NativeAuthorizationResult> {
  if (provider === 'trae-cn')
    return confirmTraeBridgeAccount(sessionId, consentToken, uid)
  const response = await api.post<Envelope<NativeAuthorizationResult>>(
    '/api/channel/self/oauth/raccoon/url/consent',
    { sessionId, consentToken, uid },
    accountConfig(true)
  )
  return requireServerSuccess(response.data).data
}

export async function completeNativeAuthorization(
  provider: BrowserAccountProvider,
  sessionId: string,
  callbackUrl: string
): Promise<NativeAuthorizationResult> {
  const response = await api.post<Envelope<NativeAuthorizationResult>>(
    `/api/channel/self/oauth/${browserAccountEndpoint(provider)}/complete`,
    { sessionId, callbackUrl },
    accountConfig(true)
  )
  return requireServerSuccess(response.data).data
}

export async function importKimiWorkCredentials(
  credentials: Record<string, unknown>,
  options: ProviderAuthorizationOptions
): Promise<NativeAuthorizationResult> {
  const response = await api.post<Envelope<NativeAuthorizationResult>>(
    '/api/channel/self/kimi_work/import',
    { credentials, ...options },
    accountConfig(true)
  )
  return requireServerSuccess(response.data).data
}

// Download protected helpers through the authenticated client, never put dashboard
// tokens in a URL or hand them to an external website. A JSON error is not a script.
export async function downloadProviderHelper(
  provider: 'kimi-work' | 'trae-cn',
  sessionId?: string
): Promise<void> {
  const config = { ...accountConfig(true), responseType: 'blob' as const }
  let response
  if (provider === 'trae-cn') {
    response = await api.post<Blob>(
      '/api/channel/self/oauth/trae/bridge/helper',
      { sessionId },
      config
    )
  } else {
    response = await api.get<Blob>(
      '/api/channel/self/kimi_work/import_helper',
      config
    )
  }
  const blob = response.data
  if (!(blob instanceof Blob) || blob.size === 0) {
    throw new Error('Failed to download the authorization helper')
  }
  if (blob.type.includes('json')) {
    throw createServerError(JSON.parse(await blob.text()))
  }
  if (blob.type.includes('html')) {
    throw new Error('Failed to download the authorization helper')
  }
  const href = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = href
  anchor.download =
    provider === 'trae-cn' ? 'trae-login-helper.py' : 'kimi-work-export.ps1'
  anchor.click()
  setTimeout(() => URL.revokeObjectURL(href), 0)
}

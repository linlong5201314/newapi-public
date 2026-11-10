/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { getGroups as getUserGroups } from '@/features/users/api'
import { api, type ApiRequestConfig } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  AddChannelRequest,
  Channel,
  ChannelTestResponse,
  CopyChannelParams,
  CopyChannelResponse,
  FetchModelsResponse,
  GetChannelResponse,
  GetChannelsParams,
  GetChannelsResponse,
  SearchChannelsParams,
  SearchChannelsResponse,
} from './types'

/**
 * [M1-FE] Self-service channel API.
 *
 * All endpoints live under `/api/channel/self` and are scoped to the
 * currently authenticated user (backend enforces owner_id = current user).
 * Platform-level operations (batch/tag/multi-key management/fix abilities)
 * are intentionally absent.
 */
const SELF_BASE = '/api/channel/self'

const channelActionConfig = (
  config: ApiRequestConfig = {}
): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
})

// ============================================================================
// Base Channel CRUD Operations (self-scoped)
// ============================================================================

/**
 * Get paginated list of the current user's channels
 */
export async function getChannels(
  params: GetChannelsParams = {}
): Promise<GetChannelsResponse> {
  const res = await api.get(`${SELF_BASE}/`, { params })
  return res.data
}

/**
 * Search the current user's channels with filters.
 * NOTE: backend contract extension — GET /api/channel/self/search
 * (same parameters as the admin search endpoint, owner-scoped).
 */
export async function searchChannels(
  params: SearchChannelsParams
): Promise<SearchChannelsResponse> {
  const res = await api.get(`${SELF_BASE}/search`, { params })
  return res.data
}

/**
 * Get single channel by ID (must belong to the current user)
 */
export async function getChannel(id: number): Promise<GetChannelResponse> {
  const res = await api.get(`${SELF_BASE}/${id}`)
  return res.data
}

/**
 * Create new channel(s) owned by the current user.
 * Supports single, batch, and multi-key modes.
 */
export async function createChannel(
  data: AddChannelRequest
): Promise<{ success: boolean; message?: string }> {
  const res = await api.post(`${SELF_BASE}/`, data, channelActionConfig())
  return res.data
}

/**
 * Update existing channel (must belong to the current user)
 */
export async function updateChannel(
  id: number,
  data: Partial<Channel>
): Promise<{ success: boolean; message?: string; data?: Channel }> {
  const res = await api.put(
    `${SELF_BASE}/`,
    { id, ...data },
    channelActionConfig()
  )
  return res.data
}

/**
 * Update channel enabled/disabled status.
 * Routed through the update endpoint — the self-service contract exposes
 * no dedicated status endpoint.
 */
export async function updateChannelStatus(
  id: number,
  status: number
): Promise<{ success: boolean; message?: string; data?: boolean }> {
  const res = await api.put(
    `${SELF_BASE}/`,
    { id, status },
    channelActionConfig()
  )
  return res.data
}

/**
 * Delete single channel (must belong to the current user)
 */
export async function deleteChannel(
  id: number
): Promise<{ success: boolean; message?: string }> {
  const res = await api.delete(`${SELF_BASE}/${id}`, channelActionConfig())
  return res.data
}

// ============================================================================
// Channel Operations (self-scoped)
// ============================================================================

/**
 * Test channel connectivity.
 * NOTE: the self-service contract uses POST /api/channel/self/test with a
 * JSON body instead of the admin GET /api/channel/test/:id.
 */
export async function testChannel(
  id: number,
  params?: { model?: string; endpoint_type?: string; stream?: boolean }
): Promise<ChannelTestResponse> {
  const res = await api.post(
    `${SELF_BASE}/test`,
    { id, ...params },
    channelActionConfig()
  )
  return res.data
}

/**
 * Fetch available models from upstream provider
 */
export async function fetchUpstreamModels(
  id: number
): Promise<FetchModelsResponse> {
  const res = await api.get(
    `${SELF_BASE}/fetch_models/${id}`,
    channelActionConfig()
  )
  return res.data
}

/**
 * Copy/clone a channel owned by the current user
 */
export async function copyChannel(
  id: number,
  params: CopyChannelParams = {}
): Promise<CopyChannelResponse> {
  const res = await api.post(
    `${SELF_BASE}/copy/${id}`,
    null,
    channelActionConfig({ params })
  )
  return res.data
}

/**
 * Get channel key (requires 2FA verification).
 * NOTE: backend contract extension — POST /api/channel/self/:id/key
 * (owner-scoped variant of the admin key disclosure endpoint).
 */
export async function getChannelKey(
  id: number,
  proofToken: string,
  signal?: AbortSignal
): Promise<{ success: boolean; message?: string; data?: { key: string } }> {
  const res = await api.post(
    `${SELF_BASE}/${id}/key`,
    undefined,
    channelActionConfig({
      headers: { 'X-Security-Proof': proofToken },
      signal,
    })
  )
  return res.data
}

/**
 * Fetch models from the current unsaved channel form configuration.
 * NOTE: backend contract extension — POST /api/channel/self/fetch_models
 * (owner-scoped variant of the admin unsaved-config fetch endpoint).
 */
export async function fetchModels(data: {
  base_url: string
  type: number
  key?: string
  channel_id?: number
  advanced_custom?: string
  header_override?: string
  proxy?: string
}): Promise<FetchModelsResponse> {
  const res = await api.post(
    `${SELF_BASE}/fetch_models`,
    data,
    channelActionConfig()
  )
  return res.data
}

/**
 * Refresh Codex OAuth credential for a channel owned by the current user.
 * NOTE: backend contract extension — POST /api/channel/self/:id/codex/refresh
 * (owner-scoped variant of the admin codex refresh endpoint).
 */
export async function refreshCodexCredential(channelId: number): Promise<{
  success: boolean
  message?: string
  data?: {
    expires_at?: string
    last_refresh?: string
    account_id?: string
    email?: string
    channel_id?: number
    channel_type?: number
    channel_name?: string
  }
}> {
  const res = await api.post(
    `${SELF_BASE}/${channelId}/codex/refresh`,
    {},
    channelActionConfig()
  )
  return res.data
}

// ============================================================================
// [M3-FE] Account-credential channel login/import (WorkBuddy / Trae)
//
// Contract: docs/M3-2api与签到中心设计.md §2 — owner-scoped endpoints under
// /api/channel/self. The backend builds these in parallel, so the consumers
// normalize loose wire shapes.
// ============================================================================

export type ChannelOAuthStartResponse = {
  success: boolean
  message?: string
  data?: {
    sessionId?: string
    session_id?: string
    authUrl?: string
    auth_url?: string
    expiresIn?: number
    expires_in?: number
  }
}

export type ChannelOAuthPollResponse = {
  success: boolean
  message?: string
  data?: unknown
}

export type ImportTraeCredentialResponse = {
  success: boolean
  message?: string
  data?: unknown
}

/**
 * Start the WorkBuddy device-authorization flow.
 * Returns {sessionId, authUrl, expiresIn}; poll with the session id.
 */
export async function startChannelOAuth(
  type: 'workbuddy'
): Promise<ChannelOAuthStartResponse> {
  const res = await api.post(
    `${SELF_BASE}/oauth/start`,
    { type },
    channelActionConfig()
  )
  return res.data
}

/**
 * Poll an OAuth session once. Terminal states surface credentials (or a
 * server-created channel); pending states carry a status hint.
 */
export async function pollChannelOAuth(
  sessionId: string
): Promise<ChannelOAuthPollResponse> {
  const res = await api.get(
    `${SELF_BASE}/oauth/poll`,
    channelActionConfig({ params: { sessionId } })
  )
  return res.data
}

/**
 * Import Trae account credentials from a localhost callback URL, or from a
 * manually supplied refreshToken + uid pair. Returns a redacted credential
 * preview for confirmation.
 */
export async function importTraeCredential(payload: {
  callbackUrl?: string
  refreshToken?: string
  uid?: string
}): Promise<ImportTraeCredentialResponse> {
  const res = await api.post(
    `${SELF_BASE}/import_trae`,
    payload,
    channelActionConfig()
  )
  return res.data
}

// ============================================================================
// Form metadata helpers
//
// The endpoints below back the create/edit drawer. Several of them live
// under the admin channel/group routers upstream; for self-service users
// every helper here is fail-soft, so a 403 simply degrades optional hints
// (default base URLs, model suggestions, prefill groups, task plugin
// metadata) instead of breaking the drawer.
// ============================================================================

/**
 * Optional per-type default base URL hints. Fail-soft: returns null when the
 * endpoint is not reachable for the current user.
 */
export async function getChannelDefaultBaseURLs(): Promise<Partial<
  Record<number, string>
> | null> {
  try {
    const response = await api.get<{
      success: boolean
      data: Partial<Record<number, string>>
    }>('/api/channel/default_base_urls')
    return requireServerSuccess(response.data).data
  } catch {
    return null
  }
}

/**
 * All available models (used for suggestions in the models editor).
 * Fail-soft: returns an empty list when not permitted.
 */
export async function getAllModels(): Promise<{
  success: boolean
  message?: string
  data?: Array<{ id: string; [key: string]: unknown }>
}> {
  try {
    const res = await api.get('/api/channel/models')
    return res.data
  } catch {
    return { success: true, data: [] }
  }
}

export type TaskPluginOption = {
  sortPriority?: number
  website?: string
  key: string
  name: string
  description?: Record<string, string> | null
  icon?: string
  hasIcon?: boolean
  baseUrl?: string
  models: string[]
  channelTypes?: number[] | null
  upstreams?: string[] | null
}

/**
 * Task plugin options. Fail-soft: returns an empty list when not permitted.
 */
export async function getTaskPluginOptions(): Promise<TaskPluginOption[]> {
  try {
    const response = await api.get<{
      success: boolean
      data: TaskPluginOption[]
    }>('/api/task_plugin_options')
    return requireServerSuccess(response.data).data
  } catch {
    return []
  }
}

/**
 * Get all available groups.
 * Uses the user-accessible groups endpoint (GET /api/user/self/groups),
 * re-exported from the users API for convenience.
 */
export const getGroups = getUserGroups

/**
 * Get prefill groups for quick model selection.
 * Fail-soft: returns an empty list when not permitted.
 */
export async function getPrefillGroups(type: 'model' | 'group' = 'model'): Promise<{
  success: boolean
  message?: string
  data?: Array<{ id: number; name: string; items: string | string[] }>
}> {
  try {
    const res = await api.get('/api/prefill_group', { params: { type } })
    return res.data
  } catch {
    return { success: true, data: [] }
  }
}

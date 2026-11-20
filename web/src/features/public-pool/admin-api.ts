import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export interface PublicAdminChannel {
  id: number
  name: string
  type: number
  status: number
  models: string
  group: string
  priority: number | null
  weight: number | null
  owner_id: number
  tier: string
  created_time: number
  response_time: number
}

export interface PublicAdminChannelInput {
  name: string
  type: number
  models: string
  group: string
  priority: number
  weight: number
  key?: string
  base_url?: string
}

interface PublicAdminChannelsResponse {
  success: boolean
  message?: string
  data: { items: PublicAdminChannel[]; total: number }
}

export const PUBLIC_ADMIN_CHANNELS_KEY = [
  'public-pool',
  'admin-channels',
] as const
const endpoint = '/api/public_pool/admin/channels'

export async function getPublicAdminChannels(
  params: { keyword: string; page: number; page_size: number },
  signal?: AbortSignal
): Promise<PublicAdminChannelsResponse['data']> {
  const response = await api.get<PublicAdminChannelsResponse>(endpoint, {
    params,
    signal,
  })
  return requireServerSuccess(response.data).data
}

export async function savePublicAdminChannel(
  input: PublicAdminChannelInput,
  id?: number
): Promise<void> {
  const response = id
    ? await api.put(`${endpoint}/${id}`, input)
    : await api.post(endpoint, input)
  requireServerSuccess(response.data)
}

export async function copyPublicAdminChannel(id: number): Promise<void> {
  const response = await api.post(`${endpoint}/${id}/copy`)
  requireServerSuccess(response.data)
}

export async function deletePublicAdminChannel(id: number): Promise<void> {
  const response = await api.delete(`${endpoint}/${id}`)
  requireServerSuccess(response.data)
}

export async function setPublicAdminChannelStatus(
  id: number,
  status: 1 | 2
): Promise<void> {
  const response = await api.post(`${endpoint}/${id}/status`, { status })
  requireServerSuccess(response.data)
}

// ---------------------------------------------------------------------------
// IP 维度可用性实测（root 一键测公共渠道在所有公共节点上的模型可用性）
// ---------------------------------------------------------------------------

export type PublicPoolProbeResult = {
  endpoint_id: number
  status: string
  reason: string
  latency_ms: number
}

export type PublicPoolModelIPView = {
  model: string
  passed: boolean
  attempted: number
  available_endpoints: number
  unavailable_endpoints: number
  unknown_endpoints: number
  not_run_endpoints: number
  total_endpoints: number
  results: PublicPoolProbeResult[]
}

export type PublicPoolProbeAllResponse = {
  channel_id: number
  endpoint_ids: number[]
  selected_count: number
  eligible_total: number
  sampled: boolean
  models: PublicPoolModelIPView[]
  checked_at: number
  ttl_seconds: number
  storage_error?: string
  storage_failed_attempts: number
  max_output_tokens: number
  health_never_deletes: boolean
}

export async function probePublicChannelAllEndpoints(
  id: number,
  models: string[]
): Promise<PublicPoolProbeAllResponse> {
  const response = await api.post(`${endpoint}/${id}/probe_all`, { models })
  return requireServerSuccess(response.data).data
}

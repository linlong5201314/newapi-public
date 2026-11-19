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

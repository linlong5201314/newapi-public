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

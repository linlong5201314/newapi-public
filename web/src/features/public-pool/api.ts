import { api } from '@/lib/api'

import type { MyChannelRow, PublicComment, PublicModelItem } from './types'

// [PUBLIC-POOL] 公共池（公开出来的自建渠道）：模型清单 / 评论 / 我的渠道可见性。
const POOL_BASE = '/api/public_pool'
const SELF_BASE = '/api/channel/self'

export async function listPublicModels(): Promise<PublicModelItem[]> {
  const res = await api.get(`${POOL_BASE}/models`)
  return res.data?.data?.items ?? []
}

export async function listPublicComments(
  model: string
): Promise<PublicComment[]> {
  const res = await api.get(`${POOL_BASE}/comments`, { params: { model } })
  return res.data?.data?.items ?? []
}

export async function addPublicComment(payload: {
  model: string
  channel_id: number
  content: string
  rating: number
}): Promise<void> {
  await api.post(`${POOL_BASE}/comments`, payload)
}

export async function listMyChannels(): Promise<MyChannelRow[]> {
  const res = await api.get(`${SELF_BASE}/`, { params: { p: 1, page_size: 100 } })
  const data = res.data?.data
  // 列表接口在不同版本里分别是 {items:[…]} / {data:[…]} / 数组本身，全兼容
  if (Array.isArray(data)) return data
  return data?.items ?? data?.data ?? []
}

export async function updateAvatar(avatar: string): Promise<string> {
  const res = await api.put('/api/user/self', { avatar })
  return (res.data?.data?.avatar as string) || avatar
}

export async function setVisibility(payload: {
  channelId: number
  visibility: number
  tier: string
  allowIps?: string
}): Promise<void> {
  await api.post(`${SELF_BASE}/visibility`, payload)
}

// [ADMIN] 平台渠道（owner_id=0）公开开关：只有公开的平台模型才会被普通用户路由到。
export async function listPlatformChannels(): Promise<import('./types').PlatformChannelRow[]> {
  const res = await api.get('/api/channel/admin/platform-channels')
  return res.data?.data?.items ?? []
}

export async function setPlatformVisibility(payload: {
  channelId: number
  visibility: number
  tier?: string
}): Promise<void> {
  await api.post('/api/channel/admin/visibility', payload)
}

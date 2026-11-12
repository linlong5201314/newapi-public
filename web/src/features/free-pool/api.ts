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
import { api } from '@/lib/api'

import type {
  FreePoolEntry,
  FreePoolReview,
  PoolSort,
  SubmitPoolPayload,
} from './types'

// [FREE-POOL] 公共免费模型提交池 API（/api/free_source_pools）。

const BASE = '/api/free_source_pools'

interface PoolListResponse {
  success: boolean
  data: FreePoolEntry[]
}

export async function listPool(sort: PoolSort): Promise<FreePoolEntry[]> {
  const res = await api.get<PoolListResponse>(BASE, { params: { sort } })
  return res.data?.data ?? []
}

export async function submitPool(
  payload: SubmitPoolPayload
): Promise<number> {
  const res = await api.post(BASE, payload)
  return res.data?.data?.id ?? 0
}

export async function ratePool(
  id: number,
  rating: number,
  comment: string
): Promise<void> {
  await api.post(`${BASE}/${id}/rate`, { rating, comment })
}

export async function reviewPool(id: number, comment: string): Promise<void> {
  await api.post(`${BASE}/${id}/review`, { comment })
}

interface ReviewListResponse {
  success: boolean
  data: FreePoolReview[]
}

export async function listReviews(id: number): Promise<FreePoolReview[]> {
  const res = await api.get<ReviewListResponse>(`${BASE}/${id}/reviews`)
  return res.data?.data ?? []
}

export async function deletePool(id: number): Promise<void> {
  await api.delete(`${BASE}/${id}`)
}

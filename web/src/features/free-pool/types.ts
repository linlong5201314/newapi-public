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

// [FREE-POOL] 公共免费模型提交池类型定义。

export type PoolSort = 'rating' | 'newest'

export interface FreePoolEntry {
  id: number
  name: string
  url: string
  signup_url?: string
  homepage_url?: string
  models: string[] | null
  description: string
  tags: string[] | null
  rating: number
  rating_count: number
  recommended: boolean
  submitter: {
    id: number
    name: string
  }
  created_at: string
  updated_at: string
}

export interface FreePoolReview {
  id: number
  user: {
    id: number
    name: string
  }
  rating: number
  comment: string
  created_at: string
}

export interface SubmitPoolPayload {
  name: string
  url: string
  signup_url?: string
  homepage_url?: string
  models: string[]
  description: string
  tags: string[]
}

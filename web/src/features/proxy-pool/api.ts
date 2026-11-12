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
import { api, type ApiRequestConfig } from '@/lib/api'

import type {
  ApiEnvelope,
  CreateEndpointPayload,
  CreateSubscriptionPayload,
  ProxyEndpoint,
  ProxyPoolSummary,
  ProxySubscription,
} from './types'

/**
 * [M7-PROXY] 用户出口 IP 池 API。
 *
 * 所有端点挂在 `/api/proxy-pool`（UserAuth 组），后端按 user_id 隔离：
 * 一个用户看不到另一个用户的节点/订阅。
 */
const BASE = '/api/proxy-pool'

/** 动作类请求自己处理错误文案，不被全局拦截器吃掉。 */
const poolActionConfig = (
  config: ApiRequestConfig = {}
): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
})

// ── 节点 ──────────────────────────────────────────────────────────────────
export async function listEndpoints(): Promise<ProxyEndpoint[]> {
  const res = await api.get(`${BASE}/endpoints`)
  return (res.data as ApiEnvelope<ProxyEndpoint[]>).data ?? []
}

export async function createEndpoint(
  data: CreateEndpointPayload
): Promise<ApiEnvelope<unknown>> {
  const res = await api.post(`${BASE}/endpoints`, data, poolActionConfig())
  return res.data as ApiEnvelope<unknown>
}

export async function updateEndpoint(
  id: number,
  data: Partial<Pick<ProxyEndpoint, 'name' | 'enabled' | 'weight' | 'region'>>
): Promise<ApiEnvelope<unknown>> {
  const res = await api.patch(
    `${BASE}/endpoints/${id}`,
    data,
    poolActionConfig()
  )
  return res.data as ApiEnvelope<unknown>
}

export async function deleteEndpoint(id: number): Promise<ApiEnvelope<unknown>> {
  const res = await api.delete(`${BASE}/endpoints/${id}`, poolActionConfig())
  return res.data as ApiEnvelope<unknown>
}

/** 连通性探测：后端会真的经该出口打一次 1.1.1.1/cdn-cgi/trace 取回 IP。 */
export async function testEndpoint(
  id: number
): Promise<ApiEnvelope<{ ok: boolean; ip?: string; error?: string }>> {
  const res = await api.post(
    `${BASE}/endpoints/${id}/test`,
    {},
    poolActionConfig()
  )
  return res.data as ApiEnvelope<{ ok: boolean; ip?: string; error?: string }>
}

// ── 订阅 ──────────────────────────────────────────────────────────────────
export async function listSubscriptions(): Promise<ProxySubscription[]> {
  const res = await api.get(`${BASE}/subscriptions`)
  return (res.data as ApiEnvelope<ProxySubscription[]>).data ?? []
}

export async function createSubscription(
  data: CreateSubscriptionPayload
): Promise<ApiEnvelope<{ node_count?: number }>> {
  const res = await api.post(`${BASE}/subscriptions`, data, poolActionConfig())
  return res.data as ApiEnvelope<{ node_count?: number }>
}

/** 重新拉订阅并解析：返回本次解析出的节点数与格式。 */
export async function syncSubscription(
  id: number
): Promise<ApiEnvelope<{ node_count?: number; format?: string }>> {
  const res = await api.post(
    `${BASE}/subscriptions/${id}/sync`,
    {},
    poolActionConfig()
  )
  return res.data as ApiEnvelope<{ node_count?: number; format?: string }>
}

export async function deleteSubscription(
  id: number
): Promise<ApiEnvelope<unknown>> {
  const res = await api.delete(
    `${BASE}/subscriptions/${id}`,
    poolActionConfig()
  )
  return res.data as ApiEnvelope<unknown>
}

// ── 汇总 / 导出 ────────────────────────────────────────────────────────────
export async function getSummary(): Promise<ProxyPoolSummary> {
  const res = await api.get(`${BASE}/summary`)
  return (res.data as ApiEnvelope<ProxyPoolSummary>).data
}

/** 导出 mihomo 配置文本（客户端型节点要这份配置才能在本地起端口）。 */
export async function exportConfig(): Promise<string> {
  const res = await api.get(`${BASE}/export`)
  const body = res.data as ApiEnvelope<{ config?: string }>
  return body?.data?.config ?? ''
}

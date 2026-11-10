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
  RouteRuleActionResponse,
  RouteRuleListResponse,
  RouteRulePayload,
  RouteRuleResponse,
} from './types'

/**
 * [M2-FE] Self-service route rule API.
 *
 * Contract: docs/M2-路由与免费源设计.md §1.4 — all endpoints live under
 * `/api/route_rule` in the UserAuth group and are owner-scoped by the
 * backend (UserId = current user enforced server-side).
 */
const BASE = '/api/route_rule'

const routeRuleActionConfig = (
  config: ApiRequestConfig = {}
): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
})

/**
 * List all route rules of the current user.
 * The per-user cap (RouteRuleMaxPerUser=20) keeps this unpaginated; the
 * response normalizer still tolerates a paginated envelope.
 */
export async function getRouteRules(): Promise<RouteRuleListResponse> {
  const res = await api.get(`${BASE}/`)
  return res.data
}

/** Get a single route rule by ID (must belong to the current user). */
export async function getRouteRule(id: number): Promise<RouteRuleResponse> {
  const res = await api.get(`${BASE}/${id}`)
  return res.data
}

/** Create a route rule. `name` doubles as the exposed virtual model name. */
export async function createRouteRule(
  data: RouteRulePayload
): Promise<RouteRuleActionResponse> {
  const res = await api.post(`${BASE}/`, data, routeRuleActionConfig())
  return res.data
}

/** Update a route rule (PUT with the id embedded in the body). */
export async function updateRouteRule(
  id: number,
  data: RouteRulePayload
): Promise<RouteRuleActionResponse> {
  const res = await api.put(
    `${BASE}/`,
    { ...data, id },
    routeRuleActionConfig()
  )
  return res.data
}

/** Delete a route rule. */
export async function deleteRouteRule(
  id: number
): Promise<RouteRuleActionResponse> {
  const res = await api.delete(`${BASE}/${id}`, routeRuleActionConfig())
  return res.data
}

/** Toggle the enabled flag of a route rule. */
export async function toggleRouteRule(
  id: number
): Promise<RouteRuleActionResponse> {
  const res = await api.post(
    `${BASE}/${id}/toggle`,
    null,
    routeRuleActionConfig()
  )
  return res.data
}

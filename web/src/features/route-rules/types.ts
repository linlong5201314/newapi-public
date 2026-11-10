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

// ============================================================================
// [M2-FE] Route Rule types
//
// Contract source: docs/M2-路由与免费源设计.md §1.1/§1.2/§1.4.
// The backend stores `targets` / `config` as JSON text columns; on the wire
// they may arrive either as a JSON string or as an already-parsed value, so
// every read path goes through the normalizers in ./lib.
// ============================================================================

export const ROUTE_RULE_STRATEGIES = [
  'priority',
  'weighted',
  'round-robin',
  'random',
  'least-used',
  'lowest-latency',
  'lkgp',
  'reset-aware',
] as const

export type RouteRuleStrategy = (typeof ROUTE_RULE_STRATEGIES)[number]

export type RouteRuleTarget = {
  channelId: number
  weight: number
  fallbackOnlyOnQuotaExhaustion?: boolean
  /** Optional per-target model rewrite (design §1.3 step 3). */
  modelOverride?: string
}

export type RouteRuleConfig = {
  maxRetries?: number
  retryDelayMs?: number
  targetTimeoutMs?: number
}

/** Normalized frontend shape of a route rule. */
export type RouteRule = {
  id: number
  user_id: number
  /** Rule name = the virtual model name exposed to the user's API calls. */
  name: string
  strategy: RouteRuleStrategy
  targets: RouteRuleTarget[]
  config: RouteRuleConfig
  enabled: boolean
  created_time: number
  updated_time: number
}

/**
 * Payload sent to POST/PUT /api/route_rule.
 * The backend (controller/route_rule.go routeRuleRequest) accepts targets as
 * a structured array and config as a structured object, and serializes them
 * into the text columns server-side.
 */
export type RouteRulePayload = {
  id?: number
  name: string
  strategy: RouteRuleStrategy
  targets: RouteRuleTarget[]
  config: RouteRuleConfig
  enabled: boolean
}

export type RouteRuleListResponse = {
  success: boolean
  message?: string
  data?:
    | unknown[]
    | {
        items?: unknown[]
        total?: number
        max_per_user?: number
        strategies?: string[]
      }
}

export type RouteRuleResponse = {
  success: boolean
  message?: string
  data?: unknown
}

export type RouteRuleActionResponse = {
  success: boolean
  message?: string
  data?: unknown
}

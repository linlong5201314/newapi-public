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
import {
  ROUTE_RULE_STRATEGIES,
  type RouteRule,
  type RouteRuleConfig,
  type RouteRuleListResponse,
  type RouteRuleStrategy,
  type RouteRuleTarget,
} from '../types'

// ============================================================================
// [M2-FE] Fail-soft wire normalizers
//
// The M2 backend stores targets/config as JSON text. Until the contract is
// fully settled the frontend accepts every plausible representation:
// snake_case or camelCase fields, JSON-string or pre-parsed targets/config,
// and list responses with or without a pagination envelope.
// ============================================================================

function asNumber(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return undefined
}

function asBoolean(value: unknown): boolean | undefined {
  if (typeof value === 'boolean') return value
  if (typeof value === 'number') return value !== 0
  if (typeof value === 'string') {
    if (value === 'true') return true
    if (value === 'false') return false
  }
  return undefined
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return undefined
}

function parseJsonText(value: string): unknown {
  try {
    return JSON.parse(value)
  } catch {
    return undefined
  }
}

/** Accepts a JSON string or an already-parsed value. */
function parseMaybeJson(value: unknown): unknown {
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (trimmed === '') return undefined
    return parseJsonText(trimmed)
  }
  return value
}

function normalizeStrategy(value: unknown): RouteRuleStrategy {
  if (
    typeof value === 'string' &&
    (ROUTE_RULE_STRATEGIES as readonly string[]).includes(value)
  ) {
    return value as RouteRuleStrategy
  }
  return 'priority'
}

function normalizeTarget(raw: unknown): RouteRuleTarget | null {
  const record = asRecord(raw)
  if (!record) return null

  const channelId =
    asNumber(record.channelId) ??
    asNumber(record.channel_id) ??
    asNumber(record.id)
  if (channelId === undefined || channelId <= 0) return null

  const weight = asNumber(record.weight)
  const fallbackOnly =
    asBoolean(record.fallbackOnlyOnQuotaExhaustion) ??
    asBoolean(record.fallback_only_on_quota_exhaustion)
  let modelOverride: string | undefined
  if (typeof record.modelOverride === 'string') {
    modelOverride = record.modelOverride
  } else if (typeof record.model_override === 'string') {
    modelOverride = record.model_override
  }

  return {
    channelId,
    weight: weight !== undefined && weight >= 0 ? Math.trunc(weight) : 1,
    ...(fallbackOnly ? { fallbackOnlyOnQuotaExhaustion: true } : {}),
    ...(modelOverride ? { modelOverride } : {}),
  }
}

export function normalizeTargets(value: unknown): RouteRuleTarget[] {
  const parsed = parseMaybeJson(value)
  if (!Array.isArray(parsed)) return []
  return parsed
    .map(normalizeTarget)
    .filter((target): target is RouteRuleTarget => target !== null)
}

export function normalizeConfig(value: unknown): RouteRuleConfig {
  const record = asRecord(parseMaybeJson(value))
  if (!record) return {}

  const config: RouteRuleConfig = {}
  const maxRetries = asNumber(record.maxRetries ?? record.max_retries)
  const retryDelayMs = asNumber(record.retryDelayMs ?? record.retry_delay_ms)
  const targetTimeoutMs = asNumber(
    record.targetTimeoutMs ?? record.target_timeout_ms
  )
  if (maxRetries !== undefined) config.maxRetries = Math.trunc(maxRetries)
  if (retryDelayMs !== undefined) config.retryDelayMs = Math.trunc(retryDelayMs)
  if (targetTimeoutMs !== undefined) {
    config.targetTimeoutMs = Math.trunc(targetTimeoutMs)
  }
  return config
}

export function normalizeRouteRule(raw: unknown): RouteRule | null {
  const record = asRecord(raw)
  if (!record) return null

  const id = asNumber(record.id ?? record.Id)
  let name: string | undefined
  if (typeof record.name === 'string') {
    name = record.name
  } else if (typeof record.Name === 'string') {
    name = record.Name
  }
  if (id === undefined || !name) return null

  return {
    id,
    user_id: asNumber(record.user_id ?? record.userId ?? record.UserId) ?? 0,
    name,
    strategy: normalizeStrategy(record.strategy ?? record.Strategy),
    targets: normalizeTargets(record.targets ?? record.Targets),
    config: normalizeConfig(record.config ?? record.Config),
    enabled: asBoolean(record.enabled ?? record.Enabled) ?? true,
    created_time:
      asNumber(record.created_time ?? record.createdTime ?? record.CreatedTime) ??
      0,
    updated_time:
      asNumber(record.updated_time ?? record.updatedTime ?? record.UpdatedTime) ??
      0,
  }
}

/**
 * Extracts the rule array from a list response, tolerating both
 * `data: [...]` and the paginated `data: { items: [...] }` envelope.
 */
export function extractRouteRules(
  response: RouteRuleListResponse
): RouteRule[] {
  const data = response.data
  const rawItems = Array.isArray(data) ? data : (asRecord(data)?.items ?? [])
  if (!Array.isArray(rawItems)) return []
  return rawItems
    .map(normalizeRouteRule)
    .filter((rule): rule is RouteRule => rule !== null)
}

export type RouteRuleListMeta = {
  rules: RouteRule[]
  /** Per-user rule cap advertised by the backend (RouteRuleMaxPerUser). */
  maxPerUser: number | null
  /** Strategy names the backend supports. */
  strategies: string[]
}

/**
 * Full list-response extraction: rules plus the optional metadata envelope
 * (`max_per_user`, `strategies`) the backend attaches.
 */
export function extractRouteRuleListMeta(
  response: RouteRuleListResponse
): RouteRuleListMeta {
  const record = asRecord(response.data)
  const maxPerUser = asNumber(record?.max_per_user ?? record?.maxPerUser)
  const strategies = Array.isArray(record?.strategies)
    ? record.strategies.filter(
        (name): name is string => typeof name === 'string'
      )
    : []
  return {
    rules: extractRouteRules(response),
    maxPerUser:
      maxPerUser !== undefined && maxPerUser > 0
        ? Math.trunc(maxPerUser)
        : null,
    strategies,
  }
}

export function getRouteRuleTableRowId(rule: RouteRule) {
  return String(rule.id)
}


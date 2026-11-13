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

/**
 * [M2-FE] Free-source templates + free quota pool API.
 *
 * Contract: docs/M2-路由与免费源设计.md §3.2/§4.2.
 * All three endpoints are owner-scoped under `/api/channel/self`.
 * Every read is fail-soft: when the backend has not shipped the endpoints
 * yet (404/403) the UI degrades to an empty list instead of toasting.
 */

// ============================================================================
// Types
// ============================================================================

export type FreeSourceTemplate = {
  id: string
  name: string
  channelType?: number
  baseUrl?: string
  models?: string[]
  /** Human-readable free-tier note, e.g. "15M tokens/月". */
  freeNote?: string
  /** Monthly quota in tokens (0/absent = uncapped or unpublished). */
  monthlyTokens?: number
  /** recurring-daily | recurring-monthly | recurring-uncapped */
  freeType?: string
  /** Extra placeholder fields in baseUrl (e.g. cloudflare-ai accountId). */
  extraFields?: string[]
  /** Quota pool key shared by several templates. */
  poolKey?: string
  /** ToS risk marker: ok | caution | avoid (avoid never ships). */
  tos?: string
  signupUrl?: string
  keyRequired?: boolean
}

export type FreeTemplateListResponse = {
  success: boolean
  message?: string
  data?: unknown
}

export type CreateFromTemplateRequest = {
  templateId: string
  key: string
  name?: string
  extra?: Record<string, string>
  /**
   * 出口代理策略：不传/空 = 跟随用户出口 IP 池（逐请求轮换）；
   * "direct" = 强制直连；其余按 http(s)/socks5 代理 URL 解析。
   */
  proxy?: string
}

export type CreateFromTemplateResponse = {
  success: boolean
  message?: string
  data?: { id?: number }
}

export type FreeUsagePool = {
  templateId: string
  name: string
  /** Monthly quota in tokens; 0/undefined means "unknown". */
  monthlyTokens?: number
  usedThisMonth?: number
  remaining?: number
  /** Unix seconds when the pool resets. */
  resetsAt?: number
}

export type FreeUsageResponse = {
  success: boolean
  message?: string
  data?: unknown
}

const actionConfig = (
  config: ApiRequestConfig = {}
): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
})

// ============================================================================
// Fail-soft normalizers (contract drift tolerance: snake_case or camelCase)
// ============================================================================

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return undefined
}

function asString(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined
}

function asNumber(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return undefined
}

function unwrapArray(data: unknown): unknown[] {
  if (Array.isArray(data)) return data
  const record = asRecord(data)
  if (record && Array.isArray(record.items)) return record.items
  return []
}

export function normalizeFreeTemplates(data: unknown): FreeSourceTemplate[] {
  return unwrapArray(data)
    .map((raw): FreeSourceTemplate | null => {
      const record = asRecord(raw)
      if (!record) return null
      const id = asString(record.id ?? record.templateId ?? record.template_id)
      const name = asString(record.name)
      if (!id || !name) return null
      const extraFieldsRaw = record.extraFields ?? record.extra_fields
      return {
        id,
        name,
        channelType: asNumber(record.channelType ?? record.channel_type),
        baseUrl: asString(record.baseUrl ?? record.base_url),
        models: Array.isArray(record.models)
          ? record.models.filter(
              (model): model is string => typeof model === 'string'
            )
          : undefined,
        freeNote: asString(record.freeNote ?? record.free_note),
        monthlyTokens: asNumber(
          record.monthlyTokens ?? record.monthly_tokens
        ),
        freeType: asString(record.freeType ?? record.free_type),
        extraFields: Array.isArray(extraFieldsRaw)
          ? extraFieldsRaw.filter(
              (field): field is string => typeof field === 'string'
            )
          : undefined,
        poolKey: asString(record.poolKey ?? record.pool_key),
        tos: asString(record.tos),
        signupUrl: asString(record.signupUrl ?? record.signup_url),
        keyRequired:
          typeof (record.keyRequired ?? record.key_required) === 'boolean'
            ? ((record.keyRequired ?? record.key_required) as boolean)
            : undefined,
      }
    })
    .filter((tpl): tpl is FreeSourceTemplate => tpl !== null)
}

export function normalizeFreeUsage(data: unknown): FreeUsagePool[] {
  return unwrapArray(data)
    .map((raw): FreeUsagePool | null => {
      const record = asRecord(raw)
      if (!record) return null
      const templateId = asString(
        record.templateId ?? record.template_id ?? record.id
      )
      const name = asString(record.name)
      if (!templateId || !name) return null
      return {
        templateId,
        name,
        monthlyTokens: asNumber(
          record.monthlyTokens ?? record.monthly_tokens
        ),
        usedThisMonth: asNumber(
          record.usedThisMonth ?? record.used_this_month
        ),
        remaining: asNumber(record.remaining),
        resetsAt: asNumber(record.resetsAt ?? record.resets_at),
      }
    })
    .filter((pool): pool is FreeUsagePool => pool !== null)
}

// ============================================================================
// API calls
// ============================================================================

/** GET /api/channel/self/free_templates — fail-soft, [] when unavailable. */
export async function getFreeTemplates(): Promise<FreeSourceTemplate[]> {
  try {
    const res = await api.get<FreeTemplateListResponse>(
      '/api/channel/self/free_templates',
      actionConfig()
    )
    if (!res.data?.success) return []
    return normalizeFreeTemplates(res.data.data)
  } catch {
    return []
  }
}

/** POST /api/channel/self/from_template — create a channel from a template. */
export async function createChannelFromTemplate(
  data: CreateFromTemplateRequest
): Promise<CreateFromTemplateResponse> {
  const res = await api.post(
    '/api/channel/self/from_template',
    data,
    actionConfig()
  )
  return res.data
}

/** GET /api/channel/self/free_usage — fail-soft, [] when unavailable. */
export async function getFreeUsage(): Promise<FreeUsagePool[]> {
  try {
    const res = await api.get<FreeUsageResponse>(
      '/api/channel/self/free_usage',
      actionConfig()
    )
    if (!res.data?.success) return []
    return normalizeFreeUsage(res.data.data)
  } catch {
    return []
  }
}

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
// [M3-FE] Check-in Center wire normalizers
//
// The backend is built in parallel; these helpers accept both snake_case and
// camelCase keys and tolerate list responses wrapped in {items}/{list}/{data}.
// ============================================================================

import {
  Briefcase,
  CalendarCheck,
  Cloud,
  Code2,
  Sparkles,
  Wrench,
  type LucideIcon,
} from 'lucide-react'

import type {
  CheckinAccount,
  CheckinLog,
  CheckinSiteCredField,
  CheckinSiteTemplate,
} from '../types'

function asRecord(value: unknown): Record<string, unknown> | null {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return null
}

function pick<T>(record: Record<string, unknown>, keys: string[]): T | undefined {
  for (const key of keys) {
    if (record[key] !== undefined && record[key] !== null) {
      return record[key] as T
    }
  }
  return undefined
}

function pickArray(data: unknown): unknown[] {
  if (Array.isArray(data)) return data
  const record = asRecord(data)
  if (!record) return []
  for (const key of ['items', 'list', 'accounts', 'logs', 'sites', 'data']) {
    const value = record[key]
    if (Array.isArray(value)) return value
  }
  return []
}

function toNumber(value: unknown, fallback = 0): number {
  const num = Number(value)
  return Number.isFinite(num) ? num : fallback
}

function toBoolean(value: unknown, fallback = false): boolean {
  if (typeof value === 'boolean') return value
  if (value === 1 || value === '1' || value === 'true') return true
  if (value === 0 || value === '0' || value === 'false') return false
  return fallback
}

export function normalizeCheckinSiteCredField(
  raw: unknown
): CheckinSiteCredField | null {
  const record = asRecord(raw)
  if (!record) return null
  const key = pick<string>(record, ['key', 'name', 'field'])
  if (!key) return null
  const optionsRaw = pick<unknown[]>(record, ['options'])
  const options = Array.isArray(optionsRaw)
    ? optionsRaw
        .map((option) => {
          const optionRecord = asRecord(option)
          if (!optionRecord) return null
          const value = pick<string>(optionRecord, ['value'])
          if (value === undefined) return null
          return {
            value: String(value),
            label: String(
              pick<string>(optionRecord, ['label']) ?? String(value)
            ),
          }
        })
        .filter((option): option is { value: string; label: string } =>
          Boolean(option)
        )
    : undefined
  return {
    key: String(key),
    label: pick<string>(record, ['label', 'title']),
    type: pick<string>(record, ['type', 'kind']) ?? 'text',
    required: toBoolean(pick(record, ['required']), false),
    placeholder: pick<string>(record, ['placeholder']),
    help: pick<string>(record, ['help', 'description', 'hint']),
    options,
  }
}

export function normalizeCheckinSite(raw: unknown): CheckinSiteTemplate | null {
  const record = asRecord(raw)
  if (!record) return null
  const site = pick<string>(record, ['site', 'id', 'key'])
  if (!site) return null
  const credFieldsRaw = pick<unknown>(record, [
    'credFields',
    'cred_fields',
    'fields',
  ])
  const credFields = pickArray(credFieldsRaw)
    .map(normalizeCheckinSiteCredField)
    .filter((field): field is CheckinSiteCredField => Boolean(field))
  return {
    site: String(site),
    name: String(pick<string>(record, ['name', 'title']) ?? site),
    icon: pick<string>(record, ['icon']),
    mode: String(pick<string>(record, ['mode']) ?? 'api'),
    credFields,
    guide: pick<string>(record, ['guide', 'description', 'help']),
  }
}

export function normalizeCheckinSites(data: unknown): CheckinSiteTemplate[] {
  return pickArray(data)
    .map(normalizeCheckinSite)
    .filter((site): site is CheckinSiteTemplate => Boolean(site))
}

export function normalizeCheckinAccount(raw: unknown): CheckinAccount | null {
  const record = asRecord(raw)
  if (!record) return null
  const id = pick<number>(record, ['id', 'Id'])
  if (id === undefined) return null
  return {
    id: toNumber(id),
    user_id: toNumber(pick(record, ['user_id', 'userId'])),
    site: String(pick<string>(record, ['site']) ?? ''),
    label: String(pick<string>(record, ['label', 'name']) ?? ''),
    channel_id: toNumber(pick(record, ['channel_id', 'channelId'])),
    mode: String(pick<string>(record, ['mode']) ?? 'api'),
    agent_url: String(pick<string>(record, ['agent_url', 'agentUrl']) ?? ''),
    enabled: toBoolean(pick(record, ['enabled']), true),
    last_status: String(pick<string>(record, ['last_status', 'lastStatus']) ?? ''),
    last_result: String(pick<string>(record, ['last_result', 'lastResult']) ?? ''),
    streak: toNumber(pick(record, ['streak'])),
    total_reward: toNumber(pick(record, ['total_reward', 'totalReward'])),
    last_checkin_day: String(
      pick<string>(record, ['last_checkin_day', 'lastCheckinDay']) ?? ''
    ),
    created_time: toNumber(pick(record, ['created_time', 'createdTime'])),
    updated_time: toNumber(pick(record, ['updated_time', 'updatedTime'])),
  }
}

export function normalizeCheckinAccounts(data: unknown): CheckinAccount[] {
  return pickArray(data)
    .map(normalizeCheckinAccount)
    .filter((account): account is CheckinAccount => Boolean(account))
}

export function normalizeCheckinLog(raw: unknown): CheckinLog | null {
  const record = asRecord(raw)
  if (!record) return null
  const id = pick<number>(record, ['id', 'Id'])
  if (id === undefined) return null
  return {
    id: toNumber(id),
    user_id: toNumber(pick(record, ['user_id', 'userId'])),
    account_id: toNumber(pick(record, ['account_id', 'accountId'])),
    site: String(pick<string>(record, ['site']) ?? ''),
    action: String(pick<string>(record, ['action']) ?? ''),
    success: toBoolean(pick(record, ['success']), false),
    detail: String(pick<string>(record, ['detail', 'message']) ?? ''),
    created_time: toNumber(pick(record, ['created_time', 'createdTime'])),
  }
}

export function normalizeCheckinLogs(data: unknown): CheckinLog[] {
  return pickArray(data)
    .map(normalizeCheckinLog)
    .filter((log): log is CheckinLog => Boolean(log))
}

// ============================================================================
// Site icon resolution (lucide)
// ============================================================================

const SITE_ICON_MAP: Record<string, LucideIcon> = {
  workbuddy: Briefcase,
  trae: Sparkles,
  qoder: Code2,
  apinex: Cloud,
  custom: Wrench,
  briefcase: Briefcase,
  sparkles: Sparkles,
  cloud: Cloud,
  wrench: Wrench,
  calendar: CalendarCheck,
  code: Code2,
}

/** Resolve the display icon for a site, honoring template-declared icons. */
export function getCheckinSiteIcon(
  site: string,
  template?: CheckinSiteTemplate
): LucideIcon {
  const iconKey = template?.icon || site
  return SITE_ICON_MAP[iconKey] ?? CalendarCheck
}

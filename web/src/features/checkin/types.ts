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
// [M3-FE] Check-in Center types
//
// Contract source: docs/M3-2api与签到中心设计.md §3.1/§3.2/§3.4.
// The backend agent builds the endpoints in parallel; every read path goes
// through the normalizers in ./lib so both snake_case and camelCase wire
// shapes are tolerated.
// ============================================================================

/** One dynamic credential field declared by a site template. */
export type CheckinSiteCredField = {
  key: string
  /** Display label; i18n key candidates are translated at render time. */
  label?: string
  /** text | password | textarea | number | select | channel (fail-soft: text) */
  type?: string
  required?: boolean
  placeholder?: string
  help?: string
  options?: { value: string; label: string }[]
}

/** Site template metadata from GET /api/checkin/sites. */
export type CheckinSiteTemplate = {
  /** workbuddy | trae | apinex | custom | ... */
  site: string
  name: string
  icon?: string
  /** api | agent */
  mode: string
  credFields: CheckinSiteCredField[]
  guide?: string
}

/** Normalized frontend shape of a check-in account. */
export type CheckinAccount = {
  id: number
  user_id: number
  site: string
  label: string
  /** Referenced own channel (workbuddy/trae). 0 = standalone credentials. */
  channel_id: number
  /** api | agent */
  mode: string
  agent_url: string
  enabled: boolean
  last_status: string
  /** Latest result summary (may be empty). */
  last_result: string
  /** Consecutive check-in days. */
  streak: number
  /** Accumulated reward (points/credits, unit depends on the site). */
  total_reward: number
  /** CST date string YYYY-MM-DD used for duplicate detection. */
  last_checkin_day: string
  created_time: number
  updated_time: number
}

export type CheckinLog = {
  id: number
  user_id: number
  account_id: number
  site: string
  action: string
  success: boolean
  detail: string
  created_time: number
}

/** Payload for POST /api/checkin/accounts. */
export type CheckinAccountPayload = {
  site: string
  label: string
  channel_id?: number
  /** Standalone credentials / custom config serialized to JSON. */
  cred_json?: string
  mode: string
  agent_url?: string
  enabled: boolean
}

export type CheckinSiteListResponse = {
  success: boolean
  message?: string
  data?: unknown
}

export type CheckinAccountListResponse = {
  success: boolean
  message?: string
  data?: unknown
}

export type CheckinAccountResponse = {
  success: boolean
  message?: string
  data?: unknown
}

export type CheckinLogListResponse = {
  success: boolean
  message?: string
  data?: unknown
}

export type CheckinActionResponse = {
  success: boolean
  message?: string
  data?: unknown
}

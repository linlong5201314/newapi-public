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
  CheckinAccountListResponse,
  CheckinAccountPayload,
  CheckinAccountResponse,
  CheckinActionResponse,
  CheckinLogListResponse,
  CheckinSiteListResponse,
} from './types'

/**
 * [M3-FE] Check-in Center API.
 *
 * Contract: docs/M3-2api与签到中心设计.md §3.4 — all endpoints live under
 * `/api/checkin` in the UserAuth group and are owner-scoped by the backend
 * (UserId = current user enforced server-side).
 */
const BASE = '/api/checkin'

const checkinActionConfig = (
  config: ApiRequestConfig = {}
): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
})

/** Site templates with their credential form schemas and guides. */
export async function getCheckinSites(): Promise<CheckinSiteListResponse> {
  const res = await api.get(`${BASE}/sites`)
  return res.data
}

/** List all check-in accounts of the current user. */
export async function getCheckinAccounts(): Promise<CheckinAccountListResponse> {
  const res = await api.get(`${BASE}/accounts`)
  return res.data
}

/** Create a check-in account. */
export async function createCheckinAccount(
  data: CheckinAccountPayload
): Promise<CheckinAccountResponse> {
  const res = await api.post(`${BASE}/accounts`, data, checkinActionConfig())
  return res.data
}

/** Update mutable fields of a check-in account (owner-scoped). */
export async function updateCheckinAccount(
  id: number,
  data: Partial<CheckinAccountPayload>
): Promise<CheckinAccountResponse> {
  const res = await api.put(
    `${BASE}/accounts/${id}`,
    data,
    checkinActionConfig()
  )
  return res.data
}

/** Enable/disable a check-in account via the update endpoint. */
export async function setCheckinAccountEnabled(
  id: number,
  enabled: boolean
): Promise<CheckinActionResponse> {
  const res = await api.put(
    `${BASE}/accounts/${id}`,
    { enabled },
    checkinActionConfig()
  )
  return res.data
}

/** Delete a check-in account. */
export async function deleteCheckinAccount(
  id: number
): Promise<CheckinActionResponse> {
  const res = await api.delete(
    `${BASE}/accounts/${id}`,
    checkinActionConfig()
  )
  return res.data
}

/** Trigger an immediate check-in run for one account. */
export async function runCheckinAccount(
  id: number
): Promise<CheckinActionResponse> {
  const res = await api.post(
    `${BASE}/accounts/${id}/run`,
    null,
    checkinActionConfig()
  )
  return res.data
}

/** List check-in logs for one account (owner-scoped). */
export async function getCheckinLogs(
  id: number
): Promise<CheckinLogListResponse> {
  const res = await api.get(`${BASE}/accounts/${id}/logs`)
  return res.data
}

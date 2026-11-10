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
import { useQuery } from '@tanstack/react-query'

import { api, type ApiRequestConfig } from '@/lib/api'

import type {
  ExtLinkAdminListResponse,
  ExtLinkEnabledListResponse,
  ExtLinkMutationResponse,
  ExtLinkPayload,
} from './types'

/**
 * [M4-FE] External links API.
 *
 * Contract: docs/M4-外链模块设计.md §2 — `/api/extlink` CRUD is RootAuth;
 * `/api/extlink/enabled` is readable by any authenticated user and returns
 * only enabled entries (drives home cards / sidebar / in-app view page).
 */
const BASE = '/api/extlink'

const mutationConfig = (config: ApiRequestConfig = {}): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
})

/** Enabled links for the current user (public catalog). */
export async function getEnabledExtLinks(): Promise<ExtLinkEnabledListResponse> {
  const res = await api.get(`${BASE}/enabled`)
  return res.data
}

/** Root admin: full list + platform options. */
export async function getExtLinksAdmin(): Promise<ExtLinkAdminListResponse> {
  const res = await api.get(`${BASE}/`)
  return res.data
}

/** Root admin: create. */
export async function createExtLink(
  data: ExtLinkPayload
): Promise<ExtLinkMutationResponse> {
  const res = await api.post(`${BASE}/`, data, mutationConfig())
  return res.data
}

/** Root admin: update (id inside payload). */
export async function updateExtLink(
  data: ExtLinkPayload
): Promise<ExtLinkMutationResponse> {
  const res = await api.put(`${BASE}/`, data, mutationConfig())
  return res.data
}

/** Root admin: delete. */
export async function deleteExtLink(
  id: number
): Promise<ExtLinkMutationResponse> {
  const res = await api.delete(`${BASE}/${id}`, mutationConfig())
  return res.data
}

export const EXT_LINKS_ENABLED_QUERY_KEY = ['extlinks', 'enabled'] as const

/**
 * Enabled external links, cached for cards/sidebar/app-view consumers.
 * staleTime matches the backend's 60s proxy cache cadence.
 */
export function useEnabledExtLinks(enabled = true) {
  return useQuery({
    queryKey: EXT_LINKS_ENABLED_QUERY_KEY,
    queryFn: async () => {
      const res = await getEnabledExtLinks()
      return res.success ? (res.data ?? []) : []
    },
    staleTime: 60_000,
    retry: 1,
    enabled,
  })
}

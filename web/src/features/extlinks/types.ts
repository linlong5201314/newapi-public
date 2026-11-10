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
// [M4-FE] External link types
//
// Contract source: docs/M4-外链模块设计.md §1 — /app/<slug> same-origin
// reverse proxy + home cards / sidebar entries managed by root.
// ============================================================================

export const EXT_LINK_MODES = ['proxy', 'iframe', 'newtab'] as const
export type ExtLinkMode = (typeof EXT_LINK_MODES)[number]

export const EXT_LINK_POSITIONS = ['home_card', 'sidebar', 'both'] as const
export type ExtLinkPosition = (typeof EXT_LINK_POSITIONS)[number]

export interface ExtLink {
  id: number
  slug: string
  name: string
  url: string
  icon: string
  mode: ExtLinkMode
  position: ExtLinkPosition
  enabled: boolean
  sort_order: number
  created_time: number
  updated_time: number
}

export interface ExtLinkOptions {
  enabled: boolean
  max_count: number
  allow_hosts: string
}

export interface ExtLinkAdminListResponse {
  success: boolean
  data: {
    links: ExtLink[]
    options: ExtLinkOptions
  }
}

export interface ExtLinkEnabledListResponse {
  success: boolean
  data: ExtLink[]
}

export interface ExtLinkPayload {
  id?: number
  slug: string
  name: string
  url: string
  icon: string
  mode: ExtLinkMode
  position: ExtLinkPosition
  enabled: boolean
  sort_order: number
}

export interface ExtLinkMutationResponse {
  success: boolean
  message?: string
  data?: ExtLink
}

/** position 命中判断（home_card/sidebar 语义含 both）。 */
export function matchesPosition(
  link: Pick<ExtLink, 'position'>,
  slot: 'home_card' | 'sidebar'
): boolean {
  return link.position === slot || link.position === 'both'
}

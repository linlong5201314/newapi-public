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
import { useTranslation } from 'react-i18next'

import { createServerError } from '@/lib/server-error-message'

import { getRouteRules } from '../api'
import { ERROR_MESSAGES } from '../constants'
import { extractRouteRuleListMeta, routeRulesQueryKeys } from '../lib'

/**
 * Shared route-rules list query. Both the table and the primary-buttons
 * consume the same cache entry (same key + queryFn), so there is exactly
 * one in-flight request.
 */
export function useRouteRulesQuery() {
  const { t } = useTranslation()

  return useQuery({
    queryKey: routeRulesQueryKeys.lists(),
    queryFn: async () => {
      const result = await getRouteRules()
      if (!result.success) {
        throw createServerError(result, t(ERROR_MESSAGES.LOAD_FAILED))
      }
      return extractRouteRuleListMeta(result)
    },
    placeholderData: (previousData) => previousData,
  })
}

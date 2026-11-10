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

import { getNodes } from '../api'
import { NODE_MESSAGES } from '../constants'
import { nodesQueryKeys, normalizeNodeList } from '../lib'

/** Shared node-list query (table + primary actions use the same cache entry). */
export function useNodesQuery() {
  const { t } = useTranslation()

  return useQuery({
    queryKey: nodesQueryKeys.lists(),
    queryFn: async () => {
      const result = await getNodes()
      if (!result.success) {
        throw createServerError(result, t(NODE_MESSAGES.LOAD_FAILED))
      }
      return normalizeNodeList(result.data)
    },
    placeholderData: (previousData) => previousData,
  })
}

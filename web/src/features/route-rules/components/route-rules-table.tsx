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
import { getRouteApi } from '@tanstack/react-router'
import { RefreshCw } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { cn } from '@/lib/utils'

import { useRouteRulesQuery } from '../hooks/use-route-rules-query'
import { getRouteRuleTableRowId } from '../lib'
import { useRouteRulesColumns } from './route-rules-columns'

const route = getRouteApi('/_authenticated/route-rules/')
const ROUTE_RULES_COLUMN_VISIBILITY_STORAGE_KEY =
  'route-rules:column-visibility'

export function RouteRulesTable() {
  const { t } = useTranslation()

  const { globalFilter, onGlobalFilterChange } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    globalFilter: { enabled: true, key: 'filter' },
  })

  // All rules are fetched at once: the per-user cap (design §1.1,
  // RouteRuleMaxPerUser=20) makes server pagination unnecessary, so the
  // table runs fully client-side (filter/sort/pagination).
  const { data, isLoading, isFetching, refetch } = useRouteRulesQuery()

  const rules = useMemo(() => data?.rules || [], [data])
  const columns = useRouteRulesColumns()

  const { table } = useDataTable({
    data: rules,
    columns,
    totalCount: rules.length,
    columnVisibilityStorageKey: ROUTE_RULES_COLUMN_VISIBILITY_STORAGE_KEY,
    globalFilter,
    onGlobalFilterChange,
    globalFilterFn: (row, _columnId, filterValue) => {
      const keyword = String(filterValue || '')
        .trim()
        .toLowerCase()
      if (!keyword) return true
      const rule = row.original
      return (
        rule.name.toLowerCase().includes(keyword) ||
        rule.strategy.toLowerCase().includes(keyword)
      )
    },
    getRowId: getRouteRuleTableRowId,
    initialSorting: [{ id: 'name', desc: false }],
  })

  return (
    <div
      role='region'
      aria-label={t('Route Rules')}
      className='relative h-full min-h-0'
    >
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={t('No Route Rules Found')}
        emptyDescription={t(
          'No route rules available. Create your first rule to expose a virtual model backed by your own channels.'
        )}
        skeletonKeyPrefix='route-rules-skeleton'
        applyHeaderSize
        toolbarProps={{
          collapsibleOnMobile: true,
          searchPlaceholder: t('Filter by name...'),
          searchDebounceMs: 300,
          preActions: (
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant='ghost'
                    size='icon'
                    onClick={() => void refetch()}
                    aria-label={t('Refresh')}
                    aria-busy={isFetching}
                    className='text-muted-foreground hover:text-foreground size-8'
                  />
                  }
                >
                  <RefreshCw
                    aria-hidden
                    className={cn(isFetching && 'animate-spin')}
                  />
                </TooltipTrigger>
                <TooltipContent>{t('Refresh')}</TooltipContent>
              </Tooltip>
            ),
        }}
      />
    </div>
  )
}

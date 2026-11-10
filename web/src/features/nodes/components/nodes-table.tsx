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
import { Plus, RefreshCw, Server } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { NODE_MESSAGES } from '../constants'
import { useNodesQuery } from '../hooks/use-nodes-query'
import { useNodesColumns } from './nodes-columns'
import { useNodes } from './nodes-provider'

const route = getRouteApi('/_authenticated/nodes/')
const NODES_COLUMN_VISIBILITY_STORAGE_KEY = 'nodes:column-visibility'

export function NodesTable() {
  const { t } = useTranslation()
  const { setOpen, setCurrentRow } = useNodes()

  const { globalFilter, onGlobalFilterChange } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    globalFilter: { enabled: true, key: 'filter' },
  })

  const { data, isLoading, isFetching, isError, error, refetch } =
    useNodesQuery()
  const nodes = useMemo(() => data ?? [], [data])
  const columns = useNodesColumns()

  const { table } = useDataTable({
    data: nodes,
    columns,
    totalCount: nodes.length,
    columnVisibilityStorageKey: NODES_COLUMN_VISIBILITY_STORAGE_KEY,
    globalFilter,
    onGlobalFilterChange,
    globalFilterFn: (row, _columnId, filterValue) => {
      const keyword = String(filterValue || '')
        .trim()
        .toLowerCase()
      if (!keyword) return true
      const node = row.original
      return (
        node.name.toLowerCase().includes(keyword) ||
        node.provider.toLowerCase().includes(keyword) ||
        node.domain.toLowerCase().includes(keyword) ||
        node.serviceUrl.toLowerCase().includes(keyword)
      )
    },
    getRowId: (node) => String(node.id),
    initialSorting: [{ id: 'name', desc: false }],
  })

  if (isError && nodes.length === 0) {
    return (
      <ErrorState
        title={t(NODE_MESSAGES.LOAD_FAILED)}
        description={`${getServerErrorMessage(
          error,
          t(NODE_MESSAGES.LOAD_FAILED)
        )} — ${t(
          'The node API may not be deployed yet. This page will work once /api/node is available.'
        )}`}
        onRetry={() => void refetch()}
      />
    )
  }

  return (
    <div
      role='region'
      aria-label={t('Node Management')}
      className='relative h-full min-h-0'
    >
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyIcon={<Server className='size-6' />}
        emptyTitle={t('No nodes yet')}
        emptyDescription={t(
          'Add the cloud services you registered manually. Nodes power health checks, deployment planning and domain failover.'
        )}
        emptyAction={
          <Button
            size='sm'
            onClick={() => {
              setCurrentRow(null)
              setOpen('create-node')
            }}
          >
            <Plus className='mr-1.5 size-4' />
            {t('Add Node')}
          </Button>
        }
        skeletonKeyPrefix='nodes-skeleton'
        applyHeaderSize
        toolbarProps={{
          collapsibleOnMobile: true,
          searchPlaceholder: t('Filter nodes...'),
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

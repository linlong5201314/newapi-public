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
import { getRouteApi } from '@tanstack/react-router'
import type { OnChangeFn, SortingState } from '@tanstack/react-table'
import { RefreshCw } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useMediaQuery } from '@/hooks'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { requireServerSuccess } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { getChannels, searchChannels } from '../api'
import {
  DEFAULT_PAGE_SIZE,
  CHANNEL_STATUS,
  CHANNEL_STATUS_OPTIONS,
} from '../constants'
import { channelsQueryKeys, getChannelTableRowId } from '../lib'
import type { Channel, ChannelSortBy } from '../types'
import { useChannelsColumns } from './channels-columns'
import { useChannels } from './channels-provider'

const route = getRouteApi('/_authenticated/my-channels/')
const MY_CHANNELS_COLUMN_VISIBILITY_STORAGE_KEY =
  'my-channels:column-visibility'
const MY_CHANNELS_COLUMN_SIZING_STORAGE_KEY = 'my-channels:column-sizing'

const CHANNEL_SORTABLE_COLUMNS = new Set<ChannelSortBy>([
  'id',
  'name',
  'priority',
  'response_time',
  'test_time',
])

function isDisabledChannelRow(channel: Channel) {
  return channel.status !== CHANNEL_STATUS.ENABLED
}

export function ChannelsTable() {
  const { t } = useTranslation()
  const { sorting, setSorting } = useChannels()
  const isMobile = useMediaQuery('(max-width: 640px)')

  // URL state management
  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    pagination: {
      defaultPage: 1,
      defaultPageSize: isMobile ? 10 : DEFAULT_PAGE_SIZE,
    },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      { columnId: 'status', searchKey: 'status', type: 'array' },
    ],
  })

  // Extract filters from column filters
  const statusFilter =
    (columnFilters.find((f) => f.id === 'status')?.value as string[]) || []

  // Determine whether to use search or regular list API
  const shouldSearch = Boolean(globalFilter?.trim())

  const sortParams = useMemo(() => {
    const activeSort = sorting[0]
    if (
      !activeSort ||
      !CHANNEL_SORTABLE_COLUMNS.has(activeSort.id as ChannelSortBy)
    ) {
      return {}
    }

    return {
      sort_by: activeSort.id as ChannelSortBy,
      sort_order: activeSort.desc ? 'desc' : 'asc',
    } as const
  }, [sorting])

  const handleSortingChange: OnChangeFn<SortingState> = (updater) => {
    setSorting(updater)
    if (pagination.pageIndex > 0) {
      onPaginationChange({ ...pagination, pageIndex: 0 })
    }
  }

  const statusParam =
    statusFilter.length > 0 && !statusFilter.includes('all')
      ? statusFilter[0]
      : undefined

  // Fetch channels data
  // eslint-disable-next-line @tanstack/query/exhaustive-deps
  const { data, isLoading, isFetching, refetch } = useQuery({
    queryKey: channelsQueryKeys.list({
      keyword: globalFilter,
      status: statusParam,
      ...sortParams,
      p: pagination.pageIndex + 1,
      page_size: pagination.pageSize,
    }),
    queryFn: async () => {
      if (shouldSearch) {
        return requireServerSuccess(
          await searchChannels({
            keyword: globalFilter,
            status: statusParam,
            ...sortParams,
            p: pagination.pageIndex + 1,
            page_size: pagination.pageSize,
          })
        )
      }
      return requireServerSuccess(
        await getChannels({
          status: statusParam,
          ...sortParams,
          p: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
        })
      )
    },
    placeholderData: (previousData) => previousData,
  })

  const channels = useMemo(() => data?.data?.items || [], [data])
  const totalCount = data?.data?.total || 0

  const columns = useChannelsColumns()

  // React Table instance
  const { table } = useDataTable({
    data: channels,
    columns,
    totalCount,
    sorting,
    columnVisibilityStorageKey: MY_CHANNELS_COLUMN_VISIBILITY_STORAGE_KEY,
    columnSizingStorageKey: isMobile
      ? false
      : MY_CHANNELS_COLUMN_SIZING_STORAGE_KEY,
    columnFilters,
    pagination,
    globalFilter,
    onSortingChange: handleSortingChange,
    onColumnFiltersChange,
    onPaginationChange,
    onGlobalFilterChange,
    getRowId: getChannelTableRowId,
    manualPagination: true,
    manualSorting: true,
    manualFiltering: true,
    enableColumnResizing: !isMobile,
    ensurePageInRange,
  })

  return (
    <div role='region' aria-label={t('My Channels')} className='relative h-full min-h-0'>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={t('No Channels Found')}
        emptyDescription={t(
          'No channels available. Create your first channel to get started.'
        )}
        skeletonKeyPrefix='my-channel-skeleton'
        applyHeaderSize
        toolbarProps={{
          collapsibleOnMobile: true,
          searchPlaceholder: t('Filter by name...'),
          searchDebounceMs: 500,
          filters: [
            {
              columnId: 'status',
              title: t('Status'),
              options: [...CHANNEL_STATUS_OPTIONS],
              singleSelect: true,
            },
          ],
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
        getRowClassName={(row, { isMobile }) => {
          if (!isDisabledChannelRow(row.original)) {
            return undefined
          }
          if (isMobile) {
            return DISABLED_ROW_MOBILE
          }
          return DISABLED_ROW_DESKTOP
        }}
      />
    </div>
  )
}

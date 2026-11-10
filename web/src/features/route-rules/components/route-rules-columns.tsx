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
/* eslint-disable react-refresh/only-export-components */
import { useQueryClient } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { Loader2, Route } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { TruncatedText } from '@/components/truncated-text'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { handleServerError } from '@/lib/handle-server-error'

import { toggleRouteRule } from '../api'
import { ERROR_MESSAGES, STRATEGY_META } from '../constants'
import { routeRulesQueryKeys } from '../lib'
import type { RouteRule } from '../types'
import { DataTableRowActions } from './data-table-row-actions'

/**
 * Inline enable/disable switch backed by POST /api/route_rule/:id/toggle.
 */
function EnabledSwitchCell({ rule }: { rule: RouteRule }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [isToggling, setIsToggling] = useState(false)

  const handleToggle = async (checked: boolean) => {
    if (checked === rule.enabled) return
    setIsToggling(true)
    try {
      const result = await toggleRouteRule(rule.id)
      if (result.success) {
        queryClient.invalidateQueries({
          queryKey: routeRulesQueryKeys.lists(),
        })
      } else {
        handleServerError(result, t(ERROR_MESSAGES.TOGGLE_FAILED))
      }
    } catch (error) {
      handleServerError(error, t(ERROR_MESSAGES.TOGGLE_FAILED))
    } finally {
      setIsToggling(false)
    }
  }

  return (
    <div className='flex items-center gap-2'>
      <Switch
        checked={rule.enabled}
        disabled={isToggling}
        onCheckedChange={(checked) => void handleToggle(checked)}
        aria-label={rule.enabled ? t('Disable') : t('Enable')}
      />
      {isToggling && (
        <Loader2 className='text-muted-foreground size-3.5 animate-spin' />
      )}
    </div>
  )
}

function StrategyCell({ rule }: { rule: RouteRule }) {
  const { t } = useTranslation()
  const meta = STRATEGY_META[rule.strategy]

  if (!meta) {
    return <Badge variant='outline'>{rule.strategy}</Badge>
  }

  return (
    <TooltipProvider delay={200}>
      <Tooltip>
        <TooltipTrigger
          render={
            <span className='inline-flex max-w-full cursor-help' />
          }
        >
          <StatusBadge
            label={t(meta.labelKey)}
            variant='info'
            copyable={false}
          />
        </TooltipTrigger>
        <TooltipContent side='bottom' className='max-w-xs'>
          {t(meta.descriptionKey)}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

/**
 * Column set for the route rules table:
 * name (= virtual model name), strategy, target count, enabled, actions.
 */
export function useRouteRulesColumns(): ColumnDef<RouteRule>[] {
  const { t } = useTranslation()

  return useMemo<ColumnDef<RouteRule>[]>(
    () => [
      {
        accessorKey: 'name',
        header: t('Name'),
        meta: { mobileTitle: true },
        cell: ({ row }) => {
          const rule = row.original
          return (
            <div className='flex max-w-full min-w-0 items-center gap-2'>
              <Route className='text-muted-foreground size-4 shrink-0' />
              <div className='flex max-w-full min-w-0 flex-col gap-1'>
                <TruncatedText
                  text={rule.name}
                  className='font-medium'
                  maxWidth='max-w-full'
                />
                <span className='text-muted-foreground text-xs'>
                  {t('Virtual model name')}
                </span>
              </div>
            </div>
          )
        },
      },
      {
        accessorKey: 'strategy',
        header: t('Strategy'),
        cell: ({ row }) => <StrategyCell rule={row.original} />,
      },
      {
        id: 'targets',
        accessorFn: (rule) => rule.targets.length,
        header: t('Targets'),
        cell: ({ row }) => {
          const count = row.original.targets.length
          return (
            <Badge variant='secondary'>
              {t('{{count}} channels', { count })}
            </Badge>
          )
        },
      },
      {
        accessorKey: 'enabled',
        header: t('Enabled'),
        cell: ({ row }) => <EnabledSwitchCell rule={row.original} />,
      },
      {
        id: 'actions',
        header: t('Actions'),
        meta: { className: 'w-28' },
        enableSorting: false,
        cell: ({ row }) => <DataTableRowActions row={row} />,
      },
    ],
    [t]
  )
}

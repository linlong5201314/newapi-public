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
import { useQueryClient } from '@tanstack/react-query'
import type { Row } from '@tanstack/react-table'
import { Loader2, Pencil, Power, PowerOff, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { handleServerError } from '@/lib/handle-server-error'

import { toggleRouteRule } from '../api'
import { ERROR_MESSAGES } from '../constants'
import { routeRulesQueryKeys } from '../lib'
import type { RouteRule } from '../types'
import { useRouteRules } from './route-rules-provider'

interface DataTableRowActionsProps {
  row: Row<RouteRule>
}

export function DataTableRowActions({ row }: DataTableRowActionsProps) {
  const { t } = useTranslation()
  const rule = row.original
  const { setOpen, setCurrentRow } = useRouteRules()
  const queryClient = useQueryClient()
  const [isToggling, setIsToggling] = useState(false)

  const handleEdit = () => {
    setCurrentRow(rule)
    setOpen('update-rule')
  }

  const handleDelete = () => {
    setCurrentRow(rule)
    setOpen('delete-rule')
  }

  const handleToggle = async () => {
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

  let statusIcon = <Power className='size-4' />
  if (isToggling) {
    statusIcon = <Loader2 className='size-4 animate-spin' />
  } else if (rule.enabled) {
    statusIcon = <PowerOff className='size-4' />
  }

  return (
    <div className='-ml-1.5 flex items-center gap-1'>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={(e) => {
                e.stopPropagation()
                handleEdit()
              }}
              aria-label={t('Edit')}
            />
          }
        >
          <Pencil className='size-4' />
        </TooltipTrigger>
        <TooltipContent>{t('Edit')}</TooltipContent>
      </Tooltip>

      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={(e) => {
                e.stopPropagation()
                void handleToggle()
              }}
              disabled={isToggling}
              aria-label={rule.enabled ? t('Disable') : t('Enable')}
              className={
                rule.enabled
                  ? 'text-destructive hover:text-destructive'
                  : 'text-success hover:text-success'
              }
            />
          }
        >
          {statusIcon}
        </TooltipTrigger>
        <TooltipContent>
          {rule.enabled ? t('Disable') : t('Enable')}
        </TooltipContent>
      </Tooltip>

      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={(e) => {
                e.stopPropagation()
                handleDelete()
              }}
              aria-label={t('Delete')}
              className='text-destructive hover:text-destructive'
            />
          }
        >
          <Trash2 className='size-4' />
        </TooltipTrigger>
        <TooltipContent>{t('Delete')}</TooltipContent>
      </Tooltip>
    </div>
  )
}

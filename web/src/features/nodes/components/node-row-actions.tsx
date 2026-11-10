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
import {
  ClipboardList,
  HeartPulse,
  Loader2,
  MoreHorizontal,
  Pencil,
  Rocket,
  Route,
  Trash2,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { handleServerError } from '@/lib/handle-server-error'

import { checkNode } from '../api'
import { NODE_MESSAGES, NODE_SUCCESS_MESSAGES } from '../constants'
import { nodesQueryKeys, normalizeNodeTestResult } from '../lib'
import type { ManagedNode } from '../types'
import { useNodes } from './nodes-provider'

export function NodeRowActions({ node }: { node: ManagedNode }) {
  const { t } = useTranslation()
  const { setOpen, setCurrentRow } = useNodes()
  const queryClient = useQueryClient()
  const [isChecking, setIsChecking] = useState(false)

  const openDialog = (
    type:
      | 'deploy-node'
      | 'failover-node'
      | 'audit-node'
      | 'update-node'
      | 'delete-node'
  ) => {
    setCurrentRow(node)
    setOpen(type)
  }

  const handleCheck = async () => {
    setIsChecking(true)
    try {
      const result = await checkNode(node.id)
      if (!result.success) {
        handleServerError(result, t(NODE_MESSAGES.CHECK_FAILED))
        return
      }
      const normalized = normalizeNodeTestResult(
        result,
        t(NODE_MESSAGES.CHECK_FAILED)
      )
      await queryClient.invalidateQueries({
        queryKey: nodesQueryKeys.lists(),
      })
      if (normalized.status === 'success') {
        const suffix =
          normalized.latencyMs !== null ? ` · ${normalized.latencyMs} ms` : ''
        toast.success(`${t(NODE_SUCCESS_MESSAGES.CHECK_OK)}${suffix}`)
      } else {
        toast.error(normalized.message || t(NODE_SUCCESS_MESSAGES.CHECK_BAD))
      }
    } catch (error) {
      handleServerError(error, t(NODE_MESSAGES.CHECK_FAILED))
    } finally {
      setIsChecking(false)
    }
  }

  return (
    <div className='flex items-center justify-end gap-1'>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={() => void handleCheck()}
              disabled={isChecking}
              aria-label={t('Run health check')}
            />
          }
        >
          {isChecking ? (
            <Loader2 className='size-4 animate-spin' />
          ) : (
            <HeartPulse className='size-4' />
          )}
        </TooltipTrigger>
        <TooltipContent>{t('Run health check')}</TooltipContent>
      </Tooltip>

      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={() => openDialog('deploy-node')}
              aria-label={t('Deployment plan')}
            />
          }
        >
          <Rocket className='size-4' />
        </TooltipTrigger>
        <TooltipContent>{t('Deployment plan')}</TooltipContent>
      </Tooltip>

      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant='ghost'
              className='data-popup-open:bg-muted flex h-8 w-8 p-0'
              aria-label={t('Open menu')}
            />
          }
        >
          <MoreHorizontal className='size-4' />
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end' className='w-44'>
          <DropdownMenuItem onClick={() => openDialog('failover-node')}>
            {t('Switch domain')}
            <DropdownMenuShortcut>
              <Route className='size-4' />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => openDialog('audit-node')}>
            {t('Audit log')}
            <DropdownMenuShortcut>
              <ClipboardList className='size-4' />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => openDialog('update-node')}>
            {t('Edit')}
            <DropdownMenuShortcut>
              <Pencil className='size-4' />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuItem
            variant='destructive'
            onClick={() => openDialog('delete-node')}
          >
            {t('Delete')}
            <DropdownMenuShortcut>
              <Trash2 className='size-4' />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

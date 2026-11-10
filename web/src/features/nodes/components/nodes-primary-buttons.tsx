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
import { Link } from '@tanstack/react-router'
import {
  HeartPulse,
  Loader2,
  Plus,
  RefreshCw,
  SlidersHorizontal,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import { checkNode } from '../api'
import { NODE_MESSAGES, NODE_SUCCESS_MESSAGES } from '../constants'
import { useNodesQuery } from '../hooks/use-nodes-query'
import { normalizeNodeTestResult } from '../lib'
import { useNodes } from './nodes-provider'

export function NodesPrimaryButtons() {
  const { t } = useTranslation()
  const { setOpen, setCurrentRow } = useNodes()
  const { data, isFetching, refetch } = useNodesQuery()
  const [isCheckingAll, setIsCheckingAll] = useState(false)

  const nodes = data ?? []
  const checkableNodes = nodes.filter((node) => node.enabled)

  const handleCheckAll = async () => {
    if (checkableNodes.length === 0) return
    setIsCheckingAll(true)
    try {
      const results = await Promise.allSettled(
        checkableNodes.map((node) => checkNode(node.id))
      )
      let succeeded = 0
      let failed = 0
      for (const result of results) {
        if (result.status === 'fulfilled') {
          const normalized = normalizeNodeTestResult(
            result.value,
            t(NODE_MESSAGES.CHECK_FAILED)
          )
          if (normalized.status === 'success') {
            succeeded++
          } else {
            failed++
          }
        } else {
          failed++
        }
      }
      await refetch()
      if (failed > 0) {
        toast.error(
          t('Health check finished: {{succeeded}} healthy, {{failed}} failed', {
            succeeded,
            failed,
          })
        )
      } else {
        toast.success(
          `${t(NODE_SUCCESS_MESSAGES.CHECK_ALL_DONE)} · ${succeeded}/${checkableNodes.length}`
        )
      }
    } catch (error) {
      handleServerError(error, t(NODE_MESSAGES.CHECK_ALL_FAILED))
    } finally {
      setIsCheckingAll(false)
    }
  }

  return (
    <div className='flex flex-wrap items-center gap-2'>
      <Button
        variant='outline'
        size='sm'
        render={
          <Link
            to='/system-settings/operations/$section'
            params={{ section: 'nodes' }}
          />
        }
      >
        <SlidersHorizontal className='mr-1.5 size-4' />
        <span className='max-sm:hidden'>{t('Node settings')}</span>
        <span className='sm:hidden'>{t('Settings')}</span>
      </Button>

      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='outline'
              size='sm'
              onClick={() => void handleCheckAll()}
              disabled={isCheckingAll || checkableNodes.length === 0}
            />
          }
        >
          {isCheckingAll ? (
            <Loader2 className='mr-1.5 size-4 animate-spin' />
          ) : (
            <HeartPulse className='mr-1.5 size-4' />
          )}
          {t('Check all')}
        </TooltipTrigger>
        <TooltipContent>
          {t('Run a health check against every enabled node')}
        </TooltipContent>
      </Tooltip>

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
          <RefreshCw aria-hidden className={cn(isFetching && 'animate-spin')} />
        </TooltipTrigger>
        <TooltipContent>{t('Refresh')}</TooltipContent>
      </Tooltip>

      <Button
        onClick={() => {
          setCurrentRow(null)
          setOpen('create-node')
        }}
      >
        <Plus className='mr-1.5 size-4' />
        <span className='max-sm:hidden'>{t('Add Node')}</span>
        <span className='sm:hidden'>{t('Add')}</span>
      </Button>
    </div>
  )
}

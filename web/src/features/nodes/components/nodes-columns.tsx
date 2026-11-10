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
import { Loader2, Server } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { TruncatedText } from '@/components/truncated-text'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import { updateNode } from '../api'
import {
  NODE_MESSAGES,
  NODE_SUCCESS_MESSAGES,
  getProviderMeta,
} from '../constants'
import { nodesQueryKeys, toExternalUrl } from '../lib'
import type { ManagedNode } from '../types'
import { NodeRowActions } from './node-row-actions'

function ProviderBadge({ node }: { node: ManagedNode }) {
  const meta = getProviderMeta(node.provider)
  const label =
    meta.value === 'other' && node.provider !== 'other'
      ? node.provider
      : meta.label
  return (
    <Badge variant='outline' className={cn('max-w-full', meta.badgeClassName)}>
      <span className='truncate'>{label}</span>
    </Badge>
  )
}

function DomainCell({ node }: { node: ManagedNode }) {
  const { t } = useTranslation()
  const primaryDomain = node.domain || node.serviceDomain
  if (!primaryDomain) {
    return <span className='text-muted-foreground text-xs'>{t('Not set')}</span>
  }
  return (
    <div className='flex max-w-56 min-w-0 flex-col gap-0.5'>
      <a
        href={toExternalUrl(primaryDomain)}
        target='_blank'
        rel='noopener noreferrer'
        onClick={(event) => event.stopPropagation()}
        className='text-primary block truncate text-sm underline-offset-4 hover:underline'
        title={primaryDomain}
      >
        {primaryDomain}
      </a>
      {node.domain &&
        node.serviceDomain &&
        node.serviceDomain !== node.domain && (
          <span
            className='text-muted-foreground truncate text-xs'
            title={node.serviceDomain}
          >
            {node.serviceDomain}
          </span>
        )}
    </div>
  )
}

function HeartbeatCell({ node }: { node: ManagedNode }) {
  const { t } = useTranslation()
  return (
    <div className='flex flex-col gap-1'>
      <div className='flex items-center gap-1.5'>
        <span
          className={cn(
            'size-2 shrink-0 rounded-full',
            node.healthy ? 'bg-success' : 'bg-destructive'
          )}
          aria-hidden
        />
        <span className='text-xs font-medium'>
          {node.healthy ? t('Healthy') : t('Unhealthy')}
        </span>
        {node.latencyMs > 0 && (
          <span className='text-muted-foreground text-xs tabular-nums'>
            {node.latencyMs} ms
          </span>
        )}
        {node.deploying && (
          <Badge variant='secondary' className='gap-1'>
            <Loader2 className='size-3 animate-spin' />
            {t('Deploying')}
          </Badge>
        )}
      </div>
      <span className='text-muted-foreground text-xs'>
        {node.lastBeat
          ? t('Last beat: {{time}}', {
              time: formatTimestampToDate(node.lastBeat),
            })
          : t('No heartbeat yet')}
      </span>
      {!node.healthy && node.lastError && (
        <span
          className='text-destructive max-w-56 truncate text-xs'
          title={node.lastError}
        >
          {node.lastError}
        </span>
      )}
    </div>
  )
}

function BalanceCell({ node }: { node: ManagedNode }) {
  const { t } = useTranslation()
  if (!node.balance || node.balance.remaining === null) {
    return (
      <span className='text-muted-foreground text-xs'>{t('Not reported')}</span>
    )
  }
  const { remaining, currency, updatedAt } = node.balance
  const amount =
    currency.toUpperCase() === 'USD'
      ? `$${remaining.toFixed(2)}`
      : `${remaining.toFixed(2)} ${currency}`

  return (
    <TooltipProvider delay={200}>
      <Tooltip>
        <TooltipTrigger render={<span className='cursor-help' />}>
          <span className='text-sm font-medium tabular-nums'>{amount}</span>
        </TooltipTrigger>
        <TooltipContent>
          {updatedAt
            ? t('Updated at {{time}}', {
                time: formatTimestampToDate(updatedAt),
              })
            : t('Provider balance snapshot')}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

function EnabledSwitchCell({ node }: { node: ManagedNode }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [pending, setPending] = useState(false)

  const handleToggle = async (checked: boolean) => {
    if (checked === node.enabled) return
    setPending(true)
    try {
      const result = await updateNode(node.id, { enabled: checked })
      if (!result.success) {
        handleServerError(result, t(NODE_MESSAGES.UPDATE_FAILED))
        return
      }
      await queryClient.invalidateQueries({
        queryKey: nodesQueryKeys.lists(),
      })
      toast.success(t(NODE_SUCCESS_MESSAGES.UPDATED))
    } catch (error) {
      handleServerError(error, t(NODE_MESSAGES.UPDATE_FAILED))
    } finally {
      setPending(false)
    }
  }

  return (
    <div className='flex items-center gap-2'>
      <Switch
        checked={node.enabled}
        disabled={pending}
        onCheckedChange={(checked) => void handleToggle(checked)}
        aria-label={node.enabled ? t('Disable') : t('Enable')}
      />
      {pending && (
        <Loader2 className='text-muted-foreground size-3.5 animate-spin' />
      )}
    </div>
  )
}

export function useNodesColumns(): ColumnDef<ManagedNode>[] {
  const { t } = useTranslation()

  return useMemo<ColumnDef<ManagedNode>[]>(
    () => [
      {
        accessorKey: 'name',
        header: t('Node'),
        meta: { mobileTitle: true },
        cell: ({ row }) => {
          const node = row.original
          return (
            <div className='flex max-w-full min-w-0 items-center gap-2'>
              <Server className='text-muted-foreground size-4 shrink-0' />
              <div className='flex max-w-full min-w-0 flex-col gap-1'>
                <TruncatedText
                  text={node.name}
                  className='font-medium'
                  maxWidth='max-w-full'
                />
                <ProviderBadge node={node} />
              </div>
            </div>
          )
        },
      },
      {
        accessorKey: 'domain',
        header: t('Domain'),
        cell: ({ row }) => <DomainCell node={row.original} />,
      },
      {
        accessorKey: 'healthy',
        header: t('Heartbeat'),
        cell: ({ row }) => <HeartbeatCell node={row.original} />,
      },
      {
        id: 'balance',
        accessorFn: (node) => node.balance?.remaining ?? -1,
        header: t('Balance'),
        cell: ({ row }) => <BalanceCell node={row.original} />,
      },
      {
        accessorKey: 'enabled',
        header: t('Enabled'),
        cell: ({ row }) => <EnabledSwitchCell node={row.original} />,
      },
      {
        id: 'actions',
        header: t('Actions'),
        meta: { className: 'w-32' },
        enableSorting: false,
        cell: ({ row }) => <NodeRowActions node={row.original} />,
      },
    ],
    [t]
  )
}

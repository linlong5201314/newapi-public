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
import { useMemo, useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { ProviderBadge } from '@/components/provider-badge'
import { StatusBadge } from '@/components/status-badge'
import { TruncatedText } from '@/components/truncated-text'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { formatCurrencyFromUSD } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'
import { translateServerText } from '@/lib/server-error-message'
import { truncateText } from '@/lib/utils'

import {
  CHANNEL_STATUS_CONFIG,
  CHANNEL_TYPE_TASK_PLUGIN,
  CHANNEL_TYPE_TRAE,
  CHANNEL_TYPE_WORKBUDDY,
} from '../constants'
import {
  channelNeedsRelogin,
  formatResponseTime,
  getChannelCredentialNickname,
  getChannelTypeIcon,
  getChannelTypeLabel,
  getResponseTimeConfig,
  parseChannelSettings,
  parseModelsList,
  handleUpdateChannelField,
  createChannelFieldUpdateScheduler,
} from '../lib'
import type { Channel } from '../types'
import { TaskPluginChannelBadge } from './channel-type-badge'
import { DataTableRowActions } from './data-table-row-actions'
import { NumericSpinnerInput } from './numeric-spinner-input'

/**
 * Numeric field cell (priority/weight) with inline editing.
 */
function ChannelFieldCell({
  channelId,
  value,
  field,
  min,
}: {
  channelId: number
  value: number | null | undefined
  field: 'priority' | 'weight'
  min: number
}) {
  const queryClient = useQueryClient()
  const fieldUpdateScheduler = useMemo(
    () =>
      createChannelFieldUpdateScheduler((nextValue) => {
        void handleUpdateChannelField(channelId, field, nextValue, queryClient)
      }),
    [channelId, field, queryClient]
  )

  useEffect(() => () => fieldUpdateScheduler.flush(), [fieldUpdateScheduler])

  return (
    <NumericSpinnerInput
      value={value ?? 0}
      onChange={fieldUpdateScheduler.schedule}
      onCommit={fieldUpdateScheduler.flush}
      min={min}
    />
  )
}

/**
 * Slim column set for the self-service "My Channels" table:
 * name, type, status, model count, priority, weight, response time, actions.
 */
export function useChannelsColumns(): ColumnDef<Channel>[] {
  const { t } = useTranslation()

  return useMemo<ColumnDef<Channel>[]>(
    () => [
      // Name column
      {
        accessorKey: 'name',
        header: t('Name'),
        meta: { mobileTitle: true },
        cell: ({ row }) => {
          const name = row.getValue('name') as string
          const channel = row.original

          // [M3-FE] Account-credential channel types surface the upstream
          // account nickname and synced balance under the channel name.
          const isAccountCredentialType =
            channel.type === CHANNEL_TYPE_WORKBUDDY ||
            channel.type === CHANNEL_TYPE_TRAE
          const credentialNickname = isAccountCredentialType
            ? getChannelCredentialNickname(channel)
            : ''

          return (
            <div className='flex max-w-full min-w-0 items-center gap-2'>
              <div className='flex max-w-full min-w-0 flex-col gap-1'>
                <TruncatedText
                  text={name}
                  className='font-medium'
                  maxWidth='max-w-full'
                />
                {isAccountCredentialType &&
                  (credentialNickname || channel.balance > 0) && (
                    <span className='text-muted-foreground truncate text-xs'>
                      {credentialNickname}
                      {credentialNickname && channel.balance > 0 && ' · '}
                      {channel.balance > 0 &&
                        t('Balance: {{balance}}', {
                          balance: formatCurrencyFromUSD(channel.balance),
                        })}
                    </span>
                  )}
                {channel.remark && (
                  <TooltipProvider delay={200}>
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <span className='text-muted-foreground text-xs' />
                        }
                      >
                        {truncateText(channel.remark, 40)}
                      </TooltipTrigger>
                      <TooltipContent side='bottom' className='max-w-xs'>
                        {channel.remark}
                      </TooltipContent>
                    </Tooltip>
                  </TooltipProvider>
                )}
              </div>
            </div>
          )
        },
        size: 240,
        minSize: 180,
      },

      // Type column
      {
        accessorKey: 'type',
        header: t('Type'),
        cell: ({ row }) => {
          const type = row.getValue('type') as number
          const typeNameKey = getChannelTypeLabel(type)
          const typeName = t(typeNameKey)
          const iconName = getChannelTypeIcon(type)
          const channel = row.original

          return (
            <div className='flex max-w-full min-w-0 items-center gap-2 overflow-hidden'>
              {type === CHANNEL_TYPE_TASK_PLUGIN ? (
                <TaskPluginChannelBadge
                  pluginKey={
                    parseChannelSettings(channel.setting)?.task_plugin_key
                  }
                />
              ) : (
                <TooltipProvider delay={300}>
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <div className='max-w-full min-w-0 overflow-hidden' />
                      }
                    >
                      <ProviderBadge
                        iconKey={`${iconName}.Color`}
                        iconSize={18}
                        label={typeName}
                        colorText={false}
                        copyable={false}
                        showDot={false}
                        className='max-w-full min-w-0 overflow-hidden'
                      />
                    </TooltipTrigger>
                    <TooltipContent side='top'>{typeName}</TooltipContent>
                  </Tooltip>
                </TooltipProvider>
              )}
            </div>
          )
        },
        size: 200,
        enableSorting: false,
      },

      // Status column
      {
        accessorKey: 'status',
        header: t('Status'),
        meta: { mobileBadge: true },
        cell: ({ row }) => {
          const status = row.getValue('status') as number
          const channel = row.original
          const config =
            CHANNEL_STATUS_CONFIG[
              status as keyof typeof CHANNEL_STATUS_CONFIG
            ] || CHANNEL_STATUS_CONFIG[0]
          // [M3-FE] Auto-disabled because the upstream account session
          // expired → surface as an explicit re-login prompt.
          const needsRelogin = channelNeedsRelogin(channel)
          const label = needsRelogin ? t('Re-login required') : t(config.label)

          // Auto-disabled: show reason and time tooltip
          if (status === 3) {
            let statusReason = ''
            let statusTime = ''
            try {
              const otherInfo = channel.other_info
                ? JSON.parse(channel.other_info)
                : null
              if (otherInfo) {
                statusReason = otherInfo.status_reason || ''
                statusTime = otherInfo.status_time
                  ? formatTimestampToDate(otherInfo.status_time)
                  : ''
              }
            } catch {
              /* empty */
            }

            if (statusReason || statusTime) {
              return (
                <TooltipProvider delay={100}>
                  <Tooltip>
                    <TooltipTrigger render={<span />}>
                      <StatusBadge
                        label={label}
                        variant={config.variant}
                        size='sm'
                        copyable={false}
                      />
                    </TooltipTrigger>
                    <TooltipContent side='top' className='max-w-xs'>
                      <div className='space-y-1 text-xs'>
                        {statusReason && (
                          <div className='wrap-anywhere'>
                            {t('Reason:')} {translateServerText(t, statusReason)}
                          </div>
                        )}
                        {statusTime && (
                          <div>
                            {t('Time:')} {statusTime}
                          </div>
                        )}
                      </div>
                    </TooltipContent>
                  </Tooltip>
                </TooltipProvider>
              )
            }
          }

          return (
            <StatusBadge
              label={label}
              variant={config.variant}
              size='sm'
              copyable={false}
            />
          )
        },
        filterFn: (row, id, value) => {
          if (!value || value.length === 0 || value.includes('all')) {
            return true
          }
          const status = row.getValue(id) as number
          if (value.includes('enabled')) {
            return status === 1
          }
          if (value.includes('disabled')) {
            return status !== 1
          }
          return false
        },
        size: 120,
        enableSorting: false,
      },

      // Models count column
      {
        accessorKey: 'models',
        header: t('Models'),
        meta: { mobileHidden: true },
        cell: ({ row }) => {
          const models = row.getValue('models') as string
          const modelArray = parseModelsList(models)

          if (modelArray.length === 0) {
            return <span className='text-muted-foreground text-xs'>-</span>
          }

          return (
            <TooltipProvider delay={200}>
              <Tooltip>
                <TooltipTrigger render={<span />}>
                  <StatusBadge
                    label={t('{{count}} models', { count: modelArray.length })}
                    variant='neutral'
                    size='sm'
                    copyable={false}
                    showDot={false}
                    className='-ml-1.5 cursor-help'
                  />
                </TooltipTrigger>
                <TooltipContent side='top' className='max-w-sm'>
                  <p className='font-mono text-xs break-all'>
                    {modelArray.slice(0, 30).join(', ')}
                    {modelArray.length > 30 ? ', …' : ''}
                  </p>
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>
          )
        },
        size: 110,
        enableSorting: false,
      },

      // Priority column
      {
        accessorKey: 'priority',
        header: t('Priority'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <ChannelFieldCell
            channelId={row.original.id}
            value={row.original.priority}
            field='priority'
            min={-999}
          />
        ),
        size: 100,
      },

      // Weight column
      {
        accessorKey: 'weight',
        header: t('Weight'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <ChannelFieldCell
            channelId={row.original.id}
            value={row.original.weight}
            field='weight'
            min={0}
          />
        ),
        size: 90,
        enableSorting: false,
      },

      // Response Time column
      {
        accessorKey: 'response_time',
        header: t('Response'),
        meta: { mobileHidden: true },
        cell: ({ row }) => {
          const responseTime = row.getValue('response_time') as number
          const config = getResponseTimeConfig(responseTime)

          return (
            <StatusBadge
              label={formatResponseTime(responseTime, t)}
              variant={config.variant}
              size='sm'
              copyable={false}
              className='-ml-1.5'
            />
          )
        },
        size: 110,
      },

      // Actions column
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => <DataTableRowActions row={row} />,
        enableSorting: false,
        enableHiding: false,
        meta: { pinned: 'right' as const },
      },
    ],
    [t]
  )
}

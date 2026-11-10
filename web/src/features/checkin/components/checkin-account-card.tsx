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
import {
  Flame,
  Gift,
  History,
  Loader2,
  Play,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Switch } from '@/components/ui/switch'

import { getCheckinSiteIcon } from '../lib'
import type { CheckinAccount, CheckinSiteTemplate } from '../types'

type CheckinAccountCardProps = {
  account: CheckinAccount
  template?: CheckinSiteTemplate
  running?: boolean
  toggling?: boolean
  deleting?: boolean
  onRun: (account: CheckinAccount) => void
  onToggle: (account: CheckinAccount, enabled: boolean) => void
  onDelete: (account: CheckinAccount) => void
  onShowLogs: (account: CheckinAccount) => void
}

export function CheckinAccountCard(props: CheckinAccountCardProps) {
  const { t } = useTranslation()
  const { account, template } = props
  const SiteIcon = getCheckinSiteIcon(account.site, template)
  const siteName = template?.name || account.site

  const isSuccessStatus =
    account.last_status === 'success' || account.last_status === 'ok'
  const lastStatusVariant = (() => {
    if (isSuccessStatus) return 'success' as const
    if (account.last_status === 'failed' || account.last_status === 'error') {
      return 'danger' as const
    }
    return 'neutral' as const
  })()

  const lastStatusLabel = isSuccessStatus
    ? t('Success')
    : account.last_status

  return (
    <Card className='flex min-w-0 flex-col'>
      <CardHeader className='pb-3'>
        <div className='flex items-start justify-between gap-3'>
          <div className='flex min-w-0 items-center gap-3'>
            <span className='bg-muted text-muted-foreground flex size-10 shrink-0 items-center justify-center rounded-lg'>
              <SiteIcon className='size-5' aria-hidden='true' />
            </span>
            <div className='min-w-0'>
              <CardTitle className='truncate text-base'>
                {account.label || siteName}
              </CardTitle>
              <CardDescription className='truncate'>
                {siteName}
                {account.channel_id > 0 &&
                  ` · ${t('Channel')} #${account.channel_id}`}
              </CardDescription>
            </div>
          </div>
          <div className='flex shrink-0 items-center gap-2'>
            <span className='text-muted-foreground text-xs'>
              {account.enabled ? t('Enabled') : t('Disabled')}
            </span>
            <Switch
              checked={account.enabled}
              disabled={props.toggling}
              onCheckedChange={(checked) => props.onToggle(account, checked)}
              aria-label={t('Toggle check-in account')}
            />
          </div>
        </div>
      </CardHeader>
      <CardContent className='flex flex-1 flex-col gap-3'>
        <div className='flex flex-wrap items-center gap-2'>
          <Badge variant='secondary' className='gap-1'>
            <Flame className='size-3' aria-hidden='true' />
            {t('{{count}}-day streak', { count: account.streak })}
          </Badge>
          <Badge variant='secondary' className='gap-1'>
            <Gift className='size-3' aria-hidden='true' />
            {t('Total reward: {{count}}', { count: account.total_reward })}
          </Badge>
          {account.last_status && (
            <StatusBadge
              label={lastStatusLabel}
              variant={lastStatusVariant}
              size='sm'
              copyable={false}
            />
          )}
        </div>
        {account.last_result && (
          <p
            className='text-muted-foreground line-clamp-2 text-xs break-all'
            title={account.last_result}
          >
            {account.last_result}
          </p>
        )}
        <p className='text-muted-foreground text-xs'>
          {account.last_checkin_day
            ? t('Last check-in: {{date}}', { date: account.last_checkin_day })
            : t('Never checked in yet')}
        </p>
        <div className='mt-auto flex flex-wrap items-center gap-2 pt-1'>
          <Button
            size='sm'
            disabled={props.running || !account.enabled}
            onClick={() => props.onRun(account)}
          >
            {props.running ? (
              <Loader2 className='mr-2 size-4 animate-spin' />
            ) : (
              <Play className='mr-2 size-4' />
            )}
            {t('Check in now')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={() => props.onShowLogs(account)}
          >
            <History className='mr-2 size-4' />
            {t('Logs')}
          </Button>
          <Button
            size='sm'
            variant='ghost'
            className='text-destructive hover:text-destructive ml-auto'
            disabled={props.deleting}
            onClick={() => props.onDelete(account)}
            aria-label={t('Delete')}
          >
            {props.deleting ? (
              <Loader2 className='size-4 animate-spin' />
            ) : (
              <Trash2 className='size-4' />
            )}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

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
import { Gift, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

import { getFreeUsage, type FreeUsagePool } from '../free-sources'
import { channelsQueryKeys } from '../lib'
import { useChannels } from './channels-provider'

// ============================================================================
// [M2-FE] Free quota pool cards
//
// Design: docs/M2-路由与免费源设计.md §4.2 — one card per template pool with
// a used/monthly progress bar, remaining tokens and a reset countdown.
// ============================================================================

function formatTokens(value: number | undefined): string {
  if (value === undefined) return '-'
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return String(Math.trunc(value))
}

function formatResetCountdown(
  resetsAt: number | undefined,
  now: number,
  t: (key: string, options?: Record<string, unknown>) => string
): string {
  if (!resetsAt) return t('Reset time unknown')
  const diffMs = resetsAt * 1000 - now
  if (diffMs <= 0) return t('Resets soon')
  const totalMinutes = Math.floor(diffMs / 60_000)
  const days = Math.floor(totalMinutes / (60 * 24))
  const hours = Math.floor((totalMinutes % (60 * 24)) / 60)
  const minutes = totalMinutes % 60
  if (days > 0) return t('Resets in {{days}}d {{hours}}h', { days, hours })
  if (hours > 0) return t('Resets in {{hours}}h {{minutes}}m', { hours, minutes })
  return t('Resets in {{minutes}}m', { minutes: Math.max(minutes, 1) })
}

function FreeUsageCard({
  pool,
  now,
}: {
  pool: FreeUsagePool
  now: number
}) {
  const { t } = useTranslation()

  const monthly = pool.monthlyTokens ?? 0
  const used = pool.usedThisMonth ?? 0
  const remaining = pool.remaining ?? Math.max(monthly - used, 0)
  const percentage =
    monthly > 0 ? Math.min(100, Math.max(0, (used / monthly) * 100)) : 0
  const exhausted = monthly > 0 && remaining <= 0

  let progressColor = 'text-success'
  if (exhausted) {
    progressColor = 'text-destructive'
  } else if (percentage >= 80) {
    progressColor = 'text-warning'
  }

  return (
    <Card className='min-w-0'>
      <CardHeader className='pb-2'>
        <CardTitle className='flex items-center justify-between gap-2 text-sm'>
          <span className='min-w-0 truncate'>{pool.name}</span>
          {exhausted && (
            <span className='text-destructive shrink-0 text-xs font-normal'>
              {t('Exhausted')}
            </span>
          )}
        </CardTitle>
      </CardHeader>
      <CardContent className='flex flex-col gap-2'>
        <Progress
          value={percentage}
          aria-label={t('Used percentage')}
          className={cn(
            'w-full [&_[data-slot=progress-indicator]]:bg-current',
            progressColor
          )}
        />
        <div className='text-muted-foreground flex items-center justify-between gap-2 text-xs'>
          <span>
            {t('{{used}} / {{total}} used', {
              used: formatTokens(used),
              total: formatTokens(pool.monthlyTokens),
            })}
          </span>
          <span>
            {t('{{remaining}} left', { remaining: formatTokens(remaining) })}
          </span>
        </div>
        <div className='text-muted-foreground text-xs'>
          {formatResetCountdown(pool.resetsAt, now, t)}
        </div>
      </CardContent>
    </Card>
  )
}

const USAGE_SKELETON_IDS = ['usage-sk-1', 'usage-sk-2', 'usage-sk-3']

export function FreeUsageCards() {
  const { t } = useTranslation()
  const { setOpen } = useChannels()
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const intervalId = window.setInterval(() => setNow(Date.now()), 60_000)
    return () => window.clearInterval(intervalId)
  }, [])

  // Fail-soft: the API helper already degrades errors/404 to an empty list.
  const { data: pools, isLoading } = useQuery({
    queryKey: [...channelsQueryKeys.all, 'free-usage'],
    queryFn: getFreeUsage,
    staleTime: 60_000,
    refetchInterval: 5 * 60_000,
  })

  if (isLoading) {
    return (
      <div className='mb-3 grid shrink-0 grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3'>
        {USAGE_SKELETON_IDS.map((id) => (
          <Skeleton key={id} className='h-28 rounded-xl' />
        ))}
      </div>
    )
  }

  if (!pools || pools.length === 0) {
    return (
      <div className='border-border/60 mb-3 flex shrink-0 flex-wrap items-center justify-between gap-3 rounded-lg border border-dashed p-3'>
        <div className='text-muted-foreground flex min-w-0 items-center gap-2 text-sm'>
          <Gift className='size-4 shrink-0' />
          <span className='truncate'>
            {t(
              'Add a channel from a free source and its monthly quota shows up here.'
            )}
          </span>
        </div>
        <Button
          variant='outline'
          size='sm'
          onClick={() => setOpen('from-template')}
        >
          <Gift className='size-4' />
          {t('Add from Free Source')}
        </Button>
      </div>
    )
  }

  return (
    <div className='mb-3 shrink-0'>
      <div className='mb-2 flex items-center gap-2'>
        <RefreshCw className='text-muted-foreground size-3.5' />
        <span className='text-sm font-medium'>{t('Free Quota')}</span>
      </div>
      <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3'>
        {pools.map((pool) => (
          <FreeUsageCard key={pool.templateId} pool={pool} now={now} />
        ))}
      </div>
    </div>
  )
}

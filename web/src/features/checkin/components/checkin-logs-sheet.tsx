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
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { formatTimestampToDate } from '@/lib/format'
import { createServerError, getServerErrorMessage } from '@/lib/server-error-message'

import { getCheckinLogs } from '../api'
import { CHECKIN_MESSAGES, checkinQueryKeys } from '../constants'
import { normalizeCheckinLogs } from '../lib'
import type { CheckinAccount } from '../types'

type CheckinLogsSheetProps = {
  account: CheckinAccount | null
  onOpenChange: (open: boolean) => void
}

export function CheckinLogsSheet(props: CheckinLogsSheetProps) {
  const { t } = useTranslation()
  const accountId = props.account?.id

  const logsQuery = useQuery({
    queryKey: checkinQueryKeys.logs(accountId ?? 0),
    queryFn: async () => {
      const result = await getCheckinLogs(accountId as number)
      if (!result.success) {
        throw createServerError(result, t(CHECKIN_MESSAGES.LOAD_LOGS_FAILED))
      }
      return normalizeCheckinLogs(result.data)
    },
    enabled: accountId !== undefined,
  })

  return (
    <Sheet
      open={props.account !== null}
      onOpenChange={props.onOpenChange}
    >
      <SheetContent className='flex w-full flex-col sm:max-w-xl'>
        <SheetHeader>
          <SheetTitle>
            {t('Check-in logs')}
            {props.account?.label ? ` · ${props.account.label}` : ''}
          </SheetTitle>
          <SheetDescription>
            {t('Recent actions and results for this account.')}
          </SheetDescription>
        </SheetHeader>

        <div className='flex justify-end px-4'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void logsQuery.refetch()}
            disabled={logsQuery.isFetching}
          >
            <RefreshCw
              className={
                logsQuery.isFetching
                  ? 'mr-2 size-4 animate-spin'
                  : 'mr-2 size-4'
              }
            />
            {t('Refresh')}
          </Button>
        </div>

        <div className='flex-1 overflow-y-auto px-4 pb-4'>
          {logsQuery.isLoading && (
            <LoadingState message={t('Loading logs...')} />
          )}
          {logsQuery.isError && (
            <ErrorState
              title={t(CHECKIN_MESSAGES.LOAD_LOGS_FAILED)}
              description={getServerErrorMessage(
                logsQuery.error,
                t(CHECKIN_MESSAGES.LOAD_LOGS_FAILED)
              )}
              onRetry={() => void logsQuery.refetch()}
            />
          )}
          {logsQuery.isSuccess && logsQuery.data.length === 0 && (
            <p className='text-muted-foreground py-10 text-center text-sm'>
              {t('No check-in logs yet.')}
            </p>
          )}
          {logsQuery.isSuccess && logsQuery.data.length > 0 && (
            <ul className='flex flex-col gap-2'>
              {logsQuery.data.map((log) => (
                <li
                  key={log.id}
                  className='flex flex-col gap-1 rounded-lg border p-3'
                >
                  <div className='flex items-center gap-2'>
                    <StatusBadge
                      label={log.success ? t('Success') : t('Failed')}
                      variant={log.success ? 'success' : 'danger'}
                      size='sm'
                      copyable={false}
                    />
                    <span className='text-sm font-medium break-all'>
                      {log.action || t('Check-in')}
                    </span>
                    <span className='text-muted-foreground ml-auto shrink-0 text-xs'>
                      {log.created_time
                        ? formatTimestampToDate(log.created_time)
                        : '-'}
                    </span>
                  </div>
                  {log.detail && (
                    <p className='text-muted-foreground text-xs break-all whitespace-pre-wrap'>
                      {log.detail}
                    </p>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

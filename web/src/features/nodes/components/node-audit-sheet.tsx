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
import {
  createServerError,
  getServerErrorMessage,
} from '@/lib/server-error-message'

import { getNodeAudit } from '../api'
import { AUDIT_ACTION_LABELS, NODE_MESSAGES } from '../constants'
import { nodesQueryKeys, normalizeAuditLogs } from '../lib'
import type { ManagedNode } from '../types'

type NodeAuditSheetProps = {
  node: ManagedNode | null
  onOpenChange: (open: boolean) => void
}

export function NodeAuditSheet(props: NodeAuditSheetProps) {
  const { t } = useTranslation()
  const nodeId = props.node?.id

  const auditQuery = useQuery({
    queryKey: nodesQueryKeys.audit(nodeId ?? 0),
    queryFn: async () => {
      const result = await getNodeAudit(nodeId as number)
      if (!result.success) {
        throw createServerError(result, t(NODE_MESSAGES.AUDIT_LOAD_FAILED))
      }
      return normalizeAuditLogs(result.data)
    },
    enabled: nodeId !== undefined,
  })

  return (
    <Sheet open={props.node !== null} onOpenChange={props.onOpenChange}>
      <SheetContent className='flex w-full flex-col sm:max-w-xl'>
        <SheetHeader>
          <SheetTitle>
            {t('Node audit log')}
            {props.node?.name ? ` · ${props.node.name}` : ''}
          </SheetTitle>
          <SheetDescription>
            {t(
              'Every deploy, failover and health check writes an audit record. Entries are read-only.'
            )}
          </SheetDescription>
        </SheetHeader>

        <div className='flex justify-end px-4'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void auditQuery.refetch()}
            disabled={auditQuery.isFetching}
          >
            <RefreshCw
              className={
                auditQuery.isFetching
                  ? 'mr-2 size-4 animate-spin'
                  : 'mr-2 size-4'
              }
            />
            {t('Refresh')}
          </Button>
        </div>

        <div className='flex-1 overflow-y-auto px-4 pb-4'>
          {auditQuery.isLoading && (
            <LoadingState message={t('Loading audit records...')} />
          )}
          {auditQuery.isError && (
            <ErrorState
              title={t(NODE_MESSAGES.AUDIT_LOAD_FAILED)}
              description={getServerErrorMessage(
                auditQuery.error,
                t(NODE_MESSAGES.AUDIT_LOAD_FAILED)
              )}
              onRetry={() => void auditQuery.refetch()}
            />
          )}
          {auditQuery.isSuccess && auditQuery.data.length === 0 && (
            <p className='text-muted-foreground py-10 text-center text-sm'>
              {t('No audit records for this node yet.')}
            </p>
          )}
          {auditQuery.isSuccess && auditQuery.data.length > 0 && (
            <ul className='flex flex-col gap-2'>
              {auditQuery.data.map((log) => (
                <li
                  key={log.id}
                  className='flex flex-col gap-1 rounded-lg border p-3'
                >
                  <div className='flex flex-wrap items-center gap-2'>
                    <StatusBadge
                      label={log.success ? t('Success') : t('Failed')}
                      variant={log.success ? 'success' : 'danger'}
                      size='sm'
                      copyable={false}
                    />
                    <span className='text-sm font-medium break-all'>
                      {t(AUDIT_ACTION_LABELS[log.action] ?? log.action) ||
                        t('Node action')}
                    </span>
                    <span className='text-muted-foreground ml-auto shrink-0 text-xs'>
                      {formatTimestampToDate(log.createdTime)}
                    </span>
                  </div>
                  <div className='text-muted-foreground flex flex-wrap gap-x-3 gap-y-1 text-xs'>
                    {log.target && (
                      <span>
                        {t('Target')}: {log.target}
                      </span>
                    )}
                    {log.operator && (
                      <span>
                        {t('Operator')}: {log.operator}
                      </span>
                    )}
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

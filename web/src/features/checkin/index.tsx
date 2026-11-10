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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CalendarCheck, Info, Plus } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import {
  createServerError,
  getServerErrorMessage,
} from '@/lib/server-error-message'

import {
  createCheckinAccount,
  deleteCheckinAccount,
  getCheckinAccounts,
  getCheckinSites,
  runCheckinAccount,
  setCheckinAccountEnabled,
} from './api'
import { CheckinAccountCard } from './components/checkin-account-card'
import { CheckinAddDialog } from './components/checkin-add-dialog'
import { CheckinLogsSheet } from './components/checkin-logs-sheet'
import {
  CHECKIN_FALLBACK_SITES,
  CHECKIN_MESSAGES,
  checkinQueryKeys,
} from './constants'
import { normalizeCheckinAccounts, normalizeCheckinSites } from './lib'
import type { CheckinAccount, CheckinAccountPayload } from './types'

/**
 * [M3-FE] Check-in Center page.
 *
 * Contract: docs/M3-2api与签到中心设计.md §3. All endpoints are fail-soft so
 * the page degrades cleanly while the backend agent lands the API.
 */
export function Checkin() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [addOpen, setAddOpen] = useState(false)
  const [logsAccount, setLogsAccount] = useState<CheckinAccount | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<CheckinAccount | null>(null)

  // Site templates; fall back to the built-in defaults when the endpoint is
  // not deployed yet (backend agent works in parallel).
  const sitesQuery = useQuery({
    queryKey: checkinQueryKeys.sites(),
    queryFn: async () => {
      try {
        const result = await getCheckinSites()
        if (!result.success) return CHECKIN_FALLBACK_SITES
        const sites = normalizeCheckinSites(result.data)
        return sites.length > 0 ? sites : CHECKIN_FALLBACK_SITES
      } catch {
        return CHECKIN_FALLBACK_SITES
      }
    },
    staleTime: 5 * 60 * 1000,
    meta: { errorToast: false },
  })

  const accountsQuery = useQuery({
    queryKey: checkinQueryKeys.accounts(),
    queryFn: async () => {
      const result = await getCheckinAccounts()
      if (!result.success) {
        throw createServerError(result, t(CHECKIN_MESSAGES.LOAD_ACCOUNTS_FAILED))
      }
      return normalizeCheckinAccounts(result.data)
    },
    placeholderData: (previous) => previous,
  })

  const invalidateAccounts = () =>
    queryClient.invalidateQueries({ queryKey: checkinQueryKeys.accounts() })

  const createMutation = useMutation({
    mutationFn: async (payload: CheckinAccountPayload) => {
      const result = await createCheckinAccount(payload)
      if (!result.success) {
        throw createServerError(result, t(CHECKIN_MESSAGES.CREATE_FAILED))
      }
      return result
    },
    onSuccess: () => {
      toast.success(t(CHECKIN_MESSAGES.CREATED))
      setAddOpen(false)
      void invalidateAccounts()
    },
    onError: (error) =>
      handleServerError(error, t(CHECKIN_MESSAGES.CREATE_FAILED)),
  })

  const toggleMutation = useMutation({
    mutationFn: async (variables: { id: number; enabled: boolean }) => {
      const result = await setCheckinAccountEnabled(
        variables.id,
        variables.enabled
      )
      if (!result.success) {
        throw createServerError(result, t(CHECKIN_MESSAGES.UPDATE_FAILED))
      }
      return result
    },
    onSuccess: () => void invalidateAccounts(),
    onError: (error) =>
      handleServerError(error, t(CHECKIN_MESSAGES.UPDATE_FAILED)),
  })

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      const result = await deleteCheckinAccount(id)
      if (!result.success) {
        throw createServerError(result, t(CHECKIN_MESSAGES.DELETE_FAILED))
      }
      return result
    },
    onSuccess: () => {
      toast.success(t(CHECKIN_MESSAGES.DELETED))
      setDeleteTarget(null)
      void invalidateAccounts()
    },
    onError: (error) =>
      handleServerError(error, t(CHECKIN_MESSAGES.DELETE_FAILED)),
  })

  const runMutation = useMutation({
    mutationFn: async (id: number) => {
      const result = await runCheckinAccount(id)
      if (!result.success) {
        throw createServerError(result, t(CHECKIN_MESSAGES.RUN_FAILED))
      }
      return result
    },
    onSuccess: (data) => {
      const record =
        data?.data && typeof data.data === 'object'
          ? (data.data as Record<string, unknown>)
          : undefined
      const summary =
        (record?.message as string) || (record?.result as string) || ''
      toast.success(
        summary ? String(summary) : t(CHECKIN_MESSAGES.RUN_STARTED)
      )
      void invalidateAccounts()
      if (logsAccount) {
        void queryClient.invalidateQueries({
          queryKey: checkinQueryKeys.logs(logsAccount.id),
        })
      }
    },
    onError: (error) =>
      handleServerError(error, t(CHECKIN_MESSAGES.RUN_FAILED)),
  })

  const accounts = accountsQuery.data ?? []
  const sites = useMemo(
    () => sitesQuery.data ?? CHECKIN_FALLBACK_SITES,
    [sitesQuery.data]
  )
  const siteByKey = useMemo(() => {
    const map = new Map<string, (typeof sites)[number]>()
    for (const site of sites) map.set(site.site, site)
    return map
  }, [sites])

  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>
        <span className='truncate'>{t('Check-in Center')}</span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button onClick={() => setAddOpen(true)}>
          <Plus className='mr-2 size-4' />
          {t('Add account')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <Alert className='mb-3 shrink-0'>
          <Info className='h-4 w-4' />
          <AlertDescription>
            {t(
              'Link your WorkBuddy/Trae channels or standalone site credentials here. The platform runs scheduled check-ins twice a day (CST); you can also trigger one manually.'
            )}
          </AlertDescription>
        </Alert>

        {accountsQuery.isLoading && (
          <LoadingState message={t('Loading check-in accounts...')} />
        )}

        {accountsQuery.isError && (
          <ErrorState
            title={t(CHECKIN_MESSAGES.LOAD_ACCOUNTS_FAILED)}
            description={getServerErrorMessage(
              accountsQuery.error,
              t(CHECKIN_MESSAGES.LOAD_ACCOUNTS_FAILED)
            )}
            onRetry={() => void accountsQuery.refetch()}
          />
        )}

        {accountsQuery.isSuccess && accounts.length === 0 && (
          <EmptyState
            icon={CalendarCheck}
            title={t('No check-in accounts yet')}
            description={t(
              'Add your first site account to start automated daily check-ins and streak rewards.'
            )}
            action={
              <Button onClick={() => setAddOpen(true)}>
                <Plus className='mr-2 size-4' />
                {t('Add account')}
              </Button>
            }
          />
        )}

        {accounts.length > 0 && (
          <div className='grid gap-4 sm:grid-cols-2 xl:grid-cols-3'>
            {accounts.map((account) => (
              <CheckinAccountCard
                key={account.id}
                account={account}
                template={siteByKey.get(account.site)}
                running={
                  runMutation.isPending &&
                  runMutation.variables === account.id
                }
                toggling={
                  toggleMutation.isPending &&
                  toggleMutation.variables?.id === account.id
                }
                deleting={
                  deleteMutation.isPending &&
                  deleteMutation.variables === account.id
                }
                onRun={(target) => runMutation.mutate(target.id)}
                onToggle={(target, enabled) =>
                  toggleMutation.mutate({ id: target.id, enabled })
                }
                onDelete={setDeleteTarget}
                onShowLogs={setLogsAccount}
              />
            ))}
          </div>
        )}
      </SectionPageLayout.Content>

      <CheckinAddDialog
        open={addOpen}
        onOpenChange={setAddOpen}
        sites={sites}
        submitting={createMutation.isPending}
        onSubmit={(payload) => createMutation.mutate(payload)}
      />

      <CheckinLogsSheet
        account={logsAccount}
        onOpenChange={(open) => {
          if (!open) setLogsAccount(null)
        }}
      />

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
        title={t('Delete check-in account')}
        desc={t(
          'This removes the account and stops its scheduled check-ins. Logs are kept.'
        )}
        confirmText={t('Delete')}
        destructive
        isLoading={deleteMutation.isPending}
        handleConfirm={() => {
          if (deleteTarget) deleteMutation.mutate(deleteTarget.id)
        }}
      />
    </SectionPageLayout>
  )
}

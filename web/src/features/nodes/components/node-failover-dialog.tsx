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
  ArrowRight,
  CheckCircle2,
  CircleAlert,
  Loader2,
  RefreshCw,
  Route,
  TriangleAlert,
} from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { applyNodeFailover, planNodeFailover } from '../api'
import {
  NODE_MESSAGES,
  NODE_SUCCESS_MESSAGES,
  PLAN_VALIDITY_MINUTES,
} from '../constants'
import { nodesQueryKeys, normalizeFailoverPlan } from '../lib'
import type { FailoverPlan, ManagedNode } from '../types'

type FailoverDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow: ManagedNode | null
}

type Phase = 'loading' | 'plan-error' | 'ready' | 'applying' | 'done' | 'failed'

const EMPTY_PLAN: FailoverPlan = {
  planId: '',
  primaryDomain: '',
  currentCname: '',
  targetCname: '',
  kvChanges: [],
  warnings: [],
  kvSkipped: false,
  raw: null,
}

function truncateDisplay(value: string, maxLength = 160): string {
  if (!value) return '—'
  if (value.length <= maxLength) return value
  return `${value.slice(0, maxLength)}…`
}

function kvKey(change: {
  key: string
  from: string
  to: string
  action: string
}): string {
  return [change.key, change.from, change.to, change.action].join('|')
}

export function NodeFailoverDialog(props: FailoverDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const node = props.currentRow

  const [phase, setPhase] = useState<Phase>('loading')
  const [plan, setPlan] = useState<FailoverPlan>(EMPTY_PLAN)
  const [errorMessage, setErrorMessage] = useState('')
  const [confirmed, setConfirmed] = useState(false)

  const loadPlan = useCallback(async () => {
    if (!node) return
    setPhase('loading')
    setPlan(EMPTY_PLAN)
    setErrorMessage('')
    setConfirmed(false)
    try {
      const result = await planNodeFailover(node.id)
      if (!result.success) {
        setErrorMessage(
          getServerErrorMessage(result, t(NODE_MESSAGES.FAILOVER_PLAN_FAILED))
        )
        setPhase('plan-error')
        return
      }
      setPlan(normalizeFailoverPlan(result.data))
      setPhase('ready')
    } catch (error) {
      setErrorMessage(
        getServerErrorMessage(error, t(NODE_MESSAGES.FAILOVER_PLAN_FAILED))
      )
      setPhase('plan-error')
    }
  }, [node, t])

  useEffect(() => {
    if (!props.open) return
    void loadPlan()
  }, [props.open, loadPlan])

  const handleApply = async () => {
    if (!node || !plan.planId || !confirmed) return
    setPhase('applying')
    setErrorMessage('')
    try {
      const result = await applyNodeFailover(node.id, plan.planId)
      if (!result.success) {
        setErrorMessage(
          getServerErrorMessage(result, t(NODE_MESSAGES.FAILOVER_APPLY_FAILED))
        )
        setPhase('ready')
        return
      }
      toast.success(t(NODE_SUCCESS_MESSAGES.FAILOVER_APPLIED))
      await queryClient.invalidateQueries({
        queryKey: nodesQueryKeys.lists(),
      })
      setPhase('done')
    } catch (error) {
      setErrorMessage(
        getServerErrorMessage(error, t(NODE_MESSAGES.FAILOVER_APPLY_FAILED))
      )
      setPhase('ready')
    }
  }

  const noRecognizedChanges =
    plan.kvChanges.length === 0 && !plan.currentCname && !plan.targetCname
  const applyDisabled = !plan.planId || !confirmed

  return (
    <Dialog
      open={props.open}
      onOpenChange={(next) => {
        if (!next && phase === 'applying') return
        props.onOpenChange(next)
      }}
    >
      <DialogContent
        className='sm:max-w-2xl'
        showCloseButton={phase !== 'applying'}
      >
        <DialogHeader>
          <DialogTitle>
            {t('Switch primary domain')}
            {node ? ` · ${node.name}` : ''}
          </DialogTitle>
          <DialogDescription>
            {t(
              'Dry-run first: review the Cloudflare CNAME and Worker KV changes, then confirm. Plans are valid for {{minutes}} minutes.',
              { minutes: PLAN_VALIDITY_MINUTES }
            )}
          </DialogDescription>
        </DialogHeader>

        {phase === 'loading' && (
          <div className='flex min-h-40 flex-col items-center justify-center gap-3'>
            <Loader2 className='text-muted-foreground size-6 animate-spin' />
            <p className='text-muted-foreground text-sm'>
              {t('Building the failover plan...')}
            </p>
          </div>
        )}

        {phase === 'plan-error' && (
          <Alert variant='destructive'>
            <CircleAlert className='size-4' />
            <AlertDescription>
              {errorMessage || t(NODE_MESSAGES.FAILOVER_PLAN_FAILED)}
            </AlertDescription>
          </Alert>
        )}

        {(phase === 'ready' ||
          phase === 'applying' ||
          phase === 'done' ||
          phase === 'failed') && (
          <div className='flex flex-col gap-4'>
            {plan.warnings.length > 0 && (
              <Alert className='border-warning/40 bg-warning/10 text-warning'>
                <TriangleAlert className='size-4' />
                <AlertDescription>
                  <div className='flex flex-col gap-1'>
                    {plan.warnings.map((warning) => (
                      <span key={warning}>{warning}</span>
                    ))}
                  </div>
                </AlertDescription>
              </Alert>
            )}

            <div className='border-border/60 flex flex-col gap-2 rounded-xl border p-4'>
              <div className='flex items-center gap-2'>
                <Route className='text-muted-foreground size-4' />
                <span className='text-sm font-medium'>
                  {t('Cloudflare DNS')}
                </span>
                {plan.primaryDomain && (
                  <Badge variant='outline'>CNAME {plan.primaryDomain}</Badge>
                )}
              </div>
              <div className='flex flex-wrap items-center gap-2 text-sm'>
                <code className='bg-muted min-w-0 rounded px-2 py-1 font-mono text-xs break-all'>
                  {plan.currentCname || t('(not configured)')}
                </code>
                <ArrowRight className='text-muted-foreground size-4 shrink-0' />
                <code className='bg-muted min-w-0 rounded px-2 py-1 font-mono text-xs break-all'>
                  {plan.targetCname || t('(unknown target)')}
                </code>
              </div>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'The primary domain CNAME will point at the target node. The existing record is preserved in the plan output.'
                )}
              </p>
            </div>

            <div className='flex flex-col gap-2'>
              <div className='flex items-center gap-2'>
                <span className='text-sm font-medium'>
                  {t('Worker KV changes')}
                </span>
                <Badge variant='secondary'>
                  {t('{{count}} entries', { count: plan.kvChanges.length })}
                </Badge>
                {plan.kvSkipped && (
                  <Badge variant='warning'>{t('Skipped')}</Badge>
                )}
              </div>
              {plan.kvChanges.length === 0 && (
                <p className='text-muted-foreground rounded-lg border border-dashed p-3 text-xs'>
                  {t(
                    'No Worker KV changes were returned. The worker may read the node table directly from the shared database.'
                  )}
                </p>
              )}
              {plan.kvChanges.length > 0 && (
                <div className='flex max-h-48 flex-col gap-1.5 overflow-y-auto pr-1'>
                  {plan.kvChanges.map((change) => (
                    <div
                      key={kvKey(change)}
                      className='border-border/60 flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-xs'
                    >
                      <code className='font-mono font-medium break-all'>
                        {change.key}
                      </code>
                      <span className='text-muted-foreground inline-flex min-w-0 items-center gap-1 break-all'>
                        <span className='line-through opacity-70'>
                          {truncateDisplay(change.from)}
                        </span>
                        <ArrowRight className='size-3 shrink-0' />
                        <span className='font-medium'>
                          {truncateDisplay(change.to)}
                        </span>
                      </span>
                      {change.action && (
                        <Badge variant='outline'>{change.action}</Badge>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>

            {noRecognizedChanges && (
              <p className='text-muted-foreground rounded-lg border border-dashed p-3 text-xs'>
                {t(
                  'The plan returned no recognizable DNS or Worker KV fields. Rebuild the plan or review the audit log before applying.'
                )}
              </p>
            )}

            {phase === 'done' && (
              <Alert className='border-success/40 bg-success/10 text-success'>
                <CheckCircle2 className='size-4' />
                <AlertDescription>
                  <p className='font-medium'>{t('Domain failover applied')}</p>
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {t(
                      'DNS propagation and certificate issuance can take a few minutes. Run a health check afterwards.'
                    )}
                  </p>
                </AlertDescription>
              </Alert>
            )}

            {errorMessage && phase === 'ready' && (
              <Alert variant='destructive'>
                <CircleAlert className='size-4' />
                <AlertDescription>{errorMessage}</AlertDescription>
              </Alert>
            )}

            {phase === 'ready' && (
              <label className='flex items-start gap-2 rounded-lg border p-3 text-sm'>
                <Checkbox
                  checked={confirmed}
                  onCheckedChange={(checked) => setConfirmed(checked === true)}
                  aria-label={t('Confirm failover')}
                />
                <span className='min-w-0'>
                  <span className='font-medium'>
                    {t('I understand the primary domain will be switched')}
                  </span>
                  <span className='text-muted-foreground mt-0.5 block text-xs'>
                    {t(
                      'Traffic moves to the target node immediately after apply. The old record is recorded in the audit log.'
                    )}
                  </span>
                </span>
              </label>
            )}

            {phase === 'applying' && (
              <div className='text-muted-foreground flex items-center gap-2 text-sm'>
                <Loader2 className='size-4 animate-spin' />
                {t('Applying the failover plan...')}
              </div>
            )}

            {!plan.planId && phase === 'ready' && (
              <p className='text-destructive text-xs'>
                {t(
                  'The plan did not return a planId, so it cannot be applied. Rebuild the plan.'
                )}
              </p>
            )}
          </div>
        )}

        <DialogFooter>
          {phase !== 'ready' && phase !== 'applying' && (
            <Button
              type='button'
              variant='outline'
              onClick={() => props.onOpenChange(false)}
            >
              {t('Close')}
            </Button>
          )}
          {phase === 'plan-error' && (
            <Button
              type='button'
              variant='outline'
              onClick={() => void loadPlan()}
            >
              <RefreshCw className='mr-1.5 size-4' />
              {t('Retry')}
            </Button>
          )}
          {phase === 'ready' && (
            <>
              <Button
                type='button'
                variant='outline'
                onClick={() => void loadPlan()}
              >
                <RefreshCw className='mr-1.5 size-4' />
                {t('Rebuild plan')}
              </Button>
              <Button
                type='button'
                onClick={() => void handleApply()}
                disabled={applyDisabled}
              >
                {t('Confirm and switch')}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

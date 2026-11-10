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
  Rocket,
  TriangleAlert,
} from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { applyNodeDeploy, planNodeDeploy } from '../api'
import {
  NODE_MESSAGES,
  NODE_SUCCESS_MESSAGES,
  PLAN_VALIDITY_MINUTES,
} from '../constants'
import {
  maskPlanValue,
  nodesQueryKeys,
  normalizeDeployApplyResult,
  normalizeDeployPlan,
} from '../lib'
import type {
  DeployAction,
  DeployApplyResult,
  DeployPlan,
  ManagedNode,
} from '../types'

type DeployDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow: ManagedNode | null
}

type Phase = 'loading' | 'plan-error' | 'ready' | 'applying' | 'done' | 'failed'

const DEPLOY_ESTIMATE_SECONDS = 900

type ActionGroup = {
  op: 'add' | 'update' | 'delete' | 'other'
  labelKey: string
  badgeClassName: string
}

const ACTION_GROUPS: ActionGroup[] = [
  {
    op: 'add',
    labelKey: 'Added',
    badgeClassName:
      'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
  },
  {
    op: 'update',
    labelKey: 'Modified',
    badgeClassName:
      'border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300',
  },
  {
    op: 'delete',
    labelKey: 'Deleted',
    badgeClassName: 'border-destructive/30 bg-destructive/10 text-destructive',
  },
  {
    op: 'other',
    labelKey: 'Other changes',
    badgeClassName: 'border-border bg-muted text-muted-foreground',
  },
]

const EMPTY_PLAN: DeployPlan = {
  planId: '',
  actions: [],
  warnings: [],
  raw: null,
}

function actionKey(action: DeployAction): string {
  return [action.op, action.resource, action.variable, action.from, action.to]
    .join('|')
    .slice(0, 512)
}

export function NodeDeployDialog(props: DeployDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const node = props.currentRow

  const [phase, setPhase] = useState<Phase>('loading')
  const [plan, setPlan] = useState<DeployPlan>(EMPTY_PLAN)
  const [errorMessage, setErrorMessage] = useState('')
  const [confirmName, setConfirmName] = useState('')
  const [elapsedSeconds, setElapsedSeconds] = useState(0)
  const [applyResult, setApplyResult] = useState<DeployApplyResult | null>(null)
  const [applyWarnings, setApplyWarnings] = useState<string[]>([])

  const loadPlan = useCallback(async () => {
    if (!node) return
    setPhase('loading')
    setPlan(EMPTY_PLAN)
    setErrorMessage('')
    setConfirmName('')
    setElapsedSeconds(0)
    setApplyResult(null)
    setApplyWarnings([])
    try {
      const result = await planNodeDeploy(node.id)
      if (!result.success) {
        setErrorMessage(
          getServerErrorMessage(result, t(NODE_MESSAGES.DEPLOY_PLAN_FAILED))
        )
        setPhase('plan-error')
        return
      }
      const normalized = normalizeDeployPlan(result.data)
      setPlan(normalized)
      setPhase('ready')
    } catch (error) {
      setErrorMessage(
        getServerErrorMessage(error, t(NODE_MESSAGES.DEPLOY_PLAN_FAILED))
      )
      setPhase('plan-error')
    }
  }, [node, t])

  useEffect(() => {
    if (!props.open) return
    void loadPlan()
  }, [props.open, loadPlan])

  // The apply request is synchronous on the backend: it waits for Railway and
  // returns the final deployment_status. Track elapsed time so the dialog still
  // communicates progress during that long request.
  useEffect(() => {
    if (phase !== 'applying') return
    setElapsedSeconds(0)
    const timer = window.setInterval(() => {
      setElapsedSeconds((current) => current + 1)
    }, 1000)
    return () => {
      window.clearInterval(timer)
    }
  }, [phase])

  const handleApply = async () => {
    if (!node || !plan.planId || confirmName !== node.name) return
    setPhase('applying')
    setErrorMessage('')
    setApplyResult(null)
    setApplyWarnings([])
    try {
      const result = await applyNodeDeploy(plan.planId)
      if (!result.success) {
        setErrorMessage(
          getServerErrorMessage(result, t(NODE_MESSAGES.DEPLOY_APPLY_FAILED))
        )
        setPhase('ready')
        return
      }
      const normalized = normalizeDeployApplyResult(result.data)
      setApplyResult(normalized)
      setApplyWarnings(normalized.warnings)
      await queryClient.invalidateQueries({
        queryKey: nodesQueryKeys.lists(),
      })
      if (normalized.status === 'failed') {
        setErrorMessage(
          t('Deployment failed with status {{status}}', {
            status: normalized.deploymentStatus || t('unknown'),
          })
        )
        setPhase('failed')
        return
      }
      setPhase('done')
      toast.success(t(NODE_SUCCESS_MESSAGES.DEPLOY_DONE))
    } catch (error) {
      setErrorMessage(
        getServerErrorMessage(error, t(NODE_MESSAGES.DEPLOY_APPLY_FAILED))
      )
      setPhase('ready')
    }
  }

  const busy = phase === 'applying'
  const planIncomplete = !plan.planId
  const confirmMatches = node !== null && confirmName === node.name
  const progressValue = Math.min(
    95,
    Math.max(5, Math.round((elapsedSeconds / DEPLOY_ESTIMATE_SECONDS) * 100))
  )

  const groupedActions = new Map<ActionGroup['op'], DeployAction[]>()
  for (const group of ACTION_GROUPS) {
    groupedActions.set(
      group.op,
      plan.actions.filter((action) => action.op === group.op)
    )
  }

  let applyHint = t('Type the node name exactly to enable Apply')
  if (planIncomplete) {
    applyHint = t(
      'The plan did not return a planId. Rebuild the plan before applying.'
    )
  } else if (confirmMatches) {
    applyHint = t('Confirmed. Apply will execute the plan above.')
  }

  const extraApplyWarnings = applyWarnings.filter(
    (warning) => !plan.warnings.includes(warning)
  )
  const progressLabel = t(
    'The backend is polling the provider deployment and will return the final status. This can take several minutes.'
  )

  let resultTitle = t('Deployment completed')
  let resultDescription = t(
    'Refresh the node list to verify the latest deployment and heartbeat.'
  )
  let resultVariant: 'success' | 'failed' = 'success'
  if (phase === 'failed') {
    resultTitle = t('Deployment failed')
    resultVariant = 'failed'
    resultDescription =
      errorMessage ||
      t('Refresh the node list to verify the latest deployment and heartbeat.')
  } else if (applyResult) {
    const parts: string[] = []
    if (applyResult.deploymentStatus) {
      parts.push(
        t('Deployment status: {{status}}', {
          status: applyResult.deploymentStatus,
        })
      )
    }
    if (applyResult.serviceDomain) {
      parts.push(
        t('Service domain: {{domain}}', {
          domain: applyResult.serviceDomain,
        })
      )
    }
    if (applyResult.domain) {
      parts.push(t('Custom domain: {{domain}}', { domain: applyResult.domain }))
    }
    if (applyResult.variablesChanged > 0) {
      parts.push(
        t('{{count}} variables changed', {
          count: applyResult.variablesChanged,
        })
      )
    }
    if (applyResult.durationMs > 0) {
      parts.push(
        t('Duration: {{seconds}}s', {
          seconds: Math.round(applyResult.durationMs / 1000),
        })
      )
    }
    if (parts.length > 0) resultDescription = parts.join(' · ')
  }

  let resultClassName: string | undefined
  if (resultVariant === 'success') {
    resultClassName = 'border-success/40 bg-success/10 text-success'
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={(next) => {
        if (!next && phase === 'applying') return
        props.onOpenChange(next)
      }}
    >
      <DialogContent
        className='sm:max-w-3xl'
        showCloseButton={phase !== 'applying'}
      >
        <DialogHeader>
          <DialogTitle>
            {t('Deploy node')}
            {node ? ` · ${node.name}` : ''}
          </DialogTitle>
          <DialogDescription>
            {t(
              'Dry-run first: review every resource and variable change below, then type the node name to apply. Plans are valid for {{minutes}} minutes.',
              { minutes: PLAN_VALIDITY_MINUTES }
            )}
          </DialogDescription>
        </DialogHeader>

        {phase === 'loading' && (
          <div className='flex min-h-40 flex-col items-center justify-center gap-3'>
            <Loader2 className='text-muted-foreground size-6 animate-spin' />
            <p className='text-muted-foreground text-sm'>
              {t('Building the dry-run deployment plan...')}
            </p>
          </div>
        )}

        {phase === 'plan-error' && (
          <div className='flex flex-col gap-3'>
            <Alert variant='destructive'>
              <CircleAlert className='size-4' />
              <AlertDescription>
                {errorMessage || t(NODE_MESSAGES.DEPLOY_PLAN_FAILED)}
              </AlertDescription>
            </Alert>
            <p className='text-muted-foreground text-xs'>
              {t(
                'The deployment planner may be unavailable or this provider is not supported yet. No changes were made.'
              )}
            </p>
          </div>
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

            <div className='flex flex-wrap items-center gap-2'>
              <Rocket className='text-muted-foreground size-4' />
              <span className='text-sm font-medium'>
                {t('Planned changes')}
              </span>
              <Badge variant='secondary'>
                {t('{{count}} actions', { count: plan.actions.length })}
              </Badge>
              {plan.warnings.length > 0 && (
                <Badge variant='warning'>
                  {t('{{count}} warnings', { count: plan.warnings.length })}
                </Badge>
              )}
            </div>

            {plan.actions.length === 0 && (
              <p className='text-muted-foreground rounded-lg border border-dashed p-4 text-center text-sm'>
                {t(
                  'No changes detected. The service already matches the target configuration.'
                )}
              </p>
            )}

            <div className='flex max-h-72 flex-col gap-3 overflow-y-auto pr-1'>
              {ACTION_GROUPS.map((group) => {
                const actions = groupedActions.get(group.op) ?? []
                if (actions.length === 0) return null
                return (
                  <div key={group.op} className='flex flex-col gap-2'>
                    <div className='flex items-center gap-2'>
                      <Badge className={group.badgeClassName}>
                        {t(group.labelKey)}
                      </Badge>
                      <span className='text-muted-foreground text-xs'>
                        {t('{{count}} items', { count: actions.length })}
                      </span>
                    </div>
                    <div className='flex flex-col gap-1.5'>
                      {actions.map((action) => (
                        <div
                          key={actionKey(action)}
                          className='border-border/60 flex flex-wrap items-center gap-x-2 gap-y-1 rounded-lg border px-3 py-2 text-xs'
                        >
                          <Badge variant='outline'>{action.resource}</Badge>
                          {action.skipped && (
                            <Badge variant='warning'>{t('Skipped')}</Badge>
                          )}
                          <code className='min-w-0 font-mono font-medium break-all'>
                            {action.variable || '—'}
                          </code>
                          <span className='text-muted-foreground inline-flex min-w-0 items-center gap-1 break-all'>
                            <span className='line-through opacity-70'>
                              {maskPlanValue(action.variable, action.from)}
                            </span>
                            <ArrowRight className='size-3 shrink-0' />
                            <span className='font-medium'>
                              {maskPlanValue(action.variable, action.to)}
                            </span>
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                )
              })}
            </div>

            {phase === 'applying' && (
              <div className='flex flex-col gap-2 rounded-lg border p-3'>
                <div className='flex items-center gap-2 text-sm font-medium'>
                  <Loader2 className='size-4 animate-spin' />
                  {t('Deployment in progress')}
                  <span className='text-muted-foreground ml-auto text-xs tabular-nums'>
                    {t('{{seconds}}s elapsed', { seconds: elapsedSeconds })}
                  </span>
                </div>
                <Progress value={progressValue} />
                <p className='text-muted-foreground text-xs'>{progressLabel}</p>
              </div>
            )}

            {(phase === 'done' || phase === 'failed') && (
              <>
                {extraApplyWarnings.length > 0 && (
                  <Alert className='border-warning/40 bg-warning/10 text-warning'>
                    <TriangleAlert className='size-4' />
                    <AlertDescription>
                      <div className='flex flex-col gap-1'>
                        {extraApplyWarnings.map((warning) => (
                          <span key={warning}>{warning}</span>
                        ))}
                      </div>
                    </AlertDescription>
                  </Alert>
                )}
                <Alert
                  variant={
                    resultVariant === 'failed' ? 'destructive' : 'default'
                  }
                  className={resultClassName}
                >
                  {resultVariant === 'failed' ? (
                    <CircleAlert className='size-4' />
                  ) : (
                    <CheckCircle2 className='size-4' />
                  )}
                  <AlertDescription>
                    <p className='font-medium'>{resultTitle}</p>
                    <p className='text-muted-foreground mt-1 text-xs'>
                      {resultDescription}
                    </p>
                  </AlertDescription>
                </Alert>
              </>
            )}

            {errorMessage && phase === 'ready' && (
              <Alert variant='destructive'>
                <CircleAlert className='size-4' />
                <AlertDescription>{errorMessage}</AlertDescription>
              </Alert>
            )}

            {phase === 'ready' && (
              <div className='flex flex-col gap-2 rounded-lg border p-3'>
                <Label htmlFor='deploy-confirm'>
                  {t('Type {{name}} to confirm', { name: node?.name ?? '' })}
                </Label>
                <Input
                  id='deploy-confirm'
                  value={confirmName}
                  onChange={(event) => setConfirmName(event.target.value)}
                  placeholder={node?.name ?? ''}
                  autoComplete='off'
                  disabled={busy}
                />
                <p className='text-muted-foreground text-xs'>{applyHint}</p>
              </div>
            )}

            {busy && (
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Applying the plan can take a few minutes. Keep this dialog open to watch progress.'
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
                disabled={!confirmMatches || planIncomplete}
              >
                <Rocket className='mr-1.5 size-4' />
                {t('Apply plan')}
              </Button>
            </>
          )}
          {phase === 'failed' && (
            <Button
              type='button'
              variant='outline'
              onClick={() => void loadPlan()}
            >
              <RefreshCw className='mr-1.5 size-4' />
              {t('Rebuild plan')}
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
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

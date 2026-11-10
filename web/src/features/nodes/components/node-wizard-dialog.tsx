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
  ArrowLeft,
  ArrowRight,
  Box,
  CheckCircle2,
  CircleAlert,
  Cloud,
  Layers,
  Loader2,
  Plane,
  Server,
  Train,
  Wifi,
  WifiOff,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

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
import { Switch } from '@/components/ui/switch'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import {
  checkNode,
  createNode,
  probeNodeServiceUrl,
  testNodeConnection,
  updateNode,
} from '../api'
import {
  NODE_MESSAGES,
  NODE_SUCCESS_MESSAGES,
  PROVIDER_GUIDE_STEP_KEYS,
  PROVIDER_OPTIONS,
  getProviderMeta,
} from '../constants'
import {
  asRecord,
  getNodeProvider,
  nodesQueryKeys,
  normalizeNodeTestResult,
} from '../lib'
import type {
  ManagedNode,
  NodePayload,
  NodeProvider,
  NodeTestResult,
} from '../types'
import { useNodes } from './nodes-provider'

type NodeWizardDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow: ManagedNode | null
}

type WizardStep = 'provider' | 'form'
type TestState = 'idle' | 'testing' | NodeTestResult['status']

type FormState = {
  name: string
  apiToken: string
  serviceUrl: string
  projectId: string
  serviceId: string
  domain: string
  enabled: boolean
}

const EMPTY_FORM: FormState = {
  name: '',
  apiToken: '',
  serviceUrl: '',
  projectId: '',
  serviceId: '',
  domain: '',
  enabled: true,
}

const PROVIDER_ICONS = {
  railway: Train,
  koyeb: Cloud,
  render: Layers,
  fly: Plane,
  sealos: Box,
  clawcloud: Cloud,
  other: Server,
} as const

function extractCreatedNodeId(value: unknown): number | null {
  const record = asRecord(value)
  if (!record) return null
  const direct = record.id
  if (typeof direct === 'number' && direct > 0) return direct
  if (typeof direct === 'string' && Number(direct) > 0) return Number(direct)
  const node = asRecord(record.node)
  if (node && typeof node.id === 'number' && node.id > 0) return node.id
  return null
}

function isValidHttpUrl(value: string): boolean {
  return /^https?:\/\/[^\s]+$/i.test(value.trim())
}

export function NodeWizardDialog(props: NodeWizardDialogProps) {
  const { t } = useTranslation()
  const { setOpen } = useNodes()
  const queryClient = useQueryClient()
  const isUpdate = props.currentRow !== null

  const [step, setStep] = useState<WizardStep>('provider')
  const [provider, setProvider] = useState<NodeProvider>('railway')
  const [form, setForm] = useState<FormState>(EMPTY_FORM)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [testState, setTestState] = useState<TestState>('idle')
  const [testMessage, setTestMessage] = useState('')
  const [testLatency, setTestLatency] = useState<number | null>(null)
  const [isSaving, setIsSaving] = useState(false)

  useEffect(() => {
    if (!props.open) return
    if (props.currentRow) {
      const row = props.currentRow
      setStep('form')
      setProvider(getNodeProvider(row.provider))
      setForm({
        name: row.name,
        apiToken: '',
        serviceUrl: row.serviceUrl,
        projectId: row.projectId,
        serviceId: row.serviceId,
        domain: row.domain,
        enabled: row.enabled,
      })
    } else {
      setStep('provider')
      setProvider('railway')
      setForm(EMPTY_FORM)
    }
    setErrors({})
    setTestState('idle')
    setTestMessage('')
    setTestLatency(null)
    setIsSaving(false)
  }, [props.open, props.currentRow])

  const meta = getProviderMeta(provider)
  const ProviderIcon = PROVIDER_ICONS[provider]

  const updateField = <K extends keyof FormState>(
    field: K,
    value: FormState[K]
  ) => {
    setForm((current) => ({ ...current, [field]: value }))
    setErrors((current) => {
      const next = { ...current }
      delete next[field]
      return next
    })
    // Any credential change invalidates the previous test result.
    setTestState('idle')
    setTestMessage('')
    setTestLatency(null)
  }

  const buildPayload = (): NodePayload => {
    const payload: NodePayload = {
      name: form.name.trim(),
      provider,
      service_url: form.serviceUrl.trim(),
      project_id: form.projectId.trim(),
      service_id: form.serviceId.trim(),
      domain: form.domain.trim(),
      enabled: form.enabled,
    }
    if (form.apiToken.trim()) {
      payload.api_token = form.apiToken.trim()
    }
    return payload
  }

  const validate = (): boolean => {
    const next: Record<string, string> = {}
    if (!form.name.trim()) {
      next.name = t('Please enter a node name')
    }
    if (!isUpdate && !form.apiToken.trim()) {
      next.apiToken = t('Please enter the provider API token')
    }
    if (meta.requiresServiceUrl && !form.serviceUrl.trim()) {
      next.serviceUrl = t('This provider requires a service URL')
    }
    if (form.serviceUrl.trim() && !isValidHttpUrl(form.serviceUrl)) {
      next.serviceUrl = t('Service URL must start with http:// or https://')
    }
    setErrors(next)
    return Object.keys(next).length === 0
  }

  const handleTest = async () => {
    if (!validate()) return
    setTestState('testing')
    setTestMessage(t('Testing connection...'))
    setTestLatency(null)
    try {
      const result = await testNodeConnection(buildPayload())
      const normalized = normalizeNodeTestResult(
        result,
        t(NODE_MESSAGES.TEST_FAILED)
      )
      setTestState(normalized.status)
      setTestMessage(normalized.message)
      setTestLatency(normalized.latencyMs)
    } catch {
      // Backend test endpoint missing/unreachable: try the service URL from
      // the browser. CORS failures are a warning, not a hard block.
      const probe = await probeNodeServiceUrl(form.serviceUrl)
      if (probe) {
        setTestState(probe.status)
        setTestMessage(probe.message)
        setTestLatency(probe.latencyMs)
      } else {
        setTestState('warning')
        setTestMessage(
          t(
            'The backend test endpoint is unavailable and the browser probe was blocked or timed out. You can save and run a health check afterwards.'
          )
        )
      }
    }
  }

  const handleSave = async () => {
    if (!validate()) return
    if (!isUpdate && testState !== 'success' && testState !== 'warning') {
      setErrors((current) => ({
        ...current,
        submit: t('Run Test connection before saving this node'),
      }))
      return
    }
    setIsSaving(true)
    try {
      const payload = buildPayload()
      const result =
        isUpdate && props.currentRow
          ? await updateNode(props.currentRow.id, payload)
          : await createNode(payload)

      if (!result.success) {
        handleServerError(
          result,
          t(
            isUpdate ? NODE_MESSAGES.UPDATE_FAILED : NODE_MESSAGES.CREATE_FAILED
          )
        )
        return
      }

      toast.success(
        t(
          isUpdate
            ? NODE_SUCCESS_MESSAGES.UPDATED
            : NODE_SUCCESS_MESSAGES.CREATED
        )
      )
      await queryClient.invalidateQueries({
        queryKey: nodesQueryKeys.lists(),
      })

      if (!isUpdate) {
        // Design §3.2: run the first health check automatically after save.
        const createdId = extractCreatedNodeId(result.data)
        if (createdId) {
          try {
            await checkNode(createdId)
            await queryClient.invalidateQueries({
              queryKey: nodesQueryKeys.lists(),
            })
          } catch {
            // A failed first check must not undo a successful creation.
          }
        }
      }
      setOpen(null)
    } catch (error) {
      handleServerError(error, t(NODE_MESSAGES.UNEXPECTED))
    } finally {
      setIsSaving(false)
    }
  }

  const saveDisabled =
    isSaving ||
    (!isUpdate && testState !== 'success' && testState !== 'warning')

  let tokenPlaceholder = t('Paste the provider API token')
  if (isUpdate) {
    tokenPlaceholder = t('Leave blank to keep the existing token')
    if (props.currentRow?.apiTokenMasked) {
      tokenPlaceholder = t('Configured ({{hint}}) — leave blank to keep', {
        hint: props.currentRow.apiTokenMasked,
      })
    }
  }

  let submitLabel = t('Create Node')
  if (isUpdate) {
    submitLabel = t('Save Changes')
  }
  if (isSaving) {
    submitLabel = t('Saving...')
  }

  let testStatusNode = null
  if (testState === 'success') {
    testStatusNode = (
      <p className='text-success flex items-center gap-1.5 text-xs'>
        <CheckCircle2 className='size-4' />
        {testMessage}
        {testLatency !== null && ` · ${testLatency} ms`}
      </p>
    )
  } else if (testState === 'warning') {
    testStatusNode = (
      <p className='text-warning flex items-start gap-1.5 text-xs'>
        <CircleAlert className='mt-0.5 size-4 shrink-0' />
        <span>{testMessage}</span>
      </p>
    )
  } else if (testState === 'failed') {
    testStatusNode = (
      <p className='text-destructive flex items-start gap-1.5 text-xs'>
        <WifiOff className='mt-0.5 size-4 shrink-0' />
        <span>{testMessage}</span>
      </p>
    )
  } else if (testState === 'testing') {
    testStatusNode = (
      <p className='text-muted-foreground flex items-center gap-1.5 text-xs'>
        <Loader2 className='size-4 animate-spin' />
        {testMessage}
      </p>
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={(next) => {
        if (!next && isSaving) return
        props.onOpenChange(next)
      }}
    >
      <DialogContent className='sm:max-w-2xl' showCloseButton={!isSaving}>
        <DialogHeader>
          <DialogTitle>{isUpdate ? t('Edit Node') : t('Add Node')}</DialogTitle>
          <DialogDescription>
            {isUpdate
              ? t(
                  'Update the connection details of this node. The API token is never shown; leave it blank to keep the existing token.'
                )
              : t(
                  'Create node records for services you registered manually. The platform never registers accounts for you.'
                )}
          </DialogDescription>
        </DialogHeader>

        {step === 'provider' && !isUpdate ? (
          <div className='flex flex-col gap-4'>
            <div className='grid grid-cols-2 gap-2 sm:grid-cols-3'>
              {PROVIDER_OPTIONS.map((option) => {
                const Icon = PROVIDER_ICONS[option.value]
                const selected = provider === option.value
                return (
                  <Button
                    key={option.value}
                    type='button'
                    variant='outline'
                    onClick={() => {
                      setProvider(option.value)
                      setTestState('idle')
                      setTestMessage('')
                    }}
                    className={cn(
                      'h-auto justify-start gap-2 px-3 py-2.5',
                      selected &&
                        'border-primary bg-primary/5 ring-primary/30 ring-1'
                    )}
                  >
                    <Icon className='size-4 shrink-0' />
                    <span className='truncate text-sm'>{option.label}</span>
                  </Button>
                )
              })}
            </div>

            <div className='border-border/60 rounded-xl border p-4'>
              <div className='flex flex-wrap items-center gap-2'>
                <ProviderIcon className='text-muted-foreground size-4' />
                <span className='text-sm font-medium'>{meta.label}</span>
                <Badge variant='outline'>{t('Manual onboarding')}</Badge>
              </div>
              <p className='text-muted-foreground mt-2 text-xs'>
                {t(meta.hintKey)}
              </p>
              <ol className='mt-3 flex list-decimal flex-col gap-1.5 pl-5 text-xs'>
                {PROVIDER_GUIDE_STEP_KEYS.map((key) => (
                  <li key={key} className='text-muted-foreground'>
                    {t(key)}
                  </li>
                ))}
              </ol>
            </div>

            <DialogFooter>
              <Button
                type='button'
                variant='outline'
                onClick={() => props.onOpenChange(false)}
              >
                {t('Cancel')}
              </Button>
              <Button
                type='button'
                onClick={() => {
                  setStep('form')
                  setForm((current) => ({
                    ...current,
                    name: current.name || meta.label,
                  }))
                }}
              >
                {t('Continue')}
                <ArrowRight className='ml-1.5 size-4' />
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <div className='flex flex-col gap-4'>
            {!isUpdate && (
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='text-muted-foreground -ml-2 w-fit'
                onClick={() => {
                  setStep('provider')
                  setTestState('idle')
                  setTestMessage('')
                }}
              >
                <ArrowLeft className='mr-1.5 size-4' />
                {t('Change provider')}
              </Button>
            )}

            <div className='bg-muted/30 flex items-start gap-2 rounded-lg border p-3'>
              <ProviderIcon className='text-muted-foreground mt-0.5 size-4 shrink-0' />
              <div className='min-w-0'>
                <div className='flex items-center gap-2'>
                  <span className='text-sm font-medium'>{meta.label}</span>
                  <Badge variant='outline' className={meta.badgeClassName}>
                    {t('Provider')}
                  </Badge>
                </div>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t(meta.hintKey)}
                </p>
              </div>
            </div>

            <div className='grid gap-4 sm:grid-cols-2'>
              <div className='flex flex-col gap-1.5'>
                <Label htmlFor='node-name'>
                  {t('Node name')}
                  <span className='text-destructive ml-0.5'>*</span>
                </Label>
                <Input
                  id='node-name'
                  value={form.name}
                  onChange={(event) => updateField('name', event.target.value)}
                  placeholder={t('e.g. railway-tokyo-1')}
                  maxLength={64}
                  aria-invalid={Boolean(errors.name)}
                />
                {errors.name && (
                  <p className='text-destructive text-xs'>{errors.name}</p>
                )}
              </div>

              <div className='flex flex-col gap-1.5'>
                <Label htmlFor='node-token'>
                  {t('API token')}
                  {!isUpdate && (
                    <span className='text-destructive ml-0.5'>*</span>
                  )}
                </Label>
                <Input
                  id='node-token'
                  type='password'
                  autoComplete='new-password'
                  value={form.apiToken}
                  onChange={(event) =>
                    updateField('apiToken', event.target.value)
                  }
                  placeholder={tokenPlaceholder}
                  aria-invalid={Boolean(errors.apiToken)}
                />
                {errors.apiToken && (
                  <p className='text-destructive text-xs'>{errors.apiToken}</p>
                )}
                <p className='text-muted-foreground text-xs'>
                  {t('Stored encrypted server-side and never displayed again.')}
                </p>
              </div>

              <div className='flex flex-col gap-1.5'>
                <Label htmlFor='node-service-url'>
                  {t('Service URL')}
                  {meta.requiresServiceUrl && (
                    <span className='text-destructive ml-0.5'>*</span>
                  )}
                </Label>
                <Input
                  id='node-service-url'
                  type='url'
                  inputMode='url'
                  value={form.serviceUrl}
                  onChange={(event) =>
                    updateField('serviceUrl', event.target.value)
                  }
                  placeholder='https://example.up.railway.app'
                  aria-invalid={Boolean(errors.serviceUrl)}
                />
                {errors.serviceUrl && (
                  <p className='text-destructive text-xs'>
                    {errors.serviceUrl}
                  </p>
                )}
              </div>

              <div className='flex flex-col gap-1.5'>
                <Label htmlFor='node-domain'>{t('Custom domain')}</Label>
                <Input
                  id='node-domain'
                  value={form.domain}
                  onChange={(event) =>
                    updateField('domain', event.target.value)
                  }
                  placeholder='gate.example.com'
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'The domain users call. Required before a domain failover can be planned.'
                  )}
                </p>
              </div>

              <div className='flex flex-col gap-1.5'>
                <Label htmlFor='node-project-id'>{t('Project ID')}</Label>
                <Input
                  id='node-project-id'
                  value={form.projectId}
                  onChange={(event) =>
                    updateField('projectId', event.target.value)
                  }
                  placeholder={t('Optional, provider project identifier')}
                />
              </div>

              <div className='flex flex-col gap-1.5'>
                <Label htmlFor='node-service-id'>{t('Service ID')}</Label>
                <Input
                  id='node-service-id'
                  value={form.serviceId}
                  onChange={(event) =>
                    updateField('serviceId', event.target.value)
                  }
                  placeholder={t('Optional, provider service identifier')}
                />
              </div>
            </div>

            <div className='flex items-center justify-between gap-4 rounded-lg border p-3'>
              <div className='min-w-0'>
                <p className='text-sm font-medium'>{t('Enabled')}</p>
                <p className='text-muted-foreground text-xs'>
                  {t('Disabled nodes are skipped by failover and alerts.')}
                </p>
              </div>
              <Switch
                checked={form.enabled}
                onCheckedChange={(checked) => updateField('enabled', checked)}
                aria-label={t('Enabled')}
              />
            </div>

            <div className='flex flex-col gap-2 rounded-lg border p-3'>
              <div className='flex flex-wrap items-center gap-2'>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={() => void handleTest()}
                  disabled={testState === 'testing' || isSaving}
                >
                  {testState === 'testing' ? (
                    <Loader2 className='mr-1.5 size-4 animate-spin' />
                  ) : (
                    <Wifi className='mr-1.5 size-4' />
                  )}
                  {t('Test connection')}
                </Button>
                <span className='text-muted-foreground text-xs'>
                  {t(
                    'A test is required before the first save. A failed test blocks saving.'
                  )}
                </span>
              </div>
              <div aria-live='polite'>{testStatusNode}</div>
              {errors.submit && (
                <p className='text-destructive text-xs'>{errors.submit}</p>
              )}
            </div>

            <DialogFooter>
              <Button
                type='button'
                variant='outline'
                onClick={() => props.onOpenChange(false)}
                disabled={isSaving}
              >
                {t('Cancel')}
              </Button>
              <Button
                type='button'
                onClick={() => void handleSave()}
                disabled={saveDisabled}
              >
                {isSaving && <Loader2 className='mr-1.5 size-4 animate-spin' />}
                {submitLabel}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

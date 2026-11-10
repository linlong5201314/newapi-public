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
import { ArrowLeft, Info, Loader2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { getChannels } from '@/features/my-channels/api'
import {
  CHANNEL_TYPE_TRAE,
  CHANNEL_TYPE_WORKBUDDY,
} from '@/features/my-channels/constants'
import { cn } from '@/lib/utils'

import {
  CHECKIN_CHANNEL_SITES,
  CHECKIN_SECRET_FIELD_KEYS,
} from '../constants'
import type {
  CheckinAccountPayload,
  CheckinSiteCredField,
  CheckinSiteTemplate,
} from '../types'
import { getCheckinSiteIcon } from '../lib'

/** Map a channel-referencing check-in site onto its channel type number. */
const SITE_CHANNEL_TYPE: Record<string, number> = {
  workbuddy: CHANNEL_TYPE_WORKBUDDY,
  trae: CHANNEL_TYPE_TRAE,
}

type CheckinAddDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  sites: CheckinSiteTemplate[]
  submitting?: boolean
  onSubmit: (payload: CheckinAccountPayload) => void
}

type WizardStep = 'site' | 'form'

function CredFieldInput(props: {
  field: CheckinSiteCredField
  value: string
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const { field, value, onChange } = props
  const label = field.label ? t(field.label) : field.key
  const inputId = `checkin-cred-${field.key}`

  let control
  if (field.type === 'select' && field.options?.length) {
    control = (
      <Select
        items={field.options.map((option) => ({
          value: option.value,
          label: t(option.label),
        }))}
        value={value}
        onValueChange={(next) => onChange(next ?? '')}
      >
        <SelectTrigger id={inputId} className='w-full'>
          <SelectValue placeholder={t('Please select')} />
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false}>
          <SelectGroup>
            {field.options.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {t(option.label)}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    )
  } else if (field.type === 'textarea') {
    control = (
      <Textarea
        id={inputId}
        rows={4}
        placeholder={field.placeholder}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    )
  } else {
    const isSecret =
      field.type === 'password' || CHECKIN_SECRET_FIELD_KEYS.has(field.key)
    let inputType = 'text'
    if (isSecret) inputType = 'password'
    else if (field.type === 'number') inputType = 'number'
    control = (
      <Input
        id={inputId}
        type={inputType}
        placeholder={field.placeholder}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    )
  }

  return (
    <div className='space-y-1.5'>
      <Label htmlFor={inputId}>
        {label}
        {field.required && <span className='text-destructive ml-0.5'>*</span>}
      </Label>
      {control}
      {field.help && (
        <p className='text-muted-foreground text-xs'>{t(field.help)}</p>
      )}
    </div>
  )
}

export function CheckinAddDialog(props: CheckinAddDialogProps) {
  const { t } = useTranslation()
  const [step, setStep] = useState<WizardStep>('site')
  const [site, setSite] = useState<CheckinSiteTemplate | null>(null)
  const [label, setLabel] = useState('')
  const [channelId, setChannelId] = useState('')
  const [credValues, setCredValues] = useState<Record<string, string>>({})
  const [agentUrl, setAgentUrl] = useState('')
  const [mode, setMode] = useState('api')
  const [enabled, setEnabled] = useState(true)
  const [validationError, setValidationError] = useState('')

  const reset = () => {
    setStep('site')
    setSite(null)
    setLabel('')
    setChannelId('')
    setCredValues({})
    setAgentUrl('')
    setMode('api')
    setEnabled(true)
    setValidationError('')
  }

  useEffect(() => {
    if (!props.open) reset()
  }, [props.open])

  const needsChannel = site ? CHECKIN_CHANNEL_SITES.has(site.site) : false
  const channelType = site ? SITE_CHANNEL_TYPE[site.site] : undefined

  // Own channels filtered to the referenced type; fail-soft (empty list).
  const channelsQuery = useQuery({
    queryKey: ['checkin-channel-options', channelType],
    queryFn: async () => {
      const res = await getChannels({ type: channelType, page_size: 100 })
      return res.data?.items ?? []
    },
    enabled: props.open && needsChannel && channelType !== undefined,
    staleTime: 30 * 1000,
    meta: { errorToast: false },
  })
  const channelOptions = useMemo(
    () =>
      (channelsQuery.data ?? []).map((channel) => ({
        value: String(channel.id),
        label: `#${channel.id} ${channel.name}`,
      })),
    [channelsQuery.data]
  )

  const effectiveMode = needsChannel ? 'api' : mode

  const selectSite = (next: CheckinSiteTemplate) => {
    setSite(next)
    setMode(next.mode || 'api')
    setLabel((current) => current || next.name)
    setStep('form')
    setValidationError('')
  }

  const validate = (): string => {
    if (!site) return t('Please select a site first')
    if (!label.trim()) return t('Please enter an account label')
    if (needsChannel && !channelId) {
      return t('Please select a channel to reference')
    }
    if (effectiveMode === 'agent' && !agentUrl.trim()) {
      return t('Please enter the agent executor URL')
    }
    for (const field of site.credFields) {
      if (field.required && !(credValues[field.key] ?? '').trim()) {
        const fieldLabel = field.label ? t(field.label) : field.key
        return t('{{field}} is required', { field: fieldLabel })
      }
    }
    return ''
  }

  const handleSubmit = () => {
    const error = validate()
    if (error) {
      setValidationError(error)
      return
    }
    if (!site) return
    const credJson = needsChannel
      ? undefined
      : JSON.stringify(
          Object.fromEntries(
            Object.entries(credValues).filter(([, value]) => value !== '')
          )
        )
    props.onSubmit({
      site: site.site,
      label: label.trim(),
      channel_id: needsChannel ? Number(channelId) : 0,
      cred_json: credJson === '{}' ? undefined : credJson,
      mode: effectiveMode,
      agent_url: effectiveMode === 'agent' ? agentUrl.trim() : undefined,
      enabled,
    })
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('Add check-in account')}</DialogTitle>
          <DialogDescription>
            {step === 'site'
              ? t('Pick a site template to continue.')
              : t('Fill in the credentials and schedule options.')}
          </DialogDescription>
        </DialogHeader>

        {step === 'site' && (
          <div className='grid gap-3 sm:grid-cols-2'>
            {props.sites.map((template) => {
              const Icon = getCheckinSiteIcon(template.site, template)
              const selected = site?.site === template.site
              return (
                <button
                  key={template.site}
                  type='button'
                  onClick={() => selectSite(template)}
                  className={cn(
                    'hover:border-primary/60 flex flex-col gap-2 rounded-lg border p-4 text-left transition-colors',
                    selected && 'border-primary'
                  )}
                >
                  <span className='flex items-center gap-2'>
                    <span className='bg-muted text-muted-foreground flex size-8 items-center justify-center rounded-md'>
                      <Icon className='size-4' aria-hidden='true' />
                    </span>
                    <span className='font-medium'>{template.name}</span>
                    <span className='text-muted-foreground ml-auto text-xs uppercase'>
                      {template.mode}
                    </span>
                  </span>
                  {template.guide && (
                    <span className='text-muted-foreground line-clamp-3 text-xs'>
                      {t(template.guide)}
                    </span>
                  )}
                </button>
              )
            })}
            {props.sites.length === 0 && (
              <p className='text-muted-foreground col-span-full text-sm'>
                {t('No check-in sites available.')}
              </p>
            )}
          </div>
        )}

        {step === 'form' && site && (
          <div className='flex max-h-[60vh] flex-col gap-4 overflow-y-auto pr-1'>
            {site.guide && (
              <Alert>
                <Info className='h-4 w-4' />
                <AlertDescription>{t(site.guide)}</AlertDescription>
              </Alert>
            )}

            <div className='space-y-1.5'>
              <Label htmlFor='checkin-label'>
                {t('Label')}
                <span className='text-destructive ml-0.5'>*</span>
              </Label>
              <Input
                id='checkin-label'
                value={label}
                onChange={(event) => setLabel(event.target.value)}
                placeholder={t('e.g., My main account')}
              />
            </div>

            {needsChannel && (
              <div className='space-y-1.5'>
                <Label>
                  {t('Reference channel')}
                  <span className='text-destructive ml-0.5'>*</span>
                </Label>
                <Select
                  items={channelOptions}
                  value={channelId}
                  onValueChange={(next) => setChannelId(next ?? '')}
                >
                  <SelectTrigger className='w-full'>
                    <SelectValue
                      placeholder={
                        channelsQuery.isLoading
                          ? t('Loading channels...')
                          : t('Select one of your channels')
                      }
                    />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {channelOptions.map((option) => (
                        <SelectItem key={option.value} value={option.value}>
                          {option.label}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                {!channelsQuery.isLoading && channelOptions.length === 0 && (
                  <p className='text-warning text-xs'>
                    {t(
                      'No matching channel yet. Create one in My Channels first, then reference it here.'
                    )}
                  </p>
                )}
              </div>
            )}

            {!needsChannel && (
              <div className='space-y-1.5'>
                <Label>{t('Mode')}</Label>
                <Select
                  items={[
                    { value: 'api', label: t('API (server executes)') },
                    { value: 'agent', label: t('Agent (external executor)') },
                  ]}
                  value={mode}
                  onValueChange={(next) => setMode(next ?? 'api')}
                >
                  <SelectTrigger className='w-full'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='api'>
                        {t('API (server executes)')}
                      </SelectItem>
                      <SelectItem value='agent'>
                        {t('Agent (external executor)')}
                      </SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
            )}

            {site.credFields.map((field) => (
              <CredFieldInput
                key={field.key}
                field={field}
                value={credValues[field.key] ?? ''}
                onChange={(value) =>
                  setCredValues((current) => ({ ...current, [field.key]: value }))
                }
              />
            ))}

            {effectiveMode === 'agent' && (
              <div className='space-y-1.5'>
                <Label htmlFor='checkin-agent-url'>
                  {t('Agent executor URL')}
                  <span className='text-destructive ml-0.5'>*</span>
                </Label>
                <Input
                  id='checkin-agent-url'
                  value={agentUrl}
                  onChange={(event) => setAgentUrl(event.target.value)}
                  placeholder='https://agent.example.com/run'
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'The server POSTs to this URL when the scheduled run fires.'
                  )}
                </p>
              </div>
            )}

            <div className='flex items-center justify-between rounded-lg border px-4 py-3'>
              <div className='space-y-0.5'>
                <Label>{t('Enable scheduled check-in')}</Label>
                <p className='text-muted-foreground text-xs'>
                  {t('The platform sweeps enabled accounts twice daily (CST).')}
                </p>
              </div>
              <Switch checked={enabled} onCheckedChange={setEnabled} />
            </div>

            {validationError && (
              <p className='text-destructive text-sm'>{validationError}</p>
            )}
          </div>
        )}

        <DialogFooter>
          {step === 'form' && (
            <Button
              type='button'
              variant='outline'
              onClick={() => setStep('site')}
              disabled={props.submitting}
            >
              <ArrowLeft className='mr-2 size-4' />
              {t('Back')}
            </Button>
          )}
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={props.submitting}
          >
            {t('Cancel')}
          </Button>
          {step === 'form' && (
            <Button
              type='button'
              onClick={handleSubmit}
              disabled={props.submitting}
            >
              {props.submitting && (
                <Loader2 className='mr-2 size-4 animate-spin' />
              )}
              {t('Add account')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { BellRing, CircleAlert, CloudCog, Info, KeyRound } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { TagInput } from '@/components/tag-input'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { getNodeSettings, updateNodeSettings } from '@/features/nodes/api'
import {
  NODE_MESSAGES,
  NODE_SUCCESS_MESSAGES,
} from '@/features/nodes/constants'
import { nodesQueryKeys, normalizeNodeSettings } from '@/features/nodes/lib'
import type { NodeSettings, NodeSettingsPayload } from '@/features/nodes/types'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'

type NodeSettingsFormValues = {
  balanceAlertThreshold: number
  alertWebhookUrl: string
  primaryDomain: string
  nodeDenylist: string[]
  nodeAllowlist: string[]
  nodeAutoFailover: boolean
  cfApiToken: string
  cfKvNamespace: string
  cfZoneId: string
  cfAccountId: string
}

const EMPTY_SETTINGS: NodeSettings = {
  balanceAlertThreshold: 1,
  alertWebhookUrl: '',
  primaryDomain: '',
  nodeDenylist: [],
  nodeAllowlist: [],
  nodeAutoFailover: false,
  cfApiToken: '',
  cfKvNamespace: '',
  cfZoneId: '',
  cfAccountId: '',
  cfTokenConfigured: false,
  cfKvNamespaceConfigured: false,
  cfZoneIdConfigured: false,
  cfAccountIdConfigured: false,
  smtpConfigured: false,
}

function toFormValues(settings: NodeSettings): NodeSettingsFormValues {
  return {
    balanceAlertThreshold: settings.balanceAlertThreshold,
    alertWebhookUrl: settings.alertWebhookUrl,
    primaryDomain: settings.primaryDomain,
    nodeDenylist: settings.nodeDenylist,
    nodeAllowlist: settings.nodeAllowlist,
    nodeAutoFailover: settings.nodeAutoFailover,
    cfApiToken: '',
    cfKvNamespace: '',
    cfZoneId: '',
    cfAccountId: '',
  }
}

function createSchema(t: (key: string) => string) {
  return z.object({
    balanceAlertThreshold: z
      .number()
      .min(0, t('Balance alert threshold cannot be negative')),
    alertWebhookUrl: z
      .string()
      .refine(
        (value) => !value.trim() || /^https?:\/\//i.test(value.trim()),
        t('Webhook URL must start with http:// or https://')
      ),
    primaryDomain: z.string(),
    nodeDenylist: z.array(z.string()),
    nodeAllowlist: z.array(z.string()),
    nodeAutoFailover: z.boolean(),
    cfApiToken: z.string(),
    cfKvNamespace: z.string(),
    cfZoneId: z.string(),
    cfAccountId: z.string(),
  })
}

function ConfiguredBadge({
  configured,
  label,
}: {
  configured: boolean
  label: string
}) {
  const { t } = useTranslation()
  return (
    <Badge variant={configured ? 'secondary' : 'outline'}>
      {configured ? t('Configured') : t('Not configured')}
      <span className='sr-only'>{t(label)}</span>
    </Badge>
  )
}

export function NodeSettingsSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [isSaving, setIsSaving] = useState(false)
  const schema = useMemo(() => createSchema(t), [t])

  const settingsQuery = useQuery({
    queryKey: nodesQueryKeys.settings(),
    queryFn: async () => {
      try {
        const result = await getNodeSettings()
        if (!result.success) {
          return {
            settings: EMPTY_SETTINGS,
            error: getServerErrorMessage(
              result,
              t(NODE_MESSAGES.SETTINGS_LOAD_FAILED)
            ),
          }
        }
        return {
          settings: normalizeNodeSettings(result.data),
          error: '',
        }
      } catch (error) {
        return {
          settings: EMPTY_SETTINGS,
          error: getServerErrorMessage(
            error,
            t(NODE_MESSAGES.SETTINGS_LOAD_FAILED)
          ),
        }
      }
    },
    staleTime: 30_000,
  })

  const settings = settingsQuery.data?.settings ?? EMPTY_SETTINGS
  const loadError = settingsQuery.data?.error ?? ''
  const formDefaults = useMemo(() => toFormValues(settings), [settings])

  const form = useForm<NodeSettingsFormValues>({
    resolver: zodResolver(schema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const onSubmit = async (values: NodeSettingsFormValues) => {
    setIsSaving(true)
    try {
      const payload: NodeSettingsPayload = {
        balanceAlertThreshold: values.balanceAlertThreshold,
        alertWebhookUrl: values.alertWebhookUrl.trim(),
        primaryDomain: values.primaryDomain.trim(),
        nodeDenylist: values.nodeDenylist,
        nodeAllowlist: values.nodeAllowlist,
        autoFailover: values.nodeAutoFailover,
      }
      if (values.cfApiToken.trim()) {
        payload.cloudflareApiToken = values.cfApiToken.trim()
      }
      if (values.cfKvNamespace.trim()) {
        payload.cloudflareKvNamespaceId = values.cfKvNamespace.trim()
      }
      if (values.cfZoneId.trim()) {
        payload.cloudflareZoneId = values.cfZoneId.trim()
      }
      if (values.cfAccountId.trim()) {
        payload.cloudflareAccountId = values.cfAccountId.trim()
      }

      const result = await updateNodeSettings(payload)
      if (!result.success) {
        toast.error(
          getServerErrorMessage(result, t(NODE_MESSAGES.SETTINGS_SAVE_FAILED))
        )
        return
      }
      toast.success(t(NODE_SUCCESS_MESSAGES.SETTINGS_SAVED))
      await queryClient.invalidateQueries({
        queryKey: nodesQueryKeys.settings(),
      })
      form.reset({
        ...values,
        cfApiToken: '',
        cfKvNamespace: '',
        cfZoneId: '',
        cfAccountId: '',
      })
    } catch (error) {
      toast.error(
        getServerErrorMessage(error, t(NODE_MESSAGES.SETTINGS_SAVE_FAILED))
      )
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <SettingsSection title={t('Nodes & Alerts')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            onReset={() => form.reset(formDefaults)}
            isSaving={isSaving}
            saveLabel='Save node settings'
          />

          {loadError && (
            <Alert className='border-warning/40 bg-warning/10 text-warning'>
              <CircleAlert className='size-4' />
              <AlertDescription>
                <p className='font-medium'>
                  {t('Node settings API is unavailable')}
                </p>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {loadError}{' '}
                  {t(
                    'Defaults are shown. Saving will retry the endpoint once the backend is deployed.'
                  )}
                </p>
              </AlertDescription>
            </Alert>
          )}

          <Alert>
            <Info className='size-4' />
            <AlertDescription>
              {settings.smtpConfigured
                ? t(
                    'SMTP is configured, so low-balance alerts can be emailed as well as sent to the webhook.'
                  )
                : t(
                    'SMTP is not configured. Low-balance alerts will only use the webhook URL until email is configured.'
                  )}
            </AlertDescription>
          </Alert>

          <div className='flex items-center gap-2 lg:col-span-2'>
            <BellRing className='text-muted-foreground size-4' />
            <h4 className='text-sm font-semibold'>{t('Balance alerts')}</h4>
          </div>

          <FormField
            control={form.control}
            name='balanceAlertThreshold'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Balance alert threshold')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={0.1}
                    value={field.value}
                    onChange={(event) => {
                      const parsed = Number(event.target.value)
                      field.onChange(
                        Number.isFinite(parsed) ? Math.max(0, parsed) : 0
                      )
                    }}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Alert when a provider-reported balance drops below this value. Defaults to $1.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='alertWebhookUrl'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Alert webhook URL')}</FormLabel>
                <FormControl>
                  <Input
                    type='url'
                    inputMode='url'
                    placeholder='https://hooks.example.com/node-alert'
                    autoComplete='off'
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Optional JSON webhook for balance alerts and automatic failover events.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='primaryDomain'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Primary domain')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder='gate.example.com'
                    autoComplete='off'
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Cloudflare CNAME record that failover switches to the target node, e.g. gate.example.com.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='nodeAutoFailover'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Automatic domain failover')}</FormLabel>
                  <FormDescription>
                    {t(
                      'When enabled and a healthy backup node is configured, switch the primary domain automatically after a balance alert. The platform never registers new accounts.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <div className='flex items-center gap-2 lg:col-span-2'>
            <KeyRound className='text-muted-foreground size-4' />
            <h4 className='text-sm font-semibold'>{t('Node allowlist')}</h4>
          </div>

          <FormField
            control={form.control}
            name='nodeDenylist'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Denylist')}</FormLabel>
                <FormControl>
                  <TagInput
                    value={field.value}
                    onChange={field.onChange}
                    placeholder={t('Add project/service IDs to hard-block...')}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Operations targeting these project/service IDs are rejected before any API call. Import existing account IDs here before automating.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='nodeAllowlist'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Allowlist')}</FormLabel>
                <FormControl>
                  <TagInput
                    value={field.value}
                    onChange={field.onChange}
                    placeholder={t('Add allowed project/service IDs...')}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Optional. When non-empty, only these project/service IDs may be targeted by deploy and failover operations.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className='flex items-center gap-2 lg:col-span-2'>
            <CloudCog className='text-muted-foreground size-4' />
            <h4 className='text-sm font-semibold'>
              {t('Cloudflare configuration')}
            </h4>
          </div>

          <Alert className='lg:col-span-2'>
            <Info className='size-4' />
            <AlertDescription>
              {t(
                'Secrets are encrypted at rest and never returned in plaintext. After saving, only the configured status is shown; leave a field blank to keep the existing value.'
              )}
            </AlertDescription>
          </Alert>

          <FormField
            control={form.control}
            name='cfApiToken'
            render={({ field }) => (
              <FormItem>
                <div className='flex items-center justify-between gap-2'>
                  <FormLabel>{t('Cloudflare API token')}</FormLabel>
                  <ConfiguredBadge
                    configured={settings.cfTokenConfigured}
                    label={t('Cloudflare API token')}
                  />
                </div>
                <FormControl>
                  <Input
                    type='password'
                    autoComplete='new-password'
                    placeholder={
                      settings.cfTokenConfigured
                        ? t('Configured — leave blank to keep')
                        : t('Enter a Cloudflare API token')
                    }
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t('Used for DNS CNAME failover and certificate checks.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='cfKvNamespace'
            render={({ field }) => (
              <FormItem>
                <div className='flex items-center justify-between gap-2'>
                  <FormLabel>{t('Cloudflare KV namespace')}</FormLabel>
                  <ConfiguredBadge
                    configured={settings.cfKvNamespaceConfigured}
                    label={t('Cloudflare KV namespace')}
                  />
                </div>
                <FormControl>
                  <Input
                    type='password'
                    autoComplete='new-password'
                    placeholder={
                      settings.cfKvNamespaceConfigured
                        ? t('Configured — leave blank to keep')
                        : t('Enter the Worker KV namespace ID')
                    }
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Namespace holding the Worker node table. Leave blank to keep the current value.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='cfZoneId'
            render={({ field }) => (
              <FormItem>
                <div className='flex items-center justify-between gap-2'>
                  <FormLabel>{t('Cloudflare zone ID')}</FormLabel>
                  <ConfiguredBadge
                    configured={settings.cfZoneIdConfigured}
                    label={t('Cloudflare zone ID')}
                  />
                </div>
                <FormControl>
                  <Input
                    type='password'
                    autoComplete='new-password'
                    placeholder={
                      settings.cfZoneIdConfigured
                        ? t('Configured — leave blank to keep')
                        : t('Enter the Cloudflare zone ID')
                    }
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Zone that owns the primary domain. Leave blank to keep the current value.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='cfAccountId'
            render={({ field }) => (
              <FormItem>
                <div className='flex items-center justify-between gap-2'>
                  <FormLabel>{t('Cloudflare account ID')}</FormLabel>
                  <ConfiguredBadge
                    configured={settings.cfAccountIdConfigured}
                    label={t('Cloudflare account ID')}
                  />
                </div>
                <FormControl>
                  <Input
                    type='password'
                    autoComplete='new-password'
                    placeholder={
                      settings.cfAccountIdConfigured
                        ? t('Configured — leave blank to keep')
                        : t('Enter the Cloudflare account ID')
                    }
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Required by the Worker KV API in addition to the KV namespace ID.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}

import { useQuery } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { listEndpoints, listSubscriptions } from '@/features/proxy-pool/api'

import type { ChannelProxyPolicy } from '../api'

export function ChannelProxySelector(props: {
  enabled: boolean
  policy: ChannelProxyPolicy
  proxy: string
  fixedConfigured?: boolean
  disabled?: boolean
  onPolicyChange: (policy: ChannelProxyPolicy) => void
  onProxyChange: (proxy: string) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const endpoints = useQuery({
    queryKey: ['proxy-pool', 'endpoints'],
    queryFn: listEndpoints,
    enabled: props.enabled,
  })
  const subscriptions = useQuery({
    queryKey: ['proxy-pool', 'subscriptions'],
    queryFn: listSubscriptions,
    enabled: props.enabled,
  })
  const ownEndpoints =
    endpoints.data?.filter(
      (endpoint) => endpoint.is_owner && endpoint.enabled
    ) ?? []
  const ownSubscriptions =
    subscriptions.data?.filter((subscription) => subscription.enabled) ?? []
  const missingEndpoint =
    props.policy.endpoint_id &&
    !ownEndpoints.some((endpoint) => endpoint.id === props.policy.endpoint_id)
  const missingSubscription =
    props.policy.subscription_id &&
    !ownSubscriptions.some(
      (subscription) => subscription.id === props.policy.subscription_id
    )
  const legacyMode =
    props.policy.mode === 'direct' || props.policy.mode === 'public_pool'
  return (
    <div className='space-y-3'>
      <div className='space-y-1.5'>
        <Label htmlFor={`${id}-mode`}>{t('Outbound proxy mode')}</Label>
        <NativeSelect
          id={`${id}-mode`}
          className='w-full'
          disabled={props.disabled}
          value={props.policy.mode}
          aria-describedby={legacyMode ? `${id}-legacy` : undefined}
          onChange={(event) => {
            const mode = event.target.value
            if (
              mode === 'user_pool' ||
              mode === 'subscription' ||
              mode === 'endpoint' ||
              mode === 'fixed'
            ) {
              props.onPolicyChange({ mode })
            }
          }}
        >
          {legacyMode && (
            <NativeSelectOption value={props.policy.mode} disabled>
              {props.policy.mode === 'direct'
                ? t('Legacy direct connection (read-only)')
                : t('Legacy public proxy pool (read-only)')}
            </NativeSelectOption>
          )}
          <NativeSelectOption value='user_pool'>
            {t('Rotate through my proxy pool')}
          </NativeSelectOption>
          <NativeSelectOption value='subscription'>
            {t('My subscription')}
          </NativeSelectOption>
          <NativeSelectOption value='endpoint'>
            {t('My proxy endpoint')}
          </NativeSelectOption>
          <NativeSelectOption value='fixed'>
            {t('Fixed proxy URL')}
          </NativeSelectOption>
        </NativeSelect>
        {legacyMode && (
          <p id={`${id}-legacy`} className='text-muted-foreground text-xs'>
            {t(
              'This legacy proxy setting is read-only. Select one of your own proxy strategies to change it. Other channel settings keep the current route.'
            )}
          </p>
        )}
      </div>
      {props.policy.mode === 'subscription' && (
        <div className='space-y-1.5'>
          <Label htmlFor={`${id}-subscription`}>{t('My subscription')}</Label>
          <NativeSelect
            id={`${id}-subscription`}
            className='w-full'
            disabled={props.disabled || subscriptions.isPending}
            value={props.policy.subscription_id ?? ''}
            onChange={(event) =>
              props.onPolicyChange({
                mode: 'subscription',
                ...(event.target.value
                  ? { subscription_id: Number(event.target.value) }
                  : {}),
              })
            }
          >
            <NativeSelectOption value=''>
              {t('Select a subscription')}
            </NativeSelectOption>
            {missingSubscription && (
              <NativeSelectOption value={props.policy.subscription_id} disabled>
                {t('Configured subscription unavailable')}
              </NativeSelectOption>
            )}
            {ownSubscriptions.map((subscription) => (
              <NativeSelectOption key={subscription.id} value={subscription.id}>
                {subscription.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          {subscriptions.isError && (
            <p role='alert' className='text-destructive text-xs'>
              {t('Failed to load proxy subscriptions')}{' '}
              <Button
                size='sm'
                variant='link'
                type='button'
                onClick={() => void subscriptions.refetch()}
              >
                {t('Retry')}
              </Button>
            </p>
          )}
          {subscriptions.isSuccess && ownSubscriptions.length === 0 && (
            <p className='text-muted-foreground text-xs'>
              {t('Add a subscription in Proxy pool first.')}
            </p>
          )}
        </div>
      )}
      {props.policy.mode === 'endpoint' && (
        <div className='space-y-1.5'>
          <Label htmlFor={`${id}-endpoint`}>{t('My proxy endpoint')}</Label>
          <NativeSelect
            id={`${id}-endpoint`}
            className='w-full'
            disabled={props.disabled || endpoints.isPending}
            value={props.policy.endpoint_id ?? ''}
            onChange={(event) =>
              props.onPolicyChange({
                mode: 'endpoint',
                ...(event.target.value
                  ? { endpoint_id: Number(event.target.value) }
                  : {}),
              })
            }
          >
            <NativeSelectOption value=''>
              {t('Select a proxy endpoint')}
            </NativeSelectOption>
            {missingEndpoint && (
              <NativeSelectOption value={props.policy.endpoint_id} disabled>
                {t('Configured proxy endpoint unavailable')}
              </NativeSelectOption>
            )}
            {ownEndpoints.map((endpoint) => (
              <NativeSelectOption key={endpoint.id} value={endpoint.id}>
                {endpoint.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          {endpoints.isError && (
            <p role='alert' className='text-destructive text-xs'>
              {t('Failed to load proxy endpoints')}{' '}
              <Button
                size='sm'
                variant='link'
                type='button'
                onClick={() => void endpoints.refetch()}
              >
                {t('Retry')}
              </Button>
            </p>
          )}
          {endpoints.isSuccess && ownEndpoints.length === 0 && (
            <p className='text-muted-foreground text-xs'>
              {t('Add an owned proxy endpoint in Proxy pool first.')}
            </p>
          )}
        </div>
      )}
      {props.policy.mode === 'fixed' && (
        <div className='space-y-1.5'>
          <Label htmlFor={`${id}-url`}>{t('Fixed proxy URL')}</Label>
          <Input
            id={`${id}-url`}
            type='password'
            autoComplete='new-password'
            spellCheck={false}
            value={props.proxy}
            disabled={props.disabled}
            placeholder='http(s)://host:port / socks5(h)://host:port'
            onChange={(event) => props.onProxyChange(event.target.value)}
          />
          {props.fixedConfigured && (
            <p className='text-muted-foreground text-xs'>
              {t('Leave blank to keep the configured proxy.')}
            </p>
          )}
        </div>
      )}
    </div>
  )
}

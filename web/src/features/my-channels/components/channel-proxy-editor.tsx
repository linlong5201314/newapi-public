import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  updateChannel,
  type ChannelProxyPolicy,
  type SelfChannelDetail,
} from '../api'
import { channelsQueryKeys } from '../lib'
import { isChannelProxySelectionComplete } from '../lib/channel-proxy'
import { ChannelProxySelector } from './channel-proxy-selector'

export function ChannelProxyEditor(props: { detail: SelfChannelDetail }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [policy, setPolicy] = useState<ChannelProxyPolicy>(() => {
    if (props.detail.proxy_policy) return props.detail.proxy_policy
    if (props.detail.proxy.mode === 'fixed') return { mode: 'fixed' }
    if (props.detail.proxy.mode === 'direct') return { mode: 'direct' }
    return { mode: 'user_pool' }
  })
  // Never initialize this from detail.proxy.value: it contains a masked URL.
  const [proxy, setProxy] = useState('')
  const mutation = useMutation({
    mutationFn: async () => {
      const setting: Record<string, unknown> = JSON.parse(
        props.detail.channel.setting || '{}'
      )
      setting.proxy_policy = policy
      setting.proxy = policy.mode === 'fixed' ? proxy.trim() : ''
      return requireServerSuccess(
        await updateChannel(props.detail.channel.id, {
          setting: JSON.stringify(setting),
        })
      )
    },
    onSuccess: () => {
      toast.success(t('Channel proxy settings saved'))
      setProxy('')
      void client.invalidateQueries({ queryKey: channelsQueryKeys.all })
      void client.invalidateQueries({
        queryKey: ['self-channel-detail', props.detail.channel.id],
      })
    },
    onError: (error) =>
      handleServerError(error, t('Failed to save channel proxy settings')),
  })
  return (
    <div className='space-y-3'>
      <ChannelProxySelector
        enabled
        policy={policy}
        proxy={proxy}
        fixedConfigured={props.detail.proxy_configured}
        disabled={mutation.isPending}
        onPolicyChange={setPolicy}
        onProxyChange={setProxy}
      />
      <Button
        type='button'
        size='sm'
        disabled={
          mutation.isPending ||
          !isChannelProxySelectionComplete(
            policy,
            proxy,
            props.detail.proxy_configured
          )
        }
        onClick={() => mutation.mutate()}
      >
        {t('Save proxy settings')}
      </Button>
    </div>
  )
}

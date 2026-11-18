import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  fetchUpstreamModels,
  saveChannelModels,
  type SelfChannelDetail,
} from '../api'
import { channelsQueryKeys } from '../lib'
import { ChannelModelSelection } from './channel-model-selection'

type ChannelModelEditorProps = {
  detail: SelfChannelDetail
  saveLabel: string
  renderModelAction?: (model: string) => ReactNode
}

export function ChannelModelEditor(props: ChannelModelEditorProps) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const channelId = props.detail.channel.id
  const [models, setModels] = useState(() => [
    ...new Set([
      ...(props.detail.all_models ?? []),
      ...props.detail.models,
      ...(props.detail.public_models ?? []),
    ]),
  ])
  const [enabled, setEnabled] = useState(props.detail.models)
  const [published, setPublished] = useState(props.detail.public_models ?? [])
  const [customModel, setCustomModel] = useState('')
  const dirty = useRef(false)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  useEffect(() => {
    setModels((previous) => [
      ...new Set([
        ...previous,
        ...(props.detail.all_models ?? []),
        ...props.detail.models,
      ]),
    ])
    if (dirty.current) return
    setEnabled(props.detail.models)
    setPublished(props.detail.public_models ?? [])
  }, [props.detail])

  const fetchMutation = useMutation({
    mutationFn: async () =>
      requireServerSuccess(await fetchUpstreamModels(channelId)),
    onSuccess: (response) => {
      if (!mounted.current) return
      const fetched =
        response.data?.map((model) => model.trim()).filter(Boolean) ?? []
      setModels((previous) => [...new Set([...previous, ...fetched])])
      // Discovery never changes either selection.
      if (fetched.length === 0) toast.info(t('No additional models found'))
      else {
        toast.success(
          t('Fetched {{count}} upstream models', { count: fetched.length })
        )
      }
    },
    onError: (error) =>
      handleServerError(error, t('Failed to fetch upstream models')),
  })
  const saveMutation = useMutation({
    mutationFn: () =>
      saveChannelModels(channelId, {
        models: enabled,
        publicModels: published,
        allModels: models,
        tier:
          props.detail.channel.tier ||
          (props.detail.template.ip_free ? 'free' : 'api'),
      }),
    onSuccess: () => {
      if (mounted.current) dirty.current = false
      toast.success(t('Channel model settings saved'))
      void client.invalidateQueries({ queryKey: channelsQueryKeys.all })
      void client.invalidateQueries({
        queryKey: ['self-channel-detail', channelId],
      })
    },
    onError: (error) =>
      handleServerError(error, t('Failed to save channel model settings')),
  })
  const busy = fetchMutation.isPending || saveMutation.isPending
  const addModel = () => {
    const model = customModel.trim()
    if (!model || busy) return
    dirty.current = true
    setModels((previous) => [...new Set([...previous, model])])
    setEnabled((previous) => [...new Set([...previous, model])])
    setCustomModel('')
  }
  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap justify-between gap-2'>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={busy}
          onClick={() => fetchMutation.mutate()}
        >
          {t('Fetch latest upstream models')}
        </Button>
        <Button
          type='button'
          size='sm'
          disabled={busy}
          onClick={() => saveMutation.mutate()}
        >
          {saveMutation.isPending ? t('Saving...') : props.saveLabel}
        </Button>
      </div>
      <ChannelModelSelection
        models={models}
        enabledModels={enabled}
        publicModels={published}
        disabled={saveMutation.isPending}
        renderModelAction={props.renderModelAction}
        onChange={(nextEnabled, nextPublic) => {
          dirty.current = true
          setEnabled(nextEnabled)
          setPublished(nextPublic)
        }}
      />
      <div className='flex flex-wrap gap-2'>
        <Input
          aria-label={t('Custom model ID')}
          placeholder={t('Custom model ID')}
          value={customModel}
          disabled={busy}
          className='min-w-0 flex-1 font-mono'
          onChange={(event) => setCustomModel(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault()
              addModel()
            }
          }}
        />
        <Button
          type='button'
          size='sm'
          variant='secondary'
          disabled={busy || !customModel.trim()}
          onClick={addModel}
        >
          {t('Add model')}
        </Button>
      </div>
    </div>
  )
}

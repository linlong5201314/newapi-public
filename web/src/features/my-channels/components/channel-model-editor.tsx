import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ModelTestButton } from '@/features/model-testing'
import { PUBLIC_CATALOG_QUERY_KEY } from '@/features/public-pool/constants'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  fetchUpstreamModels,
  saveChannelModels,
  type SelfChannelDetail,
} from '../api'
import { channelsQueryKeys } from '../lib'
import {
  ChannelModelSelection,
  UnavailablePreviewModels,
} from './channel-model-selection'

type ChannelModelEditorProps = {
  detail: SelfChannelDetail
  saveLabel: string
  renderModelAction?: (model: string) => ReactNode
}

export function ChannelModelEditor(props: ChannelModelEditorProps) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const channelId = props.detail.channel.id
  const isXiaoyiWork = props.detail.template.id === 'xiaoyiwork'
  const [unavailable, setUnavailable] = useState(
    props.detail.unavailable_models ?? []
  )
  const [shareConfirmed, setShareConfirmed] = useState(false)
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
  const [modelLabels, setModelLabels] = useState<Record<string, string>>({})
  const dirty = useRef(false)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  useEffect(() => {
    if (isXiaoyiWork && dirty.current) return
    setModels((previous) => [
      ...new Set([
        ...(isXiaoyiWork ? [] : previous),
        ...(props.detail.all_models ?? []),
        ...props.detail.models,
      ]),
    ])
    if (dirty.current) return
    setUnavailable(props.detail.unavailable_models ?? [])
    setEnabled(props.detail.models)
    setPublished(props.detail.public_models ?? [])
  }, [isXiaoyiWork, props.detail])

  const fetchMutation = useMutation({
    mutationFn: async () =>
      requireServerSuccess(await fetchUpstreamModels(channelId)),
    onSuccess: (response) => {
      if (!mounted.current) return
      setModelLabels(response.model_labels ?? {})
      const fetched =
        response.data?.map((model) => model.trim()).filter(Boolean) ?? []
      if (isXiaoyiWork) {
        const allowed = new Set([
          ...fetched,
          ...(response.unavailable_models ?? []),
        ])
        const all = [...allowed]
        setModels(all)
        setUnavailable([])
        setEnabled((previous) => [
          ...new Set([...previous.filter((model) => allowed.has(model)), ...all]),
        ])
        setPublished((previous) =>
          previous.filter((model) => allowed.has(model))
        )
        setShareConfirmed(false)
        dirty.current = true
      } else {
        setModels((previous) => [...new Set([...previous, ...fetched])])
      }
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
        shareConfirmed,
        verifiedAccountCatalog: isXiaoyiWork,
        tier:
          props.detail.channel.tier ||
          (props.detail.template.ip_free ? 'free' : 'api'),
      }),
    onSuccess: () => {
      if (mounted.current) dirty.current = false
      toast.success(t('Channel model settings saved'))
      void client.invalidateQueries({ queryKey: channelsQueryKeys.all })
      void client.invalidateQueries({ queryKey: PUBLIC_CATALOG_QUERY_KEY })
      setShareConfirmed(false)
      void client.invalidateQueries({
        queryKey: ['self-channel-detail', channelId],
      })
    },
    onError: (error) =>
      handleServerError(error, t('Failed to save channel model settings')),
  })
  const busy = fetchMutation.isPending || saveMutation.isPending
  const waitingForConsent =
    isXiaoyiWork && published.length > 0 && !shareConfirmed
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
        <div className='flex flex-wrap gap-2'>
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={busy}
            onClick={() => fetchMutation.mutate()}
          >
            {t('Fetch latest upstream models')}
          </Button>
          <ModelTestButton
            key={channelId}
            channelId={channelId}
            models={isXiaoyiWork ? props.detail.models : models}
            disabled={
              busy ||
              channelId <= 0 ||
              (isXiaoyiWork && props.detail.models.length === 0)
            }
            onPassedModels={(passed) => {
              if (!mounted.current || busy) return
              const available = passed.filter((model) => models.includes(model))
              if (available.length === 0) return
              dirty.current = true
              setEnabled((previous) => [
                ...new Set([...previous, ...available]),
              ])
            }}
          />
        </div>
        <Button
          type='button'
          size='sm'
          disabled={busy || waitingForConsent}
          onClick={() => saveMutation.mutate()}
        >
          {saveMutation.isPending ? t('Saving...') : props.saveLabel}
        </Button>
      </div>
      <ChannelModelSelection
        models={models}
        modelLabels={modelLabels}
        enabledModels={enabled}
        publicModels={published}
        disabled={busy}
        renderModelAction={(model) =>
          (!isXiaoyiWork || props.detail.models.includes(model)) &&
          props.renderModelAction?.(model)
        }
        onChange={(nextEnabled, nextPublic) => {
          dirty.current = true
          setEnabled(nextEnabled)
          setPublished(nextPublic)
          if (nextPublic.join(',') !== published.join(',')) {
            setShareConfirmed(false)
          }
        }}
      />
      {isXiaoyiWork ? (
        <>
          <UnavailablePreviewModels models={unavailable} />
          {published.length > 0 && (
            <Label className='flex items-start gap-2'>
              <Checkbox
                checked={shareConfirmed}
                disabled={busy}
                onCheckedChange={(value) => setShareConfirmed(value === true)}
              />
              {t(
                'I explicitly allow other users to use this channel and spend my account credits'
              )}
            </Label>
          )}
        </>
      ) : (
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
      )}
    </div>
  )
}

import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  getModelProbeOptions,
  selectedPassingModels,
  testModelEndpoints,
} from './api'
import type { ModelProbeBatch, ModelProbeResult } from './types'

export type ModelTestButtonProps = {
  channelId: number
  models: readonly string[]
  onPassedModels?: (models: string[]) => void
  endpointIds?: readonly number[]
  disabled?: boolean
}

export function ModelTestButton(props: ModelTestButtonProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button
        type='button'
        size='sm'
        variant='outline'
        disabled={
          props.disabled || props.channelId <= 0 || props.models.length === 0
        }
        onClick={() => setOpen(true)}
      >
        {t('Test models')}
      </Button>
      {open && <ModelTestDialog {...props} onClose={() => setOpen(false)} />}
    </>
  )
}

function ModelTestDialog(
  props: ModelTestButtonProps & { onClose: () => void }
) {
  const { t } = useTranslation()
  const models = [
    ...new Set(props.models.map((model) => model.trim()).filter(Boolean)),
  ]
  const [selected, setSelected] = useState(models.slice(0, 3))
  const [endpointIds, setEndpointIds] = useState<number[]>(
    [...(props.endpointIds ?? [])].slice(0, 3)
  )
  const [endpointOffset, setEndpointOffset] = useState(0)
  const [search, setSearch] = useState('')
  const [batch, setBatch] = useState<ModelProbeBatch>()
  const [addSelected, setAddSelected] = useState<string[]>([])
  const [cancelled, setCancelled] = useState(false)
  const abortRef = useRef<AbortController | null>(null)
  const options = useQuery({
    queryKey: [
      'model-probe-options',
      props.channelId,
      selected,
      endpointOffset,
      endpointIds,
    ],
    queryFn: ({ signal }) =>
      getModelProbeOptions(
        props.channelId,
        selected,
        signal,
        endpointOffset,
        endpointIds
      ),
    retry: false,
    refetchOnWindowFocus: false,
  })
  const test = useMutation({
    mutationFn: async () => {
      const controller = new AbortController()
      abortRef.current = controller
      return testModelEndpoints(
        props.channelId,
        selected,
        endpointIds,
        controller.signal
      )
    },
    onMutate: () => {
      setCancelled(false)
      setBatch(undefined)
      setAddSelected([])
    },
    onSuccess: (result) => {
      if (abortRef.current?.signal.aborted) return
      setBatch(result)
    },
    meta: { errorToast: false },
    retry: false,
  })
  useEffect(
    () => () => {
      abortRef.current?.abort()
    },
    []
  )
  const changeSelection = (next: string[]) => {
    setSelected(next)
    setBatch(undefined)
    setAddSelected([])
    test.reset()
  }
  const changeEndpoints = (next: number[]) => {
    setEndpointIds(next)
    setBatch(undefined)
    setAddSelected([])
    test.reset()
  }
  const passed = selectedPassingModels(batch, selected)
  const results = batch?.results ?? options.data?.results ?? []
  const cancel = () => {
    abortRef.current?.abort()
    setCancelled(true)
    setBatch(undefined)
    setAddSelected([])
  }
  const close = () => {
    cancel()
    props.onClose()
  }
  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) close()
      }}
      title={t('Test models')}
      description={t(
        'Tests request an 8-token output limit, but upstreams may exceed it and consume quota. At most 3 models and 3 endpoints run one at a time.'
      )}
      footer={
        <>
          <Button variant='outline' onClick={test.isPending ? cancel : close}>
            {test.isPending ? t('Cancel test') : t('Close')}
          </Button>
          {props.onPassedModels && (
            <Button
              variant='secondary'
              disabled={
                test.isPending ||
                addSelected.filter((model) => passed.includes(model)).length ===
                  0
              }
              onClick={() => {
                const safe = addSelected.filter((model) =>
                  selectedPassingModels(batch, selected).includes(model)
                )
                if (safe.length > 0) {
                  props.onPassedModels?.(safe)
                  close()
                }
              }}
            >
              {t('Use selected passing models')}
            </Button>
          )}
          <Button
            disabled={
              test.isPending ||
              options.isPending ||
              options.isError ||
              selected.length === 0 ||
              (!!options.data?.configured_egress_only && endpointIds.length > 0)
            }
            onClick={() => test.mutate()}
          >
            {test.isPending ? t('Testing...') : t('Run selected tests')}
          </Button>
        </>
      }
    >
      <div className='space-y-4' aria-busy={test.isPending}>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Unknown or expired results are not proof of availability. Tests never disable an entire channel.'
          )}
        </p>
        <Input
          type='search'
          aria-label={t('Search models')}
          placeholder={t('Search models...')}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
        <div
          className='max-h-48 space-y-2 overflow-y-auto rounded-lg border p-3'
          role='group'
          aria-label={t('Models to test')}
        >
          {models
            .filter((model) =>
              model.toLowerCase().includes(search.trim().toLowerCase())
            )
            .map((model) => (
              <Label key={model} className='flex min-w-0 items-center gap-2'>
                <Checkbox
                  checked={selected.includes(model)}
                  disabled={
                    test.isPending ||
                    (!selected.includes(model) && selected.length >= 3)
                  }
                  onCheckedChange={(checked) =>
                    changeSelection(
                      checked
                        ? [...selected, model]
                        : selected.filter((name) => name !== model)
                    )
                  }
                />
                <span className='min-w-0 font-mono text-xs break-all'>
                  {model}
                </span>
              </Label>
            ))}
          {models.length === 0 && <p>{t('No model found.')}</p>}
        </div>
        <div
          className='space-y-2'
          role='group'
          aria-label={t('Endpoints to test')}
        >
          <p className='text-sm font-medium'>{t('Endpoints to test')}</p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'No endpoint selected uses the channel’s configured stable egress. Selecting nodes never changes its saved proxy policy.'
            )}
          </p>
          {options.isPending && <LoadingState />}
          {options.isError && (
            <ErrorState
              description={getServerErrorMessage(options.error)}
              onRetry={() => void options.refetch()}
            />
          )}
          {options.data?.configured_egress_only && (
            <p>{t('Account model tests must use configured egress')}</p>
          )}
          {options.data?.endpoints
            .filter(
              (endpoint) =>
                endpoint.id > 0 && !options.data?.configured_egress_only
            )
            .map((endpoint) => (
              <Label
                key={endpoint.id}
                className='flex flex-wrap items-center gap-2'
              >
                <Checkbox
                  aria-label={t('Endpoint #{{id}}', { id: endpoint.id })}
                  checked={endpointIds.includes(endpoint.id)}
                  disabled={
                    test.isPending ||
                    (!endpointIds.includes(endpoint.id) &&
                      endpointIds.length >= 3)
                  }
                  onCheckedChange={(checked) =>
                    changeEndpoints(
                      checked
                        ? [...endpointIds, endpoint.id]
                        : endpointIds.filter((id) => id !== endpoint.id)
                    )
                  }
                />
                <span>{t('Endpoint #{{id}}', { id: endpoint.id })}</span>
                {endpoint.subscription_id > 0 && (
                  <span className='text-muted-foreground text-xs'>
                    {t('Subscription #{{id}}', {
                      id: endpoint.subscription_id,
                    })}
                  </span>
                )}
                {endpoint.public && (
                  <Badge variant='secondary'>{t('Public')}</Badge>
                )}
              </Label>
            ))}
          <div className='flex items-center gap-2'>
            <Button
              variant='outline'
              size='sm'
              disabled={test.isPending || endpointOffset === 0}
              onClick={() =>
                setEndpointOffset(Math.max(0, endpointOffset - 50))
              }
            >
              {t('Previous endpoints')}
            </Button>
            <Button
              variant='outline'
              size='sm'
              disabled={test.isPending || !options.data?.endpoint_has_more}
              onClick={() =>
                setEndpointOffset(
                  options.data?.endpoint_next_offset ?? endpointOffset
                )
              }
            >
              {t('Next endpoints')}
            </Button>
          </div>
        </div>
        <div aria-live='polite'>
          {test.isPending && (
            <p role='status'>
              {t('Testing selected model and endpoint samples sequentially...')}
            </p>
          )}
          {cancelled && (
            <p role='status'>
              {t('Test cancelled. Unfinished samples remain unknown.')}
            </p>
          )}
          {test.isError && !cancelled && (
            <p role='alert' className='text-destructive'>
              {getServerErrorMessage(test.error)}
            </p>
          )}
        </div>
        <div
          className='max-h-72 space-y-2 overflow-y-auto'
          role='group'
          aria-label={t('Model test results')}
        >
          {results.map((result) => (
            <ModelResult
              key={`${result.model}:${result.endpoint_id}`}
              result={result}
            />
          ))}
        </div>
        {props.onPassedModels && passed.length > 0 && (
          <div
            role='group'
            aria-label={t('Select passing models')}
            className='space-y-2 rounded-lg border p-3'
          >
            {passed.map((model) => (
              <Label key={model} className='flex items-center gap-2'>
                <Checkbox
                  checked={addSelected.includes(model)}
                  onCheckedChange={(checked) =>
                    setAddSelected(
                      checked
                        ? [...addSelected, model]
                        : addSelected.filter((name) => name !== model)
                    )
                  }
                />
                <span className='font-mono text-xs break-all'>
                  {t('Use {{model}}', { model })}
                </span>
              </Label>
            ))}
          </div>
        )}
      </div>
    </Dialog>
  )
}

function ModelResult(props: { result: ModelProbeResult }) {
  const { t } = useTranslation()
  const result = props.result
  let status = t('Unknown')
  if (result.status === 'available' && result.expires_at * 1000 > Date.now()) {
    status = t('Available')
  }
  if (
    result.status === 'unavailable' &&
    result.expires_at * 1000 > Date.now()
  ) {
    status = t('Unavailable')
  }
  return (
    <div className='space-y-1 rounded-lg border p-3 text-xs'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <span className='font-mono break-all'>{result.model}</span>
        <Badge variant={status === t('Available') ? 'default' : 'outline'}>
          {status}
        </Badge>
      </div>
      <p>
        {result.endpoint_id > 0
          ? t('Endpoint #{{id}}', { id: result.endpoint_id })
          : t('Configured egress')}{' '}
        · {t('Latency: {{ms}} ms', { ms: formatNumber(result.latency_ms) })}
      </p>
      <p className='text-muted-foreground'>
        {t('Lifetime: {{passed}}/{{total}} passed', {
          passed: result.lifetime_passes,
          total: result.lifetime_samples,
        })}
      </p>
      <p className='text-muted-foreground'>
        {t('Reason')}: {t(result.reason)} ·{' '}
        {t('Recent: {{passed}}/{{total}} passed', {
          passed: result.recent_passes,
          total: result.recent_sample_count,
        })}
      </p>
      {result.checked_at > 0 && (
        <p className='text-muted-foreground'>
          {t('Measured at')}: {new Date(result.checked_at * 1000).toISOString()}{' '}
          · {t('Expires at')}:{' '}
          {new Date(result.expires_at * 1000).toISOString()}
        </p>
      )}
    </div>
  )
}

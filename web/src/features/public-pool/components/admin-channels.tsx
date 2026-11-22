import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ModelTestButton } from '@/features/model-testing'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  copyPublicAdminChannel,
  deletePublicAdminChannel,
  getPublicAdminChannels,
  probePublicChannelAllEndpoints,
  PUBLIC_ADMIN_CHANNELS_KEY,
  setPublicAdminChannelStatus,
  type PublicAdminChannel,
  type PublicPoolProbeAllResponse,
} from '../admin-api'
import { PUBLIC_CATALOG_QUERY_KEY } from '../constants'
import { PublicAdminChannelEditor } from './admin-channel-editor'

type Action = {
  channel: PublicAdminChannel
  kind: 'copy' | 'delete' | 'enable' | 'disable'
}

// PublicPoolProbeBatchView renders one sampled probe batch. It shows how many
// of the eligible public endpoints were actually tested, breaks down each
// model's sample into available / unknown / not-run, and states that failing
// results never delete models — they only mark endpoint health.
function PublicPoolProbeBatchView(props: {
  batch: PublicPoolProbeAllResponse
}) {
  const { t } = useTranslation()
  const batch = props.batch
  return (
    <>
      <p className='mb-2 text-xs font-medium'>
        {t(
          'IP availability: tested {{tested}} of {{eligible}} eligible public endpoints',
          { tested: batch.selected_count, eligible: batch.eligible_total }
        )}
      </p>
      <ul className='space-y-1'>
        {batch.models.map((view) => (
          <li
            key={view.model}
            className='flex flex-wrap items-center gap-2 text-xs'
          >
            <span className='font-mono'>{view.model}</span>
            <Badge variant={view.passed ? 'default' : 'destructive'}>
              {view.available_endpoints}/{view.total_endpoints} {t('available')}
            </Badge>
            {view.unknown_endpoints > 0 && (
              <Badge variant='secondary'>
                {view.unknown_endpoints} {t('unknown')}
              </Badge>
            )}
            {view.not_run_endpoints > 0 && (
              <Badge variant='outline'>
                {view.not_run_endpoints} {t('not run')}
              </Badge>
            )}
            {!view.passed && (
              <span className='text-muted-foreground'>
                {view.results
                  .filter((result) => result.status !== 'available')
                  .slice(0, 3)
                  .map((result) => `#${result.endpoint_id}:${result.reason}`)
                  .join(', ')}
              </span>
            )}
          </li>
        ))}
      </ul>
      <p className='text-muted-foreground mt-2 text-xs'>
        {t(
          'Sampled batch. Results never delete models; they only mark endpoint health.'
        )}
      </p>
    </>
  )
}

export function PublicPoolAdminChannels() {
  const { t } = useTranslation()
  const isRoot = useAuthStore((state) => state.auth.user?.role === 100)
  const client = useQueryClient()
  const [search, setSearch] = useState('')
  const [keyword, setKeyword] = useState('')
  const [page, setPage] = useState(1)
  const [editor, setEditor] = useState<{
    channel: PublicAdminChannel | null
  } | null>(null)
  const [action, setAction] = useState<Action | null>(null)
  // IP 维度实测结果：channelId -> 抽样批次的模型视图
  const [ipProbes, setIpProbes] = useState<
    Record<
      number,
      { loading: boolean; batch: PublicPoolProbeAllResponse | null }
    >
  >({})
  const ipProbeMutation = useMutation({
    mutationFn: async (channel: PublicAdminChannel) => {
      const models = channel.models
        .split(',')
        .map((name) => name.trim())
        .filter(Boolean)
        .slice(0, 3)
      return probePublicChannelAllEndpoints(channel.id, models)
    },
    onMutate: (channel) => {
      setIpProbes((previous) => ({
        ...previous,
        [channel.id]: { loading: true, batch: null },
      }))
    },
    onSuccess: (data, channel) => {
      setIpProbes((previous) => ({
        ...previous,
        [channel.id]: { loading: false, batch: data },
      }))
    },
    onError: (error, channel) => {
      setIpProbes((previous) => ({
        ...previous,
        [channel.id]: { loading: false, batch: null },
      }))
      handleServerError(error)
    },
  })
  const query = useQuery({
    queryKey: [...PUBLIC_ADMIN_CHANNELS_KEY, keyword, page],
    queryFn: ({ signal }) =>
      getPublicAdminChannels({ keyword, page, page_size: 20 }, signal),
    enabled: isRoot,
  })
  const mutation = useMutation({
    mutationFn: async (value: Action) => {
      if (value.kind === 'copy') return copyPublicAdminChannel(value.channel.id)
      if (value.kind === 'delete') {
        return deletePublicAdminChannel(value.channel.id)
      }
      return setPublicAdminChannelStatus(
        value.channel.id,
        value.kind === 'enable' ? 1 : 2
      )
    },
    onSuccess: () => {
      setAction(null)
      void client.invalidateQueries({ queryKey: PUBLIC_ADMIN_CHANNELS_KEY })
      void client.invalidateQueries({ queryKey: PUBLIC_CATALOG_QUERY_KEY })
    },
    onError: (error) => handleServerError(error),
  })

  if (!isRoot) return null

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPage(1)
    setKeyword(search.trim())
  }

  let confirmLabel = t('Copy')
  let confirmDescription = t(
    'Copy this public free channel server-side without revealing its credentials? The new channel remains disabled.'
  )
  if (action?.kind === 'delete') {
    confirmLabel = t('Delete')
    confirmDescription = t(
      'Delete this public free channel and its routing abilities? This cannot be undone.'
    )
  }
  if (action?.kind === 'enable') {
    confirmLabel = t('Enable')
    confirmDescription = t(
      'Enable this public free channel for its configured groups? Test the configured models before enabling.'
    )
  }
  if (action?.kind === 'disable') {
    confirmLabel = t('Disable')
    confirmDescription = t('Disable this public free channel for all users?')
  }

  return (
    <section
      aria-label={t('Manage public free channels')}
      className='min-w-0 space-y-4'
    >
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='max-w-3xl space-y-1'>
          <h2 className='text-lg font-semibold'>
            {t('Manage public free channels')}
          </h2>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Superadmin only. Manage platform-owned free channels, including disabled channels. Private user channels and paid platform channels are excluded.'
            )}
          </p>
        </div>
        <Button onClick={() => setEditor({ channel: null })}>
          {t('Add public free channel')}
        </Button>
      </div>
      <form
        onSubmit={submitSearch}
        className='flex min-w-0 flex-wrap items-end gap-2'
      >
        <div className='min-w-0 flex-1 basis-48 space-y-2'>
          <Label htmlFor='public-channel-search'>
            {t('Search public free channels')}
          </Label>
          <Input
            id='public-channel-search'
            type='search'
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>
        <Button type='submit' variant='outline'>
          {t('Search')}
        </Button>
        <Button
          type='button'
          variant='outline'
          disabled={query.isFetching}
          onClick={() => {
            void query.refetch()
          }}
        >
          {t('Refresh')}
        </Button>
      </form>
      {query.isPending && <LoadingState />}
      {query.isError && (
        <ErrorState
          onRetry={() => {
            void query.refetch()
          }}
        />
      )}
      {query.isSuccess && query.data.items.length === 0 && (
        <EmptyState title={t('No public free channels')} />
      )}
      {query.isSuccess && (
        <div className='space-y-3'>
          {query.data.items.map((channel) => {
            const ipProbe = ipProbes[channel.id]
            return (
              <article
                key={channel.id}
                aria-label={channel.name}
                className='min-w-0 rounded-xl border p-4'
              >
                <div className='flex min-w-0 flex-wrap items-center gap-2'>
                  <h3 className='min-w-0 font-medium break-all'>
                    {channel.name}
                  </h3>
                  <Badge variant='outline'>
                    {channel.status === 1 ? t('Enabled') : t('Disabled')}
                  </Badge>
                  <span className='text-muted-foreground text-xs'>
                    #{channel.id}
                  </span>
                </div>
                <p className='text-muted-foreground mt-2 text-sm break-all'>
                  {t('Model IDs')}: {channel.models}
                </p>
                <p className='text-muted-foreground mt-1 text-xs break-all'>
                  {t('Group')}: {channel.group}
                </p>
                {ipProbe && (ipProbe.loading || ipProbe.batch !== null) && (
                  <div className='mt-3 rounded-lg border border-dashed p-3'>
                    {ipProbe.loading && (
                      <p className='text-muted-foreground text-xs'>
                        {t('Testing models across public endpoints...')}
                      </p>
                    )}
                    {!ipProbe.loading && ipProbe.batch !== null && (
                      <PublicPoolProbeBatchView batch={ipProbe.batch} />
                    )}
                  </div>
                )}
                <div className='mt-3 flex flex-wrap gap-2'>
                  <ModelTestButton
                    channelId={channel.id}
                    models={channel.models
                      .split(',')
                      .map((name) => name.trim())
                      .filter(Boolean)}
                    disabled={mutation.isPending}
                  />
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={mutation.isPending || ipProbeMutation.isPending}
                    onClick={() => ipProbeMutation.mutate(channel)}
                  >
                    {ipProbeMutation.isPending
                      ? t('Testing...')
                      : t('Test all public IPs')}
                  </Button>
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={mutation.isPending}
                    onClick={() => setEditor({ channel })}
                  >
                    {t('Edit')}
                  </Button>
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={mutation.isPending}
                    onClick={() => setAction({ channel, kind: 'copy' })}
                  >
                    {t('Copy')}
                  </Button>
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={mutation.isPending}
                    onClick={() =>
                      setAction({
                        channel,
                        kind: channel.status === 1 ? 'disable' : 'enable',
                      })
                    }
                  >
                    {channel.status === 1 ? t('Disable') : t('Enable')}
                  </Button>
                  <Button
                    size='sm'
                    variant='destructive'
                    disabled={mutation.isPending}
                    onClick={() => setAction({ channel, kind: 'delete' })}
                  >
                    {t('Delete')}
                  </Button>
                </div>
              </article>
            )
          })}
          <nav
            aria-label={t('Public free channels pagination')}
            className='flex flex-wrap items-center justify-end gap-2'
          >
            <Button
              variant='outline'
              disabled={page <= 1 || query.isFetching}
              onClick={() => setPage((value) => value - 1)}
            >
              {t('Previous')}
            </Button>
            <span className='text-muted-foreground text-sm'>
              {t('Page {{page}}', { page })}
            </span>
            <Button
              variant='outline'
              disabled={page * 20 >= query.data.total || query.isFetching}
              onClick={() => setPage((value) => value + 1)}
            >
              {t('Next')}
            </Button>
          </nav>
        </div>
      )}
      {editor && (
        <PublicAdminChannelEditor
          key={editor.channel?.id ?? 'new'}
          channel={editor.channel}
          onClose={() => setEditor(null)}
        />
      )}
      <ConfirmDialog
        open={!!action}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setAction(null)
        }}
        title={action?.channel.name ?? t('Manage public free channels')}
        desc={confirmDescription}
        confirmText={confirmLabel}
        destructive={action?.kind === 'delete'}
        isLoading={mutation.isPending}
        handleConfirm={() => {
          if (action) mutation.mutate(action)
        }}
      />
    </section>
  )
}

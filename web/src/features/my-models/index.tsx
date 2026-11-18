import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import {
  Boxes,
  ChevronDown,
  ChevronRight,
  Radio,
  Search,
  Sparkles,
  Trash2,
} from 'lucide-react'
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { getChannelDetail, getChannels } from '@/features/my-channels/api'
import { ChannelDeleteDialog } from '@/features/my-channels/components/channel-delete-dialog'
import { ChannelModelEditor } from '@/features/my-channels/components/channel-model-editor'
import { channelsQueryKeys } from '@/features/my-channels/lib'
import type { Channel } from '@/features/my-channels/types'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

export function MyModelsPage() {
  const { t } = useTranslation()
  const [expandedChannelId, setExpandedChannelId] = useState<number | null>(
    null
  )
  const [searchText, setSearchText] = useState('')
  const channelsQuery = useQuery({
    queryKey: [...channelsQueryKeys.lists(), 'my-models-all'],
    queryFn: async () =>
      requireServerSuccess(await getChannels({ p: 1, page_size: 100 })),
  })
  const channels = useMemo(
    () => channelsQuery.data?.data?.items ?? [],
    [channelsQuery.data]
  )
  const totalModelsCount = useMemo(
    () =>
      new Set(
        channels.flatMap((channel) =>
          channel.models
            .split(',')
            .map((model) => model.trim())
            .filter(Boolean)
        )
      ).size,
    [channels]
  )
  const filteredChannels = useMemo(() => {
    const keyword = searchText.trim().toLowerCase()
    return channels.filter(
      (channel) =>
        channel.name.toLowerCase().includes(keyword) ||
        channel.models.toLowerCase().includes(keyword)
    )
  }, [channels, searchText])
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <span className='flex items-center gap-2'>
          <Boxes className='text-primary size-5' aria-hidden='true' />
          {t('My models by channel')}
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='flex max-w-full flex-wrap items-center gap-2'>
          <div className='relative'>
            <Search
              className='text-muted-foreground absolute top-2.5 left-2.5 size-3.5'
              aria-hidden='true'
            />
            <Input
              type='search'
              aria-label={t('Search channels or models')}
              value={searchText}
              onChange={(event) => setSearchText(event.target.value)}
              placeholder={t('Search channels or models')}
              className='h-8 w-60 max-w-full pl-8 text-xs'
            />
          </div>
          <Button
            size='sm'
            variant='outline'
            render={<Link to='/my-channels' />}
          >
            <Radio className='me-1 size-3.5' aria-hidden='true' />
            {t('Manage my channels')}
          </Button>
        </div>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-4 pb-8'>
          <div className='grid gap-3 sm:grid-cols-3'>
            <Card className='py-3'>
              <CardContent>
                <p className='text-muted-foreground text-xs'>
                  {t('My channels')}
                </p>
                <p className='font-mono text-2xl font-semibold'>
                  {channels.length}
                </p>
              </CardContent>
            </Card>
            <Card className='py-3'>
              <CardContent>
                <p className='text-muted-foreground text-xs'>
                  {t('Enabled models (unique)')}
                </p>
                <p className='font-mono text-2xl font-semibold'>
                  {totalModelsCount}
                </p>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Unselected models remain available in the channel catalog.'
                  )}
                </p>
              </CardContent>
            </Card>
            <Card className='py-3'>
              <CardContent>
                <p className='text-muted-foreground text-xs'>
                  {t('Channels shared with the public pool')}
                </p>
                <p className='font-mono text-2xl font-semibold'>
                  {
                    channels.filter((channel) => channel.visibility === 1)
                      .length
                  }
                </p>
              </CardContent>
            </Card>
          </div>
          {channelsQuery.isPending && <LoadingState />}
          {channelsQuery.isError && (
            <ErrorState
              description={getServerErrorMessage(channelsQuery.error)}
              onRetry={() => void channelsQuery.refetch()}
            />
          )}
          {channelsQuery.isSuccess && filteredChannels.length === 0 && (
            <EmptyState
              title={t('No matching channels')}
              description={t(
                'Add a source in My channels to manage its models.'
              )}
            />
          )}
          {channelsQuery.isSuccess &&
            filteredChannels.map((channel) => (
              <ChannelModelDrilldownCard
                key={channel.id}
                channel={channel}
                expanded={expandedChannelId === channel.id}
                onToggleExpand={() =>
                  setExpandedChannelId(
                    expandedChannelId === channel.id ? null : channel.id
                  )
                }
              />
            ))}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function ChannelModelDrilldownCard(props: {
  channel: Channel
  expanded: boolean
  onToggleExpand: () => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const panelId = useId()
  const [deleteOpen, setDeleteOpen] = useState(false)
  const detailQuery = useQuery({
    queryKey: ['self-channel-detail', props.channel.id],
    queryFn: async () =>
      requireServerSuccess(await getChannelDetail(props.channel.id)),
    enabled: props.expanded,
  })
  return (
    <Card className='overflow-hidden'>
      <CardHeader className='p-0'>
        <div className='flex flex-wrap items-center justify-between gap-3 px-4 py-3.5'>
          <Button
            variant='ghost'
            onClick={props.onToggleExpand}
            aria-expanded={props.expanded}
            aria-controls={panelId}
            className='min-w-0 flex-1 justify-start text-left'
          >
            {props.expanded ? (
              <ChevronDown className='size-4 shrink-0' aria-hidden='true' />
            ) : (
              <ChevronRight className='size-4 shrink-0' aria-hidden='true' />
            )}
            <CardTitle className='truncate text-base font-medium'>
              {props.channel.name}
            </CardTitle>
            <Badge variant='outline'>#{props.channel.id}</Badge>
          </Button>
          <div className='flex flex-wrap items-center gap-2'>
            <Badge
              variant={props.channel.status === 1 ? 'default' : 'secondary'}
            >
              {props.channel.status === 1 ? t('Enabled') : t('Disabled')}
            </Badge>
            {props.channel.visibility === 1 && (
              <Badge variant='secondary'>{t('Public')}</Badge>
            )}
            <Button
              size='sm'
              variant='ghost'
              onClick={props.onToggleExpand}
              aria-expanded={props.expanded}
              aria-controls={panelId}
            >
              {props.expanded ? t('Collapse models') : t('Expand models')}
            </Button>
            <Button
              size='sm'
              variant='destructive'
              onClick={() => setDeleteOpen(true)}
            >
              <Trash2 className='me-1 size-3.5' aria-hidden='true' />
              {t('Delete channel')}
            </Button>
          </div>
        </div>
      </CardHeader>
      {props.expanded && (
        <CardContent
          id={panelId}
          className='bg-muted/15 space-y-3 border-t p-4'
        >
          {detailQuery.isPending && <LoadingState />}
          {detailQuery.isError && (
            <div role='alert'>
              <ErrorState
                description={getServerErrorMessage(detailQuery.error)}
                onRetry={() => void detailQuery.refetch()}
              />
            </div>
          )}
          {detailQuery.isSuccess && detailQuery.data.data && (
            <ChannelModelEditor
              key={props.channel.id}
              detail={detailQuery.data.data}
              saveLabel={t('Save channel models')}
              renderModelAction={(model) => (
                <Button
                  size='sm'
                  variant='ghost'
                  onClick={() => {
                    try {
                      window.localStorage.setItem(
                        'ds-demo:preferred-model',
                        model
                      )
                    } catch {
                      /* storage may be unavailable */
                    }
                    void navigate({ to: '/demo' })
                  }}
                >
                  <Sparkles className='me-1 size-3' aria-hidden='true' />
                  {t('Chat')}
                </Button>
              )}
            />
          )}
        </CardContent>
      )}
      <ChannelDeleteDialog
        channelId={props.channel.id}
        channelName={props.channel.name}
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
      />
    </Card>
  )
}

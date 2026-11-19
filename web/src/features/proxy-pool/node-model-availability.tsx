import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { ModelTestButton } from '@/features/model-testing'
import { getChannels } from '@/features/my-channels/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

interface Props {
  endpointId: number
  subscriptionId: number
  disabled?: boolean
}

export function NodeModelAvailability(props: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button
        size='sm'
        variant='outline'
        disabled={props.disabled || props.endpointId <= 0}
        onClick={() => setOpen(true)}
      >
        {t('Model availability')}
      </Button>
      {open && (
        <NodeModelAvailabilityDialog
          {...props}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  )
}

function NodeModelAvailabilityDialog(props: Props & { onClose: () => void }) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [page, setPage] = useState(1)
  const [channelId, setChannelId] = useState('')
  const query = useQuery({
    queryKey: ['proxy-pool', 'own-model-test-channels', userId, page],
    queryFn: async () => {
      const response = requireServerSuccess(
        await getChannels({ p: page, page_size: 20 })
      )
      return {
        items: response.data?.items ?? [],
        total: response.data?.total ?? 0,
      }
    },
  })
  const channel = query.data?.items.find(
    (item) => String(item.id) === channelId
  )
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Node model availability')}
      description={t(
        'Choose your own channel, then open model tests to view recent results or explicitly test this node. No test runs automatically.'
      )}
      contentClassName='sm:max-w-lg'
      footer={
        <Button variant='outline' onClick={props.onClose}>
          {t('Close')}
        </Button>
      }
    >
      <div className='space-y-4'>
        <div className='text-muted-foreground flex flex-wrap gap-2 text-xs'>
          <span>{t('Endpoint #{{id}}', { id: props.endpointId })}</span>
          {props.subscriptionId > 0 && (
            <span>
              {t('Subscription #{{id}}', { id: props.subscriptionId })}
            </span>
          )}
        </div>
        {query.isPending && <LoadingState />}
        {query.isError && (
          <ErrorState
            onRetry={() => {
              void query.refetch()
            }}
          />
        )}
        {query.isSuccess && query.data.items.length === 0 && (
          <EmptyState
            description={t(
              'Add your own channel to test model availability on this node.'
            )}
          />
        )}
        {query.isSuccess && query.data.items.length > 0 && (
          <>
            <div className='space-y-2'>
              <Label htmlFor={`node-channel-${props.endpointId}`}>
                {t('My channel')}
              </Label>
              <NativeSelect
                id={`node-channel-${props.endpointId}`}
                value={channelId}
                onChange={(event) => setChannelId(event.target.value)}
              >
                <NativeSelectOption value=''>
                  {t('Select a channel')}
                </NativeSelectOption>
                {query.data.items.map((item) => (
                  <NativeSelectOption key={item.id} value={item.id}>
                    {item.name}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            {channel && (
              <ModelTestButton
                key={channel.id}
                channelId={channel.id}
                models={channel.models
                  .split(',')
                  .map((name) => name.trim())
                  .filter(Boolean)}
                endpointIds={[props.endpointId]}
              />
            )}
          </>
        )}
        {query.isSuccess && query.data.total > 20 && (
          <nav
            aria-label={t('My channels pagination')}
            className='flex justify-end gap-2'
          >
            <Button
              variant='outline'
              disabled={page <= 1 || query.isFetching}
              onClick={() => {
                setChannelId('')
                setPage((value) => value - 1)
              }}
            >
              {t('Previous')}
            </Button>
            <Button
              variant='outline'
              disabled={page * 20 >= query.data.total || query.isFetching}
              onClick={() => {
                setChannelId('')
                setPage((value) => value + 1)
              }}
            >
              {t('Next')}
            </Button>
          </nav>
        )}
      </div>
    </Dialog>
  )
}

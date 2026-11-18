import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { useIsAdmin } from '@/hooks/use-admin'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  listMyChannels,
  listPlatformChannels,
  setPlatformVisibility,
  setVisibility,
  updateAvatar,
} from '../api'
import { PUBLIC_CATALOG_QUERY_KEY } from '../constants'

function PlatformSharingSettings() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['public-pool', 'platform-settings'],
    queryFn: listPlatformChannels,
    meta: { errorToast: false },
  })
  const mutation = useMutation({
    mutationFn: setPlatformVisibility,
    onSuccess: async () => {
      toast.success(t('Sharing settings saved'))
      await Promise.all([
        query.refetch(),
        client.invalidateQueries({ queryKey: PUBLIC_CATALOG_QUERY_KEY }),
      ])
    },
    onError: (error: unknown) =>
      handleServerError(error, t('Unable to save sharing settings')),
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Platform channel publication (admin)')}</CardTitle>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Only published platform channels are available to other users. Changes can take up to 60 seconds to propagate.'
          )}
        </p>
      </CardHeader>
      <CardContent className='space-y-3'>
        {query.isPending && <Skeleton className='h-16 w-full' />}
        {query.isError && (
          <ErrorState
            title={t('Unable to load sharing settings')}
            onRetry={() => void query.refetch()}
          />
        )}
        {query.isSuccess && query.data.length === 0 && (
          <EmptyState
            title={t('No platform channels to manage.')}
            className='min-h-32'
          />
        )}
        {query.data?.map((row) => (
          <div
            key={row.id}
            className='flex min-w-0 flex-wrap items-center gap-3 rounded-lg border p-3'
          >
            <div className='min-w-0 flex-1'>
              <p className='text-sm font-medium break-words'>{row.name}</p>
              <p
                className='text-muted-foreground truncate text-xs'
                title={row.models}
              >
                {row.models}
              </p>
            </div>
            <label className='flex items-center gap-2 text-xs'>
              <Checkbox
                checked={row.visibility === 1}
                disabled={mutation.isPending}
                onCheckedChange={(checked) =>
                  mutation.mutate({
                    channelId: row.id,
                    visibility: checked ? 1 : 0,
                    tier: row.tier,
                  })
                }
              />
              {t('Publish to other users')}
            </label>
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

export function SharingSettings() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const isAdmin = useIsAdmin()
  const [avatarDraft, setAvatarDraft] = useState<string | null>(null)
  const [ipDrafts, setIpDrafts] = useState<Record<number, string>>({})
  const query = useQuery({
    queryKey: ['public-pool', 'my-channels'],
    queryFn: listMyChannels,
    meta: { errorToast: false },
  })
  const user = useQuery({
    queryKey: ['public-pool', 'profile-avatar'],
    queryFn: async () => {
      const response = await api.get('/api/user/self')
      return (requireServerSuccess(response.data)?.data?.avatar as string) || ''
    },
    meta: { errorToast: false },
  })
  const avatar = avatarDraft ?? user.data ?? ''
  const avatarMutation = useMutation({
    mutationFn: () => updateAvatar(avatar.trim()),
    onSuccess: async () => {
      toast.success(t('Avatar saved'))
      await Promise.all([
        user.refetch(),
        client.invalidateQueries({ queryKey: PUBLIC_CATALOG_QUERY_KEY }),
      ])
    },
    onError: (error: unknown) =>
      handleServerError(error, t('Unable to save avatar')),
  })
  const mutation = useMutation({
    mutationFn: setVisibility,
    onSuccess: async () => {
      toast.success(t('Sharing settings saved'))
      await Promise.all([
        query.refetch(),
        client.invalidateQueries({ queryKey: PUBLIC_CATALOG_QUERY_KEY }),
      ])
    },
    onError: (error: unknown) =>
      handleServerError(error, t('Unable to save sharing settings')),
  })

  return (
    <div className='min-w-0 space-y-4'>
      <Card>
        <CardHeader>
          <CardTitle>{t('My public identity')}</CardTitle>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Your avatar and display name are shown on shared models and comments.'
            )}
          </p>
        </CardHeader>
        <CardContent>
          {user.isError && (
            <ErrorState
              className='min-h-32'
              title={t('Unable to load profile')}
              onRetry={() => void user.refetch()}
            />
          )}
          <form
            className='flex min-w-0 flex-wrap items-end gap-3'
            onSubmit={(event) => {
              event.preventDefault()
              if (!avatarMutation.isPending) avatarMutation.mutate()
            }}
          >
            <Avatar className='size-9'>
              {avatar && <AvatarImage src={avatar} alt='' />}
              <AvatarFallback>?</AvatarFallback>
            </Avatar>
            <label className='flex min-w-0 flex-1 flex-col gap-1.5 text-xs'>
              {t('Avatar URL')}
              <Input
                type='url'
                value={avatar}
                disabled={
                  user.isPending || user.isError || avatarMutation.isPending
                }
                onChange={(event) => setAvatarDraft(event.target.value)}
                placeholder='https://example.com/avatar.png'
              />
            </label>
            <Button
              type='submit'
              disabled={
                user.isPending || user.isError || avatarMutation.isPending
              }
            >
              {t('Save avatar')}
            </Button>
          </form>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>{t('My channel publication')}</CardTitle>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Channels remain private until you explicitly publish them. Choose which models to share in My models.'
            )}
          </p>
        </CardHeader>
        <CardContent className='space-y-3'>
          {query.isPending && <Skeleton className='h-16 w-full' />}
          {query.isError && (
            <ErrorState
              title={t('Unable to load sharing settings')}
              onRetry={() => void query.refetch()}
            />
          )}
          {query.isSuccess && query.data.length === 0 && (
            <EmptyState
              title={t('You have no channels to share.')}
              className='min-h-32'
            />
          )}
          {query.data?.map((row) => (
            <div
              key={row.id}
              className='flex min-w-0 flex-wrap items-center gap-3 rounded-lg border p-3'
            >
              <div className='min-w-0 flex-1'>
                <p className='font-mono text-sm break-words'>{row.name}</p>
                <p
                  className='text-muted-foreground truncate text-xs'
                  title={row.models}
                >
                  {row.models}
                </p>
              </div>
              <label className='flex items-center gap-2 text-xs'>
                <Checkbox
                  checked={row.visibility === 1}
                  disabled={mutation.isPending}
                  onCheckedChange={(checked) =>
                    mutation.mutate({
                      channelId: row.id,
                      visibility: checked ? 1 : 0,
                      tier: row.tier || 'api',
                    })
                  }
                />
                {t('Publish to public model pool')}
              </label>
              <label className='flex items-center gap-2 text-xs'>
                {t('Channel tier')}
                <NativeSelect
                  value={row.tier || 'api'}
                  disabled={mutation.isPending}
                  onChange={(event) =>
                    mutation.mutate({
                      channelId: row.id,
                      visibility: row.visibility === 1 ? 1 : 0,
                      tier: event.target.value,
                    })
                  }
                >
                  <NativeSelectOption value='api'>
                    {t('API key tier')}
                  </NativeSelectOption>
                  <NativeSelectOption value='free'>
                    {t('Free tier')}
                  </NativeSelectOption>
                </NativeSelect>
              </label>
              {row.tier === 'free' && (
                <form
                  className='flex w-full min-w-0 flex-wrap items-end gap-2'
                  onSubmit={(event) => {
                    event.preventDefault()
                    if (!mutation.isPending) {
                      mutation.mutate({
                        channelId: row.id,
                        visibility: row.visibility === 1 ? 1 : 0,
                        tier: row.tier || 'free',
                        allowIps: (
                          ipDrafts[row.id] ??
                          row.allow_ips ??
                          ''
                        ).trim(),
                      })
                    }
                  }}
                >
                  <label className='flex min-w-0 flex-1 flex-col gap-1.5 text-xs'>
                    {t('Subscription IP allowlist')}
                    <Input
                      value={ipDrafts[row.id] ?? row.allow_ips ?? ''}
                      disabled={mutation.isPending}
                      onChange={(event) =>
                        setIpDrafts((current) => ({
                          ...current,
                          [row.id]: event.target.value,
                        }))
                      }
                      placeholder={t(
                        'Comma-separated IPs; empty means no IP restriction'
                      )}
                    />
                  </label>
                  <Button
                    type='submit'
                    variant='outline'
                    disabled={mutation.isPending}
                  >
                    {t('Save')}
                  </Button>
                </form>
              )}
            </div>
          ))}
        </CardContent>
      </Card>
      {isAdmin && <PlatformSharingSettings />}
    </div>
  )
}

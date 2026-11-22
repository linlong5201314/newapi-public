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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { getChannels } from '@/features/my-channels/api'
import { ChannelTypeLogo } from '@/features/my-channels/components/channel-type-badge'
import { CHANNEL_STATUS } from '@/features/my-channels/constants'
import { channelsQueryKeys } from '@/features/my-channels/lib/channel-actions'
import { parseModelsList } from '@/features/my-channels/lib/channel-utils'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

const PAGE_SIZE = 100

export function OwnerModelCatalog(props: { userId: number }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: [...channelsQueryKeys.lists(), 'pricing', props.userId, page],
    queryFn: async () => {
      const { data } = requireServerSuccess(
        await getChannels({ p: page, page_size: PAGE_SIZE })
      )
      return {
        channels: (data?.items ?? []).map((channel) => ({
          id: channel.id,
          name: channel.name,
          type: channel.type,
          enabled: channel.status === CHANNEL_STATUS.ENABLED,
          models: [...new Set(parseModelsList(channel.models))],
        })),
        totalPages: Math.ceil(
          (data?.total ?? 0) / (data?.page_size || PAGE_SIZE)
        ),
      }
    },
    meta: { errorToast: false },
  })

  if (query.isPending) {
    return (
      <div role='status' aria-busy='true'>
        <LoadingState message={t('Loading models...')} />
      </div>
    )
  }
  if (query.isError) {
    return (
      <div role='alert'>
        <ErrorState
          title={t('Unable to load your channels')}
          description={getServerErrorMessage(query.error)}
          onRetry={() => void query.refetch()}
        />
      </div>
    )
  }

  const { channels, totalPages } = query.data
  if (channels.length === 0 && page === 1) {
    return (
      <EmptyState
        bordered
        title={t('No channels found')}
        description={t('Add a source in My channels to manage its models.')}
        action={
          <Button className='min-h-[44px]' render={<Link to='/my-channels' />}>
            {t('Manage my channels')}
          </Button>
        }
      />
    )
  }

  return (
    <div className='flex min-w-0 flex-col gap-4'>
      <header className='flex flex-wrap items-center justify-between gap-3'>
        <h2 className='text-base font-semibold'>{t('Channel models')}</h2>
        <Button
          variant='outline'
          className='min-h-[44px]'
          render={<Link to='/my-models' />}
        >
          {t('My models')}
        </Button>
      </header>
      {channels.length === 0 ? (
        <EmptyState bordered title={t('No channels found')} />
      ) : (
        <Accordion
          key={page}
          defaultValue={[channels[0].id]}
          className='bg-card min-w-0 rounded-xl border px-3 sm:px-4'
        >
          {channels.map((channel) => (
            <AccordionItem key={channel.id} value={channel.id}>
              <AccordionTrigger className='min-h-[44px] min-w-0 items-center gap-2'>
                <span aria-hidden='true' className='shrink-0'>
                  <ChannelTypeLogo type={channel.type} />
                </span>
                <span className='min-w-0 [overflow-wrap:anywhere]'>
                  {channel.name}
                </span>
                {!channel.enabled && (
                  <Badge variant='secondary'>{t('Disabled')}</Badge>
                )}
              </AccordionTrigger>
              <AccordionContent>
                {channel.models.length === 0 ? (
                  <p className='text-muted-foreground'>
                    {t('No models found')}
                  </p>
                ) : (
                  <ul className='flex min-w-0 flex-wrap gap-2'>
                    {channel.models.map((model) => (
                      <li key={model} className='max-w-full min-w-0'>
                        <Badge
                          variant='outline'
                          className='h-auto min-h-5 max-w-full font-mono [overflow-wrap:anywhere] whitespace-normal'
                        >
                          {model}
                        </Badge>
                      </li>
                    ))}
                  </ul>
                )}
              </AccordionContent>
            </AccordionItem>
          ))}
        </Accordion>
      )}
      {(page > 1 || totalPages > 1) && (
        <div className='flex flex-wrap items-center gap-2'>
          <Button
            variant='outline'
            disabled={page === 1}
            onClick={() => setPage(page - 1)}
          >
            {t('Previous page')}
          </Button>
          <span role='status' className='text-muted-foreground text-sm'>
            {t('Page {{page}}', { page: formatNumber(page, locale) })}
          </span>
          <Button
            variant='outline'
            disabled={page >= totalPages}
            onClick={() => setPage(page + 1)}
          >
            {t('Next page')}
          </Button>
        </div>
      )}
    </div>
  )
}

import { useMutation, useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { formatLatency } from '@/features/performance-metrics/lib/format'
import { importHaizhuNodes, listEndpointPage } from '@/features/proxy-pool/api'
import { kindLabel } from '@/features/proxy-pool/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

export function PublicExitPool() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const query = useQuery({
    queryKey: ['public-pool', 'exit-inventory-preview'],
    queryFn: () => listEndpointPage(),
    meta: { errorToast: false },
  })
  const mutation = useMutation({
    mutationFn: async () =>
      requireServerSuccess(await importHaizhuNodes('full')),
    onSuccess: async (result) => {
      toast.success(
        t('{{count}} proxy exits imported', {
          count: result.data?.imported ?? 0,
        })
      )
      await query.refetch()
    },
    onError: (error: unknown) =>
      handleServerError(error, t('Unable to import proxy exits')),
  })
  const endpoints = (query.data?.data ?? []).filter(
    (endpoint) => endpoint.public === true
  )
  const preview = endpoints.slice(0, 90)
  return (
    <Card className='min-w-0'>
      <CardHeader>
        <CardTitle>{t('Free proxy exit IP pool')}</CardTitle>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Proxy exits provide network egress, not AI models or unlimited tokens. Imported, running, and sampled nodes are different counts.'
          )}
        </p>
        <div className='flex flex-wrap gap-2'>
          <Button
            variant='outline'
            disabled={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {mutation.isPending
              ? t('Importing...')
              : t('Sync Haizhu proxy exits')}
          </Button>
          <Button
            variant='outline'
            role='link'
            render={<Link to='/proxy-pool' />}
          >
            {t('Browse all proxy inventory')}
          </Button>
        </div>
      </CardHeader>
      <CardContent className='min-w-0 space-y-3'>
        {query.isPending && (
          <Skeleton
            aria-label={t('Loading proxy exits')}
            className='h-32 w-full'
          />
        )}
        {query.isError && (
          <ErrorState
            title={t('Unable to load proxy exits')}
            onRetry={() => void query.refetch()}
          />
        )}
        {query.isSuccess && (
          <p className='text-muted-foreground text-xs'>
            {t(
              'Previewing {{shown}} public nodes from {{fetched}} fetched inventory records. This is not the complete pool or a health count.',
              {
                shown: formatNumber(preview.length, locale),
                fetched: formatNumber(query.data.data.length, locale),
              }
            )}
          </p>
        )}
        {query.isSuccess && query.data.pagination.has_more && (
          <p className='text-muted-foreground text-xs'>
            {t(
              'More inventory is available. Open proxy management to load additional pages.'
            )}
          </p>
        )}
        {query.isSuccess &&
          (endpoints.length === 0 ? (
            <EmptyState
              title={t('No public nodes in the fetched preview.')}
              bordered
            />
          ) : (
            <>
              <div className='grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3'>
                {preview.map((endpoint) => (
                  <div
                    key={endpoint.id}
                    className='flex min-w-0 flex-wrap items-center justify-between gap-2 rounded-lg border p-3 text-xs'
                  >
                    <div className='min-w-0 flex-1 space-y-1'>
                      <p className='truncate font-medium' title={endpoint.name}>
                        {endpoint.name}
                      </p>
                      <div className='text-muted-foreground flex flex-wrap gap-2'>
                        <Badge variant='outline'>
                          {kindLabel(endpoint.kind)}
                        </Badge>
                        <span>{endpoint.region || t('Region unknown')}</span>
                        {endpoint.source === 'haizhu' && <span>Haizhu</span>}
                      </div>
                    </div>
                    <span className='text-muted-foreground font-mono'>
                      {endpoint.latency_ms && endpoint.latency_ms > 0
                        ? formatLatency(endpoint.latency_ms)
                        : t('No samples yet')}
                    </span>
                  </div>
                ))}
              </div>
              {endpoints.length > 90 && (
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'This preview shows at most {{count}} public nodes. Browse inventory to view and load more records.',
                    { count: formatNumber(preview.length, locale) }
                  )}
                </p>
              )}
            </>
          ))}
      </CardContent>
    </Card>
  )
}

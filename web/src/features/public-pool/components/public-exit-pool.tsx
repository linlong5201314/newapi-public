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
import { importHaizhuNodes, listEndpoints } from '@/features/proxy-pool/api'
import { kindLabel } from '@/features/proxy-pool/types'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

export function PublicExitPool() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['public-pool', 'exit-ips'],
    queryFn: listEndpoints,
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
  const endpoints = (query.data ?? []).filter(
    (endpoint) => endpoint.public === true
  )
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
          <Button variant='outline' render={<Link to='/proxy-pool' />}>
            {t('Manage proxy exit IPs')}
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
        {query.isSuccess &&
          (endpoints.length === 0 ? (
            <EmptyState
              title={t('No public proxy exits are published.')}
              bordered
            />
          ) : (
            <>
              <p className='text-muted-foreground text-xs'>
                {t('{{count}} published proxy exits', {
                  count: endpoints.length,
                })}
              </p>
              <div className='grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3'>
                {endpoints.slice(0, 90).map((endpoint) => (
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
                    'Showing the first {{count}} proxy exits. Open proxy management for all nodes.',
                    { count: 90 }
                  )}
                </p>
              )}
            </>
          ))}
      </CardContent>
    </Card>
  )
}

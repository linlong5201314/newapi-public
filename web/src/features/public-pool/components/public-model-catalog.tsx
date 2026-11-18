import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { SearchBar } from '@/features/pricing/components/search-bar'

import { listPublicModels } from '../api'
import { PUBLIC_CATALOG_QUERY_KEY, STATUS_LABELS } from '../constants'
import { projectCatalog, type CatalogStatus } from '../lib/catalog'
import type { PublicModelSource } from '../types'
import { PublicModelCard } from './public-model-card'

type SourceFilter = 'all' | PublicModelSource

export function PublicModelCatalog() {
  const { t } = useTranslation()
  const vendorId = useId()
  const statusId = useId()
  const [search, setSearch] = useState('')
  const [vendor, setVendor] = useState('all')
  const [source, setSource] = useState<SourceFilter>('all')
  const [status, setStatus] = useState<'all' | CatalogStatus>('all')
  const query = useQuery({
    queryKey: PUBLIC_CATALOG_QUERY_KEY,
    queryFn: ({ signal }) => listPublicModels(signal),
    staleTime: 30_000,
    meta: { errorToast: false },
  })
  const catalog = useMemo(() => projectCatalog(query.data ?? []), [query.data])
  const vendors = useMemo(
    () => [...new Set(catalog.models.map((item) => item.vendor))].sort(),
    [catalog.models]
  )
  const keyword = search.trim().toLowerCase()
  const filtered = catalog.models.filter(
    (item) =>
      (!keyword || item.model.toLowerCase().includes(keyword)) &&
      (vendor === 'all' || item.vendor === vendor) &&
      (status === 'all' || item.status === status)
  )
  const hasFilters = Boolean(keyword || vendor !== 'all' || status !== 'all')
  const sources =
    source === 'all' ? (['platform_free', 'user_shared'] as const) : [source]

  return (
    <div className='min-w-0 space-y-4'>
      <div
        role='search'
        aria-label={t('Public model filters')}
        className='bg-card min-w-0 space-y-3 rounded-xl border p-3 sm:p-4'
      >
        <SearchBar
          value={search}
          onChange={setSearch}
          onClear={() => setSearch('')}
        />
        <div className='grid grid-cols-1 items-end gap-3 sm:grid-cols-2 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]'>
          <div className='min-w-0 space-y-1.5'>
            <label htmlFor={vendorId} className='text-muted-foreground text-xs'>
              {t('Provider')}
            </label>
            <NativeSelect
              id={vendorId}
              className='w-full'
              value={vendor}
              onChange={(event) => setVendor(event.target.value)}
            >
              <NativeSelectOption value='all'>
                {t('All providers')}
              </NativeSelectOption>
              {vendors.map((name) => (
                <NativeSelectOption key={name} value={name}>
                  {name || t('Other providers')}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          <div className='min-w-0 space-y-1.5'>
            <label htmlFor={statusId} className='text-muted-foreground text-xs'>
              {t('Status')}
            </label>
            <NativeSelect
              id={statusId}
              className='w-full'
              value={status}
              onChange={(event) =>
                setStatus(event.target.value as typeof status)
              }
            >
              <NativeSelectOption value='all'>
                {t('All statuses')}
              </NativeSelectOption>
              {Object.entries(STATUS_LABELS).map(([value, label]) => (
                <NativeSelectOption key={value} value={value}>
                  {t(label)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          <div className='flex flex-wrap gap-2'>
            <Button
              variant='outline'
              disabled={!hasFilters && source === 'all'}
              onClick={() => {
                setSearch('')
                setVendor('all')
                setStatus('all')
                setSource('all')
              }}
            >
              {t('Reset filters')}
            </Button>
            <Button
              variant='outline'
              aria-label={t('Refresh public models')}
              disabled={query.isFetching}
              onClick={() => void query.refetch()}
            >
              <RefreshCw aria-hidden className='size-4' />
              {t('Refresh')}
            </Button>
          </div>
        </div>
      </div>
      <Tabs
        value={source}
        onValueChange={(value) => setSource(value as SourceFilter)}
        className='min-w-0 gap-4'
      >
        <div className='flex min-w-0 flex-wrap items-center justify-between gap-3'>
          <TabsList
            aria-label={t('Model source')}
            className='h-auto max-w-full flex-wrap'
          >
            <TabsTrigger value='all'>{t('All sources')}</TabsTrigger>
            <TabsTrigger value='platform_free'>
              {t('Platform free')}
            </TabsTrigger>
            <TabsTrigger value='user_shared'>{t('User shared')}</TabsTrigger>
          </TabsList>
          <p className='text-muted-foreground text-xs'>
            {t('AI model channels only. Proxy exit IPs are listed separately.')}
          </p>
        </div>
        <TabsContent value={source} className='min-w-0 space-y-6'>
          {query.isPending && (
            <div
              role='status'
              aria-label={t('Loading public models')}
              aria-busy='true'
              className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3'
            >
              {[0, 1, 2].map((key) => (
                <Skeleton key={key} className='h-64 w-full' />
              ))}
            </div>
          )}
          {query.isError && (
            <ErrorState
              title={t('Unable to load public models')}
              description={t(
                'Public model data could not be refreshed. Retry to load the catalog.'
              )}
              onRetry={() => void query.refetch()}
            />
          )}
          {!query.isPending && !query.isError && (
            <>
              {catalog.unclassified > 0 && (
                <p
                  role='status'
                  className='text-muted-foreground rounded-lg border p-3 text-sm'
                >
                  {t(
                    'Some channels have no verified source and are not included in either catalog.'
                  )}
                </p>
              )}
              {sources.map((kind) => {
                const title =
                  kind === 'platform_free'
                    ? t('Platform free models')
                    : t('User-shared models')
                const items = filtered.filter((item) => item.source === kind)
                let empty =
                  kind === 'platform_free'
                    ? t('No platform free models are published.')
                    : t('No user-shared models are published.')
                if (hasFilters) {
                  empty =
                    kind === 'platform_free'
                      ? t('No platform free models match these filters.')
                      : t('No user-shared models match these filters.')
                }
                return (
                  <section
                    key={kind}
                    aria-label={title}
                    className='min-w-0 space-y-3'
                  >
                    <header className='flex flex-wrap items-baseline justify-between gap-2 border-b pb-2'>
                      <h2 className='text-base font-semibold'>{title}</h2>
                      <p
                        aria-live='polite'
                        className='text-muted-foreground text-xs'
                      >
                        {t('{{count}} models', { count: items.length })}
                      </p>
                    </header>
                    {items.length === 0 ? (
                      <EmptyState title={empty} bordered className='min-h-40' />
                    ) : (
                      <div className='grid min-w-0 grid-cols-1 gap-3 sm:gap-4 md:grid-cols-2 xl:grid-cols-3'>
                        {items.map((item) => (
                          <PublicModelCard key={item.key} item={item} />
                        ))}
                      </div>
                    )}
                  </section>
                )
              })}
            </>
          )}
        </TabsContent>
      </Tabs>
    </div>
  )
}

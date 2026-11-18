import { lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { PublicModelCatalog } from './components/public-model-catalog'

const PublicExitPool = lazy(() =>
  import('./components/public-exit-pool').then((module) => ({
    default: module.PublicExitPool,
  }))
)
const SharingSettings = lazy(() =>
  import('./components/sharing-settings').then((module) => ({
    default: module.SharingSettings,
  }))
)

export function PublicPoolPage() {
  const { t } = useTranslation()
  return (
    <div className='min-h-0 min-w-0 flex-1 space-y-5 overflow-y-auto p-4 sm:p-6'>
      <header className='space-y-2'>
        <h1 className='text-xl font-semibold'>{t('Public model pool')}</h1>
        <p className='text-muted-foreground max-w-3xl text-sm leading-6'>
          {t(
            'Discover platform free models and models voluntarily shared by users. Metrics reflect observed channels, not guaranteed availability or unlimited usage.'
          )}
        </p>
      </header>
      <Tabs defaultValue='models' className='min-w-0 gap-5'>
        <TabsList
          aria-label={t('Public pool resources')}
          className='h-auto max-w-full flex-wrap'
        >
          <TabsTrigger value='models'>{t('AI models')}</TabsTrigger>
          <TabsTrigger value='ips'>{t('Proxy exit IPs')}</TabsTrigger>
          <TabsTrigger value='settings'>{t('My sharing settings')}</TabsTrigger>
        </TabsList>
        <TabsContent value='models' className='min-w-0'>
          <PublicModelCatalog />
        </TabsContent>
        <TabsContent value='ips' className='min-w-0'>
          <Suspense fallback={<Skeleton className='h-40 w-full' />}>
            <PublicExitPool />
          </Suspense>
        </TabsContent>
        <TabsContent value='settings' className='min-w-0'>
          <Suspense fallback={<Skeleton className='h-40 w-full' />}>
            <SharingSettings />
          </Suspense>
        </TabsContent>
      </Tabs>
    </div>
  )
}

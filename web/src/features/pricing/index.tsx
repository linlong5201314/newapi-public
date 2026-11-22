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
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { Globe2, LockKeyhole, Radio, Users, Zap } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { PublicLayout } from '@/components/layout'
import { PageTransition } from '@/components/page-transition'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { PublicModelCatalog } from '@/features/public-pool/components/public-model-catalog'
import { useAuthStore } from '@/stores/auth-store'

import { OwnerModelCatalog } from './components/owner-model-catalog'
import { SitePricing } from './components/site-pricing'

export function Pricing() {
  const { t } = useTranslation()
  const search = useSearch({ from: '/pricing/' })
  const navigate = useNavigate({ from: '/pricing/' })
  const userId = useAuthStore((state) => state.auth.user?.id)
  const source = search.source ?? 'site'
  const sources = [
    { value: 'site', label: t('Site pricing'), icon: Globe2 },
    { value: 'platform_free', label: t('Platform free pool'), icon: Zap },
    { value: 'user_shared', label: t('User-shared pool'), icon: Users },
    { value: 'owner', label: t('Your channels'), icon: Radio },
  ] as const

  return (
    <PublicLayout showMainContainer={false}>
      <main className='relative'>
        <div
          aria-hidden
          className='pointer-events-none absolute inset-x-0 top-0 h-[600px] opacity-20 dark:opacity-[0.10]'
          style={{
            background: [
              'radial-gradient(ellipse 60% 50% at 20% 20%, oklch(0.72 0.18 250 / 80%) 0%, transparent 70%)',
              'radial-gradient(ellipse 50% 40% at 80% 15%, oklch(0.65 0.15 200 / 60%) 0%, transparent 70%)',
              'radial-gradient(ellipse 40% 35% at 50% 70%, oklch(0.70 0.12 280 / 40%) 0%, transparent 70%)',
            ].join(', '),
            maskImage:
              'linear-gradient(to bottom, black 40%, transparent 100%)',
            WebkitMaskImage:
              'linear-gradient(to bottom, black 40%, transparent 100%)',
          }}
        />
        <PageTransition className='relative mx-auto w-full max-w-[1800px] px-3 pt-16 pb-8 sm:px-6 sm:pt-20 sm:pb-10 xl:px-8'>
          <header className='mx-auto mb-5 max-w-3xl pt-5 text-center sm:mb-7 sm:pt-10'>
            <h1 className='text-[clamp(2rem,5.5vw,3.5rem)] leading-[1.15] font-bold tracking-tight'>
              {t('Model Square')}
            </h1>
            <p className='text-muted-foreground mx-auto mt-3 max-w-2xl text-sm leading-relaxed sm:mt-4 sm:text-base'>
              {t(
                'Discover curated AI models, compare pricing and capabilities, and choose the right model for every scenario.'
              )}
            </p>
          </header>
          <Tabs
            value={source}
            onValueChange={(value) => {
              const nextSource = sources.find((item) => item.value === value)
              if (!nextSource) return
              void navigate({
                to: '/pricing',
                search: (current) => ({
                  ...current,
                  source:
                    nextSource.value === 'site' ? undefined : nextSource.value,
                }),
                resetScroll: false,
              })
            }}
            className='min-w-0 gap-5 sm:gap-7'
          >
            <TabsList
              aria-label={t('Model source')}
              className='mx-auto grid w-full max-w-3xl grid-cols-2 gap-1.5 p-1.5 group-data-horizontal/tabs:h-auto sm:grid-cols-4'
            >
              {sources.map(({ value, label, icon: Icon }) => (
                <TabsTrigger
                  key={value}
                  value={value}
                  className='text-foreground data-active:border-border dark:text-foreground h-auto min-h-[44px] min-w-0 px-2 py-2 font-semibold [overflow-wrap:anywhere] whitespace-normal'
                >
                  <Icon aria-hidden />
                  {label}
                </TabsTrigger>
              ))}
            </TabsList>
            {sources.map(({ value }) => (
              <TabsContent
                key={value}
                value={value}
                keepMounted={value === 'site'}
                className='focus-visible:ring-ring min-w-0 rounded-lg focus-visible:ring-2'
              >
                {value === 'site' ? (
                  <SitePricing active={source === 'site'} />
                ) : (
                  source === value && (
                    <SourceCatalog source={value} userId={userId} />
                  )
                )}
              </TabsContent>
            ))}
          </Tabs>
        </PageTransition>
      </main>
    </PublicLayout>
  )
}

function SourceCatalog(props: {
  source: 'platform_free' | 'user_shared' | 'owner'
  userId?: number
}) {
  const { t } = useTranslation()
  // These catalogs are backed by UserAuth endpoints, including both public pools.
  if (!props.userId) {
    return (
      <EmptyState
        className='min-h-48'
        bordered
        icon={LockKeyhole}
        title={t('Sign in required')}
        description={
          props.source === 'owner'
            ? t('Sign in to see the models configured in your channels.')
            : t('Sign in to browse models in the public pools.')
        }
        action={
          <Button
            role='link'
            className='min-h-[44px] px-4'
            render={
              <Link
                to='/sign-in'
                search={{ redirect: `/pricing?source=${props.source}` }}
              />
            }
          >
            {t('Sign in')}
          </Button>
        }
      />
    )
  }
  if (props.source === 'owner') {
    return <OwnerModelCatalog key={props.userId} userId={props.userId} />
  }
  return <PublicModelCatalog fixedSource={props.source} />
}

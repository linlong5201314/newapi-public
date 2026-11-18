import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { listMagpiePlugins } from './api'

export function MagpiePluginsCard() {
  const { t } = useTranslation()
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')
  const catalog = useQuery({
    queryKey: ['magpie-plugins', search],
    queryFn: () => listMagpiePlugins(search),
    retry: false,
    meta: { errorToast: false },
  })
  const authText: Record<string, string> = {
    account: t('Provider account sign-in required'),
    oauth: t('Browser authorization required'),
    key: t('Your own API key required'),
    ip: t('Read provider requirements'),
  }

  return (
    <section
      className='bg-card mb-4 rounded-xl border p-5'
      aria-label={t('Magpie plugins')}
    >
      <div className='mb-2 flex flex-wrap items-center justify-between gap-2'>
        <h2 className='text-base font-semibold'>{t('Magpie plugins')}</h2>
        <a
          className='text-primary text-sm underline'
          href='https://github.com/yetone/magpie'
          rel='noopener noreferrer'
          target='_blank'
        >
          {t('Magpie setup guide')}
        </a>
      </div>
      <p className='text-muted-foreground mb-3 text-sm'>
        {t(
          'Copying does not install or authorize a plugin. Run commands on the machine hosting your own Magpie gateway, not in this website.'
        )}
      </p>
      <ol className='text-muted-foreground mb-4 list-inside list-decimal space-y-1 text-sm'>
        <li>
          {t(
            'Install and start Magpie on a machine you control. Hosted instances require administrator access to install plugins.'
          )}
        </li>
        <li>
          {t(
            'In Magpie, open Plugins, choose Add, and paste the import source. Alternatively run the CLI command below on that gateway host.'
          )}
        </li>
        <li>
          {t(
            'Complete the plugin sign-in in Magpie using your own account. Installing alone does not grant model access.'
          )}
        </li>
        <li>
          {t(
            'Add an OpenAI-compatible channel here using your reachable Magpie /v1 endpoint and your own gateway key, then fetch models. Do not paste an install command into the key field.'
          )}
        </li>
      </ol>
      <form
        className='mb-3 flex gap-2'
        onSubmit={(event) => {
          event.preventDefault()
          setSearch(keyword.trim())
        }}
      >
        <Input
          aria-label={t('Search Magpie plugins')}
          placeholder={t('Search plugin name, repository or purpose')}
          value={keyword}
          onChange={(event) => setKeyword(event.target.value)}
        />
        <Button type='submit' variant='outline'>
          {t('Search')}
        </Button>
      </form>
      {catalog.isPending && (
        <LoadingState message={t('Loading Magpie plugins')} />
      )}
      {catalog.isError && (
        <ErrorState
          title={t('Could not load Magpie plugins')}
          onRetry={() => {
            void catalog.refetch()
          }}
        />
      )}
      {catalog.isSuccess && catalog.data.items.length === 0 && (
        <EmptyState title={t('No matching plugins')} />
      )}
      {catalog.isSuccess && catalog.data.items.length > 0 && (
        <div className='grid gap-3 md:grid-cols-2 xl:grid-cols-3'>
          {catalog.data.items.map((plugin) => (
            <article
              className='min-w-0 space-y-2 rounded-lg border p-3'
              key={plugin.id}
            >
              <h3 className='text-sm font-medium'>{plugin.name}</h3>
              <p className='text-muted-foreground text-xs'>{plugin.desc}</p>
              <p className='text-muted-foreground text-xs'>
                {authText[plugin.authMode] ?? t('Read provider requirements')}
              </p>
              <p className='text-muted-foreground font-mono text-xs break-all'>
                {plugin.repo}
              </p>
              {plugin.url && (
                <a
                  className='text-primary block text-xs underline'
                  href={plugin.url}
                  rel='noopener noreferrer'
                  target='_blank'
                >
                  {t('Open plugin documentation')}
                </a>
              )}
              {plugin.install && plugin.importSource ? (
                <>
                  <div className='flex items-center gap-2'>
                    <code className='min-w-0 flex-1 text-xs break-all'>
                      {plugin.importSource}
                    </code>
                    <CopyButton
                      value={plugin.importSource}
                      aria-label={t('Copy import source')}
                    />
                  </div>
                  <div className='flex items-center gap-2'>
                    <code className='min-w-0 flex-1 text-xs break-all'>
                      {plugin.install}
                    </code>
                    <CopyButton
                      value={plugin.install}
                      aria-label={t('Copy CLI command')}
                    />
                  </div>
                </>
              ) : (
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Installation source is unverified. No install command is offered.'
                  )}
                </p>
              )}
            </article>
          ))}
        </div>
      )}
    </section>
  )
}

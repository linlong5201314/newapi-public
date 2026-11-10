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
import { ExternalLink, Link2Off } from 'lucide-react'
import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'

import { useEnabledExtLinks } from '../api'
import { ExtLinkIcon } from './ext-link-icon'

/**
 * [M4-FE] In-site app viewer (route /_authenticated/app/$slug).
 *
 * - proxy:  iframe src=/app/<slug>/ — same-origin reverse proxy, address bar
 *           never leaves our domain.
 * - iframe: iframe src=<target url> — target may send X-Frame-Options; a hint
 *           is shown for that case.
 * - newtab: opens the target in a new window once, then renders a fallback
 *           card with a re-open button.
 *
 * A slim top bar carries the app name + "open in new window" action.
 */
export function ExtLinkAppView({ slug }: { slug: string }) {
  const { t } = useTranslation()
  const { data, isLoading } = useEnabledExtLinks()

  const link = useMemo(
    () => (data ?? []).find((l) => l.slug === slug),
    [data, slug]
  )

  const openedRef = useRef(false)
  useEffect(() => {
    if (link?.mode === 'newtab' && !openedRef.current) {
      openedRef.current = true
      window.open(link.url, '_blank', 'noopener,noreferrer')
    }
  }, [link])

  if (isLoading) {
    return (
      <div className='flex h-full min-h-0 items-center justify-center'>
        <LoadingState message={t('Loading...')} />
      </div>
    )
  }

  if (!link) {
    return (
      <div className='flex h-full min-h-0 items-center justify-center'>
        <ErrorState
          icon={Link2Off}
          title={t('App Not Found')}
          description={t(
            'This external link does not exist or has been disabled.'
          )}
        />
      </div>
    )
  }

  const openInNewWindow = () => {
    const href =
      link.mode === 'iframe' ? link.url : `${window.location.origin}/app/${link.slug}/`
    window.open(href, '_blank', 'noopener,noreferrer')
  }

  if (link.mode === 'newtab') {
    return (
      <div className='flex h-full min-h-0 flex-col items-center justify-center gap-4 p-6'>
        <ExtLinkIcon
          icon={link.icon}
          name={link.name}
          className='text-primary size-12 object-contain'
        />
        <h1 className='text-foreground text-xl font-semibold'>{link.name}</h1>
        <p className='text-muted-foreground text-sm'>
          {t('This app opens in a new window.')}
        </p>
        <Button onClick={() => window.open(link.url, '_blank', 'noopener,noreferrer')}>
          <ExternalLink className='mr-2 size-4' />
          {t('Open in New Window')}
        </Button>
      </div>
    )
  }

  const iframeSrc =
    link.mode === 'iframe' ? link.url : `/app/${link.slug}/`

  return (
    <div className='flex h-full min-h-0 flex-col'>
      {/* 顶部细条：名称 + 新窗口打开 */}
      <div className='bg-background flex h-10 shrink-0 items-center gap-2 border-b px-3'>
        <ExtLinkIcon
          icon={link.icon}
          name={link.name}
          className='text-primary size-5 object-contain'
        />
        <span className='text-foreground min-w-0 flex-1 truncate text-sm font-medium'>
          {link.name}
        </span>
        {link.mode === 'iframe' && (
          <span className='text-muted-foreground hidden truncate text-xs md:inline'>
            {t('If the page stays blank, the target site may block embedding.')}
          </span>
        )}
        <Button
          variant='ghost'
          size='sm'
          className='h-7 shrink-0 px-2 text-xs'
          onClick={openInNewWindow}
        >
          <ExternalLink className='mr-1 size-3.5' />
          {t('Open in New Window')}
        </Button>
      </div>
      <iframe
        key={iframeSrc}
        src={iframeSrc}
        title={link.name}
        className='min-h-0 w-full flex-1 border-none'
        sandbox='allow-forms allow-modals allow-popups allow-popups-to-escape-sandbox allow-same-origin allow-scripts allow-downloads'
      />
    </div>
  )
}

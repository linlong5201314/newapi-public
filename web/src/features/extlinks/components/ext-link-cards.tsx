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
import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Card } from '@/components/ui/card'
import { useAuthStore } from '@/stores/auth-store'

import { useEnabledExtLinks } from '../api'
import { matchesPosition, type ExtLink } from '../types'
import { ExtLinkIcon } from './ext-link-icon'

/** [M4-FE] Click behavior per mode (blueprint §3). */
export function openExtLink(
  link: Pick<ExtLink, 'slug' | 'mode' | 'url'>,
  navigate: ReturnType<typeof useNavigate>
) {
  if (link.mode === 'newtab') {
    window.open(link.url, '_blank', 'noopener,noreferrer')
    return
  }
  // proxy / iframe 都进站内二级页（地址栏不变）。
  navigate({ to: '/app/$slug', params: { slug: link.slug } })
}

/**
 * Home-page prominent card grid for external links whose position includes
 * home_card. Renders nothing when signed out or when no links are configured.
 */
export function ExtLinkCards() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const isAuthenticated = useAuthStore((s) => !!s.auth.user)
  const { data } = useEnabledExtLinks(isAuthenticated)

  const links = (data ?? []).filter((l) => matchesPosition(l, 'home_card'))
  if (!isAuthenticated || links.length === 0) {
    return null
  }

  return (
    <section className='mx-auto w-full max-w-6xl px-4 pt-10'>
      <h2 className='text-foreground mb-4 text-xl font-semibold tracking-tight'>
        {t('Apps')}
      </h2>
      <div className='grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6'>
        {links.map((link) => (
          <Card
            key={link.id}
            role='button'
            tabIndex={0}
            onClick={() => openExtLink(link, navigate)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                openExtLink(link, navigate)
              }
            }}
            className='hover:border-primary/60 hover:shadow-md flex cursor-pointer flex-col items-center gap-2 p-4 transition-all'
          >
            <ExtLinkIcon
              icon={link.icon}
              name={link.name}
              className='text-primary size-8 object-contain'
            />
            <span className='text-foreground w-full truncate text-center text-sm font-medium'>
              {link.name}
            </span>
          </Card>
        ))}
      </div>
    </section>
  )
}

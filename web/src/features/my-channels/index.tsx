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
import { ChevronDown, Info } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { MagpiePluginsCard } from '@/features/account-bindings/magpie-plugins'

import { ChannelsDialogs } from './components/channels-dialogs'
import { ChannelsPrimaryButtons } from './components/channels-primary-buttons'
import { ChannelsProvider } from './components/channels-provider'
import { ChannelsTable } from './components/channels-table'
import { FreeUsageCards } from './components/free-usage-cards'

export function MyChannels() {
  const { t } = useTranslation()
  const [magpieOpen, setMagpieOpen] = useState(false)

  return (
    <ChannelsProvider>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          <span className='truncate'>{t('My Channels')}</span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <ChannelsPrimaryButtons />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='space-y-3 pb-8'>
            <Alert className='shrink-0'>
              <Info className='h-4 w-4' />
              <AlertDescription>
                {t(
                  'Channels here are private to you. API requests are only routed to your own channels.'
                )}
                {' · '}
                <span>
                  {t(
                    'Open a channel name to view details, manage your proxy, and select models.'
                  )}
                </span>
              </AlertDescription>
            </Alert>
            <div className='min-h-[540px]'>
              <ChannelsTable />
            </div>
            <FreeUsageCards />
            <Collapsible
              open={magpieOpen}
              onOpenChange={setMagpieOpen}
              className='rounded-lg border'
            >
              <CollapsibleTrigger
                aria-label={t('Magpie plugins (optional)')}
                render={
                  <Button
                    type='button'
                    variant='ghost'
                    className='text-muted-foreground h-auto w-full justify-between gap-3 px-4 py-3 text-left whitespace-normal'
                  />
                }
              >
                <span className='min-w-0'>
                  <span className='block text-sm font-medium'>
                    {t('Magpie plugins (optional)')}
                  </span>
                  <span className='block text-xs font-normal'>
                    {t('Optional setup for your own Magpie gateway')}
                  </span>
                </span>
                <ChevronDown
                  aria-hidden='true'
                  className={`size-4 shrink-0 transition-transform ${magpieOpen ? 'rotate-180' : ''}`}
                />
              </CollapsibleTrigger>
              <CollapsibleContent className='border-t p-3'>
                <MagpiePluginsCard />
              </CollapsibleContent>
            </Collapsible>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <ChannelsDialogs />
    </ChannelsProvider>
  )
}

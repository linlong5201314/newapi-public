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
import { Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'

import { RouteRulesDialogs } from './components/route-rules-dialogs'
import { RouteRulesPrimaryButtons } from './components/route-rules-primary-buttons'
import { RouteRulesProvider } from './components/route-rules-provider'
import { RouteRulesTable } from './components/route-rules-table'

export function RouteRules() {
  const { t } = useTranslation()

  return (
    <RouteRulesProvider>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          <span className='truncate'>{t('Route Rules')}</span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <RouteRulesPrimaryButtons />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <Alert className='mb-3 shrink-0'>
            <Info className='h-4 w-4' />
            <AlertDescription>
              {t(
                'A route rule turns its name into a virtual model. When your API key calls that model, requests are routed across the target channels with the chosen strategy.'
              )}
            </AlertDescription>
          </Alert>
          <RouteRulesTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <RouteRulesDialogs />
    </RouteRulesProvider>
  )
}

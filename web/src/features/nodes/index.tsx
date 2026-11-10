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
import { Info, ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'

import { NodesDialogs } from './components/nodes-dialogs'
import { NodesPrimaryButtons } from './components/nodes-primary-buttons'
import { NodesProvider } from './components/nodes-provider'
import { NodesTable } from './components/nodes-table'

/**
 * [M5-FE] Node management page (root only).
 *
 * Contract: docs/M5-部署与节点管理设计.md §7. The page is fail-soft: when the
 * M5 backend is not deployed yet the table renders an inline error with a
 * retry action instead of crashing.
 */
export function Nodes() {
  const { t } = useTranslation()

  return (
    <NodesProvider>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          <span className='truncate'>{t('Node Management')}</span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <NodesPrimaryButtons />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <Alert className='mb-3 shrink-0'>
            <Info className='h-4 w-4' />
            <AlertDescription>
              <p>
                {t(
                  'Manage the cloud services you registered manually. Health checks run against each node; deployment and domain failover always start with a dry-run and require explicit confirmation.'
                )}
              </p>
              <p className='text-muted-foreground mt-1 flex items-center gap-1.5 text-xs'>
                <ShieldCheck className='size-3.5 shrink-0' />
                {t(
                  'Root only. Every write is audited and no resource changes before the plan is applied.'
                )}
              </p>
            </AlertDescription>
          </Alert>
          <NodesTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <NodesDialogs />
    </NodesProvider>
  )
}

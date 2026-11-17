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
import { MagpiePluginsCard } from '@/features/account-bindings/magpie-plugins'

import { ChannelsDialogs } from './components/channels-dialogs'
import { ChannelsPrimaryButtons } from './components/channels-primary-buttons'
import { ChannelsProvider } from './components/channels-provider'
import { ChannelsTable } from './components/channels-table'
import { FreeUsageCards } from './components/free-usage-cards'

export function MyChannels() {
  const { t } = useTranslation()

  return (
    <ChannelsProvider>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          <span className='truncate'>{t('My Channels')}</span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <ChannelsPrimaryButtons />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <Alert className='mb-3 shrink-0'>
            <Info className='h-4 w-4' />
            <AlertDescription>
              {t(
                'Channels here are private to you. API requests are only routed to your own channels.'
              )}
              {' · '}
              <span>
                点击渠道名称可打开详情抽屉（查看实时额度、选择代理 IP / 订阅节点、编辑名称、管理模型与公开设置）；Token / OAuth 账号授权（Trae / WorkBuddy / 小浣熊 / Cline / Qoder CN / 书生·端砚 / DS2API）已全部整合至右上角「从免费源添加」。
              </span>
            </AlertDescription>
          </Alert>
          {/* [M2-FE] Free quota pool cards */}
          <FreeUsageCards />
          <MagpiePluginsCard />
          <ChannelsTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <ChannelsDialogs />
    </ChannelsProvider>
  )
}

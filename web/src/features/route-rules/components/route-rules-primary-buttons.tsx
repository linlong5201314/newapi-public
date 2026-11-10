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
import { Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import { useRouteRulesQuery } from '../hooks/use-route-rules-query'
import { useRouteRules } from './route-rules-provider'

export function RouteRulesPrimaryButtons() {
  const { t } = useTranslation()
  const { setOpen, setCurrentRow } = useRouteRules()
  const { data } = useRouteRulesQuery()

  // Per-user cap advertised by the backend (RouteRuleMaxPerUser, design §1.1).
  const maxPerUser = data?.maxPerUser ?? null
  const ruleCount = data?.rules.length ?? 0
  const atCap = maxPerUser !== null && ruleCount >= maxPerUser

  const button = (
    <Button
      disabled={atCap}
      onClick={() => {
        setCurrentRow(null)
        setOpen('create-rule')
      }}
    >
      <Plus className='h-4 w-4' />
      <span className='max-sm:hidden'>{t('Create Route Rule')}</span>
      <span className='sm:hidden'>{t('Create')}</span>
    </Button>
  )

  return (
    <div className='flex flex-wrap items-center gap-2'>
      {atCap ? (
        <Tooltip>
          <TooltipTrigger render={<span className='inline-flex' />}>
            {button}
          </TooltipTrigger>
          <TooltipContent>
            {t('Route rule limit reached: max {{max}} rules per user', {
              max: maxPerUser,
            })}
          </TooltipContent>
        </Tooltip>
      ) : (
        button
      )}
      {maxPerUser !== null && (
        <span className='text-muted-foreground text-xs'>
          {t('{{count}} / {{max}} rules', { count: ruleCount, max: maxPerUser })}
        </span>
      )}
    </div>
  )
}

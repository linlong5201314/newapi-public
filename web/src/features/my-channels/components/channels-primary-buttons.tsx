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
import { Gift, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { useChannels } from './channels-provider'

export function ChannelsPrimaryButtons() {
  const { t } = useTranslation()
  const { setOpen, setCurrentRow } = useChannels()

  return (
    <div className='flex flex-wrap items-center gap-2'>
      {/* [M2-FE] Add from Free Source */}
      <Button variant='outline' onClick={() => setOpen('from-template')}>
        <Gift className='h-4 w-4' />
        <span className='max-sm:hidden'>{t('Add from Free Source')}</span>
        <span className='sm:hidden'>{t('Free Source')}</span>
      </Button>
      {/* Create Channel */}
      <Button
        onClick={() => {
          setCurrentRow(null)
          setOpen('create-channel')
        }}
      >
        <Plus className='h-4 w-4' />
        <span className='max-sm:hidden'>{t('Create Channel')}</span>
        <span className='sm:hidden'>{t('Create')}</span>
      </Button>
    </div>
  )
}

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
import { useTranslation } from 'react-i18next'

import { GroupSelector, ModelSelector } from '@/components/model-group-selector'

import type { DemoGroupOption, DemoModelOption } from '../types'

interface ModelPickerProps {
  groups: DemoGroupOption[]
  models: DemoModelOption[]
  value: string
  group: string
  disabled?: boolean
  isLoading?: boolean
  onGroupChange: (group: string) => void
  onChange: (model: string) => void
}

export function ModelPicker(props: ModelPickerProps) {
  const { t } = useTranslation()
  return (
    <div className='ds-demo-model-controls' aria-label={t('Model and group')}>
      <ModelSelector
        selectedModel={props.value}
        models={props.models}
        onModelChange={props.onChange}
        disabled={
          props.disabled || props.isLoading || props.models.length === 0
        }
        className='max-w-[min(45vw,16rem)] rounded-full'
      />
      {props.groups.length > 1 && (
        <GroupSelector
          selectedGroup={props.group}
          groups={props.groups}
          onGroupChange={props.onGroupChange}
          disabled={props.disabled}
          className='max-w-28 rounded-full'
        />
      )}
      {props.isLoading && (
        <span role='status' className='sr-only'>
          {t('Loading models...')}
        </span>
      )}
    </div>
  )
}

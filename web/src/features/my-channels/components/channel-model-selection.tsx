import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

type ChannelModelSelectionProps = {
  models: string[]
  enabledModels: string[]
  publicModels: string[]
  onChange: (enabledModels: string[], publicModels: string[]) => void
  disabled?: boolean
  renderModelAction?: (model: string) => ReactNode
}

/** Two independent choices, with public models always a subset of enabled models.
 * UpstreamModelSelection is a single-selection-list editor; this composes the
 * same shared primitives for the owner/public publishing contract.
 */
export function ChannelModelSelection(props: ChannelModelSelectionProps) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const models = useMemo(
    () => [
      ...new Set(props.models.map((model) => model.trim()).filter(Boolean)),
    ],
    [props.models]
  )
  const visibleModels = useMemo(() => {
    const keyword = search.trim().toLowerCase()
    return models.filter((model) => model.toLowerCase().includes(keyword))
  }, [models, search])

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap gap-2'>
        <Button
          type='button'
          size='sm'
          variant='secondary'
          disabled={props.disabled || models.length === 0}
          onClick={() => props.onChange([...models], props.publicModels)}
        >
          {t('Select all for my channels')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={props.disabled || props.enabledModels.length === 0}
          onClick={() => props.onChange([], [])}
        >
          {t('Deselect all for my channels')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={props.disabled || models.length === 0}
          onClick={() => {
            const enabledSet = new Set(props.enabledModels)
            const nextEnabled = models.filter((model) => !enabledSet.has(model))
            const nextEnabledSet = new Set(nextEnabled)
            // Keep public models a subset of the inverted enabled selection.
            props.onChange(
              nextEnabled,
              props.publicModels.filter((model) => nextEnabledSet.has(model))
            )
          }}
        >
          {t('Invert selection for my channels')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={props.disabled || models.length === 0}
          onClick={() => props.onChange([...models], [...models])}
        >
          {t('Select all for the public pool')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={props.disabled || props.publicModels.length === 0}
          onClick={() => props.onChange(props.enabledModels, [])}
        >
          {t('Deselect all for the public pool')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={props.disabled || models.length === 0}
          onClick={() => {
            const publicSet = new Set(props.publicModels)
            const nextPublic = models.filter((model) => !publicSet.has(model))
            // Publishing auto-enables; keep enabled a superset of public.
            props.onChange(
              [...new Set([...props.enabledModels, ...nextPublic])],
              nextPublic
            )
          }}
        >
          {t('Invert selection for the public pool')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t('Bulk actions apply to all models, including filtered-out models.')}
      </p>
      <p className='text-muted-foreground text-xs' aria-live='polite'>
        {t('Enabled: {{enabled}} / {{total}} · Public: {{public}}', {
          enabled: props.enabledModels.length,
          total: models.length,
          public: props.publicModels.length,
        })}
      </p>
      <Input
        type='search'
        aria-label={t('Search models')}
        placeholder={t('Search models...')}
        value={search}
        onChange={(event) => setSearch(event.target.value)}
      />
      <div
        className='max-h-80 space-y-1 overflow-y-auto overscroll-contain rounded-lg border p-2'
        role='group'
        aria-label={t('Channel models')}
      >
        {models.length === 0 && (
          <p className='text-muted-foreground py-4 text-center text-xs'>
            {t('Fetch upstream models or add a model to begin.')}
          </p>
        )}
        {models.length > 0 && visibleModels.length === 0 && (
          <p className='text-muted-foreground py-4 text-center text-xs'>
            {t('No model found.')}
          </p>
        )}
        {visibleModels.map((model) => {
          const enabled = props.enabledModels.includes(model)
          const published = props.publicModels.includes(model)
          return (
            <div
              key={model}
              className='hover:bg-muted/60 flex flex-wrap items-center justify-between gap-2 rounded-md px-2 py-2 text-xs'
            >
              <span className='min-w-0 flex-1 font-mono font-medium break-all'>
                {model}
              </span>
              <div className='flex flex-wrap items-center gap-4'>
                <Label className='text-xs'>
                  <Checkbox
                    checked={enabled}
                    disabled={props.disabled}
                    aria-label={t('Enable {{model}} in my channels', { model })}
                    onCheckedChange={(checked) =>
                      props.onChange(
                        checked
                          ? [...new Set([...props.enabledModels, model])]
                          : props.enabledModels.filter(
                              (item) => item !== model
                            ),
                        checked
                          ? props.publicModels
                          : props.publicModels.filter((item) => item !== model)
                      )
                    }
                  />
                  <span aria-hidden='true'>{t('Enable in my channels')}</span>
                </Label>
                <Label className='text-xs'>
                  <Checkbox
                    checked={published}
                    disabled={props.disabled}
                    aria-label={t('Publish {{model}} to the public pool', {
                      model,
                    })}
                    onCheckedChange={(checked) =>
                      props.onChange(
                        checked
                          ? [...new Set([...props.enabledModels, model])]
                          : props.enabledModels,
                        checked
                          ? [...new Set([...props.publicModels, model])]
                          : props.publicModels.filter((item) => item !== model)
                      )
                    }
                  />
                  <span aria-hidden='true'>{t('Public pool')}</span>
                </Label>
                {props.renderModelAction?.(model)}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

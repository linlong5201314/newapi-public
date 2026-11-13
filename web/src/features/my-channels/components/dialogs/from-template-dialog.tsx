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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, ExternalLink, Gift, Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import {
  createChannelFromTemplate,
  getFreeTemplates,
  type FreeSourceTemplate,
} from '../../free-sources'
import { channelsQueryKeys } from '../../lib'

type FromTemplateDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const TEMPLATE_SKELETON_IDS = ['tpl-sk-1', 'tpl-sk-2', 'tpl-sk-3', 'tpl-sk-4']

/** ToS marker → status dot color. */
function tosDotClass(tos?: string): string {
  switch (tos) {
    case 'ok':
      return 'bg-success'
    case 'caution':
      return 'bg-warning'
    case 'avoid':
      return 'bg-destructive'
    default:
      return 'bg-muted-foreground'
  }
}

function tosLabelKey(tos?: string): string {
  switch (tos) {
    case 'ok':
      return 'ToS: friendly'
    case 'caution':
      return 'ToS: review recommended'
    case 'avoid':
      return 'ToS: risky'
    default:
      return 'ToS: unknown'
  }
}

export function FromTemplateDialog({
  open,
  onOpenChange,
}: FromTemplateDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState<FreeSourceTemplate | null>(null)
  const [apiKey, setApiKey] = useState('')
  const [customName, setCustomName] = useState('')
  const [extraValues, setExtraValues] = useState<Record<string, string>>({})
  const [proxyMode, setProxyMode] = useState<'pool' | 'direct' | 'custom'>('pool')
  const [proxyUrl, setProxyUrl] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)

  const { data: templates, isLoading } = useQuery({
    queryKey: [...channelsQueryKeys.all, 'free-templates'],
    queryFn: getFreeTemplates,
    enabled: open,
    staleTime: 5 * 60_000,
  })

  // Reset the wizard whenever the dialog is reopened.
  useEffect(() => {
    if (!open) return
    setSelected(null)
    setApiKey('')
    setCustomName('')
    setExtraValues({})
    setProxyMode('pool')
    setProxyUrl('')
    setIsSubmitting(false)
  }, [open])

  const keyRequired = selected?.keyRequired !== false
  const extraFields = selected?.extraFields ?? []
  const extrasComplete = extraFields.every(
    (field) => (extraValues[field] ?? '').trim() !== ''
  )
  const canSubmit =
    selected !== null &&
    (!keyRequired || apiKey.trim() !== '') &&
    extrasComplete

  const handleSubmit = async () => {
    if (!selected || !canSubmit) return
    setIsSubmitting(true)
    try {
      const extra = extraFields.length
        ? Object.fromEntries(
            extraFields.map((field) => [
              field,
              (extraValues[field] ?? '').trim(),
            ])
          )
        : undefined
      const proxy =
        proxyMode === 'direct'
          ? 'direct'
          : proxyMode === 'custom'
            ? proxyUrl.trim()
            : ''
      const result = await createChannelFromTemplate({
        templateId: selected.id,
        key: apiKey.trim(),
        ...(customName.trim() ? { name: customName.trim() } : {}),
        ...(extra ? { extra } : {}),
        ...(proxy ? { proxy } : {}),
      })
      if (result.success) {
        toast.success(t('Channel created from free source'))
        queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.lists(),
        })
        queryClient.invalidateQueries({
          queryKey: [...channelsQueryKeys.all, 'free-usage'],
        })
        onOpenChange(false)
      } else {
        handleServerError(result, t('Failed to create channel from template'))
      }
    } catch (error) {
      handleServerError(error, t('Failed to create channel from template'))
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[85vh] flex-col sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <Gift className='size-4' />
            {t('Add from Free Source')}
          </DialogTitle>
          <DialogDescription>
            {selected
              ? t('Enter the API key for {{name}}.', { name: selected.name })
              : t(
                  'Pick a free provider template. Models and endpoint are prefilled.'
                )}
          </DialogDescription>
        </DialogHeader>

        {selected === null ? (
          <div className='min-h-0 flex-1 overflow-y-auto pr-1'>
            {(() => {
              if (isLoading) {
                return (
                  <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                    {TEMPLATE_SKELETON_IDS.map((id) => (
                      <Skeleton key={id} className='h-24 rounded-lg' />
                    ))}
                  </div>
                )
              }
              if (!templates || templates.length === 0) {
                return (
                  <div className='text-muted-foreground rounded-lg border border-dashed p-8 text-center text-sm'>
                    {t(
                      'No free source templates available yet. Check back after the backend ships them.'
                    )}
                  </div>
                )
              }
              return null
            })()}
            {!isLoading && templates && templates.length > 0 && (
              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                {templates.map((template) => (
                  <button
                    key={template.id}
                    type='button'
                    onClick={() => setSelected(template)}
                    className='border-border/60 hover:border-primary/60 hover:bg-muted/40 rounded-lg border p-3 text-start transition-colors'
                  >
                    <div className='flex items-center justify-between gap-2'>
                      <span className='min-w-0 truncate text-sm font-medium'>
                        {template.name}
                      </span>
                      <span
                        className='flex shrink-0 items-center gap-1.5'
                        title={t(tosLabelKey(template.tos))}
                      >
                        <span
                          className={cn(
                            'size-2 rounded-full',
                            tosDotClass(template.tos)
                          )}
                        />
                        <span className='text-muted-foreground text-xs'>
                          {t(tosLabelKey(template.tos))}
                        </span>
                      </span>
                    </div>
                    {template.freeNote && (
                      <div className='text-muted-foreground mt-1 text-xs'>
                        {template.freeNote}
                      </div>
                    )}
                    {template.models && template.models.length > 0 && (
                      <div className='text-muted-foreground mt-1 text-xs'>
                        {t('{{count}} models', {
                          count: template.models.length,
                        })}
                      </div>
                    )}
                  </button>
                ))}
              </div>
            )}
          </div>
        ) : (
          <div className='flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto pr-1'>
            <div className='border-border/60 rounded-lg border p-3'>
              <div className='flex items-center justify-between gap-2'>
                <span className='text-sm font-medium'>{selected.name}</span>
                <span
                  className='flex shrink-0 items-center gap-1.5'
                  title={t(tosLabelKey(selected.tos))}
                >
                  <span
                    className={cn(
                      'size-2 rounded-full',
                      tosDotClass(selected.tos)
                    )}
                  />
                  <span className='text-muted-foreground text-xs'>
                    {t(tosLabelKey(selected.tos))}
                  </span>
                </span>
              </div>
              {selected.freeNote && (
                <div className='text-muted-foreground mt-1 text-xs'>
                  {selected.freeNote}
                </div>
              )}
              {selected.signupUrl && (
                <a
                  href={selected.signupUrl}
                  target='_blank'
                  rel='noreferrer'
                  className='text-primary mt-2 inline-flex items-center gap-1 text-xs hover:underline'
                >
                  {t('Get an API key')}
                  <ExternalLink className='size-3' />
                </a>
              )}
            </div>

            <div className='flex flex-col gap-2'>
              <Label htmlFor='from-template-key'>
                {t('API Key')}
                {keyRequired && <span className='text-destructive'> *</span>}
              </Label>
              <Input
                id='from-template-key'
                type='password'
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder={t('Paste the API key for this provider')}
                autoComplete='off'
              />
            </div>

            {/* Extra placeholder fields required by the template
                (e.g. cloudflare-ai accountId), substituted into baseUrl. */}
            {extraFields.map((field) => (
              <div key={field} className='flex flex-col gap-2'>
                <Label htmlFor={`from-template-extra-${field}`}>
                  {field}
                  <span className='text-destructive'> *</span>
                </Label>
                <Input
                  id={`from-template-extra-${field}`}
                  value={extraValues[field] ?? ''}
                  onChange={(e) =>
                    setExtraValues((prev) => ({
                      ...prev,
                      [field]: e.target.value,
                    }))
                  }
                  placeholder={t('Required by this template')}
                  autoComplete='off'
                />
              </div>
            ))}

            <div className='flex flex-col gap-2'>
              <Label htmlFor='from-template-proxy'>
                {t('Outbound proxy for this channel')}
              </Label>
              <select
                id='from-template-proxy'
                value={proxyMode}
                onChange={(e) =>
                  setProxyMode(e.target.value as 'pool' | 'direct' | 'custom')
                }
                className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
              >
                <option value='pool'>
                  {t('Follow my egress IP pool (rotate per request)')}
                </option>
                <option value='direct'>
                  {t('Direct connection (no proxy)')}
                </option>
                <option value='custom'>{t('Fixed proxy URL')}</option>
              </select>
              {proxyMode === 'custom' && (
                <Input
                  value={proxyUrl}
                  onChange={(e) => setProxyUrl(e.target.value)}
                  placeholder={t('e.g. http://1.2.3.4:8080 or socks5://host:1080')}
                  autoComplete='off'
                />
              )}
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Reverse-proxy accounts carry risk: use a throwaway account, never your main one.'
                )}
              </p>
            </div>

            <div className='flex flex-col gap-2'>
              <Label htmlFor='from-template-name'>
                {t('Channel Name (optional)')}
              </Label>
              <Input
                id='from-template-name'
                value={customName}
                onChange={(e) => setCustomName(e.target.value)}
                placeholder={selected.name}
                maxLength={64}
              />
            </div>
          </div>
        )}

        <DialogFooter className='gap-2'>
          {selected !== null && (
            <Button
              variant='ghost'
              onClick={() => setSelected(null)}
              disabled={isSubmitting}
            >
              <ArrowLeft className='size-4' />
              {t('Back')}
            </Button>
          )}
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          {selected !== null && (
            <Button
              onClick={() => void handleSubmit()}
              disabled={!canSubmit || isSubmitting}
            >
              {isSubmitting && <Loader2 className='size-4 animate-spin' />}
              {t('Create Channel')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}


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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  CircleAlert,
  ListOrdered,
  Route,
  Settings2,
  Trash2,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import {
  useFieldArray,
  useForm,
  type SubmitErrorHandler,
} from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SideDrawerSection,
  SideDrawerSectionHeader,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { MultiSelect } from '@/components/multi-select'
import {
  Alert,
  AlertDescription,
} from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { getChannels } from '@/features/my-channels/api'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import { createRouteRule, updateRouteRule } from '../api'
import { ERROR_MESSAGES, STRATEGY_OPTIONS, SUCCESS_MESSAGES } from '../constants'
import {
  getRouteRuleFormDefaultValues,
  getRouteRuleFormSchema,
  routeRulesQueryKeys,
  transformFormValuesToPayload,
  transformRouteRuleToFormDefaults,
  type RouteRuleFormValues,
} from '../lib'
import type { RouteRule } from '../types'
import { useRouteRules } from './route-rules-provider'

type RouteRuleMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow: RouteRule | null
}

type ChannelOption = {
  id: number
  name: string
  status: number
}

export function RouteRuleMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: RouteRuleMutateDrawerProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { setOpen } = useRouteRules()
  const isUpdate = currentRow !== null
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [advancedOpen, setAdvancedOpen] = useState(false)

  const schema = useMemo(() => getRouteRuleFormSchema(t), [t])

  const form = useForm<RouteRuleFormValues>({
    resolver: zodResolver(schema),
    defaultValues: getRouteRuleFormDefaultValues(),
  })

  const targetsArray = useFieldArray({
    control: form.control,
    name: 'targets',
  })

  // Load the current user's channels for the target picker.
  // Fail-soft: an unreachable/403 self API degrades to an empty option list
  // with an inline warning instead of breaking the drawer.
  const { data: channelOptions, isError: channelsFailed } = useQuery({
    queryKey: ['route-rules', 'channel-options'],
    queryFn: async (): Promise<ChannelOption[]> => {
      try {
        const res = await getChannels({ p: 1, page_size: 200 })
        if (!res.success) return []
        return (res.data?.items || []).map((channel) => ({
          id: channel.id,
          name: channel.name,
          status: channel.status,
        }))
      } catch {
        return []
      }
    },
    enabled: open,
    staleTime: 30_000,
  })

  const channels = useMemo(() => channelOptions || [], [channelOptions])
  const channelNameById = useMemo(() => {
    const map = new Map<number, string>()
    for (const channel of channels) map.set(channel.id, channel.name)
    return map
  }, [channels])

  const multiSelectOptions = useMemo(
    () =>
      channels.map((channel) => ({
        value: String(channel.id),
        label: channel.name,
      })),
    [channels]
  )

  // Reset the form whenever the drawer opens for a different target.
  useEffect(() => {
    if (!open) return
    if (isUpdate && currentRow) {
      form.reset(transformRouteRuleToFormDefaults(currentRow))
    } else {
      form.reset(getRouteRuleFormDefaultValues())
    }
    setAdvancedOpen(false)
  }, [open, isUpdate, currentRow, form])

  const selectedChannelIds = targetsArray.fields.map((field) =>
    String(field.channelId)
  )

  // Reconcile the MultiSelect selection with the field array: append new
  // channels with defaults, drop deselected ones, keep existing order.
  const handleSelectionChange = (values: string[]) => {
    const nextIds = values
      .map((value) => Number(value))
      .filter((id) => Number.isInteger(id) && id > 0)
    const current = form.getValues('targets')

    const kept = current.filter((target) =>
      nextIds.includes(target.channelId)
    )
    const keptIds = new Set(kept.map((target) => target.channelId))
    const appended = nextIds
      .filter((id) => !keptIds.has(id))
      .map((channelId) => ({
        channelId,
        weight: 1,
        fallbackOnlyOnQuotaExhaustion: false,
      }))

    targetsArray.replace([...kept, ...appended])
    form.trigger('targets')
  }

  const moveTarget = (index: number, direction: -1 | 1) => {
    const next = index + direction
    if (next < 0 || next >= targetsArray.fields.length) return
    targetsArray.move(index, next)
  }

  const onSubmit = async (values: RouteRuleFormValues) => {
    setIsSubmitting(true)
    try {
      const payload = transformFormValuesToPayload(values)
      const result =
        isUpdate && currentRow
          ? await updateRouteRule(currentRow.id, payload)
          : await createRouteRule(payload)

      if (result.success) {
        toast.success(
          t(isUpdate ? SUCCESS_MESSAGES.UPDATED : SUCCESS_MESSAGES.CREATED)
        )
        queryClient.invalidateQueries({
          queryKey: routeRulesQueryKeys.lists(),
        })
        setOpen(null)
      } else {
        handleServerError(
          result,
          t(isUpdate ? ERROR_MESSAGES.UPDATE_FAILED : ERROR_MESSAGES.CREATE_FAILED)
        )
      }
    } catch (error) {
      handleServerError(error, t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setIsSubmitting(false)
    }
  }

  const onInvalid: SubmitErrorHandler<RouteRuleFormValues> = () => {
    toast.error(t('Please fix the highlighted fields before saving'))
  }

  let submitLabel = t('Create')
  if (isSubmitting) {
    submitLabel = t('Saving...')
  } else if (isUpdate) {
    submitLabel = t('Save Changes')
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        className={sideDrawerContentClassName('max-w-none sm:!max-w-[620px]')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isUpdate ? t('Update Route Rule') : t('Create Route Rule')}
          </SheetTitle>
          <SheetDescription>
            {t(
              'The rule name becomes a virtual model your API key can call; requests are routed across the target channels below.'
            )}
          </SheetDescription>
        </SheetHeader>
        <Form {...form}>
          <form
            id='route-rule-form'
            onSubmit={form.handleSubmit(onSubmit, onInvalid)}
            aria-busy={isSubmitting}
            className={sideDrawerFormClassName('gap-5')}
          >
            {/* Basic */}
            <SideDrawerSection>
              <SideDrawerSectionHeader
                title={t('Basic Information')}
                description={t('Name the rule and pick a routing strategy')}
                icon={<Route className='size-4' />}
                iconTone='info'
              />
              <FormField
                control={form.control}
                name='name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Name')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder={t('e.g. free-chat')}
                        maxLength={64}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'This name is also the model name you call through the API.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='strategy'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Strategy')}</FormLabel>
                    <Select
                      value={field.value}
                      onValueChange={field.onChange}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue placeholder={t('Select a strategy')} />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        {STRATEGY_OPTIONS.map((option) => (
                          <SelectItem
                            key={option.value}
                            value={option.value}
                          >
                            <div className='flex flex-col gap-0.5 py-0.5'>
                              <span>{t(option.labelKey)}</span>
                              <span className='text-muted-foreground text-xs'>
                                {t(option.descriptionKey)}
                              </span>
                            </div>
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <FormDescription>
                      {t(
                        STRATEGY_OPTIONS.find(
                          (option) => option.value === field.value
                        )?.descriptionKey ?? ''
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='enabled'
                render={({ field }) => (
                  <div className={sideDrawerSwitchItemClassName()}>
                    <div className='min-w-0 flex-1'>
                      <FormLabel>{t('Enabled')}</FormLabel>
                      <FormDescription>
                        {t('Disabled rules are skipped during routing.')}
                      </FormDescription>
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </div>
                )}
              />
            </SideDrawerSection>

            {/* Targets */}
            <SideDrawerSection>
              <SideDrawerSectionHeader
                title={t('Target Channels')}
                description={t(
                  'Pick your own channels; order matters for priority and fallback.'
                )}
                icon={<ListOrdered className='size-4' />}
                iconTone='info'
              />

              {channelsFailed && (
                <Alert variant='destructive'>
                  <CircleAlert className='h-4 w-4' />
                  <AlertDescription>
                    {t(
                      'Could not load your channels. You can still save, but verify the target IDs.'
                    )}
                  </AlertDescription>
                </Alert>
              )}

              <FormField
                control={form.control}
                name='targets'
                render={() => (
                  <FormItem>
                    <FormLabel>{t('Channels')}</FormLabel>
                    <FormControl>
                      <MultiSelect
                        options={multiSelectOptions}
                        selected={selectedChannelIds}
                        onChange={handleSelectionChange}
                        placeholder={t('Select target channels')}
                        emptyText={t('No channels yet. Create one first.')}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {targetsArray.fields.length > 0 && (
                <div className='flex flex-col gap-2'>
                  {targetsArray.fields.map((fieldItem, index) => (
                    <div
                      key={fieldItem.id}
                      className='border-border/60 flex flex-col gap-2 rounded-lg border p-3'
                    >
                      <div className='flex items-center gap-2'>
                        <span className='text-muted-foreground w-5 text-center text-xs font-medium'>
                          {index + 1}
                        </span>
                        <span className='min-w-0 flex-1 truncate text-sm font-medium'>
                          {channelNameById.get(fieldItem.channelId) ??
                            `#${fieldItem.channelId}`}
                        </span>
                        <div className='flex items-center gap-1'>
                          <Button
                            type='button'
                            variant='ghost'
                            size='icon-sm'
                            onClick={() => moveTarget(index, -1)}
                            disabled={index === 0}
                            aria-label={t('Move up')}
                          >
                            <ArrowUp className='size-4' />
                          </Button>
                          <Button
                            type='button'
                            variant='ghost'
                            size='icon-sm'
                            onClick={() => moveTarget(index, 1)}
                            disabled={
                              index === targetsArray.fields.length - 1
                            }
                            aria-label={t('Move down')}
                          >
                            <ArrowDown className='size-4' />
                          </Button>
                          <Button
                            type='button'
                            variant='ghost'
                            size='icon-sm'
                            onClick={() => {
                              targetsArray.remove(index)
                              form.trigger('targets')
                            }}
                            aria-label={t('Remove')}
                            className='text-destructive hover:text-destructive'
                          >
                            <Trash2 className='size-4' />
                          </Button>
                        </div>
                      </div>
                      <div className='flex flex-wrap items-center gap-x-4 gap-y-2'>
                        <div className='flex items-center gap-2'>
                          <span className='text-muted-foreground text-xs'>
                            {t('Weight')}
                          </span>
                          <Input
                            type='number'
                            min={0}
                            step={1}
                            className='h-8 w-20'
                            value={form.watch(`targets.${index}.weight`)}
                            onChange={(e) => {
                              const parsed = Number(e.target.value)
                              form.setValue(
                                `targets.${index}.weight`,
                                Number.isFinite(parsed)
                                  ? Math.max(0, Math.trunc(parsed))
                                  : 0,
                                { shouldDirty: true }
                              )
                            }}
                          />
                        </div>
                        <div className='flex items-center gap-2'>
                          <Switch
                            checked={form.watch(
                              `targets.${index}.fallbackOnlyOnQuotaExhaustion`
                            )}
                            onCheckedChange={(checked) =>
                              form.setValue(
                                `targets.${index}.fallbackOnlyOnQuotaExhaustion`,
                                checked,
                                { shouldDirty: true }
                              )
                            }
                          />
                          <span className='text-muted-foreground text-xs'>
                            {t('Only when quota exhausted')}
                          </span>
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </SideDrawerSection>

            {/* Advanced */}
            <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
              <CollapsibleTrigger
                render={
                  <Button
                    type='button'
                    variant='ghost'
                    className='w-full justify-between px-2'
                  />
                }
              >
                <span className='flex items-center gap-2 text-sm font-medium'>
                  <Settings2 className='size-4' />
                  {t('Advanced')}
                </span>
                <ChevronDown
                  className={cn(
                    'size-4 transition-transform',
                    advancedOpen && 'rotate-180'
                  )}
                />
              </CollapsibleTrigger>
              <CollapsibleContent className='pt-4'>
                <div className='grid grid-cols-1 gap-4 sm:grid-cols-3'>
                  <FormField
                    control={form.control}
                    name='maxRetries'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Max Retries')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={0}
                            step={1}
                            value={field.value}
                            onChange={(e) =>
                              field.onChange(
                                Math.max(
                                  0,
                                  Math.trunc(Number(e.target.value) || 0)
                                )
                              )
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='retryDelayMs'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Retry Delay (ms)')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={0}
                            step={100}
                            value={field.value}
                            onChange={(e) =>
                              field.onChange(
                                Math.max(
                                  0,
                                  Math.trunc(Number(e.target.value) || 0)
                                )
                              )
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='targetTimeoutMs'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Target Timeout (ms)')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={0}
                            step={1000}
                            value={field.value}
                            onChange={(e) =>
                              field.onChange(
                                Math.max(
                                  0,
                                  Math.trunc(Number(e.target.value) || 0)
                                )
                              )
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>
              </CollapsibleContent>
            </Collapsible>
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose
            render={
              <Button variant='outline' disabled={isSubmitting}>
                {t('Cancel')}
              </Button>
            }
          />
          <Button
            type='submit'
            form='route-rule-form'
            disabled={isSubmitting}
          >
            {submitLabel}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

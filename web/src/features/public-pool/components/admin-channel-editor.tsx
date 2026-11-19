import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { CHANNEL_TYPE_OPTIONS } from '@/features/channels/constants'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  PUBLIC_ADMIN_CHANNELS_KEY,
  savePublicAdminChannel,
  type PublicAdminChannel,
  type PublicAdminChannelInput,
} from '../admin-api'
import { PUBLIC_CATALOG_QUERY_KEY } from '../constants'

const schema = z.object({
  name: z.string().trim().min(1).max(128),
  models: z.string().trim().min(1),
  group: z.string().trim().min(1).max(64),
  type: z.number().int().positive(),
  key: z.string(),
  base_url: z
    .string()
    .refine(
      (value) => !value || /^https?:\/\/[^/]+/i.test(value),
      'Invalid URL'
    ),
  priority: z.number().int().min(0).max(2147483647),
  weight: z.number().int().min(0).max(2147483647),
})
type Values = z.infer<typeof schema>

interface Props {
  channel: PublicAdminChannel | null
  onClose: () => void
}

export function PublicAdminChannelEditor(props: Props) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: props.channel?.name ?? '',
      models: props.channel?.models ?? '',
      group: props.channel?.group ?? 'default',
      type: props.channel?.type ?? 1,
      key: '',
      base_url: '',
      priority: props.channel?.priority ?? 0,
      weight: props.channel?.weight ?? 0,
    },
  })
  const save = useMutation({
    mutationFn: (input: PublicAdminChannelInput) =>
      savePublicAdminChannel(input, props.channel?.id),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: PUBLIC_ADMIN_CHANNELS_KEY })
      void client.invalidateQueries({ queryKey: PUBLIC_CATALOG_QUERY_KEY })
      props.onClose()
    },
    onError: (error) => handleServerError(error),
  })

  function submit(values: Values) {
    if (!props.channel && !values.key.trim()) {
      form.setError('key', { type: 'required', message: t('Required') })
      return
    }
    const input: PublicAdminChannelInput = {
      name: values.name,
      type: values.type,
      models: values.models,
      group: values.group,
      priority: values.priority,
      weight: values.weight,
    }
    if (values.key.trim()) input.key = values.key
    if (values.base_url.trim()) input.base_url = values.base_url.trim()
    save.mutate(input)
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) props.onClose()
      }}
      title={
        props.channel
          ? t('Edit public free channel')
          : t('Add public free channel')
      }
      description={t(
        'Credentials and endpoint URLs are write-only. Leave replacements blank to keep the saved values.'
      )}
      contentClassName='sm:max-w-xl'
      footer={
        <>
          <Button
            variant='outline'
            disabled={save.isPending}
            onClick={props.onClose}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='public-free-channel-editor'
            disabled={save.isPending}
          >
            {save.isPending ? t('Saving...') : t('Save')}
          </Button>
        </>
      }
    >
      <form
        id='public-free-channel-editor'
        onSubmit={form.handleSubmit(submit)}
        className='space-y-4'
        noValidate
      >
        <p className='bg-muted text-muted-foreground rounded-lg p-3 text-sm'>
          {t(
            'New and copied channels stay disabled until you test and explicitly enable them.'
          )}
        </p>
        {save.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {getServerErrorMessage(save.error)}
          </p>
        )}
        <fieldset disabled={save.isPending} className='space-y-4'>
          <div className='space-y-2'>
            <Label htmlFor='public-channel-name'>{t('Channel name')}</Label>
            <Input
              id='public-channel-name'
              {...form.register('name')}
              aria-invalid={!!form.formState.errors.name}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='public-channel-type'>{t('Channel Type')}</Label>
            <NativeSelect
              id='public-channel-type'
              {...form.register('type', { valueAsNumber: true })}
              disabled={!!props.channel}
            >
              {CHANNEL_TYPE_OPTIONS.map((option) => (
                <NativeSelectOption key={option.value} value={option.value}>
                  {t(option.label)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          <div className='space-y-2'>
            <Label htmlFor='public-channel-models'>{t('Model IDs')}</Label>
            <Input
              id='public-channel-models'
              {...form.register('models')}
              aria-invalid={!!form.formState.errors.models}
              placeholder={t('Comma-separated model IDs')}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='public-channel-group'>{t('Group')}</Label>
            <Input
              id='public-channel-group'
              {...form.register('group')}
              aria-invalid={!!form.formState.errors.group}
            />
          </div>
          <div className='grid grid-cols-2 gap-3'>
            <div className='space-y-2'>
              <Label htmlFor='public-channel-priority'>{t('Priority')}</Label>
              <Input
                id='public-channel-priority'
                type='number'
                min={0}
                {...form.register('priority', { valueAsNumber: true })}
                aria-invalid={!!form.formState.errors.priority}
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor='public-channel-weight'>{t('Weight')}</Label>
              <Input
                id='public-channel-weight'
                type='number'
                min={0}
                {...form.register('weight', { valueAsNumber: true })}
                aria-invalid={!!form.formState.errors.weight}
              />
            </div>
          </div>
          <div className='space-y-2'>
            <Label htmlFor='public-channel-url'>
              {t('Base URL (write-only)')}
            </Label>
            <Input
              id='public-channel-url'
              type='password'
              autoComplete='off'
              {...form.register('base_url')}
              aria-invalid={!!form.formState.errors.base_url}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='public-channel-key'>
              {t('Provider key (write-only)')}
            </Label>
            <Input
              id='public-channel-key'
              type='password'
              autoComplete='new-password'
              {...form.register('key')}
              aria-invalid={!!form.formState.errors.key}
            />
          </div>
          {Object.keys(form.formState.errors).length > 0 && (
            <p role='alert' className='text-destructive text-sm'>
              {t('Check the required channel fields and valid values.')}
            </p>
          )}
        </fieldset>
      </form>
    </Dialog>
  )
}

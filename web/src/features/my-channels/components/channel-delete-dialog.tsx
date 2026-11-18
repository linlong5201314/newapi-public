import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { deleteChannel } from '../api'
import { channelsQueryKeys } from '../lib'

export function ChannelDeleteDialog(props: {
  channelId: number
  channelName: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onDeleted?: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const mutation = useMutation({
    mutationFn: async () =>
      requireServerSuccess(await deleteChannel(props.channelId)),
    onSuccess: () => {
      toast.success(t('Channel deleted'))
      props.onOpenChange(false)
      props.onDeleted?.()
      void client.invalidateQueries({ queryKey: channelsQueryKeys.all })
      client.removeQueries({
        queryKey: ['self-channel-detail', props.channelId],
      })
    },
    onError: (error) => handleServerError(error, t('Failed to delete channel')),
  })
  return (
    <ConfirmDialog
      open={props.open}
      onOpenChange={(open) => {
        if (!mutation.isPending) props.onOpenChange(open)
      }}
      title={t('Delete channel')}
      desc={t(
        'Delete channel “{{name}}” and all its model configuration? This cannot be undone.',
        { name: props.channelName }
      )}
      confirmText={t('Delete channel')}
      destructive
      isLoading={mutation.isPending}
      handleConfirm={() => mutation.mutate()}
    />
  )
}

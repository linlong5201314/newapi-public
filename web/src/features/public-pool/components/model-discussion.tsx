import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { MessageSquare } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { toIntlLocale } from '@/i18n/languages'
import { handleServerError } from '@/lib/handle-server-error'

import { addPublicComment, listPublicComments } from '../api'

export function ModelDiscussion(props: { model: string; channelId: number }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const panelId = useId()
  const inputId = useId()
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const [rating, setRating] = useState(5)
  const query = useQuery({
    queryKey: ['public-pool', 'comments', props.model],
    queryFn: () => listPublicComments(props.model),
    enabled: open,
    meta: { errorToast: false },
  })
  const mutation = useMutation({
    mutationFn: () =>
      addPublicComment({
        model: props.model,
        channel_id: props.channelId,
        content: text.trim(),
        rating,
      }),
    onSuccess: async () => {
      setText('')
      toast.success(t('Comment published'))
      await client.invalidateQueries({
        queryKey: ['public-pool', 'comments', props.model],
      })
    },
    onError: (error: unknown) =>
      handleServerError(error, t('Unable to publish comment')),
  })

  return (
    <div className='w-full min-w-0'>
      <Button
        variant='ghost'
        size='sm'
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen(!open)}
      >
        <MessageSquare aria-hidden className='size-4' />
        {t('Model discussion')}
      </Button>
      {open && (
        <div id={panelId} className='mt-2 min-w-0 space-y-3 border-t pt-3'>
          <p className='text-muted-foreground text-xs'>
            {t('Discussion is shared across all sources of this model.')}
          </p>
          {query.isPending && (
            <Skeleton
              aria-label={t('Loading comments')}
              className='h-16 w-full'
            />
          )}
          {query.isError && (
            <ErrorState
              className='min-h-32'
              title={t('Unable to load comments')}
              onRetry={() => void query.refetch()}
            />
          )}
          {query.isSuccess &&
            (query.data.length === 0 ? (
              <p className='text-muted-foreground text-xs'>
                {t('No comments yet.')}
              </p>
            ) : (
              <ul
                aria-label={t('Model comments')}
                className='max-h-64 space-y-3 overflow-y-auto'
              >
                {query.data.map((comment) => (
                  <li key={comment.id} className='flex min-w-0 gap-2'>
                    <Avatar className='size-6 shrink-0'>
                      {comment.avatar && (
                        <AvatarImage src={comment.avatar} alt='' />
                      )}
                      <AvatarFallback>
                        {(comment.display_name || comment.username || '?')
                          .slice(0, 1)
                          .toUpperCase()}
                      </AvatarFallback>
                    </Avatar>
                    <div className='min-w-0 space-y-1 text-xs'>
                      <p className='flex flex-wrap gap-2'>
                        <span className='font-medium'>
                          {comment.display_name ||
                            comment.username ||
                            t('Anonymous user')}
                        </span>
                        <time
                          className='text-muted-foreground'
                          dateTime={new Date(
                            comment.created_at * 1000
                          ).toISOString()}
                        >
                          {new Date(comment.created_at * 1000).toLocaleString(
                            locale
                          )}
                        </time>
                      </p>
                      <p className='break-words whitespace-pre-wrap'>
                        {comment.content}
                      </p>
                    </div>
                  </li>
                ))}
              </ul>
            ))}
          <form
            className='flex min-w-0 flex-col gap-2'
            onSubmit={(event) => {
              event.preventDefault()
              if (text.trim() && !mutation.isPending) mutation.mutate()
            }}
          >
            <label htmlFor={inputId} className='text-xs'>
              {t('Comment')}
            </label>
            <Input
              id={inputId}
              value={text}
              disabled={mutation.isPending}
              maxLength={500}
              required
              onChange={(event) => setText(event.target.value)}
              placeholder={t('Share your experience with this model')}
            />
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <label className='flex items-center gap-2 text-xs'>
                {t('Rating')}
                <NativeSelect
                  value={rating}
                  disabled={mutation.isPending}
                  onChange={(event) => setRating(Number(event.target.value))}
                >
                  {[5, 4, 3, 2, 1].map((value) => (
                    <NativeSelectOption key={value} value={value}>
                      {value}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </label>
              <Button
                type='submit'
                size='sm'
                disabled={mutation.isPending || !text.trim()}
              >
                {mutation.isPending ? t('Publishing...') : t('Publish comment')}
              </Button>
            </div>
          </form>
        </div>
      )}
    </div>
  )
}

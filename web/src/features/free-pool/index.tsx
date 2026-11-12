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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ExternalLink, Link2, MessageSquare, Plus, Star, Trash2 } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
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
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

import { deletePool, listPool, listReviews, ratePool, reviewPool, submitPool } from './api'
import type { FreePoolEntry, PoolSort } from './types'

const TAG_PRESETS = ['free', 'quota-limited', 'checkin'] as const

/** 解析 quota:xxx 标签，按 k/m 单位显示额度。 */
function parseQuota(tags: string[] | null): string | null {
  if (!tags) return null
  const hit = tags.find((tag) => tag.startsWith('quota:'))
  if (!hit) return null
  const raw = hit.slice('quota:'.length).trim()
  if (!raw) return null
  const match = /^([\d.]+)\s*([kKmM]?)$/.exec(raw)
  if (!match) return raw
  const value = Number(match[1])
  const unit = match[2].toLowerCase()
  if (unit === 'm') return `${value}M`
  if (unit === 'k') return `${value}K`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return String(value)
}

export function FreePoolPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [sort, setSort] = useState<PoolSort>('rating')
  const [submitOpen, setSubmitOpen] = useState(false)
  const [reviewTarget, setReviewTarget] = useState<FreePoolEntry | null>(null)

  const listQuery = useQuery({
    queryKey: ['free-pool', sort],
    queryFn: () => listPool(sort),
  })

  const rateMutation = useMutation({
    mutationFn: (payload: { id: number; rating: number; comment: string }) =>
      ratePool(payload.id, payload.rating, payload.comment),
    onSuccess: () => {
      toast.success(t('Rating submitted'))
      void queryClient.invalidateQueries({ queryKey: ['free-pool'] })
    },
    onError: (error) => handleServerError(error, t('Failed to submit rating')),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deletePool(id),
    onSuccess: () => {
      toast.success(t('Entry deleted'))
      void queryClient.invalidateQueries({ queryKey: ['free-pool'] })
    },
    onError: (error) => handleServerError(error, t('Failed to delete entry')),
  })

  const entries = listQuery.data ?? []

  let content: ReactNode
  if (listQuery.isLoading) {
    content = <LoadingState />
  } else if (listQuery.isError) {
    content = <ErrorState onRetry={() => void listQuery.refetch()} />
  } else if (entries.length === 0) {
    content = (
      <EmptyState
        title={t('No free sources yet')}
        description={t(
          'Submit a free model endpoint you found; everyone can use it after review-free publishing.'
        )}
      />
    )
  } else {
    content = (
      <div className='grid gap-4 md:grid-cols-2 xl:grid-cols-3'>
        {entries.map((entry) => (
          <PoolCard
            key={entry.id}
            entry={entry}
            onRate={(rating) =>
              rateMutation.mutate({ id: entry.id, rating, comment: '' })
            }
            onReview={() => setReviewTarget(entry)}
            onDelete={() => deleteMutation.mutate(entry.id)}
          />
        ))}
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Free Model Pool')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='flex items-center gap-2'>
          <Button
            variant={sort === 'rating' ? 'default' : 'outline'}
            size='sm'
            onClick={() => setSort('rating')}
          >
            <Star className='me-1 h-4 w-4' />
            {t('Top rated')}
          </Button>
          <Button
            variant={sort === 'newest' ? 'default' : 'outline'}
            size='sm'
            onClick={() => setSort('newest')}
          >
            {t('Newest')}
          </Button>
          <Button size='sm' onClick={() => setSubmitOpen(true)}>
            <Plus className='me-1 h-4 w-4' />
            {t('Submit free source')}
          </Button>
        </div>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>{content}</SectionPageLayout.Content>

      <SubmitDialog open={submitOpen} onOpenChange={setSubmitOpen} />
      <ReviewDialog target={reviewTarget} onClose={() => setReviewTarget(null)} />
    </SectionPageLayout>
  )
}

interface PoolCardProps {
  entry: FreePoolEntry
  onRate: (rating: number) => void
  onReview: () => void
  onDelete: () => void
}

function PoolCard(props: PoolCardProps) {
  const { t } = useTranslation()
  const entry = props.entry
  const quota = parseQuota(entry.tags)
  const tags = (entry.tags ?? []).filter((tag) => !tag.startsWith('quota:'))

  return (
    <Card>
      <CardHeader>
        <div className='flex items-start justify-between gap-2'>
          <div className='min-w-0'>
            <CardTitle className='truncate text-base'>
              {entry.recommended ? '★ ' : ''}
              {entry.name}
            </CardTitle>
            <CardDescription className='truncate'>
              {entry.submitter.name} · {new Date(entry.created_at).toLocaleDateString()}
            </CardDescription>
          </div>
          <div className='flex items-center gap-1'>
            <a
              href={entry.signup_url || entry.homepage_url || entry.url}
              target='_blank'
              rel='noreferrer noopener'
              aria-label={t('Sign up')}
              title={t('Sign up')}
              className={buttonVariants({ variant: 'ghost', size: 'icon' })}
            >
              <ExternalLink className='h-4 w-4' />
            </a>
            <Button variant='ghost' size='icon' onClick={props.onDelete}>
              <Trash2 className='h-4 w-4' />
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className='space-y-3'>
        {entry.description ? (
          <p className='text-muted-foreground line-clamp-3 text-sm'>
            {entry.description}
          </p>
        ) : null}

        <div className='flex flex-wrap gap-1'>
          {quota ? <Badge variant='secondary'>{t('Quota')}: {quota}</Badge> : null}
          {tags.map((tag) => (
            <Badge key={tag} variant='outline'>
              {t(tag)}
            </Badge>
          ))}
        </div>

        {entry.models?.length ? (
          <div className='flex flex-wrap gap-1'>
            {entry.models.map((model) => (
              <Badge key={model} variant='secondary' className='font-mono text-xs'>
                {model}
              </Badge>
            ))}
          </div>
        ) : null}

        <div className='flex items-center justify-between'>
          <div className='flex items-center gap-0.5'>
            {[1, 2, 3, 4, 5].map((star) => (
              <button
                key={star}
                type='button'
                aria-label={`${t('Rate')} ${star}`}
                onClick={() => props.onRate(star)}
                className='p-0.5'
              >
                <Star
                  className={
                    star <= Math.round(entry.rating)
                      ? 'h-4 w-4 fill-yellow-400 text-yellow-400'
                      : 'text-muted-foreground h-4 w-4'
                  }
                />
              </button>
            ))}
            <span className='text-muted-foreground ms-1 text-xs'>
              {entry.rating.toFixed(1)} ({entry.rating_count})
            </span>
          </div>
          <Button variant='ghost' size='sm' onClick={props.onReview}>
            <MessageSquare className='me-1 h-4 w-4' />
            {t('Comments')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

interface SubmitDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

function SubmitDialog(props: SubmitDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [signupUrl, setSignupUrl] = useState('')
  const [homepageUrl, setHomepageUrl] = useState('')
  const [models, setModels] = useState('')
  const [description, setDescription] = useState('')
  const [quota, setQuota] = useState('')
  const [tags, setTags] = useState<string[]>(['free'])

  const mutation = useMutation({
    mutationFn: () =>
      submitPool({
        name,
        url,
        signup_url: signupUrl.trim() || undefined,
        homepage_url: homepageUrl.trim() || undefined,
        models: models
          .split(',')
          .map((model) => model.trim())
          .filter(Boolean),
        description,
        tags: quota.trim() ? [...tags, `quota:${quota.trim()}`] : tags,
      }),
    onSuccess: () => {
      toast.success(t('Submitted'))
      props.onOpenChange(false)
      setName('')
      setUrl('')
      setSignupUrl('')
      setHomepageUrl('')
      setModels('')
      setDescription('')
      setQuota('')
      void queryClient.invalidateQueries({ queryKey: ['free-pool'] })
    },
    onError: (error) => handleServerError(error, t('Failed to submit')),
  })

  const toggleTag = (tag: string) => {
    setTags((prev) =>
      prev.includes(tag) ? prev.filter((item) => item !== tag) : [...prev, tag]
    )
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Submit free source')}</DialogTitle>
          <DialogDescription>
            {t('Share a free model endpoint. All users can see and rate it.')}
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-3'>
          <div className='space-y-1'>
            <Label htmlFor='pool-name'>{t('Name')}</Label>
            <Input
              id='pool-name'
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder='Groq free tier'
            />
          </div>
          <div className='space-y-1'>
            <Label htmlFor='pool-url'>{t('URL')}</Label>
            <Input
              id='pool-url'
              value={url}
              onChange={(event) => setUrl(event.target.value)}
              placeholder='https://console.groq.com/keys'
            />
          </div>
          <div className='space-y-1'>
            <Label htmlFor='pool-signup-url'>{t('Signup / API key page')}</Label>
            <Input
              id='pool-signup-url'
              value={signupUrl}
              onChange={(event) => setSignupUrl(event.target.value)}
              placeholder='https://example.com/signup'
            />
          </div>
          <div className='space-y-1'>
            <Label htmlFor='pool-homepage-url'>{t('Homepage')}</Label>
            <Input
              id='pool-homepage-url'
              value={homepageUrl}
              onChange={(event) => setHomepageUrl(event.target.value)}
              placeholder='https://example.com'
            />
          </div>
          <div className='space-y-1'>
            <Label htmlFor='pool-models'>{t('Models')}</Label>
            <Input
              id='pool-models'
              value={models}
              onChange={(event) => setModels(event.target.value)}
              placeholder='llama-3.3-70b, whisper-large-v3'
            />
          </div>
          <div className='space-y-1'>
            <Label htmlFor='pool-quota'>{t('Quota (k/m allowed)')}</Label>
            <Input
              id='pool-quota'
              value={quota}
              onChange={(event) => setQuota(event.target.value)}
              placeholder='50m'
            />
          </div>
          <div className='space-y-1'>
            <Label>{t('Tags')}</Label>
            <div className='flex flex-wrap gap-2'>
              {TAG_PRESETS.map((tag) => (
                <Button
                  key={tag}
                  type='button'
                  size='sm'
                  variant={tags.includes(tag) ? 'default' : 'outline'}
                  onClick={() => toggleTag(tag)}
                >
                  {t(tag)}
                </Button>
              ))}
            </div>
          </div>
          <div className='space-y-1'>
            <Label htmlFor='pool-desc'>{t('Description')}</Label>
            <Textarea
              id='pool-desc'
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              rows={3}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={!name.trim() || !url.trim() || mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {t('Submit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

interface ReviewDialogProps {
  target: FreePoolEntry | null
  onClose: () => void
}

function ReviewDialog(props: ReviewDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [comment, setComment] = useState('')
  const [rating, setRating] = useState(5)

  const reviewsQuery = useQuery({
    queryKey: ['free-pool', 'reviews', props.target?.id],
    queryFn: () => listReviews(props.target?.id ?? 0),
    enabled: Boolean(props.target),
  })

  const mutation = useMutation({
    mutationFn: async () => {
      if (!props.target) return
      if (rating > 0) {
        await ratePool(props.target.id, rating, comment)
      } else {
        await reviewPool(props.target.id, comment)
      }
    },
    onSuccess: () => {
      toast.success(t('Comment posted'))
      setComment('')
      void queryClient.invalidateQueries({ queryKey: ['free-pool'] })
    },
    onError: (error) => handleServerError(error, t('Failed to post comment')),
  })

  return (
    <Dialog open={Boolean(props.target)} onOpenChange={(open) => !open && props.onClose()}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{props.target?.name}</DialogTitle>
          <DialogDescription>{t('Comments and ratings')}</DialogDescription>
        </DialogHeader>
        <div className='max-h-64 space-y-2 overflow-auto'>
          {reviewsQuery.data?.length ? (
            reviewsQuery.data.map((review) => (
              <div key={review.id} className='rounded-md border p-2 text-sm'>
                <div className='text-muted-foreground mb-1 flex items-center gap-2 text-xs'>
                  <span>{review.user.name}</span>
                  {review.rating > 0 ? <span>· {review.rating}★</span> : null}
                  <span>· {new Date(review.created_at).toLocaleString()}</span>
                </div>
                <p>{review.comment}</p>
              </div>
            ))
          ) : (
            <p className='text-muted-foreground text-sm'>{t('No comments yet')}</p>
          )}
        </div>
        <div className='space-y-2'>
          <div className='flex items-center gap-1'>
            {[1, 2, 3, 4, 5].map((star) => (
              <button
                key={star}
                type='button'
                aria-label={`${t('Rate')} ${star}`}
                onClick={() => setRating(star)}
              >
                <Star
                  className={
                    star <= rating
                      ? 'h-5 w-5 fill-yellow-400 text-yellow-400'
                      : 'text-muted-foreground h-5 w-5'
                  }
                />
              </button>
            ))}
          </div>
          <Textarea
            value={comment}
            onChange={(event) => setComment(event.target.value)}
            rows={3}
            placeholder={t('Share your experience with this source')}
          />
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={props.onClose}>
            {t('Close')}
          </Button>
          <Button
            disabled={!comment.trim() || mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {t('Post')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export default FreePoolPage

// 保持 Link2 图标引用（渠道共享入口预留）
export const FreePoolLinkIcon = Link2

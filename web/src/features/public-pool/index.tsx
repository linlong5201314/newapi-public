import { useCallback, useEffect, useMemo, useState } from 'react'
import { Globe, MessageSquare, Timer, Users } from 'lucide-react'
import { toast } from 'sonner'

import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'

import {
  addPublicComment,
  listMyChannels,
  listPublicComments,
  listPublicModels,
  setVisibility,
} from './api'
import type { MyChannelRow, PublicComment, PublicModelItem } from './types'

function initials(name?: string) {
  const n = (name || '?').trim()
  return n.slice(0, 1).toUpperCase()
}

function OwnerBadge(props: { name: string; avatar: string }) {
  return (
    <span className='inline-flex items-center gap-1.5 rounded-full bg-muted px-2 py-0.5 text-xs'>
      <Avatar className='h-4 w-4'>
        {props.avatar ? <AvatarImage src={props.avatar} alt={props.name} /> : null}
        <AvatarFallback className='text-[10px]'>{initials(props.name)}</AvatarFallback>
      </Avatar>
      <span className='max-w-24 truncate'>{props.name || '匿名'}</span>
    </span>
  )
}

function CommentRow(props: { item: PublicComment }) {
  const c = props.item
  return (
    <div className='flex gap-2 border-t py-2 first:border-t-0'>
      <Avatar className='mt-0.5 h-6 w-6'>
        {c.avatar ? <AvatarImage src={c.avatar} alt={c.display_name || c.username} /> : null}
        <AvatarFallback className='text-[10px]'>{initials(c.display_name || c.username)}</AvatarFallback>
      </Avatar>
      <div className='min-w-0 flex-1'>
        <div className='flex items-center gap-2 text-xs text-muted-foreground'>
          <span className='font-medium text-foreground'>{c.display_name || c.username || '匿名用户'}</span>
          {c.rating > 0 ? <span>{'★'.repeat(Math.min(5, c.rating))}</span> : null}
          <span>{new Date(c.created_at * 1000).toLocaleString()}</span>
        </div>
        <div className='mt-0.5 text-sm break-words whitespace-pre-wrap'>{c.content}</div>
      </div>
    </div>
  )
}

function ModelCard(props: { item: PublicModelItem }) {
  const { item } = props
  const [open, setOpen] = useState(false)
  const [comments, setComments] = useState<PublicComment[] | null>(null)
  const [text, setText] = useState('')
  const [rating, setRating] = useState(5)
  const [busy, setBusy] = useState(false)

  const loadComments = useCallback(async () => {
    try {
      setComments(await listPublicComments(item.model))
    } catch {
      setComments([])
    }
  }, [item.model])

  useEffect(() => {
    if (open) void loadComments()
  }, [open, loadComments])

  const submit = async () => {
    const content = text.trim()
    if (!content) return
    setBusy(true)
    try {
      await addPublicComment({
        model: item.model,
        channel_id: item.channels[0]?.channel_id ?? 0,
        content,
        rating,
      })
      setText('')
      await loadComments()
      toast.success('评论已发布')
    } catch (e: any) {
      toast.error(e?.response?.data?.message || '评论失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader className='pb-3'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <CardTitle className='font-mono text-base'>{item.model}</CardTitle>
          <div className='flex flex-wrap items-center gap-1.5'>
            {item.free_count > 0 ? <Badge variant='secondary'>免费层 ×{item.free_count}</Badge> : null}
            <Badge variant='outline'>
              <Users className='mr-1 h-3 w-3' />
              {item.providers}
            </Badge>
            {item.fastest_ms > 0 ? (
              <Badge variant='outline'>
                <Timer className='mr-1 h-3 w-3' />
                {item.fastest_ms} ms
              </Badge>
            ) : null}
            {item.rating > 0 ? <Badge variant='outline'>★ {item.rating.toFixed(1)}</Badge> : null}
          </div>
        </div>
        <div className='flex flex-wrap items-center gap-1.5 pt-1'>
          {item.owners.slice(0, 6).map((o) => (
            <OwnerBadge key={o.user_id} name={o.name} avatar={o.avatar} />
          ))}
          {item.owners.length > 6 ? (
            <span className='text-xs text-muted-foreground'>+{item.owners.length - 6}</span>
          ) : null}
        </div>
      </CardHeader>
      <CardContent className='pt-0'>
        <div className='flex flex-wrap gap-2 text-xs text-muted-foreground'>
          {item.channels.slice(0, 4).map((c) => (
            <span key={c.channel_id} className='rounded border px-1.5 py-0.5'>
              {c.channel_name} · {c.tier === 'free' ? '免费' : '密钥'}
              {c.response_time > 0 ? ` · ${c.response_time}ms` : ''}
            </span>
          ))}
        </div>
        <Button variant='ghost' size='sm' className='mt-2 px-2' onClick={() => setOpen((v) => !v)}>
          <MessageSquare className='mr-1 h-4 w-4' />
          {open ? '收起评论' : `评论 (${item.comments})`}
        </Button>
        {open ? (
          <div className='mt-1 space-y-2'>
            {comments === null ? (
              <Skeleton className='h-10 w-full' />
            ) : comments.length === 0 ? (
              <div className='text-xs text-muted-foreground'>还没有评论，来说说这个模型跑得怎么样。</div>
            ) : (
              <div className='max-h-64 overflow-auto rounded border px-2'>
                {comments.map((c) => (
                  <CommentRow key={c.id} item={c} />
                ))}
              </div>
            )}
            <div className='flex items-center gap-2'>
              <select
                className='h-8 rounded border bg-background px-1 text-xs'
                value={rating}
                onChange={(e) => setRating(Number(e.target.value))}
              >
                {[5, 4, 3, 2, 1].map((v) => (
                  <option key={v} value={v}>
                    {v} 星
                  </option>
                ))}
              </select>
              <Input
                value={text}
                onChange={(e) => setText(e.target.value)}
                placeholder='说说速度、稳定性、有没有限流…'
                className='h-8'
                maxLength={500}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey) {
                    e.preventDefault()
                    void submit()
                  }
                }}
              />
              <Button size='sm' disabled={busy || !text.trim()} onClick={() => void submit()}>
                发布
              </Button>
            </div>
          </div>
        ) : null}
      </CardContent>
    </Card>
  )
}

export function PublicPoolPage() {
  const [models, setModels] = useState<PublicModelItem[] | null>(null)
  const [mine, setMine] = useState<MyChannelRow[] | null>(null)
  const [saving, setSaving] = useState<number | null>(null)

  const refresh = useCallback(async () => {
    try {
      const [m, own] = await Promise.all([listPublicModels(), listMyChannels()])
      setModels(m)
      setMine(own)
    } catch (e: any) {
      toast.error(e?.response?.data?.message || '加载公共池失败')
      setModels([])
      setMine([])
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const toggle = async (row: MyChannelRow, next: boolean) => {
    setSaving(row.id)
    try {
      await setVisibility({
        channelId: row.id,
        visibility: next ? 1 : 0,
        tier: row.tier || 'api',
      })
      toast.success(next ? '已公开到公共池' : '已收回，仅自己可见')
      await refresh()
    } catch (e: any) {
      toast.error(e?.response?.data?.message || '设置失败')
    } finally {
      setSaving(null)
    }
  }

  const changeTier = async (row: MyChannelRow, tier: string) => {
    setSaving(row.id)
    try {
      await setVisibility({
        channelId: row.id,
        visibility: row.visibility === 1 ? 1 : 0,
        tier,
      })
      await refresh()
    } catch (e: any) {
      toast.error(e?.response?.data?.message || '设置失败')
    } finally {
      setSaving(null)
    }
  }

  const totalModels = useMemo(() => models?.length ?? 0, [models])

  return (
    <div className='space-y-4 p-4'>
      <div className='flex items-center gap-2'>
        <Globe className='h-5 w-5' />
        <h1 className='text-xl font-semibold'>公共池</h1>
        <span className='text-sm text-muted-foreground'>公开出来的自建渠道，登录用户都能用</span>
      </div>

      <Card>
        <CardHeader className='pb-2'>
          <CardTitle className='text-base'>我的渠道公开设置</CardTitle>
          <p className='text-xs text-muted-foreground'>
            默认私有：只有你自己能路由。勾选公开后其他用户才能看见并使用；公开者昵称与头像会显示给所有人。
          </p>
        </CardHeader>
        <CardContent className='space-y-2'>
          {mine === null ? (
            <Skeleton className='h-12 w-full' />
          ) : mine.length === 0 ? (
            <div className='text-sm text-muted-foreground'>你还没有自建渠道。先在「我的渠道」里加一个。</div>
          ) : (
            mine.map((row) => (
              <div key={row.id} className='flex flex-wrap items-center gap-3 rounded border p-2'>
                <span className='font-mono text-sm'>{row.name}</span>
                <span className='max-w-72 truncate text-xs text-muted-foreground'>{row.models}</span>
                <label className='ml-auto flex cursor-pointer items-center gap-1.5 text-xs'>
                  <input
                    type='checkbox'
                    checked={row.visibility === 1}
                    disabled={saving === row.id}
                    onChange={(e) => void toggle(row, e.target.checked)}
                  />
                  公开到公共池
                </label>
                <select
                  className='h-7 rounded border bg-background px-1 text-xs'
                  value={row.tier || 'api'}
                  disabled={saving === row.id}
                  onChange={(e) => void changeTier(row, e.target.value)}
                >
                  <option value='api'>密钥层</option>
                  <option value='free'>免费层</option>
                </select>
              </div>
            ))
          )}
        </CardContent>
      </Card>

      <div className='flex items-center justify-between'>
        <h2 className='text-base font-semibold'>公开中的模型</h2>
        <span className='text-xs text-muted-foreground'>{totalModels} 个模型</span>
      </div>

      {models === null ? (
        <Skeleton className='h-32 w-full' />
      ) : models.length === 0 ? (
        <div className='rounded border p-6 text-center text-sm text-muted-foreground'>
          还没有人公开模型。上面勾一个，其他用户就能在这里看到。
        </div>
      ) : (
        <div className='grid gap-3 md:grid-cols-2'>
          {models.map((m) => (
            <ModelCard key={m.model} item={m} />
          ))}
        </div>
      )}
    </div>
  )
}

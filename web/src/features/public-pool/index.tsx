import { useCallback, useEffect, useMemo, useState } from 'react'
import { Building2, Globe, MessageSquare, Network, Sparkles, Timer, Users } from 'lucide-react'
import { toast } from 'sonner'

import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { importHaizhuNodes, listEndpoints } from '@/features/proxy-pool/api'
import { kindLabel, type ProxyEndpoint } from '@/features/proxy-pool/types'
import { useIsAdmin } from '@/hooks/use-admin'
import { api } from '@/lib/api'

import {
  addPublicComment,
  listMyChannels,
  listPlatformChannels,
  listPublicComments,
  listPublicModels,
  setPlatformVisibility,
  setVisibility,
  updateAvatar,
} from './api'
import type { MyChannelRow, PlatformChannelRow, PublicComment, PublicModelItem } from './types'

function initials(name?: string) {
  const n = (name || '?').trim()
  return n.slice(0, 1).toUpperCase()
}

/** 根据模型名自动识别所属厂家（Item 3：公共池里的模型按厂家分类） */
export function inferModelVendor(modelName: string): string {
  const m = (modelName || '').toLowerCase()
  if (m.includes('deepseek')) return 'DeepSeek'
  if (m.includes('claude') || m.includes('anthropic')) return 'Anthropic / Claude'
  if (m.includes('gpt') || m.includes('o1') || m.includes('o3') || m.includes('o4') || m.startsWith('openai'))
    return 'OpenAI'
  if (m.includes('gemini') || m.includes('gemma') || m.startsWith('google/')) return 'Google / Gemini'
  if (m.includes('qwen') || m.includes('qwq') || m.includes('tongyi')) return '阿里 / 通义千问 Qwen'
  if (m.includes('glm') || m.includes('chatglm') || m.includes('zai')) return '智谱 GLM'
  if (m.includes('llama') || m.startsWith('meta')) return 'Meta / Llama'
  if (m.includes('kimi') || m.includes('moonshot')) return '月之暗面 / Kimi'
  if (m.includes('minimax') || m.includes('abab')) return 'MiniMax'
  if (m.includes('mistral') || m.includes('codestral') || m.includes('devstral')) return 'Mistral'
  if (m.includes('nemotron') || m.startsWith('nvidia/')) return 'NVIDIA'
  if (m.includes('sense') || m.includes('raccoon') || m.startsWith('sn-')) return '商汤 / 小浣熊'
  if (m.includes('intern')) return '书生 / InternLM'
  if (m.includes('qoder')) return 'Qoder'
  if (m.includes('mimo')) return '小米 MiMo'
  if (m.includes('command') || m.startsWith('cohere/')) return 'Cohere'
  if (m.includes('hunyuan')) return '腾讯混元'
  return '其他厂家'
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
  const vendor = useMemo(() => inferModelVendor(item.model), [item.model])

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
          <div className='flex items-center gap-2 min-w-0'>
            <CardTitle className='truncate font-mono text-base'>{item.model}</CardTitle>
            <Badge variant='outline' className='shrink-0 text-[10px]'>
              {vendor}
            </Badge>
          </div>
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

function PlatformChannelsCard() {
  const [rows, setRows] = useState<PlatformChannelRow[] | null>(null)
  const [saving, setSaving] = useState<number | null>(null)
  const load = useCallback(async () => {
    try {
      setRows(await listPlatformChannels())
    } catch {
      setRows([])
    }
  }, [])
  useEffect(() => {
    void load()
  }, [load])
  const toggle = async (row: PlatformChannelRow, next: number) => {
    setSaving(row.id)
    try {
      await setPlatformVisibility({ channelId: row.id, visibility: next, tier: row.tier })
      toast.success(next === 1 ? '已对普通用户开放' : '已收回（仅自己可用）')
      await load()
    } catch {
      toast.error('切换失败，请稍后再试')
    } finally {
      setSaving(null)
    }
  }
  return (
    <Card>
      <CardHeader className='pb-3'>
        <CardTitle className='text-base'>平台渠道公开设置（管理员）</CardTitle>
        <p className='text-xs text-muted-foreground'>
          这里控制平台自带的免费渠道：取消勾选后，普通用户无法再通过该渠道调用模型（约 60 秒内全部节点生效）。
        </p>
      </CardHeader>
      <CardContent className='space-y-2'>
        {rows === null ? (
          <Skeleton className='h-16 w-full' />
        ) : rows.length === 0 ? (
          <div className='text-xs text-muted-foreground'>没有可管理的平台渠道。</div>
        ) : (
          rows.map((row) => (
            <div key={row.id} className='flex flex-wrap items-center gap-3 rounded border p-2 text-sm'>
              <span className='font-mono text-xs text-muted-foreground'>#{row.id}</span>
              <span className='font-medium'>{row.name}</span>
              <span className='max-w-72 truncate text-xs text-muted-foreground'>{row.models}</span>
              <span className='ml-auto inline-flex items-center gap-2 text-xs'>
                <label className='inline-flex items-center gap-1'>
                  <input
                    type='checkbox'
                    checked={row.visibility === 1}
                    disabled={saving === row.id}
                    onChange={(e) => void toggle(row, e.target.checked ? 1 : 0)}
                  />
                  对普通用户公开
                </label>
                <span className={row.visibility === 1 ? 'text-emerald-600' : 'text-muted-foreground'}>
                  {row.visibility === 1 ? '公开中' : '仅自己'}
                </span>
              </span>
            </div>
          ))
        )}
      </CardContent>
    </Card>
  )
}

export function PublicPoolPage() {
  const isAdmin = useIsAdmin()
  const [models, setModels] = useState<PublicModelItem[] | null>(null)
  const [mine, setMine] = useState<MyChannelRow[] | null>(null)
  const [publicEndpoints, setPublicEndpoints] = useState<ProxyEndpoint[]>([])
  const [saving, setSaving] = useState<number | null>(null)
  const [avatar, setAvatar] = useState('')
  const [ipDrafts, setIpDrafts] = useState<Record<number, string>>({})
  const [selectedVendor, setSelectedVendor] = useState<string>('ALL')
  const [searchModel, setSearchModel] = useState<string>('')
  const [activeTab, setActiveTab] = useState<'models' | 'ips'>('models')
  const [importingHaizhu, setImportingHaizhu] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const [m, own, eps] = await Promise.all([
        listPublicModels(),
        listMyChannels(),
        listEndpoints().catch(() => [] as ProxyEndpoint[]),
      ])
      setModels(m)
      setMine(own)
      setPublicEndpoints(eps.filter((e) => e.public || e.source === 'haizhu'))
    } catch (e: any) {
      toast.error(e?.response?.data?.message || '加载公共池失败')
      setModels([])
      setMine([])
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  useEffect(() => {
    api
      .get('/api/user/self')
      .then((res) => setAvatar((res.data?.data?.avatar as string) || ''))
      .catch(() => undefined)
  }, [])

  const saveAvatar = async () => {
    try {
      await updateAvatar(avatar.trim())
      toast.success('头像已保存')
      await refresh()
    } catch (e: any) {
      toast.error(e?.response?.data?.message || '保存头像失败')
    }
  }

  const saveIps = async (row: MyChannelRow) => {
    const value = (ipDrafts[row.id] ?? row.allow_ips ?? '').trim()
    setSaving(row.id)
    try {
      await setVisibility({
        channelId: row.id,
        visibility: row.visibility === 1 ? 1 : 0,
        tier: row.tier || 'api',
        allowIps: value,
      })
      toast.success('订阅 IP 白名单已保存')
      await refresh()
    } catch (e: any) {
      toast.error(e?.response?.data?.message || '保存失败')
    } finally {
      setSaving(null)
    }
  }

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

  const handleImportHaizhu = async () => {
    setImportingHaizhu(true)
    try {
      const res = await importHaizhuNodes('full')
      if (res.success === false) {
        toast.error(res.message || '导入失败')
      } else {
        toast.success(`已将 ${res.data?.imported ?? 0} 个 Haizhu 节点导入公共池 IP`)
        await refresh()
      }
    } catch {
      toast.error('导入 Haizhu 节点失败')
    } finally {
      setImportingHaizhu(false)
    }
  }

  // 按厂家统计与分组（Item 3：公共池里的模型要能按厂家分类）
  const vendorGroups = useMemo(() => {
    const map = new Map<string, PublicModelItem[]>()
    const kw = searchModel.trim().toLowerCase()
    for (const item of models ?? []) {
      if (kw && !item.model.toLowerCase().includes(kw)) continue
      const v = inferModelVendor(item.model)
      const list = map.get(v) ?? []
      list.push(item)
      map.set(v, list)
    }
    return Array.from(map.entries()).sort((a, b) => b[1].length - a[1].length)
  }, [models, searchModel])

  const filteredModels = useMemo(() => {
    const kw = searchModel.trim().toLowerCase()
    return (models ?? []).filter((item) => {
      if (kw && !item.model.toLowerCase().includes(kw)) return false
      if (selectedVendor !== 'ALL' && inferModelVendor(item.model) !== selectedVendor) return false
      return true
    })
  }, [models, selectedVendor, searchModel])

  const totalModels = useMemo(() => models?.length ?? 0, [models])

  return (
    <div className='min-h-0 flex-1 space-y-4 overflow-y-auto p-4'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex items-center gap-2'>
          <Globe className='h-5 w-5' />
          <h1 className='text-xl font-semibold'>公共池（模型按厂家分类 & 公共池共享 IP）</h1>
          <span className='text-sm text-muted-foreground'>
            公开出来的模型与代理 IP（含 Haizhu 7114 节点），全站用户可共享使用，优先低延迟节点并自动熔断切换
          </span>
        </div>
        <div className='flex rounded-lg border p-0.5 text-xs'>
          <button
            type='button'
            onClick={() => setActiveTab('models')}
            className={`rounded-md px-3 py-1.5 font-medium transition-colors ${
              activeTab === 'models' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'
            }`}
          >
            🤖 公共池模型 ({totalModels})
          </button>
          <button
            type='button'
            onClick={() => setActiveTab('ips')}
            className={`rounded-md px-3 py-1.5 font-medium transition-colors ${
              activeTab === 'ips' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'
            }`}
          >
            🌐 公共池共享 IP ({publicEndpoints.length})
          </button>
        </div>
      </div>

      {activeTab === 'ips' ? (
        <Card>
          <CardHeader className='pb-3'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <div>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <Network className='h-4 w-4' />
                  公共池免费代理 IP（含 Haizhu 7114 节点 + 管理员/用户公开节点）
                </CardTitle>
                <p className='mt-1 text-xs text-muted-foreground'>
                  这里只展示勾选了「公开」的节点与 Haizhu 内置节点；用户未勾选公开的私有节点/订阅不会出现在这里，别人也无法调用。
                </p>
              </div>
              <Button
                size='sm'
                disabled={importingHaizhu}
                onClick={() => void handleImportHaizhu()}
              >
                <Sparkles className='me-1 h-4 w-4' />
                {importingHaizhu ? '正在导入…' : '一键同步 Haizhu 7114 节点到公共池'}
              </Button>
            </div>
          </CardHeader>
          <CardContent className='space-y-3'>
            {publicEndpoints.length === 0 ? (
              <div className='rounded border p-6 text-center text-sm text-muted-foreground'>
                公共池还没有公开节点，点击右上角「一键同步 Haizhu 7114 节点到公共池」即可立即注入。
              </div>
            ) : (
              <div className='grid gap-2 sm:grid-cols-2 lg:grid-cols-3'>
                {publicEndpoints.slice(0, 90).map((ep) => (
                  <div
                    key={ep.id}
                    className='flex items-center justify-between gap-2 rounded-lg border px-3 py-2 text-xs'
                  >
                    <div className='min-w-0'>
                      <div className='truncate font-medium'>{ep.name}</div>
                      <div className='mt-0.5 flex items-center gap-1.5 text-[11px] text-muted-foreground'>
                        <Badge variant='secondary' className='text-[10px]'>
                          {kindLabel(ep.kind)}
                        </Badge>
                        <span>{ep.region || 'GLOBAL'}</span>
                        {ep.source === 'haizhu' && (
                          <Badge variant='outline' className='text-[10px]'>
                            Haizhu内置
                          </Badge>
                        )}
                      </div>
                    </div>
                    <div className='shrink-0 text-right font-mono'>
                      {ep.latency_ms && ep.latency_ms > 0 ? (
                        <span className='text-emerald-600'>{ep.latency_ms} ms</span>
                      ) : (
                        <span className='text-muted-foreground'>就绪</span>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            )}
            {publicEndpoints.length > 90 && (
              <div className='text-center text-xs text-muted-foreground'>
                共 {publicEndpoints.length} 个公共池代理 IP 已在共享 mihomo 内核就绪（前往「出口 IP 池」可查看全部或按地区检索）
              </div>
            )}
          </CardContent>
        </Card>
      ) : (
        <>
          <Card>
            <CardHeader className='pb-2'>
              <CardTitle className='text-base'>我的署名</CardTitle>
              <p className='text-xs text-muted-foreground'>
                公开模型和评论会显示这个名字与头像，填图片直链（http/https）。
              </p>
            </CardHeader>
            <CardContent className='flex flex-wrap items-center gap-2'>
              <Avatar className='h-9 w-9'>
                {avatar ? <AvatarImage src={avatar} alt='avatar' /> : null}
                <AvatarFallback className='text-xs'>ME</AvatarFallback>
              </Avatar>
              <Input
                className='max-w-md'
                value={avatar}
                onChange={(e) => setAvatar(e.target.value)}
                placeholder='https://example.com/avatar.png'
              />
              <Button size='sm' onClick={() => void saveAvatar()}>
                保存头像
              </Button>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className='pb-2'>
              <CardTitle className='text-base'>我的渠道公开设置</CardTitle>
              <p className='text-xs text-muted-foreground'>
                默认私有：只有你自己能路由。勾选公开后其他用户才能看见并使用；也可在「我的模型」页面精细勾选每个渠道只公开哪些模型。
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
                    {row.tier === 'free' && (
                      <span className='flex items-center gap-1'>
                        <Input
                          className='h-7 w-56 text-xs'
                          value={ipDrafts[row.id] ?? row.allow_ips ?? ''}
                          onChange={(e) =>
                            setIpDrafts((prev) => ({
                              ...prev,
                              [row.id]: e.target.value,
                            }))
                          }
                          placeholder='订阅 IP（逗号分隔，空=不限）'
                        />
                        <Button
                          size='sm'
                          variant='outline'
                          className='h-7 px-2 text-xs'
                          disabled={saving === row.id}
                          onClick={() => void saveIps(row)}
                        >
                          保存
                        </Button>
                      </span>
                    )}
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

          {isAdmin ? <PlatformChannelsCard /> : null}

          {/* 厂家分类筛选栏（Item 3：公共池里的模型要能按厂家分类） */}
          <Card>
            <CardHeader className='pb-2'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <CardTitle className='flex items-center gap-1.5 text-base'>
                  <Building2 className='h-4 w-4' />
                  按厂家分类浏览公共池模型
                </CardTitle>
                <Input
                  value={searchModel}
                  onChange={(e) => setSearchModel(e.target.value)}
                  placeholder='搜索模型名称…'
                  className='h-8 w-56 text-xs'
                />
              </div>
            </CardHeader>
            <CardContent className='flex flex-wrap gap-1.5 pt-1'>
              <button
                type='button'
                onClick={() => setSelectedVendor('ALL')}
                className={`rounded-full border px-3 py-1 text-xs transition-colors ${
                  selectedVendor === 'ALL'
                    ? 'bg-primary text-primary-foreground border-primary font-medium'
                    : 'bg-muted/50 text-muted-foreground hover:text-foreground'
                }`}
              >
                全部厂家 ({totalModels})
              </button>
              {vendorGroups.map(([vendorName, list]) => (
                <button
                  key={vendorName}
                  type='button'
                  onClick={() => setSelectedVendor(vendorName)}
                  className={`rounded-full border px-3 py-1 text-xs transition-colors ${
                    selectedVendor === vendorName
                      ? 'bg-primary text-primary-foreground border-primary font-medium'
                      : 'bg-muted/50 text-muted-foreground hover:text-foreground'
                  }`}
                >
                  {vendorName} ({list.length})
                </button>
              ))}
            </CardContent>
          </Card>

          {models === null ? (
            <Skeleton className='h-32 w-full' />
          ) : filteredModels.length === 0 ? (
            <div className='rounded border p-6 text-center text-sm text-muted-foreground'>
              没有匹配的公开模型。
            </div>
          ) : selectedVendor === 'ALL' ? (
            /* 按厂家分组展示 */
            <div className='space-y-6'>
              {vendorGroups.map(([vendorName, list]) => (
                <div key={vendorName} className='space-y-2.5'>
                  <div className='flex items-center justify-between border-b pb-1.5'>
                    <h2 className='flex items-center gap-2 text-base font-semibold'>
                      <Building2 className='h-4 w-4 text-primary' />
                      {vendorName}
                    </h2>
                    <Badge variant='secondary'>{list.length} 个模型</Badge>
                  </div>
                  <div className='grid gap-3 md:grid-cols-2'>
                    {list.map((m) => (
                      <ModelCard key={m.model} item={m} />
                    ))}
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div className='grid gap-3 md:grid-cols-2'>
              {filteredModels.map((m) => (
                <ModelCard key={m.model} item={m} />
              ))}
            </div>
          )}
        </>
      )}
    </div>
  )
}

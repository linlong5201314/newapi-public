import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import {
  Boxes,
  CheckSquare,
  ChevronDown,
  ChevronRight,
  Globe,
  Loader2,
  Plus,
  Radio,
  RefreshCw,
  Save,
  Search,
  Sparkles,
  Square,
  Trash2,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  deleteChannel,
  fetchModels,
  getChannelDetail,
  getChannels,
  updateChannel,
} from '@/features/my-channels/api'
import { channelsQueryKeys } from '@/features/my-channels/lib'
import type { Channel } from '@/features/my-channels/types'
import { inferModelVendor } from '@/features/public-pool'
import { api } from '@/lib/api'

/**
 * 「我的模型」页面（修复问题 4）：
 * 1. 按渠道分开展示用户自己的渠道列表，支持在渠道行直接「一键删除渠道」；
 * 2. 点击渠道才展开显示该渠道下的全部候选模型列表（all_models + 已选 models，即使只保留 1 个模型保存，下次打开也不会丢失其它模型）；
 * 3. 提供「一键全选（启用在我的渠道）」、「一键全不选（启用在我的渠道）」、「全选公开到公共池」、「全不选公开」按钮；
 * 4. 点击「从上游获取最新模型」可实时拉取 OpenRouter :free、OpenCode Free、Trae、WorkBuddy、小浣熊、Qoder CN 等最新模型。
 */
export function MyModelsPage() {
  const [expandedChannelId, setExpandedChannelId] = useState<number | null>(null)
  const [searchText, setSearchText] = useState('')

  const channelsQuery = useQuery({
    queryKey: [...channelsQueryKeys.lists(), 'my-models-all'],
    queryFn: () => getChannels({ page: 1, page_size: 100 }),
  })

  const channels: Channel[] = useMemo(
    () => channelsQuery.data?.data?.items ?? [],
    [channelsQuery.data]
  )

  const totalModelsCount = useMemo(() => {
    const set = new Set<string>()
    for (const ch of channels) {
      for (const m of (ch.models || '').split(',')) {
        const trimmed = m.trim()
        if (trimmed) set.add(trimmed)
      }
    }
    return set.size
  }, [channels])

  const filteredChannels = useMemo(() => {
    const kw = searchText.trim().toLowerCase()
    if (!kw) return channels
    return channels.filter(
      (ch) =>
        ch.name.toLowerCase().includes(kw) ||
        (ch.models || '').toLowerCase().includes(kw)
    )
  }, [channels, searchText])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <div className='flex items-center gap-2'>
          <Boxes className='h-5 w-5 text-primary' />
          <span>我的模型（按渠道下钻管理）</span>
        </div>
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='flex items-center gap-2'>
          <div className='relative'>
            <Search className='text-muted-foreground absolute top-2.5 left-2.5 h-3.5 w-3.5' />
            <Input
              value={searchText}
              onChange={(e) => setSearchText(e.target.value)}
              placeholder='搜索渠道或模型名称…'
              className='h-8 w-60 pl-8 text-xs'
            />
          </div>
          <Link to='/my-channels'>
            <Button size='sm' variant='outline'>
              <Radio className='me-1 h-3.5 w-3.5' />
              管理我的渠道
            </Button>
          </Link>
        </div>
      </SectionPageLayout.Actions>

      <SectionPageLayout.Content>
        <div className='space-y-4 pb-8'>
          <div className='grid gap-3 sm:grid-cols-3'>
            <Card className='py-3'>
              <CardContent className='space-y-0.5'>
                <div className='text-muted-foreground text-xs'>我的渠道数</div>
                <div className='font-mono text-2xl font-semibold'>{channels.length}</div>
                <div className='text-muted-foreground text-xs'>点击下方任意渠道展开并编辑其模型，或一键删除渠道</div>
              </CardContent>
            </Card>
            <Card className='py-3'>
              <CardContent className='space-y-0.5'>
                <div className='text-muted-foreground text-xs'>当前启用模型总数（去重）</div>
                <div className='font-mono text-2xl font-semibold'>{totalModelsCount}</div>
                <div className='text-muted-foreground text-xs'>未勾选的模型也会保留在渠道候选池中，随时可重新启用</div>
              </CardContent>
            </Card>
            <Card className='py-3'>
              <CardContent className='space-y-0.5'>
                <div className='text-muted-foreground text-xs'>已公开到公共池的渠道</div>
                <div className='font-mono text-2xl font-semibold'>
                  {channels.filter((c) => (c as any).visibility === 1).length}
                </div>
                <div className='text-muted-foreground text-xs'>支持一键全选/全不选启用在我的渠道或公开到公共池</div>
              </CardContent>
            </Card>
          </div>

          {channelsQuery.isLoading ? (
            <div className='text-muted-foreground flex items-center gap-2 py-12 text-sm'>
              <Loader2 className='h-4 w-4 animate-spin' />
              正在加载你的渠道与模型…
            </div>
          ) : filteredChannels.length === 0 ? (
            <Card>
              <CardContent className='py-10 text-center text-sm text-muted-foreground'>
                你还没有匹配的渠道。先去「我的渠道」添加一个免费源或授权账号。
              </CardContent>
            </Card>
          ) : (
            <div className='space-y-3'>
              {filteredChannels.map((channel) => {
                const isOpen = expandedChannelId === channel.id
                return (
                  <ChannelModelDrilldownCard
                    key={channel.id}
                    channel={channel}
                    expanded={isOpen}
                    onToggleExpand={() =>
                      setExpandedChannelId(isOpen ? null : channel.id)
                    }
                  />
                )
              })}
            </div>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function ChannelModelDrilldownCard({
  channel,
  expanded,
  onToggleExpand,
}: {
  channel: Channel
  expanded: boolean
  onToggleExpand: () => void
}) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const currentModels = useMemo(
    () =>
      (channel.models || '')
        .split(',')
        .map((m) => m.trim())
        .filter(Boolean),
    [channel.models]
  )

  const detailQuery = useQuery({
    queryKey: ['self-channel-detail', channel.id],
    queryFn: () => getChannelDetail(channel.id),
    enabled: expanded,
  })

  const [candidatePool, setCandidatePool] = useState<string[]>(currentModels)
  const [selectedMyModels, setSelectedMyModels] = useState<string[]>(currentModels)
  const [selectedPublicModels, setSelectedPublicModels] = useState<string[]>([])
  const [newModelInput, setNewModelInput] = useState('')
  const [modelFilter, setModelFilter] = useState('')

  useEffect(() => {
    setCandidatePool((prev) => Array.from(new Set([...prev, ...currentModels])))
    setSelectedMyModels(currentModels)
  }, [currentModels])

  useEffect(() => {
    const detail = detailQuery.data?.data
    if (!detail) return
    const allFromBackend = Array.from(
      new Set([
        ...(detail.all_models ?? []),
        ...(detail.models ?? []),
        ...currentModels,
      ])
    )
    setCandidatePool((prev) => Array.from(new Set([...allFromBackend, ...prev])))
    setSelectedMyModels(detail.models ?? [])
    setSelectedPublicModels(detail.public_models ?? [])
  }, [detailQuery.data, currentModels])

  const deleteChannelMutation = useMutation({
    mutationFn: () => deleteChannel(channel.id),
    onSuccess: () => {
      toast.success(`已删除渠道「${channel.name}」`)
      void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
    },
    onError: () => toast.error('删除渠道失败'),
  })

  const fetchUpstreamMutation = useMutation({
    mutationFn: () => fetchModels(channel.id),
    onSuccess: (res) => {
      if (res.success && Array.isArray(res.data) && res.data.length > 0) {
        const fetched = res.data.map((m) => m.trim()).filter(Boolean)
        setCandidatePool((prev) => Array.from(new Set([...prev, ...fetched])))
        toast.success(`成功获取到 ${fetched.length} 个可用模型！可点击「一键全选」或按需勾选后保存`)
      } else {
        toast.error(res.message || '未获取到额外模型')
      }
    },
    onError: () => toast.error('从上游获取模型失败'),
  })

  const saveMutation = useMutation({
    mutationFn: async () => {
      await updateChannel(channel.id, { models: selectedMyModels.join(',') })
      const isIpFree = Boolean(detailQuery.data?.data?.template?.ip_free)
      await api.post('/api/channel/self/visibility', {
        channelId: channel.id,
        visibility: selectedPublicModels.length > 0 ? 1 : 0,
        tier: isIpFree ? 'free' : 'api',
        publicModels: selectedPublicModels,
        allModels: candidatePool,
      })
    },
    onSuccess: () => {
      toast.success(
        `已保存渠道「${channel.name}」：启用 ${selectedMyModels.length} 个模型（其余 ${
          candidatePool.length - selectedMyModels.length
        } 个仍保留在列表中可随时勾选）`
      )
      void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
      void detailQuery.refetch()
    },
    onError: () => toast.error('保存模型设置失败'),
  })

  const handleAddCustomModel = () => {
    const m = newModelInput.trim()
    if (!m) return
    setCandidatePool((prev) => (prev.includes(m) ? prev : [m, ...prev]))
    setSelectedMyModels((prev) => (prev.includes(m) ? prev : [m, ...prev]))
    setNewModelInput('')
  }

  const visibleCandidates = useMemo(() => {
    const kw = modelFilter.trim().toLowerCase()
    if (!kw) return candidatePool
    return candidatePool.filter((m) => m.toLowerCase().includes(kw))
  }, [candidatePool, modelFilter])

  const detail = detailQuery.data?.data

  return (
    <Card className='overflow-hidden'>
      <CardHeader className='p-0'>
        <div className='hover:bg-muted/40 flex w-full items-center justify-between gap-3 px-4 py-3.5 transition-colors'>
          <button
            type='button'
            onClick={onToggleExpand}
            className='flex min-w-0 flex-1 items-center gap-2.5 text-left'
          >
            {expanded ? (
              <ChevronDown className='h-4 w-4 shrink-0 text-primary' />
            ) : (
              <ChevronRight className='h-4 w-4 shrink-0 text-muted-foreground' />
            )}
            <CardTitle className='truncate text-base font-medium'>
              {channel.name}
            </CardTitle>
            <Badge variant='outline' className='font-mono text-xs'>
              #{channel.id}
            </Badge>
            <Badge variant={channel.status === 1 ? 'default' : 'secondary'} className='text-xs'>
              {channel.status === 1 ? '已启用' : '已禁用'}
            </Badge>
            {(channel as any).visibility === 1 && (
              <Badge variant='secondary' className='text-emerald-600 text-xs'>
                公开中
              </Badge>
            )}
          </button>

          <div className='flex shrink-0 items-center gap-2 text-xs'>
            <span className='text-muted-foreground'>
              已启用 {currentModels.length} 个模型
            </span>
            <Button
              size='sm'
              variant='ghost'
              className='h-7 px-2 text-xs text-primary'
              onClick={onToggleExpand}
            >
              {expanded ? '收起模型' : '点击展开模型'}
            </Button>
            <Button
              size='sm'
              variant='destructive'
              className='h-7 px-2.5 text-xs'
              disabled={deleteChannelMutation.isPending}
              onClick={() => deleteChannelMutation.mutate()}
              title='一键删除该渠道'
            >
              {deleteChannelMutation.isPending ? (
                <Loader2 className='me-1 h-3.5 w-3.5 animate-spin' />
              ) : (
                <Trash2 className='me-1 h-3.5 w-3.5' />
              )}
              删除渠道
            </Button>
          </div>
        </div>
      </CardHeader>

      {expanded && (
        <CardContent className='border-t bg-muted/15 p-4 space-y-3'>
          {/* 渠道状态与全选/全不选操作条 */}
          <div className='flex flex-wrap items-center justify-between gap-2 text-xs'>
            <div className='flex flex-wrap items-center gap-2 text-muted-foreground'>
              {detail?.proxy?.label && (
                <Badge variant='outline'>出口: {detail.proxy.label}</Badge>
              )}
              {detail?.health && (
                <Badge variant='outline'>
                  成功率: {detail.health.samples > 0 ? `${detail.health.success_rate.toFixed(0)}%` : '—'}
                </Badge>
              )}
              <span>
                启用在我的渠道: <strong className='text-foreground'>{selectedMyModels.length}</strong> / {candidatePool.length} 个 ·
                公开到公共池: <strong className='text-emerald-600'>{selectedPublicModels.length}</strong> 个
              </span>
            </div>

            <div className='flex flex-wrap items-center gap-1.5'>
              <Button
                size='sm'
                variant='outline'
                className='h-8 text-xs'
                disabled={fetchUpstreamMutation.isPending}
                onClick={() => fetchUpstreamMutation.mutate()}
              >
                {fetchUpstreamMutation.isPending ? (
                  <Loader2 className='me-1 h-3.5 w-3.5 animate-spin' />
                ) : (
                  <RefreshCw className='me-1 h-3.5 w-3.5' />
                )}
                获取上游最新模型
              </Button>
              <Button
                size='sm'
                variant='secondary'
                className='h-8 text-xs'
                onClick={() => setSelectedMyModels([...candidatePool])}
              >
                <CheckSquare className='me-1 h-3.5 w-3.5' />
                一键全选（启用在我的渠道）
              </Button>
              <Button
                size='sm'
                variant='outline'
                className='h-8 text-xs'
                onClick={() => {
                  setSelectedMyModels([])
                  setSelectedPublicModels([])
                }}
              >
                <Square className='me-1 h-3.5 w-3.5' />
                一键全不选（我的渠道）
              </Button>
              <Button
                size='sm'
                variant='outline'
                className='h-8 text-xs text-emerald-600'
                onClick={() => {
                  setSelectedMyModels([...candidatePool])
                  setSelectedPublicModels([...candidatePool])
                }}
              >
                <Globe className='me-1 h-3.5 w-3.5' />
                全选公开到公共池
              </Button>
              <Button
                size='sm'
                variant='outline'
                className='h-8 text-xs'
                onClick={() => setSelectedPublicModels([])}
              >
                全不选公开
              </Button>
              <Button
                size='sm'
                className='h-8 text-xs'
                disabled={saveMutation.isPending}
                onClick={() => saveMutation.mutate()}
              >
                {saveMutation.isPending ? (
                  <Loader2 className='me-1 h-3.5 w-3.5 animate-spin' />
                ) : (
                  <Save className='me-1 h-3.5 w-3.5' />
                )}
                保存该渠道模型
              </Button>
            </div>
          </div>

          {/* 筛选与手动新增模型 */}
          <div className='flex flex-wrap gap-2'>
            <Input
              value={modelFilter}
              onChange={(e) => setModelFilter(e.target.value)}
              placeholder='过滤本渠道内的模型…'
              className='h-8 w-56 text-xs'
            />
            <div className='flex flex-1 items-center gap-2 min-w-[240px]'>
              <Input
                value={newModelInput}
                onChange={(e) => setNewModelInput(e.target.value)}
                placeholder='手动添加模型 ID（如 deepseek-chat）'
                className='h-8 flex-1 font-mono text-xs'
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    handleAddCustomModel()
                  }
                }}
              />
              <Button
                size='sm'
                variant='secondary'
                className='h-8 text-xs'
                onClick={handleAddCustomModel}
              >
                <Plus className='me-1 h-3.5 w-3.5' />
                添加模型
              </Button>
            </div>
          </div>

          {/* 模型列表 */}
          <div className='max-h-80 overflow-y-auto rounded-lg border bg-background p-2 space-y-1'>
            {visibleCandidates.length === 0 ? (
              <div className='py-6 text-center text-xs text-muted-foreground'>
                暂无模型，请点击右上方「获取上游最新模型」或在输入框手动添加。
              </div>
            ) : (
              visibleCandidates.map((modelName) => {
                const inMine = selectedMyModels.includes(modelName)
                const inPub = selectedPublicModels.includes(modelName)
                const vendor = inferModelVendor(modelName)
                return (
                  <div
                    key={modelName}
                    className='flex flex-wrap items-center justify-between gap-2 rounded-md px-2.5 py-1.5 hover:bg-muted/60 text-xs'
                  >
                    <div className='flex min-w-0 items-center gap-2'>
                      <span className='truncate font-mono font-medium'>{modelName}</span>
                      <Badge variant='outline' className='text-[10px]'>
                        {vendor}
                      </Badge>
                    </div>
                    <div className='flex shrink-0 items-center gap-4'>
                      <label className='inline-flex cursor-pointer items-center gap-1'>
                        <input
                          type='checkbox'
                          checked={inMine}
                          onChange={(e) => {
                            const checked = e.target.checked
                            setSelectedMyModels((prev) =>
                              checked
                                ? [...prev, modelName]
                                : prev.filter((m) => m !== modelName)
                            )
                            if (!checked) {
                              setSelectedPublicModels((prev) =>
                                prev.filter((m) => m !== modelName)
                              )
                            }
                          }}
                          className='size-3.5 accent-primary'
                        />
                        <span>启用在我的渠道</span>
                      </label>
                      <label className='inline-flex cursor-pointer items-center gap-1 text-emerald-600'>
                        <input
                          type='checkbox'
                          checked={inPub}
                          onChange={(e) => {
                            const checked = e.target.checked
                            if (checked && !inMine) {
                              setSelectedMyModels((prev) => [...prev, modelName])
                            }
                            setSelectedPublicModels((prev) =>
                              checked
                                ? [...prev, modelName]
                                : prev.filter((m) => m !== modelName)
                            )
                          }}
                          className='size-3.5 accent-emerald-600'
                        />
                        <Globe className='size-3' />
                        <span>公开到公共池</span>
                      </label>
                      <Button
                        size='sm'
                        variant='ghost'
                        className='h-6 px-2 text-xs text-primary'
                        onClick={() => {
                          try {
                            window.localStorage.setItem('ds-demo:preferred-model', modelName)
                          } catch {
                            // ignore
                          }
                          void navigate({ to: '/demo' })
                        }}
                      >
                        <Sparkles className='me-1 h-3 w-3' />
                        去对话
                      </Button>
                    </div>
                  </div>
                )
              })
            )}
          </div>
        </CardContent>
      )}
    </Card>
  )
}

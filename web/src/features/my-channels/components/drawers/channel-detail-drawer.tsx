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
import { Link } from '@tanstack/react-router'
import {
  Activity,
  BadgeCheck,
  Gauge,
  Globe,
  Globe2,
  Loader2,
  Network,
  Pencil,
  RefreshCw,
  Save,
  ShieldQuestion,
  Timer,
  Trash2,
  Waypoints,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { selectRuntimeNode } from '@/features/proxy-pool/api'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'

import {
  deleteChannel,
  fetchModels,
  getChannelDetail,
  setChannelAllowIps,
  updateChannel,
} from '../../api'
import { channelsQueryKeys } from '../../lib'
import type { Channel } from '../../types'

interface ChannelDetailDrawerProps {
  channel: Channel | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * 「我的渠道」二级详情抽屉：支持全屏顺畅上下滑动，实时查看额度、选择代理 IP / 订阅节点、
 * 获取上游最新免费模型、一键全选/全不选模型、一键删除渠道。
 */
export function ChannelDetailDrawer({
  channel,
  open,
  onOpenChange,
}: ChannelDetailDrawerProps) {
  const queryClient = useQueryClient()
  const channelId = channel?.id ?? 0

  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['self-channel-detail', channelId],
    queryFn: () => getChannelDetail(channelId),
    enabled: open && channelId > 0,
  })

  const detail = data?.data

  const [name, setName] = useState('')
  const [allowIps, setAllowIps] = useState('')
  const [selectedKernelNode, setSelectedKernelNode] = useState('PUBLIC_FAST')

  useEffect(() => {
    if (!detail) return
    setName(detail.channel.name)
    setAllowIps(detail.allow_ips ?? '')
  }, [detail])

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })

  const renameMutation = useMutation({
    mutationFn: (next: string) => updateChannel(channelId, { name: next }),
    onSuccess: () => {
      toast.success('渠道名称已更新')
      void invalidate()
      void refetch()
    },
    onError: () => toast.error('改名失败，请重试'),
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteChannel(channelId),
    onSuccess: () => {
      toast.success('渠道已删除')
      void invalidate()
      onOpenChange(false)
    },
    onError: () => toast.error('删除渠道失败'),
  })

  const allowIpsMutation = useMutation({
    mutationFn: (next: string) => setChannelAllowIps(channelId, next),
    onSuccess: () => {
      toast.success('IP 白名单已保存')
      void refetch()
    },
    onError: () => toast.error('保存白名单失败'),
  })

  const switchNodeMutation = useMutation({
    mutationFn: (nodeName: string) => selectRuntimeNode(nodeName),
    onSuccess: (res) => {
      if (res.success === false) {
        toast.error(res.message || '切换代理节点失败')
        return
      }
      toast.success(`已切换出口代理节点：${res.data?.selected ?? selectedKernelNode}`)
      void refetch()
    },
    onError: () => toast.error('切换代理节点失败'),
  })

  const health = detail?.health
  const healthTone = useMemo(() => {
    switch (health?.state) {
      case 'healthy':
        return { label: '可用', className: 'bg-success/15 text-success' }
      case 'degraded':
        return { label: '降权', className: 'bg-warning/20 text-warning' }
      case 'down':
        return { label: '连续失败', className: 'bg-destructive/15 text-destructive' }
      default:
        return { label: '样本不足', className: 'bg-muted text-muted-foreground' }
    }
  }, [health?.state])

  const template = detail?.template
  const isIpFree = Boolean(template?.ip_free)
  const estimate = detail?.token_estimate

  const [candidateModels, setCandidateModels] = useState<string[]>([])
  const [selectedModels, setSelectedModels] = useState<string[]>([])
  const [publicModels, setPublicModels] = useState<string[]>([])

  useEffect(() => {
    if (!detail) return
    const all = Array.from(
      new Set([...(detail.all_models ?? []), ...(detail.models ?? [])])
    )
    setCandidateModels(all)
    setSelectedModels(detail.models ?? [])
    setPublicModels(detail.public_models ?? [])
  }, [detail])

  const fetchUpstreamMutation = useMutation({
    mutationFn: () => fetchModels(channelId),
    onSuccess: (res) => {
      if (res.success && Array.isArray(res.data) && res.data.length > 0) {
        const fetched = res.data.map((m) => m.trim()).filter(Boolean)
        setCandidateModels((prev) => Array.from(new Set([...prev, ...fetched])))
        toast.success(`已从上游获取到 ${fetched.length} 个可用模型`)
      } else {
        toast.error(res.message || '未获取到额外模型')
      }
    },
    onError: () => toast.error('获取上游模型失败'),
  })

  const modelsMutation = useMutation({
    mutationFn: async (payload: { myModels: string[]; pubModels: string[]; allModels: string[] }) => {
      await updateChannel(channelId, { models: payload.myModels.join(',') })
      await api.post('/api/channel/self/visibility', {
        channelId,
        visibility: payload.pubModels.length > 0 ? 1 : detail?.channel.visibility ?? 0,
        tier: isIpFree ? 'free' : 'api',
        publicModels: payload.pubModels,
        allModels: payload.allModels,
      })
    },
    onSuccess: () => {
      toast.success('渠道模型与公共池公开配置已保存')
      void invalidate()
      void refetch()
    },
    onError: () => toast.error('更新模型失败'),
  })

  const ownCount = detail?.proxy_pool.own_endpoint_count ?? 0
  const ownTokensTotal = detail?.proxy_pool.own_ip_tokens_total ?? 0
  const selectableNodes = detail?.proxy_pool.selectable_nodes ?? []

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className='flex h-dvh max-h-dvh w-full flex-col overflow-hidden p-0 sm:max-w-xl'
      >
        <SheetHeader className='shrink-0 border-b px-5 py-4'>
          <div className='flex items-center justify-between gap-2 pr-6'>
            <SheetTitle className='flex flex-wrap items-center gap-2'>
              {detail?.channel.name ?? channel?.name ?? '渠道详情'}
              {template?.name ? (
                <Badge variant='secondary' className='text-xs'>
                  {template.name}
                </Badge>
              ) : null}
              {isIpFree ? (
                <Badge className='bg-[#4d6bfe]/15 text-[#4d6bfe] text-xs'>
                  IP 免费层
                </Badge>
              ) : (
                <Badge variant='outline' className='text-xs'>
                  密钥层
                </Badge>
              )}
            </SheetTitle>
            <Button
              size='sm'
              variant='destructive'
              className='h-7 px-2 text-xs'
              disabled={deleteMutation.isPending}
              onClick={() => deleteMutation.mutate()}
            >
              <Trash2 className='me-1 size-3.5' />
              删除渠道
            </Button>
          </div>
          <SheetDescription>
            渠道 #{channelId} 的实时状态、额度、出口代理 IP 选择、订阅 IP 数量与模型管理
          </SheetDescription>
        </SheetHeader>

        <div className='min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 py-4'>
          {isLoading && (
            <div className='flex items-center gap-2 py-10 text-sm text-muted-foreground'>
              <Loader2 className='size-4 animate-spin' /> 正在加载渠道详情…
            </div>
          )}

          {isError && (
            <div className='rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive'>
              {(error as Error)?.message || '加载失败，请重试'}
            </div>
          )}

          {detail && (
            <div className='space-y-4 pb-10'>
              {/* 名称编辑 */}
              <section className='rounded-xl border border-border/70 p-3'>
                <SectionTitle icon={<Pencil className='size-3.5' />} title='名称编辑' />
                <div className='mt-2 flex gap-2'>
                  <input
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    className='h-9 w-full rounded-lg border border-border/70 bg-background px-3 text-sm outline-none focus:border-[#4d6bfe]/60'
                  />
                  <Button
                    size='sm'
                    disabled={
                      renameMutation.isPending ||
                      !name.trim() ||
                      name.trim() === detail.channel.name
                    }
                    onClick={() => renameMutation.mutate(name.trim())}
                  >
                    {renameMutation.isPending ? (
                      <Loader2 className='size-3.5 animate-spin' />
                    ) : (
                      <Save className='size-3.5' />
                    )}
                    保存
                  </Button>
                </div>
              </section>

              {/* 健康度 */}
              <section className='rounded-xl border border-border/70 p-3'>
                <SectionTitle icon={<Activity className='size-3.5' />} title='实时健康度 & 熔断状态' />
                <div className='mt-2 flex flex-wrap items-center gap-2'>
                  <span
                    className={cn(
                      'rounded-full px-2 py-0.5 text-xs font-medium',
                      healthTone.className
                    )}
                  >
                    {healthTone.label}
                  </span>
                  <Metric
                    icon={<Gauge className='size-3.5' />}
                    label='成功率'
                    value={
                      health && health.samples > 0
                        ? `${health.success_rate.toFixed(0)}%`
                        : '—'
                    }
                  />
                  <Metric
                    icon={<Timer className='size-3.5' />}
                    label='平均延迟'
                    value={
                      health && health.avg_latency_ms > 0
                        ? `${health.avg_latency_ms} ms`
                        : '—'
                    }
                  />
                  <Metric
                    icon={<Waypoints className='size-3.5' />}
                    label='样本'
                    value={`${health?.samples ?? 0} 次`}
                  />
                </div>
                <p className='mt-2 text-[11px] text-muted-foreground'>
                  最近 5 次调用样本，跨节点共享；权重系数 {health?.factor ?? 1}（遇错自动熔断切换下一个出口 IP）
                </p>
              </section>

              {/* 额度 / 大概能用的 token */}
              <section className='rounded-xl border border-border/70 p-3'>
                <SectionTitle
                  icon={<BadgeCheck className='size-3.5' />}
                  title='实时额度 / 大概能使用的 Token'
                />
                {estimate?.hidden ? (
                  <div className='mt-2 space-y-1.5 rounded-lg bg-muted/60 px-3 py-2 text-xs text-muted-foreground'>
                    <p>{estimate.reason}</p>
                    {ownCount > 0 && ownTokensTotal > 0 ? (
                      <p className='font-medium text-emerald-600'>
                        你已绑定 {ownCount} 个自有订阅/代理 IP，按每 IP 额度粗估共可使用约{' '}
                        <span className='font-mono'>{formatTokens(ownTokensTotal)}</span> tokens（免费公共池 IP 不显示剩余额度）。
                      </p>
                    ) : (
                      <p className='text-[11px]'>
                        当前使用免费公共 IP 池（不显示剩余 token 数量）；如添加自有机场订阅或私有 IP，将在此显示每个 IP 的预估可用额度总和。
                      </p>
                    )}
                  </div>
                ) : (
                  <div className='mt-2 space-y-1.5'>
                    <div className='flex items-center justify-between text-xs'>
                      <span className='text-muted-foreground'>本月已用 Token</span>
                      <span className='font-mono'>
                        {formatTokens(estimate?.used ?? 0)}
                        {estimate?.monthly
                          ? ` / ${formatTokens(estimate.monthly)}`
                          : ''}
                      </span>
                    </div>
                    <div className='h-1.5 w-full overflow-hidden rounded-full bg-muted'>
                      <div
                        className='h-full rounded-full bg-[#4d6bfe]'
                        style={{
                          width: `${estimate?.monthly ? Math.min(100, ((estimate.used ?? 0) / estimate.monthly) * 100) : 2}%`,
                        }}
                      />
                    </div>
                    <p className='text-xs text-muted-foreground'>
                      {estimate?.remaining !== null &&
                      estimate?.remaining !== undefined
                        ? `预计还能用 ${formatTokens(estimate.remaining)} token`
                        : estimate?.reason}
                    </p>
                    {ownCount > 0 && ownTokensTotal > 0 && (
                      <p className='text-xs font-medium text-emerald-600'>
                        自有订阅/IP ({ownCount} 个) 叠加可用总额度 ≈ {formatTokens(ownTokensTotal)} tokens
                      </p>
                    )}
                  </div>
                )}
              </section>

              {/* 出口代理 & 代理 IP 选择 */}
              <section className='rounded-xl border border-border/70 p-3'>
                <SectionTitle icon={<Network className='size-3.5' />} title='代理与代理 IP 选择 / 订阅 IP 统计' />
                <div className='mt-2 flex items-center justify-between gap-2 rounded-lg bg-muted/50 px-3 py-2'>
                  <div className='min-w-0'>
                    <p className='truncate text-sm'>{detail.proxy.label}</p>
                    {detail.proxy.value && detail.proxy.mode === 'fixed' ? (
                      <p className='truncate font-mono text-[11px] text-muted-foreground'>
                        {detail.proxy.value}
                      </p>
                    ) : null}
                  </div>
                  <Link
                    to='/proxy-pool'
                    className='shrink-0 text-xs text-[#4d6bfe] hover:underline'
                  >
                    管理出口池 / 订阅
                  </Link>
                </div>

                <div className='mt-3 grid grid-cols-2 gap-2 sm:grid-cols-4'>
                  <Stat label='可见总节点' value={detail.proxy_pool.endpoint_total} />
                  <Stat label='自有订阅节点' value={detail.proxy_pool.subscription_nodes} />
                  <Stat label='订阅数' value={detail.proxy_pool.subscription_total} />
                  <Stat label='公共池共享IP' value={detail.proxy_pool.public_pool_count ?? detail.proxy_pool.endpoint_enabled} />
                </div>

                {selectableNodes.length > 0 && (
                  <div className='mt-3 space-y-1.5'>
                    <p className='text-[11px] font-medium text-muted-foreground'>
                      切换当前出口代理 IP / 机场订阅节点（优先展示低延迟节点）：
                    </p>
                    <div className='flex gap-2'>
                      <select
                        value={selectedKernelNode}
                        onChange={(e) => setSelectedKernelNode(e.target.value)}
                        className='h-8 flex-1 rounded-lg border border-border/70 bg-background px-2 font-mono text-xs'
                      >
                        <option value='PUBLIC_FAST'>⚡ PUBLIC_FAST（自动优选最快节点 + 故障熔断）</option>
                        {selectableNodes.map((n) => (
                          <option key={n.id} value={`u1·${n.name}`}>
                            {n.is_owner ? '[我的订阅] ' : '[公共池] '}
                            {n.name} ({n.region || n.kind}
                            {n.latency_ms > 0 ? ` · ${n.latency_ms}ms` : ''})
                          </option>
                        ))}
                      </select>
                      <Button
                        size='sm'
                        variant='outline'
                        className='h-8 text-xs'
                        disabled={switchNodeMutation.isPending}
                        onClick={() => switchNodeMutation.mutate(selectedKernelNode)}
                      >
                        切换节点
                      </Button>
                    </div>
                  </div>
                )}

                {Object.keys(detail.proxy_pool.regions ?? {}).length > 0 && (
                  <div className='mt-2 flex flex-wrap gap-1'>
                    {Object.entries(detail.proxy_pool.regions)
                      .sort((a, b) => b[1] - a[1])
                      .slice(0, 12)
                      .map(([region, count]) => (
                        <span
                          key={region}
                          className='rounded-full bg-muted px-2 py-0.5 text-[11px] text-muted-foreground'
                        >
                          {region} · {count}
                        </span>
                      ))}
                  </div>
                )}
              </section>

              {/* IP 白名单（免费层） */}
              <section className='rounded-xl border border-border/70 p-3'>
                <SectionTitle
                  icon={<ShieldQuestion className='size-3.5' />}
                  title='入口 IP 白名单'
                />
                <p className='mt-1 text-[11px] text-muted-foreground'>
                  免费层按来源 IP 放行，不校验密钥。留空表示不限来源；支持逗号/换行与 CIDR。
                </p>
                <textarea
                  value={allowIps}
                  onChange={(event) => setAllowIps(event.target.value)}
                  rows={2}
                  placeholder='1.2.3.4, 10.0.0.0/8'
                  className='mt-2 w-full resize-y rounded-lg border border-border/70 bg-background px-3 py-2 font-mono text-xs outline-none focus:border-[#4d6bfe]/60'
                />
                <div className='mt-2 flex justify-end'>
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={
                      allowIpsMutation.isPending ||
                      allowIps === (detail.allow_ips ?? '')
                    }
                    onClick={() => allowIpsMutation.mutate(allowIps)}
                  >
                    {allowIpsMutation.isPending ? (
                      <Loader2 className='size-3.5 animate-spin' />
                    ) : (
                      <Save className='size-3.5' />
                    )}
                    保存白名单
                  </Button>
                </div>
              </section>

              {/* 渠道模型 & 公共池模型选择 */}
              <section className='rounded-xl border border-border/70 p-3'>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <SectionTitle
                    icon={<Globe2 className='size-3.5' />}
                    title='渠道模型选择（支持获取最新免费模型、一键全选/全不选）'
                  />
                  <Button
                    size='sm'
                    variant='outline'
                    className='h-7 px-2 text-xs'
                    disabled={fetchUpstreamMutation.isPending}
                    onClick={() => fetchUpstreamMutation.mutate()}
                  >
                    {fetchUpstreamMutation.isPending ? (
                      <Loader2 className='me-1 size-3 animate-spin' />
                    ) : (
                      <RefreshCw className='me-1 size-3' />
                    )}
                    获取上游最新模型
                  </Button>
                </div>

                <div className='mt-2 flex flex-wrap items-center gap-1.5 text-xs'>
                  <Button
                    size='sm'
                    variant='secondary'
                    className='h-7 px-2 text-xs'
                    onClick={() => setSelectedModels([...candidateModels])}
                  >
                    一键全选（启用在我的渠道）
                  </Button>
                  <Button
                    size='sm'
                    variant='outline'
                    className='h-7 px-2 text-xs'
                    onClick={() => {
                      setSelectedModels([])
                      setPublicModels([])
                    }}
                  >
                    一键全不选（我的渠道）
                  </Button>
                  <Button
                    size='sm'
                    variant='outline'
                    className='h-7 px-2 text-xs text-emerald-600'
                    onClick={() => {
                      setSelectedModels([...candidateModels])
                      setPublicModels([...candidateModels])
                    }}
                  >
                    全选公开到公共池
                  </Button>
                  <Button
                    size='sm'
                    variant='outline'
                    className='h-7 px-2 text-xs'
                    onClick={() => setPublicModels([])}
                  >
                    全不选公开
                  </Button>
                </div>

                <div className='mt-2 max-h-64 space-y-1 overflow-y-auto rounded-lg border border-border/60 p-2'>
                  {candidateModels.length === 0 && (
                    <p className='px-1 py-2 text-xs text-muted-foreground'>
                      点击右上方「获取上游最新模型」拉取可用模型列表。
                    </p>
                  )}
                  {candidateModels.map((model) => {
                    const checked = selectedModels.includes(model)
                    const pubChecked = publicModels.includes(model)
                    return (
                      <div
                        key={model}
                        className='flex items-center justify-between gap-2 rounded-md px-1.5 py-1 hover:bg-muted text-xs'
                      >
                        <span className='truncate font-mono'>{model}</span>
                        <div className='flex shrink-0 items-center gap-3'>
                          <label className='inline-flex cursor-pointer items-center gap-1'>
                            <input
                              type='checkbox'
                              checked={checked}
                              onChange={() => {
                                setSelectedModels((prev) =>
                                  checked
                                    ? prev.filter((item) => item !== model)
                                    : [...prev, model]
                                )
                                if (checked) {
                                  setPublicModels((prev) =>
                                    prev.filter((item) => item !== model)
                                  )
                                }
                              }}
                              className='size-3.5 accent-[#4d6bfe]'
                            />
                            <span>启用在我的渠道</span>
                          </label>
                          <label className='inline-flex cursor-pointer items-center gap-1 text-emerald-600'>
                            <input
                              type='checkbox'
                              checked={pubChecked}
                              onChange={() => {
                                if (!pubChecked && !checked) {
                                  setSelectedModels((prev) => [...prev, model])
                                }
                                setPublicModels((prev) =>
                                  pubChecked
                                    ? prev.filter((item) => item !== model)
                                    : [...prev, model]
                                )
                              }}
                              className='size-3.5 accent-emerald-600'
                            />
                            <Globe className='size-3' />
                            <span>公共池</span>
                          </label>
                        </div>
                      </div>
                    )
                  })}
                </div>
                <div className='mt-2 flex items-center justify-between'>
                  <span className='text-[11px] text-muted-foreground'>
                    已启用在我的渠道: {selectedModels.length} / {candidateModels.length} 个 · 公开到公共池: {publicModels.length} 个
                  </span>
                  <Button
                    size='sm'
                    disabled={modelsMutation.isPending}
                    onClick={() =>
                      modelsMutation.mutate({
                        myModels: selectedModels,
                        pubModels: publicModels,
                        allModels: candidateModels,
                      })
                    }
                  >
                    {modelsMutation.isPending ? (
                      <Loader2 className='size-3.5 animate-spin' />
                    ) : (
                      <Save className='size-3.5' />
                    )}
                    保存模型配置
                  </Button>
                </div>
              </section>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function SectionTitle({
  icon,
  title,
}: {
  icon: ReactNode
  title: string
}) {
  return (
    <div className='flex items-center gap-1.5 text-xs font-medium text-muted-foreground'>
      {icon}
      {title}
    </div>
  )
}

function Metric({
  icon,
  label,
  value,
}: {
  icon: ReactNode
  label: string
  value: string
}) {
  return (
    <span className='inline-flex items-center gap-1 rounded-md bg-muted/60 px-2 py-1 text-xs'>
      <span className='text-muted-foreground'>{icon}</span>
      <span className='text-muted-foreground'>{label}</span>
      <span className='font-mono'>{value}</span>
    </span>
  )
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className='rounded-lg bg-muted/50 px-2 py-1.5'>
      <p className='text-[11px] text-muted-foreground'>{label}</p>
      <p className='font-mono text-sm'>{value}</p>
    </div>
  )
}

function formatTokens(value: number): string {
  if (!value || value <= 0) return '0'
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(2)}B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return String(value)
}

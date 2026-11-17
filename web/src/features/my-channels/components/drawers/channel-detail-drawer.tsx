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
  Globe2,
  Loader2,
  Network,
  Pencil,
  Save,
  ShieldQuestion,
  Timer,
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
import { cn } from '@/lib/utils'

import { getChannelDetail, setChannelAllowIps, updateChannel } from '../../api'
import { channelsQueryKeys } from '../../lib'
import type { Channel } from '../../types'

interface ChannelDetailDrawerProps {
  channel: Channel | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * 「我的渠道」详情抽屉：点开一个渠道就能看清它的健康度、额度、出口代理与模型。
 *
 * 数据来自 `/api/channel/self/:id/detail`（一次取齐），改名字 / 白名单直接落库，
 * 改完刷新列表缓存，不整页重载。
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

  const allowIpsMutation = useMutation({
    mutationFn: (next: string) => setChannelAllowIps(channelId, next),
    onSuccess: () => {
      toast.success('IP 白名单已保存')
      void refetch()
    },
    onError: () => toast.error('保存白名单失败'),
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

  const models = detail?.models ?? []
  const [selectedModels, setSelectedModels] = useState<string[]>([])
  useEffect(() => {
    if (!detail) return
    setSelectedModels(detail.models)
  }, [detail])

  const modelsMutation = useMutation({
    mutationFn: (next: string[]) =>
      updateChannel(channelId, { models: next.join(',') }),
    onSuccess: () => {
      toast.success('渠道模型已更新')
      void invalidate()
      void refetch()
    },
    onError: () => toast.error('更新模型失败'),
  })

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side='right' className='w-full sm:max-w-xl'>
        <SheetHeader>
          <SheetTitle className='flex items-center gap-2'>
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
          <SheetDescription>
            渠道 #{channelId} 的实时状态、额度、出口与模型
          </SheetDescription>
        </SheetHeader>

        <div className='min-h-0 flex-1 overflow-y-auto px-1 pb-6'>
          {isLoading && (
            <div className='flex items-center gap-2 px-4 py-10 text-sm text-muted-foreground'>
              <Loader2 className='size-4 animate-spin' /> 正在加载渠道详情…
            </div>
          )}

          {isError && (
            <div className='mx-4 mt-4 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive'>
              {(error as Error)?.message || '加载失败，请重试'}
            </div>
          )}

          {detail && (
            <div className='space-y-4 px-4 pt-2'>
              {/* 名称编辑 */}
              <section className='rounded-xl border border-border/70 p-3'>
                <SectionTitle icon={<Pencil className='size-3.5' />} title='名称' />
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
                <SectionTitle icon={<Activity className='size-3.5' />} title='健康度' />
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
                  最近 5 次调用样本，跨节点共享；权重系数 {health?.factor ?? 1}
                </p>
              </section>

              {/* 额度 */}
              <section className='rounded-xl border border-border/70 p-3'>
                <SectionTitle
                  icon={<BadgeCheck className='size-3.5' />}
                  title='额度 / 大概能用的 token'
                />
                {estimate?.hidden ? (
                  <p className='mt-2 rounded-lg bg-muted/60 px-3 py-2 text-xs text-muted-foreground'>
                    {estimate.reason}
                  </p>
                ) : (
                  <div className='mt-2 space-y-1.5'>
                    <div className='flex items-center justify-between text-xs'>
                      <span className='text-muted-foreground'>本月已用</span>
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
                  </div>
                )}
              </section>

              {/* 出口代理 */}
              <section className='rounded-xl border border-border/70 p-3'>
                <SectionTitle icon={<Network className='size-3.5' />} title='出口代理' />
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
                    管理出口池
                  </Link>
                </div>

                <div className='mt-3 grid grid-cols-2 gap-2 sm:grid-cols-4'>
                  <Stat label='出口节点' value={detail.proxy_pool.endpoint_total} />
                  <Stat label='可用节点' value={detail.proxy_pool.usable_url_count} />
                  <Stat label='订阅' value={detail.proxy_pool.subscription_total} />
                  <Stat label='订阅节点' value={detail.proxy_pool.subscription_nodes} />
                </div>
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
                  rows={3}
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

              {/* 模型 */}
              <section className='rounded-xl border border-border/70 p-3'>
                <SectionTitle icon={<Globe2 className='size-3.5' />} title='渠道模型' />
                <p className='mt-1 text-[11px] text-muted-foreground'>
                  勾选要挂在这个渠道上的模型；取消勾选会立即从路由候选里移除。
                </p>
                <div className='mt-2 max-h-56 space-y-1 overflow-y-auto rounded-lg border border-border/60 p-2'>
                  {models.length === 0 && (
                    <p className='px-1 py-2 text-xs text-muted-foreground'>
                      这个渠道还没有模型，去编辑抽屉里拉一次模型列表。
                    </p>
                  )}
                  {models.map((model) => {
                    const checked = selectedModels.includes(model)
                    return (
                      <label
                        key={model}
                        className='flex cursor-pointer items-center gap-2 rounded-md px-1.5 py-1 hover:bg-muted'
                      >
                        <input
                          type='checkbox'
                          checked={checked}
                          onChange={() =>
                            setSelectedModels((prev) =>
                              checked
                                ? prev.filter((item) => item !== model)
                                : [...prev, model]
                            )
                          }
                          className='size-3.5 accent-[#4d6bfe]'
                        />
                        <span className='truncate font-mono text-xs'>{model}</span>
                      </label>
                    )
                  })}
                </div>
                <div className='mt-2 flex items-center justify-between'>
                  <span className='text-[11px] text-muted-foreground'>
                    已选 {selectedModels.length} / {models.length}
                  </span>
                  <Button
                    size='sm'
                    disabled={
                      modelsMutation.isPending ||
                      !hasModelChanges(selectedModels, models)
                    }
                    onClick={() => modelsMutation.mutate(selectedModels)}
                  >
                    {modelsMutation.isPending ? (
                      <Loader2 className='size-3.5 animate-spin' />
                    ) : (
                      <Save className='size-3.5' />
                    )}
                    保存模型
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

/** 选中的模型集合和渠道现有集合是否真的不同（顺序无关）。 */
function hasModelChanges(selected: string[], current: string[]): boolean {
  if (selected.length !== current.length) return true
  const currentSet = new Set(current)
  return selected.some((item) => !currentSet.has(item))
}

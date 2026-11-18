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
  Trash2,
  Waypoints,
} from 'lucide-react'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { getChannelDetail, setChannelAllowIps, updateChannel } from '../../api'
import { channelsQueryKeys } from '../../lib'
import type { Channel } from '../../types'
import { ChannelDeleteDialog } from '../channel-delete-dialog'
import { ChannelModelEditor } from '../channel-model-editor'
import { ChannelProxyEditor } from '../channel-proxy-editor'

interface ChannelDetailDrawerProps {
  channel: Channel | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * 「我的渠道」二级详情抽屉：支持全屏顺畅上下滑动，实时查看额度、选择代理 IP / 订阅节点、
 * 获取上游最新免费模型、一键全选/全不选模型、一键删除渠道。
 */
export function ChannelDetailDrawer(props: ChannelDetailDrawerProps) {
  return (
    <ChannelDetailDrawerContent
      key={`${props.channel?.id}-${props.open}`}
      {...props}
    />
  )
}

function ChannelDetailDrawerContent({
  channel,
  open,
  onOpenChange,
}: ChannelDetailDrawerProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const channelId = channel?.id ?? 0

  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['self-channel-detail', channelId],
    queryFn: async () =>
      requireServerSuccess(await getChannelDetail(channelId)),
    enabled: open && channelId > 0,
  })

  const detail = data?.data

  const [name, setName] = useState('')
  const [allowIps, setAllowIps] = useState('')
  const [deleteOpen, setDeleteOpen] = useState(false)

  useEffect(() => {
    if (!detail) return
    setName(detail.channel.name)
    setAllowIps(detail.allow_ips ?? '')
  }, [detail])

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })

  const renameMutation = useMutation({
    mutationFn: async (next: string) =>
      requireServerSuccess(await updateChannel(channelId, { name: next })),
    onSuccess: () => {
      toast.success(t('Channel name updated'))
      void invalidate()
      void refetch()
    },
    onError: (error) => handleServerError(error, t('Failed to rename channel')),
  })

  const allowIpsMutation = useMutation({
    mutationFn: async (next: string) =>
      requireServerSuccess(await setChannelAllowIps(channelId, next)),
    onSuccess: () => {
      toast.success(t('IP allowlist saved'))
      void refetch()
    },
    onError: (error) =>
      handleServerError(error, t('Failed to save IP allowlist')),
  })

  const health = detail?.health
  const healthTone = useMemo(() => {
    switch (health?.state) {
      case 'healthy':
        return { label: t('Healthy'), className: 'bg-success/15 text-success' }
      case 'degraded':
        return { label: t('Degraded'), className: 'bg-warning/20 text-warning' }
      case 'down':
        return {
          label: t('Down'),
          className: 'bg-destructive/15 text-destructive',
        }
      default:
        return {
          label: t('Not enough samples'),
          className: 'bg-muted text-muted-foreground',
        }
    }
  }, [health?.state, t])

  const template = detail?.template
  const isIpFree = Boolean(template?.ip_free)
  const estimate = detail?.token_estimate

  const ownCount = detail?.proxy_pool.own_endpoint_count ?? 0
  const ownTokensTotal = detail?.proxy_pool.own_ip_tokens_total ?? 0

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('max-h-dvh sm:max-w-xl')}
      >
        <SheetHeader className='shrink-0 border-b px-5 py-4'>
          <div className='flex items-center justify-between gap-2 pr-6'>
            <SheetTitle className='flex flex-wrap items-center gap-2'>
              {detail?.channel.name ?? channel?.name ?? t('Channel details')}
              {template?.name ? (
                <Badge variant='secondary' className='text-xs'>
                  {template.name}
                </Badge>
              ) : null}
              {isIpFree ? (
                <Badge className='bg-[#4d6bfe]/15 text-xs text-[#4d6bfe]'>
                  {t('IP free tier')}
                </Badge>
              ) : (
                <Badge variant='outline' className='text-xs'>
                  {t('API key tier')}
                </Badge>
              )}
            </SheetTitle>
            <Button
              size='sm'
              variant='destructive'
              className='h-7 px-2 text-xs'
              onClick={() => setDeleteOpen(true)}
            >
              <Trash2 className='me-1 size-3.5' />
              {t('Delete channel')}
            </Button>
          </div>
          <SheetDescription>
            渠道 #{channelId} 的实时状态、额度、出口代理 IP 选择、订阅 IP
            数量与模型管理
          </SheetDescription>
        </SheetHeader>

        <div
          role='region'
          aria-label={t('Channel detail content')}
          tabIndex={0}
          className={sideDrawerFormClassName('gap-4 px-4 py-4')}
        >
          {isLoading && (
            <div className='text-muted-foreground flex items-center gap-2 py-10 text-sm'>
              <Loader2 className='size-4 animate-spin' />{' '}
              {t('Loading channel details...')}
            </div>
          )}

          {isError && (
            <div role='alert'>
              <ErrorState
                description={getServerErrorMessage(error)}
                onRetry={() => void refetch()}
              />
            </div>
          )}

          {detail && (
            <div className='space-y-4 pb-10'>
              {/* 名称编辑 */}
              <section className='border-border/70 rounded-xl border p-3'>
                <SectionTitle
                  icon={<Pencil className='size-3.5' />}
                  title={t('Channel name')}
                />
                <div className='mt-2 flex gap-2'>
                  <Input
                    aria-label={t('Channel name')}
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    className='border-border/70 bg-background h-9 w-full rounded-lg border px-3 text-sm outline-none focus:border-[#4d6bfe]/60'
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
              <section className='border-border/70 rounded-xl border p-3'>
                <SectionTitle
                  icon={<Activity className='size-3.5' />}
                  title={t('Health and circuit breaker')}
                />
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
                    label={t('Success rate')}
                    value={
                      health && health.samples > 0
                        ? `${health.success_rate.toFixed(0)}%`
                        : '—'
                    }
                  />
                  <Metric
                    icon={<Timer className='size-3.5' />}
                    label={t('Average latency')}
                    value={
                      health && health.avg_latency_ms > 0
                        ? `${health.avg_latency_ms} ms`
                        : '—'
                    }
                  />
                  <Metric
                    icon={<Waypoints className='size-3.5' />}
                    label={t('Samples')}
                    value={`${health?.samples ?? 0} 次`}
                  />
                </div>
                <p className='text-muted-foreground mt-2 text-[11px]'>
                  最近 5 次调用样本，跨节点共享；权重系数 {health?.factor ?? 1}
                  （遇错自动熔断切换下一个出口 IP）
                </p>
              </section>

              {/* 额度 / 大概能用的 token */}
              <section className='border-border/70 rounded-xl border p-3'>
                <SectionTitle
                  icon={<BadgeCheck className='size-3.5' />}
                  title={t('Token usage and estimate')}
                />
                {estimate?.hidden ? (
                  <div className='bg-muted/60 text-muted-foreground mt-2 space-y-1.5 rounded-lg px-3 py-2 text-xs'>
                    <p>{estimate.reason}</p>
                    {ownCount > 0 && ownTokensTotal > 0 ? (
                      <p className='font-medium text-emerald-600'>
                        你已绑定 {ownCount} 个自有订阅/代理 IP，按每 IP
                        额度粗估共可使用约{' '}
                        <span className='font-mono'>
                          {formatTokens(ownTokensTotal)}
                        </span>{' '}
                        tokens（免费公共池 IP 不显示剩余额度）。
                      </p>
                    ) : (
                      <p className='text-[11px]'>
                        当前使用免费公共 IP 池（不显示剩余 token
                        数量）；如添加自有机场订阅或私有 IP，将在此显示每个 IP
                        的预估可用额度总和。
                      </p>
                    )}
                  </div>
                ) : (
                  <div className='mt-2 space-y-1.5'>
                    <div className='flex items-center justify-between text-xs'>
                      <span className='text-muted-foreground'>
                        {t('Tokens used this month')}
                      </span>
                      <span className='font-mono'>
                        {formatTokens(estimate?.used ?? 0)}
                        {estimate?.monthly
                          ? ` / ${formatTokens(estimate.monthly)}`
                          : ''}
                      </span>
                    </div>
                    <div className='bg-muted h-1.5 w-full overflow-hidden rounded-full'>
                      <div
                        className='h-full rounded-full bg-[#4d6bfe]'
                        style={{
                          width: `${estimate?.monthly ? Math.min(100, ((estimate.used ?? 0) / estimate.monthly) * 100) : 2}%`,
                        }}
                      />
                    </div>
                    <p className='text-muted-foreground text-xs'>
                      {estimate?.remaining !== null &&
                      estimate?.remaining !== undefined
                        ? `预计还能用 ${formatTokens(estimate.remaining)} token`
                        : estimate?.reason}
                    </p>
                    {ownCount > 0 && ownTokensTotal > 0 && (
                      <p className='text-xs font-medium text-emerald-600'>
                        自有订阅/IP ({ownCount} 个) 叠加可用总额度 ≈{' '}
                        {formatTokens(ownTokensTotal)} tokens
                      </p>
                    )}
                  </div>
                )}
              </section>

              {/* 出口代理 & 代理 IP 选择 */}
              <section className='border-border/70 rounded-xl border p-3'>
                <SectionTitle
                  icon={<Network className='size-3.5' />}
                  title={t('Outbound proxy and subscriptions')}
                />
                <div className='bg-muted/50 mt-2 flex items-center justify-between gap-2 rounded-lg px-3 py-2'>
                  <div className='min-w-0'>
                    <p className='truncate text-sm'>{detail.proxy.label}</p>
                    {detail.proxy.value && detail.proxy.mode === 'fixed' ? (
                      <p className='text-muted-foreground truncate font-mono text-[11px]'>
                        {detail.proxy.value}
                      </p>
                    ) : null}
                  </div>
                  <Link
                    to='/proxy-pool'
                    className='shrink-0 text-xs text-[#4d6bfe] hover:underline'
                  >
                    {t('Manage proxy pool / subscriptions')}
                  </Link>
                </div>

                <div className='mt-3 grid grid-cols-2 gap-2 sm:grid-cols-4'>
                  <Stat
                    label={t('Visible endpoints')}
                    value={detail.proxy_pool.endpoint_total}
                  />
                  <Stat
                    label={t('Subscription nodes')}
                    value={detail.proxy_pool.subscription_nodes}
                  />
                  <Stat
                    label={t('Subscriptions')}
                    value={detail.proxy_pool.subscription_total}
                  />
                  <Stat
                    label={t('Public pool IPs')}
                    value={
                      detail.proxy_pool.public_pool_count ??
                      detail.proxy_pool.endpoint_enabled
                    }
                  />
                </div>

                <div className='mt-3'>
                  <ChannelProxyEditor detail={detail} />
                </div>

                {Object.keys(detail.proxy_pool.regions ?? {}).length > 0 && (
                  <div className='mt-2 flex flex-wrap gap-1'>
                    {Object.entries(detail.proxy_pool.regions)
                      .sort((a, b) => b[1] - a[1])
                      .slice(0, 12)
                      .map(([region, count]) => (
                        <span
                          key={region}
                          className='bg-muted text-muted-foreground rounded-full px-2 py-0.5 text-[11px]'
                        >
                          {region} · {count}
                        </span>
                      ))}
                  </div>
                )}
              </section>

              {/* IP 白名单（免费层） */}
              <section className='border-border/70 rounded-xl border p-3'>
                <SectionTitle
                  icon={<ShieldQuestion className='size-3.5' />}
                  title={t('Entry IP allowlist')}
                />
                <p className='text-muted-foreground mt-1 text-[11px]'>
                  免费层按来源 IP
                  放行，不校验密钥。留空表示不限来源；支持逗号/换行与 CIDR。
                </p>
                <Textarea
                  aria-label={t('Entry IP allowlist')}
                  value={allowIps}
                  onChange={(event) => setAllowIps(event.target.value)}
                  rows={2}
                  placeholder='1.2.3.4, 10.0.0.0/8'
                  className='border-border/70 bg-background mt-2 w-full resize-y rounded-lg border px-3 py-2 font-mono text-xs outline-none focus:border-[#4d6bfe]/60'
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
                    {t('Save allowlist')}
                  </Button>
                </div>
              </section>

              <section className='border-border/70 rounded-xl border p-3'>
                <SectionTitle
                  icon={<Globe2 className='size-3.5' />}
                  title={t('Channel models')}
                />
                <div className='mt-3'>
                  <ChannelModelEditor
                    detail={detail}
                    saveLabel={t('Save model configuration')}
                  />
                </div>
              </section>
            </div>
          )}
        </div>
      </SheetContent>
      <ChannelDeleteDialog
        channelId={channelId}
        channelName={detail?.channel.name ?? channel?.name ?? ''}
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        onDeleted={() => onOpenChange(false)}
      />
    </Sheet>
  )
}

function SectionTitle({ icon, title }: { icon: ReactNode; title: string }) {
  return (
    <div className='text-muted-foreground flex items-center gap-1.5 text-xs font-medium'>
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
    <span className='bg-muted/60 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs'>
      <span className='text-muted-foreground'>{icon}</span>
      <span className='text-muted-foreground'>{label}</span>
      <span className='font-mono'>{value}</span>
    </span>
  )
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className='bg-muted/50 rounded-lg px-2 py-1.5'>
      <p className='text-muted-foreground text-[11px]'>{label}</p>
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

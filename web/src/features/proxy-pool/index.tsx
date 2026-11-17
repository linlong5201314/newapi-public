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
import {
  Copy,
  Cpu,
  Download,
  Globe,
  KeyRound,
  Lock,
  Plus,
  RefreshCw,
  Rss,
  Sparkles,
  Trash2,
  Zap,
} from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { handleServerError } from '@/lib/handle-server-error'

import {
  createEndpoint,
  createSubscription,
  deleteEndpoint,
  deleteSubscription,
  exportConfig,
  getRuntimeStatus,
  getSummary,
  importHaizhuNodes,
  listEndpoints,
  listSubscriptions,
  reloadRuntime,
  selectRuntimeNode,
  syncSubscription,
  testEndpoint,
  updateEndpoint,
} from './api'
import { kindLabel, type ProxyEndpoint, type ProxySubscription } from './types'

const QUERY_KEY = ['proxy-pool'] as const

export function ProxyPoolPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [endpointOpen, setEndpointOpen] = useState(false)
  const [subscriptionOpen, setSubscriptionOpen] = useState(false)
  const [busyId, setBusyId] = useState<string>('')
  const [scopeFilter, setScopeFilter] = useState<'all' | 'mine' | 'public' | 'haizhu'>('all')
  const [searchKeyword, setSearchKeyword] = useState('')
  const [pageSize, setPageSize] = useState(120)

  const endpointsQuery = useQuery({
    queryKey: [...QUERY_KEY, 'endpoints'],
    queryFn: listEndpoints,
  })
  const subscriptionsQuery = useQuery({
    queryKey: [...QUERY_KEY, 'subscriptions'],
    queryFn: listSubscriptions,
  })
  const summaryQuery = useQuery({
    queryKey: [...QUERY_KEY, 'summary'],
    queryFn: getSummary,
  })
  const runtimeQuery = useQuery({
    queryKey: [...QUERY_KEY, 'runtime'],
    queryFn: getRuntimeStatus,
  })

  const refreshAll = () => {
    void queryClient.invalidateQueries({ queryKey: QUERY_KEY })
  }

  const createEndpointMutation = useMutation({
    mutationFn: createEndpoint,
    onSuccess: (res) => {
      if (res.success === false) {
        toast.error(res.message || t('Failed to add endpoint'))
        return
      }
      toast.success(t('Endpoint added'))
      setEndpointOpen(false)
      refreshAll()
    },
    onError: (error) => handleServerError(error, t('Failed to add endpoint')),
  })

  const createSubscriptionMutation = useMutation({
    mutationFn: createSubscription,
    onSuccess: (res) => {
      if (res.success === false) {
        toast.error(res.message || t('Failed to import subscription'))
        return
      }
      toast.success(
        t('Subscription imported: {{count}} nodes', {
          count: res.data?.node_count ?? 0,
        })
      )
      setSubscriptionOpen(false)
      refreshAll()
    },
    onError: (error) =>
      handleServerError(error, t('Failed to import subscription')),
  })

  const importHaizhuMutation = useMutation({
    mutationFn: (mode: 'full' | 'top') => importHaizhuNodes(mode),
    onSuccess: (res) => {
      if (res.success === false) {
        toast.error(res.message || '导入 Haizhu 节点失败')
        return
      }
      toast.success(`已导入 ${res.data?.imported ?? 0} 个 Haizhu 节点到公共池`)
      refreshAll()
    },
    onError: (error) => handleServerError(error, '导入 Haizhu 节点失败'),
  })

  const reloadRuntimeMutation = useMutation({
    mutationFn: reloadRuntime,
    onSuccess: (res) => {
      if (res.success === false) {
        toast.error(res.message || '重载 mihomo 内核失败')
        return
      }
      toast.success('共享 mihomo 内核已重载')
      refreshAll()
    },
    onError: (error) => handleServerError(error, '重载 mihomo 内核失败'),
  })

  const selectNodeMutation = useMutation({
    mutationFn: selectRuntimeNode,
    onSuccess: (res) => {
      if (res.success === false) {
        toast.error(res.message || '切换节点失败')
        return
      }
      toast.success(`已切换出口节点：${res.data?.selected ?? ''}`)
      refreshAll()
    },
    onError: (error) => handleServerError(error, '切换节点失败'),
  })

  const syncMutation = useMutation({
    mutationFn: syncSubscription,
    onMutate: (id) => setBusyId(`sub-${id}`),
    onSettled: () => setBusyId(''),
    onSuccess: (res) => {
      if (res.success === false) {
        toast.error(res.message || t('Sync failed'))
        return
      }
      toast.success(
        t('Subscription synced: {{count}} nodes', {
          count: res.data?.node_count ?? 0,
        })
      )
      refreshAll()
    },
    onError: (error) => handleServerError(error, t('Sync failed')),
  })

  const deleteSubscriptionMutation = useMutation({
    mutationFn: deleteSubscription,
    onMutate: (id) => setBusyId(`sub-${id}`),
    onSettled: () => setBusyId(''),
    onSuccess: () => {
      toast.success(t('Subscription deleted'))
      refreshAll()
    },
    onError: (error) => handleServerError(error, t('Failed to delete')),
  })

  const toggleMutation = useMutation({
    mutationFn: (payload: { id: number; enabled?: boolean; public?: boolean }) =>
      updateEndpoint(payload.id, {
        ...(payload.enabled !== undefined ? { enabled: payload.enabled } : {}),
        ...(payload.public !== undefined ? { public: payload.public } : {}),
      }),
    onSuccess: () => refreshAll(),
    onError: (error) => handleServerError(error, t('Failed to update')),
  })

  const deleteEndpointMutation = useMutation({
    mutationFn: deleteEndpoint,
    onMutate: (id) => setBusyId(`ep-${id}`),
    onSettled: () => setBusyId(''),
    onSuccess: () => {
      toast.success(t('Endpoint deleted'))
      refreshAll()
    },
    onError: (error) => handleServerError(error, t('Failed to delete')),
  })

  const testMutation = useMutation({
    mutationFn: testEndpoint,
    onMutate: (id) => setBusyId(`test-${id}`),
    onSettled: () => setBusyId(''),
    onSuccess: (res) => {
      if (res.success === false || res.data?.ok === false) {
        toast.error(res.data?.error || res.message || t('Unreachable'))
        return
      }
      const lat = res.data?.latency_ms ? ` (${res.data.latency_ms} ms)` : ''
      toast.success(`节点可用，出口: ${res.data?.ip || '—'}${lat}`)
      refreshAll()
    },
    onError: (error) => handleServerError(error, t('Test failed')),
  })

  const exportMutation = useMutation({
    mutationFn: exportConfig,
    onSuccess: (config) => {
      if (!config) {
        toast.error(t('Nothing to export'))
        return
      }
      void navigator.clipboard
        ?.writeText(config)
        .then(() => toast.success(t('mihomo config copied to clipboard')))
        .catch(() => toast.error(t('Copy failed')))
    },
    onError: (error) => handleServerError(error, t('Export failed')),
  })

  const endpoints = endpointsQuery.data ?? []
  const subscriptions = subscriptionsQuery.data ?? []
  const summary = summaryQuery.data
  const runtime = runtimeQuery.data

  const filteredEndpoints = useMemo(() => {
    const kw = searchKeyword.trim().toLowerCase()
    return endpoints.filter((ep) => {
      if (scopeFilter === 'mine' && (!ep.is_owner || ep.source === 'haizhu')) return false
      if (scopeFilter === 'public' && !ep.public) return false
      if (scopeFilter === 'haizhu' && ep.source !== 'haizhu') return false
      if (!kw) return true
      return (
        ep.name.toLowerCase().includes(kw) ||
        ep.region.toLowerCase().includes(kw) ||
        ep.kind.toLowerCase().includes(kw)
      )
    })
  }, [endpoints, scopeFilter, searchKeyword])

  const visibleEndpoints = useMemo(
    () => filteredEndpoints.slice(0, pageSize),
    [filteredEndpoints, pageSize]
  )

  let endpointsContent: ReactNode
  if (endpointsQuery.isLoading) {
    endpointsContent = <LoadingState />
  } else if (endpointsQuery.isError) {
    endpointsContent = (
      <ErrorState onRetry={() => void endpointsQuery.refetch()} />
    )
  } else if (filteredEndpoints.length === 0) {
    endpointsContent = (
      <EmptyState
        title={t('No egress nodes yet')}
        description='点击右上角「导入 Haizhu 7114 公共节点」或「导入订阅」即可立即填充代理池。'
      />
    )
  } else {
    endpointsContent = (
      <div className='space-y-2'>
        <div className='overflow-x-auto rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Name')}</TableHead>
                <TableHead>{t('Protocol')}</TableHead>
                <TableHead>{t('Region')}</TableHead>
                <TableHead>延迟</TableHead>
                <TableHead>可见性 / 共享</TableHead>
                <TableHead>{t('Status')}</TableHead>
                <TableHead className='text-end'>{t('Actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {visibleEndpoints.map((endpoint) => (
                <EndpointRow
                  key={endpoint.id}
                  endpoint={endpoint}
                  busy={busyId === `ep-${endpoint.id}` || busyId === `test-${endpoint.id}`}
                  testing={busyId === `test-${endpoint.id}`}
                  onToggle={(enabled) =>
                    toggleMutation.mutate({ id: endpoint.id, enabled })
                  }
                  onTogglePublic={(pub) =>
                    toggleMutation.mutate({ id: endpoint.id, public: pub })
                  }
                  onTest={() => testMutation.mutate(endpoint.id)}
                  onDelete={() => deleteEndpointMutation.mutate(endpoint.id)}
                />
              ))}
            </TableBody>
          </Table>
        </div>
        {filteredEndpoints.length > pageSize && (
          <div className='flex items-center justify-between px-1 text-xs text-muted-foreground'>
            <span>
              当前显示前 {visibleEndpoints.length} / 共 {filteredEndpoints.length} 个节点（已按低延迟优先排序）
            </span>
            <Button
              size='sm'
              variant='outline'
              onClick={() => setPageSize((p) => p + 300)}
            >
              加载更多 (+300)
            </Button>
          </div>
        )}
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Egress IP Pool')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='flex flex-wrap items-center gap-2'>
          <Button
            size='sm'
            variant='default'
            disabled={importHaizhuMutation.isPending}
            onClick={() => importHaizhuMutation.mutate('full')}
          >
            <Sparkles className='me-1 h-4 w-4' />
            {importHaizhuMutation.isPending ? '正在导入…' : '导入 Haizhu 7114 公共节点'}
          </Button>
          <Button size='sm' variant='outline' onClick={() => setSubscriptionOpen(true)}>
            <Rss className='me-1 h-4 w-4' />
            {t('Import subscription')}
          </Button>
          <Button size='sm' variant='outline' onClick={() => setEndpointOpen(true)}>
            <Plus className='me-1 h-4 w-4' />
            {t('Add node')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={exportMutation.isPending}
            onClick={() => exportMutation.mutate()}
          >
            <Download className='me-1 h-4 w-4' />
            {t('Export mihomo config')}
          </Button>
        </div>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='grid gap-4 sm:grid-cols-2 xl:grid-cols-4'>
          <StatCard
            icon={<Zap className='h-4 w-4' />}
            label='可见可用节点总数'
            value={String(summary?.endpoint_total ?? endpoints.length)}
            hint={`公共池公开: ${summary?.public_node_count ?? endpoints.filter((e) => e.public).length} · Haizhu内置: ${summary?.haizhu_node_count ?? 0}`}
          />
          <StatCard
            icon={<Globe className='h-4 w-4' />}
            label='共享 mihomo 内核'
            value={runtime?.running ? `运行中 (:${runtime.port || 23456})` : '待启动'}
            hint={`已装载 ${runtime?.proxies ?? 0} 个代理节点，自动优选最快节点`}
          />
          <StatCard
            icon={<Rss className='h-4 w-4' />}
            label={t('Subscriptions')}
            value={String(summary?.subscription_total ?? subscriptions.length)}
            hint='未公开订阅仅自己可见可调，公开订阅进公共池'
          />
          <StatCard
            icon={<Copy className='h-4 w-4' />}
            label='故障熔断与调度'
            value='低延迟优先 + 自动熔断'
            hint='遇 429/403/5xx 或超时无感切换下一个节点'
          />
        </div>

        {/* 共享 mihomo 内核控制卡 */}
        <Card>
          <CardHeader className='pb-3'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <div>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <Cpu className='h-4 w-4' />
                  单实例共享 mihomo 边车内核（端口 127.0.0.1:23456）
                </CardTitle>
                <CardDescription>
                  全站共用一套 mihomo 内核。公开节点（含 Haizhu 7114 节点与管理员公开节点）全员可用；你未勾选公开的私有节点/订阅只对你本人可见和可调用，别人既看不见也无法调用。
                </CardDescription>
              </div>
              <Button
                size='sm'
                variant='outline'
                disabled={reloadRuntimeMutation.isPending}
                onClick={() => reloadRuntimeMutation.mutate()}
              >
                <RefreshCw className='me-1 h-3.5 w-3.5' />
                重载内核配置
              </Button>
            </div>
          </CardHeader>
          <CardContent className='flex flex-wrap items-center gap-3 pt-0 text-xs'>
            <Badge variant={runtime?.running ? 'default' : 'secondary'}>
              {runtime?.running ? '内核在线' : '内核未运行'}
            </Badge>
            <span className='text-muted-foreground'>
              当前出站策略：
              <strong className='ms-1 font-mono text-foreground'>
                {runtime?.selected_node || 'PUBLIC_FAST (自动测速最快)'}
              </strong>
            </span>
            {runtime?.nodes && runtime.nodes.length > 0 && (
              <div className='ms-auto flex items-center gap-2'>
                <span className='text-muted-foreground'>手动指定出站节点：</span>
                <select
                  className='border-input bg-background h-8 max-w-[280px] rounded-md border px-2 text-xs'
                  value={runtime.selected_node || 'PUBLIC_FAST'}
                  onChange={(e) => selectNodeMutation.mutate(e.target.value)}
                >
                  <option value='PUBLIC_FAST'>⚡ PUBLIC_FAST（自动测速优选最快节点）</option>
                  {runtime.nodes.slice(0, 200).map((n) => (
                    <option key={n} value={n}>
                      {n}
                    </option>
                  ))}
                </select>
              </div>
            )}
          </CardContent>
        </Card>

        {subscriptions.length > 0 && (
          <Card>
            <CardHeader>
              <CardTitle className='text-base'>{t('Subscriptions')}</CardTitle>
              <CardDescription>
                {t(
                  'Clash / base64 / uri-list links are parsed into individual nodes on import and every re-sync.'
                )}
              </CardDescription>
            </CardHeader>
            <CardContent className='space-y-2'>
              {subscriptions.map((subscription) => (
                <SubscriptionRow
                  key={subscription.id}
                  subscription={subscription}
                  busy={busyId === `sub-${subscription.id}`}
                  onSync={() => syncMutation.mutate(subscription.id)}
                  onDelete={() => deleteSubscriptionMutation.mutate(subscription.id)}
                />
              ))}
            </CardContent>
          </Card>
        )}

        <Card>
          <CardHeader>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <div>
                <CardTitle className='text-base'>{t('Nodes')}</CardTitle>
                <CardDescription>
                  按延迟从快到慢排序；调用出错自动熔断切换到下一个健康节点。
                </CardDescription>
              </div>
              <div className='flex flex-wrap items-center gap-2'>
                <div className='flex rounded-md border p-0.5 text-xs'>
                  {(
                    [
                      ['all', `全部可见 (${endpoints.length})`],
                      ['mine', `我的私有/自建 (${endpoints.filter((e) => e.is_owner && e.source !== 'haizhu').length})`],
                      ['public', `公共池公开 (${endpoints.filter((e) => e.public).length})`],
                      ['haizhu', `Haizhu内置 (${endpoints.filter((e) => e.source === 'haizhu').length})`],
                    ] as const
                  ).map(([key, label]) => (
                    <button
                      key={key}
                      type='button'
                      onClick={() => setScopeFilter(key)}
                      className={`rounded px-2.5 py-1 transition-colors ${
                        scopeFilter === key
                          ? 'bg-primary text-primary-foreground font-medium'
                          : 'text-muted-foreground hover:text-foreground'
                      }`}
                    >
                      {label}
                    </button>
                  ))}
                </div>
                <Input
                  value={searchKeyword}
                  onChange={(e) => setSearchKeyword(e.target.value)}
                  placeholder='搜节点名 / 地区 (如 HK, US, SG)…'
                  className='h-8 w-52 text-xs'
                />
              </div>
            </div>
          </CardHeader>
          <CardContent>{endpointsContent}</CardContent>
        </Card>
      </SectionPageLayout.Content>

      <AddEndpointDialog
        open={endpointOpen}
        onOpenChange={setEndpointOpen}
        submitting={createEndpointMutation.isPending}
        onSubmit={(payload) => createEndpointMutation.mutate(payload)}
      />
      <AddSubscriptionDialog
        open={subscriptionOpen}
        onOpenChange={setSubscriptionOpen}
        submitting={createSubscriptionMutation.isPending}
        onSubmit={(payload) => createSubscriptionMutation.mutate(payload)}
      />
    </SectionPageLayout>
  )
}

interface StatCardProps {
  icon: ReactNode
  label: string
  value: string
  hint: string
}

function StatCard(props: StatCardProps) {
  return (
    <Card className='py-4'>
      <CardContent className='space-y-1'>
        <div className='text-muted-foreground flex items-center gap-2 text-xs'>
          {props.icon}
          <span>{props.label}</span>
        </div>
        <div className='font-mono text-2xl font-semibold tabular-nums'>
          {props.value}
        </div>
        <div className='text-muted-foreground text-xs'>{props.hint}</div>
      </CardContent>
    </Card>
  )
}

interface EndpointRowProps {
  endpoint: ProxyEndpoint
  busy: boolean
  testing: boolean
  onToggle: (enabled: boolean) => void
  onTogglePublic: (pub: boolean) => void
  onTest: () => void
  onDelete: () => void
}

function EndpointRow(props: EndpointRowProps) {
  const { t } = useTranslation()
  const endpoint = props.endpoint
  const failures = endpoint.fail_count ?? 0
  const isOwner = endpoint.is_owner !== false

  return (
    <TableRow>
      <TableCell className='max-w-[240px] truncate font-medium'>
        {endpoint.name}
        {endpoint.source === 'haizhu' ? (
          <Badge variant='secondary' className='ms-2 text-[10px]'>
            Haizhu公共池
          </Badge>
        ) : endpoint.subscription_id > 0 ? (
          <Badge variant='outline' className='ms-2'>
            {t('From subscription')}
          </Badge>
        ) : null}
      </TableCell>
      <TableCell>
        <Badge variant='secondary'>{kindLabel(endpoint.kind)}</Badge>
      </TableCell>
      <TableCell className='text-muted-foreground text-sm'>
        {endpoint.region || '—'}
      </TableCell>
      <TableCell className='font-mono text-xs'>
        {endpoint.latency_ms && endpoint.latency_ms > 0 ? (
          <span className='text-emerald-600 font-medium'>{endpoint.latency_ms} ms</span>
        ) : (
          <span className='text-muted-foreground'>未测速</span>
        )}
      </TableCell>
      <TableCell>
        {isOwner && endpoint.source !== 'haizhu' ? (
          <label className='inline-flex cursor-pointer items-center gap-1.5 text-xs'>
            <Switch
              checked={Boolean(endpoint.public)}
              onCheckedChange={props.onTogglePublic}
            />
            {endpoint.public ? (
              <Badge variant='default' className='text-[10px]'>
                公开(公共池)
              </Badge>
            ) : (
              <Badge variant='outline' className='gap-1 text-[10px]'>
                <Lock className='h-2.5 w-2.5' />
                仅自己可用
              </Badge>
            )}
          </label>
        ) : (
          <Badge variant='secondary' className='text-[10px]'>
            公共池共享
          </Badge>
        )}
      </TableCell>
      <TableCell>
        <div className='flex items-center gap-2'>
          {isOwner && (
            <Switch
              checked={endpoint.enabled}
              onCheckedChange={props.onToggle}
              aria-label={t('Enabled')}
            />
          )}
          {failures > 0 && (
            <Badge variant='warning'>{t('{{n}} failures', { n: failures })}</Badge>
          )}
          {endpoint.last_error && (
            <Badge variant='destructive' className='max-w-[160px] truncate'>
              {endpoint.last_error}
            </Badge>
          )}
        </div>
      </TableCell>
      <TableCell className='text-end'>
        <div className='flex items-center justify-end gap-1'>
          <Button
            size='sm'
            variant='ghost'
            disabled={props.busy || !endpoint.dialable}
            title='测速并验证该节点可用性'
            onClick={props.onTest}
          >
            <KeyRound className='h-4 w-4' />
          </Button>
          {isOwner && endpoint.source !== 'haizhu' && (
            <Button
              size='sm'
              variant='ghost'
              disabled={props.busy}
              onClick={props.onDelete}
            >
              <Trash2 className='h-4 w-4' />
            </Button>
          )}
        </div>
      </TableCell>
    </TableRow>
  )
}

interface SubscriptionRowProps {
  subscription: ProxySubscription
  busy: boolean
  onSync: () => void
  onDelete: () => void
}

/** 字节数 → 人类可读（1024 进制）。 */
function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  return `${value >= 100 || index === 0 ? Math.round(value) : value.toFixed(1)} ${units[index]}`
}
function SubscriptionRow(props: SubscriptionRowProps) {
  const { t } = useTranslation()
  const subscription = props.subscription

  const total = subscription.traffic_total ?? 0
  const used = subscription.traffic_used ?? 0
  const trafficText = total > 0 ? `${formatBytes(used)} / ${formatBytes(total)}` : used > 0 ? `${formatBytes(used)}` : ''
  const expireText = subscription.expire_at
    ? new Date(subscription.expire_at).toLocaleDateString()
    : ''

  return (
    <div className='flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2'>
      <div className='min-w-0 space-y-0.5'>
        <div className='flex items-center gap-2'>
          <span className='truncate text-sm font-medium'>{subscription.name}</span>
          <Badge variant='outline'>{subscription.format || 'auto'}</Badge>
          <Badge variant='secondary'>
            {t('{{n}} IPs', { n: subscription.node_count })}
          </Badge>
          <Badge variant={subscription.public ? 'default' : 'outline'} className='text-xs'>
            {subscription.public ? '已公开到公共池' : '私有（仅自己可用）'}
          </Badge>
          {trafficText ? (
            <Badge variant='outline' className='font-mono text-xs'>
              {trafficText}
            </Badge>
          ) : null}
          {expireText ? (
            <Badge variant='outline' className='text-xs'>
              {t('Expires')} {expireText}
            </Badge>
          ) : null}
        </div>
        <div className='text-muted-foreground truncate font-mono text-xs'>
          {subscription.url}
        </div>
        {subscription.last_error && (
          <div className='text-destructive truncate text-xs'>
            {subscription.last_error}
          </div>
        )}
      </div>
      <div className='flex items-center gap-1'>
        <Button
          size='sm'
          variant='ghost'
          disabled={props.busy}
          onClick={props.onSync}
        >
          <RefreshCw className='h-4 w-4' />
        </Button>
        <Button
          size='sm'
          variant='ghost'
          disabled={props.busy}
          onClick={props.onDelete}
        >
          <Trash2 className='h-4 w-4' />
        </Button>
      </div>
    </div>
  )
}

interface AddEndpointDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  submitting: boolean
  onSubmit: (payload: { name: string; url: string; public: boolean }) => void
}

function AddEndpointDialog(props: AddEndpointDialogProps) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [isPublic, setIsPublic] = useState(false)

  const submit = () => {
    if (!url.trim()) {
      toast.error(t('Proxy URL is required'))
      return
    }
    props.onSubmit({ name: name.trim(), url: url.trim(), public: isPublic })
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('Add egress node')}</DialogTitle>
          <DialogDescription>
            添加代理 IP 节点。若不勾选公开，则只有你自己的账户看得见、调得动，别人看不见也用不了。
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-3'>
          <div className='space-y-1'>
            <Label htmlFor='proxy-name'>{t('Name')}</Label>
            <Input
              id='proxy-name'
              value={name}
              placeholder={t('e.g. home broadband')}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className='space-y-1'>
            <Label htmlFor='proxy-url'>{t('Proxy URL')}</Label>
            <Input
              id='proxy-url'
              value={url}
              placeholder='http://user:pass@host:port 或 socks5://host:port'
              onChange={(event) => setUrl(event.target.value)}
            />
          </div>
          <label className='flex cursor-pointer items-center gap-2 pt-1 text-xs'>
            <input
              type='checkbox'
              checked={isPublic}
              onChange={(e) => setIsPublic(e.target.checked)}
              className='size-4'
            />
            <span>公开到公共池 IP（勾选后所有人可见可调用；不勾选则仅自己账户可见可用）</span>
          </label>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button disabled={props.submitting} onClick={submit}>
            {props.submitting ? t('Saving...') : t('Add')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

interface AddSubscriptionDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  submitting: boolean
  onSubmit: (payload: {
    name: string
    url: string
    format: 'auto' | 'clash' | 'base64' | 'uri-list'
    public: boolean
  }) => void
}

function AddSubscriptionDialog(props: AddSubscriptionDialogProps) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [format, setFormat] = useState<
    'auto' | 'clash' | 'base64' | 'uri-list'
  >('auto')
  const [isPublic, setIsPublic] = useState(false)

  const submit = () => {
    if (!url.trim()) {
      toast.error(t('Subscription URL is required'))
      return
    }
    props.onSubmit({ name: name.trim(), url: url.trim(), format, public: isPublic })
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('Import subscription')}</DialogTitle>
          <DialogDescription>
            粘贴机场订阅链接，自动过滤流量提示假节点并注入共享 mihomo 内核。不勾选公开时仅你本人可见可调用。
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-3'>
          <div className='space-y-1'>
            <Label htmlFor='sub-name'>{t('Name')}</Label>
            <Input
              id='sub-name'
              value={name}
              placeholder={t('optional')}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className='space-y-1'>
            <Label htmlFor='sub-url'>{t('Subscription URL')}</Label>
            <Input
              id='sub-url'
              value={url}
              placeholder='https://example.com/api/v1/client/subscribe?token=...'
              onChange={(event) => setUrl(event.target.value)}
            />
          </div>
          <div className='space-y-1'>
            <Label htmlFor='sub-format'>{t('Format')}</Label>
            <select
              id='sub-format'
              className='border-input bg-background h-9 w-full rounded-md border px-2 text-sm'
              value={format}
              onChange={(event) =>
                setFormat(
                  event.target.value as
                    | 'auto'
                    | 'clash'
                    | 'base64'
                    | 'uri-list'
                )
              }
            >
              <option value='auto'>{t('Auto detect')}</option>
              <option value='clash'>Clash YAML</option>
              <option value='base64'>Base64</option>
              <option value='uri-list'>URI list</option>
            </select>
          </div>
          <label className='flex cursor-pointer items-center gap-2 pt-1 text-xs'>
            <input
              type='checkbox'
              checked={isPublic}
              onChange={(e) => setIsPublic(e.target.checked)}
              className='size-4'
            />
            <span>公开到公共池 IP（勾选后显示在公共池供大家调用；不勾选仅自己账户可见可用）</span>
          </label>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button disabled={props.submitting} onClick={submit}>
            {props.submitting ? t('Importing...') : t('Import')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

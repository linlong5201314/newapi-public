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
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import {
  getCoreRowModel,
  getPaginationRowModel,
  useReactTable,
} from '@tanstack/react-table'
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

import { DataTablePagination, StaticDataTable } from '@/components/data-table'
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
import { TableCell, TableRow } from '@/components/ui/table'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  createEndpoint,
  createSubscription,
  deleteEndpoint,
  deleteSubscription,
  exportConfig,
  getRuntimeStatus,
  getSummary,
  importHaizhuNodes,
  listEndpointPage,
  listSubscriptions,
  reloadRuntime,
  selectRuntimeNode,
  syncSubscription,
  testEndpoint,
  updateEndpoint,
} from './api'
import { NodeModelAvailability } from './node-model-availability'
import { kindLabel, type ProxyEndpoint, type ProxySubscription } from './types'

const QUERY_KEY = ['proxy-pool'] as const

export function ProxyPoolPage() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const isSuperAdmin = useAuthStore(
    (state) => (state.auth.user?.role ?? 0) >= ROLE.SUPER_ADMIN
  )
  const queryClient = useQueryClient()
  const [endpointOpen, setEndpointOpen] = useState(false)
  const [subscriptionOpen, setSubscriptionOpen] = useState(false)
  const [busyId, setBusyId] = useState<string>('')
  const [scopeFilter, setScopeFilter] = useState<
    'all' | 'mine' | 'public' | 'haizhu'
  >('all')
  const [searchKeyword, setSearchKeyword] = useState('')
  const endpointsQuery = useInfiniteQuery({
    queryKey: [...QUERY_KEY, 'endpoint-pages'],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => listEndpointPage(pageParam),
    getNextPageParam: (lastPage) => {
      const pagination = lastPage.pagination
      if (!pagination.has_more || pagination.returned <= 0) return undefined
      return pagination.offset + pagination.returned
    },
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
    enabled: isSuperAdmin,
    refetchInterval: isSuperAdmin ? 10000 : false,
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
    mutationFn: (payload: {
      id: number
      enabled?: boolean
      public?: boolean
    }) =>
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

  const endpoints = useMemo(() => {
    const byId = new Map<number, ProxyEndpoint>()
    for (const page of endpointsQuery.data?.pages ?? []) {
      for (const endpoint of page.data) byId.set(endpoint.id, endpoint)
    }
    return [...byId.values()]
  }, [endpointsQuery.data])
  const subscriptions = subscriptionsQuery.data ?? []
  const summary = summaryQuery.data
  const runtime = runtimeQuery.data

  const filteredEndpoints = useMemo(() => {
    const kw = searchKeyword.trim().toLowerCase()
    return endpoints.filter((ep) => {
      if (scopeFilter === 'mine' && (!ep.is_owner || ep.source === 'haizhu')) {
        return false
      }
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

  const endpointColumns = useMemo(
    () => [
      { id: 'name', header: t('Name') },
      { id: 'kind', header: t('Protocol') },
      { id: 'region', header: t('Region') },
      { id: 'latency', header: t('Latency') },
      { id: 'visibility', header: t('Visibility / sharing') },
      { id: 'status', header: t('Status') },
      { id: 'actions', header: t('Actions'), className: 'text-end' },
    ],
    [t]
  )
  const endpointTable = useReactTable({
    data: filteredEndpoints,
    columns: endpointColumns,
    getRowId: (endpoint) => String(endpoint.id),
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageIndex: 0, pageSize: 100 } },
  })
  const visibleEndpoints = endpointTable
    .getRowModel()
    .rows.map((row) => row.original)

  let endpointsContent: ReactNode
  if (endpointsQuery.isPending) {
    endpointsContent = <LoadingState />
  } else if (endpointsQuery.isError && !endpointsQuery.data) {
    endpointsContent = (
      <ErrorState onRetry={() => void endpointsQuery.refetch()} />
    )
  } else if (filteredEndpoints.length === 0) {
    const hasFilter = scopeFilter !== 'all' || searchKeyword.trim().length > 0
    endpointsContent = (
      <EmptyState
        title={
          hasFilter ? t('No matching fetched nodes') : t('No egress nodes yet')
        }
        description={
          hasFilter
            ? t(
                'Change the filters or load more inventory. Unfetched records are not searched.'
              )
            : t(
                'Import a subscription to add inventory. Imported nodes still require probing.'
              )
        }
      />
    )
  } else {
    endpointsContent = (
      <div className='space-y-2'>
        <StaticDataTable
          columns={endpointColumns}
          data={visibleEndpoints}
          getRowKey={(endpoint) => endpoint.id}
          renderRow={(endpoint) => (
            <EndpointRow
              endpoint={endpoint}
              busy={
                busyId === `ep-${endpoint.id}` ||
                busyId === `test-${endpoint.id}`
              }
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
          )}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            '{{count}} matching fetched nodes. Row pagination below does not fetch more inventory.',
            {
              count: formatNumber(filteredEndpoints.length, locale),
            }
          )}
        </p>
        <DataTablePagination table={endpointTable} />
      </div>
    )
  }

  let loadNextLabel = t('Load next inventory page')
  if (endpointsQuery.isFetchingNextPage) {
    loadNextLabel = t('Loading inventory...')
  } else if (endpointsQuery.isFetchNextPageError) {
    loadNextLabel = t('Retry loading inventory')
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Egress IP Pool')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='flex flex-wrap items-center gap-2'>
          {isSuperAdmin && (
            <Button
              size='sm'
              variant='default'
              disabled={importHaizhuMutation.isPending}
              onClick={() => importHaizhuMutation.mutate('full')}
            >
              <Sparkles className='me-1 h-4 w-4' />
              {importHaizhuMutation.isPending
                ? '正在导入…'
                : '导入 Haizhu 7114 公共节点'}
            </Button>
          )}
          <Button
            size='sm'
            variant='outline'
            onClick={() => setSubscriptionOpen(true)}
          >
            <Rss className='me-1 h-4 w-4' />
            {t('Import subscription')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={() => setEndpointOpen(true)}
          >
            <Plus className='me-1 h-4 w-4' />
            {t('Add node')}
          </Button>
          {isSuperAdmin && (
            <Button
              size='sm'
              variant='outline'
              disabled={exportMutation.isPending}
              onClick={() => exportMutation.mutate()}
            >
              <Download className='me-1 h-4 w-4' />
              {t('Export mihomo config')}
            </Button>
          )}
        </div>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        {summaryQuery.isError && (
          <ErrorState
            title={t('Failed to load proxy inventory')}
            onRetry={() => void summaryQuery.refetch()}
          />
        )}
        <div className='grid gap-4 sm:grid-cols-2 xl:grid-cols-4'>
          <StatCard
            icon={<Zap className='h-4 w-4' />}
            label={t('Imported inventory (database, visible to you)')}
            value={formatNumber(
              summary?.imported_total ?? summary?.endpoint_total,
              locale
            )}
            hint={t(
              'Enabled: {{enabled}}. Enabled does not mean verified healthy.',
              {
                enabled: formatNumber(
                  summary?.enabled ?? summary?.endpoint_enabled,
                  locale
                ),
              }
            )}
          />
          <StatCard
            icon={<Globe className='h-4 w-4' />}
            label={t('Loaded in this process (visible to you)')}
            value={formatNumber(summary?.loaded_this_process, locale)}
            hint={t(
              'Not summed across instances. Unloaded inventory: {{remaining}}.',
              { remaining: formatNumber(summary?.inventory_remaining, locale) }
            )}
          />
          <StatCard
            icon={<Rss className='h-4 w-4' />}
            label={t('Probed healthy (database, enabled nodes)')}
            value={formatNumber(summary?.probed_healthy, locale)}
            hint={t('Unknown: {{unknown}} · Failed: {{failed}}', {
              unknown: formatNumber(summary?.unknown, locale),
              failed: formatNumber(summary?.failed, locale),
            })}
          />
          <StatCard
            icon={<Copy className='h-4 w-4' />}
            label={t('Stable account egress')}
            value={t('One account, one selected endpoint')}
            hint={t('Changes only when unusable. No implicit direct fallback.')}
          />
        </div>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Node records are not unique exit IPs. Probe results are historical, not a current availability guarantee.'
          )}
        </p>
        {endpointsQuery.data && (
          <p className='text-muted-foreground text-xs' role='status'>
            {t('Fetched {{fetched}} of {{total}} visible inventory records.', {
              fetched: formatNumber(endpoints.length, locale),
              total: formatNumber(
                summary?.imported_total ?? summary?.endpoint_total,
                locale
              ),
            })}
          </p>
        )}
        <p className='text-muted-foreground text-xs'>
          {t(
            'Browser list pages do not control runtime loading or prove node health.'
          )}{' '}
          {t(
            'Preload limit: {{limit}}. Preloading is separate from browser list pages.',
            {
              limit: formatNumber(summary?.runtime_preload_limit, locale),
            }
          )}{' '}
          {summary?.on_demand_loading === true &&
            t(
              'Client-protocol nodes can load on first use, up to {{limit}} listeners per process. Active listeners are not evicted; capacity or probe failures can still prevent use.',
              { limit: formatNumber(summary.runtime_listener_limit, locale) }
            )}
          {summary?.on_demand_loading !== true &&
            t(
              'On-demand runtime loading is not confirmed by this server. Unloaded inventory is not a healthy-node count.'
            )}
        </p>

        {/* 共享 mihomo 内核控制卡：仅超级管理员可见 */}
        {isSuperAdmin && (
          <Card>
            <CardHeader className='pb-3'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <div>
                  <CardTitle className='flex items-center gap-2 text-base'>
                    <Cpu className='h-4 w-4' />
                    {t('Shared mihomo runtime (super administrator only)')}
                  </CardTitle>
                  <CardDescription>
                    {t(
                      'This process has its own bounded mihomo runtime. Database inventory is shared; listeners are not. Private listeners remain owner-only.'
                    )}
                  </CardDescription>
                </div>
                <Button
                  size='sm'
                  variant='outline'
                  disabled={reloadRuntimeMutation.isPending}
                  onClick={() => reloadRuntimeMutation.mutate()}
                >
                  <RefreshCw className='me-1 h-3.5 w-3.5' />
                  {t('Reload kernel configuration')}
                </Button>
              </div>
            </CardHeader>
            <CardContent className='flex flex-wrap items-center gap-3 pt-0 text-xs'>
              {runtimeQuery.isError && (
                <ErrorState
                  className='min-h-0 w-full'
                  title={t('Failed to load kernel status')}
                  onRetry={() => void runtimeQuery.refetch()}
                />
              )}
              {runtime?.last_error && (
                <p role='alert' className='text-destructive w-full break-words'>
                  {runtime.last_error}
                </p>
              )}
              <Badge variant={runtime?.running ? 'default' : 'secondary'}>
                {runtime?.running ? '内核在线' : '内核未运行'}
              </Badge>
              <span className='text-muted-foreground'>
                {t('Legacy shared selector (not used by account egress):')}
                <strong className='text-foreground ms-1 font-mono'>
                  {runtime?.selected_node || 'PUBLIC_FAST (自动测速最快)'}
                </strong>
              </span>
              {runtime?.nodes && runtime.nodes.length > 0 && (
                <div className='ms-auto flex items-center gap-2'>
                  <span className='text-muted-foreground'>
                    {t('Legacy shared selector:')}
                  </span>
                  <select
                    className='border-input bg-background h-8 max-w-[280px] rounded-md border px-2 text-xs'
                    value={runtime.selected_node || 'PUBLIC_FAST'}
                    onChange={(e) => selectNodeMutation.mutate(e.target.value)}
                  >
                    <option value='PUBLIC_FAST'>
                      ⚡ PUBLIC_FAST（自动测速优选最快节点）
                    </option>
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
        )}

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
                  onDelete={() =>
                    deleteSubscriptionMutation.mutate(subscription.id)
                  }
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
                  {t(
                    'The same account keeps its endpoint until it is unusable. Enabled and imported nodes are not verified healthy.'
                  )}
                </CardDescription>
              </div>
              <div className='flex flex-wrap items-center gap-2'>
                <div className='flex rounded-md border p-0.5 text-xs'>
                  {(
                    [
                      [
                        'all',
                        t('All fetched ({{count}})', {
                          count: formatNumber(endpoints.length, locale),
                        }),
                      ],
                      [
                        'mine',
                        t('Mine fetched ({{count}})', {
                          count: formatNumber(
                            endpoints.filter(
                              (e) => e.is_owner && e.source !== 'haizhu'
                            ).length,
                            locale
                          ),
                        }),
                      ],
                      [
                        'public',
                        t('Public fetched ({{count}})', {
                          count: formatNumber(
                            endpoints.filter((e) => e.public).length,
                            locale
                          ),
                        }),
                      ],
                      [
                        'haizhu',
                        t('Haizhu fetched ({{count}})', {
                          count: formatNumber(
                            endpoints.filter((e) => e.source === 'haizhu')
                              .length,
                            locale
                          ),
                        }),
                      ],
                    ] as const
                  ).map(([key, label]) => (
                    <Button
                      key={key}
                      size='sm'
                      variant={scopeFilter === key ? 'default' : 'ghost'}
                      aria-pressed={scopeFilter === key}
                      onClick={() => {
                        setScopeFilter(key)
                        endpointTable.setPageIndex(0)
                      }}
                    >
                      {label}
                    </Button>
                  ))}
                </div>
                <Input
                  value={searchKeyword}
                  onChange={(e) => {
                    setSearchKeyword(e.target.value)
                    endpointTable.setPageIndex(0)
                  }}
                  aria-label={t('Search fetched nodes')}
                  aria-describedby='proxy-inventory-filter-scope'
                  placeholder={t('Search fetched names, regions, protocols...')}
                  className='h-8 w-52 text-xs'
                />
              </div>
            </div>
          </CardHeader>
          <CardContent className='space-y-3'>
            <p
              id='proxy-inventory-filter-scope'
              className='text-muted-foreground text-xs'
            >
              {t(
                'Search and visibility filters apply only to fetched records. Load more inventory to include the rest.'
              )}
            </p>
            {endpointsContent}
            {endpointsQuery.isFetchNextPageError && (
              <p role='alert' className='text-destructive text-sm'>
                {t(
                  'Could not load more inventory. Fetched nodes are still available.'
                )}
              </p>
            )}
            {endpointsQuery.isRefetchError &&
              !endpointsQuery.isFetchNextPageError &&
              endpointsQuery.data && (
                <ErrorState
                  className='min-h-0'
                  title={t(
                    'Could not refresh inventory. Previously fetched records are shown.'
                  )}
                  onRetry={() => void endpointsQuery.refetch()}
                />
              )}
            {endpointsQuery.hasNextPage && (
              <Button
                variant='outline'
                disabled={endpointsQuery.isFetching}
                onClick={() =>
                  void endpointsQuery.fetchNextPage({ cancelRefetch: false })
                }
              >
                {loadNextLabel}
              </Button>
            )}
          </CardContent>
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
        ) : null}
        {endpoint.source !== 'haizhu' && endpoint.subscription_id > 0 ? (
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
          <span className='font-medium text-emerald-600'>
            {endpoint.latency_ms} ms
          </span>
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
            <Badge variant='warning'>
              {t('{{n}} failures', { n: failures })}
            </Badge>
          )}
          {endpoint.last_error && (
            <Badge variant='destructive' className='max-w-[160px] truncate'>
              {endpoint.last_error}
            </Badge>
          )}
        </div>
      </TableCell>
      <TableCell className='text-end'>
        <div className='flex flex-wrap items-center justify-end gap-1'>
          <NodeModelAvailability
            endpointId={endpoint.id}
            subscriptionId={endpoint.subscription_id}
            disabled={props.busy || !endpoint.dialable}
          />
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
  let trafficText = ''
  if (total > 0) {
    trafficText = `${formatBytes(used)} / ${formatBytes(total)}`
  } else if (used > 0) {
    trafficText = formatBytes(used)
  }
  const expireText = subscription.expire_at
    ? new Date(subscription.expire_at).toLocaleDateString()
    : ''

  return (
    <div className='flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2'>
      <div className='min-w-0 space-y-0.5'>
        <div className='flex items-center gap-2'>
          <span className='truncate text-sm font-medium'>
            {subscription.name}
          </span>
          <Badge variant='outline'>{subscription.format || 'auto'}</Badge>
          <Badge variant='secondary'>
            {t('{{n}} IPs', { n: subscription.node_count })}
          </Badge>
          <Badge
            variant={subscription.public ? 'default' : 'outline'}
            className='text-xs'
          >
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
            添加代理 IP
            节点。若不勾选公开，则只有你自己的账户看得见、调得动，别人看不见也用不了。
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
            <span>
              公开到公共池
              IP（勾选后所有人可见可调用；不勾选则仅自己账户可见可用）
            </span>
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
    props.onSubmit({
      name: name.trim(),
      url: url.trim(),
      format,
      public: isPublic,
    })
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('Import subscription')}</DialogTitle>
          <DialogDescription>
            粘贴机场订阅链接，自动过滤流量提示假节点并注入共享 mihomo
            内核。不勾选公开时仅你本人可见可调用。
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
                  event.target.value as 'auto' | 'clash' | 'base64' | 'uri-list'
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
            <span>
              公开到公共池
              IP（勾选后显示在公共池供大家调用；不勾选仅自己账户可见可用）
            </span>
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

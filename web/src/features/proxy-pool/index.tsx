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
  Download,
  Globe,
  KeyRound,
  Plus,
  RefreshCw,
  Rss,
  Trash2,
  Zap,
} from 'lucide-react'
import { useState, type ReactNode } from 'react'
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
  getSummary,
  listEndpoints,
  listSubscriptions,
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
    mutationFn: (payload: { id: number; enabled: boolean }) =>
      updateEndpoint(payload.id, { enabled: payload.enabled }),
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
      toast.success(t('Reachable, exit IP: {{ip}}', { ip: res.data?.ip || '—' }))
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

  let endpointsContent: ReactNode
  if (endpointsQuery.isLoading) {
    endpointsContent = <LoadingState />
  } else if (endpointsQuery.isError) {
    endpointsContent = (
      <ErrorState onRetry={() => void endpointsQuery.refetch()} />
    )
  } else if (endpoints.length === 0) {
    endpointsContent = (
      <EmptyState
        title={t('No egress nodes yet')}
        description={t(
          'Import an airport subscription or paste a residential proxy; every extra exit multiplies your free quota.'
        )}
      />
    )
  } else {
    endpointsContent = (
      <div className='overflow-x-auto rounded-lg border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Name')}</TableHead>
              <TableHead>{t('Protocol')}</TableHead>
              <TableHead>{t('Region')}</TableHead>
              <TableHead>{t('Address')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead className='text-end'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {endpoints.map((endpoint) => (
              <EndpointRow
                key={endpoint.id}
                endpoint={endpoint}
                busy={busyId === `ep-${endpoint.id}` || busyId === `test-${endpoint.id}`}
                testing={busyId === `test-${endpoint.id}`}
                onToggle={(enabled) =>
                  toggleMutation.mutate({ id: endpoint.id, enabled })
                }
                onTest={() => testMutation.mutate(endpoint.id)}
                onDelete={() => deleteEndpointMutation.mutate(endpoint.id)}
              />
            ))}
          </TableBody>
        </Table>
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Egress IP Pool')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='flex flex-wrap items-center gap-2'>
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
            label={t('Usable exits')}
            value={String(summary?.usable_url_count ?? endpoints.filter((e) => e.dialable).length)}
            hint={t('Quota multiplier: {{n}}x', {
              n: summary?.usable_url_count ?? endpoints.filter((e) => e.dialable).length,
            })}
          />
          <StatCard
            icon={<Globe className='h-4 w-4' />}
            label={t('Nodes')}
            value={String(summary?.endpoint_total ?? endpoints.length)}
            hint={t('{{n}} enabled', {
              n: summary?.endpoint_enabled ?? endpoints.filter((e) => e.enabled).length,
            })}
          />
          <StatCard
            icon={<Rss className='h-4 w-4' />}
            label={t('Subscriptions')}
            value={String(summary?.subscription_total ?? subscriptions.length)}
            hint={t('Client-only nodes: {{n}}', {
              n: summary?.client_node_count ?? 0,
            })}
          />
          <StatCard
            icon={<Copy className='h-4 w-4' />}
            label={t('Rotation')}
            value={t('Per request')}
            hint={t('Failing exits are skipped automatically')}
          />
        </div>

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
            <CardTitle className='text-base'>{t('Nodes')}</CardTitle>
            <CardDescription>
              {t(
                'Upstream calls rotate through these exits in round-robin, so a 30 RPM/IP free tier becomes 30 RPM × N.'
              )}
            </CardDescription>
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
  onTest: () => void
  onDelete: () => void
}

function EndpointRow(props: EndpointRowProps) {
  const { t } = useTranslation()
  const endpoint = props.endpoint
  const failures = endpoint.fail_count ?? 0

  return (
    <TableRow>
      <TableCell className='max-w-[220px] truncate font-medium'>
        {endpoint.name}
        {endpoint.subscription_id > 0 && (
          <Badge variant='outline' className='ms-2'>
            {t('From subscription')}
          </Badge>
        )}
      </TableCell>
      <TableCell>
        <Badge variant='secondary'>{kindLabel(endpoint.kind)}</Badge>
      </TableCell>
      <TableCell className='text-muted-foreground text-sm'>
        {endpoint.region || '—'}
      </TableCell>
      <TableCell className='text-muted-foreground max-w-[240px] truncate font-mono text-xs'>
        {endpoint.url || endpoint.raw_uri_prefix || '—'}
      </TableCell>
      <TableCell>
        <div className='flex items-center gap-2'>
          <Switch
            checked={endpoint.enabled}
            onCheckedChange={props.onToggle}
            aria-label={t('Enabled')}
          />
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
            title={
              endpoint.dialable
                ? t('Test this exit')
                : t('Client-only node: run mihomo and use the exported port')
            }
            onClick={props.onTest}
          >
            <KeyRound className='h-4 w-4' />
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

function SubscriptionRow(props: SubscriptionRowProps) {
  const { t } = useTranslation()
  const subscription = props.subscription

  return (
    <div className='flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2'>
      <div className='min-w-0 space-y-0.5'>
        <div className='flex items-center gap-2'>
          <span className='truncate text-sm font-medium'>{subscription.name}</span>
          <Badge variant='outline'>{subscription.format || 'auto'}</Badge>
          <Badge variant='secondary'>
            {t('{{n}} nodes', { n: subscription.node_count })}
          </Badge>
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
  onSubmit: (payload: { name: string; url: string }) => void
}

function AddEndpointDialog(props: AddEndpointDialogProps) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')

  const submit = () => {
    if (!url.trim()) {
      toast.error(t('Proxy URL is required'))
      return
    }
    props.onSubmit({ name: name.trim(), url: url.trim() })
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('Add egress node')}</DialogTitle>
          <DialogDescription>
            {t(
              'HTTP / HTTPS / SOCKS5 proxies work directly. vless, vmess, trojan and ss nodes must be imported as a subscription.'
            )}
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
              placeholder='http://user:pass@host:port'
              onChange={(event) => setUrl(event.target.value)}
            />
          </div>
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
  }) => void
}

function AddSubscriptionDialog(props: AddSubscriptionDialogProps) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [format, setFormat] = useState<
    'auto' | 'clash' | 'base64' | 'uri-list'
  >('auto')

  const submit = () => {
    if (!url.trim()) {
      toast.error(t('Subscription URL is required'))
      return
    }
    props.onSubmit({ name: name.trim(), url: url.trim(), format })
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('Import subscription')}</DialogTitle>
          <DialogDescription>
            {t(
              'Paste an airport subscription link. Clash YAML, base64 and uri-list are all recognised; every node becomes one exit.'
            )}
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

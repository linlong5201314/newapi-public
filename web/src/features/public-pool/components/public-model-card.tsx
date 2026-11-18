import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ProviderBadge } from '@/components/provider-badge'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardFooter, CardHeader } from '@/components/ui/card'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  formatLatency,
  formatThroughput,
} from '@/features/performance-metrics/lib/format'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getLobeIcon } from '@/lib/lobe-icon'

import { STATUS_LABELS } from '../constants'
import {
  channelSamples,
  channelStatus,
  type CatalogModel,
} from '../lib/catalog'
import { ModelDiscussion } from './model-discussion'

export function PublicModelCard(props: { item: CatalogModel }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [selectedChannel, setSelectedChannel] = useState(
    props.item.channels[0]?.channel_id
  )
  const channel =
    props.item.channels.find((entry) => entry.channel_id === selectedChannel) ??
    props.item.channels[0]
  const titleId = useId()
  const sampleCount = channelSamples(channel)
  const status = channelStatus(channel)
  const measured = sampleCount > 0
  const relayLogMetrics =
    channel.metric_source === 'relay_logs_24h' &&
    channel.metric_scope === 'channel_model' &&
    channel.window_seconds === 86400
  const endToEndRate = relayLogMetrics && channel.tps_basis === 'end_to_end'
  const latency = channel.latency_ms ?? channel.avg_latency_ms
  const hasLatency =
    measured &&
    typeof latency === 'number' &&
    Number.isFinite(latency) &&
    latency > 0
  const tps = channel.tps
  const throughput = channel.throughput
  const hasTps =
    measured && typeof tps === 'number' && Number.isFinite(tps) && tps >= 0
  const hasThroughput =
    measured &&
    typeof throughput === 'number' &&
    Number.isFinite(throughput) &&
    throughput >= 0
  let rpmText = t('No samples yet')
  if (hasThroughput) {
    // One request over 24 hours is below the shared formatter's 0.01 precision.
    const rpm =
      throughput > 0 && throughput < 0.01
        ? new Intl.NumberFormat(locale, { maximumSignificantDigits: 3 }).format(
            throughput
          )
        : formatNumber(throughput, locale)
    rpmText = `${rpm} RPM`
  }
  let tpsText = t('No samples yet')
  if (hasTps) {
    tpsText =
      tps === 0 ? `${formatNumber(tps, locale)} t/s` : formatThroughput(tps)
  }
  let latencyWindowText = t('Latency window unavailable')
  if (relayLogMetrics) {
    latencyWindowText = t('Latency: last 24 hours of this model and channel')
  } else if (channel.latency_window === 'accumulated_successful_results') {
    latencyWindowText = t('Latency: accumulated successful channel results')
  }
  const successRate = channel.success_rate
  const hasSuccessRate =
    measured &&
    typeof successRate === 'number' &&
    Number.isFinite(successRate) &&
    successRate >= 0 &&
    successRate <= 100
  const sampledAt = channel.sampled_at
  const hasSampleTime =
    measured &&
    typeof sampledAt === 'number' &&
    Number.isFinite(sampledAt) &&
    sampledAt > 0 &&
    Number.isFinite(new Date(sampledAt * 1000).getTime())
  const owners =
    props.item.source === 'user_shared'
      ? props.item.channels.filter(
          (entry, index, all) =>
            all.findIndex(
              (other) =>
                other.owner_name === entry.owner_name &&
                other.owner_avatar === entry.owner_avatar
            ) === index
        )
      : []

  return (
    <article aria-labelledby={titleId} className='h-full min-w-0'>
      <Card className='hover:ring-foreground/20 h-full min-w-0 gap-3 transition-colors'>
        <CardHeader className='flex flex-row items-start gap-3'>
          <div
            aria-hidden
            className='bg-muted/50 flex size-10 shrink-0 items-center justify-center rounded-lg'
          >
            {props.item.icon ? (
              getLobeIcon(props.item.icon, 28)
            ) : (
              <span className='text-muted-foreground text-sm font-bold'>
                {props.item.model.charAt(0).toUpperCase()}
              </span>
            )}
          </div>
          <div className='min-w-0 flex-1'>
            <h3
              id={titleId}
              className='line-clamp-2 font-mono text-[15px] leading-snug font-semibold [overflow-wrap:anywhere]'
              title={props.item.model}
            >
              {props.item.model}
            </h3>
            <ProviderBadge
              className='mt-1'
              label={props.item.vendor || t('Other providers')}
              colorText={false}
            />
          </div>
          <CopyButton
            value={props.item.model}
            tooltip={t('Copy model name')}
            className='size-7'
            iconClassName='size-3.5'
          />
        </CardHeader>
        <CardContent className='flex min-w-0 flex-1 flex-col gap-3'>
          <div className='flex flex-wrap items-center gap-2'>
            <Badge variant='secondary'>
              {props.item.source === 'platform_free'
                ? t('Platform free')
                : t('User shared')}
            </Badge>
            <span className='text-muted-foreground text-xs'>
              {t('{{count}} channel', {
                count: props.item.channels.length,
                defaultValue: '{{count}} channel',
                defaultValue_other: '{{count}} channels',
              })}
            </span>
          </div>
          <p className='text-muted-foreground text-xs leading-5'>
            {props.item.source === 'platform_free'
              ? t(
                  'Platform-published free channels. Availability and usage limits still apply.'
                )
              : t(
                  'Channels voluntarily published by users. Sharing does not imply unlimited or free usage.'
                )}
          </p>
          {owners.length > 0 && (
            <div
              className='flex min-w-0 flex-wrap gap-2'
              aria-label={t('Shared by')}
            >
              {owners.slice(0, 4).map((owner) => (
                <span
                  key={owner.channel_id}
                  className='inline-flex min-w-0 items-center gap-1.5 text-xs'
                >
                  <Avatar className='size-5'>
                    {owner.owner_avatar && (
                      <AvatarImage src={owner.owner_avatar} alt='' />
                    )}
                    <AvatarFallback>
                      {(owner.owner_name || '?').slice(0, 1).toUpperCase()}
                    </AvatarFallback>
                  </Avatar>
                  <span className='max-w-32 truncate'>
                    {owner.owner_name || t('Anonymous user')}
                  </span>
                </span>
              ))}
              {owners.length > 4 && (
                <span className='text-muted-foreground text-xs'>
                  +{formatNumber(owners.length - 4, locale)}
                </span>
              )}
            </div>
          )}
          <div className='mt-auto min-w-0 space-y-2 border-t pt-3'>
            <label className='flex min-w-0 flex-col gap-1.5 text-xs'>
              <span className='text-muted-foreground'>
                {t('Observed channel metrics')}
              </span>
              <NativeSelect
                className='w-full min-w-0'
                value={channel.channel_id}
                onChange={(event) =>
                  setSelectedChannel(Number(event.target.value))
                }
                aria-label={t('Metric channel')}
              >
                {props.item.channels.map((entry) => (
                  <NativeSelectOption
                    key={entry.channel_id}
                    value={entry.channel_id}
                  >
                    {entry.channel_name}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </label>
            <div className='flex flex-wrap items-center justify-between gap-2 text-xs'>
              <Badge variant='outline'>{t(STATUS_LABELS[status])}</Badge>
              <span className='text-muted-foreground'>
                {measured
                  ? t('{{count}} samples', { count: sampleCount })
                  : t('No samples yet')}
              </span>
            </div>
            <dl
              aria-label={t('Observed channel metrics')}
              className='grid min-w-0 grid-cols-2 gap-x-3 gap-y-3 text-xs sm:grid-cols-3'
            >
              <div className='min-w-0'>
                <dt className='text-muted-foreground'>
                  {t('Request throughput')}
                </dt>
                <dd className='mt-1 font-mono tabular-nums'>{rpmText}</dd>
              </div>
              <div className='min-w-0'>
                <dt className='text-muted-foreground'>
                  {endToEndRate
                    ? t('End-to-end output rate (not generation speed)')
                    : t('Output speed')}
                </dt>
                <dd className='mt-1 font-mono tabular-nums'>{tpsText}</dd>
              </div>
              <div className='min-w-0'>
                <dt className='text-muted-foreground'>
                  {t('Average latency')}
                </dt>
                <dd className='mt-1 font-mono tabular-nums'>
                  {hasLatency ? formatLatency(latency) : t('No samples yet')}
                </dd>
              </div>
              <div className='min-w-0'>
                <dt className='text-muted-foreground'>{t('Success rate')}</dt>
                <dd className='mt-1 font-mono tabular-nums'>
                  {hasSuccessRate
                    ? `${formatNumber(successRate, locale)}%`
                    : t('No samples yet')}
                </dd>
              </div>
            </dl>
            {measured && (
              <div className='text-muted-foreground space-y-1 text-[11px] leading-4'>
                {relayLogMetrics && (
                  <p>{t('Metrics: last 24 hours · this model and channel')}</p>
                )}
                <p>
                  {channel.health_window === 'last_5_results'
                    ? t('Health: last 5 channel results')
                    : t('Health window unavailable')}
                </p>
                {hasLatency && <p>{latencyWindowText}</p>}
                <p>
                  {hasSampleTime
                    ? t('Last sample: {{time}}', {
                        time: new Date(sampledAt * 1000).toLocaleString(locale),
                      })
                    : t('Sample time unavailable')}
                </p>
              </div>
            )}
          </div>
        </CardContent>
        <CardFooter className='border-0 bg-transparent pt-0'>
          <ModelDiscussion
            model={props.item.model}
            channelId={channel.channel_id}
          />
        </CardFooter>
      </Card>
    </article>
  )
}

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { PublicModelCatalog } from '../components/public-model-catalog'
import { PublicPoolPage } from '../index'

const channel = (id: number, source: string, extra = {}) => ({
  channel_id: id,
  channel_name: `channel-${id}`,
  owner_name: source === 'user_shared' ? 'Alice' : '',
  owner_avatar: '',
  tier: 'free',
  response_time: 0,
  source_kind: source,
  provider: 'OpenAI',
  enabled: true,
  status: 'unknown',
  sample_count: 0,
  throughput: null,
  tps: null,
  latency_ms: null,
  ...extra,
})
const model = (
  name = 'gpt-4.1',
  channels = [channel(1, 'platform_free'), channel(2, 'user_shared')]
) => ({
  model: name,
  providers: 2,
  free_count: 2,
  fastest_ms: -1,
  rating: 0,
  comments: 0,
  owners: [{ user_id: 8, name: 'Alice', avatar: '' }],
  channels,
})

function mountCatalog() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <PublicModelCatalog />
    </QueryClientProvider>
  )
}

afterEach(() => vi.restoreAllMocks())

describe('Public model catalog', () => {
  it('splits the same model by actual channel source without leaking owners or provider totals', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [model()] } },
    })
    mountCatalog()
    const platform = await screen.findByRole('region', {
      name: 'Platform free models',
    })
    const shared = screen.getByRole('region', { name: 'User-shared models' })
    expect(
      within(platform).getByRole('heading', { name: 'gpt-4.1' })
    ).toBeVisible()
    expect(
      within(shared).getByRole('heading', { name: 'gpt-4.1' })
    ).toBeVisible()
    expect(within(platform).queryByText('Alice')).not.toBeInTheDocument()
    expect(within(shared).getByText('Alice')).toBeVisible()
    expect(within(platform).getByText('1 channel')).toBeVisible()
    expect(within(shared).getByText('1 channel')).toBeVisible()
  })

  it('does not turn unsampled metrics, enabled channels, or proxy IP counts into healthy AI models', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [model()] } },
    })
    mountCatalog()
    const platform = await screen.findByRole('region', {
      name: 'Platform free models',
    })
    expect(
      within(platform).getAllByText('No samples yet').length
    ).toBeGreaterThanOrEqual(3)
    expect(within(platform).getByText('Unknown status')).toBeVisible()
    expect(
      screen.queryByText(/0 ?ms|unlimited tokens|7114/i)
    ).not.toBeInTheDocument()
    expect(get).toHaveBeenCalledWith(
      '/api/public_pool/models',
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('shows measured values only with samples and a channel-scoped observation label', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            model('gpt-4.1', [
              channel(1, 'platform_free', {
                sample_count: 12,
                throughput: 3,
                tps: 24,
                latency_ms: 1600,
                status: 'healthy',
              }),
            ]),
          ],
        },
      },
    })
    mountCatalog()
    const card = await screen.findByRole('article', { name: 'gpt-4.1' })
    expect(within(card).getByText('1.60s')).toBeVisible()
    expect(within(card).getByText('24.0 t/s')).toBeVisible()
    expect(within(card).getByText('3 RPM')).toBeVisible()
    expect(within(card).getByText('12 samples')).toBeVisible()
    expect(within(card).getByText('Observed channel metrics')).toBeVisible()
  })

  it('keeps independent source empty states and filters at the top with clear keyboard-operable controls', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            model('claude-sonnet-4', [
              channel(2, 'user_shared', { provider: 'Anthropic' }),
            ]),
          ],
        },
      },
    })
    mountCatalog()
    expect(
      await screen.findByText('No platform free models are published.')
    ).toBeVisible()
    expect(
      screen.getByRole('heading', { name: 'claude-sonnet-4' })
    ).toBeVisible()
    const search = screen.getByRole('textbox', { name: /search/i })
    await user.type(search, 'not-a-model')
    expect(
      screen.getByText('No user-shared models match these filters.')
    ).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Reset filters' }))
    expect(search).toHaveValue('')
    const sources = screen.getByRole('tablist', { name: 'Model source' })
    const platform = within(sources).getByRole('tab', { name: 'Platform free' })
    await user.click(platform)
    expect(platform).toHaveAttribute('aria-selected', 'true')
    expect(
      screen.queryByRole('region', { name: 'User-shared models' })
    ).not.toBeInTheDocument()
    await user.keyboard('{ArrowRight}{Enter}')
    expect(
      within(sources).getByRole('tab', { name: 'User shared' })
    ).toHaveAttribute('aria-selected', 'true')
  })

  it('never silently classifies legacy unknown-source channels as platform free', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: { items: [model('legacy-model', [channel(9, '')])] },
      },
    })
    mountCatalog()
    expect(
      await screen.findByText(
        'Some channels have no verified source and are not included in either catalog.'
      )
    ).toBeVisible()
    expect(
      screen.queryByRole('heading', { name: 'legacy-model' })
    ).not.toBeInTheDocument()
  })

  it('renders API business failure as retryable error rather than successful empty state', async () => {
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValueOnce({
        data: { success: false, message: 'Catalog unavailable' },
      })
      .mockResolvedValue({ data: { success: true, data: { items: [] } } })
    mountCatalog()
    expect(
      await screen.findByText('Unable to load public models')
    ).toBeVisible()
    expect(
      screen.queryByText('No platform free models are published.')
    ).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(
      await screen.findByText('No platform free models are published.')
    ).toBeVisible()
    expect(get).toHaveBeenCalledTimes(2)
  })
})

describe('Catalog observation and filter boundaries', () => {
  it('labels last-five health separately from accumulated latency and never invents a sample timestamp', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            model('gpt-4.1', [
              channel(1, 'platform_free', {
                sample_count: 5,
                status: 'ok',
                latency_ms: 1600,
                success_rate: 80,
                metric_scope: 'channel',
                health_window: 'last_5_results',
                latency_window: 'accumulated_successful_results',
                sampled_at: null,
              }),
            ]),
          ],
        },
      },
    })
    mountCatalog()
    const card = await screen.findByRole('article', { name: 'gpt-4.1' })
    expect(
      within(card).getByText('Health: last 5 channel results')
    ).toBeVisible()
    expect(
      within(card).getByText('Latency: accumulated successful channel results')
    ).toBeVisible()
    expect(within(card).getByText('Sample time unavailable')).toBeVisible()
    expect(within(card).getByText('80%')).toBeVisible()
  })

  it('lets users switch measured channels without mixing metrics or owner attribution across sources', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            model('gpt-4.1', [
              channel(1, 'platform_free', {
                sample_count: 2,
                latency_ms: 1200,
              }),
              channel(2, 'platform_free', {
                sample_count: null,
                samples: 0,
                latency_ms: 900,
                tps: 99,
              }),
            ]),
          ],
        },
      },
    })
    mountCatalog()
    const card = await screen.findByRole('article', { name: 'gpt-4.1' })
    expect(within(card).getByText('1.20s')).toBeVisible()
    fireEvent.change(
      within(card).getByRole('combobox', { name: 'Metric channel' }),
      { target: { value: '2' } }
    )
    expect(within(card).queryByText('1.20s')).not.toBeInTheDocument()
    expect(within(card).queryByText('900ms')).not.toBeInTheDocument()
    expect(within(card).queryByText('99.0 t/s')).not.toBeInTheDocument()
    expect(
      within(card).getAllByText('No samples yet').length
    ).toBeGreaterThanOrEqual(3)
  })

  it('filters by provider and observed status and resets the source filter too', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            model('gpt-4.1', [
              channel(1, 'platform_free', { sample_count: 2, status: 'down' }),
            ]),
            model('claude-sonnet-4', [
              channel(2, 'user_shared', { provider: 'Anthropic' }),
            ]),
          ],
        },
      },
    })
    mountCatalog()
    await screen.findByRole('heading', { name: 'gpt-4.1' })
    fireEvent.change(screen.getByRole('combobox', { name: 'Provider' }), {
      target: { value: 'Anthropic' },
    })
    expect(
      screen.queryByRole('heading', { name: 'gpt-4.1' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('heading', { name: 'claude-sonnet-4' })
    ).toBeVisible()
    fireEvent.change(screen.getByRole('combobox', { name: 'Provider' }), {
      target: { value: 'all' },
    })
    fireEvent.change(screen.getByRole('combobox', { name: 'Status' }), {
      target: { value: 'down' },
    })
    expect(
      screen.queryByRole('heading', { name: 'claude-sonnet-4' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'Platform free' }))
    await user.click(screen.getByRole('button', { name: 'Reset filters' }))
    expect(screen.getByRole('tab', { name: 'All sources' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    expect(
      screen.getByRole('heading', { name: 'claude-sonnet-4' })
    ).toBeVisible()
  })

  it('keeps filters before results and uses responsive non-overflowing card grids for long model names', async () => {
    const longName = 'custom-model-'.repeat(30)
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [model(longName)] } },
    })
    mountCatalog()
    const region = await screen.findByRole('region', {
      name: 'Platform free models',
    })
    const heading = within(region).getByRole('heading', { name: longName })
    const filters = screen.getByRole('search', { name: 'Public model filters' })
    expect(
      filters.compareDocumentPosition(region) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    expect(heading).toHaveClass('[overflow-wrap:anywhere]')
    expect(within(region).getByRole('article', { name: longName })).toHaveClass(
      'min-w-0'
    )
    expect(heading.closest('article')?.parentElement).toHaveClass(
      'grid-cols-1',
      'md:grid-cols-2',
      'xl:grid-cols-3'
    )
  })
})

describe('Public pool entry boundary', () => {
  it('shows loading and independently completes the catalog without fetching admin, profile, or proxy APIs', async () => {
    let resolve: ((value: unknown) => void) | undefined
    const response = new Promise((done) => {
      resolve = done
    })
    const get = vi
      .spyOn(api, 'get')
      .mockImplementation(() => response as ReturnType<typeof api.get>)
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <PublicPoolPage />
      </QueryClientProvider>
    )
    expect(
      screen.getByRole('status', { name: 'Loading public models' })
    ).toHaveAttribute('aria-busy', 'true')
    resolve?.({ data: { success: true, data: { items: [] } } })
    expect(
      await screen.findByText('No user-shared models are published.')
    ).toBeVisible()
    expect(get).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledWith(
      '/api/public_pool/models',
      expect.any(Object)
    )
  })

  it('uses the model brand rather than an OpenAI-compatible channel protocol as its vendor icon', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            model('claude-sonnet-4', [
              channel(1, 'platform_free', { provider: 'OpenAI' }),
            ]),
          ],
        },
      },
    })
    mountCatalog()
    const card = await screen.findByRole('article', { name: 'claude-sonnet-4' })
    expect(within(card).getByText('Anthropic')).toBeVisible()
    expect(within(card).getByTitle('Claude')).toBeInTheDocument()
  })
})

describe('Public channel-model relay log metrics', () => {
  it('labels actual last-24-hour pair metrics and end-to-end rate without mixing channel health scope', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            model('gpt-4.1', [
              channel(1, 'platform_free', {
                metric_source: 'relay_logs_24h',
                metric_scope: 'channel_model',
                window_seconds: 86400,
                tps_basis: 'end_to_end',
                sample_count: 720,
                throughput: 0.5,
                tps: 24,
                latency_ms: 1600,
                sampled_at: 1795046400,
                health_scope: 'channel',
                health_window: 'last_5_results',
                samples: 5,
                success_rate: 80,
                status: 'healthy',
                latency_window: 'last_24_hours',
              }),
            ]),
          ],
        },
      },
    })
    mountCatalog()
    const card = await screen.findByRole('article', { name: 'gpt-4.1' })
    expect(
      within(card).getByText('Metrics: last 24 hours · this model and channel')
    ).toBeVisible()
    expect(
      within(card).getByText('End-to-end output rate (not generation speed)')
    ).toBeVisible()
    expect(
      within(card).getByText('Latency: last 24 hours of this model and channel')
    ).toBeVisible()
    expect(within(card).getByText('0.5 RPM')).toBeVisible()
    expect(within(card).getByText('24.0 t/s')).toBeVisible()
    expect(within(card).getByText('1.60s')).toBeVisible()
    expect(within(card).getByText('720 samples')).toBeVisible()
    expect(
      within(card).getByText('Health: last 5 channel results')
    ).toBeVisible()
    expect(within(card).getByText('80%')).toBeVisible()
    expect(within(card).getByText(/Last sample:/)).toBeVisible()
    expect(
      within(card).queryByText('Sample time unavailable')
    ).not.toBeInTheDocument()
    expect(
      within(card).queryByText(
        'Latency: accumulated successful channel results'
      )
    ).not.toBeInTheDocument()
  })

  it('preserves a real low-volume RPM and zero end-to-end token rate rather than treating either as missing', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            model('gpt-4.1', [
              channel(1, 'platform_free', {
                metric_source: 'relay_logs_24h',
                metric_scope: 'channel_model',
                window_seconds: 86400,
                tps_basis: 'end_to_end',
                sample_count: 1,
                throughput: 1 / 1440,
                tps: 0,
                latency_ms: null,
                sampled_at: 1795046400,
                samples: null,
                success_rate: null,
              }),
            ]),
          ],
        },
      },
    })
    mountCatalog()
    const card = await screen.findByRole('article', { name: 'gpt-4.1' })
    expect(within(card).getByText('0.000694 RPM')).toBeVisible()
    expect(within(card).getByText('0 t/s')).toBeVisible()
    expect(within(card).getByText('Unknown status')).toBeVisible()
    expect(within(card).getAllByText('No samples yet')).toHaveLength(2)
  })

  it('retains channel-health provenance when no model-specific relay log measurement exists', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            model('gpt-4.1', [
              channel(1, 'platform_free', {
                metric_source: null,
                metric_scope: 'channel',
                window_seconds: null,
                tps_basis: null,
                health_scope: 'channel',
                samples: 5,
                sample_count: 5,
                latency_ms: 1600,
                health_window: 'last_5_results',
                latency_window: 'accumulated_successful_results',
              }),
            ]),
          ],
        },
      },
    })
    mountCatalog()
    const card = await screen.findByRole('article', { name: 'gpt-4.1' })
    expect(
      within(card).getByText('Latency: accumulated successful channel results')
    ).toBeVisible()
    expect(
      within(card).queryByText(
        'Metrics: last 24 hours · this model and channel'
      )
    ).not.toBeInTheDocument()
    expect(
      within(card).queryByText('End-to-end output rate (not generation speed)')
    ).not.toBeInTheDocument()
  })
})

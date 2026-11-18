export const PUBLIC_CATALOG_QUERY_KEY = ['public-pool', 'models'] as const

export const STATUS_LABELS = {
  available: 'Observed available',
  degraded: 'Degraded',
  down: 'Unavailable',
  disabled: 'Disabled',
  unknown: 'Unknown status',
} as const

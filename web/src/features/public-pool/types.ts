export interface PublicOwner {
  user_id: number
  name: string
  avatar: string
}

export type PublicModelSource = 'platform_free' | 'user_shared'

export interface PublicChannelView {
  channel_id: number
  channel_name: string
  owner_name: string
  owner_avatar: string
  tier: string
  response_time: number | null
  // Missing source during rolling upgrades must never imply platform ownership.
  source_kind?: PublicModelSource | null
  provider?: string | null
  enabled?: boolean
  status?: string | null
  health_state?: string | null
  success_rate?: number | null
  avg_latency_ms?: number | null
  samples?: number | null
  metric_scope?: 'channel' | 'channel_model' | null
  metric_source?: 'relay_logs_24h' | null
  window_seconds?: number | null
  tps_basis?: 'end_to_end' | null
  health_scope?: 'channel' | null
  health_window?: string | null
  latency_window?: string | null
  latency_source?: string | null
  sampled_at?: number | null
  sample_count?: number | null
  throughput?: number | null
  tps?: number | null
  latency_ms?: number | null
}

export interface PublicModelItem {
  model: string
  providers: number
  free_count: number
  fastest_ms: number | null
  rating: number
  comments: number
  owners: PublicOwner[]
  channels: PublicChannelView[]
}

export interface PublicComment {
  id: number
  model: string
  channel_id: number
  channel_name: string
  user_id: number
  username: string
  display_name: string
  avatar: string
  content: string
  rating: number
  created_at: number
}

export interface MyChannelRow {
  id: number
  name: string
  visibility?: number
  tier?: string
  models?: string
  status?: number
  allow_ips?: string
}

export interface PlatformChannelRow {
  id: number
  name: string
  models: string
  group: string
  status: number
  visibility: number
  tier: string
  allow_ips: string
}

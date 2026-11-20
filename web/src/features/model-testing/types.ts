export type ModelProbeStatus = 'unknown' | 'available' | 'unavailable'

export type ModelProbeResult = {
  channel_id: number
  model: string
  endpoint_id: number
  subscription_id: number
  status: ModelProbeStatus
  reason: string
  http_status: number
  latency_ms: number
  checked_at: number
  expires_at: number
  first_checked_at: number
  last_available_at: number
  lifetime_samples: number
  lifetime_passes: number
  lifetime_failures: number
  recent_sample_count: number
  recent_passes: number
  recent_failures: number
}

export type ModelProbeEndpoint = {
  id: number
  subscription_id: number
  public: boolean
  status: ModelProbeStatus
}

export type ModelProbeOptions = {
  channel_id: number
  endpoints: ModelProbeEndpoint[]
  results: ModelProbeResult[]
  endpoint_has_more?: boolean
  endpoint_next_offset?: number
  configured_egress_only?: boolean
  max_models: number
  max_endpoints: number
  max_output_tokens: number
}

export type ModelProbeBatch = {
  channel_id: number
  results: ModelProbeResult[]
  passed_models: string[]
}

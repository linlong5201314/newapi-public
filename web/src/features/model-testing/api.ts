import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { ModelProbeBatch, ModelProbeOptions } from './types'

type ProbeResponse<T> = { success: boolean; message?: string; data: T }

export async function getModelProbeOptions(
  channelId: number,
  models: readonly string[],
  signal?: AbortSignal,
  endpointOffset = 0,
  endpointIds: readonly number[] = []
): Promise<ModelProbeOptions> {
  const response = await api.get<ProbeResponse<ModelProbeOptions>>(
    `/api/channel/self/model_probe/${channelId}`,
    {
      params: {
        models: models.slice(0, 3).join(','),
        endpoint_offset: endpointOffset,
        endpoint_limit: 50,
        endpoint_ids: endpointIds.length ? endpointIds.join(',') : undefined,
      },
      signal,
      headers: { 'X-Account-Action': 'authorize' },
    }
  )
  return requireServerSuccess(response.data).data
}

export async function testModelEndpoints(
  channelId: number,
  models: readonly string[],
  endpointIds: readonly number[],
  signal?: AbortSignal
): Promise<ModelProbeBatch> {
  const response = await api.post<ProbeResponse<ModelProbeBatch>>(
    `/api/channel/self/model_probe/${channelId}`,
    {
      models,
      endpoint_ids: endpointIds,
    },
    { signal, timeout: 130000, headers: { 'X-Account-Action': 'authorize' } }
  )
  return requireServerSuccess(response.data).data
}

/** Only selected, fresh successes from this explicit run can be enabled. */
export function selectedPassingModels(
  batch: ModelProbeBatch | undefined,
  selectedModels: readonly string[],
  now = Date.now()
): string[] {
  if (!batch) return []
  return selectedModels.filter((model) => {
    const results = batch.results.filter((result) => result.model === model)
    return (
      batch.passed_models.includes(model) &&
      results.length > 0 &&
      results.every(
        (result) =>
          result.status === 'available' &&
          result.reason === 'passed' &&
          result.checked_at > 0 &&
          result.expires_at * 1000 > now
      )
    )
  })
}

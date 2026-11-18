import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { getSummary, listEndpoints, listSubscriptions } from '../api'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn() } }))

beforeEach(() => vi.resetAllMocks())

describe('proxy pool queries', () => {
  it.each([getSummary, listEndpoints, listSubscriptions])(
    'rejects a failed business response instead of returning missing data',
    async (query) => {
      vi.mocked(api.get).mockResolvedValue({
        data: { success: false, message: 'Proxy pool schema unavailable' },
      })
      await expect(query()).rejects.toThrow('Proxy pool schema unavailable')
    }
  )

  it('preserves a successful summary', async () => {
    const data = {
      endpoint_total: 7,
      endpoint_enabled: 3,
      usable_url_count: 2,
      subscription_total: 1,
      client_node_count: 4,
    }
    vi.mocked(api.get).mockResolvedValue({ data: { success: true, data } })
    await expect(getSummary()).resolves.toEqual(data)
  })

  it.each([listEndpoints, listSubscriptions])(
    'normalizes an empty successful list to an empty array',
    async (query) => {
      vi.mocked(api.get).mockResolvedValue({
        data: { success: true, data: null },
      })
      await expect(query()).resolves.toEqual([])
    }
  )
})

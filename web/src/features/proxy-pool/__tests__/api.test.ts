import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  getSummary,
  listEndpointPage,
  listEndpoints,
  listSubscriptions,
} from '../api'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn() } }))

beforeEach(() => vi.resetAllMocks())

describe('proxy pool queries', () => {
  it.each([getSummary, listEndpointPage, listEndpoints, listSubscriptions])(
    'rejects a failed business response instead of returning missing data',
    async (query) => {
      vi.mocked(api.get).mockResolvedValue({
        data: { success: false, message: 'Proxy pool schema unavailable' },
      })
      await expect(query()).rejects.toThrow('Proxy pool schema unavailable')
    }
  )

  it('requests the next 600 endpoints and preserves top-level pagination', async () => {
    const data = [{ id: 601, name: 'Next inventory node' }]
    const pagination = { offset: 600, limit: 600, returned: 1, has_more: false }
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data, pagination },
    })

    await expect(listEndpointPage(600)).resolves.toEqual({ data, pagination })
    expect(api.get).toHaveBeenCalledWith('/api/proxy-pool/endpoints', {
      params: { offset: 600, limit: 600 },
    })
  })

  it('keeps listEndpoints array-shaped when the server includes pagination', async () => {
    const data = [{ id: 1, name: 'Selector node' }]
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data,
        pagination: { offset: 0, limit: 600, returned: 1, has_more: false },
      },
    })

    await expect(listEndpoints()).resolves.toEqual(data)
    expect(api.get).toHaveBeenCalledWith('/api/proxy-pool/endpoints')
  })

  it('normalizes an empty page without requesting more inventory', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: null },
    })

    await expect(listEndpointPage()).resolves.toEqual({
      data: [],
      pagination: { offset: 0, limit: 600, returned: 0, has_more: false },
    })
  })

  it('treats an older response without pagination as a single page', async () => {
    const data = [{ id: 1, name: 'Legacy inventory node' }]
    vi.mocked(api.get).mockResolvedValue({ data: { success: true, data } })

    await expect(listEndpointPage()).resolves.toEqual({
      data,
      pagination: { offset: 0, limit: 600, returned: 1, has_more: false },
    })
  })

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

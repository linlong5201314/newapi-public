import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  getAllModels,
  getChannelDefaultBaseURLs,
  getChannelDetail,
  getPrefillGroups,
  getTaskPluginOptions,
} from '../api'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn() } }))
beforeEach(() => vi.resetAllMocks())

describe('owner channel metadata', () => {
  it('uses the UserAuth model catalog rather than the administrator channel catalog', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data: { 1: ['deepseek-chat', 'qwen-coder'], 2: ['deepseek-chat'] },
      },
    })
    await expect(getAllModels()).resolves.toEqual({
      success: true,
      data: [{ id: 'deepseek-chat' }, { id: 'qwen-coder' }],
    })
    expect(api.get).toHaveBeenCalledWith('/api/models')
  })

  it('does not probe administrator-only default URLs, prefill groups or plugin metadata', async () => {
    await expect(getChannelDefaultBaseURLs()).resolves.toBeNull()
    await expect(getPrefillGroups()).resolves.toEqual({
      success: true,
      data: [],
    })
    await expect(getTaskPluginOptions()).resolves.toEqual([])
    expect(api.get).not.toHaveBeenCalled()
  })

  it('loads only the current owner detail route', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: { models: ['deepseek-chat'] } },
    })
    await getChannelDetail(7)
    expect(api.get).toHaveBeenCalledWith('/api/channel/self/7/detail')
  })
})

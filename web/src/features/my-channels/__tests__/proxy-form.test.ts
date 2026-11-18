import { describe, expect, it } from 'vitest'

import {
  buildSettingJSON,
  transformChannelToFormDefaults,
} from '../lib/channel-form'
import { channelSchema } from '../types'

describe('owner channel proxy form', () => {
  it.each([
    { proxy: 'direct', custom_setting: 'preserved' },
    {
      proxy: 'direct',
      proxy_policy: { mode: 'direct' },
      custom_setting: 'preserved',
    },
    { proxy_policy: { mode: 'public_pool' }, custom_setting: 'preserved' },
  ])(
    'preserves the exact legacy route while editing an unrelated field: %j',
    (setting) => {
      const channel = channelSchema.parse({
        id: 7,
        name: 'Legacy source',
        type: 1,
        models: 'deepseek-chat',
        key: '',
        status: 1,
        created_time: 0,
        test_time: 0,
        response_time: 0,
        balance_updated_time: 0,
        setting: JSON.stringify(setting),
      })
      const form = transformChannelToFormDefaults(channel)
      form.name = 'Renamed source'
      const saved = JSON.parse(buildSettingJSON(form))
      expect(saved.proxy).toBe(setting.proxy)
      expect(saved.proxy_policy).toEqual(setting.proxy_policy)
      expect(saved.custom_setting).toBe('preserved')
    }
  )

  it('keeps a subscription policy and unrelated settings when another channel field is edited', () => {
    const channel = channelSchema.parse({
      id: 7,
      name: 'Own source',
      type: 1,
      models: 'deepseek-chat',
      key: '',
      status: 1,
      created_time: 0,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      setting: JSON.stringify({
        proxy_policy: { mode: 'subscription', subscription_id: 9 },
        custom_setting: 'preserved',
        proxy: '',
      }),
    })
    const data = transformChannelToFormDefaults(channel)
    expect(JSON.parse(buildSettingJSON(data))).toMatchObject({
      proxy_policy: { mode: 'subscription', subscription_id: 9 },
      custom_setting: 'preserved',
    })
  })
})

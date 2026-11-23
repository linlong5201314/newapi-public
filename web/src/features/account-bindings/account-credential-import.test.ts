import { describe, expect, it } from 'vitest'

import { parseSingleAccountImport } from './account-credential-import'

const trae = {
  type: 'oauth',
  access: 'access-token',
  refresh: 'refresh-token',
  uid: '1554089915735664',
  clientId: 'ono9krqynydwx5',
  machineId: 'machine',
  deviceId: 'device',
  expires: 1796198723245,
  accountId: 'example',
}

describe('desktop and Magpie credential imports', () => {
  it('lists Trae accounts without submitting the whole library', () => {
    const accounts = parseSingleAccountImport('trae-cn', {
      'trae-cn': trae,
      'trae-cn#other': { ...trae, accountId: 'other', uid: '2' },
      raccoon: { access: 'other-access', refresh: 'other-refresh' },
    })
    expect(accounts.map((item) => item.id)).toEqual(['trae-cn', 'trae-cn#other'])
    expect(accounts[0].value).toMatchObject({
      accessToken: 'access-token',
      refreshToken: 'refresh-token',
      expiresAt: 1796198723,
    })
    expect(JSON.stringify(accounts)).not.toContain('other-access')
  })

  it('accepts one Raccoon desktop JSON object', () => {
    const accounts = parseSingleAccountImport('raccoon', {
      access_token: 'access-token',
      refresh_token: 'refresh-token',
      accountId: 'raccoon-user',
    })
    expect(accounts).toHaveLength(1)
    expect(accounts[0].value).toEqual({
      accessToken: 'access-token',
      refreshToken: 'refresh-token',
    })
  })

  it('does not silently pick an unrelated provider', () => {
    expect(parseSingleAccountImport('raccoon', { 'trae-cn': trae })).toEqual([])
  })
})

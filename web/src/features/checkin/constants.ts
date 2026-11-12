/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

// ============================================================================
// [M3-FE] Check-in Center constants
// Label values are i18n keys; use t(value) when displaying.
// ============================================================================

import type { CheckinSiteTemplate } from './types'

export const checkinQueryKeys = {
  all: ['checkin'] as const,
  sites: () => [...checkinQueryKeys.all, 'sites'] as const,
  accounts: () => [...checkinQueryKeys.all, 'accounts'] as const,
  logs: (accountId: number) =>
    [...checkinQueryKeys.all, 'logs', accountId] as const,
}

export const CHECKIN_MESSAGES = {
  LOAD_ACCOUNTS_FAILED: 'Failed to load check-in accounts',
  LOAD_LOGS_FAILED: 'Failed to load check-in logs',
  CREATE_FAILED: 'Failed to add check-in account',
  UPDATE_FAILED: 'Failed to update check-in account',
  DELETE_FAILED: 'Failed to delete check-in account',
  RUN_FAILED: 'Failed to run check-in',
  CREATED: 'Check-in account added',
  DELETED: 'Check-in account deleted',
  RUN_STARTED: 'Check-in completed',
} as const

/**
 * Built-in fallback site templates.
 *
 * The wizard renders GET /api/checkin/sites when the backend is available;
 * these defaults keep the page usable (fail-soft) while the backend agent is
 * still landing the endpoints. Mirror of design doc §3.2.
 */
export const CHECKIN_FALLBACK_SITES: CheckinSiteTemplate[] = [
  {
    site: 'workbuddy',
    name: 'WorkBuddy',
    icon: 'briefcase',
    mode: 'api',
    credFields: [],
    guide:
      'Daily check-in plus streak-manager loop (resign cards, gifts, compensation, streak exchange, lottery) and task sweep. References one of your WorkBuddy channels.',
  },
  {
    site: 'trae',
    name: 'Trae',
    icon: 'sparkles',
    mode: 'api',
    credFields: [],
    guide:
      'Daily check-in claim (+200/day) and balance sync onto the referenced channel. References one of your Trae channels.',
  },
  {
    site: 'qoder',
    name: 'Qoder',
    icon: 'qoder',
    mode: 'api',
    credFields: [
      {
        key: 'token',
        label: 'Qoder desktop token',
        type: 'password',
        required: true,
        placeholder: 'auth.v1.dat 解密后的 token',
        help: 'Qoder CN 桌面端登录态文件 auth.v1.dat 解密后的 token 字段。',
      },
      {
        key: 'baseUrl',
        label: 'API base URL',
        type: 'text',
        required: false,
        placeholder: 'https://openapi.qoder.com.cn',
        help: '国际版账号填 https://openapi.qoder.com。',
      },
      {
        key: 'machineId',
        label: 'Machine ID',
        type: 'text',
        required: false,
        help: '可选设备标识（Cosy-MachineId）。',
      },
    ],
    guide:
      '每日 100 通用 Credits（UTC+8 10:00 刷新，无补领）。token 取自 Qoder 桌面端登录态；领取幂等，已领过会返回 skipped。',
  },
  {
    site: 'apinex',
    name: 'Apinex',
    icon: 'cloud',
    mode: 'agent',
    credFields: [
      {
        key: 'cookie',
        label: 'Cookie',
        type: 'textarea',
        required: true,
        placeholder: 'session=...; ...',
        help: 'Full cookie string captured after logging in to Apinex.',
      },
    ],
    guide:
      'Posts to the configured Agent executor URL with your label; the result callback keeps the cookie alive.',
  },
  {
    site: 'custom',
    name: 'Custom',
    icon: 'wrench',
    mode: 'api',
    credFields: [
      {
        key: 'endpoint',
        label: 'Endpoint',
        type: 'text',
        required: true,
        placeholder: 'https://example.com/api/checkin',
      },
      {
        key: 'method',
        label: 'HTTP Method',
        type: 'select',
        required: true,
        options: [
          { value: 'GET', label: 'GET' },
          { value: 'POST', label: 'POST' },
        ],
      },
      {
        key: 'request_template',
        label: 'Request Template',
        type: 'textarea',
        required: false,
        placeholder: '{"headers": {"Authorization": "Bearer ..."}}',
        help: 'Optional JSON request template (headers/body).',
      },
      {
        key: 'success_rule',
        label: 'Success Rule (JSONPath)',
        type: 'text',
        required: true,
        placeholder: '$.success == true',
        help: 'JSONPath/expression evaluated against the response.',
      },
    ],
    guide:
      'Generic template: endpoint, method, request template and a success rule so you can wire any site.',
  },
]

/** Sites that reference an existing own channel instead of standalone creds. */
export const CHECKIN_CHANNEL_SITES = new Set(['workbuddy', 'trae'])

/** Field keys that carry secrets; rendered as password inputs. */
export const CHECKIN_SECRET_FIELD_KEYS = new Set([
  'cookie',
  'token',
  'secret',
  'authorization',
  'refresh_token',
])

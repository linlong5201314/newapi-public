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
// [M5-FE] Node API normalization helpers
//
// The M5 backend contract (§7) is the source of truth, but the backend may
// land after this frontend. Every normalizer therefore accepts:
//   - snake_case and camelCase field names,
//   - a full `{success, data}` envelope or the inner payload,
//   - `balance_info` / `targets` / `actions` encoded as JSON strings.
// A malformed payload degrades to an empty plan instead of crashing a dialog.
// ============================================================================

import type {
  DeployAction,
  DeployActionOp,
  DeployApplyResult,
  DeployPlan,
  DeployStatus,
  FailoverKvChange,
  FailoverPlan,
  ManagedNode,
  NodeAuditLog,
  NodeBalance,
  NodeProvider,
  NodeSettings,
  NodeTestResult,
} from '../types'

export const nodesQueryKeys = {
  all: ['nodes'] as const,
  lists: () => [...nodesQueryKeys.all, 'list'] as const,
  details: () => [...nodesQueryKeys.all, 'detail'] as const,
  detail: (id: number) => [...nodesQueryKeys.details(), id] as const,
  settings: () => [...nodesQueryKeys.all, 'settings'] as const,
  audit: (nodeId: number) => [...nodesQueryKeys.all, 'audit', nodeId] as const,
  deployStatus: (nodeId: number) =>
    [...nodesQueryKeys.all, 'deploy-status', nodeId] as const,
}

export function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

export function asArray(value: unknown): unknown[] {
  if (Array.isArray(value)) return value
  if (typeof value === 'string' && value.trim()) {
    try {
      const parsed: unknown = JSON.parse(value)
      return Array.isArray(parsed) ? parsed : []
    } catch {
      return []
    }
  }
  return []
}

function firstValue(
  record: Record<string, unknown>,
  keys: readonly string[]
): unknown {
  for (const key of keys) {
    if (record[key] !== undefined && record[key] !== null) {
      return record[key]
    }
  }
  return undefined
}

export function toText(value: unknown): string {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value)
  }
  try {
    return JSON.stringify(value)
  } catch {
    return ''
  }
}

function toNumber(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : null
  }
  return null
}

export function toBoolean(value: unknown): boolean {
  if (typeof value === 'boolean') return value
  if (typeof value === 'number') return value === 1
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase()
    return (
      normalized === '1' ||
      normalized === 'true' ||
      normalized === 'yes' ||
      normalized === 'healthy' ||
      normalized === 'online' ||
      normalized === 'up'
    )
  }
  return false
}

function toStringArray(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value
      .map((item) => toText(item).trim())
      .filter((item) => item.length > 0)
  }
  if (typeof value === 'string' && value.trim()) {
    try {
      const parsed: unknown = JSON.parse(value)
      if (Array.isArray(parsed)) {
        return parsed
          .map((item) => toText(item).trim())
          .filter((item) => item.length > 0)
      }
    } catch {
      return value
        .split(',')
        .map((item) => item.trim())
        .filter(Boolean)
    }
  }
  return []
}

function parseJsonRecord(value: unknown): Record<string, unknown> | null {
  const direct = asRecord(value)
  if (direct) return direct
  if (typeof value === 'string' && value.trim()) {
    try {
      return asRecord(JSON.parse(value))
    } catch {
      return null
    }
  }
  return null
}

function normalizeBalance(value: unknown): NodeBalance | null {
  const record = parseJsonRecord(value)
  if (!record) return null
  const remaining = toNumber(
    firstValue(record, [
      'remaining',
      'balance',
      'creditBalance',
      'credit_balance',
      'remainingUsageCreditBalance',
      'remaining_usage_credit_balance',
    ])
  )
  const currency = toText(
    firstValue(record, ['currency', 'unit', 'unitName', 'unit_name'])
  )
  const updatedAt = toNumber(
    firstValue(record, [
      'updatedAt',
      'updated_at',
      'updatedTime',
      'updated_time',
    ])
  )
  if (remaining === null && !currency && updatedAt === null) return null
  return {
    remaining,
    currency: currency || 'USD',
    updatedAt,
  }
}

/** Normalize one node row from either snake_case or camelCase JSON. */
export function normalizeNode(value: unknown): ManagedNode | null {
  const record = asRecord(value)
  if (!record) return null

  const id = toNumber(firstValue(record, ['id', 'ID'])) ?? 0
  if (id <= 0) return null

  const provider = toText(
    firstValue(record, ['provider', 'Provider'])
  ).toLowerCase()
  const healthyValue = firstValue(record, ['healthy', 'Healthy'])
  const statusText = toText(
    firstValue(record, ['status', 'health', 'state', 'deployStatus'])
  ).toLowerCase()
  const healthy =
    healthyValue === undefined
      ? ['healthy', 'online', 'up', 'running'].includes(statusText)
      : toBoolean(healthyValue)

  const deployingValue = firstValue(record, ['deploying', 'Deploying'])
  const deploying =
    deployingValue === undefined
      ? ['deploying', 'building', 'queued', 'deploying...'].includes(statusText)
      : toBoolean(deployingValue)

  const apiTokenMasked = toText(
    firstValue(record, [
      'apiTokenMasked',
      'api_token_masked',
      'api_token_hint',
      'tokenMasked',
      'token_masked',
    ])
  )

  return {
    id,
    name: toText(firstValue(record, ['name', 'Name'])) || `#${id}`,
    provider: provider || 'other',
    apiTokenMasked,
    hasApiToken:
      toBoolean(firstValue(record, ['has_api_token', 'hasApiToken'])) ||
      apiTokenMasked !== '',
    serviceUrl: toText(
      firstValue(record, ['serviceUrl', 'service_url', 'ServiceUrl', 'url'])
    ),
    serviceDomain: toText(
      firstValue(record, ['serviceDomain', 'service_domain', 'ServiceDomain'])
    ),
    projectId: toText(
      firstValue(record, ['projectId', 'project_id', 'ProjectId'])
    ),
    serviceId: toText(
      firstValue(record, ['serviceId', 'service_id', 'ServiceId'])
    ),
    domain: toText(firstValue(record, ['domain', 'Domain', 'host'])),
    healthy,
    latencyMs:
      toNumber(
        firstValue(record, ['latencyMs', 'latency_ms', 'LatencyMs', 'latency'])
      ) ?? 0,
    lastBeat:
      toNumber(
        firstValue(record, [
          'lastBeat',
          'last_beat',
          'LastBeat',
          'lastHeartbeat',
          'last_heartbeat',
        ])
      ) ?? 0,
    lastError: toText(
      firstValue(record, ['lastError', 'last_error', 'LastError'])
    ),
    balance: normalizeBalance(
      firstValue(record, [
        'balance',
        'balanceInfo',
        'balance_info',
        'BalanceInfo',
      ])
    ),
    enabled:
      firstValue(record, ['enabled', 'Enabled']) === undefined
        ? true
        : toBoolean(firstValue(record, ['enabled', 'Enabled'])),
    deploying,
    createdTime:
      toNumber(
        firstValue(record, ['createdTime', 'created_time', 'CreatedTime'])
      ) ?? 0,
    updatedTime:
      toNumber(
        firstValue(record, ['updatedTime', 'updated_time', 'UpdatedTime'])
      ) ?? 0,
    raw: record,
  }
}

function extractList(value: unknown, keys: readonly string[]): unknown[] {
  const direct = asArray(value)
  if (direct.length > 0) return direct
  const record = asRecord(value)
  if (!record) return []
  for (const key of keys) {
    const candidate = record[key]
    const list = asArray(candidate)
    if (list.length > 0) return list
    const nested = asRecord(candidate)
    if (nested) {
      const deeper = extractList(nested, keys)
      if (deeper.length > 0) return deeper
    }
  }
  return []
}

export function normalizeNodeList(value: unknown): ManagedNode[] {
  const record = asRecord(value)
  const payload = record?.data !== undefined ? record.data : value
  const list = extractList(payload, ['items', 'nodes', 'list', 'records'])
  return list
    .map((item) => normalizeNode(item))
    .filter((node): node is ManagedNode => node !== null)
}

function normalizeOp(value: string): DeployActionOp {
  const normalized = value.trim().toLowerCase()
  if (['add', 'create', 'new', 'insert', 'added'].includes(normalized)) {
    return 'add'
  }
  if (
    [
      'update',
      'modify',
      'change',
      'patch',
      'set',
      'updated',
      'modified',
    ].includes(normalized)
  ) {
    return 'update'
  }
  if (
    ['delete', 'remove', 'destroy', 'deleted', 'removed'].includes(normalized)
  ) {
    return 'delete'
  }
  return 'other'
}

function normalizeDeployAction(
  value: unknown,
  opHint?: string
): DeployAction | null {
  const record = asRecord(value)
  if (!record) {
    return {
      op: normalizeOp(opHint ?? ''),
      resource: 'resource',
      variable: toText(value),
      from: '',
      to: '',
      skipped: false,
    }
  }
  const actionValue = toText(
    firstValue(record, ['op', 'action', 'operation', 'type', 'kind'])
  )
  // "noop" entries only echo unchanged resources (e.g. an already-present
  // service domain); they are not part of the actionable diff.
  if (actionValue.trim().toLowerCase() === 'noop') return null
  return {
    op: normalizeOp(actionValue || opHint || ''),
    resource:
      toText(
        firstValue(record, [
          'resource',
          'resourceType',
          'resource_type',
          'target',
        ])
      ) || 'variable',
    variable:
      toText(
        firstValue(record, [
          'variable',
          'key',
          'name',
          'field',
          'path',
          'env',
          'envKey',
        ])
      ) || '',
    from: toText(
      firstValue(record, ['from', 'before', 'current', 'old', 'oldValue'])
    ),
    to: toText(
      firstValue(record, ['to', 'after', 'next', 'new', 'newValue', 'value'])
    ),
    skipped: toBoolean(firstValue(record, ['skipped', 'Skipped'])),
  }
}

function normalizeActionList(value: unknown): DeployAction[] {
  const direct = asArray(value)
  if (direct.length > 0) {
    return direct
      .map((item) => normalizeDeployAction(item))
      .filter((item): item is DeployAction => item !== null)
  }
  const record = asRecord(value)
  if (!record) return []
  // Backend may group actions as {added: [...], modified: [...], deleted: [...]}.
  return Object.entries(record)
    .flatMap(([opHint, entry]) => {
      const list = asArray(entry)
      return list.map((item) => normalizeDeployAction(item, opHint))
    })
    .filter((item): item is DeployAction => item !== null)
}

function normalizeWarnings(value: unknown): string[] {
  const list = asArray(value)
  if (list.length > 0) {
    return list
      .map((item) => {
        if (typeof item === 'string') return item
        const record = asRecord(item)
        if (!record) return toText(item)
        return toText(
          firstValue(record, ['message', 'warning', 'detail', 'description'])
        )
      })
      .filter((item) => item.length > 0)
  }
  const text = toText(value)
  return text ? [text] : []
}

export function normalizeDeployPlan(value: unknown): DeployPlan {
  const record = asRecord(value)
  const data = asRecord(record?.data) ?? record ?? {}
  const plan = asRecord(data.plan) ?? data
  const planId = toText(
    firstValue(plan, ['planId', 'plan_id', 'id', 'planToken', 'plan_token']) ??
      firstValue(data, ['planId', 'plan_id'])
  )
  const actionsRaw =
    firstValue(plan, ['actions', 'changes', 'diff', 'resources']) ??
    firstValue(data, ['actions', 'changes', 'diff'])
  const warningsRaw =
    firstValue(plan, ['warnings', 'warning']) ??
    firstValue(data, ['warnings', 'warning'])
  return {
    planId,
    actions: normalizeActionList(actionsRaw),
    warnings: normalizeWarnings(warningsRaw),
    raw: value,
  }
}

function nestedText(
  record: Record<string, unknown>,
  parentKeys: readonly string[],
  keys: readonly string[]
): string {
  const parent = firstValue(record, parentKeys)
  const parentRecord = asRecord(parent)
  if (parentRecord) {
    const nested = toText(firstValue(parentRecord, keys))
    if (nested) return nested
  }
  return toText(firstValue(record, keys)) || toText(parent)
}

function normalizeKvChange(value: unknown): FailoverKvChange | null {
  const record = asRecord(value)
  if (!record) {
    const key = toText(value)
    return key ? { key, from: '', to: '', action: '' } : null
  }
  const key = toText(
    firstValue(record, ['key', 'name', 'nodeId', 'node_id', 'path'])
  )
  if (!key) return null
  return {
    key,
    from: toText(
      firstValue(record, ['from', 'old', 'before', 'current', 'oldValue'])
    ),
    to: toText(
      firstValue(record, ['to', 'new', 'after', 'target', 'newValue', 'value'])
    ),
    action: toText(firstValue(record, ['action', 'op', 'operation', 'type'])),
  }
}

function normalizeKvChanges(value: unknown): FailoverKvChange[] {
  const direct = asArray(value)
  if (direct.length > 0) {
    return direct
      .map((item) => normalizeKvChange(item))
      .filter((item): item is FailoverKvChange => item !== null)
  }
  const record = asRecord(value)
  if (!record) return []
  const nested = firstValue(record, [
    'changes',
    'items',
    'entries',
    'kvChanges',
  ])
  if (nested !== undefined) return normalizeKvChanges(nested)
  return Object.entries(record).flatMap(([key, entry]) => {
    const change = normalizeKvChange(entry)
    if (!change) {
      return [{ key, from: '', to: '', action: '' }]
    }
    return [{ ...change, key: change.key || key }]
  })
}

export function normalizeFailoverPlan(value: unknown): FailoverPlan {
  const record = asRecord(value)
  const data = asRecord(record?.data) ?? record ?? {}
  const plan = asRecord(data.plan) ?? data
  const dns = asRecord(plan.dns) ?? {}
  const combined = { ...plan, ...dns }
  const planId = toText(
    firstValue(plan, ['planId', 'plan_id', 'id', 'planToken', 'plan_token']) ??
      firstValue(data, ['planId', 'plan_id'])
  )
  const primaryDomain = toText(
    firstValue(plan, ['primaryDomain', 'primary_domain', 'domain', 'record'])
  )
  const currentCname =
    nestedText(
      combined,
      ['current', 'currentDns', 'current_dns', 'cf'],
      ['cname', 'currentCname', 'current_cname', 'value', 'host', 'target']
    ) ||
    toText(
      firstValue(plan, [
        'fromTarget',
        'from_target',
        'currentCname',
        'current_cname',
        'from',
      ])
    )
  let targetCname =
    nestedText(
      combined,
      ['target', 'targetDomain', 'target_domain', 'node'],
      ['cname', 'targetCname', 'target_cname', 'value', 'host', 'domain']
    ) || ''
  if (!targetCname) {
    targetCname =
      toText(
        firstValue(plan, [
          'toTarget',
          'to_target',
          'targetCname',
          'target_cname',
          'newCname',
          'new_cname',
          'to',
        ])
      ) || ''
  }
  const kvRaw =
    firstValue(plan, [
      'kvChanges',
      'kv_changes',
      'workerKv',
      'worker_kv',
      'kv',
      'changes',
    ]) ?? firstValue(data, ['kvChanges', 'kv_changes', 'changes'])
  const kvKey = toText(firstValue(plan, ['kvKey', 'kv_key', 'KVKey']))
  const kvSkipped = toBoolean(
    firstValue(plan, ['kvSkipped', 'kv_skipped', 'KVSkipped'])
  )
  let kvChanges = normalizeKvChanges(kvRaw)
  if (kvKey && kvChanges.length === 0) {
    kvChanges = [
      {
        key: kvKey,
        from: toText(firstValue(plan, ['kvFrom', 'kv_from', 'KVFrom'])),
        to: toText(firstValue(plan, ['kvTo', 'kv_to', 'KVTo'])),
        action: kvSkipped ? 'skipped' : 'update',
      },
    ]
  }
  return {
    planId,
    primaryDomain,
    currentCname,
    targetCname,
    kvChanges,
    warnings: normalizeWarnings(
      firstValue(plan, ['warnings', 'warning']) ??
        firstValue(data, ['warnings', 'warning'])
    ),
    kvSkipped,
    raw: value,
  }
}

export function normalizeDeployStatus(value: unknown): DeployStatus {
  const record = asRecord(value)
  const data = asRecord(record?.data) ?? record ?? {}
  const statusText = toText(
    firstValue(data, [
      'status',
      'state',
      'phase',
      'deployStatus',
      'deploy_status',
      'deploymentStatus',
    ])
  ).toLowerCase()
  const message = toText(
    firstValue(data, ['message', 'detail', 'error', 'description', 'url'])
  )
  let status: DeployStatus['status'] = 'unknown'
  if (
    [
      'success',
      'succeeded',
      'deployed',
      'live',
      'active',
      'healthy',
      'done',
    ].includes(statusText)
  ) {
    status = 'success'
  } else if (
    [
      'failed',
      'failure',
      'error',
      'crashed',
      'errored',
      'canceled',
      'cancelled',
      'removed',
    ].includes(statusText)
  ) {
    status = 'failed'
  } else if (
    [
      'building',
      'deploying',
      'queued',
      'pending',
      'initializing',
      'running',
      'in_progress',
    ].includes(statusText)
  ) {
    status =
      statusText === 'queued' || statusText === 'pending'
        ? 'pending'
        : 'running'
  }
  return { status, message, raw: value }
}

export function normalizeDeployApplyResult(value: unknown): DeployApplyResult {
  const record = asRecord(value)
  const data = asRecord(record?.data) ?? record ?? {}
  const deploymentStatus = toText(
    firstValue(data, ['deployment_status', 'deploymentStatus'])
  )
  const status = normalizeDeployStatus({
    ...data,
    status: deploymentStatus || firstValue(data, ['status', 'state']),
  })
  return {
    planId: toText(firstValue(data, ['plan_id', 'planId'])),
    nodeId: toNumber(firstValue(data, ['node_id', 'nodeId'])) ?? 0,
    projectId: toText(firstValue(data, ['project_id', 'projectId'])),
    environmentId: toText(
      firstValue(data, ['environment_id', 'environmentId'])
    ),
    serviceId: toText(firstValue(data, ['service_id', 'serviceId'])),
    serviceDomain: toText(
      firstValue(data, ['service_domain', 'serviceDomain'])
    ),
    domain: toText(firstValue(data, ['domain'])),
    deploymentId: toText(firstValue(data, ['deployment_id', 'deploymentId'])),
    deploymentStatus,
    status: status.status,
    variablesChanged:
      toNumber(firstValue(data, ['variables_changed', 'variablesChanged'])) ??
      0,
    warnings: normalizeWarnings(firstValue(data, ['warnings', 'warning'])),
    durationMs: toNumber(firstValue(data, ['duration_ms', 'durationMs'])) ?? 0,
  }
}

export function normalizeNodeSettings(value: unknown): NodeSettings {
  const record = asRecord(value)
  const data = asRecord(record?.data) ?? record ?? {}
  let threshold = toNumber(
    firstValue(data, [
      'balanceAlertThreshold',
      'balance_alert_threshold',
      'BalanceAlertThreshold',
    ])
  )
  if (threshold === null) threshold = 1

  const kvConfiguredRaw = firstValue(data, [
    'cloudflareKvConfigured',
    'cloudflare_kv_configured',
    'cfKvConfigured',
    'cf_kv_configured',
  ])
  const cfKvNamespaceConfigured =
    kvConfiguredRaw === undefined
      ? toText(
          firstValue(data, [
            'cfKvNamespace',
            'cf_kv_namespace',
            'cloudflareKvNamespaceId',
            'cloudflare_kv_namespace_id',
          ])
        ) !== ''
      : toBoolean(kvConfiguredRaw)

  return {
    balanceAlertThreshold: threshold,
    alertWebhookUrl: toText(
      firstValue(data, [
        'alertWebhookUrl',
        'alert_webhook_url',
        'AlertWebhookURL',
      ])
    ),
    nodeDenylist: toStringArray(
      firstValue(data, [
        'nodeDenylist',
        'node_denylist',
        'denylist',
        'Denylist',
      ])
    ),
    nodeAllowlist: toStringArray(
      firstValue(data, [
        'nodeAllowlist',
        'node_allowlist',
        'allowlist',
        'Allowlist',
      ])
    ),
    primaryDomain: toText(
      firstValue(data, ['primaryDomain', 'primary_domain', 'NodePrimaryDomain'])
    ),
    nodeAutoFailover: toBoolean(
      firstValue(data, [
        'nodeAutoFailover',
        'node_auto_failover',
        'autoFailover',
        'auto_failover',
        'NodeAutoFailover',
      ])
    ),
    cfApiToken: '',
    cfKvNamespace: '',
    cfZoneId: '',
    cfAccountId: '',
    cfTokenConfigured:
      toBoolean(
        firstValue(data, [
          'cfTokenConfigured',
          'cf_token_configured',
          'cfConfigured',
          'cf_configured',
          'cloudflareTokenConfigured',
          'cloudflare_token_configured',
        ])
      ) ||
      toText(
        firstValue(data, ['cfApiTokenConfigured', 'cf_api_token_configured'])
      ) !== '',
    cfKvNamespaceConfigured,
    cfZoneIdConfigured:
      toBoolean(
        firstValue(data, [
          'cfZoneIdConfigured',
          'cf_zone_id_configured',
          'cfZoneConfigured',
          'cf_zone_configured',
          'cloudflareZoneConfigured',
          'cloudflare_zone_configured',
        ])
      ) ||
      toText(
        firstValue(data, [
          'cfZoneId',
          'cf_zone_id',
          'cloudflareZoneId',
          'cloudflare_zone_id',
        ])
      ) !== '',
    cfAccountIdConfigured:
      toBoolean(
        firstValue(data, [
          'cfAccountIdConfigured',
          'cf_account_id_configured',
          'cloudflareAccountConfigured',
          'cloudflare_account_configured',
        ])
      ) ||
      toText(
        firstValue(data, [
          'cfAccountId',
          'cf_account_id',
          'cloudflareAccountId',
          'cloudflare_account_id',
        ])
      ) !== '',
    smtpConfigured: toBoolean(
      firstValue(data, ['smtpConfigured', 'smtp_configured', 'SMTPConfigured'])
    ),
  }
}

export function normalizeAuditLogs(value: unknown): NodeAuditLog[] {
  const record = asRecord(value)
  const payload = record?.data !== undefined ? record.data : value
  const list = extractList(payload, [
    'items',
    'logs',
    'records',
    'audits',
    'list',
  ])
  return list
    .map((item, index) => {
      const entry = asRecord(item)
      if (!entry) return null
      const resultRaw = firstValue(entry, ['result', 'success', 'ok', 'status'])
      let success = toBoolean(resultRaw)
      if (typeof resultRaw === 'string') {
        const normalized = resultRaw.trim().toLowerCase()
        success =
          normalized !== 'failed' &&
          normalized !== 'failure' &&
          normalized !== 'error' &&
          normalized !== 'denied' &&
          normalized !== 'rejected'
      }
      return {
        id: toNumber(firstValue(entry, ['id', 'logId', 'log_id'])) ?? index + 1,
        action: toText(
          firstValue(entry, ['action', 'operation', 'event', 'type'])
        ),
        target: toText(
          firstValue(entry, [
            'target',
            'resource',
            'nodeName',
            'node_name',
            'object',
          ])
        ),
        success,
        operator: toText(
          firstValue(entry, [
            'operator',
            'operator_name',
            'operatorName',
            'username',
            'user',
            'actor',
            'admin',
          ])
        ),
        detail: toText(
          firstValue(entry, [
            'detail',
            'message',
            'description',
            'resultDetail',
          ])
        ),
        createdTime:
          toNumber(
            firstValue(entry, [
              'createdTime',
              'created_time',
              'time',
              'timestamp',
              'createdAt',
              'created_at',
            ])
          ) ?? 0,
      }
    })
    .filter((log): log is NodeAuditLog => log !== null)
}

export function normalizeNodeTestResult(
  value: unknown,
  fallbackMessage: string
): NodeTestResult {
  const record = asRecord(value)
  const data = asRecord(record?.data) ?? record ?? {}
  const statusText = toText(
    firstValue(data, ['status', 'state', 'result', 'health'])
  ).toLowerCase()
  const healthyRaw = firstValue(data, [
    'healthy',
    'connected',
    'ok',
    'reachable',
  ])
  const latencyMs = toNumber(
    firstValue(data, ['latencyMs', 'latency_ms', 'latency', 'ms'])
  )
  const message =
    toText(firstValue(data, ['message', 'detail', 'error'])) || fallbackMessage

  if (record?.success === false) {
    return {
      status: 'failed',
      message,
      latencyMs,
      checkedBy: 'backend',
    }
  }

  const healthy = healthyRaw === undefined ? null : toBoolean(healthyRaw)
  if (healthy === true) {
    return { status: 'success', message, latencyMs, checkedBy: 'backend' }
  }
  if (healthy === false && healthyRaw !== undefined) {
    return { status: 'failed', message, latencyMs, checkedBy: 'backend' }
  }
  if (['success', 'ok', 'healthy', 'connected'].includes(statusText)) {
    return { status: 'success', message, latencyMs, checkedBy: 'backend' }
  }
  if (['failed', 'error', 'unhealthy', 'unreachable'].includes(statusText)) {
    return { status: 'failed', message, latencyMs, checkedBy: 'backend' }
  }
  return { status: 'warning', message, latencyMs, checkedBy: 'backend' }
}

const SECRET_VARIABLE_PATTERN =
  /(token|secret|passw|pwd|credential|authorization|api[-_]?key|private[-_]?key|dsn|cookie|session)/i

/**
 * Mask a plan diff value. Secret-looking variables are fully masked; long
 * non-secret values are shortened to keep the diff readable.
 */
export function maskPlanValue(variable: string, value: string): string {
  if (!value) return '—'
  if (value === '******') return value
  if (SECRET_VARIABLE_PATTERN.test(variable)) {
    return value.length > 4 ? `••••${value.slice(-4)}` : '••••'
  }
  if (value.length > 80) {
    return `${value.slice(0, 36)}…${value.slice(-24)}`
  }
  return value
}

export function toExternalUrl(domain: string): string {
  const trimmed = domain.trim()
  if (!trimmed) return ''
  if (/^https?:\/\//i.test(trimmed)) return trimmed
  return `https://${trimmed}`
}

export function getNodeProvider(provider: string): NodeProvider {
  switch (provider.toLowerCase()) {
    case 'railway':
    case 'koyeb':
    case 'render':
    case 'fly':
    case 'sealos':
    case 'clawcloud':
      return provider.toLowerCase() as NodeProvider
    default:
      return 'other'
  }
}

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
// [M5-FE] Node management types
//
// Contract source: docs/M5-部署与节点管理设计.md §7. The backend may not be
// deployed yet while this frontend lands, so every read path goes through the
// normalizers in ./lib and tolerates snake_case / camelCase / string-encoded
// JSON payloads.
// ============================================================================

export const NODE_PROVIDERS = [
  'railway',
  'koyeb',
  'render',
  'fly',
  'sealos',
  'clawcloud',
  'other',
] as const

export type NodeProvider = (typeof NODE_PROVIDERS)[number]

export type NodeBalance = {
  remaining: number | null
  currency: string
  updatedAt: number | null
}

/** Normalized node shape used throughout the UI. */
export type ManagedNode = {
  id: number
  name: string
  provider: NodeProvider | string
  apiTokenMasked: string
  hasApiToken: boolean
  serviceUrl: string
  serviceDomain: string
  projectId: string
  serviceId: string
  domain: string
  healthy: boolean
  latencyMs: number
  lastBeat: number
  lastError: string
  balance: NodeBalance | null
  enabled: boolean
  deploying: boolean
  createdTime: number
  updatedTime: number
  raw: Record<string, unknown>
}

/**
 * Request body field names follow the M5 backend (`nodeRequest` in
 * controller/node.go): snake_case, with the id in the body for PUT.
 */
export type NodePayload = {
  name: string
  provider: NodeProvider
  api_token?: string
  service_url?: string
  project_id?: string
  service_id?: string
  domain?: string
  enabled?: boolean
}

export type NodeTestResult = {
  status: 'success' | 'warning' | 'failed'
  message: string
  latencyMs: number | null
  checkedBy: 'backend' | 'browser' | 'none'
}

export type DeployActionOp = 'add' | 'update' | 'delete' | 'other'

export type DeployAction = {
  op: DeployActionOp
  resource: string
  variable: string
  from: string
  to: string
  skipped: boolean
}

export type DeployPlan = {
  planId: string
  actions: DeployAction[]
  warnings: string[]
  raw: unknown
}

export type DeployStatus = {
  status: 'pending' | 'running' | 'success' | 'failed' | 'unknown'
  message: string
  raw: unknown
}

export type DeployApplyResult = {
  planId: string
  nodeId: number
  projectId: string
  environmentId: string
  serviceId: string
  serviceDomain: string
  domain: string
  deploymentId: string
  deploymentStatus: string
  status: DeployStatus['status']
  variablesChanged: number
  warnings: string[]
  durationMs: number
}

export type FailoverKvChange = {
  key: string
  from: string
  to: string
  action: string
}

export type FailoverPlan = {
  planId: string
  primaryDomain: string
  currentCname: string
  targetCname: string
  kvChanges: FailoverKvChange[]
  warnings: string[]
  kvSkipped: boolean
  raw: unknown
}

export type NodeSettings = {
  balanceAlertThreshold: number
  alertWebhookUrl: string
  primaryDomain: string
  nodeDenylist: string[]
  nodeAllowlist: string[]
  nodeAutoFailover: boolean
  cfApiToken: string
  cfKvNamespace: string
  cfZoneId: string
  cfAccountId: string
  cfTokenConfigured: boolean
  cfKvNamespaceConfigured: boolean
  cfZoneIdConfigured: boolean
  cfAccountIdConfigured: boolean
  smtpConfigured: boolean
}

export type NodeSettingsPayload = {
  balanceAlertThreshold: number
  alertWebhookUrl: string
  primaryDomain: string
  nodeDenylist: string[]
  nodeAllowlist: string[]
  autoFailover: boolean
  cloudflareApiToken?: string
  cloudflareZoneId?: string
  cloudflareKvNamespaceId?: string
  cloudflareAccountId?: string
}

export type NodeAuditLog = {
  id: number
  action: string
  target: string
  success: boolean
  operator: string
  detail: string
  createdTime: number
}

export type NodeActionResponse<T = unknown> = {
  success: boolean
  message?: string
  message_key?: string
  data?: T
}

export type NodeListResponse = NodeActionResponse<unknown>

export type NodeSettingsResponse = NodeActionResponse<unknown>

export type NodeAuditResponse = NodeActionResponse<unknown>

export type DeployPlanResponse = NodeActionResponse<unknown>

export type DeployApplyResponse = NodeActionResponse<unknown>

export type FailoverPlanResponse = NodeActionResponse<unknown>

export type FailoverApplyResponse = NodeActionResponse<unknown>

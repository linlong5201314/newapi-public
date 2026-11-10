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
import { api, type ApiRequestConfig } from '@/lib/api'

import {
  normalizeDeployStatus,
  normalizeNodeList,
  normalizeNodeTestResult,
} from './lib'
import type {
  DeployApplyResponse,
  DeployPlanResponse,
  DeployStatus,
  FailoverApplyResponse,
  FailoverPlanResponse,
  NodeActionResponse,
  NodeAuditResponse,
  NodeListResponse,
  NodePayload,
  NodeSettingsPayload,
  NodeSettingsResponse,
  NodeTestResult,
} from './types'

/**
 * [M5-FE] Node management API.
 *
 * Contract: docs/M5-部署与节点管理设计.md §7 — every endpoint is root-only
 * (RootAuth) and every write is audited server-side. The backend may not be
 * deployed yet, so callers always fail soft and show inline errors.
 */
const BASE = '/api/node'

const nodeRequestConfig = (
  config: ApiRequestConfig = {}
): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
})

/** GET /api/node → node list (tokens masked server-side). */
export async function getNodes(): Promise<NodeListResponse> {
  const res = await api.get(`${BASE}`, nodeRequestConfig())
  return res.data
}

/** POST /api/node → create a node. */
export async function createNode(
  payload: NodePayload
): Promise<NodeActionResponse> {
  const res = await api.post(`${BASE}`, payload, nodeRequestConfig())
  return res.data
}

/**
 * PUT /api/node → update a node. The endpoint is not id-scoped in §7, so the
 * id travels in the body (same convention as route rules).
 */
export async function updateNode(
  id: number,
  payload: Partial<NodePayload>
): Promise<NodeActionResponse> {
  const res = await api.put(`${BASE}`, { ...payload, id }, nodeRequestConfig())
  return res.data
}

/** DELETE /api/node/:id */
export async function deleteNode(id: number): Promise<NodeActionResponse> {
  const res = await api.delete(`${BASE}/${id}`, nodeRequestConfig())
  return res.data
}

/** POST /api/node/:id/check → immediate health check. */
export async function checkNode(id: number): Promise<NodeActionResponse> {
  const res = await api.post(`${BASE}/${id}/check`, null, nodeRequestConfig())
  return res.data
}

/**
 * POST /api/node/test → optional pre-save connection test.
 *
 * This endpoint is an M5-FE extension used by the new-node wizard. When the
 * backend does not implement it the wizard falls back to a browser-side
 * reachability probe and still allows saving with a warning.
 */
export async function testNodeConnection(
  payload: NodePayload
): Promise<NodeActionResponse> {
  const res = await api.post(`${BASE}/test`, payload, nodeRequestConfig())
  return res.data
}

/** POST /api/node/deploy/plan → dry-run diff for one node. */
export async function planNodeDeploy(
  nodeId: number,
  envOverrides?: Record<string, string>
): Promise<DeployPlanResponse> {
  const body: Record<string, unknown> = { nodeId }
  if (envOverrides && Object.keys(envOverrides).length > 0) {
    body.envOverrides = envOverrides
  }
  const res = await api.post(`${BASE}/deploy/plan`, body, nodeRequestConfig())
  return res.data
}

/** POST /api/node/deploy/apply → apply a dry-run plan (10-minute validity). */
export async function applyNodeDeploy(
  planId: string
): Promise<DeployApplyResponse> {
  const res = await api.post(
    `${BASE}/deploy/apply`,
    { planId, confirm: true },
    nodeRequestConfig()
  )
  return res.data
}

/**
 * GET /api/node/:id/deploy/status → deployment progress.
 *
 * The status endpoint is not listed in §7; when it is unavailable this falls
 * back to the node list and reports `unknown` so the dialog can show a
 * "submitted, status reporting unavailable" state instead of hanging.
 */
export async function getNodeDeployStatus(
  nodeId: number
): Promise<DeployStatus> {
  try {
    const res = await api.get(
      `${BASE}/${nodeId}/deploy/status`,
      nodeRequestConfig()
    )
    return normalizeDeployStatus(res.data)
  } catch {
    // Fall through to the list endpoint.
  }

  try {
    const result = await getNodes()
    const node = normalizeNodeList(result.data).find(
      (item) => item.id === nodeId
    )
    if (!node) {
      return {
        status: 'unknown',
        message: 'Node not found while polling deployment status.',
        raw: null,
      }
    }
    const rawStatus = node.raw.deployStatus ?? node.raw.deploy_status
    if (rawStatus !== undefined) {
      return normalizeDeployStatus({ status: rawStatus, node: node.raw })
    }
    return {
      status: node.deploying ? 'running' : 'unknown',
      message: node.deploying
        ? 'Deployment is still running.'
        : 'Deployment status endpoint is unavailable.',
      raw: node.raw,
    }
  } catch {
    return {
      status: 'unknown',
      message: 'Deployment status is temporarily unavailable.',
      raw: null,
    }
  }
}

/** POST /api/node/:id/failover/plan → CF DNS + Worker KV dry-run. */
export async function planNodeFailover(
  nodeId: number
): Promise<FailoverPlanResponse> {
  const res = await api.post(
    `${BASE}/${nodeId}/failover/plan`,
    null,
    nodeRequestConfig()
  )
  return res.data
}

/** POST /api/node/:id/failover/apply → apply a failover plan. */
export async function applyNodeFailover(
  nodeId: number,
  planId: string
): Promise<FailoverApplyResponse> {
  const res = await api.post(
    `${BASE}/${nodeId}/failover/apply`,
    { planId, confirm: true },
    nodeRequestConfig()
  )
  return res.data
}

/** GET /api/node/settings → alert thresholds, lists and CF configuration. */
export async function getNodeSettings(): Promise<NodeSettingsResponse> {
  const res = await api.get(`${BASE}/settings`, nodeRequestConfig())
  return res.data
}

/** PUT /api/node/settings */
export async function updateNodeSettings(
  payload: NodeSettingsPayload
): Promise<NodeSettingsResponse> {
  const res = await api.put(`${BASE}/settings`, payload, nodeRequestConfig())
  return res.data
}

/** GET /api/node/audit?nodeId= → audit records for one node. */
export async function getNodeAudit(nodeId: number): Promise<NodeAuditResponse> {
  const res = await api.get(`${BASE}/audit`, {
    params: { nodeId },
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return res.data
}

/**
 * Browser-side reachability probe used only when `/api/node/test` is missing.
 * A CORS rejection is indistinguishable from a network error, so callers must
 * treat this as a warning rather than a hard failure.
 */
export async function probeNodeServiceUrl(
  serviceUrl: string
): Promise<NodeTestResult | null> {
  const trimmed = serviceUrl.trim()
  if (!trimmed || !/^https?:\/\//i.test(trimmed)) return null

  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), 8000)
  const startedAt = performance.now()
  try {
    const response = await fetch(`${trimmed.replace(/\/+$/, '')}/api/status`, {
      method: 'GET',
      signal: controller.signal,
      credentials: 'omit',
      cache: 'no-store',
    })
    const latencyMs = Math.round(performance.now() - startedAt)
    return normalizeNodeTestResult(
      {
        success: response.ok,
        data: {
          healthy: response.ok,
          status: response.ok ? 'healthy' : 'unhealthy',
          latencyMs,
          message: response.ok
            ? 'Service URL responded from the browser.'
            : `Service URL returned HTTP ${response.status}.`,
        },
      },
      'Service URL probe finished.'
    )
  } catch {
    return null
  } finally {
    window.clearTimeout(timeout)
  }
}

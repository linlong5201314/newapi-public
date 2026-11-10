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
import type { NodeProvider } from './types'

// ============================================================================
// [M5-FE] Provider metadata
//
// All provider registration is manual (design §3.2 + M6 manual onboarding).
// The four guide steps below are the shared M6 "register → token → service →
// paste" flow; `hintKey` adds the provider-specific console paths.
// ============================================================================

export type ProviderMeta = {
  value: NodeProvider
  label: string
  /** i18n key with provider-specific console guidance. */
  hintKey: string
  /** Badge class used in the node table. */
  badgeClassName: string
  /** Railway can create the service/domain itself, so a service URL is optional. */
  requiresServiceUrl: boolean
}

export const PROVIDER_META: Record<NodeProvider, ProviderMeta> = {
  railway: {
    value: 'railway',
    label: 'Railway',
    hintKey:
      'M6 step ①: register at railway.com and create an Account Token at railway.com/account/tokens. Railway deployment can create the project/service for you; project and service IDs are only needed when targeting an existing service.',
    badgeClassName:
      'border-violet-500/30 bg-violet-500/10 text-violet-700 dark:text-violet-300',
    requiresServiceUrl: false,
  },
  koyeb: {
    value: 'koyeb',
    label: 'Koyeb',
    hintKey:
      'Create an API token under Koyeb → Account → API, then create an app/service and paste its public URL. Koyeb IDs are optional.',
    badgeClassName:
      'border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300',
    requiresServiceUrl: true,
  },
  render: {
    value: 'render',
    label: 'Render',
    hintKey:
      'Create a Render web service or deploy the Blueprint, then paste the service URL and service ID. Render has no account-wide deploy token, so a service URL is required.',
    badgeClassName:
      'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
    requiresServiceUrl: true,
  },
  fly: {
    value: 'fly',
    label: 'Fly.io',
    hintKey:
      'Run `fly tokens create org` and `fly apps create`, then paste the token and the public .fly.dev URL. A deploy token needs access to the target organisation.',
    badgeClassName:
      'border-fuchsia-500/30 bg-fuchsia-500/10 text-fuchsia-700 dark:text-fuchsia-300',
    requiresServiceUrl: true,
  },
  sealos: {
    value: 'sealos',
    label: 'Sealos',
    hintKey:
      'Create an application in Sealos and copy its public URL. Paste the API token/kubeconfig-derived credential and the project/service identifiers when available.',
    badgeClassName:
      'border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300',
    requiresServiceUrl: true,
  },
  clawcloud: {
    value: 'clawcloud',
    label: 'ClawCloud Run',
    hintKey:
      'M6 step ①: sign in with GitHub, claim the $5/month free quota, deploy from the production image, then paste the public URL and access token.',
    badgeClassName:
      'border-orange-500/30 bg-orange-500/10 text-orange-700 dark:text-orange-300',
    requiresServiceUrl: true,
  },
  other: {
    value: 'other',
    label: 'Other',
    hintKey:
      'Any platform that exposes an HTTP service URL. Fill in project/service IDs when the platform provides them; the health check only needs the URL.',
    badgeClassName: 'border-border bg-muted text-muted-foreground',
    requiresServiceUrl: true,
  },
}

export const PROVIDER_OPTIONS: ProviderMeta[] = Object.values(PROVIDER_META)

export function getProviderMeta(provider: string): ProviderMeta {
  const normalized = provider.trim().toLowerCase() as NodeProvider
  return PROVIDER_META[normalized] ?? PROVIDER_META.other
}

/** The shared M6 four-step manual onboarding flow. */
export const PROVIDER_GUIDE_STEP_KEYS = [
  'Register or sign in at the provider console manually. The platform never creates accounts for you.',
  'Create an API token in the provider console (Account → API Tokens).',
  'Create the project/service, or copy the public service URL and IDs of an existing one.',
  'Paste the credentials below and run Test connection before saving.',
] as const

export const NODE_MESSAGES = {
  LOAD_FAILED: 'Failed to load nodes',
  CREATE_FAILED: 'Failed to create node',
  UPDATE_FAILED: 'Failed to update node',
  DELETE_FAILED: 'Failed to delete node',
  CHECK_FAILED: 'Health check failed',
  CHECK_ALL_FAILED: 'Health check completed with failures',
  DEPLOY_PLAN_FAILED: 'Failed to build the deployment plan',
  DEPLOY_APPLY_FAILED: 'Failed to apply the deployment plan',
  DEPLOY_STATUS_FAILED: 'Failed to read deployment status',
  FAILOVER_PLAN_FAILED: 'Failed to build the failover plan',
  FAILOVER_APPLY_FAILED: 'Failed to apply the failover plan',
  TEST_FAILED: 'Connection test failed',
  SETTINGS_LOAD_FAILED: 'Failed to load node settings',
  SETTINGS_SAVE_FAILED: 'Failed to save node settings',
  AUDIT_LOAD_FAILED: 'Failed to load node audit logs',
  UNEXPECTED: 'An unexpected error occurred',
} as const

export const NODE_SUCCESS_MESSAGES = {
  CREATED: 'Node created successfully',
  UPDATED: 'Node updated successfully',
  DELETED: 'Node deleted successfully',
  CHECK_OK: 'Node is healthy',
  CHECK_BAD: 'Node did not pass the health check',
  CHECK_ALL_DONE: 'Health check finished',
  DEPLOY_APPLIED: 'Deployment started',
  DEPLOY_DONE: 'Deployment completed',
  DEPLOY_FAILED: 'Deployment failed',
  FAILOVER_APPLIED: 'Domain failover started',
  SETTINGS_SAVED: 'Node settings saved',
} as const

/** Plan IDs are only valid for 10 minutes (design §3.3 / §7). */
export const PLAN_VALIDITY_MINUTES = 10

/** Human labels for backend NodeAudit.action values. */
export const AUDIT_ACTION_LABELS: Record<string, string> = {
  create: 'Create node',
  update: 'Update node',
  delete: 'Delete node',
  check: 'Health check',
  deploy_plan: 'Deployment plan',
  deploy_apply: 'Deployment apply',
  failover_plan: 'Failover plan',
  failover_apply: 'Failover apply',
  alert: 'Balance alert',
  settings_update: 'Node settings update',
}

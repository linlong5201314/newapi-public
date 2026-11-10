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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import { DEFAULT_STRATEGY, STRATEGY_OPTIONS } from '../constants'
import type {
  RouteRule,
  RouteRuleConfig,
  RouteRulePayload,
  RouteRuleTarget,
} from '../types'

// ============================================================================
// Form Schema
// ============================================================================

const strategyValues = STRATEGY_OPTIONS.map((option) => option.value) as [
  RouteRule['strategy'],
  ...RouteRule['strategy'][],
]

export function getRouteRuleFormSchema(t: TFunction) {
  return z.object({
    name: z
      .string()
      .trim()
      .min(1, t('Please enter a name'))
      .max(64, t('Name must be 64 characters or fewer')),
    strategy: z.enum(strategyValues),
    enabled: z.boolean(),
    targets: z
      .array(
        z.object({
          channelId: z.number().int().positive(),
          weight: z.number().int().min(0),
          fallbackOnlyOnQuotaExhaustion: z.boolean(),
        })
      )
      .min(1, t('Select at least one target channel')),
    maxRetries: z.number().int().min(0),
    retryDelayMs: z.number().int().min(0),
    targetTimeoutMs: z.number().int().min(0),
  })
}

export type RouteRuleFormValues = z.infer<
  ReturnType<typeof getRouteRuleFormSchema>
>

// ============================================================================
// Defaults & Transforms
// ============================================================================

export function getRouteRuleFormDefaultValues(): RouteRuleFormValues {
  return {
    name: '',
    strategy: DEFAULT_STRATEGY,
    enabled: true,
    targets: [],
    // Defaults mirror the backend (model/route_rule.go RouteRuleDefault*).
    maxRetries: 1,
    retryDelayMs: 1000,
    targetTimeoutMs: 30000,
  }
}

export function transformRouteRuleToFormDefaults(
  rule: RouteRule
): RouteRuleFormValues {
  const defaults = getRouteRuleFormDefaultValues()
  return {
    name: rule.name,
    strategy: rule.strategy,
    enabled: rule.enabled,
    targets: rule.targets.map((target) => ({
      channelId: target.channelId,
      weight: target.weight,
      fallbackOnlyOnQuotaExhaustion:
        target.fallbackOnlyOnQuotaExhaustion === true,
    })),
    maxRetries: rule.config.maxRetries ?? defaults.maxRetries,
    retryDelayMs: rule.config.retryDelayMs ?? defaults.retryDelayMs,
    targetTimeoutMs: rule.config.targetTimeoutMs ?? defaults.targetTimeoutMs,
  }
}

export function transformFormValuesToPayload(
  values: RouteRuleFormValues
): RouteRulePayload {
  const targets: RouteRuleTarget[] = values.targets.map((target) => ({
    channelId: target.channelId,
    weight: target.weight,
    ...(target.fallbackOnlyOnQuotaExhaustion
      ? { fallbackOnlyOnQuotaExhaustion: true }
      : {}),
  }))
  const config: RouteRuleConfig = {
    maxRetries: values.maxRetries,
    retryDelayMs: values.retryDelayMs,
    targetTimeoutMs: values.targetTimeoutMs,
  }

  return {
    name: values.name.trim(),
    strategy: values.strategy,
    enabled: values.enabled,
    targets,
    config,
  }
}

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
import type { RouteRuleStrategy } from './types'

// ============================================================================
// [M2-FE] Strategy metadata
//
// Design doc §1.2. Each strategy carries an English source string (the
// i18next key) whose zh.json entry is the one-line Chinese explanation shown
// in the strategy dropdown and as a tooltip in the table.
// ============================================================================

export type StrategyMeta = {
  value: RouteRuleStrategy
  /** i18n key: short human label. */
  labelKey: string
  /** i18n key: one-line semantic explanation. */
  descriptionKey: string
}

export const STRATEGY_META: Record<RouteRuleStrategy, StrategyMeta> = {
  priority: {
    value: 'priority',
    labelKey: 'Priority',
    descriptionKey:
      'Pick the first healthy target in order; fall through on failure.',
  },
  weighted: {
    value: 'weighted',
    labelKey: 'Weighted',
    descriptionKey:
      'Weighted-random first pick; remaining targets fall back by descending weight.',
  },
  'round-robin': {
    value: 'round-robin',
    labelKey: 'Round Robin',
    descriptionKey: 'Rotate through targets one by one.',
  },
  random: {
    value: 'random',
    labelKey: 'Random',
    descriptionKey: 'Shuffle targets and try them in random order.',
  },
  'least-used': {
    value: 'least-used',
    labelKey: 'Least Used',
    descriptionKey: 'Prefer the target with the lowest usage today.',
  },
  'lowest-latency': {
    value: 'lowest-latency',
    labelKey: 'Lowest Latency',
    descriptionKey:
      'Prefer the target with the lowest recent average response time.',
  },
  lkgp: {
    value: 'lkgp',
    labelKey: 'Last Known Good',
    descriptionKey: 'Promote the target that succeeded last time to the front.',
  },
  'reset-aware': {
    value: 'reset-aware',
    labelKey: 'Reset Aware',
    descriptionKey:
      'Prefer targets with known balance; low-balance targets go last.',
  },
}

export const STRATEGY_OPTIONS: StrategyMeta[] = Object.values(STRATEGY_META)

export const DEFAULT_STRATEGY: RouteRuleStrategy = 'priority'

export const ERROR_MESSAGES = {
  LOAD_FAILED: 'Failed to load route rules',
  CREATE_FAILED: 'Failed to create route rule',
  UPDATE_FAILED: 'Failed to update route rule',
  DELETE_FAILED: 'Failed to delete route rule',
  TOGGLE_FAILED: 'Failed to toggle route rule',
  UNEXPECTED: 'An unexpected error occurred',
} as const

export const SUCCESS_MESSAGES = {
  CREATED: 'Route rule created successfully',
  UPDATED: 'Route rule updated successfully',
  DELETED: 'Route rule deleted successfully',
} as const

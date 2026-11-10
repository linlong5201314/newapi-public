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
/* eslint-disable react-refresh/only-export-components */
import React, { createContext, useContext, useMemo, useState } from 'react'

import type { RouteRule } from '../types'

// ============================================================================
// Types
// ============================================================================

type DialogType = 'create-rule' | 'update-rule' | 'delete-rule' | null

type RouteRulesContextType = {
  open: DialogType
  setOpen: (open: DialogType) => void
  currentRow: RouteRule | null
  setCurrentRow: (row: RouteRule | null) => void
}

// ============================================================================
// Context
// ============================================================================

const RouteRulesContext = createContext<RouteRulesContextType | undefined>(
  undefined
)

// ============================================================================
// Provider
// ============================================================================

export function RouteRulesProvider({
  children,
}: {
  children: React.ReactNode
}) {
  const [open, setOpen] = useState<DialogType>(null)
  const [currentRow, setCurrentRow] = useState<RouteRule | null>(null)

  const value = useMemo<RouteRulesContextType>(
    () => ({ open, setOpen, currentRow, setCurrentRow }),
    [open, currentRow]
  )

  return (
    <RouteRulesContext.Provider value={value}>
      {children}
    </RouteRulesContext.Provider>
  )
}

// ============================================================================
// Hook
// ============================================================================

export function useRouteRules() {
  const context = useContext(RouteRulesContext)
  if (!context) {
    throw new Error('useRouteRules must be used within RouteRulesProvider')
  }
  return context
}

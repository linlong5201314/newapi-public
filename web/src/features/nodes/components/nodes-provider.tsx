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

import type { ManagedNode } from '../types'

export type NodeDialogType =
  | 'create-node'
  | 'update-node'
  | 'delete-node'
  | 'deploy-node'
  | 'failover-node'
  | 'audit-node'
  | null

type NodesContextType = {
  open: NodeDialogType
  setOpen: (open: NodeDialogType) => void
  currentRow: ManagedNode | null
  setCurrentRow: (row: ManagedNode | null) => void
}

const NodesContext = createContext<NodesContextType | undefined>(undefined)

export function NodesProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useState<NodeDialogType>(null)
  const [currentRow, setCurrentRow] = useState<ManagedNode | null>(null)

  const value = useMemo<NodesContextType>(
    () => ({ open, setOpen, currentRow, setCurrentRow }),
    [open, currentRow]
  )

  return <NodesContext.Provider value={value}>{children}</NodesContext.Provider>
}

export function useNodes() {
  const context = useContext(NodesContext)
  if (!context) {
    throw new Error('useNodes must be used within NodesProvider')
  }
  return context
}

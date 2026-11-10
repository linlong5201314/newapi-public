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
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { handleServerError } from '@/lib/handle-server-error'

import { deleteNode } from '../api'
import { NODE_MESSAGES, NODE_SUCCESS_MESSAGES } from '../constants'
import { nodesQueryKeys } from '../lib'
import { NodeAuditSheet } from './node-audit-sheet'
import { NodeDeployDialog } from './node-deploy-dialog'
import { NodeFailoverDialog } from './node-failover-dialog'
import { NodeWizardDialog } from './node-wizard-dialog'
import { useNodes } from './nodes-provider'

export function NodesDialogs() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow } = useNodes()
  const queryClient = useQueryClient()
  const [isDeleting, setIsDeleting] = useState(false)

  const handleDelete = async () => {
    if (!currentRow) return
    setIsDeleting(true)
    try {
      const result = await deleteNode(currentRow.id)
      if (result.success) {
        toast.success(t(NODE_SUCCESS_MESSAGES.DELETED))
        await queryClient.invalidateQueries({
          queryKey: nodesQueryKeys.lists(),
        })
        setOpen(null)
      } else {
        handleServerError(result, t(NODE_MESSAGES.DELETE_FAILED))
      }
    } catch (error) {
      handleServerError(error, t(NODE_MESSAGES.DELETE_FAILED))
    } finally {
      setIsDeleting(false)
    }
  }

  return (
    <>
      <NodeWizardDialog
        open={open === 'create-node' || open === 'update-node'}
        onOpenChange={(next) => !next && setOpen(null)}
        currentRow={open === 'update-node' ? currentRow : null}
      />

      <NodeDeployDialog
        open={open === 'deploy-node'}
        onOpenChange={(next) => !next && setOpen(null)}
        currentRow={open === 'deploy-node' ? currentRow : null}
      />

      <NodeFailoverDialog
        open={open === 'failover-node'}
        onOpenChange={(next) => !next && setOpen(null)}
        currentRow={open === 'failover-node' ? currentRow : null}
      />

      <NodeAuditSheet
        node={open === 'audit-node' ? currentRow : null}
        onOpenChange={(next) => !next && setOpen(null)}
      />

      <ConfirmDialog
        open={open === 'delete-node'}
        onOpenChange={(next) => !next && setOpen(null)}
        title={t('Delete Node')}
        desc={t(
          'Are you sure you want to delete node "{{name}}"? Health checks and automated failover will stop using it. This action cannot be undone.',
          { name: currentRow?.name ?? '' }
        )}
        confirmText={t('Delete')}
        destructive
        isLoading={isDeleting}
        handleConfirm={() => void handleDelete()}
      />
    </>
  )
}

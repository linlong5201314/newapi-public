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

import { deleteRouteRule } from '../api'
import { ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import { routeRulesQueryKeys } from '../lib'
import { RouteRuleMutateDrawer } from './route-rule-mutate-drawer'
import { useRouteRules } from './route-rules-provider'

export function RouteRulesDialogs() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow } = useRouteRules()
  const queryClient = useQueryClient()
  const [isDeleting, setIsDeleting] = useState(false)

  const handleDelete = async () => {
    if (!currentRow) return
    setIsDeleting(true)
    try {
      const result = await deleteRouteRule(currentRow.id)
      if (result.success) {
        toast.success(t(SUCCESS_MESSAGES.DELETED))
        queryClient.invalidateQueries({
          queryKey: routeRulesQueryKeys.lists(),
        })
        setOpen(null)
      } else {
        handleServerError(result, t(ERROR_MESSAGES.DELETE_FAILED))
      }
    } catch (error) {
      handleServerError(error, t(ERROR_MESSAGES.DELETE_FAILED))
    } finally {
      setIsDeleting(false)
    }
  }

  return (
    <>
      {/* Create / Update Drawer */}
      <RouteRuleMutateDrawer
        open={open === 'create-rule' || open === 'update-rule'}
        onOpenChange={(v) => !v && setOpen(null)}
        currentRow={open === 'update-rule' ? currentRow : null}
      />

      {/* Delete Confirmation */}
      <ConfirmDialog
        open={open === 'delete-rule'}
        onOpenChange={(v) => !v && setOpen(null)}
        title={t('Delete Route Rule')}
        desc={t(
          'Are you sure you want to delete route rule "{{name}}"? This action cannot be undone.',
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

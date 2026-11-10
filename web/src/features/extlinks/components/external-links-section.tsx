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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { handleServerError } from '@/lib/handle-server-error'

import { useUpdateOption } from '../../system-settings/hooks/use-update-option'
import {
  createExtLink,
  deleteExtLink,
  getExtLinksAdmin,
  updateExtLink,
} from '../api'
import {
  EXT_LINK_MODES,
  EXT_LINK_POSITIONS,
  type ExtLink,
  type ExtLinkMode,
  type ExtLinkPayload,
  type ExtLinkPosition,
} from '../types'
import { ExtLinkIcon } from './ext-link-icon'

/**
 * [M4-FE] Root settings card: external links management.
 *
 * Mounted as a section of system-settings/content (section-registry). Talks
 * to /api/extlink (RootAuth CRUD); the three platform options
 * (ExtLinkEnabled/ExtLinkMaxCount/ExtLinkAllowHosts) save through the generic
 * /api/option channel via useUpdateOption.
 */

const EMPTY_FORM: ExtLinkPayload = {
  slug: '',
  name: '',
  url: '',
  icon: '',
  mode: 'proxy',
  position: 'home_card',
  enabled: true,
  sort_order: 0,
}

const MODE_LABEL_KEYS: Record<ExtLinkMode, string> = {
  proxy: 'Proxy (in-site)',
  iframe: 'Iframe (embed)',
  newtab: 'New Window',
}

const POSITION_LABEL_KEYS: Record<ExtLinkPosition, string> = {
  home_card: 'Home Card',
  sidebar: 'Sidebar',
  both: 'Home Card + Sidebar',
}

export function ExternalLinksSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption()

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<ExtLink | null>(null)
  const [form, setForm] = useState<ExtLinkPayload>(EMPTY_FORM)
  const [deleteTarget, setDeleteTarget] = useState<ExtLink | null>(null)

  const listQuery = useQuery({
    queryKey: ['extlinks', 'admin'],
    queryFn: async () => {
      const res = await getExtLinksAdmin()
      return res.data
    },
  })

  const links = listQuery.data?.links ?? []
  const options = listQuery.data?.options

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['extlinks'] })
  }

  const saveMutation = useMutation({
    mutationFn: async (payload: ExtLinkPayload) => {
      const res =
        payload.id != null
          ? await updateExtLink(payload)
          : await createExtLink(payload)
      if (!res.success) {
        throw new Error(res.message || 'failed')
      }
      return res
    },
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      setDialogOpen(false)
      setEditing(null)
      setForm(EMPTY_FORM)
      invalidate()
    },
    onError: (error: Error) => handleServerError(error, t('Failed to save')),
  })

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      const res = await deleteExtLink(id)
      if (!res.success) {
        throw new Error(res.message || 'failed')
      }
      return res
    },
    onSuccess: () => {
      toast.success(t('Deleted successfully'))
      setDeleteTarget(null)
      invalidate()
    },
    onError: (error: Error) => handleServerError(error, t('Failed to delete')),
  })

  const toggleMutation = useMutation({
    mutationFn: async (link: ExtLink) => {
      const res = await updateExtLink({
        id: link.id,
        slug: link.slug,
        name: link.name,
        url: link.url,
        icon: link.icon,
        mode: link.mode,
        position: link.position,
        enabled: !link.enabled,
        sort_order: link.sort_order,
      })
      if (!res.success) {
        throw new Error(res.message || 'failed')
      }
      return res
    },
    onSuccess: () => invalidate(),
    onError: (error: Error) => handleServerError(error, t('Failed to save')),
  })

  const openCreate = () => {
    setEditing(null)
    setForm(EMPTY_FORM)
    setDialogOpen(true)
  }

  const openEdit = (link: ExtLink) => {
    setEditing(link)
    setForm({
      id: link.id,
      slug: link.slug,
      name: link.name,
      url: link.url,
      icon: link.icon,
      mode: link.mode,
      position: link.position,
      enabled: link.enabled,
      sort_order: link.sort_order,
    })
    setDialogOpen(true)
  }

  const submit = () => {
    saveMutation.mutate(form)
  }

  return (
    <div className='space-y-6'>
      {/* 平台开关三项（ExtLinkEnabled/ExtLinkMaxCount/ExtLinkAllowHosts） */}
      <div className='space-y-4 rounded-lg border p-4'>
        <div className='flex items-center justify-between gap-4'>
          <div className='space-y-0.5'>
            <Label className='text-base'>{t('External Links Enabled')}</Label>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Master switch for the external links module and the /app/* reverse proxy.'
              )}
            </p>
          </div>
          <Switch
            checked={options?.enabled ?? true}
            disabled={updateOption.isPending}
            onCheckedChange={(checked) =>
              updateOption.mutate({ key: 'ExtLinkEnabled', value: checked })
            }
          />
        </div>
        <ExtLinkNumberOption
          label={t('Max Link Count')}
          value={options?.max_count ?? 20}
          disabled={updateOption.isPending}
          onSave={(v) => updateOption.mutate({ key: 'ExtLinkMaxCount', value: v })}
        />
        <ExtLinkTextOption
          label={t('Allow Hosts Whitelist')}
          description={t(
            'Comma-separated target hosts the proxy may reach. Empty = unrestricted (not recommended). Prefix with a dot for subdomains, e.g. .example.com.'
          )}
          value={options?.allow_hosts ?? ''}
          disabled={updateOption.isPending}
          onSave={(v) => updateOption.mutate({ key: 'ExtLinkAllowHosts', value: v })}
        />
      </div>

      {/* 链接列表 */}
      <div className='space-y-3'>
        <div className='flex items-center justify-between'>
          <h3 className='text-base font-medium'>{t('External Links')}</h3>
          <Button size='sm' onClick={openCreate}>
            <Plus className='mr-1 size-4' />
            {t('Add Link')}
          </Button>
        </div>

        {listQuery.isLoading ? (
          <LoadingState message={t('Loading...')} />
        ) : links.length === 0 ? (
          <p className='text-muted-foreground rounded-lg border border-dashed p-6 text-center text-sm'>
            {t('No external links configured yet.')}
          </p>
        ) : (
          <div className='divide-y rounded-lg border'>
            {links.map((link) => (
              <div
                key={link.id}
                className='flex items-center gap-3 p-3'
              >
                <ExtLinkIcon
                  icon={link.icon}
                  name={link.name}
                  className='text-primary size-6 shrink-0 object-contain'
                />
                <div className='min-w-0 flex-1'>
                  <div className='flex items-center gap-2'>
                    <span className='truncate font-medium'>{link.name}</span>
                    <code className='text-muted-foreground shrink-0 text-xs'>
                      /app/{link.slug}
                    </code>
                  </div>
                  <div className='text-muted-foreground truncate text-xs'>
                    {link.url} · {t(MODE_LABEL_KEYS[link.mode])} ·{' '}
                    {t(POSITION_LABEL_KEYS[link.position])} · #{link.sort_order}
                  </div>
                </div>
                <Switch
                  checked={link.enabled}
                  disabled={toggleMutation.isPending}
                  onCheckedChange={() => toggleMutation.mutate(link)}
                />
                <Button
                  variant='ghost'
                  size='icon'
                  onClick={() => openEdit(link)}
                >
                  <Pencil className='size-4' />
                </Button>
                <Button
                  variant='ghost'
                  size='icon'
                  onClick={() => setDeleteTarget(link)}
                >
                  <Trash2 className='text-destructive size-4' />
                </Button>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* 增删改对话框 */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className='max-w-lg'>
          <DialogHeader>
            <DialogTitle>
              {editing ? t('Edit Link') : t('Add Link')}
            </DialogTitle>
            <DialogDescription>
              {t(
                'Configure an external app. Proxy mode serves the target under /app/<slug> on this domain.'
              )}
            </DialogDescription>
          </DialogHeader>
          <div className='grid gap-4 py-2'>
            <div className='grid grid-cols-2 gap-4'>
              <div className='space-y-2'>
                <Label htmlFor='extlink-slug'>{t('Slug')}</Label>
                <Input
                  id='extlink-slug'
                  placeholder='my-app'
                  value={form.slug}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, slug: e.target.value }))
                  }
                />
              </div>
              <div className='space-y-2'>
                <Label htmlFor='extlink-name'>{t('Name')}</Label>
                <Input
                  id='extlink-name'
                  value={form.name}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, name: e.target.value }))
                  }
                />
              </div>
            </div>
            <div className='space-y-2'>
              <Label htmlFor='extlink-url'>{t('Target URL')}</Label>
              <Input
                id='extlink-url'
                placeholder='https://example.com'
                value={form.url}
                onChange={(e) =>
                  setForm((f) => ({ ...f, url: e.target.value }))
                }
              />
            </div>
            <div className='grid grid-cols-2 gap-4'>
              <div className='space-y-2'>
                <Label htmlFor='extlink-icon'>{t('Icon')}</Label>
                <Input
                  id='extlink-icon'
                  placeholder={t('globe / rocket / emoji / image URL')}
                  value={form.icon}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, icon: e.target.value }))
                  }
                />
              </div>
              <div className='space-y-2'>
                <Label htmlFor='extlink-sort'>{t('Sort Order')}</Label>
                <Input
                  id='extlink-sort'
                  type='number'
                  value={form.sort_order}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      sort_order: Number.parseInt(e.target.value, 10) || 0,
                    }))
                  }
                />
              </div>
            </div>
            <div className='grid grid-cols-2 gap-4'>
              <div className='space-y-2'>
                <Label>{t('Mode')}</Label>
                <Select
                  value={form.mode}
                  onValueChange={(v) =>
                    setForm((f) => ({ ...f, mode: v as ExtLinkMode }))
                  }
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {EXT_LINK_MODES.map((m) => (
                      <SelectItem key={m} value={m}>
                        {t(MODE_LABEL_KEYS[m])}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className='space-y-2'>
                <Label>{t('Position')}</Label>
                <Select
                  value={form.position}
                  onValueChange={(v) =>
                    setForm((f) => ({ ...f, position: v as ExtLinkPosition }))
                  }
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {EXT_LINK_POSITIONS.map((p) => (
                      <SelectItem key={p} value={p}>
                        {t(POSITION_LABEL_KEYS[p])}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className='flex items-center justify-between'>
              <Label htmlFor='extlink-enabled'>{t('Enabled')}</Label>
              <Switch
                id='extlink-enabled'
                checked={form.enabled}
                onCheckedChange={(checked) =>
                  setForm((f) => ({ ...f, enabled: checked }))
                }
              />
            </div>
          </div>
          <DialogFooter>
            <Button
              variant='outline'
              onClick={() => setDialogOpen(false)}
              disabled={saveMutation.isPending}
            >
              {t('Cancel')}
            </Button>
            <Button onClick={submit} disabled={saveMutation.isPending}>
              {saveMutation.isPending ? t('Saving...') : t('Save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={deleteTarget != null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t('Delete Link')}
        desc={t(
          'Delete this external link? The /app/<slug> route stops working immediately.'
        )}
        confirmText={t('Delete')}
        destructive
        handleConfirm={() =>
          deleteTarget && deleteMutation.mutate(deleteTarget.id)
        }
        isLoading={deleteMutation.isPending}
      />
    </div>
  )
}

/** 数字型 option 行：失焦/回车保存。 */
function ExtLinkNumberOption(props: {
  label: string
  value: number
  disabled?: boolean
  onSave: (value: number) => void
}) {
  const [draft, setDraft] = useState(String(props.value))
  useEffect(() => setDraft(String(props.value)), [props.value])
  const commit = () => {
    const v = Number.parseInt(draft, 10)
    if (Number.isFinite(v) && v > 0 && v !== props.value) {
      props.onSave(v)
    } else {
      setDraft(String(props.value))
    }
  }
  return (
    <div className='flex items-center justify-between gap-4'>
      <Label className='text-sm'>{props.label}</Label>
      <Input
        className='w-32'
        type='number'
        value={draft}
        disabled={props.disabled}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => e.key === 'Enter' && commit()}
      />
    </div>
  )
}

/** 文本型 option 行：失焦保存。 */
function ExtLinkTextOption(props: {
  label: string
  description?: string
  value: string
  disabled?: boolean
  onSave: (value: string) => void
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState(props.value)
  useEffect(() => setDraft(props.value), [props.value])
  const commit = () => {
    if (draft !== props.value) {
      props.onSave(draft)
    }
  }
  return (
    <div className='space-y-2'>
      <div className='flex items-center justify-between gap-4'>
        <div className='space-y-0.5'>
          <Label className='text-sm'>{props.label}</Label>
          {props.description && (
            <p className='text-muted-foreground text-xs'>
              {props.description}
            </p>
          )}
        </div>
        <Input
          className='w-72'
          placeholder={t('e.g. example.com, .foo.dev')}
          value={draft}
          disabled={props.disabled}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => e.key === 'Enter' && commit()}
        />
      </div>
    </div>
  )
}

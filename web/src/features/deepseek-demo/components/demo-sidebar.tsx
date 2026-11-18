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
import { Link } from '@tanstack/react-router'
import {
  Key,
  MessageSquarePlus,
  PanelLeftClose,
  PanelLeftOpen,
  Radio,
  FlaskConical,
  Trash2,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import type { DemoConversation } from '../types'

interface DemoSidebarProps {
  conversations: DemoConversation[]
  activeId: string
  userName: string
  collapsed?: boolean
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (id: string) => void
  onToggleCollapse: () => void
}

export function DemoSidebar(props: DemoSidebarProps) {
  const { t } = useTranslation()
  const [deleteId, setDeleteId] = useState<string | null>(null)
  const now = new Date()
  const today = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate()
  ).getTime()
  const sections = [
    {
      label: t('Today'),
      items: props.conversations.filter((item) => item.updatedAt >= today),
    },
    {
      label: t('Previous 7 days'),
      items: props.conversations.filter(
        (item) =>
          item.updatedAt < today && item.updatedAt >= today - 6 * 86400000
      ),
    },
    {
      label: t('Earlier'),
      items: props.conversations.filter(
        (item) => item.updatedAt < today - 6 * 86400000
      ),
    },
  ]

  if (props.collapsed) {
    return (
      <aside
        aria-label={t('Chat history')}
        className='border-border/60 bg-muted/40 flex h-full w-14 shrink-0 flex-col items-center gap-3 border-r py-4'
      >
        <Button
          variant='ghost'
          size='icon'
          onClick={props.onToggleCollapse}
          aria-label={t('Expand sidebar')}
        >
          <PanelLeftOpen aria-hidden='true' />
        </Button>
        <Button
          variant='ghost'
          size='icon'
          onClick={props.onNew}
          aria-label={t('New chat')}
        >
          <MessageSquarePlus aria-hidden='true' />
        </Button>
      </aside>
    )
  }

  return (
    <>
      <aside
        aria-label={t('Chat history')}
        className='border-border/60 bg-muted/40 flex h-full w-64 max-w-full shrink-0 flex-col border-r'
      >
        <div className='flex items-center justify-between px-4 pt-4 pb-2'>
          <span className='text-lg font-semibold tracking-tight text-[#4d6bfe]'>
            DeepSeek · NewAPI
          </span>
          <Button
            variant='ghost'
            size='icon'
            onClick={props.onToggleCollapse}
            aria-label={t('Collapse sidebar')}
          >
            <PanelLeftClose aria-hidden='true' />
          </Button>
        </div>
        <div className='px-3 pt-2 pb-4'>
          <Button
            onClick={props.onNew}
            variant='secondary'
            className='h-11 w-full justify-start rounded-xl bg-[#4d6bfe]/10 text-[#4d6bfe] hover:bg-[#4d6bfe]/20'
          >
            <MessageSquarePlus aria-hidden='true' />
            {t('New chat')}
          </Button>
        </div>
        <nav
          aria-label={t('Conversations')}
          className='min-h-0 flex-1 overflow-y-auto px-2 pb-2'
        >
          {props.conversations.length === 0 && (
            <p className='text-muted-foreground px-3 py-6 text-xs'>
              {t('Your conversations will appear here.')}
            </p>
          )}
          {sections
            .filter((section) => section.items.length > 0)
            .map((section) => (
              <section key={section.label} className='mb-3'>
                <h2 className='text-muted-foreground px-2 py-1 text-xs'>
                  {section.label}
                </h2>
                {[...section.items]
                  .sort((a, b) => b.updatedAt - a.updatedAt)
                  .map((conversation) => (
                    <div
                      key={conversation.id}
                      className={cn(
                        'group flex items-center gap-1 rounded-xl px-1',
                        conversation.id === props.activeId &&
                          'bg-background shadow-xs'
                      )}
                    >
                      <Button
                        variant='ghost'
                        onClick={() => props.onSelect(conversation.id)}
                        aria-current={
                          conversation.id === props.activeId
                            ? 'page'
                            : undefined
                        }
                        className='h-11 min-w-0 flex-1 justify-start'
                        title={conversation.title}
                      >
                        <span className='truncate'>{conversation.title}</span>
                      </Button>
                      <Button
                        variant='ghost'
                        size='icon'
                        aria-label={t('Delete conversation')}
                        onClick={() => setDeleteId(conversation.id)}
                        className='text-muted-foreground hover:text-destructive size-9 shrink-0 sm:opacity-0 sm:group-focus-within:opacity-100 sm:group-hover:opacity-100'
                      >
                        <Trash2 className='size-4' aria-hidden='true' />
                      </Button>
                    </div>
                  ))}
              </section>
            ))}
        </nav>
        <div className='border-border/60 border-t p-3'>
          <div className='mb-2 grid grid-cols-3 gap-1'>
            <Link
              to='/playground'
              className='text-muted-foreground hover:bg-muted flex min-h-11 flex-col items-center justify-center gap-1 rounded-lg text-xs'
            >
              <FlaskConical className='size-4' aria-hidden='true' />
              {t('Playground')}
            </Link>
            <Link
              to='/my-channels'
              className='text-muted-foreground hover:bg-muted flex min-h-11 flex-col items-center justify-center gap-1 rounded-lg text-xs'
            >
              <Radio className='size-4' aria-hidden='true' />
              {t('My channels')}
            </Link>
            <Link
              to='/keys'
              className='text-muted-foreground hover:bg-muted flex min-h-11 flex-col items-center justify-center gap-1 rounded-lg text-xs'
            >
              <Key className='size-4' aria-hidden='true' />
              {t('Keys')}
            </Link>
          </div>
          <div className='flex items-center gap-2 rounded-lg px-2 py-1.5'>
            <span
              aria-hidden='true'
              className='bg-muted flex size-8 shrink-0 items-center justify-center rounded-full text-xs font-semibold'
            >
              {props.userName.slice(0, 1).toUpperCase()}
            </span>
            <span className='text-muted-foreground truncate text-xs'>
              {props.userName}
            </span>
          </div>
        </div>
      </aside>
      <ConfirmDialog
        open={deleteId !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteId(null)
        }}
        destructive
        title={t('Delete conversation?')}
        desc={t(
          'This conversation will be removed from this device. This cannot be undone.'
        )}
        confirmText={t('Delete')}
        handleConfirm={() => {
          if (deleteId) props.onDelete(deleteId)
          setDeleteId(null)
        }}
      />
    </>
  )
}

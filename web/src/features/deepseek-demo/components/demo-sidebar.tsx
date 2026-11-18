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
import { Key, Radio, FlaskConical, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'

import type { DemoConversation } from '../types'
import { IconPanelLeftOutline16, NewChatIcon } from './official-icons'

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
        className='ds-demo-sidebar ds-demo-sidebar-collapsed'
      >
        <Button
          variant='ghost'
          size='icon'
          onClick={props.onToggleCollapse}
          aria-label={t('Expand sidebar')}
        >
          <IconPanelLeftOutline16 />
        </Button>
        <Button
          variant='ghost'
          size='icon'
          onClick={props.onNew}
          aria-label={t('New chat')}
        >
          <NewChatIcon />
        </Button>
      </aside>
    )
  }

  return (
    <>
      <aside aria-label={t('Chat history')} className='ds-demo-sidebar'>
        <div className='ds-demo-sidebar-top'>
          <span className='ds-demo-app-name'>DeepSeek · NewAPI</span>
          <Button
            variant='ghost'
            size='icon'
            onClick={props.onToggleCollapse}
            aria-label={t('Collapse sidebar')}
            className='ds-demo-sidebar-toggle'
          >
            <IconPanelLeftOutline16 />
          </Button>
        </div>
        <div>
          <Button
            onClick={props.onNew}
            variant='secondary'
            className='ds-demo-new-chat'
          >
            <NewChatIcon />
            {t('New chat')}
          </Button>
        </div>
        <nav aria-label={t('Conversations')} className='ds-demo-sidebar-nav'>
          {props.conversations.length === 0 && (
            <p className='text-muted-foreground px-3 py-6 text-xs'>
              {t('Your conversations will appear here.')}
            </p>
          )}
          {sections
            .filter((section) => section.items.length > 0)
            .map((section) => (
              <section key={section.label} className='ds-demo-history-section'>
                <h2 className='ds-demo-history-heading'>{section.label}</h2>
                {[...section.items]
                  .sort((a, b) => b.updatedAt - a.updatedAt)
                  .map((conversation) => (
                    <div
                      key={conversation.id}
                      className='group ds-demo-history-item'
                      data-active={conversation.id === props.activeId}
                    >
                      <Button
                        variant='ghost'
                        onClick={() => props.onSelect(conversation.id)}
                        aria-current={
                          conversation.id === props.activeId
                            ? 'page'
                            : undefined
                        }
                        className='ds-demo-history-select'
                        title={conversation.title}
                      >
                        <span className='truncate'>{conversation.title}</span>
                      </Button>
                      <Button
                        variant='ghost'
                        size='icon'
                        aria-label={t('Delete conversation')}
                        onClick={() => setDeleteId(conversation.id)}
                        className='ds-demo-history-delete'
                      >
                        <Trash2 className='size-4' aria-hidden='true' />
                      </Button>
                    </div>
                  ))}
              </section>
            ))}
        </nav>
        <div className='ds-demo-sidebar-footer'>
          <Link to='/dashboard' className='ds-demo-sidebar-link'>
            {t('Back to console')}
          </Link>
          <p className='text-muted-foreground py-1 text-[11px]'>
            NewAPI · QuantumNous
          </p>
          <div className='ds-demo-sidebar-links'>
            <Link to='/playground' className='ds-demo-sidebar-link'>
              <FlaskConical className='size-4' aria-hidden='true' />
              {t('Playground')}
            </Link>
            <Link to='/my-channels' className='ds-demo-sidebar-link'>
              <Radio className='size-4' aria-hidden='true' />
              {t('My channels')}
            </Link>
            <Link to='/keys' className='ds-demo-sidebar-link'>
              <Key className='size-4' aria-hidden='true' />
              {t('Keys')}
            </Link>
          </div>
          <div className='ds-demo-sidebar-profile'>
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

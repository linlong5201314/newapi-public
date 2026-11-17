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
  Radio,
  FlaskConical,
  Trash2,
} from 'lucide-react'

import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

import type { DemoConversation } from '../types'

type DemoSidebarLink = '/playground' | '/my-channels' | '/keys'

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

/** 会话时间分组：今天 / 近 7 天 / 更早。 */
function groupLabel(timestamp: number): string {
  const now = new Date()
  const date = new Date(timestamp)
  const startOfToday = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate()
  ).getTime()
  if (timestamp >= startOfToday) return '今天'
  if (timestamp >= startOfToday - 6 * 86400000) return '近 7 天'
  return '更早'
}

export function DemoSidebar({
  conversations,
  activeId,
  userName,
  collapsed,
  onSelect,
  onNew,
  onDelete,
  onToggleCollapse,
}: DemoSidebarProps) {
  const grouped = conversations.reduce<Record<string, DemoConversation[]>>(
    (acc, conversation) => {
      const label = groupLabel(conversation.updatedAt)
      acc[label] = acc[label] ?? []
      acc[label].push(conversation)
      return acc
    },
    {}
  )
  const order = ['今天', '近 7 天', '更早'].filter((label) => grouped[label])

  if (collapsed) {
    return (
      <aside className='flex w-14 shrink-0 flex-col items-center gap-3 border-r border-border/60 bg-[#f7f8fc] py-4 dark:bg-[#141414]'>
        <button
          type='button'
          onClick={onToggleCollapse}
          aria-label='展开侧边栏'
          className='flex size-9 items-center justify-center rounded-lg text-muted-foreground hover:bg-muted'
        >
          <MessageSquarePlus className='size-4.5' />
        </button>
        <button
          type='button'
          onClick={onNew}
          aria-label='新对话'
          className='flex size-9 items-center justify-center rounded-lg bg-[#4d6bfe] text-white'
        >
          <MessageSquarePlus className='size-4.5' />
        </button>
      </aside>
    )
  }

  return (
    <aside className='flex w-64 shrink-0 flex-col border-r border-border/60 bg-[#f7f8fc] dark:bg-[#141414]'>
      <div className='flex items-center justify-between px-4 pt-4 pb-2'>
        <div className='flex items-center gap-2'>
          <span className='flex size-7 items-center justify-center rounded-lg bg-[#4d6bfe] text-sm font-bold text-white'>
            D
          </span>
          <span className='text-sm font-semibold'>DeepSeek 对话</span>
        </div>
        <button
          type='button'
          onClick={onToggleCollapse}
          aria-label='收起侧边栏'
          className='flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-muted'
        >
          <PanelLeftClose className='size-4' />
        </button>
      </div>

      <div className='px-3 pt-2 pb-3'>
        <button
          type='button'
          onClick={onNew}
          className='flex w-full items-center justify-center gap-2 rounded-xl bg-[#4d6bfe] py-2.5 text-sm font-medium text-white transition-colors hover:bg-[#3f5bea]'
        >
          <MessageSquarePlus className='size-4' />
          开启新对话
        </button>
      </div>

      <div className='min-h-0 flex-1 overflow-y-auto px-2 pb-2'>
        {order.length === 0 && (
          <p className='px-3 py-6 text-xs text-muted-foreground'>
            还没有会话，发第一条消息就会出现在这里。
          </p>
        )}
        {order.map((label) => (
          <div key={label} className='mb-2'>
            <p className='px-2 py-1 text-[11px] font-medium text-muted-foreground'>
              {label}
            </p>
            {grouped[label].map((conversation) => (
              <div
                key={conversation.id}
                className={cn(
                  'group flex items-center gap-1 rounded-lg px-2 py-2 text-sm transition-colors',
                  conversation.id === activeId
                    ? 'bg-white text-foreground shadow-xs dark:bg-[#232323]'
                    : 'text-muted-foreground hover:bg-white/70 dark:hover:bg-[#1e1e1e]'
                )}
              >
                <button
                  type='button'
                  onClick={() => onSelect(conversation.id)}
                  className='min-w-0 flex-1 truncate text-left'
                  title={conversation.title}
                >
                  {conversation.title}
                </button>
                <button
                  type='button'
                  aria-label='删除会话'
                  onClick={() => onDelete(conversation.id)}
                  className='flex size-6 shrink-0 items-center justify-center rounded-md opacity-0 transition-opacity group-hover:opacity-100 hover:bg-muted hover:text-destructive'
                >
                  <Trash2 className='size-3.5' />
                </button>
              </div>
            ))}
          </div>
        ))}
      </div>

      <div className='border-t border-border/60 p-3'>
        <div className='mb-2 grid grid-cols-3 gap-1'>
          <SidebarLink to='/playground' icon={<FlaskConical className='size-4' />} label='游乐场' />
          <SidebarLink to='/my-channels' icon={<Radio className='size-4' />} label='我的渠道' />
          <SidebarLink to='/keys' icon={<Key className='size-4' />} label='密钥' />
        </div>
        <div className='flex items-center gap-2 rounded-lg px-2 py-1.5'>
          <span className='flex size-7 items-center justify-center rounded-full bg-muted text-xs font-semibold'>
            {userName.slice(0, 1).toUpperCase()}
          </span>
          <span className='truncate text-xs text-muted-foreground'>{userName}</span>
        </div>
      </div>
    </aside>
  )
}

function SidebarLink({
  to,
  icon,
  label,
}: {
  to: DemoSidebarLink
  icon: ReactNode
  label: string
}) {
  return (
    <Link
      to={to}
      className='flex flex-col items-center gap-1 rounded-lg py-2 text-[11px] text-muted-foreground transition-colors hover:bg-white/70 hover:text-foreground dark:hover:bg-[#1e1e1e]'
    >
      {icon}
      {label}
    </Link>
  )
}

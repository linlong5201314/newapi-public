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
import { Check, ChevronDown, Search } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'

import { cn } from '@/lib/utils'

import type { DemoGroupOption, DemoModelOption } from '../types'

interface ModelPickerProps {
  groups: DemoGroupOption[]
  models: DemoModelOption[]
  value: string
  group: string
  disabled?: boolean
  isLoading?: boolean
  onGroupChange: (group: string) => void
  onChange: (model: string) => void
}

/**
 * DeepSeek 风格的「模型选择」胶囊：点开是搜索框 + 分组切换 + 模型列表。
 * 一手可操作，不需要跳去设置页。
 */
export function ModelPicker({
  groups,
  models,
  value,
  group,
  disabled,
  isLoading,
  onGroupChange,
  onChange,
}: ModelPickerProps) {
  const [open, setOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handlePointerDown = (event: MouseEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) {
        setOpen(false)
      }
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('mousedown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [open])

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    if (!kw) return models
    return models.filter((model) => model.value.toLowerCase().includes(kw))
  }, [keyword, models])

  const currentLabel = value || (isLoading ? '加载模型…' : '选择模型')

  return (
    <div ref={containerRef} className='relative'>
      <button
        type='button'
        disabled={disabled}
        onClick={() => setOpen((prev) => !prev)}
        className={cn(
          'flex h-9 max-w-[16rem] items-center gap-1.5 rounded-full border border-border/70 bg-background/80 px-3 text-sm font-medium text-foreground shadow-xs transition-colors',
          'hover:border-[#4d6bfe]/50 hover:text-[#4d6bfe] disabled:cursor-not-allowed disabled:opacity-60'
        )}
      >
        <span className='truncate'>{currentLabel}</span>
        <ChevronDown className='size-3.5 shrink-0 opacity-60' />
      </button>

      {open && (
        <div className='absolute top-11 left-0 z-50 w-[22rem] overflow-hidden rounded-2xl border border-border/70 bg-popover shadow-xl'>
          {groups.length > 1 && (
            <div className='flex flex-wrap gap-1.5 border-b border-border/60 px-3 py-2'>
              {groups.map((item) => (
                <button
                  key={item.value}
                  type='button'
                  onClick={() => onGroupChange(item.value)}
                  className={cn(
                    'rounded-full px-2.5 py-1 text-xs transition-colors',
                    item.value === group
                      ? 'bg-[#4d6bfe] text-white'
                      : 'bg-muted text-muted-foreground hover:text-foreground'
                  )}
                >
                  {item.label}
                </button>
              ))}
            </div>
          )}

          <div className='flex items-center gap-2 border-b border-border/60 px-3 py-2'>
            <Search className='size-4 shrink-0 text-muted-foreground' />
            <input
              autoFocus
              value={keyword}
              onChange={(event) => setKeyword(event.target.value)}
              placeholder='搜索模型'
              className='h-7 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground/70'
            />
          </div>

          <div className='max-h-72 overflow-y-auto py-1'>
            {filtered.length === 0 && (
              <p className='px-3 py-6 text-center text-xs text-muted-foreground'>
                {isLoading ? '正在拉取模型列表…' : '没有匹配的模型'}
              </p>
            )}
            {filtered.map((model) => (
              <button
                key={model.value}
                type='button'
                onClick={() => {
                  onChange(model.value)
                  setOpen(false)
                  setKeyword('')
                }}
                className='flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-sm hover:bg-muted'
              >
                <span className='truncate font-mono text-[0.8rem]'>
                  {model.value}
                </span>
                {model.value === value && (
                  <Check className='size-4 shrink-0 text-[#4d6bfe]' />
                )}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

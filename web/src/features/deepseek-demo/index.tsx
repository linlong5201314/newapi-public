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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Boxes, FlaskConical, Menu, SquarePen } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
} from '@/components/ui/sheet'
import { getUserGroups, getUserModels } from '@/features/playground/api'
import { useAuthStore } from '@/stores/auth-store'

import { Composer } from './components/composer'
import { DemoSidebar } from './components/demo-sidebar'
import { DemoMessageList } from './components/message-list'
import { ModelPicker } from './components/model-picker'
import { useDemoChat } from './use-demo-chat'

/** Never persist an old account's state under the next account's storage key. */
export function DeepSeekDemo() {
  const userId = useAuthStore((state) => state.auth.user?.id)
  return userId === undefined ? null : (
    <DemoSession key={userId} userId={userId} />
  )
}

function DemoSession(props: { userId: number }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const chat = useDemoChat(props.userId)
  const [collapsed, setCollapsed] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [input, setInput] = useState('')
  const [model, setModel] = useState(chat.activeConversation?.model ?? '')
  const [group, setGroup] = useState(chat.activeConversation?.group ?? '')

  const groupsQuery = useQuery({
    queryKey: ['deepseek-demo-groups', props.userId],
    queryFn: getUserGroups,
    staleTime: 5 * 60 * 1000,
  })
  const effectiveGroup = group || groupsQuery.data?.[0]?.value || 'default'
  const modelsQuery = useQuery({
    queryKey: ['deepseek-demo-models', props.userId, effectiveGroup],
    queryFn: () => getUserModels(effectiveGroup),
    enabled: groupsQuery.isSuccess,
    staleTime: 60 * 1000,
  })
  const models = modelsQuery.data ?? []
  const selectedModel = models.some((item) => item.value === model)
    ? model
    : ((models.find((item) => /deepseek|r1|v3/i.test(item.value)) ?? models[0])
        ?.value ?? '')
  const unavailable =
    groupsQuery.isError ||
    modelsQuery.isError ||
    (modelsQuery.isSuccess && models.length === 0)
  const canSend = !!selectedModel && !unavailable && !modelsQuery.isFetching

  useEffect(() => {
    try {
      const preferred = window.localStorage.getItem('ds-demo:preferred-model')
      if (preferred) {
        setModel(preferred)
        window.localStorage.removeItem('ds-demo:preferred-model')
      }
    } catch {
      /* Storage is optional. */
    }
  }, [])

  const handleNew = () => {
    chat.select('')
    setInput('')
    setMobileOpen(false)
  }
  const handleSelect = (id: string) => {
    const conversation = chat.conversations.find((item) => item.id === id)
    if (!conversation) return
    chat.select(id)
    setModel(conversation.model)
    setGroup(conversation.group)
    setInput('')
    setMobileOpen(false)
  }
  const sidebarProps = {
    conversations: chat.conversations,
    activeId: chat.activeId,
    userName: user?.display_name?.trim() || user?.username || t('User'),
    onSelect: handleSelect,
    onNew: handleNew,
    onDelete: chat.remove,
  }

  return (
    <div className='bg-background flex h-full min-h-0 w-full overflow-hidden'>
      <div className='hidden h-full shrink-0 md:block'>
        <DemoSidebar
          {...sidebarProps}
          collapsed={collapsed}
          onToggleCollapse={() => setCollapsed((prev) => !prev)}
        />
      </div>
      <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
        <SheetContent
          side='left'
          showCloseButton={false}
          className='w-72 max-w-[85vw] gap-0 p-0'
        >
          <SheetTitle className='sr-only'>{t('Chat history')}</SheetTitle>
          <SheetDescription className='sr-only'>
            {t('Choose a conversation or start a new chat.')}
          </SheetDescription>
          <DemoSidebar
            {...sidebarProps}
            onToggleCollapse={() => setMobileOpen(false)}
          />
        </SheetContent>
      </Sheet>
      <div className='flex min-w-0 flex-1 flex-col'>
        <header className='flex shrink-0 items-center justify-between gap-2 px-3 py-3 sm:px-5'>
          <div className='flex min-w-0 items-center gap-2'>
            <Button
              variant='ghost'
              size='icon'
              className='md:hidden'
              aria-label={t('Open chat history')}
              aria-expanded={mobileOpen}
              onClick={() => setMobileOpen(true)}
            >
              <Menu aria-hidden='true' />
            </Button>
            <ModelPicker
              groups={groupsQuery.data ?? []}
              models={models}
              value={selectedModel}
              group={effectiveGroup}
              isLoading={groupsQuery.isPending || modelsQuery.isPending}
              disabled={chat.pending}
              onGroupChange={(next) => {
                setGroup(next)
                setModel('')
              }}
              onChange={setModel}
            />
          </div>
          <div className='flex shrink-0 items-center gap-1'>
            <Button
              variant='ghost'
              size='icon'
              className='hidden sm:inline-flex'
              nativeButton={false}
              render={<Link to='/my-models' />}
              aria-label={t('My models')}
            >
              <Boxes aria-hidden='true' />
            </Button>
            <Button
              variant='ghost'
              size='icon'
              className='hidden sm:inline-flex'
              nativeButton={false}
              render={<Link to='/playground' />}
              aria-label={t('Playground')}
            >
              <FlaskConical aria-hidden='true' />
            </Button>
            <Button
              variant='ghost'
              size='icon'
              aria-label={t('New chat')}
              onClick={handleNew}
            >
              <SquarePen aria-hidden='true' />
            </Button>
          </div>
        </header>
        {unavailable && (
          <ErrorState
            className='min-h-0 py-3'
            title={
              modelsQuery.isError || groupsQuery.isError
                ? t('Unable to load models')
                : t('No models available')
            }
            description={t(
              'Check your connection and account model access, then retry.'
            )}
            onRetry={() => {
              void groupsQuery.refetch()
              void modelsQuery.refetch()
            }}
          />
        )}
        <DemoMessageList
          key={chat.activeId}
          messages={chat.activeConversation?.messages ?? []}
          isStreaming={chat.pending}
          onSuggestion={setInput}
          onRegenerate={() => {
            if (canSend) chat.regenerate(selectedModel, effectiveGroup)
          }}
        />
        <Composer
          value={input}
          isStreaming={chat.pending}
          disabled={!canSend}
          placeholder={
            selectedModel
              ? t('Message {{model}}', { model: selectedModel })
              : t('Loading models...')
          }
          onChange={setInput}
          onSubmit={() => {
            if (canSend && chat.send(input, selectedModel, effectiveGroup)) {
              setInput('')
            }
          }}
          onStop={chat.stop}
        />
      </div>
    </div>
  )
}

import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { DemoSidebar } from '../components/demo-sidebar'
import type { DemoConversation } from '../types'

const chat: DemoConversation = {
  id: 'chat',
  title: 'Current conversation',
  model: 'deepseek-chat',
  group: 'default',
  messages: [],
  createdAt: 1,
  updatedAt: Date.now(),
}

async function renderSidebar(
  options: {
    collapsed?: boolean
    onDelete?: (id: string) => void
    onSelect?: (id: string) => void
  } = {}
) {
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => (
        <DemoSidebar
          conversations={[chat]}
          activeId='chat'
          userName='Alice'
          onNew={vi.fn()}
          onDelete={options.onDelete ?? vi.fn()}
          onSelect={options.onSelect ?? vi.fn()}
          onToggleCollapse={vi.fn()}
          collapsed={options.collapsed}
        />
      ),
    }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(<RouterProvider router={router} />)
}

describe('demo history sidebar', () => {
  it('requires explicit destructive confirmation before deleting a conversation', async () => {
    const onDelete = vi.fn()
    await renderSidebar({ onDelete })
    fireEvent.click(
      await screen.findByRole('button', { name: 'Delete conversation' })
    )
    const dialog = await screen.findByRole('alertdialog')
    expect(onDelete).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
    expect(onDelete).toHaveBeenCalledWith('chat')
  })

  it('exposes the selected conversation and preserves navigation actions', async () => {
    const onSelect = vi.fn()
    await renderSidebar({ onSelect })
    const selected = await screen.findByRole('button', {
      name: 'Current conversation',
    })
    expect(selected).toHaveAttribute('aria-current', 'page')
    fireEvent.click(selected)
    expect(onSelect).toHaveBeenCalledWith('chat')
    expect(screen.getByRole('link', { name: 'My channels' })).toHaveAttribute(
      'href',
      '/my-channels'
    )
    expect(screen.getByRole('link', { name: 'Keys' })).toHaveAttribute(
      'href',
      '/keys'
    )
  })

  it('keeps accessible expand and new-chat controls when collapsed', async () => {
    await renderSidebar({ collapsed: true })
    expect(
      await screen.findByRole('button', { name: 'Expand sidebar' })
    ).toBeEnabled()
    expect(screen.getByRole('button', { name: 'New chat' })).toBeEnabled()
    expect(
      screen.queryByRole('button', { name: 'Delete conversation' })
    ).not.toBeInTheDocument()
  })
})

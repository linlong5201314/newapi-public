import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { ChannelsProvider } from '../../channels-provider'
import { FetchModelsDialog } from '../fetch-models-dialog'

afterEach(() => {
  vi.restoreAllMocks()
})

function renderDialog(select: (models: string[]) => void, close: () => void) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ChannelsProvider>
        <FetchModelsDialog
          open
          onOpenChange={close}
          onModelsSelected={select}
          existingModelsOverride={['gpt-existing']}
          customFetcher={async () => ['gpt-existing', 'gpt-new']}
        />
      </ChannelsProvider>
    </QueryClientProvider>
  )
}

it('opens with no models selected even though saved models are listed as candidates', async () => {
  const select = vi.fn()
  renderDialog(select, vi.fn())
  // gpt-new is on the default "New Models" tab and must not be pre-selected.
  const fresh = await screen.findByRole('checkbox', { name: 'gpt-new' })
  expect(fresh).not.toBeChecked()
  // The saved model lives on the "Existing Models" tab and is not pre-selected either.
  await userEvent
    .setup()
    .click(screen.getByRole('tab', { name: /Existing Models/ }))
  expect(
    screen.getByRole('checkbox', { name: 'gpt-existing' })
  ).not.toBeChecked()
  // Apply is disabled while nothing is chosen and never fires.
  expect(screen.getByRole('button', { name: 'Save Models' })).toBeDisabled()
  expect(select).not.toHaveBeenCalled()
})

it('blocks an empty apply so saved channel models are never overwritten with none', async () => {
  const select = vi.fn()
  const close = vi.fn()
  const user = userEvent.setup()
  renderDialog(select, close)
  const fresh = await screen.findByRole('checkbox', { name: 'gpt-new' })
  // Select then deselect to reach an empty selection with the dialog interactive.
  await user.click(fresh)
  await user.click(fresh)
  expect(fresh).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Save Models' })).toBeDisabled()
  expect(select).not.toHaveBeenCalled()
  expect(close).not.toHaveBeenCalled()
})

it('applies only the explicitly chosen models', async () => {
  const select = vi.fn()
  const close = vi.fn()
  const user = userEvent.setup()
  renderDialog(select, close)
  await user.click(await screen.findByRole('checkbox', { name: 'gpt-new' }))
  const save = screen.getByRole('button', { name: 'Save Models' })
  expect(save).toBeEnabled()
  await user.click(save)
  expect(select).toHaveBeenCalledWith(['gpt-new'])
  expect(close).toHaveBeenCalledWith(false)
})

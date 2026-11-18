import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { expect, it } from 'vitest'

import { ChannelModelSelection } from '../channel-model-selection'

function SelectionFixture(props: { disabled?: boolean }) {
  const [enabled, setEnabled] = useState<string[]>(['deepseek-chat'])
  const [published, setPublished] = useState<string[]>([])
  return (
    <ChannelModelSelection
      models={['deepseek-chat', 'qwen-coder']}
      enabledModels={enabled}
      publicModels={published}
      disabled={props.disabled}
      onChange={(nextEnabled, nextPublic) => {
        setEnabled(nextEnabled)
        setPublished(nextPublic)
      }}
    />
  )
}

it('applies select all and deselect all to the whole catalog even when search hides models', async () => {
  const user = userEvent.setup()
  render(<SelectionFixture />)
  const search = screen.getByRole('searchbox', { name: 'Search models' })
  await user.type(search, 'deepseek')
  await user.click(
    screen.getByRole('button', { name: 'Select all for my channels' })
  )
  await user.clear(search)
  expect(
    screen.getByRole('checkbox', { name: 'Enable qwen-coder in my channels' })
  ).toBeChecked()
  await user.click(
    screen.getByRole('button', { name: 'Select all for the public pool' })
  )
  expect(
    screen.getByRole('checkbox', {
      name: 'Publish qwen-coder to the public pool',
    })
  ).toBeChecked()
  await user.click(
    screen.getByRole('button', { name: 'Deselect all for the public pool' })
  )
  expect(
    screen.getByRole('checkbox', { name: 'Enable qwen-coder in my channels' })
  ).toBeChecked()
  expect(
    screen.getByRole('checkbox', {
      name: 'Publish qwen-coder to the public pool',
    })
  ).not.toBeChecked()
  await user.click(
    screen.getByRole('button', { name: 'Deselect all for my channels' })
  )
  for (const checkbox of screen.getAllByRole('checkbox')) {
    expect(checkbox).not.toBeChecked()
  }
})

it('supports Space to publish a model and automatically enables it', async () => {
  const user = userEvent.setup()
  render(<SelectionFixture />)
  const publish = screen.getByRole('checkbox', {
    name: 'Publish qwen-coder to the public pool',
  })
  publish.focus()
  await user.keyboard(' ')
  expect(publish).toBeChecked()
  expect(
    screen.getByRole('checkbox', { name: 'Enable qwen-coder in my channels' })
  ).toBeChecked()
})

it('prevents model selection changes while saving', async () => {
  const user = userEvent.setup()
  render(<SelectionFixture disabled />)
  const checkbox = screen.getByRole('checkbox', {
    name: 'Enable qwen-coder in my channels',
  })
  expect(checkbox).toHaveAttribute('aria-disabled', 'true')
  await user.click(checkbox)
  expect(checkbox).not.toBeChecked()
  expect(
    screen.getByRole('button', { name: 'Select all for my channels' })
  ).toBeDisabled()
})

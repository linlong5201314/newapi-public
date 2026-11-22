import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { expect, it } from 'vitest'

import { UpstreamModelSelection } from '../upstream-model-selection'

function InlineSelection() {
  const [selected, setSelected] = useState<string[]>(['manual-model'])
  return (
    <>
      <UpstreamModelSelection
        models={['gpt-one', 'gpt-two', 'another-model']}
        selected={selected}
        existingModels={[]}
        showChanges={false}
        onChange={setSelected}
      />
      <output aria-label='Selected models'>{selected.join(',')}</output>
    </>
  )
}

it('deselect all only clears the models currently shown by the search filter', async () => {
  const user = userEvent.setup()
  render(<InlineSelection />)
  // Select everything first so hidden selections exist.
  await user.click(screen.getByRole('checkbox', { name: 'gpt-one' }))
  await user.click(screen.getByRole('checkbox', { name: 'gpt-two' }))
  await user.click(screen.getByRole('checkbox', { name: 'another-model' }))
  expect(screen.getByLabelText('Selected models')).toHaveTextContent(
    'manual-model,gpt-one,gpt-two,another-model'
  )
  // Narrow to the gpt- prefix; only those two are visible.
  await user.type(
    screen.getByRole('textbox', { name: 'Search models...' }),
    'gpt-'
  )
  await user.click(screen.getByRole('button', { name: 'Deselect all' }))
  // The hidden selections (manual-model, another-model) survive; visible ones clear.
  expect(screen.getByLabelText('Selected models')).toHaveTextContent(
    'manual-model,another-model'
  )
})

it('invert selection toggles only the visible models and leaves hidden ones intact', async () => {
  const user = userEvent.setup()
  render(<InlineSelection />)
  await user.click(screen.getByRole('checkbox', { name: 'gpt-one' }))
  expect(screen.getByLabelText('Selected models')).toHaveTextContent(
    'manual-model,gpt-one'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Search models...' }),
    'gpt-'
  )
  await user.click(screen.getByRole('button', { name: 'Invert selection' }))
  // gpt-one (visible, selected) is removed; gpt-two (visible, unselected) is added;
  // the hidden manual-model is untouched.
  expect(screen.getByLabelText('Selected models')).toHaveTextContent(
    'manual-model,gpt-two'
  )
})

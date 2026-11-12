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
import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test, vi } from 'vitest'

import { createAppQueryClient } from '@/lib/query-client'

import { Checkin } from '../index'

vi.mock('../api', () => ({
  getCheckinSites: vi.fn(async () => ({
    success: true,
    data: [
      {
        site: 'qoder',
        name: 'Qoder',
        mode: 'api',
        icon: 'qoder',
        cred_fields: [
          { key: 'token', label: 'Token', type: 'password', required: true },
        ],
      },
    ],
  })),
  getCheckinAccounts: vi.fn(async () => ({ success: true, data: [] })),
  createCheckinAccount: vi.fn(async () => ({ success: true })),
  deleteCheckinAccount: vi.fn(async () => ({ success: true })),
  runCheckinAccount: vi.fn(async () => ({ success: true })),
  setCheckinAccountEnabled: vi.fn(async () => ({ success: true })),
}))

vi.mock('@/features/my-channels/api', () => ({
  getChannels: vi.fn(async () => ({ success: true, data: { items: [] } })),
}))

function renderCheckin() {
  const client = createAppQueryClient()
  return render(
    <QueryClientProvider client={client}>
      <Checkin />
    </QueryClientProvider>
  )
}

describe('Check-in Center add-account dialog', () => {
  test('opens from the header action button', async () => {
    const user = userEvent.setup()
    renderCheckin()

    const buttons = await screen.findAllByRole('button', {
      name: /Add account/i,
    })
    expect(buttons.length).toBeGreaterThan(0)
    await user.click(buttons[0])

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeNull()
    })
    expect(await screen.findByText(/Add check-in account/i)).toBeTruthy()
  })

  test('opens from the empty-state button', async () => {
    const user = userEvent.setup()
    renderCheckin()

    const buttons = await screen.findAllByRole('button', {
      name: /Add account/i,
    })
    const last = buttons.at(-1)
    expect(last).toBeTruthy()
    await user.click(last as HTMLElement)

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeNull()
    })
  })
})

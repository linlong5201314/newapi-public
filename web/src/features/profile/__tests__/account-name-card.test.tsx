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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { updateUserProfile } from '../api'
import { AccountNameCard } from '../components/account-name-card'
import type { UserProfile } from '../types'

vi.mock('../api', () => ({ updateUserProfile: vi.fn() }))

const updateUserProfileMock = vi.mocked(updateUserProfile)

const profile: UserProfile = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 1,
  group: 'default',
  quota: 0,
  used_quota: 0,
  request_count: 0,
  status: 1,
}

function renderCard() {
  const onProfileUpdate = vi.fn()
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'alice',
    display_name: 'Alice',
    role: 1,
  })
  render(
    <AccountNameCard profile={profile} onProfileUpdate={onProfileUpdate} />
  )
  return onProfileUpdate
}

afterEach(() => {
  updateUserProfileMock.mockReset()
})

it('sends only the changed name fields', async () => {
  updateUserProfileMock.mockResolvedValue({ success: true })
  const onProfileUpdate = renderCard()

  fireEvent.change(screen.getByLabelText('Username'), {
    target: { value: 'alice-2' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))

  await waitFor(() =>
    expect(updateUserProfileMock).toHaveBeenCalledWith({ username: 'alice-2' })
  )
  expect(onProfileUpdate).toHaveBeenCalled()
  expect(useAuthStore.getState().auth.user?.username).toBe('alice-2')
})

it('disables saving when the username is blank', () => {
  renderCard()

  fireEvent.change(screen.getByLabelText('Username'), {
    target: { value: '   ' },
  })
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  expect(updateUserProfileMock).not.toHaveBeenCalled()
})

it('does not refresh the profile when the server rejects the rename', async () => {
  updateUserProfileMock.mockResolvedValue({
    success: false,
    message: 'Username already exists or has been deleted',
  })
  const onProfileUpdate = renderCard()

  fireEvent.change(screen.getByLabelText('Username'), {
    target: { value: 'taken' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))

  await waitFor(() => expect(updateUserProfileMock).toHaveBeenCalled())
  expect(onProfileUpdate).not.toHaveBeenCalled()
  expect(useAuthStore.getState().auth.user?.username).toBe('alice')
})

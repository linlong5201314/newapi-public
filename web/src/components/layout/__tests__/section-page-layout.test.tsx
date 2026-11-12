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
import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import { SectionPageLayout } from '../components/section-page-layout'

describe('SectionPageLayout', () => {
  test('renders named slots and keeps non-slot children mounted', () => {
    render(
      <SectionPageLayout>
        <SectionPageLayout.Title>Page title</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <button type='button'>Header action</button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <p>Body content</p>
        </SectionPageLayout.Content>
        <div data-testid='overlay'>Overlay mount point</div>
      </SectionPageLayout>
    )

    expect(screen.getByText('Page title')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Header action' })).toBeTruthy()
    expect(screen.getByText('Body content')).toBeTruthy()
    // Regression: overlays (dialogs/sheets) declared next to Content used to be
    // dropped by the slot whitelist, which made every page-level dialog inert.
    expect(screen.getByTestId('overlay')).toBeTruthy()
  })
})

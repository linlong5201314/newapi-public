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
import { Loader2, UserPen } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { TitledCard } from '@/components/ui/titled-card'
import { handleServerError } from '@/lib/handle-server-error'
import { createServerError } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { updateUserProfile } from '../api'
import type { UserProfile } from '../types'

type AccountNameCardProps = {
  profile: UserProfile | null
  onProfileUpdate: () => void
}

/**
 * Renames the signed-in account. The username is the sign-in identifier, so it
 * is limited to the same length the server accepts and is saved on its own
 * request; the display name stays free-form.
 */
export function AccountNameCard(props: AccountNameCardProps) {
  const { t } = useTranslation()
  const { auth } = useAuthStore()
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [saving, setSaving] = useState(false)

  const savedUsername = props.profile?.username ?? ''
  const savedDisplayName = props.profile?.display_name ?? ''

  useEffect(() => {
    setUsername(savedUsername)
    setDisplayName(savedDisplayName)
  }, [savedUsername, savedDisplayName])

  const trimmedUsername = username.trim()
  const trimmedDisplayName = displayName.trim()
  const usernameChanged = trimmedUsername !== savedUsername
  const displayNameChanged = trimmedDisplayName !== savedDisplayName
  const canSave =
    !saving &&
    trimmedUsername !== '' &&
    (usernameChanged || displayNameChanged)

  const handleSave = async () => {
    if (!canSave) return
    setSaving(true)
    try {
      const payload: { display_name?: string; username?: string } = {}
      if (usernameChanged) payload.username = trimmedUsername
      if (displayNameChanged) payload.display_name = trimmedDisplayName
      const response = await updateUserProfile(payload)
      if (!response.success) {
        throw createServerError(response, t('Failed to update profile'))
      }
      if (auth.user) {
        auth.setUser({
          ...auth.user,
          username: trimmedUsername,
          display_name: trimmedDisplayName,
        })
      }
      props.onProfileUpdate()
      toast.success(t('Profile updated successfully'))
    } catch (error) {
      handleServerError(error, t('Failed to update profile'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <TitledCard
      title={t('Account Name')}
      description={t('Change the name shown across the site')}
      icon={<UserPen className='h-4 w-4' />}
      iconTone='info'
      disableHoverEffect
    >
      <form
        className='flex flex-col gap-4'
        onSubmit={(event) => {
          event.preventDefault()
          void handleSave()
        }}
      >
        <div className='grid gap-4 sm:grid-cols-2'>
          <div className='space-y-2'>
            <Label htmlFor='profile-username'>{t('Username')}</Label>
            <Input
              id='profile-username'
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              maxLength={20}
              autoComplete='username'
              disabled={saving}
            />
            <p className='text-muted-foreground text-xs'>
              {t('Used to sign in. Letters, numbers and symbols, up to 20.')}
            </p>
          </div>
          <div className='space-y-2'>
            <Label htmlFor='profile-display-name'>{t('Display Name')}</Label>
            <Input
              id='profile-display-name'
              value={displayName}
              onChange={(event) => setDisplayName(event.target.value)}
              placeholder={t('Leave empty to use username')}
              maxLength={20}
              disabled={saving}
            />
            <p className='text-muted-foreground text-xs'>
              {t('Shown on your profile and in place of the username.')}
            </p>
          </div>
        </div>
        <div className='flex items-center gap-2'>
          <Button type='submit' disabled={!canSave}>
            {saving && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            {t('Save')}
          </Button>
          {!usernameChanged && !displayNameChanged && (
            <span className='text-muted-foreground text-xs'>
              {t('No changes to save')}
            </span>
          )}
        </div>
      </form>
    </TitledCard>
  )
}

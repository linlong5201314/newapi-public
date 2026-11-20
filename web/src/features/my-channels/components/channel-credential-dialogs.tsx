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

// ============================================================================
// [M3-FE] Credential-assist dialogs for account-login channel types.
//
// WorkBuddy: device-authorization flow (start → open authUrl → poll).
// Trae: reuse the local-helper authorization flow. The server creates the
// channel after account identity confirmation; no credential key is disclosed.
// Contract: docs/M3-2api与签到中心设计.md §2. All wire shapes are normalized
// defensively because the backend lands in parallel.
// ============================================================================

import {
  AlertTriangle,
  Check,
  Copy,
  ExternalLink,
  Loader2,
  RefreshCw,
} from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ProviderAuthorizationCard } from '@/features/account-bindings/provider-authorization'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { pollChannelOAuth, startChannelOAuth } from '../api'

const POLL_INTERVAL_MS = 2500

function asRecord(value: unknown): Record<string, unknown> | null {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return null
}

/**
 * Pull the serialized credential out of a poll/import response.
 *
 * Backend contract (controller/channel_self_account.go): both the OAuth poll
 * and the Trae import endpoints return `{done, credential: <masked preview>,
 * key: <full serialized JSON>}`. The masked `credential` is display-only, so
 * the full `key` string always wins. `credentials`/`credential` objects and an
 * `importToken` handle are accepted as fail-soft fallbacks.
 */
function extractCredentialJson(data: unknown): string {
  const record = asRecord(data)
  if (!record) return ''
  if (typeof record.key === 'string' && record.key.trim()) {
    return record.key
  }
  const credentials =
    asRecord(record.credentials) ?? asRecord(record.credential)
  if (credentials) return JSON.stringify(credentials)
  const importToken = record.importToken ?? record.import_token
  if (typeof importToken === 'string' && importToken.trim()) {
    return JSON.stringify({ importToken })
  }
  return ''
}

/** Normalize the OAuth session descriptor from the start endpoint. */
function extractOAuthSession(data: unknown): {
  sessionId: string
  authUrl: string
  expiresIn: number
} | null {
  const record = asRecord(data)
  if (!record) return null
  const sessionId = record.sessionId ?? record.session_id
  const authUrl = record.authUrl ?? record.auth_url
  const expiresIn = record.expiresIn ?? record.expires_in
  if (typeof sessionId !== 'string' || typeof authUrl !== 'string') {
    return null
  }
  return {
    sessionId,
    authUrl,
    expiresIn: typeof expiresIn === 'number' ? expiresIn : 600,
  }
}

type OAuthPhase =
  | 'starting'
  | 'waiting'
  | 'success'
  | 'expired'
  | 'error'
  | 'channel-created'

type WorkbuddyOAuthDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Called with the serialized credential JSON to backfill the key field. */
  onCredentials: (json: string) => void
  /** Called when the backend already created the channel server-side. */
  onChannelCreated?: () => void
}

export function WorkbuddyOAuthDialog(props: WorkbuddyOAuthDialogProps) {
  const { t } = useTranslation()
  const { copyToClipboard } = useCopyToClipboard()
  const [phase, setPhase] = useState<OAuthPhase>('starting')
  const [errorMessage, setErrorMessage] = useState('')
  const [session, setSession] = useState<{
    sessionId: string
    authUrl: string
    expiresIn: number
  } | null>(null)
  const [deadline, setDeadline] = useState(0)
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const settledRef = useRef(false)

  const stopPolling = useCallback(() => {
    if (pollTimerRef.current) {
      clearInterval(pollTimerRef.current)
      pollTimerRef.current = null
    }
  }, [])

  const begin = useCallback(async () => {
    stopPolling()
    settledRef.current = false
    setPhase('starting')
    setErrorMessage('')
    setSession(null)
    try {
      const result = await startChannelOAuth('workbuddy')
      if (!result.success) {
        throw new Error(result.message || t('Failed to start authorization'))
      }
      const next = extractOAuthSession(result.data)
      if (!next) {
        throw new Error(t('Invalid authorization session response'))
      }
      setSession(next)
      setDeadline(Date.now() + next.expiresIn * 1000)
      setPhase('waiting')
    } catch (error) {
      setErrorMessage(
        getServerErrorMessage(error, t('Failed to start authorization'))
      )
      setPhase('error')
    }
  }, [stopPolling, t])

  useEffect(() => {
    if (props.open) {
      void begin()
    } else {
      stopPolling()
      settledRef.current = false
    }
    return stopPolling
  }, [props.open, begin, stopPolling])

  useEffect(() => {
    if (phase !== 'waiting' || !session) return

    const poll = async () => {
      if (settledRef.current) return
      if (Date.now() > deadline) {
        settledRef.current = true
        stopPolling()
        setPhase('expired')
        return
      }
      try {
        const result = await pollChannelOAuth(session.sessionId)
        const record = asRecord(result.data)
        const status = String(record?.status ?? '').toLowerCase()
        const credentialJson = extractCredentialJson(result.data)

        if (credentialJson) {
          settledRef.current = true
          stopPolling()
          setPhase('success')
          props.onCredentials(credentialJson)
          toast.success(t('WorkBuddy account authorized'))
          return
        }
        if (asRecord(record?.channel)) {
          settledRef.current = true
          stopPolling()
          setPhase('channel-created')
          toast.success(t('WorkBuddy channel created'))
          props.onChannelCreated?.()
          return
        }
        if (status === 'expired' || status === 'error' || status === 'failed') {
          settledRef.current = true
          stopPolling()
          setPhase('expired')
          return
        }
        if (!result.success && status !== 'pending') {
          throw new Error(result.message || t('Authorization poll failed'))
        }
      } catch (error) {
        // A transient network error should not kill the session; only surface
        // it when the user is still waiting.
        if (!settledRef.current) {
          setErrorMessage(
            getServerErrorMessage(error, t('Authorization poll failed'))
          )
        }
      }
    }

    void poll()
    pollTimerRef.current = setInterval(() => {
      void poll()
    }, POLL_INTERVAL_MS)
    return stopPolling
  }, [phase, session, deadline, props, stopPolling, t])

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Authorize WorkBuddy account')}</DialogTitle>
          <DialogDescription>
            {t(
              'Open the link below and approve the device authorization. This dialog updates automatically once you finish.'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className='flex flex-col items-center gap-4 py-2'>
          {phase === 'starting' && (
            <div className='flex flex-col items-center gap-2 py-8'>
              <Loader2 className='size-6 animate-spin' />
              <p className='text-muted-foreground text-sm'>
                {t('Starting authorization session...')}
              </p>
            </div>
          )}

          {phase === 'waiting' && session && (
            <>
              <div className='rounded-lg border bg-white p-3'>
                <QRCodeSVG value={session.authUrl} size={168} />
              </div>
              <div className='flex w-full items-center gap-2'>
                <Input
                  readOnly
                  value={session.authUrl}
                  className='font-mono text-xs'
                />
                <Button
                  type='button'
                  variant='outline'
                  size='icon'
                  aria-label={t('Copy link')}
                  onClick={() => void copyToClipboard(session.authUrl)}
                >
                  <Copy className='size-4' />
                </Button>
                <Button
                  type='button'
                  variant='outline'
                  size='icon'
                  aria-label={t('Open link')}
                  onClick={() =>
                    window.open(
                      session.authUrl,
                      '_blank',
                      'noopener,noreferrer'
                    )
                  }
                >
                  <ExternalLink className='size-4' />
                </Button>
              </div>
              <p className='text-muted-foreground flex items-center gap-2 text-sm'>
                <Loader2 className='size-4 animate-spin' />
                {t('Waiting for authorization...')}
              </p>
              {errorMessage && (
                <p className='text-warning text-xs'>{errorMessage}</p>
              )}
            </>
          )}

          {phase === 'success' && (
            <div className='flex flex-col items-center gap-2 py-8'>
              <Check className='text-success size-8' />
              <p className='text-sm'>
                {t('Credential received and filled into the key field.')}
              </p>
            </div>
          )}

          {phase === 'channel-created' && (
            <div className='flex flex-col items-center gap-2 py-8'>
              <Check className='text-success size-8' />
              <p className='text-sm'>
                {t('The channel was created automatically.')}
              </p>
            </div>
          )}

          {phase === 'expired' && (
            <div className='flex flex-col items-center gap-2 py-8'>
              <AlertTriangle className='text-warning size-8' />
              <p className='text-sm'>
                {t('The authorization session expired or was rejected.')}
              </p>
            </div>
          )}

          {phase === 'error' && (
            <div className='flex flex-col items-center gap-2 py-8'>
              <AlertTriangle className='text-destructive size-8' />
              <p className='text-destructive text-sm'>{errorMessage}</p>
            </div>
          )}
        </div>

        <DialogFooter>
          {(phase === 'expired' || phase === 'error') && (
            <Button
              type='button'
              variant='outline'
              onClick={() => void begin()}
            >
              <RefreshCw className='mr-2 size-4' />
              {t('Retry')}
            </Button>
          )}
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {phase === 'success' || phase === 'channel-created'
              ? t('Done')
              : t('Cancel')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

type TraeImportDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onChannelCreated: () => void
  // When editing an existing channel the flow replaces its credential in
  // place instead of navigating away to a newly created channel.
  onCredentialReplaced?: (channelId: number) => void
  existingChannelId?: number
}

export function TraeImportDialog(props: TraeImportDialogProps) {
  const { t } = useTranslation()
  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Import Trae credential')}</DialogTitle>
          <DialogDescription>
            {t(
              'Trae domestic login requires a local helper because the official client only redirects to localhost. This is not browser-only OAuth. Python 3 is required on your computer.'
            )}
          </DialogDescription>
        </DialogHeader>
        {props.open && (
          <ProviderAuthorizationCard
            provider='trae-cn'
            onComplete={(channelId) => {
              if (props.existingChannelId) {
                props.onCredentialReplaced?.(channelId)
              } else {
                props.onChannelCreated()
              }
              props.onOpenChange(false)
            }}
          />
        )}
        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

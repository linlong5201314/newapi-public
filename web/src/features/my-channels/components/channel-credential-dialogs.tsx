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
// Trae: callback-URL paste or manual refreshToken+uid import with a redacted
// preview before the credential is written into the channel key field.
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

import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  importTraeCredential,
  pollChannelOAuth,
  startChannelOAuth,
} from '../api'

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
      setErrorMessage(getServerErrorMessage(error, t('Failed to start authorization')))
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
        if (
          status === 'expired' ||
          status === 'error' ||
          status === 'failed'
        ) {
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
                <Input readOnly value={session.authUrl} className='font-mono text-xs' />
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
                    window.open(session.authUrl, '_blank', 'noopener,noreferrer')
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
            <Button type='button' variant='outline' onClick={() => void begin()}>
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

type TraeImportPreview = {
  nickname: string
  uid: string
  maskedSummary: string
  credentialJson: string
}

type TraeImportDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Called with the serialized credential JSON to backfill the key field. */
  onCredentials: (json: string) => void
}

export function TraeImportDialog(props: TraeImportDialogProps) {
  const { t } = useTranslation()
  const [tab, setTab] = useState<'callback' | 'manual'>('callback')
  const [callbackUrl, setCallbackUrl] = useState('')
  const [refreshToken, setRefreshToken] = useState('')
  const [uid, setUid] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')
  const [preview, setPreview] = useState<TraeImportPreview | null>(null)

  useEffect(() => {
    if (!props.open) {
      setCallbackUrl('')
      setRefreshToken('')
      setUid('')
      setErrorMessage('')
      setPreview(null)
      setSubmitting(false)
      setTab('callback')
    }
  }, [props.open])

  const handleImport = async () => {
    setErrorMessage('')
    const payload =
      tab === 'callback'
        ? { callbackUrl: callbackUrl.trim() }
        : { refreshToken: refreshToken.trim(), uid: uid.trim() }
    if (tab === 'callback' && !payload.callbackUrl) {
      setErrorMessage(t('Please paste the callback URL'))
      return
    }
    if (tab === 'manual' && (!payload.refreshToken || !payload.uid)) {
      setErrorMessage(t('Please fill in both the refresh token and the uid'))
      return
    }
    setSubmitting(true)
    try {
      const result = await importTraeCredential(payload)
      if (!result.success) {
        throw new Error(result.message || t('Failed to import credential'))
      }
      const record = asRecord(result.data)
      const credentialJson = extractCredentialJson(result.data)
      if (!credentialJson) {
        throw new Error(t('Invalid credential preview response'))
      }
      const credentials =
        asRecord(record?.credentials) ?? asRecord(record?.credential) ?? record
      const nickname =
        typeof credentials?.nickname === 'string' ? credentials.nickname : ''
      let resolvedUid = payload.uid || ''
      if (typeof credentials?.uid === 'string') {
        resolvedUid = credentials.uid
      } else if (typeof record?.uid === 'string') {
        resolvedUid = record.uid
      }
      const maskedRefresh =
        typeof credentials?.refreshToken === 'string'
          ? (credentials.refreshToken as string)
          : ''
      setPreview({
        nickname,
        uid: resolvedUid,
        maskedSummary: maskedRefresh,
        credentialJson,
      })
    } catch (error) {
      setErrorMessage(getServerErrorMessage(error, t('Failed to import credential')))
    } finally {
      setSubmitting(false)
    }
  }

  const handleConfirm = () => {
    if (!preview) return
    props.onCredentials(preview.credentialJson)
    toast.success(t('Trae credential filled into the key field'))
    props.onOpenChange(false)
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Import Trae credential')}</DialogTitle>
          <DialogDescription>
            {t(
              'Log in to trae.cn in this browser, copy the redirect callback URL, and paste it below — or enter the refresh token manually.'
            )}
          </DialogDescription>
        </DialogHeader>

        <Alert className='border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-50'>
          <AlertTriangle className='h-4 w-4' />
          <AlertDescription>
            {t(
              'Refreshing the token signs out every other Trae client logged in with the same account.'
            )}
          </AlertDescription>
        </Alert>

        {!preview && (
          <Tabs
            value={tab}
            onValueChange={(value) => setTab(value as 'callback' | 'manual')}
          >
            <TabsList className='w-full'>
              <TabsTrigger value='callback' className='flex-1'>
                {t('Paste callback URL')}
              </TabsTrigger>
              <TabsTrigger value='manual' className='flex-1'>
                {t('Manual refresh token')}
              </TabsTrigger>
            </TabsList>
            <TabsContent value='callback' className='space-y-2 pt-3'>
              <Label htmlFor='trae-callback-url'>{t('Callback URL')}</Label>
              <Input
                id='trae-callback-url'
                value={callbackUrl}
                onChange={(event) => setCallbackUrl(event.target.value)}
                placeholder='http://localhost:8080/...?refreshToken=...&userJwt=...'
                className='font-mono text-xs'
              />
              <p className='text-muted-foreground text-xs'>
                {t(
                  'After logging in, the browser is redirected to a localhost address. Copy that full URL here.'
                )}
              </p>
            </TabsContent>
            <TabsContent value='manual' className='space-y-3 pt-3'>
              <div className='space-y-2'>
                <Label htmlFor='trae-refresh-token'>{t('Refresh token')}</Label>
                <Input
                  id='trae-refresh-token'
                  type='password'
                  value={refreshToken}
                  onChange={(event) => setRefreshToken(event.target.value)}
                />
              </div>
              <div className='space-y-2'>
                <Label htmlFor='trae-uid'>{t('UID')}</Label>
                <Input
                  id='trae-uid'
                  value={uid}
                  onChange={(event) => setUid(event.target.value)}
                />
              </div>
            </TabsContent>
          </Tabs>
        )}

        {preview && (
          <div className='flex flex-col gap-2 rounded-lg border p-4'>
            <p className='text-sm'>
              <span className='text-muted-foreground'>{t('Account')}:</span>{' '}
              <span className='font-medium'>
                {preview.nickname || t('(nickname unknown)')}
              </span>
            </p>
            {preview.uid && (
              <p className='text-sm'>
                <span className='text-muted-foreground'>{t('UID')}:</span>{' '}
                <span className='font-mono'>{preview.uid}</span>
              </p>
            )}
            {preview.maskedSummary && (
              <p className='text-sm break-all'>
                <span className='text-muted-foreground'>
                  {t('Refresh token')}:
                </span>{' '}
                <span className='font-mono text-xs'>{preview.maskedSummary}</span>
              </p>
            )}
          </div>
        )}

        {errorMessage && (
          <p className='text-destructive text-sm'>{errorMessage}</p>
        )}

        <DialogFooter>
          {preview ? (
            <>
              <Button
                type='button'
                variant='outline'
                onClick={() => setPreview(null)}
                disabled={submitting}
              >
                {t('Back')}
              </Button>
              <Button type='button' onClick={handleConfirm}>
                <Check className='mr-2 size-4' />
                {t('Confirm and fill credential')}
              </Button>
            </>
          ) : (
            <>
              <Button
                type='button'
                variant='outline'
                onClick={() => props.onOpenChange(false)}
                disabled={submitting}
              >
                {t('Cancel')}
              </Button>
              <Button
                type='button'
                onClick={() => void handleImport()}
                disabled={submitting}
              >
                {submitting && <Loader2 className='mr-2 size-4 animate-spin' />}
                {t('Import and preview')}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

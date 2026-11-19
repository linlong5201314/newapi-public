import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  cancelNativeAuthorization,
  confirmTraeBridgeAccount,
  downloadProviderHelper,
  getProviderCapabilities,
  importKimiWorkCredentials,
  pollNativeAuthorization,
  startNativeAuthorization,
  type NativeAccountProvider,
  type NativeAuthorizationResult,
  type NativeAuthorizationSession,
  type ProviderAuthorizationOptions,
} from './provider-authorization-api'

type Props = {
  provider: NativeAccountProvider
  options?: ProviderAuthorizationOptions
  disabled?: boolean
  onComplete?: (channelId: number) => void
}
type Pending = {
  provider: 'trae-cn' | 'xiaomi-mimo'
  session: NativeAuthorizationSession
  expiresAt: number
}

export function ProviderAuthorizationCard(props: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [pending, setPending] = useState<Pending | null>(null)
  const [consent, setConsent] = useState<NativeAuthorizationResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')
  const [status, setStatus] = useState('')
  const [rotationConfirmed, setRotationConfirmed] = useState(false)
  const [importFile, setImportFile] = useState<File | null>(null)
  const [completed, setCompleted] = useState<number | null>(null)
  const generation = useRef(0)
  const inFlight = useRef(false)
  const pendingRef = useRef<Pending | null>(null)
  const popup = useRef<Window | null>(null)
  const onComplete = useRef(props.onComplete)
  onComplete.current = props.onComplete

  const capability = useQuery({
    queryKey: ['account-capabilities', props.provider],
    queryFn: ({ signal }) => getProviderCapabilities(props.provider, signal),
    enabled: props.provider !== 'trae-cn',
    retry: false,
    meta: { errorToast: false },
  })

  useEffect(() => {
    const requests = generation
    return () => {
      requests.current++
      popup.current?.close()
      const active = pendingRef.current
      pendingRef.current = null
      if (active)
        {void cancelNativeAuthorization(
          active.provider,
          active.session.sessionId
        ).catch(() => undefined)}
    }
  }, [props.provider])

  const finish = (result: NativeAuthorizationResult) => {
    if (!result.done || !result.channel?.id)
      {throw new Error(t('Authorization did not return a created channel'))}
    pendingRef.current = null
    setPending(null)
    setConsent(null)
    popup.current?.close()
    setCompleted(result.channel.id)
    setImportFile(null)
    void queryClient.invalidateQueries({ queryKey: ['my-channels'] })
    onComplete.current?.(result.channel.id)
  }
  const finishRef = useRef(finish)
  finishRef.current = finish

  useEffect(() => {
    if (!pending || consent) return
    let active = true
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      try {
        if (Date.now() >= pending.expiresAt)
          {throw new Error(t('Authorization session expired'))}
        const result = await pollNativeAuthorization(
          pending.provider,
          pending.session.sessionId
        )
        if (!active) return
        if (result.done) {
          finishRef.current(result)
          return
        }
        if (result.status === 'awaiting_consent' && result.requiresConsent) {
          if (
            pending.provider !== 'trae-cn' ||
            !result.account?.uid ||
            !result.consentToken
          )
            {throw new Error(t('Invalid authorization session response'))}
          setConsent(result)
          setStatus(t('Verify the account identity below before connecting it'))
          return
        }
        timer = setTimeout(
          () => {
            void poll()
          },
          Math.max(2, result.interval ?? pending.session.interval ?? 2) * 1000
        )
      } catch (error) {
        if (!active) return
        pendingRef.current = null
        setPending(null)
        popup.current?.close()
        setStatus('')
        setProblem(getServerErrorMessage(error, t('Authorization poll failed')))
        void cancelNativeAuthorization(
          pending.provider,
          pending.session.sessionId
        ).catch(() => undefined)
      }
    }
    timer = setTimeout(
      () => {
        void poll()
      },
      Math.max(2, pending.session.interval ?? 2) * 1000
    )
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [pending, consent, t])

  // Identity approval is time limited too, not just the polling phase.
  useEffect(() => {
    if (!pending) return
    const timer = setTimeout(
      () => {
        generation.current++
        pendingRef.current = null
        setPending(null)
        setConsent(null)
        popup.current?.close()
        setProblem(t('Authorization session expired'))
        void cancelNativeAuthorization(
          pending.provider,
          pending.session.sessionId
        ).catch(() => undefined)
      },
      Math.max(0, pending.expiresAt - Date.now())
    )
    return () => clearTimeout(timer)
  }, [pending, t])

  const begin = async () => {
    if (inFlight.current || props.disabled || props.provider === 'kimi-work')
      {return}
    if (props.provider === 'trae-cn' && !rotationConfirmed) return
    if (
      props.provider === 'xiaomi-mimo' &&
      (capability.data?.method !== 'xiaomi_qr' ||
        !capability.data.browserLoginAvailable)
    )
      {return}
    const provider = props.provider
    const request = ++generation.current
    // Only MiMo uses a browser window; Trae's local helper opens official login.
    const reserved =
      provider === 'xiaomi-mimo' ? window.open('about:blank', '_blank') : null
    if (reserved) reserved.opener = null
    popup.current = reserved
    inFlight.current = true
    setBusy(true)
    setProblem('')
    setStatus(t('Creating authorization session...'))
    try {
      if (provider === 'xiaomi-mimo' && (!reserved || reserved.closed))
        {throw new Error(
          t(
            'Your browser blocked the login window. Allow popups and try again.'
          )
        )}
      const session = await startNativeAuthorization(
        provider,
        props.options ?? {}
      )
      if (request !== generation.current || (reserved && reserved.closed)) {
        reserved?.close()
        await cancelNativeAuthorization(provider, session.sessionId)
        return
      }
      const current = {
        provider,
        session,
        expiresAt: Date.now() + session.expiresIn * 1000,
      }
      pendingRef.current = current
      if (!session.sessionId || !(session.expiresIn > 0))
        {throw new Error(t('Invalid authorization session response'))}
      if (provider === 'xiaomi-mimo') {
        const url = new URL(session.authUrl ?? '')
        if (
          url.protocol !== 'https:' ||
          !['account.xiaomi.com', 'ak.account.xiaomi.com'].includes(url.host) ||
          url.username ||
          url.password ||
          url.hash
        )
          {throw new Error(t('Invalid authorization session response'))}
        if (reserved) reserved.location.href = url.href
        setStatus(
          t(
            'Complete Xiaomi sign-in in the opened window. This page creates your channel after approval.'
          )
        )
      } else {
        await downloadProviderHelper('trae-cn', session.sessionId)
        if (request !== generation.current) return
        setStatus(
          t(
            'Run the downloaded helper once, sign in on the official Trae page, then confirm the account identity here.'
          )
        )
      }
      setPending(current)
    } catch (error) {
      reserved?.close()
      if (request === generation.current) {
        const current = pendingRef.current
        pendingRef.current = null
        if (current)
          {void cancelNativeAuthorization(
            current.provider,
            current.session.sessionId
          ).catch(() => undefined)}
        setPending(null)
        setProblem(
          getServerErrorMessage(error, t('Failed to start authorization'))
        )
        setStatus('')
      }
    } finally {
      if (request === generation.current) {
        inFlight.current = false
        setBusy(false)
      }
    }
  }

  const cancel = async () => {
    generation.current++
    inFlight.current = false
    setBusy(false)
    const current = pendingRef.current
    pendingRef.current = null
    setPending(null)
    setConsent(null)
    popup.current?.close()
    setStatus(t('Account login was cancelled. Start again'))
    if (current) {
      try {
        await cancelNativeAuthorization(
          current.provider,
          current.session.sessionId
        )
      } catch (error) {
        setProblem(getServerErrorMessage(error))
      }
    }
  }

  const approveIdentity = async () => {
    if (
      inFlight.current ||
      !pending ||
      !consent?.account?.uid ||
      !consent.consentToken
    )
      {return}
    const request = generation.current
    inFlight.current = true
    setBusy(true)
    setProblem('')
    try {
      const result = await confirmTraeBridgeAccount(
        pending.session.sessionId,
        consent.consentToken,
        consent.account.uid
      )
      if (request === generation.current) finish(result)
    } catch (error) {
      if (request === generation.current)
        {setProblem(getServerErrorMessage(error))}
    } finally {
      if (request === generation.current) {
        inFlight.current = false
        setBusy(false)
      }
    }
  }

  const runKimiAction = async (download: boolean) => {
    if (inFlight.current || props.disabled || !capability.data?.importAvailable)
      {return}
    const request = ++generation.current
    inFlight.current = true
    setBusy(true)
    setProblem('')
    try {
      if (download) {
        await downloadProviderHelper('kimi-work')
        return
      }
      if (!importFile || importFile.size > 1024 * 1024)
        {throw new Error(
          t('Choose a Kimi Work export JSON file smaller than 1 MB')
        )}
      const credentials: unknown = JSON.parse(await importFile.text())
      if (
        !credentials ||
        typeof credentials !== 'object' ||
        Array.isArray(credentials)
      )
        {throw new Error(
          t('Choose a valid Kimi Work credential export JSON object')
        )}
      if (request !== generation.current) return
      const result = await importKimiWorkCredentials(
        credentials as Record<string, unknown>,
        props.options ?? {}
      )
      if (request === generation.current) finish(result)
    } catch (error) {
      if (request === generation.current)
        {setProblem(
          getServerErrorMessage(
            error,
            t('Failed to import account credentials')
          )
        )}
    } finally {
      if (request === generation.current) {
        inFlight.current = false
        setBusy(false)
      }
    }
  }

  if (props.provider !== 'trae-cn' && capability.isPending)
    {return (
      <LoadingState
        inline
        message={t('Loading authorization capabilities...')}
      />
    )}
  if (props.provider !== 'trae-cn' && capability.isError)
    {return (
      <ErrorState
        className='min-h-0'
        description={getServerErrorMessage(capability.error)}
        onRetry={() => {
          void capability.refetch()
        }}
      />
    )}

  return (
    <section
      className='bg-muted/50 space-y-3 rounded-lg border p-3 text-sm'
      aria-label={t('Provider account authorization')}
    >
      {completed ? (
        <p role='status'>
          {t('Account login completed; channel #{{id}} created', {
            id: completed,
          })}
        </p>
      ) : (
        <>
          {props.provider === 'kimi-work' ? (
            <>
              <p>
                {t(
                  'Kimi Work reads an existing login from the official desktop app on your computer. A website login, Kimi Code API key or browser cookie is not a Work credential.'
                )}
              </p>
              {capability.data?.method === 'local_import' &&
              capability.data.importAvailable ? (
                <>
                  <p>
                    {t(
                      'Sign in to Work in the official Kimi desktop app. Download and review the export helper, run it locally, then choose its minimal JSON export below. The file contains credentials; do not share it and delete it after import.'
                    )}
                  </p>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={busy || props.disabled}
                    onClick={() => {
                      void runKimiAction(true)
                    }}
                  >
                    {t('Download Kimi Work export helper')}
                  </Button>
                  <p className='font-mono text-xs'>
                    powershell -NoProfile -File .\kimi-work-export.ps1
                  </p>
                  <Label htmlFor='kimi-work-credential-file'>
                    {t('Kimi Work credential export JSON')}
                  </Label>
                  <Input
                    id='kimi-work-credential-file'
                    type='file'
                    accept='.json,application/json'
                    disabled={busy || props.disabled}
                    onChange={(event) =>
                      setImportFile(event.target.files?.[0] ?? null)
                    }
                  />
                  <Button
                    type='button'
                    disabled={busy || props.disabled || !importFile}
                    onClick={() => {
                      void runKimiAction(false)
                    }}
                  >
                    {t('Import Kimi Work and create channel')}
                  </Button>
                </>
              ) : (
                <p role='status'>
                  {t(
                    'Kimi Work local credential import is not available on this server. Ask the administrator to enable the supported desktop import protocol.'
                  )}
                </p>
              )}
            </>
          ) : (
            <>
              {props.provider === 'trae-cn' ? (
                <>
                  <p>
                    {t(
                      'Trae domestic login requires a local helper because the official client only redirects to localhost. This is not browser-only OAuth. Python 3 is required on your computer.'
                    )}
                  </p>
                  <p>
                    {t(
                      'Download and review the helper, run this command from its download folder, then sign in. It uploads only this one-time authorization result, not your dashboard session.'
                    )}
                  </p>
                  <div className='flex items-center gap-2'>
                    <code>python -I trae-login-helper.py</code>
                    <CopyButton
                      value='python -I trae-login-helper.py'
                      aria-label={t('Copy Trae helper command')}
                    />
                  </div>
                  <Label className='flex items-start gap-2'>
                    <Checkbox
                      checked={rotationConfirmed}
                      disabled={busy || Boolean(pending)}
                      onCheckedChange={(checked) =>
                        setRotationConfirmed(checked === true)
                      }
                    />
                    {t(
                      'I understand that this local Trae login may rotate tokens and sign out other clients.'
                    )}
                  </Label>
                </>
              ) : (
                <p>
                  {t(
                    'Sign in with your own Xiaomi account or scan Xiaomi’s QR code. MiMo app membership and Token Plan permissions remain controlled by Xiaomi.'
                  )}
                </p>
              )}
              {!pending && (
                <Button
                  type='button'
                  disabled={
                    busy ||
                    props.disabled ||
                    (props.provider === 'trae-cn' && !rotationConfirmed) ||
                    (props.provider === 'xiaomi-mimo' &&
                      !capability.data?.browserLoginAvailable)
                  }
                  onClick={() => {
                    void begin()
                  }}
                >
                  {props.provider === 'trae-cn'
                    ? t('Download helper and start Trae domestic login')
                    : t('Authorize Xiaomi MiMo account')}
                </Button>
              )}
              {props.provider === 'xiaomi-mimo' &&
                !capability.data?.browserLoginAvailable && (
                  <p role='status'>
                    {t(
                      'Xiaomi account authorization is not available on this server. No browser login URL will be invented.'
                    )}
                  </p>
                )}
              {consent?.account && (
                <div className='space-y-2 rounded border p-3'>
                  <p>
                    {t('Confirm this is your Trae account before connecting')}
                  </p>
                  <p>
                    {consent.account.nickname} · {consent.account.uid}
                  </p>
                  <Button
                    type='button'
                    disabled={busy || props.disabled}
                    onClick={() => {
                      void approveIdentity()
                    }}
                  >
                    {t('Confirm and connect this account')}
                  </Button>
                </div>
              )}
              {(pending || busy) && (
                <Button
                  type='button'
                  variant='outline'
                  onClick={() => {
                    void cancel()
                  }}
                >
                  {t('Cancel authorization')}
                </Button>
              )}
            </>
          )}
          {status && <p role='status'>{status}</p>}
        </>
      )}
      {problem && (
        <p role='alert' className='text-destructive'>
          {problem}
        </p>
      )}
    </section>
  )
}

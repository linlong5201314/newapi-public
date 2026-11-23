import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  getServerErrorMessage,
  getServerErrorStatus,
} from '@/lib/server-error-message'

import { AccountCredentialImport } from './account-credential-import'
import {
  cancelBrowserAuthorization,
  completeNativeAuthorization,  confirmBrowserAccount,
  pollBrowserAuthorization,
  startBrowserAuthorization,
  type BrowserAccountProvider,
  type NativeAuthorizationResult,
  type NativeAuthorizationSession,
  type ProviderAuthorizationOptions,
} from './provider-authorization-api'

export type WebAccountProvider = Exclude<BrowserAccountProvider, 'xiaomi-mimo'>
type Props = {
  provider: WebAccountProvider
  options?: ProviderAuthorizationOptions
  disabled?: boolean
  onComplete?: (id: number) => void
}

function isAllowedAccountLogin(provider: WebAccountProvider, raw: string) {
  try {
    if (raw.length > 16384 || /[\r\n]/.test(raw)) return false
    const u = new URL(raw)
    if (
      u.protocol !== 'https:' ||
      u.username ||
      u.password ||
      u.port ||
      u.hash ||
      /^https:\/\/[^/]+:\d+/.test(raw)
    ) {
      return false
    }
    if (provider === 'trae-cn') {
      return (
        ['www.trae.cn', 'trae.cn'].includes(u.hostname) &&
        u.pathname === '/authorization'
      )
    }
    if (provider === 'raccoon') {
      return (
        u.hostname === 'xiaohuanxiong.com' && u.pathname === '/code/authorize'
      )
    }
    if (provider === 'cline-account') {
      return u.hostname === 'authkit.cline.bot' && u.pathname === '/device'
    }
    if (provider === 'qoder-cn') {
      return (
        ['qoder.cn', 'qoder.com.cn'].includes(u.hostname) &&
        u.pathname === '/device/selectAccounts'
      )
    }
    if (provider === 'qoder-global') {
      return (
        u.hostname === 'qoder.com' && u.pathname === '/device/selectAccounts'
      )
    }
    if (provider === 'nous-portal') {
      return u.hostname === 'portal.nousresearch.com'
    }
    return (
      (u.hostname === 'bigmodel.cn' && u.pathname === '/login') ||
      (u.hostname === 'chat.z.ai' && u.pathname === '/api/oauth/authorize') ||
      (u.hostname === 'zcode.z.ai' && u.pathname === '/app/oauth/login')
    )
  } catch {
    return false
  }
}

export function BrowserAccountConnection({
  provider,
  options,
  disabled,
  onComplete,
}: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [session, setSession] = useState<NativeAuthorizationSession | null>(
    null
  )
  const [problem, setProblem] = useState('')
  const [status, setStatus] = useState('')
  const [busy, setBusy] = useState(false)
  const [returned, setReturned] = useState('')
  const [callbackPending, setCallbackPending] = useState(false)
  const [consent, setConsent] = useState<NativeAuthorizationResult | null>(null)
  const [rotation, setRotation] = useState(false)
  const [created, setCreated] = useState<number | null>(null)
  const current = useRef<{
    provider: WebAccountProvider
    session: NativeAuthorizationSession
    expires: number
  } | null>(null)
  const generation = useRef(0)
  const popup = useRef<Window | null>(null)
  const activeRequest = useRef(false)
  const callback = useRef(onComplete)
  callback.current = onComplete
  const native =
    typeof navigator !== 'undefined' &&
    navigator.userAgent.includes('DeepSeekNewAPIClient/')
  const finish = (result: NativeAuthorizationResult) => {
    if (result.status === 'awaiting_consent' && result.requiresConsent) {
      if (!result.account?.uid || !result.consentToken) {
        throw new Error(t('Invalid authorization session response'))
      }
      setConsent(result)
      setStatus(t('Verify the account identity below before connecting it'))
      return
    }
    if (!result.done) return
    if (!result.channel?.id) {
      throw new Error(t('Authorization did not return a created channel'))
    }
    current.current = null
    setSession(null)
    setConsent(null)
    setReturned('')
    setCreated(result.channel.id)
    popup.current?.close()
    void queryClient.invalidateQueries({ queryKey: ['my-channels'] })
    callback.current?.(result.channel.id)
  }
  const finishRef = useRef(finish)
  finishRef.current = finish
  useEffect(
    () => () => {
      generation.current++
      const pending = current.current
      current.current = null
      popup.current?.close()
      if (pending) {
        void cancelBrowserAuthorization(
          pending.provider,
          pending.session.sessionId
        ).catch(() => undefined)
      }
    },
    [provider]
  )
  const checkStatus = useRef<(() => void) | null>(null)
  const devicePoll =
    provider === 'cline-account' ||
    provider === 'qoder-cn' ||
    provider === 'qoder-global' ||
    provider === 'nous-portal'
  const waitsForManualCallback =
    session?.automaticCallbackSupported === false && !devicePoll
  useEffect(() => {
    if (!session || consent || (waitsForManualCallback && !callbackPending)) {
      return
    }
    let active = true
    let timer: ReturnType<typeof setTimeout>
    let interval = Math.max(2, session.interval ?? 3)
    const request = generation.current
    const poll = async () => {
      const pending = current.current
      if (!active || !pending || request !== generation.current) return
      if (activeRequest.current) {
        timer = setTimeout(() => {
          void poll()
        }, interval * 1000)
        return
      }
      activeRequest.current = true
      // 轮询不操作全局 busy：busy 只表示用户主动操作（start/complete/cancel/approve），
      // 否则回调粘贴按钮会随每次轮询一闪一闪（busy true→false→true）。
      try {
        if (Date.now() >= pending.expires) {
          throw new Error(t('Authorization session expired'))
        }
        const result = await pollBrowserAuthorization(
          provider,
          session.sessionId
        )
        if (
          !active ||
          request !== generation.current ||
          current.current !== pending
        ) {
          return
        }
        setProblem('')
        finishRef.current(result)
        if (!result.done && result.status !== 'awaiting_consent') {
          interval = Math.max(2, result.interval ?? interval)
          timer = setTimeout(() => {
            void poll()
          }, interval * 1000)
        }
      } catch (error) {
        if (
          !active ||
          request !== generation.current ||
          current.current !== pending
        ) {
          return
        }
        setProblem(getServerErrorMessage(error, t('Authorization poll failed')))
        if (
          [400, 401, 403, 404, 409, 410].includes(
            getServerErrorStatus(error) ?? 0
          )
        ) {
          current.current = null
          setSession(null)
          setConsent(null)
          setReturned('')
          setCallbackPending(false)
          setStatus('')
          return
        }
        setStatus(
          t(
            'Keep this page open and check again after completing official sign-in.'
          )
        )
        timer = setTimeout(
          () => {
            void poll()
          },
          Math.max(5, interval) * 1000
        )
      } finally {
        if (request === generation.current) {
          setBusy(false)
          activeRequest.current = false
        }
      }
    }
    checkStatus.current = () => {
      clearTimeout(timer)
      void poll()
    }
    timer = setTimeout(() => {
      void poll()
    }, interval * 1000)
    return () => {
      active = false
      clearTimeout(timer)
      checkStatus.current = null
    }
  }, [session, consent, provider, t, waitsForManualCallback, callbackPending])
  useEffect(() => {
    if (!session) return
    const timer = setTimeout(
      () => {
        const pending = current.current
        if (!pending) return
        current.current = null
        generation.current++
        activeRequest.current = false
        setBusy(false)
        setSession(null)
        setConsent(null)
        setReturned('')
        popup.current?.close()
        setProblem(t('Authorization session expired'))
        void cancelBrowserAuthorization(
          pending.provider,
          pending.session.sessionId
        ).catch(() => undefined)
      },
      Math.max(0, (current.current?.expires ?? 0) - Date.now())
    )
    return () => clearTimeout(timer)
  }, [session, t])
  const start = async () => {
    if (
      activeRequest.current ||
      disabled ||
      (provider === 'trae-cn' && !rotation)
    ) {
      return
    }
    activeRequest.current = true
    const request = ++generation.current
    setBusy(true)
    setProblem('')
    setStatus(t('Creating authorization session...'))
    setCallbackPending(false)
    const reserved = native ? null : window.open('about:blank', '_blank')
    if (reserved) reserved.opener = null
    popup.current = reserved
    try {
      const next = await startBrowserAuthorization(provider, options ?? {})
      if (request !== generation.current) {
        reserved?.close()
        await cancelBrowserAuthorization(provider, next.sessionId)
        return
      }
      current.current = {
        provider,
        session: next,
        expires: Date.now() + next.expiresIn * 1000,
      }
      if (
        !next.sessionId ||
        !Number.isFinite(next.expiresIn) ||
        next.expiresIn <= 0 ||
        next.expiresIn > 900 ||
        !isAllowedAccountLogin(provider, next.authUrl ?? '')
      ) {
        throw new Error(t('Invalid authorization session response'))
      }
      setSession(next)
      if (reserved && !reserved.closed && next.authUrl) {
        reserved.location.href = next.authUrl
      }
      if (devicePoll) {
        setStatus(
          t(
            'Complete official sign-in in the opened page. This page automatically checks the original device authorization; no callback URL is required.'
          )
        )
      } else if (next.automaticCallbackSupported === false) {
        setStatus(
          t(
            'Complete official sign-in, then paste the final callback URL below. No helper download is required.'
          )
        )
      } else {
        setStatus(
          t(
            'Complete official sign-in. This page checks the result automatically; keep the generated link as a fallback.'
          )
        )
      }
    } catch (error) {
      reserved?.close()
      if (request === generation.current) {
        const pending = current.current
        current.current = null
        setSession(null)
        if (pending) {
          void cancelBrowserAuthorization(
            provider,
            pending.session.sessionId
          ).catch(() => undefined)
        }
        setProblem(
          getServerErrorMessage(error, t('Failed to start authorization'))
        )
      }
    } finally {
      if (request === generation.current) {
        setBusy(false)
        activeRequest.current = false
      }
    }
  }
  const complete = async () => {
    if (activeRequest.current || !session) return
    activeRequest.current = true
    setBusy(true)
    setProblem('')
    const request = generation.current
    const callbackUrl = returned.trim()
    setReturned('')
    try {
      const result = await completeNativeAuthorization(
        provider,
        session.sessionId,
        callbackUrl
      )
      if (request === generation.current) {
        finishRef.current(result)
        setReturned('')
        if (!result.done && result.status !== 'awaiting_consent') {
          setCallbackPending(true)
          setStatus(
            t(
              'Authorization response received; waiting for account validation.'
            )
          )
        }
      }
    } catch (error) {
      if (request === generation.current) {
        // Completion may consume the one-use flow. Keep its original error;
        // do not replace it with a later poll's consumed/expired response.
        current.current = null
        setSession(null)
        setConsent(null)
        setCallbackPending(false)
        setStatus('')
        setProblem(getServerErrorMessage(error))
      }
    } finally {
      if (request === generation.current) {
        setBusy(false)
        activeRequest.current = false
      }
    }
  }
  const approve = async () => {
    if (
      activeRequest.current ||
      !session ||
      !consent?.account?.uid ||
      !consent.consentToken
    ) {
      return
    }
    activeRequest.current = true
    setBusy(true)
    try {
      const result = await confirmBrowserAccount(
        provider,
        session.sessionId,
        consent.consentToken,
        consent.account.uid
      )
      finishRef.current(result)
    } catch (error) {
      setProblem(getServerErrorMessage(error))
    } finally {
      setBusy(false)
      activeRequest.current = false
    }
  }
  const cancel = async () => {
    generation.current++
    activeRequest.current = false
    setBusy(false)
    const pending = current.current
    current.current = null
    setSession(null)
    setConsent(null)
    setReturned('')
    setCallbackPending(false)
    popup.current?.close()
    if (pending) {
      try {
        await cancelBrowserAuthorization(provider, pending.session.sessionId)
      } catch (error) {
        setProblem(getServerErrorMessage(error))
      }
    }
  }
  return (
    <section
      aria-label={t('Browser account connection')}
      className='bg-muted/40 space-y-3 rounded-lg border p-3 text-sm'
    >
      <p>
        {t(
          'Credentials remain encrypted on the server. Only your own channels are created; sharing is optional.'
        )}
      </p>
      {provider === 'trae-cn' && !session && (
        <Label className='flex items-start gap-2'>
          <Checkbox
            checked={rotation}
            onCheckedChange={(v) => setRotation(v === true)}
            disabled={busy}
          />
          {t(
            'I understand that this Trae login may rotate tokens and sign out other clients.'
          )}
        </Label>
      )}
      {(provider === 'trae-cn' || provider === 'raccoon') && !session && !created && (
        <AccountCredentialImport
          provider={provider}
          options={options}
          disabled={disabled}
          busy={busy}
          onComplete={finish}
        />
      )}
      {created ? (
        <p role='status'>
          {t('Account login completed; channel #{{id}} created', {
            id: created,
          })}
        </p>
      ) : (
        <>
          {!session && (
            <Button
              type='button'
              disabled={
                busy || disabled || (provider === 'trae-cn' && !rotation)
              }
              onClick={() => {
                void start()
              }}
            >
              {t('Generate authorization URL')}
            </Button>
          )}
          {session?.userCode && (
            <div className='rounded-lg border p-3'>
              <p className='text-muted-foreground text-xs'>
                {t('Enter this device code on the official page')}
              </p>
              <div className='mt-1 flex items-center gap-2'>
                <code className='bg-muted rounded px-2 py-1 font-mono text-lg tracking-widest'>
                  {session.userCode}
                </code>
                <CopyButton
                  value={session.userCode}
                  aria-label={t('Copy device code')}
                />
              </div>
            </div>
          )}
          {session?.authUrl && (
            <>
              <div className='flex items-center gap-2'>
                <a
                  className='text-primary underline'
                  href={session.authUrl}
                  target={native ? '_self' : '_blank'}
                  rel='noreferrer'
                  referrerPolicy='no-referrer'
                >
                  {t('Open official account login in your browser')}
                </a>
                <CopyButton
                  value={session.authUrl}
                  aria-label={t('Copy authorization URL')}
                />
              </div>
              <Label htmlFor={`auth-url-${provider}`}>
                {t('Generated authorization URL')}
              </Label>
              <Input
                id={`auth-url-${provider}`}
                value={session.authUrl}
                readOnly
                className='font-mono text-xs'
                autoComplete='off'
              />
              {devicePoll ? (
                !consent?.account && (
                  <Button
                    type='button'
                    variant='outline'
                    disabled={busy}
                    onClick={() => checkStatus.current?.()}
                  >
                    {t('Check authorization status')}
                  </Button>
                )
              ) : (
                <>
                  {session.automaticCallbackSupported === false && (
                    <p>
                      {t(
                        'The provider only redirects to a local client address. Copy the complete final URL even if that page cannot open, then paste it here.'
                      )}
                    </p>
                  )}
                  <Label htmlFor={`callback-${provider}`}>
                    {t('Final authorization callback URL (fallback)')}
                  </Label>
                  <Input
                    id={`callback-${provider}`}
                    type='password'
                    value={returned}
                    onChange={(event) => setReturned(event.target.value)}
                    autoComplete='off'
                    spellCheck={false}
                    disabled={busy}
                  />
                  <Button
                    type='button'
                    variant='outline'
                    disabled={busy || !returned.trim()}
                    onClick={() => {
                      void complete()
                    }}
                  >
                    {t('Validate returned URL and connect')}
                  </Button>
                  <p>
                    {t(
                      'Do not share the callback URL. It may contain a one-time login credential and is cleared after submission.'
                    )}
                  </p>
                </>
              )}
              <Button
                type='button'
                variant='ghost'
                onClick={() => {
                  void cancel()
                }}
              >
                {t('Cancel authorization')}
              </Button>
            </>
          )}
          {consent?.account && (
            <div className='space-y-2 rounded border p-2'>
              <p>
                {t('Confirm this is your account before connecting')}:{' '}
                {consent.account.nickname} · {consent.account.uid}
              </p>
              <Button
                disabled={busy}
                onClick={() => {
                  void approve()
                }}
              >
                {t('Confirm and connect this account')}
              </Button>
            </div>
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

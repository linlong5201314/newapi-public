import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  getServerErrorSources,
} from '@/lib/server-error-message'

import {
  cancelFreePiOAuth,
  consentFreePiOAuth,
  pollFreePiOAuth,
  startFreePiOAuth,
  type BindingProxyPolicy,
  type FreePiPollResult,
  type FreePiSession,
} from './api'

type FreePiAuthorizationProps = {
  onComplete?: (channelId: number) => void
  proxy?: string
  proxyPolicy?: BindingProxyPolicy
}

// Only trust a complete URL actually supplied by the device issuer. Never
// synthesize user_code links: providers may require the manual device step.
function completeVerificationUri(session: FreePiSession): string | undefined {
  if (!session.verificationUriComplete) return undefined
  try {
    const uri = new URL(session.verificationUriComplete)
    if (
      uri.origin !== 'https://github.com' ||
      uri.pathname !== '/login/device' ||
      uri.username ||
      uri.password ||
      uri.hash ||
      uri.searchParams.getAll('user_code').length !== 1 ||
      uri.searchParams.get('user_code') !== session.userCode ||
      [...uri.searchParams.keys()].some((key) => key !== 'user_code')
    ) {
      return undefined
    }
    return uri.href
  } catch {
    return undefined
  }
}

export function FreePiAuthorizationCard(props: FreePiAuthorizationProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const verification = useSecureVerification()
  const [displayName, setDisplayName] = useState('')
  const [method, setMethod] = useState<'connect' | 'device'>('connect')
  const [isPublic, setIsPublic] = useState(false)
  const [accepted, setAccepted] = useState(false)
  const [session, setSession] = useState<
    (FreePiSession & { expiresAt: number }) | null
  >(null)
  const [result, setResult] = useState<FreePiPollResult | null>(null)
  const [problem, setProblem] = useState('')
  const completedID = result?.status === 'done' ? result.channel?.id : undefined
  const onComplete = props.onComplete
  const completeUri = session ? completeVerificationUri(session) : undefined
  const browserConnect = session?.method === 'connect'
  let verificationLinkText = t('Open GitHub and enter this device code')
  if (browserConnect) {
    verificationLinkText = t('Open FreePi to sign in and Connect')
  } else if (completeUri) {
    verificationLinkText = t('Open GitHub to confirm authorization')
  }

  useEffect(() => {
    if (!completedID) return
    void queryClient.invalidateQueries({ queryKey: ['account-bindings'] })
    void queryClient.invalidateQueries({ queryKey: ['self-channels'] })
    onComplete?.(completedID)
  }, [completedID, onComplete, queryClient])

  useEffect(() => {
    if (!session || completedID) return
    const timer = setTimeout(
      () => {
        setProblem(t('FreePi authorization expired. Start again'))
        setSession(null)
      },
      Math.max(0, session.expiresAt - Date.now())
    )
    return () => clearTimeout(timer)
  }, [session, completedID, t])

  const start = useMutation({
    mutationFn: async () => {
      const payload = {
        displayName,
        public: isPublic,
        confirmPublic: isPublic,
        method,
        ...(props.proxy !== undefined ? { proxy: props.proxy } : {}),
        ...(props.proxyPolicy ? { proxy_policy: props.proxyPolicy } : {}),
      }
      try {
        return await startFreePiOAuth(payload)
      } catch (error) {
        if (
          !getServerErrorSources(error).some(
            (source) => source.code === 'FREEPI_REAUTH_REQUIRED'
          )
        ) {
          throw error
        }
        const proof = await verification.requestVerification({
          scope: 'upstream.account.bind',
          context: { provider: 'freepi' },
          title: t('Confirm your identity to link FreePi'),
        })
        if (!proof) return null
        return startFreePiOAuth(payload, proof.proof_token)
      }
    },
    retry: false,
    onSuccess: (data) => {
      if (!data) return
      let validVerification = false
      try {
        const uri = new URL(data.verificationUri)
        validVerification =
          data.method === 'connect'
            ? uri.protocol === 'https:' &&
              uri.hostname === 'freepi.ai' &&
              !uri.username &&
              !uri.password &&
              uri.pathname === '/connect' &&
              !uri.search &&
              /^#t=[^&#/?]{1,1024}$/.test(uri.hash)
            : data.method === 'device' &&
              data.verificationUri === 'https://github.com/login/device'
      } catch {
        validVerification = false
      }
      if (
        !validVerification ||
        !data.state ||
        !data.userCode ||
        data.expiresIn <= 0
      ) {
        setProblem(
          t('FreePi device authorization response could not be verified')
        )
        return
      }
      setProblem('')
      setAccepted(false)
      setResult({ status: 'pending' })
      setSession({ ...data, expiresAt: Date.now() + data.expiresIn * 1000 })
    },
    onError: (error) => {
      setProblem(
        getServerErrorMessage(
          error,
          t('FreePi authorization could not be started. Try again later')
        )
      )
      handleServerError(error)
    },
  })

  const consent = useMutation({
    mutationFn: () => {
      if (!session || !accepted || !result?.consentVersion) {
        throw new Error(t('FreePi consent requires your confirmation'))
      }
      return consentFreePiOAuth(session.state, result.consentVersion)
    },
    retry: false,
    onSuccess: (data) => {
      setResult(data)
      setAccepted(false)
    },
    onError: (error) => {
      setProblem(
        getServerErrorMessage(error, t('FreePi consent request failed'))
      )
      handleServerError(error)
    },
  })

  const cancel = useMutation({
    mutationFn: () => {
      if (!session) throw new Error(t('Invalid FreePi authorization state'))
      return cancelFreePiOAuth(session.state)
    },
    retry: false,
    onSuccess: (data) => {
      setResult(data)
      setSession(null)
      setProblem('')
    },
    onError: (error) => {
      setProblem(
        getServerErrorMessage(
          error,
          t('FreePi cancellation failed. Authorization expires automatically')
        )
      )
      handleServerError(error)
    },
  })

  useEffect(() => {
    if (!session || result?.status !== 'pending' || cancel.isPending) return
    let active = true
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      if (Date.now() >= session.expiresAt) {
        setProblem(t('FreePi authorization expired. Start again'))
        setSession(null)
        return
      }
      try {
        const data = await pollFreePiOAuth(session.state)
        if (!active) return
        if (data.status === 'pending') {
          timer = setTimeout(
            () => {
              void poll()
            },
            Math.max(5, data.interval ?? session.interval) * 1000
          )
        } else {
          setResult(data)
          if (data.status === 'restart_required') {
            setProblem(
              t(
                'FreePi authorization could not be completed. Start a new authorization'
              )
            )
            setSession(null)
          }
        }
      } catch (error) {
        if (!active) return
        setProblem(
          getServerErrorMessage(
            error,
            t(
              'FreePi authorization stopped. Start again after checking your login session'
            )
          )
        )
        setSession(null)
        handleServerError(error)
      }
    }
    timer = setTimeout(
      () => {
        void poll()
      },
      Math.max(5, session.interval) * 1000
    )
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [session, result?.status, cancel.isPending, t])

  return (
    <section
      className='bg-card mb-4 space-y-3 rounded-xl border p-5'
      aria-label={t('FreePi account authorization')}
    >
      <h2 className='font-semibold'>{t('FreePi account authorization')}</h2>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Connect your FreePi account in the official browser page using GitHub, Google or email, or use a GitHub device code. If your dashboard login is older than ten minutes, confirm your identity here before linking FreePi.'
        )}
      </p>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Your FreePi token stays encrypted on the server. The channel is private unless you explicitly allow public use. FreePi credits, consent and advertisement requirements still apply; this gateway does not fabricate ad views or bypass limits.'
        )}
      </p>
      <p className='text-destructive text-sm'>
        {t(
          'FreePi consent v4 permits retaining prompts, code, files and responses for training, sharing or selling traces, and sending conversation excerpts to its advertising provider. Do not send confidential data. Review the provider terms before continuing.'
        )}
      </p>
      <div className='flex flex-wrap gap-3 text-sm'>
        <a
          href='https://freepi.ai/terms'
          target='_blank'
          rel='noopener noreferrer'
          className='text-primary underline'
        >
          {t('FreePi terms of service')}
        </a>
        <a
          href='https://freepi.ai/privacy'
          target='_blank'
          rel='noopener noreferrer'
          className='text-primary underline'
        >
          {t('FreePi privacy policy')}
        </a>
        <a
          href='https://freepi.ai/dashboard'
          target='_blank'
          rel='noopener noreferrer'
          className='text-primary underline'
        >
          {t('FreePi account dashboard')}
        </a>
        <a
          href='https://freepi.ai/docs/getting-started'
          target='_blank'
          rel='noopener noreferrer'
          className='text-primary underline'
        >
          {t('FreePi sign-in guide')}
        </a>
        <a
          href='https://freepi.ai/docs/updating-and-troubleshooting#sign-in-does-not-open-a-browser'
          target='_blank'
          rel='noopener noreferrer'
          className='text-primary underline'
        >
          {t('Browser or device sign-in help')}
        </a>
      </div>
      {!session && !completedID && (
        <form
          className='space-y-3'
          onSubmit={(event) => {
            event.preventDefault()
            start.mutate()
          }}
        >
          <div
            className='space-y-2'
            role='group'
            aria-label={t('FreePi authorization method')}
          >
            <Label>{t('Authorization method')}</Label>
            <div className='flex flex-wrap gap-2'>
              <Button
                type='button'
                variant={method === 'connect' ? 'default' : 'outline'}
                aria-pressed={method === 'connect'}
                disabled={start.isPending}
                onClick={() => setMethod('connect')}
              >
                {t('Browser sign-in and Connect (recommended)')}
              </Button>
              <Button
                type='button'
                variant={method === 'device' ? 'default' : 'outline'}
                aria-pressed={method === 'device'}
                disabled={start.isPending}
                onClick={() => setMethod('device')}
              >
                {t('GitHub device code')}
              </Button>
            </div>
            <p className='text-muted-foreground text-sm'>
              {method === 'connect'
                ? t(
                    'Sign in on FreePi with GitHub, Google or email, check the confirmation code, then press Connect. This page finishes automatically; no callback URL is needed.'
                  )
                : t(
                    'Open GitHub on any device, enter the one-time code and approve it. This page finishes automatically.'
                  )}
            </p>
          </div>
          <Label htmlFor='freepi-name'>{t('Display name')}</Label>
          <Input
            id='freepi-name'
            value={displayName}
            maxLength={64}
            onChange={(event) => setDisplayName(event.target.value)}
            disabled={start.isPending}
          />
          <div className='flex items-start gap-2'>
            <Checkbox
              id='freepi-public'
              checked={isPublic}
              onCheckedChange={(checked) => setIsPublic(checked === true)}
              disabled={start.isPending}
            />
            <Label htmlFor='freepi-public'>
              {t(
                'I explicitly allow other users to call my FreePi channel and consume my account credits'
              )}
            </Label>
          </div>
          <Button type='submit' disabled={start.isPending}>
            {method === 'connect'
              ? t('Start browser authorization')
              : t('Start GitHub device authorization')}
          </Button>
        </form>
      )}
      {session && !completedID && (
        <div className='space-y-3'>
          <div className='flex items-center gap-2'>
            <span>
              {browserConnect
                ? t('FreePi confirmation code')
                : t('GitHub device code')}
            </span>
            <code>{session.userCode}</code>
            <CopyButton
              value={session.userCode}
              aria-label={
                browserConnect
                  ? t('Copy FreePi confirmation code')
                  : t('Copy GitHub device code')
              }
            />
          </div>
          <a
            href={
              browserConnect
                ? session.verificationUri
                : (completeUri ?? session.verificationUri)
            }
            target='_blank'
            rel='noopener noreferrer'
            className='text-primary text-sm underline'
          >
            {verificationLinkText}
          </a>
          {browserConnect ? (
            <p className='text-muted-foreground text-sm'>
              {t(
                'Use GitHub, Google or email on the official FreePi page. Check that its code matches this page, then press Connect. No callback URL is required.'
              )}
            </p>
          ) : (
            !completeUri && (
              <p className='text-muted-foreground text-sm'>
                {t(
                  'GitHub requires you to enter this code once. After confirmation, this page finishes binding automatically.'
                )}
              </p>
            )
          )}
          <p role='status' className='text-muted-foreground text-sm'>
            {browserConnect
              ? t(
                  'Waiting for your FreePi browser confirmation. No channel is created until authorization finishes.'
                )
              : t(
                  'Waiting for your GitHub confirmation. No channel is created until authorization finishes.'
                )}
          </p>
          {result?.status === 'consent_required' && (
            <div className='space-y-2 rounded-lg border p-3'>
              <a
                href='https://freepi.ai'
                target='_blank'
                rel='noopener noreferrer'
                className='text-primary text-sm underline'
              >
                {t('Review the current FreePi terms and consent requirements')}
              </a>
              <div className='flex items-start gap-2'>
                <Checkbox
                  id='freepi-consent'
                  checked={accepted}
                  onCheckedChange={(checked) => setAccepted(checked === true)}
                  disabled={consent.isPending}
                />
                <Label htmlFor='freepi-consent'>
                  {t(
                    'I have reviewed and accept FreePi consent version {{version}}',
                    { version: result.consentVersion }
                  )}
                </Label>
              </div>
              <Button
                disabled={!accepted || consent.isPending || cancel.isPending}
                onClick={() => {
                  setProblem('')
                  consent.mutate()
                }}
              >
                {t('Accept FreePi consent and continue')}
              </Button>
            </div>
          )}
          <Button
            variant='outline'
            disabled={cancel.isPending || consent.isPending}
            onClick={() => cancel.mutate()}
          >
            {t('Cancel authorization')}
          </Button>
        </div>
      )}
      {completedID && (
        <p role='status'>
          {t(
            'FreePi authorization completed. Channel #{{id}} uses your account',
            { id: completedID }
          )}
        </p>
      )}
      {problem && (
        <div className='space-y-2'>
          <p role='alert' className='text-destructive text-sm'>
            {problem}
          </p>
          {method === 'connect' && !session && !completedID && (
            <Button
              variant='outline'
              onClick={() => {
                setMethod('device')
                setProblem('')
              }}
            >
              {t('Use GitHub device code instead')}
            </Button>
          )}
        </div>
      )}
      <SecureVerificationDialog {...verification.dialogProps} />
    </section>
  )
}

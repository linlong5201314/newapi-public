import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { handleServerError } from '@/lib/handle-server-error'

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
  const [displayName, setDisplayName] = useState('')
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
    mutationFn: () =>
      startFreePiOAuth({
        displayName,
        public: isPublic,
        confirmPublic: isPublic,
        ...(props.proxy !== undefined ? { proxy: props.proxy } : {}),
        ...(props.proxyPolicy ? { proxy_policy: props.proxyPolicy } : {}),
      }),
    retry: false,
    onSuccess: (data) => {
      if (
        data.verificationUri !== 'https://github.com/login/device' ||
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
        t('FreePi authorization could not be started. Try again later')
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
      setProblem(t('FreePi consent request failed'))
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
        t('FreePi cancellation failed. Authorization expires automatically')
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
          t(
            'FreePi authorization stopped. Start again after checking your login session'
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
      aria-label={t('FreePi GitHub authorization')}
    >
      <h2 className='font-semibold'>{t('FreePi GitHub authorization')}</h2>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Use your own GitHub account. GitHub confirmation is required in your browser; this page cannot complete it for you. Sign in again here if your dashboard login is older than ten minutes.'
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
      </div>
      {!session && !completedID && (
        <form
          className='space-y-3'
          onSubmit={(event) => {
            event.preventDefault()
            start.mutate()
          }}
        >
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
            {t('Start GitHub device authorization')}
          </Button>
        </form>
      )}
      {session && !completedID && (
        <div className='space-y-3'>
          <div className='flex items-center gap-2'>
            <span>{t('GitHub device code')}</span>
            <code>{session.userCode}</code>
            <CopyButton
              value={session.userCode}
              aria-label={t('Copy GitHub device code')}
            />
          </div>
          <a
            href={completeUri ?? session.verificationUri}
            target='_blank'
            rel='noopener noreferrer'
            className='text-primary text-sm underline'
          >
            {completeUri
              ? t('Open GitHub to confirm authorization')
              : t('Open GitHub and enter this device code')}
          </a>
          {!completeUri && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'GitHub requires you to enter this code once. After confirmation, this page finishes binding automatically.'
              )}
            </p>
          )}
          <p role='status' className='text-muted-foreground text-sm'>
            {t(
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
        <p role='alert' className='text-destructive text-sm'>
          {problem}
        </p>
      )}
    </section>
  )
}

import { useQueryClient } from '@tanstack/react-query'
import { ExternalLink, Loader2, LogIn, RefreshCw } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { PUBLIC_CATALOG_QUERY_KEY } from '@/features/public-pool/constants'
import { api, type ApiRequestConfig } from '@/lib/api'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

import { channelsQueryKeys } from '../../lib'
import { getChannelDetail } from '../../api'
import { ChannelModelSelection, UnavailablePreviewModels } from '../channel-model-selection'

const channelActionConfig = (config: ApiRequestConfig = {}): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
  singleUseAuthorization: true,
  headers: { ...config.headers, 'X-Account-Action': 'authorize' },
})

type AuthorizationStart = {
  sessionId: string
  authUrl: string
  callbackPort: number
  callbackPath: string
}

const draftKey = 'new-api:xiaoyiwork:authorization-draft'

type AuthorizationDraft = {
  authorization: AuthorizationStart | null
  callbackUrl: string
  code: string
  name: string
  channelId: number | null
}

function readDraft(): AuthorizationDraft | null {
  try {
    const value = sessionStorage.getItem(draftKey)
    if (!value) return null
    const draft = JSON.parse(value) as AuthorizationDraft
    return draft.authorization?.sessionId && draft.authorization.authUrl ? draft : null
  } catch {
    return null
  }
}

export function XiaoyiWorkConnection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const pending = useRef(false)
  const [authorization, setAuthorization] = useState<AuthorizationStart | null>(() => readDraft()?.authorization ?? null)
  const [callbackUrl, setCallbackUrl] = useState(() => readDraft()?.callbackUrl ?? '')
  const [code, setCode] = useState(() => readDraft()?.code ?? '')
  const [name, setName] = useState(() => readDraft()?.name ?? '')
  const [catalog, setCatalog] = useState<string[]>([])
  const [selectedModels, setSelectedModels] = useState<string[]>([])
  const [channelId, setChannelId] = useState<number | null>(() => readDraft()?.channelId ?? null)
  const [discovered, setDiscovered] = useState(false)
  const [unavailable, setUnavailable] = useState<string[]>([])
  const [publicModels, setPublicModels] = useState<string[]>([])
  const [shareConfirmed, setShareConfirmed] = useState(false)
  const [busy, setBusy] = useState<'start' | 'connect' | null>(null)
  const [error, setError] = useState<unknown>(null)

  useEffect(() => {
    if (!channelId) return
    let active = true
    void getChannelDetail(channelId)
      .then((response) => {
        if (!active || response.success) return
        setChannelId(null)
        setAuthorization(null)
        setCallbackUrl('')
        setCode('')
        setDiscovered(false)
        setCatalog([])
        setSelectedModels([])
        setError(new Error('The saved Xiaoyi Work channel no longer exists. Start authorization again'))
      })
      .catch(() => undefined)
    return () => { active = false }
  }, [channelId])

  useEffect(() => {
    if (!authorization) {
      sessionStorage.removeItem(draftKey)
      return
    }
    sessionStorage.setItem(draftKey, JSON.stringify({ authorization, callbackUrl, code, name, channelId } satisfies AuthorizationDraft))
  }, [authorization, callbackUrl, code, name, channelId])

  async function startAuthorization() {
    if (pending.current) return
    pending.current = true
    setBusy('start')
    setError(null)
    setChannelId(null)
    setAuthorization(null)
    setCallbackUrl('')
    setCode('')
    setDiscovered(false)
    setCatalog([])
    setSelectedModels([])
    setPublicModels([])
    setShareConfirmed(false)
    sessionStorage.removeItem(draftKey)
    try {
      const response = requireServerSuccess(
        await api.post('/api/channel/self/oauth/xiaoyiwork/start', {}, channelActionConfig({ singleUseAuthorization: true }))
      )
      const next = response.data?.data as AuthorizationStart | undefined
      if (!next?.sessionId || !next.authUrl) throw new Error('Authorization could not be started')
      setAuthorization(next)
      const popup = window.open(next.authUrl, '_blank', 'noopener,noreferrer')
      if (!popup) toast.info(t('Open the authorization link below in your browser'))
    } catch (cause) {
      setError(cause)
    } finally {
      pending.current = false
      setBusy(null)
    }
  }

  async function connect() {
    if (!authorization || (!channelId && !callbackUrl.trim() && !code.trim()) || pending.current) return
    if (publicModels.length > 0 && !shareConfirmed) return
    pending.current = true
    setBusy('connect')
    setError(null)
    try {
      const response = requireServerSuccess(
        channelId
          ? await api.post('/api/channel/self/visibility', {
              channelId,
              models: selectedModels,
              visibility: publicModels.length > 0 ? 1 : 0,
              publicModels,
              share_confirmed: shareConfirmed,
            }, channelActionConfig({ singleUseAuthorization: true }))
          : await api.post('/api/channel/self/oauth/xiaoyiwork/complete', {
              sessionId: authorization.sessionId,
              callbackUrl: callbackUrl.trim(),
              code: code.trim(),
              displayName: name.trim(),
              ...(discovered ? { models: selectedModels } : {}),
              public_models: publicModels,
              share_confirmed: shareConfirmed,
            }, channelActionConfig({ singleUseAuthorization: true }))
      )
      setCatalog(response.data?.data?.models ?? [])
      setUnavailable(response.data?.data?.unavailable_models ?? [])
      setSelectedModels(response.data?.data?.models ?? [])
      setDiscovered(true)
      setChannelId(response.data?.data?.channelId ?? channelId)
      setCallbackUrl('')
      setCode('')
      toast.success(t(channelId ? 'Xiaoyi Work selection saved' : 'Xiaoyi Work channel connected'))
      void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
      void queryClient.invalidateQueries({ queryKey: PUBLIC_CATALOG_QUERY_KEY })
    } catch (cause) {
      setError(cause)
    } finally {
      pending.current = false
      setBusy(null)
    }
  }

  async function cancel() {
    if (authorization) {
      await api.post('/api/channel/self/oauth/xiaoyiwork/cancel', { sessionId: authorization.sessionId }, channelActionConfig({ singleUseAuthorization: true })).catch(() => undefined)
    }
    setAuthorization(null)
    setCallbackUrl('')
    setCode('')
    setError(null)
  }

  return (
    <div className='min-h-0 space-y-4 overflow-y-auto overscroll-contain px-1'>
      <section className='bg-muted/35 space-y-3 rounded-xl border p-3.5'>
        <h3 className='font-semibold'>{t('Connect your Huawei account to Xiaoyi Work')}</h3>
        <p className='text-muted-foreground text-sm'>
          {t('Sign in with Huawei in your browser. New API exchanges the authorization directly, discovers your models and calls Xiaoyi Work for you. No Magpie installation, gateway address or API key is needed.')}
        </p>
        {!authorization ? (
          <Button type='button' className='min-h-11 px-4' disabled={busy !== null} onClick={() => void startAuthorization()}>
            {busy === 'start' ? <Loader2 className='animate-spin' aria-hidden='true' /> : <LogIn aria-hidden='true' />}
            {t('Authorize Huawei account')}
          </Button>
        ) : (
          <div className='space-y-3'>
            <Button role='link' className='min-h-11 px-4' render={<a href={authorization.authUrl} target='_blank' rel='noreferrer' />}>
              <ExternalLink aria-hidden='true' />
              {t('Open Huawei authorization')}
            </Button>
            <p className='text-muted-foreground text-xs'>
              {t('After authorizing, return to this page. If the browser shows a local callback page, paste its full address below.')}
            </p>
          </div>
        )}
      </section>

      {authorization && (
        <div className='space-y-3'>
          <div className='space-y-1.5'>
            <Label htmlFor='xiaoyi-callback-url'>{t('Local callback URL (optional if you have the code)')}</Label>
            <Input id='xiaoyi-callback-url' type='url' placeholder='http://127.0.0.1:17355/auth/callback?code=...&state=...' value={callbackUrl} disabled={busy !== null} onChange={(event) => setCallbackUrl(event.target.value)} />
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='xiaoyi-code'>{t('Authorization code')}</Label>
            <Input id='xiaoyi-code' placeholder={t('Paste code if the callback cannot open')} value={code} disabled={busy !== null} onChange={(event) => setCode(event.target.value)} />
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='xiaoyi-name'>{t('Channel name')}</Label>
            <Input id='xiaoyi-name' placeholder='华为小艺 Work' value={name} disabled={busy !== null} onChange={(event) => setName(event.target.value)} />
          </div>
          <Button type='button' className='min-h-11 w-full px-4 sm:w-auto' disabled={busy !== null || (!channelId && !callbackUrl.trim() && !code.trim()) || (discovered && selectedModels.length === 0) || (publicModels.length > 0 && !shareConfirmed)} onClick={() => void connect()}>
            {busy === 'connect' ? <Loader2 className='animate-spin' aria-hidden='true' /> : <RefreshCw aria-hidden='true' />}
            {t(discovered ? 'Save selection and connect' : 'Connect and sync models')}
          </Button>
          <Button type='button' variant='outline' disabled={busy !== null} onClick={() => void cancel()}>{t('Cancel authorization')}</Button>
        </div>
      )}

      {error != null && <p role='alert' className='text-destructive text-sm'>{t(getServerErrorMessage(error, t('Failed to authorize Xiaoyi Work')))}</p>}
      {catalog.length > 0 && <p role='status' className='text-muted-foreground text-sm'>{t('{{count}} models are available to this account.', { count: catalog.length })}</p>}
      {channelId && <p role='status' className='text-success text-sm font-medium'>{t('Xiaoyi Work channel created in My channels. You can close this window or adjust models below.')}</p>}
      {discovered && catalog.length > 0 && (
        <ChannelModelSelection models={catalog} enabledModels={selectedModels} publicModels={publicModels} disabled={busy !== null} onChange={(enabled, published) => { setSelectedModels(enabled); setPublicModels(published); if (published.join(',') !== publicModels.join(',')) setShareConfirmed(false) }} />
      )}
      {publicModels.length > 0 && <Label className='flex items-start gap-2'><Checkbox checked={shareConfirmed} disabled={busy !== null} onCheckedChange={(value) => setShareConfirmed(value === true)} />{t('I explicitly allow other users to use this channel and spend my account credits')}</Label>}
      <UnavailablePreviewModels models={unavailable} />
    </div>
  )
}

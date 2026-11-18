import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import {
  getFreeTemplates,
  type FreeSourceTemplate,
} from '@/features/my-channels/free-sources'
import { handleServerError } from '@/lib/handle-server-error'

import {
  cancelWorkbuddyOAuth,
  createBinding,
  deleteBinding,
  importTrae,
  listBindings,
  pollWorkbuddyOAuth,
  setBindingPublic,
  startWorkbuddyOAuth,
  type AccountBinding,
  type OAuthSession,
} from './api'
import { FreePiAuthorizationCard } from './freepi-authorization'

const STATUS_TEXT: Record<string, string> = {
  pending: '排队开通',
  provisioning: '开通中',
  ready: '已开通',
  failed: '开通失败',
}

export function AccountBindingsCard() {
  const { t } = useTranslation()
  const [templates, setTemplates] = useState<FreeSourceTemplate[]>([])
  const [provider, setProvider] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [accounts, setAccounts] = useState('')
  const [isPublic, setIsPublic] = useState(false)
  const [proxy, setProxy] = useState('')
  const [rows, setRows] = useState<AccountBinding[]>([])
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [oauthRealm, setOauthRealm] = useState<'cn' | 'global'>('cn')
  const [oauthSession, setOauthSession] = useState<OAuthSession | null>(null)
  const [oauthMsg, setOauthMsg] = useState('')
  const [traeInput, setTraeInput] = useState('')
  const [traeUid, setTraeUid] = useState('')
  const [traeMsg, setTraeMsg] = useState('')
  const [traeConfirmed, setTraeConfirmed] = useState(false)
  const [traeSubmitting, setTraeSubmitting] = useState(false)
  const [oauthStarting, setOauthStarting] = useState(false)
  const oauthRequest = useRef(0)
  const pendingOAuth = useRef<string | null>(null)
  const oauthPopup = useRef<Window | null>(null)
  useEffect(
    () => () => {
      oauthRequest.current++
      oauthPopup.current?.close()
      const sessionId = pendingOAuth.current
      pendingOAuth.current = null
      if (sessionId) void cancelWorkbuddyOAuth(sessionId).catch(() => undefined)
    },
    []
  )

  const accountTemplates = useMemo(
    () =>
      templates.filter(
        (item) =>
          item.id !== 'freepi' &&
          (item.authMode === 'account' ||
            item.authMode === 'oauth' ||
            item.authMode === 'gateway' ||
            item.authMode === 'bridge')
      ),
    [templates]
  )

  const selectedTemplate = useMemo(
    () => templates.find((item) => item.id === provider),
    [templates, provider]
  )
  const mode = selectedTemplate?.authMode || 'account'
  const isGateway = mode === 'gateway'
  const isOAuth = mode === 'oauth'
  const isBridge = mode === 'bridge'

  const reload = useCallback(async () => {
    setRows(await listBindings())
  }, [])

  useEffect(() => {
    let alive = true
    ;(async () => {
      const list = await getFreeTemplates()
      if (!alive) return
      setTemplates(list)
      const first = list.find(
        (item) =>
          item.id !== 'freepi' &&
          (item.authMode === 'account' ||
            item.authMode === 'oauth' ||
            item.authMode === 'gateway' ||
            item.authMode === 'bridge')
      )
      if (first) setProvider(first.id)
    })()
    void reload()
    return () => {
      alive = false
    }
  }, [reload])

  let accountPlaceholder =
    '账号----密码\n账号----密码（每行一个，可一次粘贴多个）'
  if (isGateway) {
    accountPlaceholder =
      '供应商----密钥\n如 deepseek----sk-xxx（每行一个，可留空先开网关）'
  } else if (isBridge) {
    accountPlaceholder =
      'access_token----refresh_token\n（每行一个账号，来源：小浣熊 auth.json）'
  } else if (isOAuth && provider === 'internlm-inkstone') {
    accountPlaceholder =
      'sk-…（书生 Token Plan 里的 API Key，每行一个）\n也可粘贴控制台 Cookie 里的 uaa-token，由平台代建 Key'
  } else if (isOAuth) {
    accountPlaceholder = '浏览器授权后的回调链接或 Token（每行一个）'
  } else if (provider === 'ds2api') {
    accountPlaceholder =
      '账号----密码\n或 账号----会话Token（推荐）\n或 直接粘贴 Token（每行一个）'
  }

  const submit = async () => {
    if (!provider) {
      setMsg('请选择要授权的来源')
      return
    }
    if (!accounts.trim() && !isGateway) {
      setMsg(
        isOAuth
          ? '请粘贴浏览器授权后的回调链接或 Token（每行一个）'
          : '请按「账号----密码」每行一个填写'
      )
      return
    }
    if (provider === 'trae-cn' || provider === 'trae-intl') {
      await submitTrae(accounts)
      return
    }
    setBusy(true)
    setMsg('')
    const res = await createBinding({
      provider,
      accounts,
      displayName,
      public: isPublic,
      proxy,
    })
    setBusy(false)
    if (res.ok) {
      setAccounts('')
      setProxy('')
      setMsg('已提交，平台正在为你单独开通（进度见下方列表）')
      await reload()
    } else {
      setMsg(res.message || '提交失败')
    }
  }

  // Keep a single owner-scoped ceremony and stop on expiry or terminal failure.
  useEffect(() => {
    if (!oauthSession) return
    let alive = true
    let timer: ReturnType<typeof setTimeout>
    const expiresAt = Date.now() + (oauthSession.expiresIn ?? 600) * 1000
    const poll = async () => {
      try {
        if (Date.now() >= expiresAt) {
          throw new Error(t('Authorization session expired'))
        }
        const res = await pollWorkbuddyOAuth(oauthSession.sessionId)
        if (!alive) return
        if (!res.done) {
          timer = setTimeout(() => {
            void poll()
          }, 2000)
          return
        }
        pendingOAuth.current = null
        setOauthSession(null)
        oauthPopup.current?.close()
        setOauthMsg(t('Account login completed; channel created'))
        await reload()
      } catch (error) {
        if (!alive) return
        const sessionId = pendingOAuth.current
        pendingOAuth.current = null
        setOauthSession(null)
        oauthPopup.current?.close()
        setOauthMsg(
          t(
            'Authorization stopped. Start again after checking your login session'
          )
        )
        if (sessionId) {
          void cancelWorkbuddyOAuth(sessionId).catch(() => undefined)
        }
        handleServerError(error)
      }
    }
    timer = setTimeout(() => {
      void poll()
    }, 2000)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [oauthSession, reload, t])

  const startOAuth = async () => {
    if (oauthStarting) return
    const request = ++oauthRequest.current
    oauthPopup.current?.close()
    const popup = window.open('about:blank', '_blank')
    if (popup) popup.opener = null
    oauthPopup.current = popup
    setOauthStarting(true)
    setOauthMsg(t('Creating authorization session...'))
    try {
      const previous = pendingOAuth.current
      pendingOAuth.current = null
      setOauthSession(null)
      if (previous) await cancelWorkbuddyOAuth(previous)
      if (request !== oauthRequest.current) return
      if (!popup || popup.closed) {
        throw new Error(
          t(
            'Your browser blocked the login window. Allow popups and try again.'
          )
        )
      }
      const sess = await startWorkbuddyOAuth(oauthRealm, {
        proxy: proxy.trim(),
        proxy_policy: { mode: proxy.trim() ? 'fixed' : 'user_pool' },
        displayName: displayName.trim() || undefined,
        public: isPublic,
      })
      if (!sess) throw new Error(t('Failed to start authorization'))
      if (request !== oauthRequest.current || popup.closed) {
        popup.close()
        await cancelWorkbuddyOAuth(sess.sessionId)
        return
      }
      pendingOAuth.current = sess.sessionId
      setOauthSession(sess)
      popup.location.href = sess.authUrl
      setOauthMsg(
        t(
          'Finish signing in in the authorization window. This page refreshes automatically.'
        )
      )
    } catch (error) {
      popup?.close()
      if (request === oauthRequest.current) {
        const sessionId = pendingOAuth.current
        pendingOAuth.current = null
        setOauthSession(null)
        if (sessionId) {
          void cancelWorkbuddyOAuth(sessionId).catch(() => undefined)
        }
        setOauthMsg(t('Failed to start authorization'))
        handleServerError(error)
      }
    } finally {
      if (request === oauthRequest.current) setOauthStarting(false)
    }
  }

  const cancelOAuth = async () => {
    oauthRequest.current++
    setOauthStarting(false)
    const sessionId = pendingOAuth.current
    pendingOAuth.current = null
    setOauthSession(null)
    oauthPopup.current?.close()
    setOauthMsg(t('Account login was cancelled. Start again'))
    if (sessionId) {
      try {
        await cancelWorkbuddyOAuth(sessionId)
      } catch (error) {
        handleServerError(error)
      }
    }
  }

  const submitTrae = async (input = traeInput) => {
    if (!traeConfirmed || traeSubmitting) return
    const raw = input.trim()
    if (!raw) {
      setTraeMsg('请粘贴 Trae 的回调链接或 refreshToken')
      return
    }
    setTraeMsg('正在导入…')
    const looksUrl =
      /^https?:/i.test(raw) || raw.includes('://') || raw.includes('code=')
    try {
      if (looksUrl) {
        const url = new URL(raw)
        if (
          url.protocol !== 'http:' ||
          url.hostname !== '127.0.0.1' ||
          !url.port ||
          url.pathname !== '/authorize' ||
          url.username ||
          url.password ||
          url.hash
        ) {
          setTraeMsg(
            t(
              'Use your own refresh token or the official http://127.0.0.1:port/authorize callback.'
            )
          )
          return
        }
      }
      setTraeSubmitting(true)
      const res = await importTrae({
        ...(looksUrl ? { callbackUrl: raw } : { refreshToken: raw }),
        uid: traeUid.trim() || undefined,
        confirmRotation: traeConfirmed,
        proxy: proxy.trim(),
        proxy_policy: { mode: proxy.trim() ? 'fixed' : 'user_pool' },
        displayName: displayName.trim() || undefined,
        public: isPublic,
      })
      setTraeMsg(
        res.ok
          ? res.notice || '导入成功，渠道已开通'
          : res.message || '导入失败'
      )
      if (res.ok) {
        setTraeInput('')
        setTraeConfirmed(false)
        await reload()
      }
    } catch (error) {
      setTraeMsg(t('Failed to import account credentials'))
      handleServerError(error)
    } finally {
      setTraeSubmitting(false)
    }
  }

  return (
    <div className='mb-3 rounded-lg border p-3'>
      <div className='mb-1 text-sm font-medium'>
        上游账号授权（账号密码 / 浏览器授权 / 配套网关）
      </div>
      <div className='text-muted-foreground mb-2 text-xs'>
        {t(
          'Use only accounts you own. FreePi uses the dedicated GitHub device authorization below. Magpie requires your own running gateway endpoint and gateway key; copying a plugin command does not deploy a gateway.'
        )}
      </div>
      <FreePiAuthorizationCard onComplete={reload} />
      <div className='flex flex-wrap gap-2'>
        <select
          className='h-8 rounded-md border bg-transparent px-2 text-sm'
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
        >
          {accountTemplates.map((item) => (
            <option key={item.id} value={item.id}>
              {item.name}
            </option>
          ))}
        </select>
        <input
          className='h-8 rounded-md border bg-transparent px-2 text-sm'
          placeholder='备注名（可选）'
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
        />
      </div>
      <textarea
        className='mt-2 h-24 w-full rounded-md border bg-transparent p-2 font-mono text-xs'
        placeholder={accountPlaceholder}
        value={accounts}
        onChange={(e) => setAccounts(e.target.value)}
      />
      {provider === 'ds2api' && (
        <div className='text-muted-foreground mt-1 text-xs leading-relaxed'>
          {t(
            'DS2API uses a platform account login link, not upstream OAuth. Use the account login action to submit your own credentials over HTTPS; do not put tokens in callback URLs or run browser-console scripts.'
          )}
        </div>
      )}
      {!isGateway && (
        <div className='mt-2'>
          <input
            className='h-8 w-full rounded-md border bg-transparent px-2 text-xs'
            placeholder='出口代理（可选）：http://127.0.0.1:7890 或 socks5://127.0.0.1:1080；填 direct 表示强制直连'
            value={proxy}
            onChange={(e) => setProxy(e.target.value)}
          />
          <div className='text-muted-foreground mt-1 text-xs'>
            不填=跟随平台出口池；填 direct=强制直连；填地址=该账号专用固定代理。
          </div>
        </div>
      )}
      <div className='mt-2 flex flex-wrap items-center gap-3'>
        <label className='flex items-center gap-1 text-xs'>
          <input
            type='checkbox'
            checked={isPublic}
            onChange={(e) => setIsPublic(e.target.checked)}
          />
          公开到公共池（模型对其他人可见可调用，展示你的昵称头像）
        </label>
        <button
          type='button'
          disabled={
            busy ||
            ((provider === 'trae-cn' || provider === 'trae-intl') &&
              !traeConfirmed)
          }
          className='h-8 rounded-md border px-3 text-sm disabled:opacity-50'
          onClick={submit}
        >
          {busy ? '提交中…' : '提交授权'}
        </button>
        {msg ? <span className='text-xs'>{msg}</span> : null}
      </div>

      <div className='mt-3 grid gap-3 md:grid-cols-2'>
        <div className='rounded border p-2'>
          <div className='mb-1 text-xs font-medium'>
            WorkBuddy 设备授权（Google / 邮箱登录，仅国内版有签到）
          </div>
          <div className='text-muted-foreground mb-2 text-xs'>
            点「开始授权」后在新窗口完成登录，回来这里会自动显示结果；不需要填任何
            Key。国际版没有签到功能，只用于授权调用。
          </div>
          <div className='flex flex-wrap items-center gap-2'>
            <select
              className='h-8 rounded-md border bg-transparent px-2 text-sm'
              value={oauthRealm}
              onChange={(e) => setOauthRealm(e.target.value as 'cn' | 'global')}
            >
              <option value='cn'>国内版</option>
              <option value='global'>国际版</option>
            </select>
            <button
              type='button'
              className='h-8 rounded-md border px-3 text-sm'
              disabled={oauthStarting}
              onClick={startOAuth}
            >
              {t(
                oauthStarting
                  ? 'Creating authorization session...'
                  : 'Start WorkBuddy authorization'
              )}
            </button>
            {(oauthSession || oauthStarting) && (
              <Button
                type='button'
                size='sm'
                variant='outline'
                onClick={() => void cancelOAuth()}
              >
                {t('Cancel authorization')}
              </Button>
            )}
            {oauthMsg ? (
              <span role='status' className='text-xs'>
                {oauthMsg}
              </span>
            ) : null}
          </div>
        </div>

        <div className='rounded border p-2'>
          <div className='mb-1 text-xs font-medium'>
            {t('Raccoon client credentials')}
          </div>
          <p role='status' className='text-muted-foreground text-xs'>
            {t(
              'Raccoon browser authorization only supports an official local client. Gateway callbacks are not supported'
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Use credentials exported by your own official Raccoon client below. A website login URL or authorization code is not a usable API credential.'
            )}
          </p>
          <a
            className='text-primary text-xs underline'
            href='https://xiaohuanxiong.com'
            target='_blank'
            rel='noopener noreferrer'
          >
            {t('Open official Raccoon client')}
          </a>
        </div>

        <div className='rounded border p-2'>
          <div className='mb-1 text-xs font-medium'>
            Trae 授权导入（回调链接或 refreshToken）
          </div>
          <div className='text-muted-foreground mb-2 text-xs'>
            {t(
              'Trae import uses your own official client credentials, not one-click browser OAuth.'
            )}
          </div>
          <textarea
            className='h-16 w-full rounded-md border bg-transparent p-2 font-mono text-xs'
            aria-label={t('Trae callback URL or refresh token')}
            placeholder={t(
              'Use your own refresh token or the official http://127.0.0.1:port/authorize callback.'
            )}
            value={traeInput}
            onChange={(e) => setTraeInput(e.target.value)}
          />
          <Label className='my-2 flex items-start gap-2 text-xs'>
            <Checkbox
              disabled={traeSubmitting}
              checked={traeConfirmed}
              onCheckedChange={(checked) => setTraeConfirmed(checked === true)}
            />
            {t(
              'I understand that importing Trae credentials rotates tokens and may sign out other clients.'
            )}
          </Label>
          <div className='mt-1 flex flex-wrap items-center gap-2'>
            <input
              className='h-8 w-32 rounded-md border bg-transparent px-2 text-sm'
              placeholder='uid（可选）'
              value={traeUid}
              onChange={(e) => setTraeUid(e.target.value)}
            />
            <button
              type='button'
              className='h-8 rounded-md border px-3 text-sm'
              disabled={traeSubmitting || !traeConfirmed || !traeInput.trim()}
              onClick={() => void submitTrae()}
            >
              导入并开通
            </button>
            {traeMsg ? <span className='text-xs'>{traeMsg}</span> : null}
          </div>
        </div>
      </div>

      {rows.length > 0 && (
        <div className='mt-3 space-y-1'>
          {rows.map((row) => (
            <div
              key={row.id}
              className='flex flex-wrap items-center gap-2 rounded border px-2 py-1 text-xs'
            >
              <span className='font-medium'>{row.provider}</span>
              <span className='text-muted-foreground'>
                {row.account_mask || `${row.account_count || 0} 个账号`}
              </span>
              <span>{STATUS_TEXT[row.status || ''] || row.status}</span>
              {row.public ? <span>已公开</span> : <span>仅自己</span>}
              <button
                type='button'
                className='rounded border px-2'
                onClick={async () => {
                  await setBindingPublic(row.id, !row.public)
                  await reload()
                }}
              >
                {row.public ? '收回私有' : '公开'}
              </button>
              <button
                type='button'
                className='rounded border px-2'
                onClick={async () => {
                  await deleteBinding(row.id)
                  await reload()
                }}
              >
                删除
              </button>
              {row.last_error ? (
                <span className='text-red-500'>{row.last_error}</span>
              ) : null}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

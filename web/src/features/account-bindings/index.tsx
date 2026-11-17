import { useCallback, useEffect, useMemo, useState } from 'react'

import { getFreeTemplates, type FreeSourceTemplate } from '@/features/my-channels/free-sources'

import {
  createBinding,
  deleteBinding,
  importTrae,
  listBindings,
  pollRaccoonOAuth,
  pollWorkbuddyOAuth,
  setBindingPublic,
  startRaccoonOAuth,
  startWorkbuddyOAuth,
  type AccountBinding,
  type OAuthSession,
} from './api'

const STATUS_TEXT: Record<string, string> = {
  pending: '排队开通',
  provisioning: '开通中',
  ready: '已开通',
  failed: '开通失败',
}

export function AccountBindingsCard() {
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
  const [raccoonSession, setRaccoonSession] = useState<OAuthSession | null>(null)
  const [raccoonMsg, setRaccoonMsg] = useState('')
  const [traeInput, setTraeInput] = useState('')
  const [traeUid, setTraeUid] = useState('')
  const [traeMsg, setTraeMsg] = useState('')

  const accountTemplates = useMemo(
    () =>
      templates.filter(
        (item) => item.authMode === 'account' || item.authMode === 'oauth' || item.authMode === 'gateway' || item.authMode === 'bridge'
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
        (item) => item.authMode === 'account' || item.authMode === 'oauth' || item.authMode === 'gateway' || item.authMode === 'bridge'
      )
      if (first) setProvider(first.id)
    })()
    void reload()
    return () => {
      alive = false
    }
  }, [reload])

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

  // WorkBuddy 设备授权：拿到 authUrl 后让用户在浏览器里登录，成功后后端直接建渠道。
  useEffect(() => {
    if (!oauthSession) return
    let alive = true
    const timer = window.setInterval(async () => {
      const res = await pollWorkbuddyOAuth(oauthSession.sessionId)
      if (!alive) return
      if (res.done) {
        window.clearInterval(timer)
        setOauthSession(null)
        setOauthMsg(
          res.channel?.id
            ? `授权成功，已为你开通渠道 #${res.channel.id}`
            : '授权成功，渠道已开通'
        )
        await reload()
      }
    }, 3000)
    return () => {
      alive = false
      window.clearInterval(timer)
    }
  }, [oauthSession, reload])

  const startOAuth = async () => {
    setOauthMsg('正在创建授权会话…')
    const sess = await startWorkbuddyOAuth(oauthRealm)
    if (!sess) {
      setOauthMsg('发起授权失败，请稍后再试')
      return
    }
    setOauthSession(sess)
    setOauthMsg('请在新窗口完成登录，完成后本页会自动刷新')
    window.open(sess.authUrl, '_blank', 'noopener,noreferrer')
  }

  // 小浣熊浏览器授权：授权页登录后浏览器直接回跳到本服务，前端只轮询结果。
  useEffect(() => {
    if (!raccoonSession) return
    let alive = true
    const timer = window.setInterval(async () => {
      const res = await pollRaccoonOAuth(raccoonSession.sessionId)
      if (!alive) return
      if (res.done) {
        window.clearInterval(timer)
        setRaccoonSession(null)
        setRaccoonMsg(
          res.channel?.id
            ? `授权成功，已为你开通渠道 #${res.channel.id}`
            : '授权成功，渠道已开通'
        )
        await reload()
      } else if (res.message) {
        window.clearInterval(timer)
        setRaccoonSession(null)
        setRaccoonMsg(res.message)
      }
    }, 3000)
    return () => {
      alive = false
      window.clearInterval(timer)
    }
  }, [raccoonSession, reload])

  const startRaccoon = async () => {
    setRaccoonMsg('正在创建授权会话…')
    const sess = await startRaccoonOAuth()
    if (!sess) {
      setRaccoonMsg('发起授权失败，请稍后再试')
      return
    }
    setRaccoonSession(sess)
    setRaccoonMsg('请在新窗口登录小浣熊账号，授权后本页会自动开通渠道')
    window.open(sess.authUrl, '_blank', 'noopener,noreferrer')
  }

  const submitTrae = async () => {
    const raw = traeInput.trim()
    if (!raw) {
      setTraeMsg('请粘贴 Trae 的回调链接或 refreshToken')
      return
    }
    setTraeMsg('正在导入…')
    const looksUrl = raw.startsWith('http') || raw.includes('code=')
    const res = await importTrae(
      looksUrl
        ? { callbackUrl: raw, uid: traeUid.trim() || undefined }
        : { refreshToken: raw, uid: traeUid.trim() || undefined }
    )
    setTraeMsg(
      res.ok ? res.notice || '导入成功，渠道已开通' : res.message || '导入失败'
    )
    if (res.ok) {
      setTraeInput('')
      await reload()
    }
  }

  return (
    <div className='mb-3 rounded-lg border p-3'>
      <div className='mb-1 text-sm font-medium'>
        上游账号授权（账号密码 / 浏览器授权 / 配套网关）
      </div>
      <div className='text-muted-foreground mb-2 text-xs'>
        只支持你自己的账号：提交后平台为你单独开通（一用户一实例），凭据与实例都不与其他用户共用，也看不到任何部署地址。选「Magpie 配套网关」填上游「供应商----密钥」，平台为你单独起一台网关；小浣熊点「开始授权」用浏览器登录一次即可；书生·端砚直接粘贴 Token Plan 里的 sk- Key（或在控制台复制 Cookie 里的 uaa-token 让平台代建 Key）。
      </div>
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
        placeholder={
          isGateway
            ? '供应商----密钥\n如 deepseek----sk-xxx（每行一个，可留空先开网关）'
            : isBridge
              ? 'access_token----refresh_token\n（每行一个账号，来源：小浣熊 auth.json）'
              : isOAuth
                ? provider === 'internlm-inkstone'
                  ? 'sk-…（书生 Token Plan 里的 API Key，每行一个）\n也可粘贴控制台 Cookie 里的 uaa-token，由平台代建 Key'
                  : '浏览器授权后的回调链接或 Token（每行一个）'
                : provider === 'ds2api'
                  ? '账号----密码\n或 账号----会话Token（推荐）\n或 直接粘贴 Token（每行一个）'
                  : '账号----密码\n账号----密码（每行一个，可一次粘贴多个）'
        }
        value={accounts}
        onChange={(e) => setAccounts(e.target.value)}
      />
      {provider === 'ds2api' && (
        <div className='text-muted-foreground mt-1 text-xs leading-relaxed'>
          DeepSeek 网页端对服务器 IP 的密码登录会返回 RISK_DEVICE_DETECTED，
          建议改用会话 Token：浏览器登录 chat.deepseek.com → F12 → Application →
          Local Storage → 复制 <code>userToken</code> 的值，按「账号----Token」粘贴。
          Token 有效期数月，过期后重新粘贴即可。
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
          disabled={busy}
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
            点「开始授权」后在新窗口完成登录，回来这里会自动显示结果；不需要填任何 Key。国际版没有签到功能，只用于授权调用。
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
              onClick={startOAuth}
            >
              {oauthSession ? '重新授权' : '开始授权'}
            </button>
            {oauthMsg ? <span className='text-xs'>{oauthMsg}</span> : null}
          </div>
        </div>

        <div className='rounded border p-2'>
          <div className='mb-1 text-xs font-medium'>
            小浣熊浏览器授权（办公小浣熊客户端）
          </div>
          <div className='text-muted-foreground mb-2 text-xs'>
            点「开始授权」会打开小浣熊授权页，用你自己的账号登录后浏览器自动回跳，平台直接用返回的授权码换取凭据并开通渠道；不需要填 Key，也看不到任何部署地址。
          </div>
          <div className='flex flex-wrap items-center gap-2'>
            <button
              type='button'
              className='h-8 rounded-md border px-3 text-sm'
              onClick={startRaccoon}
            >
              {raccoonSession ? '重新授权' : '开始授权'}
            </button>
            {raccoonMsg ? (
              <span className='text-xs'>{raccoonMsg}</span>
            ) : null}
          </div>
        </div>

        <div className='rounded border p-2'>
          <div className='mb-1 text-xs font-medium'>
            Trae 授权导入（回调链接或 refreshToken）
          </div>
          <div className='text-muted-foreground mb-2 text-xs'>
            把 Trae 登录后的回调链接整段粘贴进来即可；刷新令牌会轮换并踢下同账号其他客户端，建议专号专用。
          </div>
          <textarea
            className='h-16 w-full rounded-md border bg-transparent p-2 font-mono text-xs'
            placeholder='https://…?code=…  或  refreshToken'
            value={traeInput}
            onChange={(e) => setTraeInput(e.target.value)}
          />
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
              onClick={submitTrae}
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

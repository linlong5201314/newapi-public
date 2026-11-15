import { useCallback, useEffect, useMemo, useState } from 'react'

import { getFreeTemplates, type FreeSourceTemplate } from '@/features/my-channels/free-sources'

import {
  createBinding,
  deleteBinding,
  listBindings,
  setBindingPublic,
  type AccountBinding,
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
  const [rows, setRows] = useState<AccountBinding[]>([])
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')

  const accountTemplates = useMemo(
    () =>
      templates.filter(
        (item) => item.authMode === 'account' || item.authMode === 'oauth'
      ),
    [templates]
  )

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
        (item) => item.authMode === 'account' || item.authMode === 'oauth'
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
    if (!accounts.trim()) {
      setMsg('请按「账号----密码」每行一个填写')
      return
    }
    setBusy(true)
    setMsg('')
    const res = await createBinding({
      provider,
      accounts,
      displayName,
      public: isPublic,
    })
    setBusy(false)
    if (res.ok) {
      setAccounts('')
      setMsg('已提交，平台正在为你单独开通（进度见下方列表）')
      await reload()
    } else {
      setMsg(res.message || '提交失败')
    }
  }

  return (
    <div className='mb-3 rounded-lg border p-3'>
      <div className='mb-1 text-sm font-medium'>
        上游账号授权（账号密码加入代理）
      </div>
      <div className='text-muted-foreground mb-2 text-xs'>
        只支持你自己账号：填入后平台为你单独开通实例，账号不会与其他人共用；不需要填写任何 API Key，也看不到部署地址。
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
        placeholder={'账号----密码\n账号----密码（每行一个，可一次粘贴多个）'}
        value={accounts}
        onChange={(e) => setAccounts(e.target.value)}
      />
      <div className='mt-2 flex flex-wrap items-center gap-3'>
        <label className='flex items-center gap-1 text-xs'>
          <input
            type='checkbox'
            checked={isPublic}
            onChange={(e) => setIsPublic(e.target.checked)}
          />
          公开到公共池（其他人可调用，显示你的昵称头像）
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

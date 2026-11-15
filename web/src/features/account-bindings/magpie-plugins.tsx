import { useCallback, useEffect, useState } from 'react'
import { listMagpiePlugins, type MagpiePlugin } from './api'

const AUTH_TEXT: Record<string, string> = {
  account: '账号密码授权',
  oauth: '浏览器 OAuth',
  key: 'API Key / 网关密钥',
}

export function MagpiePluginsCard() {
  const [items, setItems] = useState<MagpiePlugin[]>([])
  const [keyword, setKeyword] = useState('')
  const [loading, setLoading] = useState(false)
  const [copied, setCopied] = useState('')

  const load = useCallback(async (q: string) => {
    setLoading(true)
    try {
      const res = await listMagpiePlugins(q)
      setItems(res.items)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load('')
  }, [load])

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(text)
      window.setTimeout(() => setCopied(''), 1500)
    } catch {
      setCopied('')
    }
  }

  return (
    <section className="mb-4 rounded-xl border border-neutral-200 bg-white p-5">
      <div className="mb-1 flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-base font-semibold">Magpie 插件（22 个精选 provider）</h2>
        <a
          className="text-xs text-blue-600 hover:underline"
          href="https://github.com/topics/magpie-plugin"
          rel="noreferrer"
          target="_blank"
        >
          查看 GitHub 全部 469 个插件
        </a>
      </div>
      <p className="mb-3 text-xs text-neutral-500">
        Magpie 是「一个网关接所有账号」的本地网关。装插件 → <code className="rounded bg-neutral-100 px-1">magpie provider add</code> 加自己的账号 →
        把网关地址和 gateway key 填进上面的「Magpie 聚合网关」渠道。账号风险自担，建议用小号。
      </p>
      <div className="mb-3 flex gap-2">
        <input
          className="w-full rounded-md border border-neutral-300 px-3 py-1.5 text-sm"
          onChange={(e) => setKeyword(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void load(keyword)
          }}
          placeholder="搜索插件名称 / 仓库 / 用途，回车搜索"
          value={keyword}
        />
        <button
          className="rounded-md border border-neutral-300 px-3 py-1.5 text-sm hover:bg-neutral-50"
          onClick={() => void load(keyword)}
          type="button"
        >
          搜索
        </button>
      </div>
      {loading ? (
        <div className="py-6 text-center text-sm text-neutral-400">加载中…</div>
      ) : items.length === 0 ? (
        <div className="py-6 text-center text-sm text-neutral-400">没有匹配的插件</div>
      ) : (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {items.map((p) => (
            <div className="rounded-lg border border-neutral-200 p-3" key={p.id}>
              <div className="flex items-start justify-between gap-2">
                <a className="text-sm font-medium text-neutral-900 hover:underline" href={p.url} rel="noreferrer" target="_blank">
                  {p.name}
                </a>
                <span className="shrink-0 rounded bg-neutral-100 px-1.5 py-0.5 text-[11px] text-neutral-600">★ {p.stars}</span>
              </div>
              <div className="mt-1 text-xs text-neutral-500">{p.desc}</div>
              <div className="mt-2 flex flex-wrap items-center gap-2">
                <span className="rounded bg-blue-50 px-1.5 py-0.5 text-[11px] text-blue-700">
                  {AUTH_TEXT[p.authMode] ?? p.authMode}
                </span>
                <span className="truncate text-[11px] text-neutral-400">{p.repo}</span>
              </div>
              <button
                className="mt-2 w-full truncate rounded border border-neutral-300 px-2 py-1 text-left text-[11px] text-neutral-700 hover:bg-neutral-50"
                onClick={() => void copy(p.install)}
                title={p.install}
                type="button"
              >
                {copied === p.install ? '已复制 ✓' : p.install}
              </button>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}

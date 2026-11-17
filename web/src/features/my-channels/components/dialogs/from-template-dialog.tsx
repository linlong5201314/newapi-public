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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  AlertTriangle,
  ArrowLeft,
  CheckSquare,
  Copy,
  Cpu,
  ExternalLink,
  Gift,
  Globe,
  KeyRound,
  Loader2,
  Network,
  Plus,
  RefreshCw,
  Rss,
  ShieldCheck,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
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
import { Skeleton } from '@/components/ui/skeleton'
import {
  createBinding,
  importTrae,
  pollDs2ApiOAuth,
  pollRaccoonOAuth,
  pollWorkbuddyOAuth,
  startDs2ApiOAuth,
  startRaccoonOAuth,
  startWorkbuddyOAuth,
  type OAuthSession,
} from '@/features/account-bindings/api'
import {
  createSubscription,
  getRuntimeStatus,
  listEndpoints,
  selectRuntimeNode,
} from '@/features/proxy-pool/api'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import {
  createChannelFromTemplate,
  getFreeTemplates,
  probeTemplateModels,
  type FreeSourceTemplate,
} from '../../free-sources'
import { channelsQueryKeys } from '../../lib'

type FromTemplateDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const TEMPLATE_SKELETON_IDS = ['tpl-sk-1', 'tpl-sk-2', 'tpl-sk-3', 'tpl-sk-4']

function tosDotClass(tos?: string): string {
  switch (tos) {
    case 'ok':
      return 'bg-success'
    case 'caution':
      return 'bg-warning'
    case 'avoid':
      return 'bg-destructive'
    default:
      return 'bg-muted-foreground'
  }
}

/** 判定模板类别：api=需API密钥免费层, ip=IP免费层(免密钥), auth=Token/OAuth账号授权, sub=机场订阅 */
function classifyTemplate(tpl: FreeSourceTemplate): 'api' | 'ip' | 'auth' | 'sub' {
  if (tpl.authType === 'subscription' || tpl.authMode === 'subscription') {
    return 'sub'
  }
  if (
    tpl.authMode === 'account' ||
    tpl.authMode === 'oauth' ||
    tpl.authMode === 'gateway' ||
    tpl.authMode === 'bridge'
  ) {
    return 'auth'
  }
  if (tpl.authMode === 'ip' || tpl.keyRequired === false || tpl.authType === 'none') {
    return 'ip'
  }
  return 'api'
}

/** 从粘贴文本中实时提取 Bearer Token 并预览确定性虚拟设备指纹摘要 */
function previewDeepSeekDevices(raw: string): Array<{ label: string; deviceId: string; session: string }> {
  const lines = raw
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l && !l.startsWith('#'))
  if (lines.length === 0) return []
  const bearerMatch = raw.match(/(?:authorization\s*[:=]\s*Bearer\s+|^Bearer\s+)([A-Za-z0-9+/=._-]{32,})/im)
  const seeds = bearerMatch ? [bearerMatch[1]] : lines.slice(0, 5)
  return seeds.map((s, idx) => {
    let h = 2166136261
    for (let i = 0; i < s.length; i++) {
      h ^= s.charCodeAt(i)
      h = Math.imul(h, 16777619)
    }
    const hex = Math.abs(h).toString(16).padStart(8, '0')
    return {
      label: s.includes('----') ? s.split('----')[0] : `Token #${idx + 1} (${s.slice(0, 8)}…)`,
      deviceId: `${hex}-${hex.slice(0, 4)}-4${hex.slice(1, 4)}-a${hex.slice(2, 5)}-${hex}${hex.slice(0, 4)}`,
      session: `ds_session_${hex}${hex.slice(0, 8)}`,
    }
  })
}

function formatTokenCount(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(0)}K`
  return String(n)
}

export function FromTemplateDialog({
  open,
  onOpenChange,
}: FromTemplateDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [categoryTab, setCategoryTab] = useState<'all' | 'api' | 'ip' | 'auth' | 'sub'>('all')
  const [selected, setSelected] = useState<FreeSourceTemplate | null>(null)
  const [apiKey, setApiKey] = useState('')
  const [customName, setCustomName] = useState('')
  const [extraValues, setExtraValues] = useState<Record<string, string>>({})

  // 出口代理策略（独立分区）
  const [proxyMode, setProxyMode] = useState<'own_pool' | 'free_pool' | 'node' | 'direct' | 'custom'>('own_pool')
  const [selectedNodeName, setSelectedNodeName] = useState('')
  const [proxyUrl, setProxyUrl] = useState('')
  const [allowIps, setAllowIps] = useState('')
  const [subscriptionUrl, setSubscriptionUrl] = useState('')
  const [inlineSubUrl, setInlineSubUrl] = useState('')
  const [inlineSubName, setInlineSubName] = useState('')
  const [addingInlineSub, setAddingInlineSub] = useState(false)

  // 模型获取与双列选择（加入我的渠道 vs 加入公共池）
  const [availableModels, setAvailableModels] = useState<string[]>([])
  const [myModels, setMyModels] = useState<string[]>([])
  const [publicModels, setPublicModels] = useState<string[]>([])
  const [probingModels, setProbingModels] = useState(false)
  const [probeInfo, setProbeInfo] = useState<string>('')

  const [visibility, setVisibility] = useState(false)
  const [multiKeys, setMultiKeys] = useState('')
  const [multiKeyMode, setMultiKeyMode] = useState<'random' | 'polling'>('random')
  const [isSubmitting, setIsSubmitting] = useState(false)

  // Token / OAuth 账号授权状态（内嵌在创建渠道弹窗内）
  const [authTokensInput, setAuthTokensInput] = useState('')
  const [traeUid, setTraeUid] = useState('')
  const [oauthRealm, setOauthRealm] = useState<'cn' | 'global'>('cn')
  const [wbSession, setWbSession] = useState<OAuthSession | null>(null)
  const [raccoonSession, setRaccoonSession] = useState<OAuthSession | null>(null)
  const [ds2apiSession, setDs2apiSession] = useState<OAuthSession | null>(null)
  const [oauthStatusText, setOauthStatusText] = useState('')

  const { data: templates, isLoading } = useQuery({
    queryKey: [...channelsQueryKeys.all, 'free-templates'],
    queryFn: getFreeTemplates,
    enabled: open,
    staleTime: 60_000,
  })

  const endpointsQuery = useQuery({
    queryKey: ['proxy-pool', 'endpoints'],
    queryFn: listEndpoints,
    enabled: open,
  })

  const runtimeQuery = useQuery({
    queryKey: ['proxy-pool', 'runtime'],
    queryFn: getRuntimeStatus,
    enabled: open,
  })

  const endpoints = endpointsQuery.data ?? []
  const ownNodes = useMemo(
    () => endpoints.filter((e) => e.is_owner && e.source !== 'haizhu' && e.enabled),
    [endpoints]
  )
  const publicPoolNodes = useMemo(
    () => endpoints.filter((e) => (e.public || e.source === 'haizhu') && e.enabled),
    [endpoints]
  )
  // 自有 IP 每个节点粗估可扩充约 50 万免费层 token；免费公共池不显示剩余 token
  const ownEstimatedTokens = useMemo(() => {
    const perNode = selected?.monthlyTokens && selected.monthlyTokens > 0 ? selected.monthlyTokens : 500_000
    return ownNodes.length * perNode
  }, [ownNodes.length, selected?.monthlyTokens])

  useEffect(() => {
    if (!open) return
    setSelected(null)
    setApiKey('')
    setCustomName('')
    setExtraValues({})
    setProxyMode('own_pool')
    setSelectedNodeName('')
    setProxyUrl('')
    setVisibility(false)
    setMultiKeys('')
    setMultiKeyMode('random')
    setAllowIps('')
    setSubscriptionUrl('')
    setInlineSubUrl('')
    setInlineSubName('')
    setAuthTokensInput('')
    setTraeUid('')
    setOauthStatusText('')
    setWbSession(null)
    setRaccoonSession(null)
    setDs2apiSession(null)
    setIsSubmitting(false)
  }, [open])

  // 切换模板时初始化模型列表与默认代理模式，针对 openrouter/opencode 等自动拉取最新免费模型
  useEffect(() => {
    if (!selected) return
    const list = selected.models ?? []
    setAvailableModels(list)
    setMyModels(list)
    setPublicModels([])
    setProbeInfo('')
    const kind = classifyTemplate(selected)
    if (kind === 'ip') {
      setProxyMode(ownNodes.length > 0 ? 'own_pool' : 'free_pool')
    } else {
      setProxyMode('own_pool')
    }
    if (
      selected.id === 'openrouter-free' ||
      selected.id === 'opencode-free' ||
      selected.id === 'opencode-zen'
    ) {
      void probeTemplateModels({ templateId: selected.id }).then((res) => {
        if (res.success && res.data?.models && res.data.models.length > 0) {
          setAvailableModels(res.data.models)
          setMyModels(res.data.models)
          setProbeInfo(`已自动同步 ${res.data.models.length} 个最新免费 (Free) 模型`)
        }
      })
    }
  }, [selected, ownNodes.length])

  // WorkBuddy OAuth 轮询
  useEffect(() => {
    if (!wbSession) return
    let alive = true
    const timer = window.setInterval(async () => {
      const res = await pollWorkbuddyOAuth(wbSession.sessionId)
      if (!alive) return
      if (res.done) {
        window.clearInterval(timer)
        setWbSession(null)
        toast.success(
          res.channel?.id
            ? `WorkBuddy 授权成功，已开通渠道 #${res.channel.id}`
            : 'WorkBuddy 授权成功，渠道已开通'
        )
        void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        onOpenChange(false)
      }
    }, 3000)
    return () => {
      alive = false
      window.clearInterval(timer)
    }
  }, [wbSession, queryClient, onOpenChange])

  // 小浣熊 OAuth 轮询
  useEffect(() => {
    if (!raccoonSession) return
    let alive = true
    const timer = window.setInterval(async () => {
      const res = await pollRaccoonOAuth(raccoonSession.sessionId)
      if (!alive) return
      if (res.done) {
        window.clearInterval(timer)
        setRaccoonSession(null)
        toast.success(
          res.channel?.id
            ? `小浣熊授权成功，已开通渠道 #${res.channel.id}`
            : '小浣熊授权成功，渠道已开通'
        )
        void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        onOpenChange(false)
      } else if (res.message) {
        window.clearInterval(timer)
        setRaccoonSession(null)
        setOauthStatusText(res.message)
      }
    }, 3000)
    return () => {
      alive = false
      window.clearInterval(timer)
    }
  }, [raccoonSession, queryClient, onOpenChange])

  // DS2API（DeepSeek 网页端）授权链接轮询：授权页确认后自动返回并建好渠道
  useEffect(() => {
    if (!ds2apiSession) return
    let alive = true
    const timer = window.setInterval(async () => {
      const res = await pollDs2ApiOAuth(ds2apiSession.sessionId)
      if (!alive) return
      if (res.done) {
        window.clearInterval(timer)
        setDs2apiSession(null)
        toast.success(
          res.channel?.id
            ? `DS2API 授权成功，已分配独立虚拟设备指纹并开通渠道 #${res.channel.id}`
            : 'DS2API 授权成功，渠道已自动开通'
        )
        void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        onOpenChange(false)
      }
    }, 2500)
    return () => {
      alive = false
      window.clearInterval(timer)
    }
  }, [ds2apiSession, queryClient, onOpenChange])

  const templateKind = selected ? classifyTemplate(selected) : 'api'
  const isSubscription = templateKind === 'sub'
  const isAuthTemplate = templateKind === 'auth'
  const isIpFreeTemplate = templateKind === 'ip'
  const isApiKeyTemplate = templateKind === 'api'

  const extraFields = isAuthTemplate ? [] : (selected?.extraFields ?? [])
  const extraValue = (field: string) =>
    (extraValues[field] ?? selected?.defaultExtra?.[field] ?? '').trim()
  const extrasComplete = extraFields.every((field) => extraValue(field) !== '')

  const parsedKeys = multiKeys
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean)
  const hasAnyKey = apiKey.trim() !== '' || parsedKeys.length > 0
  const subscriptionOk =
    !isSubscription || /^https?:\/\/\S+$/i.test(subscriptionUrl.trim())

  const canSubmit = useMemo(() => {
    if (!selected) return false
    if (isSubscription) return subscriptionOk
    if (isAuthTemplate) {
      if (selected.id === 'magpie') return true
      return authTokensInput.trim().length > 0
    }
    if (isApiKeyTemplate) {
      // API 密钥免费层：必须填 API Key，绝不允许无 Key 绑定 IP 直接创建
      return hasAnyKey && extrasComplete && myModels.length > 0
    }
    // IP 免费层：无需 API Key
    return extrasComplete && myModels.length > 0
  }, [
    selected,
    isSubscription,
    subscriptionOk,
    isAuthTemplate,
    authTokensInput,
    isApiKeyTemplate,
    hasAnyKey,
    extrasComplete,
    myModels.length,
  ])

  const resolveOutboundProxy = (): string => {
    if (proxyMode === 'direct') return 'direct'
    if (proxyMode === 'custom') return proxyUrl.trim()
    return ''
  }

  // 在创建渠道弹窗内直接添加机场订阅并刷新节点
  const handleAddInlineSubscription = async () => {
    const url = inlineSubUrl.trim()
    if (!/^https?:\/\/\S+$/i.test(url)) {
      toast.error('请输入以 http:// 或 https:// 开头的机场订阅链接')
      return
    }
    setAddingInlineSub(true)
    try {
      const res = await createSubscription({
        name: inlineSubName.trim() || `${selected?.name || '渠道'}订阅`,
        url,
        format: 'auto',
        public: false,
      })
      if (res.success === false) {
        toast.error(res.message || '同步订阅失败')
        return
      }
      toast.success(`订阅已同步：解析出 ${res.data?.node_count ?? 0} 个可用节点（默认仅自己可见可用）`)
      setInlineSubUrl('')
      setInlineSubName('')
      setProxyMode('node')
      await Promise.all([endpointsQuery.refetch(), runtimeQuery.refetch()])
    } catch (error) {
      handleServerError(error, '同步订阅失败')
    } finally {
      setAddingInlineSub(false)
    }
  }

  // 点击「获取模型」按钮（优先走低延迟可用节点探测上游）
  const handleProbeModels = async () => {
    if (!selected) return
    setProbingModels(true)
    setProbeInfo('')
    try {
      if (proxyMode === 'node' && selectedNodeName) {
        await selectRuntimeNode(selectedNodeName).catch(() => undefined)
      }
      const extra = extraFields.length
        ? Object.fromEntries(extraFields.map((field) => [field, extraValue(field)]))
        : undefined
      const res = await probeTemplateModels({
        templateId: selected.id,
        key: apiKey.trim() || parsedKeys[0] || '',
        proxy: resolveOutboundProxy(),
        extra,
      })
      if (res.success && res.data?.models && res.data.models.length > 0) {
        const fetched = res.data.models
        setAvailableModels(fetched)
        setMyModels((prev) => (prev.length > 0 ? Array.from(new Set([...prev, ...fetched])) : fetched))
        const modeNote = res.data.probed_live
          ? `已从上游实时拉取 ${fetched.length} 个模型（耗时 ${res.data.latency_ms} ms）`
          : `已同步 ${fetched.length} 个可用模型`
        setProbeInfo(modeNote)
        toast.success(modeNote)
      } else {
        toast.error(res.message || '未获取到额外模型，已保留默认模型列表')
      }
    } catch (error) {
      handleServerError(error, '获取模型失败')
    } finally {
      setProbingModels(false)
    }
  }

  const startWorkBuddyFlow = async () => {
    setOauthStatusText('正在创建 WorkBuddy 设备授权会话…')
    const sess = await startWorkbuddyOAuth(oauthRealm)
    if (!sess) {
      setOauthStatusText('发起授权失败，请稍后重试')
      return
    }
    setWbSession(sess)
    setOauthStatusText('已打开授权窗口，请在浏览器完成登录，本弹窗将自动开通渠道…')
    window.open(sess.authUrl, '_blank', 'noopener,noreferrer')
  }

  const startRaccoonFlow = async () => {
    setOauthStatusText('正在创建小浣熊浏览器授权会话…')
    const sess = await startRaccoonOAuth()
    if (!sess) {
      setOauthStatusText('发起授权失败，请稍后重试')
      return
    }
    setRaccoonSession(sess)
    setOauthStatusText('已打开小浣熊登录页，授权后本弹窗将自动开通渠道…')
    window.open(sess.authUrl, '_blank', 'noopener,noreferrer')
  }

  const startDs2ApiFlow = async () => {
    setOauthStatusText('正在生成专属 DS2API 授权登录链接…')
    const sess = await startDs2ApiOAuth()
    if (!sess) {
      setOauthStatusText('生成授权链接失败，请稍后重试')
      return
    }
    setDs2apiSession(sess)
    setOauthStatusText('已生成专属授权链接！可复制链接去浏览器登录，授权完成后本页将自动返回并开通渠道…')
    void navigator.clipboard
      ?.writeText(sess.authUrl)
      .then(() => toast.success('DS2API 授权链接已复制到剪贴板，去浏览器打开完成授权即可自动返回'))
      .catch(() => undefined)
  }

  const handleSubmit = async () => {
    if (!selected || !canSubmit) return
    setIsSubmitting(true)
    try {
      if (proxyMode === 'node' && selectedNodeName) {
        await selectRuntimeNode(selectedNodeName).catch(() => undefined)
      }

      // 1) Token / OAuth 账号授权类模板：直接在弹窗内完成授权建渠道
      if (isAuthTemplate) {
        const raw = authTokensInput.trim()
        if (selected.id === 'trae-cn' || selected.id === 'trae-intl') {
          const looksUrl = raw.startsWith('http') || raw.includes('code=')
          const res = await importTrae(
            looksUrl
              ? { callbackUrl: raw, uid: traeUid.trim() || undefined }
              : { refreshToken: raw, uid: traeUid.trim() || undefined }
          )
          if (res.ok) {
            toast.success(res.notice || 'Trae 授权导入成功，渠道已开通')
            void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
            onOpenChange(false)
          } else {
            toast.error(res.message || 'Trae 授权导入失败')
          }
          return
        }

        // Qoder CN / 书生·端砚 / Kimi 办公版 / 小米 MiMo：若用户直接贴了 Token，同时支持直接建渠道 + 写入账号绑定
        if (
          selected.id === 'qoder-cn' ||
          selected.id === 'internlm-inkstone' ||
          selected.id === 'kimi-work' ||
          selected.id === 'xiaomi-mimo' ||
          selected.id === 'cline-account'
        ) {
          const firstToken = raw
            .split('\n')
            .map((l) => l.trim())
            .find((l) => l && !l.startsWith('#'))
          if (firstToken && !firstToken.includes('http')) {
            const directRes = await createChannelFromTemplate({
              templateId: selected.id,
              key: firstToken,
              ...(customName.trim() ? { name: customName.trim() } : {}),
              proxy: resolveOutboundProxy(),
              visibility: visibility || publicModels.length > 0 ? 1 : 0,
              tier: 'api',
              models: myModels,
              public_models: publicModels,
            })
            if (directRes.success) {
              toast.success(`${selected.name} Token 授权渠道已创建`)
              void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
              onOpenChange(false)
              return
            }
          }
        }

        // DS2API / 小浣熊 / Magpie 等：提交 Token 绑定（自带每号独立虚拟设备指纹）
        const bindRes = await createBinding({
          provider: selected.id,
          accounts: raw,
          displayName: customName.trim() || selected.name,
          public: visibility || publicModels.length > 0,
          proxy: resolveOutboundProxy(),
        })
        if (bindRes.ok) {
          toast.success('Token 授权已提交，已为每个账号分配独立虚拟设备指纹并开通渠道')
          void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
          onOpenChange(false)
        } else {
          toast.error(bindRes.message || '提交授权失败')
        }
        return
      }

      // 2) API 密钥免费层 / IP 免费层 / 机场订阅模板
      const extra = extraFields.length
        ? Object.fromEntries(extraFields.map((field) => [field, extraValue(field)]))
        : undefined
      const proxy = resolveOutboundProxy()
      const effectiveTier = isIpFreeTemplate ? 'free' : 'api'
      const effectiveVisibility = visibility || publicModels.length > 0 ? 1 : 0

      const result = await createChannelFromTemplate({
        templateId: selected.id,
        key: apiKey.trim(),
        ...(customName.trim() ? { name: customName.trim() } : {}),
        ...(extra ? { extra } : {}),
        ...(proxy ? { proxy } : {}),
        visibility: effectiveVisibility,
        tier: effectiveTier,
        models: myModels,
        public_models: publicModels,
        ...(parsedKeys.length > 0
          ? { keys: parsedKeys, multi_key_mode: multiKeyMode }
          : {}),
        ...(effectiveTier === 'free' && allowIps.trim()
          ? { allow_ips: allowIps.trim() }
          : {}),
        ...(subscriptionUrl.trim()
          ? { subscription_url: subscriptionUrl.trim() }
          : {}),
      })
      if (result.success) {
        toast.success(
          isSubscription
            ? '机场订阅已保存并同步到出口池'
            : `渠道已创建（加入我的渠道 ${myModels.length} 个模型${
                publicModels.length > 0 ? `，公开到公共池 ${publicModels.length} 个模型` : ''
              }）`
        )
        void queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.lists(),
        })
        void queryClient.invalidateQueries({
          queryKey: [...channelsQueryKeys.all, 'free-usage'],
        })
        onOpenChange(false)
      } else {
        handleServerError(result, t('Failed to create channel from template'))
      }
    } catch (error) {
      handleServerError(error, t('Failed to create channel from template'))
    } finally {
      setIsSubmitting(false)
    }
  }

  const filteredTemplates = useMemo(() => {
    if (!templates) return []
    if (categoryTab === 'all') return templates
    return templates.filter((tpl) => classifyTemplate(tpl) === categoryTab)
  }, [templates, categoryTab])

  const dsDevicePreviews = useMemo(
    () => (selected?.id === 'ds2api' ? previewDeepSeekDevices(authTokensInput) : []),
    [selected?.id, authTokensInput]
  )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[88vh] flex-col sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <Gift className='size-4' />
            添加免费源 / 授权创建渠道
          </DialogTitle>
          <DialogDescription>
            {selected
              ? `${selected.name} — ${
                  isApiKeyTemplate
                    ? 'API 密钥免费层（必须填 API Key，不可仅凭 IP 绕过）'
                    : isIpFreeTemplate
                      ? 'IP 免费层（无需密钥，按出口 IP 与虚拟设备轮换扩容）'
                      : isAuthTemplate
                        ? 'Token / OAuth 账号授权（直接在此完成授权登录并选模型）'
                        : '机场订阅导入'
                }`
              : '严格区分「API 密钥免费层」「IP 免费层（免密钥换 IP）」与「Token/OAuth 账号授权（WorkBuddy / Trae / 小浣熊 / Cline / Qoder CN / 书生·端砚 / DS2API）」。'}
          </DialogDescription>
        </DialogHeader>

        {selected === null ? (
          <div className='flex min-h-0 flex-1 flex-col gap-3 overflow-hidden'>
            {/* 分类切换栏 */}
            <div className='flex flex-wrap gap-1.5 border-b pb-2 text-xs'>
              {(
                [
                  ['all', '全部来源'],
                  ['ip', '🌐 IP 免费层（免密钥·换 IP 用）'],
                  ['auth', '🛡️ Token / OAuth 授权（Trae/小浣熊/Qoder/DS2API）'],
                  ['api', '🔑 API 密钥免费层（需 API Key）'],
                  ['sub', '✈️ 机场订阅'],
                ] as const
              ).map(([key, label]) => (
                <button
                  key={key}
                  type='button'
                  onClick={() => setCategoryTab(key)}
                  className={cn(
                    'rounded-md px-2.5 py-1 transition-colors',
                    categoryTab === key
                      ? 'bg-primary text-primary-foreground font-medium'
                      : 'bg-muted/60 text-muted-foreground hover:text-foreground'
                  )}
                >
                  {label}
                </button>
              ))}
            </div>

            <div className='min-h-0 flex-1 overflow-y-auto pr-1'>
              {isLoading ? (
                <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                  {TEMPLATE_SKELETON_IDS.map((id) => (
                    <Skeleton key={id} className='h-24 rounded-lg' />
                  ))}
                </div>
              ) : filteredTemplates.length === 0 ? (
                <div className='text-muted-foreground rounded-lg border border-dashed p-8 text-center text-sm'>
                  暂无匹配的免费源模板
                </div>
              ) : (
                <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                  {filteredTemplates.map((template) => {
                    const kind = classifyTemplate(template)
                    return (
                      <button
                        key={template.id}
                        type='button'
                        onClick={() => setSelected(template)}
                        className='border-border/60 hover:border-primary/60 hover:bg-muted/40 rounded-lg border p-3 text-start transition-colors'
                      >
                        <div className='flex items-center justify-between gap-2'>
                          <span className='min-w-0 truncate text-sm font-medium'>
                            {template.name}
                          </span>
                          <Badge
                            variant={
                              kind === 'ip'
                                ? 'default'
                                : kind === 'auth'
                                  ? 'secondary'
                                  : 'outline'
                            }
                            className='shrink-0 text-[10px]'
                          >
                            {kind === 'ip'
                              ? 'IP免费层·免Key'
                              : kind === 'auth'
                                ? 'Token/OAuth授权'
                                : kind === 'sub'
                                  ? '机场订阅'
                                  : '需API密钥'}
                          </Badge>
                        </div>
                        {template.freeNote && (
                          <div className='text-muted-foreground mt-1 line-clamp-2 text-xs'>
                            {template.freeNote}
                          </div>
                        )}
                        <div className='mt-1.5 flex items-center justify-between text-xs text-muted-foreground'>
                          <span>
                            {template.models && template.models.length > 0
                              ? `${template.models.length} 个预置模型`
                              : '支持动态获取模型'}
                          </span>
                          <span className='inline-flex items-center gap-1'>
                            <span
                              className={cn(
                                'size-2 rounded-full',
                                tosDotClass(template.tos)
                              )}
                            />
                          </span>
                        </div>
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          </div>
        ) : (
          <div className='flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto pr-1'>
            {/* 顶部模板说明卡 */}
            <div className='border-border/60 rounded-lg border p-3'>
              <div className='flex items-center justify-between gap-2'>
                <span className='text-sm font-medium'>{selected.name}</span>
                <Badge variant={isIpFreeTemplate ? 'default' : isAuthTemplate ? 'secondary' : 'outline'}>
                  {isIpFreeTemplate
                    ? '🌐 IP 免费层（免密钥）'
                    : isAuthTemplate
                      ? '🛡️ Token / OAuth 账号授权'
                      : isSubscription
                        ? '✈️ 机场订阅'
                        : '🔑 API 密钥免费层（必填 Key）'}
                </Badge>
              </div>
              {selected.freeNote && (
                <div className='text-muted-foreground mt-1 whitespace-pre-line text-xs'>
                  {selected.freeNote}
                </div>
              )}
              {selected.signupUrl && (
                <a
                  href={selected.signupUrl}
                  target='_blank'
                  rel='noreferrer'
                  className='text-primary mt-2 inline-flex items-center gap-1 text-xs hover:underline'
                >
                  打开官方控制台 / 获取凭据 ({selected.signupUrl})
                  <ExternalLink className='size-3' />
                </a>
              )}
            </div>

            {/* 区块一：凭据 / Token / OAuth 授权区（与下方代理区严格分离） */}
            <div className='rounded-xl border border-border/80 bg-card p-3.5 space-y-3'>
              <div className='flex items-center gap-2 text-xs font-semibold'>
                <KeyRound className='size-3.5 text-primary' />
                <span>
                  第一步：
                  {isSubscription
                    ? '填写机场订阅链接'
                    : isAuthTemplate
                      ? 'Token / OAuth 授权登录'
                      : isIpFreeTemplate
                        ? '免密钥验证说明（IP 免费层）'
                        : '填写上游 API Key'}
                </span>
              </div>

              {isSubscription ? (
                <div className='space-y-2'>
                  <Input
                    value={subscriptionUrl}
                    onChange={(e) => setSubscriptionUrl(e.target.value)}
                    placeholder='https://example.com/api/v1/client/subscribe?token=...'
                    autoComplete='off'
                  />
                  <p className='text-muted-foreground text-xs'>
                    自动解析 AnyTLS / VLESS / VMess / Trojan / SS 节点并过滤流量公告假节点，注入共享 mihomo 内核。
                  </p>
                </div>
              ) : isAuthTemplate ? (
                <div className='space-y-3'>
                  {/* WorkBuddy 一键设备授权 */}
                  {selected.id === 'workbuddy' && (
                    <div className='flex flex-wrap items-center gap-2 rounded-lg bg-muted/50 p-2.5 text-xs'>
                      <select
                        className='h-8 rounded-md border bg-background px-2 text-xs'
                        value={oauthRealm}
                        onChange={(e) => setOauthRealm(e.target.value as 'cn' | 'global')}
                      >
                        <option value='cn'>WorkBuddy 国内版（支持签到）</option>
                        <option value='global'>WorkBuddy 国际版</option>
                      </select>
                      <Button size='sm' type='button' onClick={() => void startWorkBuddyFlow()}>
                        {wbSession ? '重新打开设备授权' : '🚀 开始 WorkBuddy OAuth 授权'}
                      </Button>
                      {oauthStatusText && <span className='text-primary'>{oauthStatusText}</span>}
                    </div>
                  )}

                  {/* 小浣熊 一键浏览器授权 */}
                  {selected.id === 'xiaohuanxiong' && (
                    <div className='flex flex-wrap items-center gap-2 rounded-lg bg-muted/50 p-2.5 text-xs'>
                      <Button size='sm' type='button' onClick={() => void startRaccoonFlow()}>
                        {raccoonSession ? '重新打开小浣熊授权' : '🦝 开始小浣熊浏览器 OAuth 授权'}
                      </Button>
                      <span className='text-muted-foreground'>
                        点击在新窗口登录小浣熊即可自动回跳建渠道，或在下方直接粘贴回调链接 / Token：
                      </span>
                      {oauthStatusText && <span className='text-primary'>{oauthStatusText}</span>}
                    </div>
                  )}

                  {/* DS2API 一键生成授权链接 → 复制去浏览器登录 → 自动返回开通渠道 */}
                  {selected.id === 'ds2api' && (
                    <div className='space-y-2 rounded-lg border border-primary/30 bg-primary/5 p-3 text-xs'>
                      <div className='flex flex-wrap items-center justify-between gap-2'>
                        <span className='font-medium text-foreground'>
                          🔗 方式一：生成专属授权链接 → 复制去浏览器登录，授权后自动返回开通渠道
                        </span>
                        <div className='flex flex-wrap gap-1.5'>
                          <Button
                            size='sm'
                            type='button'
                            className='h-8 text-xs'
                            onClick={() => void startDs2ApiFlow()}
                          >
                            {ds2apiSession ? '🔄 重新生成并复制链接' : '🔗 生成 DS2API 授权链接'}
                          </Button>
                          {ds2apiSession && (
                            <>
                              <Button
                                size='sm'
                                variant='secondary'
                                type='button'
                                className='h-8 text-xs'
                                onClick={() => {
                                  void navigator.clipboard
                                    ?.writeText(ds2apiSession.authUrl)
                                    .then(() => toast.success('授权链接已复制'))
                                }}
                              >
                                <Copy className='me-1 size-3.5' />
                                复制链接
                              </Button>
                              <Button
                                size='sm'
                                variant='outline'
                                type='button'
                                className='h-8 text-xs'
                                onClick={() =>
                                  window.open(ds2apiSession.authUrl, '_blank', 'noopener,noreferrer')
                                }
                              >
                                <ExternalLink className='me-1 size-3.5' />
                                直接打开授权页
                              </Button>
                            </>
                          )}
                        </div>
                      </div>
                      {ds2apiSession && (
                        <Input
                          readOnly
                          value={ds2apiSession.authUrl}
                          onClick={(e) => (e.target as HTMLInputElement).select()}
                          className='h-8 font-mono text-xs'
                        />
                      )}
                      {oauthStatusText && (
                        <div className='flex items-center gap-1.5 text-primary'>
                          <Loader2 className='size-3.5 animate-spin' />
                          <span>{oauthStatusText}</span>
                        </div>
                      )}
                    </div>
                  )}

                  {/* Trae / Cline / Qoder CN / 书生·端砚 快捷跳转授权按钮 */}
                  {(selected.id === 'trae-cn' ||
                    selected.id === 'trae-intl' ||
                    selected.id === 'cline-account' ||
                    selected.id === 'qoder-cn' ||
                    selected.id === 'internlm-inkstone') && (
                    <div className='flex flex-wrap items-center gap-2 rounded-lg bg-muted/50 p-2.5 text-xs'>
                      <Button
                        size='sm'
                        variant='outline'
                        type='button'
                        onClick={() =>
                          window.open(
                            selected.signupUrl ||
                              (selected.id === 'trae-cn'
                                ? 'https://www.trae.cn'
                                : selected.id === 'trae-intl'
                                  ? 'https://www.trae.ai'
                                  : selected.id === 'cline-account'
                                    ? 'https://app.cline.bot'
                                    : selected.id === 'qoder-cn'
                                      ? 'https://qoder.cn/account/integrations'
                                      : 'https://discovery.intern-ai.org.cn/'),
                            '_blank',
                            'noopener,noreferrer'
                          )
                        }
                      >
                        <ExternalLink className='me-1 size-3.5' />
                        打开 {selected.name} 授权 / Token 页面
                      </Button>
                      <span className='text-muted-foreground'>
                        登录后复制 Token / 回调 URL 粘贴到下方即可直接开通渠道
                      </span>
                    </div>
                  )}

                  <textarea
                    value={authTokensInput}
                    onChange={(e) => setAuthTokensInput(e.target.value)}
                    rows={4}
                    placeholder={
                      selected.id === 'ds2api'
                        ? '推荐直接粘贴 DeepSeek Bearer Token（如 SlCmRF5j+IX2LBorDe4nBY5OHI1F8bmSp2HEkCsAX1pPWJ38B7GOPyYhcqGzoCMD）\n或直接把 F12 抓包的整段 Request Headers（含 authorization: Bearer ...）粘贴进来，系统会自动提取 Token！\n也支持 账号----Token 或 账号----密码（每行一个）'
                        : selected.id === 'trae-cn' || selected.id === 'trae-intl'
                          ? '粘贴 Trae 登录回调链接 https://...?code=... 或 refreshToken'
                          : selected.id === 'qoder-cn'
                            ? '粘贴 Qoder CN 的 Personal Access Token (PAT) 或桌面端登录态 token（每行一个）'
                            : selected.id === 'cline-account'
                              ? '粘贴 Cline WorkOS 浏览器授权后的回调链接或 access_token（每行一个）'
                              : selected.id === 'internlm-inkstone'
                                ? '粘贴书生·端砚 Token Plan 的 sk-/ink- Key，或控制台 Cookie 中的 uaa-token'
                                : '粘贴授权 Token（每行一个）'
                    }
                    className='border-input bg-background w-full rounded-md border p-2.5 font-mono text-xs'
                  />

                  {(selected.id === 'trae-cn' || selected.id === 'trae-intl') && (
                    <Input
                      value={traeUid}
                      onChange={(e) => setTraeUid(e.target.value)}
                      placeholder='Trae UID（可选，用回调链接导入时自动识别）'
                      className='h-8 text-xs'
                    />
                  )}

                  {/* DS2API 每号独立虚拟设备指纹实时预览 */}
                  {selected.id === 'ds2api' && dsDevicePreviews.length > 0 && (
                    <div className='rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-2.5 text-xs space-y-1'>
                      <div className='flex items-center gap-1.5 font-medium text-emerald-600'>
                        <Cpu className='size-3.5' />
                        已为当前输入的 {dsDevicePreviews.length} 个账号生成独立虚拟设备指纹与请求头（一账号一设备）：
                      </div>
                      {dsDevicePreviews.map((d, i) => (
                        <div key={i} className='font-mono text-[11px] text-muted-foreground truncate'>
                          • {d.label} → x-device-id: {d.deviceId} | Cookie: {d.session}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              ) : isIpFreeTemplate ? (
                <div className='rounded-lg border border-primary/30 bg-primary/5 p-3 text-xs space-y-1'>
                  <div className='font-medium text-foreground'>
                    ✅ 该来源属于「IP 免费层」，无需填写 API Key！
                  </div>
                  <div className='text-muted-foreground'>
                    直接通过下方绑定的出口 IP 池（机场订阅或 Haizhu 7114 公共免费节点池）与虚拟设备指纹轮换调用，遇限流或报错自动熔断切换下一个 IP。
                  </div>
                </div>
              ) : (
                /* API 密钥免费层：强制要求 API Key，不提供免 Key 绑 IP 绕过 */
                <div className='space-y-3'>
                  <div className='space-y-1.5'>
                    <Label htmlFor='from-template-key' className='text-xs'>
                      上游 API Key <span className='text-destructive'>*（API 免费层必填，不可仅绑 IP 空手创建）</span>
                    </Label>
                    <Input
                      id='from-template-key'
                      type='password'
                      value={apiKey}
                      onChange={(e) => setApiKey(e.target.value)}
                      placeholder='粘贴该免费源的 API Key (如 gsk_... / AIza... / sk-...)'
                      autoComplete='off'
                    />
                  </div>
                  <div className='space-y-1.5'>
                    <Label htmlFor='from-template-multikey' className='text-xs'>
                      多密钥轮询（可选，每行一个 API Key，自动合并为多密钥渠道）
                    </Label>
                    <textarea
                      id='from-template-multikey'
                      value={multiKeys}
                      onChange={(e) => setMultiKeys(e.target.value)}
                      rows={2}
                      placeholder='留空则使用上方单个 API Key；填多个 Key 时自动按轮询/随机分摊额度'
                      className='border-input bg-background w-full rounded-md border px-3 py-1.5 font-mono text-xs'
                    />
                  </div>
                </div>
              )}

              {extraFields.map((field) => (
                <div key={field} className='space-y-1'>
                  <Label htmlFor={`from-template-extra-${field}`} className='text-xs'>
                    {field} <span className='text-destructive'>*</span>
                  </Label>
                  <Input
                    id={`from-template-extra-${field}`}
                    value={extraValues[field] ?? selected.defaultExtra?.[field] ?? ''}
                    onChange={(e) =>
                      setExtraValues((prev) => ({
                        ...prev,
                        [field]: e.target.value,
                      }))
                    }
                    placeholder={selected.defaultExtra?.[field] ?? `填写 ${field}`}
                    autoComplete='off'
                  />
                </div>
              ))}
            </div>

            {/* 区块二：独立分离的「出口代理 & 机场订阅 & 节点选择」区 */}
            {!isSubscription && (
              <div className='rounded-xl border border-border/80 bg-card p-3.5 space-y-3'>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <div className='flex items-center gap-2 text-xs font-semibold'>
                    <Network className='size-3.5 text-primary' />
                    <span>第二步：出口代理与机场订阅节点选择（独立配置，与上方凭据分离）</span>
                  </div>
                  {proxyMode !== 'free_pool' && ownNodes.length > 0 && (
                    <Badge variant='secondary' className='font-mono text-[11px]'>
                      自有订阅/IP: {ownNodes.length} 个 · 预估总额度 ≈ {formatTokenCount(ownEstimatedTokens)} tokens
                    </Badge>
                  )}
                </div>

                <div className='grid grid-cols-1 gap-2 sm:grid-cols-2'>
                  <select
                    value={proxyMode}
                    onChange={(e) =>
                      setProxyMode(
                        e.target.value as 'own_pool' | 'free_pool' | 'node' | 'direct' | 'custom'
                      )
                    }
                    className='border-input bg-background h-9 w-full rounded-md border px-3 text-xs'
                  >
                    <option value='own_pool'>
                      🔄 跟随我的自有订阅/出口池轮换（自有节点 {ownNodes.length} 个）
                    </option>
                    <option value='free_pool'>
                      🌐 使用免费公共 IP 池（Haizhu 等公开节点 {publicPoolNodes.length} 个 · 自动熔断）
                    </option>
                    <option value='node'>
                      ⚡ 指定机场订阅/池内单个低延迟节点（优先测速好的节点）
                    </option>
                    <option value='direct'>🚫 强制直连（不走代理）</option>
                    <option value='custom'>🔧 手动填写固定代理 URL (http/socks5)</option>
                  </select>

                  {proxyMode === 'node' && (
                    <select
                      value={selectedNodeName}
                      onChange={(e) => setSelectedNodeName(e.target.value)}
                      className='border-input bg-background h-9 w-full rounded-md border px-3 text-xs font-mono'
                    >
                      <option value='PUBLIC_FAST'>⚡ 自动测速最快节点 (PUBLIC_FAST)</option>
                      {endpoints.slice(0, 150).map((ep) => (
                        <option
                          key={ep.id}
                          value={`u${ep.user_id || 1}·${ep.name}`}
                        >
                          {ep.is_owner && ep.source !== 'haizhu' ? '[自有] ' : '[公共池] '}
                          {ep.name} ({ep.region || ep.kind}
                          {ep.latency_ms ? ` · ${ep.latency_ms}ms` : ''})
                        </option>
                      ))}
                    </select>
                  )}

                  {proxyMode === 'custom' && (
                    <Input
                      value={proxyUrl}
                      onChange={(e) => setProxyUrl(e.target.value)}
                      placeholder='http://user:pass@1.2.3.4:8080 或 socks5://127.0.0.1:7897'
                      className='h-9 text-xs font-mono'
                    />
                  )}
                </div>

                {/* 免费公共 IP 池慢速与不显示剩余 Token 警告（Item 2.3 & 5） */}
                {proxyMode === 'free_pool' && (
                  <div className='flex items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 p-2.5 text-xs text-amber-700 dark:text-amber-300'>
                    <AlertTriangle className='mt-0.5 size-4 shrink-0' />
                    <div>
                      <strong>免费公共 IP 源提示：</strong>
                      使用公共免费 IP 池（含 Haizhu 节点）可能存在速度较慢、延迟波动或偶发连接出错；系统已开启<strong>自动熔断切换</strong>（遇错无感转接下一个 IP）。免费公共 IP 池不计量也不显示剩余 Token 额度。
                    </div>
                  </div>
                )}

                {/* 在创建渠道弹窗内直接添加机场订阅（Item 2.3 & 2.5） */}
                <div className='rounded-lg border border-dashed p-2.5 space-y-2'>
                  <div className='flex items-center justify-between text-xs'>
                    <span className='flex items-center gap-1.5 font-medium text-muted-foreground'>
                      <Rss className='size-3.5' />
                      在此直接添加机场订阅到我的出口池（可选，同步后即可在上方选节点）：
                    </span>
                  </div>
                  <div className='flex flex-wrap gap-2'>
                    <Input
                      value={inlineSubName}
                      onChange={(e) => setInlineSubName(e.target.value)}
                      placeholder='订阅备注（可选）'
                      className='h-8 w-36 text-xs'
                    />
                    <Input
                      value={inlineSubUrl}
                      onChange={(e) => setInlineSubUrl(e.target.value)}
                      placeholder='粘贴机场订阅链接 https://...'
                      className='h-8 min-w-[220px] flex-1 font-mono text-xs'
                    />
                    <Button
                      size='sm'
                      variant='secondary'
                      type='button'
                      disabled={addingInlineSub || !inlineSubUrl.trim()}
                      onClick={() => void handleAddInlineSubscription()}
                      className='h-8 text-xs'
                    >
                      {addingInlineSub ? (
                        <Loader2 className='me-1 size-3.5 animate-spin' />
                      ) : (
                        <Plus className='me-1 size-3.5' />
                      )}
                      同步订阅节点
                    </Button>
                  </div>
                </div>
              </div>
            )}

            {/* 区块三：获取模型 + 可选择性勾选加入「我的渠道」与「公共池」（Items 2.3, 2.5） */}
            {!isSubscription && (
              <div className='rounded-xl border border-border/80 bg-card p-3.5 space-y-3'>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <div className='flex items-center gap-2 text-xs font-semibold'>
                    <CheckSquare className='size-3.5 text-primary' />
                    <span>第三步：获取模型并选择加入「我的渠道」或「公共池」</span>
                  </div>
                  <div className='flex items-center gap-2'>
                    <Button
                      size='sm'
                      variant='outline'
                      type='button'
                      disabled={probingModels}
                      onClick={() => void handleProbeModels()}
                      className='h-7 text-xs'
                    >
                      {probingModels ? (
                        <Loader2 className='me-1 size-3.5 animate-spin' />
                      ) : (
                        <RefreshCw className='me-1 size-3.5' />
                      )}
                      获取模型（优先测速快节点）
                    </Button>
                  </div>
                </div>

                {probeInfo && (
                  <div className='text-[11px] text-emerald-600'>{probeInfo}</div>
                )}

                <div className='flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground'>
                  <span>
                    已选加入我的渠道: <strong>{myModels.length}</strong> 个 · 已选公开到公共池:{' '}
                    <strong>{publicModels.length}</strong> 个
                  </span>
                  <div className='flex flex-wrap gap-2'>
                    <button
                      type='button'
                      className='text-primary hover:underline font-medium'
                      onClick={() => setMyModels([...availableModels])}
                    >
                      一键全选（启用在我的渠道）
                    </button>
                    <span>·</span>
                    <button
                      type='button'
                      className='text-muted-foreground hover:text-foreground hover:underline'
                      onClick={() => {
                        setMyModels([])
                        setPublicModels([])
                      }}
                    >
                      一键全不选（我的渠道）
                    </button>
                    <span>·</span>
                    <button
                      type='button'
                      className='text-emerald-600 hover:underline font-medium'
                      onClick={() => {
                        if (publicModels.length === availableModels.length) {
                          setPublicModels([])
                        } else {
                          setMyModels([...availableModels])
                          setPublicModels([...availableModels])
                        }
                      }}
                    >
                      {publicModels.length === availableModels.length
                        ? '全不选公开'
                        : '全选加入公共池'}
                    </button>
                  </div>
                </div>

                <div className='max-h-48 overflow-y-auto rounded-lg border p-2 space-y-1'>
                  {availableModels.length === 0 ? (
                    <div className='py-4 text-center text-xs text-muted-foreground'>
                      点击右上方「获取模型」按钮拉取可用模型列表
                    </div>
                  ) : (
                    availableModels.map((modelName) => {
                      const inMine = myModels.includes(modelName)
                      const inPub = publicModels.includes(modelName)
                      return (
                        <div
                          key={modelName}
                          className='flex items-center justify-between gap-2 rounded px-2 py-1 hover:bg-muted/60 text-xs'
                        >
                          <span className='truncate font-mono'>{modelName}</span>
                          <div className='flex shrink-0 items-center gap-4'>
                            <label className='inline-flex cursor-pointer items-center gap-1'>
                              <input
                                type='checkbox'
                                checked={inMine}
                                onChange={(e) => {
                                  const checked = e.target.checked
                                  setMyModels((prev) =>
                                    checked
                                      ? [...prev, modelName]
                                      : prev.filter((m) => m !== modelName)
                                  )
                                  if (!checked) {
                                    setPublicModels((prev) =>
                                      prev.filter((m) => m !== modelName)
                                    )
                                  }
                                }}
                                className='size-3.5 accent-primary'
                              />
                              <span>加入我的渠道</span>
                            </label>
                            <label className='inline-flex cursor-pointer items-center gap-1 text-emerald-600'>
                              <input
                                type='checkbox'
                                checked={inPub}
                                onChange={(e) => {
                                  const checked = e.target.checked
                                  if (checked && !inMine) {
                                    setMyModels((prev) => [...prev, modelName])
                                  }
                                  setPublicModels((prev) =>
                                    checked
                                      ? [...prev, modelName]
                                      : prev.filter((m) => m !== modelName)
                                  )
                                }}
                                className='size-3.5 accent-emerald-600'
                              />
                              <Globe className='size-3' />
                              <span>加入公共池</span>
                            </label>
                          </div>
                        </div>
                      )
                    })
                  )}
                </div>

                <div className='grid grid-cols-1 gap-2 sm:grid-cols-2 pt-1'>
                  <div className='space-y-1'>
                    <Label htmlFor='from-template-name' className='text-xs'>
                      渠道名称（可选）
                    </Label>
                    <Input
                      id='from-template-name'
                      value={customName}
                      onChange={(e) => setCustomName(e.target.value)}
                      placeholder={selected.name}
                      className='h-8 text-xs'
                    />
                  </div>
                  {isIpFreeTemplate && (
                    <div className='space-y-1'>
                      <Label className='text-xs'>入口 IP 白名单（可选，空=不限）</Label>
                      <Input
                        value={allowIps}
                        onChange={(e) => setAllowIps(e.target.value)}
                        placeholder='如 1.2.3.4, 10.0.0.0/8'
                        className='h-8 font-mono text-xs'
                      />
                    </div>
                  )}
                </div>
              </div>
            )}
          </div>
        )}

        <DialogFooter className='gap-2'>
          {selected !== null && (
            <Button
              variant='ghost'
              onClick={() => setSelected(null)}
              disabled={isSubmitting}
            >
              <ArrowLeft className='size-4' />
              {t('Back')}
            </Button>
          )}
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          {selected !== null && (
            <Button
              onClick={() => void handleSubmit()}
              disabled={!canSubmit || isSubmitting}
            >
              {isSubmitting && <Loader2 className='size-4 animate-spin' />}
              <ShieldCheck className='me-1 size-4' />
              {isSubscription
                ? '保存并同步订阅'
                : isAuthTemplate
                  ? '提交 Token 授权并开通渠道'
                  : '创建渠道'}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

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
  ArrowLeft,
  CheckSquare,
  Copy,
  ExternalLink,
  Gift,
  KeyRound,
  Loader2,
  Network,
  Plus,
  RefreshCw,
  Rss,
  ShieldCheck,
} from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
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
  cancelDs2ApiOAuth,
  cancelWorkbuddyOAuth,
  createBinding,
  importTrae,
  pollDs2ApiOAuth,
  pollWorkbuddyOAuth,
  startDs2ApiOAuth,
  startWorkbuddyOAuth,
  type OAuthSession,
} from '@/features/account-bindings/api'
import { FreePiAuthorizationCard } from '@/features/account-bindings/freepi-authorization'
import { ProviderAuthorizationCard } from '@/features/account-bindings/provider-authorization'
import {BrowserAccountConnection,type WebAccountProvider} from '@/features/account-bindings/browser-account-connection'
import { createSubscription, listEndpoints } from '@/features/proxy-pool/api'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import type { ChannelProxyPolicy } from '../../api'
import {
  createChannelFromTemplate,
  getFreeTemplates,
  probeTemplateModels,
  type FreeSourceTemplate,
} from '../../free-sources'
import { channelsQueryKeys } from '../../lib'
import { isChannelProxySelectionComplete } from '../../lib/channel-proxy'
import { ChannelModelSelection } from '../channel-model-selection'
import { ChannelProxySelector } from '../channel-proxy-selector'

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
function classifyTemplate(
  tpl: FreeSourceTemplate
): 'api' | 'ip' | 'auth' | 'sub' {
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
  if (
    tpl.authMode === 'ip' ||
    tpl.keyRequired === false ||
    tpl.authType === 'none'
  ) {
    return 'ip'
  }
  return 'api'
}

export function FromTemplateDialog({
  open,
  onOpenChange,
}: FromTemplateDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [categoryTab, setCategoryTab] = useState<
    'all' | 'api' | 'ip' | 'auth' | 'sub'
  >('all')
  const [selected, setSelected] = useState<FreeSourceTemplate | null>(null)
  const [apiKey, setApiKey] = useState('')
  const [customName, setCustomName] = useState('')
  const [extraValues, setExtraValues] = useState<Record<string, string>>({})

  // 出口代理策略（独立分区）
  const [proxyPolicy, setProxyPolicy] = useState<ChannelProxyPolicy>({
    mode: 'user_pool',
  })
  const [proxySelectionTouched, setProxySelectionTouched] = useState(false)
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
  const [callable, setCallable] = useState(true)
  const [probedCount, setProbedCount] = useState<number | null>(null)
  const [probeError, setProbeError] = useState<unknown>(null)
  const modelRequest = useRef(0)

  const [visibility, setVisibility] = useState(false)
  const [multiKeys, setMultiKeys] = useState('')
  const [multiKeyMode, setMultiKeyMode] = useState<'random' | 'polling'>(
    'random'
  )
  const [isSubmitting, setIsSubmitting] = useState(false)

  // Token / OAuth 账号授权状态（内嵌在创建渠道弹窗内）
  const [authTokensInput, setAuthTokensInput] = useState('')
  const [traeUid, setTraeUid] = useState('')
  const [traeConfirmed, setTraeConfirmed] = useState(false)
  const wbRequest = useRef(0)
  const pendingWb = useRef<string | null>(null)
  const wbPopup = useRef<Window | null>(null)
  const [oauthRealm, setOauthRealm] = useState<'cn' | 'global'>('cn')
  const [wbSession, setWbSession] = useState<OAuthSession | null>(null)
  const [ds2apiSession, setDs2apiSession] = useState<OAuthSession | null>(null)
  const [oauthStatusText, setOauthStatusText] = useState('')
  const [authStarting, setAuthStarting] = useState(false)
  const authRequest = useRef(0)
  const pendingDs2 = useRef<string | null>(null)
  const authPopup = useRef<Window | null>(null)

  useEffect(() => {
    if (open) return
    authRequest.current++
    setAuthStarting(false)
    const state = pendingDs2.current
    pendingDs2.current = null
    setDs2apiSession(null)
    authPopup.current?.close()
    if (state) {
      void cancelDs2ApiOAuth(state).catch((error: unknown) =>
        handleServerError(error)
      )
    }
  }, [open])

  useEffect(
    () => () => {
      authRequest.current++
      authPopup.current?.close()
      if (pendingDs2.current) {
        void cancelDs2ApiOAuth(pendingDs2.current).catch(() => undefined)
      }
    },
    []
  )

  useEffect(() => {
    setTraeConfirmed(false)
    if (open && selected?.id === 'workbuddy') return
    wbRequest.current++
    setWbSession(null)
    setAuthStarting(false)
    const sessionId = pendingWb.current
    pendingWb.current = null
    wbPopup.current?.close()
    if (sessionId) {
      void cancelWorkbuddyOAuth(sessionId).catch((error: unknown) =>
        handleServerError(error)
      )
    }
  }, [open, selected?.id])

  useEffect(
    () => () => {
      wbRequest.current++
      wbPopup.current?.close()
      const sessionId = pendingWb.current
      pendingWb.current = null
      if (sessionId) void cancelWorkbuddyOAuth(sessionId).catch(() => undefined)
    },
    []
  )

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

  const endpoints = useMemo(
    () => endpointsQuery.data ?? [],
    [endpointsQuery.data]
  )
  const ownNodes = useMemo(
    () =>
      endpoints.filter((e) => e.is_owner && e.source !== 'haizhu' && e.enabled),
    [endpoints]
  )

  useEffect(() => {
    if (!open) return
    setSelected(null)
    setApiKey('')
    setCustomName('')
    setExtraValues({})
    setProxySelectionTouched(false)
    setProxyPolicy({ mode: 'user_pool' })
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
    setDs2apiSession(null)
    setIsSubmitting(false)
  }, [open])

  // Late discovery responses belong to their original template session only.
  useEffect(() => {
    const requests = modelRequest
    const requestId = ++requests.current
    setProbingModels(false)
    if (!open || !selected) return
    const list = [...new Set(selected.models ?? [])]
    setAvailableModels(list)
    setMyModels(list)
    setPublicModels([])
    setProbedCount(null)
    setProbeError(null)
    setCallable(!selected.id.startsWith('opencode-'))
    setProxySelectionTouched(false)
    setProxyPolicy({ mode: 'user_pool' })
    let active = true
    if (
      ['openrouter-free', 'opencode-free', 'opencode-zen'].includes(selected.id)
    ) {
      setProbingModels(true)
      void probeTemplateModels({ templateId: selected.id })
        .then((response) => {
          if (!active || requestId !== modelRequest.current) return
          requireServerSuccess(response)
          setCallable(response.data?.callable !== false)
          const fetched = response.data?.models ?? []
          setAvailableModels((previous) => [
            ...new Set([...previous, ...fetched]),
          ])
          setProbedCount(fetched.length)
        })
        .catch((error: unknown) => {
          if (active && requestId === modelRequest.current) {
            setProbeError(error)
          }
        })
        .finally(() => {
          if (active && requestId === modelRequest.current) {
            setProbingModels(false)
          }
        })
    }
    return () => {
      active = false
      requests.current++
    }
  }, [selected, open])

  // Poll sequentially and stop on expiry, cancellation, or a terminal error.
  useEffect(() => {
    if (!wbSession || !open) return
    let active = true
    let timer: ReturnType<typeof setTimeout>
    const expiresAt = Date.now() + (wbSession.expiresIn ?? 600) * 1000
    const poll = async () => {
      try {
        if (Date.now() >= expiresAt) {
          throw new Error(t('Authorization session expired'))
        }
        const res = await pollWorkbuddyOAuth(wbSession.sessionId)
        if (!active) return
        if (!res.done) {
          timer = setTimeout(() => {
            void poll()
          }, 2000)
          return
        }
        pendingWb.current = null
        setWbSession(null)
        wbPopup.current?.close()
        toast.success(t('Account login completed; channel created'))
        void queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.lists(),
        })
        onOpenChange(false)
      } catch (error) {
        if (!active) return
        const sessionId = pendingWb.current
        pendingWb.current = null
        setWbSession(null)
        wbPopup.current?.close()
        setOauthStatusText(
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
      active = false
      clearTimeout(timer)
    }
  }, [wbSession, open, queryClient, onOpenChange, t])

  // Sequential owner-scoped polling; errors and expiry are terminal.
  useEffect(() => {
    if (!ds2apiSession || !open) return
    let active = true
    let timer: ReturnType<typeof setTimeout>
    const expiresAt = Date.now() + (ds2apiSession.expiresIn ?? 300) * 1000
    const poll = async () => {
      try {
        if (Date.now() >= expiresAt) {
          throw new Error(t('Account login session not found or expired'))
        }
        const res = await pollDs2ApiOAuth(ds2apiSession.sessionId)
        if (!active) return
        if (!res.done) {
          timer = setTimeout(() => {
            void poll()
          }, 2500)
          return
        }
        pendingDs2.current = null
        setDs2apiSession(null)
        toast.success(
          t('Account login completed; channel #{{id}} created', {
            id: res.channel?.id,
          })
        )
        void queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.lists(),
        })
        onOpenChange(false)
      } catch (error) {
        if (!active) return
        const state = pendingDs2.current
        pendingDs2.current = null
        setDs2apiSession(null)
        setOauthStatusText(
          t(
            'Account login stopped. Start again after checking your dashboard session'
          )
        )
        if (state) void cancelDs2ApiOAuth(state).catch(() => undefined)
        handleServerError(error)
      }
    }
    timer = setTimeout(() => {
      void poll()
    }, 2500)
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [ds2apiSession, open, queryClient, onOpenChange, t])

  const templateKind = selected ? classifyTemplate(selected) : 'api'
  const isSubscription = templateKind === 'sub'
  const isAuthTemplate = templateKind === 'auth'
  const webAccountProvider = (() : WebAccountProvider | null => {
    if (selected?.id === 'xiaohuanxiong') return 'raccoon'
    if (['trae-cn','cline-account','qoder-cn','qoder-global','zcode-bigmodel','zcode-zai'].includes(selected?.id ?? '')) return selected?.id as WebAccountProvider
    return null
  })()
  const isZCodeTemplate = Boolean(webAccountProvider)
  const isIpFreeTemplate = templateKind === 'ip'
  const isApiKeyTemplate = templateKind === 'api'
  const templatePresentation = {
    api: {
      description: 'API 密钥免费层（必须填 API Key，不可仅凭 IP 绕过）',
      badge: '🔑 API 密钥免费层（必填 Key）',
      step: '填写上游 API Key',
      submit: '创建渠道',
      variant: 'outline',
    },
    ip: {
      description: 'IP 免费层（无需密钥，按出口 IP 与虚拟设备轮换扩容）',
      badge: '🌐 IP 免费层（免密钥）',
      step: '免密钥验证说明（IP 免费层）',
      submit: '创建渠道',
      variant: 'default',
    },
    auth: {
      description: 'Token / OAuth 账号授权（直接在此完成授权登录并选模型）',
      badge: '🛡️ Token / OAuth 账号授权',
      step: 'Token / OAuth 授权登录',
      submit: '提交 Token 授权并开通渠道',
      variant: 'secondary',
    },
    sub: {
      description: '机场订阅导入',
      badge: '✈️ 机场订阅',
      step: '填写机场订阅链接',
      submit: '保存并同步订阅',
      variant: 'outline',
    },
  } as const
  const presentation = templatePresentation[templateKind]
  const authorizationUrls: Record<string, string> = {
    'trae-cn': 'https://www.trae.cn',
    'cline-account': 'https://app.cline.bot',
    'qoder-cn': 'https://qoder.cn/account/integrations',
  }
  const tokenPlaceholders: Record<string, string> = {
    ds2api: t('Paste an existing token, or use the account login link above.'),
    'trae-cn': t(
      'Use your own refresh token or the official http://127.0.0.1:port/authorize callback.'
    ),
    'qoder-cn':
      '粘贴 Qoder CN 的 Personal Access Token (PAT) 或桌面端登录态 token（每行一个）',
    'cline-account':
      '粘贴 Cline WorkOS 浏览器授权后的回调链接或 access_token（每行一个）',
    'internlm-inkstone':
      '粘贴书生·端砚 Token Plan 的 sk-/ink- Key，或控制台 Cookie 中的 uaa-token',
  }

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
    if (
      !selected ||
      !callable ||
      [
        'freepi',
        'kimi-work',
        'xiaomi-mimo',
        'zcode-bigmodel',
        'zcode-zai',
      ].includes(selected.id)
    ) {
      return false
    }
    if (
      !isSubscription &&
      !isChannelProxySelectionComplete(proxyPolicy, proxyUrl)
    ) {
      return false
    }
    if (isSubscription) return subscriptionOk
    if (isAuthTemplate) {
      if (selected.id === 'magpie') return true
      if (selected.id === 'trae-cn' && !traeConfirmed) {
        return false
      }
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
    callable,
    isSubscription,
    subscriptionOk,
    isAuthTemplate,
    authTokensInput,
    traeConfirmed,
    isApiKeyTemplate,
    hasAnyKey,
    extrasComplete,
    myModels.length,
    proxyPolicy,
    proxyUrl,
  ])

  const resolveOutboundProxy = (): string => {
    if (proxyPolicy.mode === 'direct') return 'direct'
    if (proxyPolicy.mode === 'fixed') return proxyUrl.trim()
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
      toast.success(
        `订阅已同步：解析出 ${res.data?.node_count ?? 0} 个可用节点（默认仅自己可见可用）`
      )
      setInlineSubUrl('')
      setInlineSubName('')
      setProxySelectionTouched(false)
      setProxyPolicy({ mode: 'user_pool' })
      await endpointsQuery.refetch()
      void queryClient.invalidateQueries({
        queryKey: ['proxy-pool', 'subscriptions'],
      })
    } catch (error) {
      handleServerError(error, '同步订阅失败')
    } finally {
      setAddingInlineSub(false)
    }
  }

  const handleProbeModels = async () => {
    if (!selected || probingModels) return
    const requestId = ++modelRequest.current
    setProbingModels(true)
    setProbedCount(null)
    setProbeError(null)
    try {
      const extra = extraFields.length
        ? Object.fromEntries(
            extraFields.map((field) => [field, extraValue(field)])
          )
        : undefined
      const response = await probeTemplateModels({
        templateId: selected.id,
        key: apiKey.trim() || parsedKeys[0] || '',
        proxy: resolveOutboundProxy(),
        proxy_policy: proxyPolicy,
        extra,
      })
      if (requestId !== modelRequest.current) return
      requireServerSuccess(response)
      setCallable(response.data?.callable !== false)
      const fetched = response.data?.models ?? []
      setAvailableModels((previous) => [...new Set([...previous, ...fetched])])
      setProbedCount(fetched.length)
    } catch (error) {
      if (requestId === modelRequest.current) {
        handleServerError(error, t('Failed to fetch upstream models'))
      }
    } finally {
      if (requestId === modelRequest.current) setProbingModels(false)
    }
  }

  const startWorkBuddyFlow = async () => {
    if (authStarting) return
    const request = ++wbRequest.current
    wbPopup.current?.close()
    const popup = window.open('about:blank', '_blank')
    if (popup) popup.opener = null
    wbPopup.current = popup
    setAuthStarting(true)
    setOauthStatusText(t('Creating authorization session...'))
    try {
      const previous = pendingWb.current
      pendingWb.current = null
      setWbSession(null)
      if (previous) await cancelWorkbuddyOAuth(previous)
      if (request !== wbRequest.current) return
      if (!popup || popup.closed) {
        throw new Error(
          t(
            'Your browser blocked the login window. Allow popups and try again.'
          )
        )
      }
      const sess = await startWorkbuddyOAuth(oauthRealm, {
        proxy: proxySelectionTouched ? resolveOutboundProxy() : undefined,
        proxy_policy:
          proxySelectionTouched || oauthRealm !== 'cn'
            ? proxyPolicy
            : undefined,
        displayName: customName.trim() || undefined,
        public: visibility,
      })
      if (!sess) throw new Error(t('Failed to start authorization'))
      if (request !== wbRequest.current || popup.closed) {
        popup.close()
        await cancelWorkbuddyOAuth(sess.sessionId)
        return
      }
      pendingWb.current = sess.sessionId
      setWbSession(sess)
      popup.location.href = sess.authUrl
      setOauthStatusText(
        t(
          'Finish signing in in the authorization window. This page refreshes automatically.'
        )
      )
    } catch (error) {
      popup?.close()
      if (request === wbRequest.current) {
        const sessionId = pendingWb.current
        pendingWb.current = null
        setWbSession(null)
        if (sessionId) {
          void cancelWorkbuddyOAuth(sessionId).catch(() => undefined)
        }
        setOauthStatusText(t('Failed to start authorization'))
        handleServerError(error)
      }
    } finally {
      if (request === wbRequest.current) setAuthStarting(false)
    }
  }

  const cancelWorkBuddyFlow = async () => {
    wbRequest.current++
    setAuthStarting(false)
    const sessionId = pendingWb.current
    pendingWb.current = null
    setWbSession(null)
    wbPopup.current?.close()
    setOauthStatusText(t('Account login was cancelled. Start again'))
    if (sessionId) {
      try {
        await cancelWorkbuddyOAuth(sessionId)
      } catch (error) {
        handleServerError(error)
      }
    }
  }

  const startDs2ApiFlow = async () => {
    if (authStarting) return
    const request = ++authRequest.current
    // Reserve on the user gesture, before awaiting the network.
    const popup = window.open('about:blank', '_blank')
    if (popup) popup.opener = null
    authPopup.current = popup
    setAuthStarting(true)
    setOauthStatusText(t('Creating an account login link...'))
    try {
      if (pendingDs2.current) await cancelDs2ApiOAuth(pendingDs2.current)
      pendingDs2.current = null
      const sess = await startDs2ApiOAuth({
        proxy: resolveOutboundProxy(),
        proxy_policy: proxyPolicy,
        models: myModels,
        all_models: availableModels,
        public_models: publicModels,
      })
      if (!sess) throw new Error(t('Unable to create account login session'))
      if (request !== authRequest.current) {
        popup?.close()
        await cancelDs2ApiOAuth(sess.sessionId)
        return
      }
      pendingDs2.current = sess.sessionId
      setDs2apiSession(sess)
      if (popup && !popup.closed) {
        popup.location.href = sess.authUrl
        setOauthStatusText(
          t(
            'Open the account login page and sign in with your own account. The channel is created after login.'
          )
        )
      } else {
        setOauthStatusText(
          t(
            'Your browser blocked the login window. Use Open account login page to continue.'
          )
        )
      }
    } catch (error) {
      popup?.close()
      if (request === authRequest.current) {
        setDs2apiSession(null)
        setOauthStatusText(t('Unable to create account login session'))
        handleServerError(error)
      }
    } finally {
      if (request === authRequest.current) setAuthStarting(false)
    }
  }

  const cancelDs2Flow = async () => {
    if (!ds2apiSession) return
    const state = ds2apiSession.sessionId
    try {
      await cancelDs2ApiOAuth(state)
      pendingDs2.current = null
      setDs2apiSession(null)
      authPopup.current?.close()
      setOauthStatusText(t('Account login was cancelled. Start again'))
    } catch (error) {
      handleServerError(error)
    }
  }

  const handleSubmit = async () => {
    if (!selected || !canSubmit) return
    setIsSubmitting(true)
    try {
      // 1) Token / OAuth 账号授权类模板：直接在弹窗内完成授权建渠道
      if (isAuthTemplate) {
        const raw = authTokensInput.trim()
        if (selected.id === 'trae-cn') {
          if (!traeConfirmed) return
          const looksUrl =
            /^https?:/i.test(raw) ||
            raw.includes('://') ||
            raw.includes('code=')
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
              setOauthStatusText(
                t(
                  'Use your own refresh token or the official http://127.0.0.1:port/authorize callback.'
                )
              )
              return
            }
          }
          const res = await importTrae({
            ...(looksUrl ? { callbackUrl: raw } : { refreshToken: raw }),
            uid: traeUid.trim() || undefined,
            confirmRotation: traeConfirmed,
            proxy: proxySelectionTouched ? resolveOutboundProxy() : undefined,
            proxy_policy:
              proxySelectionTouched || selected.id !== 'trae-cn'
                ? proxyPolicy
                : undefined,
            displayName: customName.trim() || undefined,
            public: visibility,
          })
          if (res.ok) {
            toast.success(res.notice || 'Trae 授权导入成功，渠道已开通')
            void queryClient.invalidateQueries({
              queryKey: channelsQueryKeys.lists(),
            })
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
              all_models: availableModels,
              proxy_policy: proxyPolicy,
              public_models: publicModels,
            })
            if (directRes.success) {
              toast.success(`${selected.name} Token 授权渠道已创建`)
              void queryClient.invalidateQueries({
                queryKey: channelsQueryKeys.lists(),
              })
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
          proxy_policy: proxyPolicy,
        })
        if (bindRes.ok) {
          toast.success(
            'Token 授权已提交，已为每个账号分配独立虚拟设备指纹并开通渠道'
          )
          void queryClient.invalidateQueries({
            queryKey: channelsQueryKeys.lists(),
          })
          onOpenChange(false)
        } else {
          toast.error(bindRes.message || '提交授权失败')
        }
        return
      }

      // 2) API 密钥免费层 / IP 免费层 / 机场订阅模板
      const extra = extraFields.length
        ? Object.fromEntries(
            extraFields.map((field) => [field, extraValue(field)])
          )
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
        all_models: availableModels,
        proxy_policy: proxyPolicy,
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
                publicModels.length > 0
                  ? `，公开到公共池 ${publicModels.length} 个模型`
                  : ''
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
    return templates.filter(
      (tpl) =>
        !tpl.id.startsWith('opencode-') &&
        tpl.id !== 'trae-intl' &&
        (categoryTab === 'all' || classifyTemplate(tpl) === categoryTab)
    )
  }, [templates, categoryTab])

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) modelRequest.current++
        onOpenChange(next)
      }}
    >
      <DialogContent className='flex max-h-[88vh] flex-col sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <Gift className='size-4' />
            添加免费源 / 授权创建渠道
          </DialogTitle>
          <DialogDescription>
            {selected
              ? `${selected.name} — ${presentation.description}`
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
              ) : null}
              {!isLoading && filteredTemplates.length === 0 && (
                <div className='text-muted-foreground rounded-lg border border-dashed p-8 text-center text-sm'>
                  暂无匹配的免费源模板
                </div>
              )}
              {!isLoading && filteredTemplates.length > 0 && (
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
                            variant={templatePresentation[kind].variant}
                            className='shrink-0 text-[10px]'
                          >
                            {
                              {
                                ip: 'IP免费层·免Key',
                                auth: 'Token/OAuth授权',
                                sub: '机场订阅',
                                api: '需API密钥',
                              }[kind]
                            }
                          </Badge>
                        </div>
                        {template.freeNote && (
                          <div className='text-muted-foreground mt-1 line-clamp-2 text-xs'>
                            {template.freeNote}
                          </div>
                        )}
                        <div className='text-muted-foreground mt-1.5 flex items-center justify-between text-xs'>
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
                <Badge variant={presentation.variant}>
                  {presentation.badge}
                </Badge>
              </div>
              {selected.freeNote && (
                <div className='text-muted-foreground mt-1 text-xs whitespace-pre-line'>
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
            <div className='border-border/80 bg-card space-y-3 rounded-xl border p-3.5'>
              <div className='flex items-center gap-2 text-xs font-semibold'>
                <KeyRound className='text-primary size-3.5' />
                <span>
                  第一步：
                  {presentation.step}
                </span>
              </div>

              {isSubscription && (
                <div className='space-y-2'>
                  <Input
                    value={subscriptionUrl}
                    onChange={(e) => setSubscriptionUrl(e.target.value)}
                    placeholder='https://example.com/api/v1/client/subscribe?token=...'
                    autoComplete='off'
                  />
                  <p className='text-muted-foreground text-xs'>
                    自动解析 AnyTLS / VLESS / VMess / Trojan / SS
                    节点并过滤流量公告假节点，注入共享 mihomo 内核。
                  </p>
                </div>
              )}
              {selected.id === 'freepi' && (
                <FreePiAuthorizationCard
                  proxy={resolveOutboundProxy()}
                  proxyPolicy={proxyPolicy}
                  onComplete={() => {
                    void queryClient.invalidateQueries({
                      queryKey: channelsQueryKeys.all,
                    })
                    onOpenChange(false)
                  }}
                />
              )}
              {webAccountProvider && (
                <BrowserAccountConnection key={webAccountProvider} provider={webAccountProvider} options={{displayName:customName.trim()||undefined,public:visibility,proxy:proxySelectionTouched?resolveOutboundProxy():undefined,proxyPolicy}} disabled={isSubmitting || !isChannelProxySelectionComplete(proxyPolicy,proxyUrl)} onComplete={()=>{void queryClient.invalidateQueries({queryKey:channelsQueryKeys.all});onOpenChange(false)}}/>
              )}
              {(['kimi-work','xiaomi-mimo'] as const).map((provider)=> selected.id===provider && <ProviderAuthorizationCard key={provider} provider={provider} options={{displayName:customName.trim()||undefined,public:visibility,proxy:proxySelectionTouched?resolveOutboundProxy():undefined,proxyPolicy}} disabled={isSubmitting||!isChannelProxySelectionComplete(proxyPolicy,proxyUrl)} onComplete={()=>{void queryClient.invalidateQueries({queryKey:channelsQueryKeys.all});onOpenChange(false)}}/>)}
              {isAuthTemplate && !webAccountProvider &&
                ![
                  'freepi',
                  'kimi-work',
                  'xiaomi-mimo',
                  'zcode-bigmodel',
                  'zcode-zai',
                ].includes(selected.id) && (
                  <div className='space-y-3'>
                    {/* WorkBuddy 一键设备授权 */}
                    {selected.id === 'workbuddy' && (
                      <div className='bg-muted/50 flex flex-wrap items-center gap-2 rounded-lg p-2.5 text-xs'>
                        <select
                          className='bg-background h-8 rounded-md border px-2 text-xs'
                          value={oauthRealm}
                          onChange={(e) =>
                            setOauthRealm(e.target.value as 'cn' | 'global')
                          }
                        >
                          <option value='cn'>
                            WorkBuddy 国内版（支持签到）
                          </option>
                          <option value='global'>WorkBuddy 国际版</option>
                        </select>
                        <Button
                          size='sm'
                          type='button'
                          disabled={
                            authStarting ||
                            !isChannelProxySelectionComplete(
                              proxyPolicy,
                              proxyUrl
                            )
                          }
                          onClick={() => void startWorkBuddyFlow()}
                        >
                          {t(
                            authStarting
                              ? 'Creating authorization session...'
                              : 'Start WorkBuddy authorization'
                          )}
                        </Button>
                        {(wbSession || authStarting) && (
                          <Button
                            type='button'
                            size='sm'
                            variant='outline'
                            onClick={() => void cancelWorkBuddyFlow()}
                          >
                            {t('Cancel authorization')}
                          </Button>
                        )}
                        {oauthStatusText && (
                          <span role='status' className='text-primary'>
                            {oauthStatusText}
                          </span>
                        )}
                      </div>
                    )}

                    {selected.id === 'xiaohuanxiong' && (
                      <div
                        role='status'
                        className='bg-muted/50 space-y-2 rounded-lg p-2.5 text-xs'
                      >
                        <p>
                          {t(
                            'Raccoon browser authorization only supports an official local client. Gateway callbacks are not supported'
                          )}
                        </p>
                        <p>
                          {t(
                            'Use credentials exported by your own official Raccoon client below. A website login URL or authorization code is not a usable API credential.'
                          )}
                        </p>
                        <a
                          href='https://xiaohuanxiong.com'
                          target='_blank'
                          rel='noopener noreferrer'
                          className='text-primary underline'
                        >
                          {t('Open official Raccoon client')}
                        </a>
                      </div>
                    )}

                    {/* DS2API 一键生成授权链接 → 复制去浏览器登录 → 自动返回开通渠道 */}
                    {selected.id === 'ds2api' && (
                      <div className='border-primary/30 bg-primary/5 space-y-2 rounded-lg border p-3 text-xs'>
                        <div className='flex flex-wrap items-center justify-between gap-2'>
                          <span className='text-foreground font-medium'>
                            {t(
                              'Create an account login link and sign in with your own DeepSeek account. This is not browser OAuth.'
                            )}
                          </span>
                          <div className='flex flex-wrap gap-1.5'>
                            <Button
                              size='sm'
                              type='button'
                              className='h-8 text-xs'
                              onClick={() => void startDs2ApiFlow()}
                              disabled={authStarting}
                            >
                              {ds2apiSession
                                ? t('Regenerate account login link')
                                : t('Create account login link')}
                            </Button>
                            {ds2apiSession && (
                              <>
                                <Button
                                  size='sm'
                                  variant='outline'
                                  type='button'
                                  onClick={() => void cancelDs2Flow()}
                                >
                                  {t('Cancel authorization')}
                                </Button>
                                <Button
                                  size='sm'
                                  variant='secondary'
                                  type='button'
                                  className='h-8 text-xs'
                                  onClick={() => {
                                    void navigator.clipboard
                                      ?.writeText(ds2apiSession.authUrl)
                                      .then(() =>
                                        toast.success('授权链接已复制')
                                      )
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
                                    window.open(
                                      ds2apiSession.authUrl,
                                      '_blank',
                                      'noopener,noreferrer'
                                    )
                                  }
                                >
                                  <ExternalLink className='me-1 size-3.5' />
                                  {t('Open account login page')}
                                </Button>
                              </>
                            )}
                          </div>
                        </div>
                        {ds2apiSession && (
                          <Input
                            readOnly
                            value={ds2apiSession.authUrl}
                            onClick={(e) =>
                              (e.target as HTMLInputElement).select()
                            }
                            className='h-8 font-mono text-xs'
                          />
                        )}
                        {oauthStatusText && (
                          <div className='text-primary flex items-center gap-1.5'>
                            <Loader2 className='size-3.5 animate-spin' />
                            <span>{oauthStatusText}</span>
                          </div>
                        )}
                      </div>
                    )}

                    {/* Trae / Cline / Qoder CN / 书生·端砚 快捷跳转授权按钮 */}
                    {(selected.id === 'cline-account' ||
                      selected.id === 'qoder-cn' ||
                      selected.id === 'internlm-inkstone') && (
                      <div className='bg-muted/50 flex flex-wrap items-center gap-2 rounded-lg p-2.5 text-xs'>
                        <Button
                          size='sm'
                          variant='outline'
                          type='button'
                          onClick={() =>
                            window.open(
                              selected.signupUrl ||
                                authorizationUrls[selected.id] ||
                                'https://discovery.intern-ai.org.cn/',
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
                      aria-label={
                        selected.id === 'trae-cn'
                          ? t('Trae callback URL or refresh token')
                          : undefined
                      }
                      value={authTokensInput}
                      onChange={(e) => setAuthTokensInput(e.target.value)}
                      rows={4}
                      placeholder={
                        tokenPlaceholders[selected.id] ??
                        '粘贴授权 Token（每行一个）'
                      }
                      className='border-input bg-background w-full rounded-md border p-2.5 font-mono text-xs'
                    />

                    {selected.id === 'trae-cn' && (
                      <div className='space-y-2'>
                        <Input
                          value={traeUid}
                          onChange={(e) => setTraeUid(e.target.value)}
                          placeholder='Trae UID（可选，用回调链接导入时自动识别）'
                          className='h-8 text-xs'
                        />
                        <Label className='flex items-start gap-2 text-xs'>
                          <Checkbox
                            checked={traeConfirmed}
                            disabled={isSubmitting}
                            onCheckedChange={(checked) =>
                              setTraeConfirmed(checked === true)
                            }
                          />
                          {t(
                            'I understand that importing Trae credentials rotates tokens and may sign out other clients.'
                          )}
                        </Label>
                        {oauthStatusText && (
                          <p
                            role='status'
                            className='text-muted-foreground text-xs'
                          >
                            {oauthStatusText}
                          </p>
                        )}
                      </div>
                    )}
                  </div>
                )}
              {isIpFreeTemplate && (
                <div className='border-primary/30 bg-primary/5 space-y-1 rounded-lg border p-3 text-xs'>
                  <div className='text-foreground font-medium'>
                    ✅ 该来源属于「IP 免费层」，无需填写 API Key！
                  </div>
                  <div className='text-muted-foreground'>
                    直接通过下方绑定的出口 IP 池（机场订阅或 Haizhu 7114
                    公共免费节点池）与虚拟设备指纹轮换调用，遇限流或报错自动熔断切换下一个
                    IP。
                  </div>
                </div>
              )}
              {isApiKeyTemplate && (
                /* API 密钥免费层：强制要求 API Key，不提供免 Key 绑 IP 绕过 */
                <div className='space-y-3'>
                  <div className='space-y-1.5'>
                    <Label htmlFor='from-template-key' className='text-xs'>
                      上游 API Key{' '}
                      <span className='text-destructive'>
                        *（API 免费层必填，不可仅绑 IP 空手创建）
                      </span>
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
                  <Label
                    htmlFor={`from-template-extra-${field}`}
                    className='text-xs'
                  >
                    {field} <span className='text-destructive'>*</span>
                  </Label>
                  <Input
                    id={`from-template-extra-${field}`}
                    value={
                      extraValues[field] ?? selected.defaultExtra?.[field] ?? ''
                    }
                    onChange={(e) =>
                      setExtraValues((prev) => ({
                        ...prev,
                        [field]: e.target.value,
                      }))
                    }
                    placeholder={
                      selected.defaultExtra?.[field] ?? `填写 ${field}`
                    }
                    autoComplete='off'
                  />
                </div>
              ))}
            </div>

            {/* 区块二：独立分离的「出口代理 & 机场订阅 & 节点选择」区 */}
            {!isSubscription && (
              <div className='border-border/80 bg-card space-y-3 rounded-xl border p-3.5'>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <div className='flex items-center gap-2 text-xs font-semibold'>
                    <Network className='text-primary size-3.5' />
                    <span>
                      第二步：出口代理与机场订阅节点选择（独立配置，与上方凭据分离）
                    </span>
                  </div>
                  {proxyPolicy.mode !== 'public_pool' &&
                    ownNodes.length > 0 && (
                      <Badge
                        variant='secondary'
                        className='font-mono text-[11px]'
                      >
                        {t(
                          'Your proxy endpoints: {{count}}. Token quota is not measured.',
                          { count: ownNodes.length }
                        )}
                      </Badge>
                    )}
                </div>

                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Platform IP-based free models are available to everyone through Public models. Your sources stay private unless you choose to share them.'
                  )}
                </p>
                {!proxySelectionTouched &&
                  (selected?.id === 'workbuddy' ||
                    selected?.id === 'trae-cn') && (
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'Domestic account login uses the server default when no proxy is selected. Choosing your own proxy applies it strictly.'
                      )}
                    </p>
                  )}
                <ChannelProxySelector
                  enabled={open}
                  policy={proxyPolicy}
                  proxy={proxyUrl}
                  disabled={isSubmitting}
                  onPolicyChange={(policy) => {
                    setProxySelectionTouched(true)
                    setProxyPolicy(policy)
                  }}
                  onProxyChange={(proxy) => {
                    setProxySelectionTouched(true)
                    setProxyUrl(proxy)
                  }}
                />

                {/* 在创建渠道弹窗内直接添加机场订阅（Item 2.3 & 2.5） */}
                <div className='space-y-2 rounded-lg border border-dashed p-2.5'>
                  <div className='flex items-center justify-between text-xs'>
                    <span className='text-muted-foreground flex items-center gap-1.5 font-medium'>
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

            {isZCodeTemplate && (
              <div className='space-y-3 rounded-xl border p-3.5'>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Account models are discovered securely after login. Edit the channel to select models after connection.'
                  )}
                </p>
                <Label className='flex items-start gap-2'>
                  <Checkbox
                    checked={visibility}
                    disabled={isSubmitting}
                    onCheckedChange={(checked) =>
                      setVisibility(checked === true)
                    }
                  />
                  {t(
                    'I explicitly allow other users to use this channel and spend my account credits'
                  )}
                </Label>
                <Label htmlFor='zcode-channel-name'>{t('Channel name')}</Label>
                <Input
                  id='zcode-channel-name'
                  value={customName}
                  onChange={(event) => setCustomName(event.target.value)}
                  placeholder={selected.name}
                />
              </div>
            )}
            {/* 区块三：获取模型 + 可选择性勾选加入「我的渠道」与「公共池」（Items 2.3, 2.5） */}
            {!isSubscription && !isZCodeTemplate && (
              <div className='border-border/80 bg-card space-y-3 rounded-xl border p-3.5'>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <div className='flex items-center gap-2 text-xs font-semibold'>
                    <CheckSquare className='text-primary size-3.5' />
                    <span>
                      第三步：获取模型并选择加入「我的渠道」或「公共池」
                    </span>
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

                {!callable && (
                  <p role='alert' className='text-destructive text-xs'>
                    {t(
                      'OpenCode free models are only callable from the OpenCode client, not through this gateway.'
                    )}
                  </p>
                )}
                {probedCount !== null && (
                  <p className='text-muted-foreground text-xs'>
                    {t('Fetched {{count}} upstream models', {
                      count: probedCount,
                    })}
                  </p>
                )}
                {probeError != null && (
                  <p role='alert' className='text-destructive text-xs'>
                    {getServerErrorMessage(
                      probeError,
                      t('Failed to fetch upstream models')
                    )}
                  </p>
                )}

                <ChannelModelSelection
                  key={selected.id}
                  models={availableModels}
                  enabledModels={myModels}
                  publicModels={publicModels}
                  disabled={isSubmitting}
                  onChange={(enabled, published) => {
                    setMyModels(enabled)
                    setPublicModels(published)
                  }}
                />

                <div className='grid grid-cols-1 gap-2 pt-1 sm:grid-cols-2'>
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
                      <Label className='text-xs'>
                        入口 IP 白名单（可选，空=不限）
                      </Label>
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
          {selected !== null && !isZCodeTemplate && (
            <Button
              onClick={() => void handleSubmit()}
              disabled={!canSubmit || isSubmitting}
            >
              {isSubmitting && <Loader2 className='size-4 animate-spin' />}
              <ShieldCheck className='me-1 size-4' />
              {presentation.submit}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

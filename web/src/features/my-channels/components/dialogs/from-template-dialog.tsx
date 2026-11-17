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
import { ArrowLeft, ExternalLink, Gift, Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

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
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import {
  createChannelFromTemplate,
  getFreeTemplates,
  type FreeSourceTemplate,
} from '../../free-sources'
import { channelsQueryKeys } from '../../lib'

type FromTemplateDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const TEMPLATE_SKELETON_IDS = ['tpl-sk-1', 'tpl-sk-2', 'tpl-sk-3', 'tpl-sk-4']

/** ToS marker → status dot color. */
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

function tosLabelKey(tos?: string): string {
  switch (tos) {
    case 'ok':
      return 'ToS: friendly'
    case 'caution':
      return 'ToS: review recommended'
    case 'avoid':
      return 'ToS: risky'
    default:
      return 'ToS: unknown'
  }
}

export function FromTemplateDialog({
  open,
  onOpenChange,
}: FromTemplateDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState<FreeSourceTemplate | null>(null)
  const [apiKey, setApiKey] = useState('')
  const [customName, setCustomName] = useState('')
  const [extraValues, setExtraValues] = useState<Record<string, string>>({})
  const [proxyMode, setProxyMode] = useState<'pool' | 'direct' | 'custom'>('pool')
  const [proxyUrl, setProxyUrl] = useState('')
  const [tier, setTier] = useState<'free' | 'api'>('api')
  const [visibility, setVisibility] = useState(false)
  const [multiKeys, setMultiKeys] = useState('')
  const [multiKeyMode, setMultiKeyMode] = useState<'random' | 'polling'>('random')
  const [allowIps, setAllowIps] = useState('')
  const [subscriptionUrl, setSubscriptionUrl] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)

  const { data: templates, isLoading } = useQuery({
    queryKey: [...channelsQueryKeys.all, 'free-templates'],
    queryFn: getFreeTemplates,
    enabled: open,
    staleTime: 5 * 60_000,
  })

  // Reset the wizard whenever the dialog is reopened.
  useEffect(() => {
    if (!open) return
    setSelected(null)
    setApiKey('')
    setCustomName('')
    setExtraValues({})
    setProxyMode('pool')
    setProxyUrl('')
    setTier('api')
    setVisibility(false)
    setMultiKeys('')
    setMultiKeyMode('random')
    setAllowIps('')
    setSubscriptionUrl('')
    setIsSubmitting(false)
  }, [open])

  // 账号授权类来源（账号密码 / 浏览器授权 / 配套网关 / 登录态反代）不在本弹窗里要 API Key，
  // 统一走「上游账号授权」卡片提交账号，由平台代建实例后自动建渠道。
  const needsAccountAuth =
    selected !== null &&
    selected.authMode !== 'key' &&
    selected.authMode !== 'ip'
  // 接入形态三态：none=无需密钥（IP 白名单/账号授权/配套网关）、
  // key=填上游 API Key、subscription=填机场订阅链接（节点与额度由平台解析）。
  const authType = selected?.authType ?? 'key'
  const isSubscription = !needsAccountAuth && authType === 'subscription'
  const isNoKey = !needsAccountAuth && authType === 'none'
  const keyRequired =
    selected?.keyRequired !== false &&
    !needsAccountAuth &&
    !isSubscription &&
    !isNoKey
  const extraFields = needsAccountAuth ? [] : (selected?.extraFields ?? [])
  // 用户没填时回落到模板默认值（如 ds2api 平台已部署的网关地址）
  const extraValue = (field: string) =>
    (extraValues[field] ?? selected?.defaultExtra?.[field] ?? '').trim()
  const extrasComplete = extraFields.every(
    (field) => extraValue(field) !== ''
  )
  // 多密钥：换行或逗号分隔，填了就优先于单密钥输入框。
  const parsedKeys = multiKeys
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean)
  const hasAnyKey = apiKey.trim() !== '' || parsedKeys.length > 0
  const subscriptionOk =
    !isSubscription ||
    /^https?:\/\/\S+$/i.test(subscriptionUrl.trim())
  const canSubmit =
    selected !== null &&
    !needsAccountAuth &&
    (tier === 'free' || isSubscription || isNoKey || !keyRequired || hasAnyKey) &&
    extrasComplete &&
    subscriptionOk

  const handleSubmit = async () => {
    if (!selected || !canSubmit) return
    setIsSubmitting(true)
    try {
      const extra = extraFields.length
        ? Object.fromEntries(extraFields.map((field) => [field, extraValue(field)]))
        : undefined
      const proxy =
        proxyMode === 'direct'
          ? 'direct'
          : proxyMode === 'custom'
            ? proxyUrl.trim()
            : ''
      const result = await createChannelFromTemplate({
        templateId: selected.id,
        key: apiKey.trim(),
        ...(customName.trim() ? { name: customName.trim() } : {}),
        ...(extra ? { extra } : {}),
        ...(proxy ? { proxy } : {}),
        visibility: visibility ? 1 : 0,
        tier,
        ...(parsedKeys.length > 0
          ? { keys: parsedKeys, multi_key_mode: multiKeyMode }
          : {}),
        ...(tier === 'free' && allowIps.trim()
          ? { allow_ips: allowIps.trim() }
          : {}),
        ...(isSubscription
          ? { subscription_url: subscriptionUrl.trim() }
          : {}),
      })
      if (result.success) {
        // 订阅型来源不建渠道，只是把节点登记进出口池，提示要区分开。
        toast.success(
          isSubscription
            ? t('Subscription saved to your egress pool')
            : t('Channel created from free source')
        )
        queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.lists(),
        })
        queryClient.invalidateQueries({
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

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[85vh] flex-col sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <Gift className='size-4' />
            {t('Add from Free Source')}
          </DialogTitle>
          <DialogDescription>
            {selected
              ? needsAccountAuth
                ? t(
                    '{{name}} is an account-authorized source — submit it in the account authorization card instead of entering an API key.',
                    { name: selected.name }
                  )
                : t('Enter the API key for {{name}}.', { name: selected.name })
              : t(
                  'Pick a free provider template. Models and endpoint are prefilled.'
                )}
          </DialogDescription>
        </DialogHeader>

        {selected === null ? (
          <div className='min-h-0 flex-1 overflow-y-auto pr-1'>
            {(() => {
              if (isLoading) {
                return (
                  <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                    {TEMPLATE_SKELETON_IDS.map((id) => (
                      <Skeleton key={id} className='h-24 rounded-lg' />
                    ))}
                  </div>
                )
              }
              if (!templates || templates.length === 0) {
                return (
                  <div className='text-muted-foreground rounded-lg border border-dashed p-8 text-center text-sm'>
                    {t(
                      'No free source templates available yet. Check back after the backend ships them.'
                    )}
                  </div>
                )
              }
              return null
            })()}
            {!isLoading && templates && templates.length > 0 && (
              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                {templates.map((template) => (
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
                      <span
                        className='flex shrink-0 items-center gap-1.5'
                        title={t(tosLabelKey(template.tos))}
                      >
                        <span
                          className={cn(
                            'size-2 rounded-full',
                            tosDotClass(template.tos)
                          )}
                        />
                        <span className='text-muted-foreground text-xs'>
                          {t(tosLabelKey(template.tos))}
                        </span>
                      </span>
                    </div>
                    {template.freeNote && (
                      <div className='text-muted-foreground mt-1 text-xs'>
                        {template.freeNote}
                      </div>
                    )}
                    {template.models && template.models.length > 0 && (
                      <div className='text-muted-foreground mt-1 text-xs'>
                        {t('{{count}} models', {
                          count: template.models.length,
                        })}
                      </div>
                    )}
                  </button>
                ))}
              </div>
            )}
          </div>
        ) : (
          <div className='flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto pr-1'>
            <div className='border-border/60 rounded-lg border p-3'>
              <div className='flex items-center justify-between gap-2'>
                <span className='text-sm font-medium'>{selected.name}</span>
                <span
                  className='flex shrink-0 items-center gap-1.5'
                  title={t(tosLabelKey(selected.tos))}
                >
                  <span
                    className={cn(
                      'size-2 rounded-full',
                      tosDotClass(selected.tos)
                    )}
                  />
                  <span className='text-muted-foreground text-xs'>
                    {t(tosLabelKey(selected.tos))}
                  </span>
                </span>
              </div>
              {selected.freeNote && (
                <div className='text-muted-foreground mt-1 text-xs'>
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
                  {t('Get an API key')}
                  <ExternalLink className='size-3' />
                </a>
              )}
            </div>

            {needsAccountAuth && (
              <div className='rounded-md border border-dashed p-3 text-sm'>
                <div className='font-medium'>
                  这是账号授权来源，不需要 API Key
                </div>
                <div className='text-muted-foreground mt-1 text-xs'>
                  请到本页「上游账号授权」卡片里选「{selected.name}
                  」，填「账号----密码」（登录态来源填
                  access_token----refresh_token，配套网关填
                  供应商----密钥），需要固定出口时在那里选代理。平台会为你单独开通实例并自动建好渠道，全程不显示部署地址。
                </div>
              </div>
            )}
            {!needsAccountAuth && (
              <>
            {isSubscription ? (
              <div className='flex flex-col gap-2'>
                <Label htmlFor='from-template-subscription'>
                  {t('Airport subscription link')}
                  <span className='text-destructive'> *</span>
                </Label>
                <Input
                  id='from-template-subscription'
                  value={subscriptionUrl}
                  onChange={(e) => setSubscriptionUrl(e.target.value)}
                  placeholder={t('Paste the subscription URL (http/https)')}
                  autoComplete='off'
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'The platform parses node count and remaining traffic, adds those nodes to your egress pool, then routes this source through them. No API key needed.'
                  )}
                </p>
              </div>
            ) : isNoKey ? (
              <div className='rounded-md border border-dashed p-3 text-sm'>
                <div className='font-medium'>{t('No key needed for this source')}</div>
                <div className='text-muted-foreground mt-1 text-xs'>
                  {t(
                    'This source is authorized by IP or an account login; nothing secret is stored here.'
                  )}
                </div>
              </div>
            ) : tier === 'free' ? (
              <div className='rounded-md border border-dashed p-3 text-sm'>
                <div className='font-medium'>免费层（IP 白名单）不需要 API Key</div>
                <div className='text-muted-foreground mt-1 text-xs'>
                  填好下面的允许 IP 即可调用；平台按出口 IP 放行，不保存也不展示任何密钥。
                </div>
              </div>
            ) : (
            <div className='flex flex-col gap-2'>
              <Label htmlFor='from-template-key'>
                {t('API Key')}
                {keyRequired && <span className='text-destructive'> *</span>}
              </Label>
              <Input
                id='from-template-key'
                type='password'
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder={t('Paste the API key for this provider')}
                autoComplete='off'
              />
            </div>
            )}

            {/* Extra placeholder fields required by the template
                (e.g. cloudflare-ai accountId), substituted into baseUrl. */}
            {extraFields.map((field) => (
              <div key={field} className='flex flex-col gap-2'>
                <Label htmlFor={`from-template-extra-${field}`}>
                  {field}
                  <span className='text-destructive'> *</span>
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
                  placeholder={
                    selected.defaultExtra?.[field] ??
                    t('Required by this template')
                  }
                  autoComplete='off'
                />
              </div>
            ))}

            <div className='flex flex-col gap-2'>
              <Label htmlFor='from-template-proxy'>
                {t('Outbound proxy for this channel')}
              </Label>
              <select
                id='from-template-proxy'
                value={proxyMode}
                onChange={(e) =>
                  setProxyMode(e.target.value as 'pool' | 'direct' | 'custom')
                }
                className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
              >
                <option value='pool'>
                  {t('Follow my egress IP pool (rotate per request)')}
                </option>
                <option value='direct'>
                  {t('Direct connection (no proxy)')}
                </option>
                <option value='custom'>{t('Fixed proxy URL')}</option>
              </select>
              {proxyMode === 'custom' && (
                <Input
                  value={proxyUrl}
                  onChange={(e) => setProxyUrl(e.target.value)}
                  placeholder={t('e.g. http://1.2.3.4:8080 or socks5://host:1080')}
                  autoComplete='off'
                />
              )}
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Reverse-proxy accounts carry risk: use a throwaway account, never your main one.'
                )}
              </p>
            </div>

            <div className='flex flex-col gap-2'>
              <Label htmlFor='from-template-tier'>{t('Channel tier')}</Label>
              <select
                id='from-template-tier'
                value={tier}
                onChange={(e) => setTier(e.target.value as 'free' | 'api')}
                className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
              >
                <option value='api'>{t('API key tier')}</option>
                <option value='free'>{t('Free tier (subscription IP)')}</option>
              </select>
              {tier === 'free' && (
                <Input
                  value={allowIps}
                  onChange={(e) => setAllowIps(e.target.value)}
                  placeholder={t('Allowed IPs (comma or newline separated, CIDR ok)')}
                  autoComplete='off'
                />
              )}
            </div>

            {tier === 'api' && !isSubscription && !isNoKey && (
            <div className='flex flex-col gap-2'>
              <Label htmlFor='from-template-multikey'>
                {t('Multiple keys (optional, one per line)')}
              </Label>
              <textarea
                id='from-template-multikey'
                value={multiKeys}
                onChange={(e) => setMultiKeys(e.target.value)}
                placeholder={t('Leave empty to use the single API key above')}
                className='border-input bg-background min-h-[72px] w-full rounded-md border px-3 py-2 text-sm'
              />
              {parsedKeys.length > 1 && (
                <select
                  value={multiKeyMode}
                  onChange={(e) =>
                    setMultiKeyMode(e.target.value as 'random' | 'polling')
                  }
                  className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
                >
                  <option value='random'>{t('Random key each request')}</option>
                  <option value='polling'>{t('Round-robin keys')}</option>
                </select>
              )}
            </div>
            )}

            <label className='flex items-center gap-2 text-sm'>
              <input
                type='checkbox'
                checked={visibility}
                onChange={(e) => setVisibility(e.target.checked)}
                className='size-4'
              />
              {t('Publish to the public pool (others can call it)')}
            </label>
            <div className='flex flex-col gap-2'>
              <Label htmlFor='from-template-name'>
                {t('Channel Name (optional)')}
              </Label>
              <Input
                id='from-template-name'
                value={customName}
                onChange={(e) => setCustomName(e.target.value)}
                placeholder={selected.name}
                maxLength={64}
              />
            </div>
              </>
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
              {t('Create Channel')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}


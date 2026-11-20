import type {
  PublicChannelView,
  PublicModelItem,
  PublicModelSource,
} from '../types'

export type CatalogStatus =
  | 'available'
  | 'degraded'
  | 'down'
  | 'disabled'
  | 'unknown'
export interface CatalogModel {
  key: string
  model: string
  source: PublicModelSource
  vendor: string
  icon: string | null
  channels: PublicChannelView[]
  status: CatalogStatus
  comments: number
  healthScore: number | null
  successRate24h: number | null
  activeRequests: number | null
  activeUsers: number | null
  aliases: string[]
}

const VENDORS: { pattern: RegExp; name: string; icon: string }[] = [
  { pattern: /deepseek/i, name: 'DeepSeek', icon: 'DeepSeek.Color' },
  { pattern: /anthropic|claude/i, name: 'Anthropic', icon: 'Claude.Color' },
  {
    pattern: /openai|(?:^|[/\s-])(?:gpt|chatgpt|o[134](?:-|$))/i,
    name: 'OpenAI',
    icon: 'OpenAI',
  },
  { pattern: /google|gemini|gemma/i, name: 'Google', icon: 'Gemini.Color' },
  { pattern: /qwen|qwq|tongyi|alibaba/i, name: 'Qwen', icon: 'Qwen.Color' },
  { pattern: /chatglm|glm|zhipu|z[.-]?ai/i, name: 'Z.ai', icon: 'Zhipu.Color' },
  { pattern: /meta|llama/i, name: 'Meta', icon: 'Meta.Color' },
  { pattern: /moonshot|kimi/i, name: 'Moonshot', icon: 'Kimi.Color' },
  { pattern: /minimax|abab/i, name: 'MiniMax', icon: 'Minimax.Color' },
  {
    pattern: /mistral|codestral|devstral/i,
    name: 'Mistral',
    icon: 'Mistral.Color',
  },
  { pattern: /nvidia|nemotron/i, name: 'NVIDIA', icon: 'Nvidia.Color' },
  { pattern: /cohere|command-r/i, name: 'Cohere', icon: 'Cohere.Color' },
  { pattern: /hunyuan/i, name: 'Tencent', icon: 'Hunyuan.Color' },
]

/** Channel provider can be an OpenAI-compatible transport; prefer a recognized model brand. */
export function modelVendor(
  model: string,
  provider?: string | null
): { name: string; icon: string | null } {
  const modelMatch = VENDORS.find((vendor) => vendor.pattern.test(model))
  const value = provider?.trim() || model
  const match =
    modelMatch ?? VENDORS.find((vendor) => vendor.pattern.test(value))
  return {
    name: match?.name ?? provider?.trim() ?? '',
    icon: match?.icon ?? null,
  }
}

export function channelSamples(channel: PublicChannelView): number {
  const count = channel.sample_count ?? channel.samples
  return typeof count === 'number' && Number.isFinite(count) && count > 0
    ? count
    : 0
}

export function channelStatus(channel: PublicChannelView): CatalogStatus {
  if (channel.enabled === false) return 'disabled'
  if (channelSamples(channel) === 0) return 'unknown'
  const status = channel.status || channel.health_state
  if (status === 'healthy' || status === 'available' || status === 'ok') {
    return 'available'
  }
  if (status === 'degraded') return 'degraded'
  if (status === 'down' || status === 'unavailable') return 'down'
  return 'unknown'
}

/** One source per card; global item counters/owners are deliberately not copied. */
export function projectCatalog(items: PublicModelItem[]): {
  models: CatalogModel[]
  unclassified: number
} {
  const cards = new Map<string, CatalogModel>()
  let unclassified = 0
  for (const item of items) {
    for (const channel of item.channels ?? []) {
      const source = channel.source_kind
      if (source !== 'platform_free' && source !== 'user_shared') {
        unclassified++
        continue
      }
      const key = `${source}:${item.model}`
      let card = cards.get(key)
      if (!card) {
        const vendor = modelVendor(item.model, channel.provider)
        card = {
          key,
          model: item.model,
          source,
          vendor: vendor.name,
          icon: vendor.icon,
          channels: [],
          status: 'unknown',
          comments: item.comments,
          healthScore: item.health_score ?? null,
          successRate24h: item.success_rate_24h ?? null,
          activeRequests: item.active_requests ?? null,
          activeUsers: item.active_users ?? null,
          aliases: item.aliases ?? [],
        }
        cards.set(key, card)
      }
      if (
        !card.channels.some(
          (existing) => existing.channel_id === channel.channel_id
        )
      ) {
        card.channels.push(channel)
      }
    }
  }
  for (const card of cards.values()) {
    const statuses = card.channels.map(channelStatus)
    if (statuses.every((status) => status === 'disabled')) {
      card.status = 'disabled'
    } else if (statuses.includes('available')) card.status = 'available'
    else if (statuses.includes('degraded')) card.status = 'degraded'
    else if (
      statuses.every((status) => status === 'down' || status === 'disabled')
    ) {
      card.status = 'down'
    }
  }
  return { models: [...cards.values()], unclassified }
}

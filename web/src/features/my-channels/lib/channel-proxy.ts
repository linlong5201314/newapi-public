import type { ChannelProxyPolicy } from '../api'

export function isChannelProxySelectionComplete(
  policy: ChannelProxyPolicy,
  proxy: string,
  fixedConfigured = false
): boolean {
  if (policy.mode === 'subscription') return Boolean(policy.subscription_id)
  if (policy.mode === 'endpoint') return Boolean(policy.endpoint_id)
  if (policy.mode === 'fixed') {
    return fixedConfigured || /^(https?|socks5h?):\/\/\S+$/i.test(proxy.trim())
  }
  return policy.mode === 'user_pool'
}

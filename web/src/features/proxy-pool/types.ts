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

/**
 * [M7-PROXY] 用户出口 IP 池类型。
 *
 * 免费层几乎都按「出口 IP」限流（30 RPM/IP、150K tokens/day/IP 之类）。
 * 把机场订阅 / 住宅 IP / vless 等解析成多条出口后，上游请求按节点轮换，
 * 同一账号就能用到 N 倍的额度——「加 IP = 额度变多」的实现。
 *
 * 字段名与 `controller/proxy_pool.go` 的 `proxyEndpointView` /
 * `proxySubscriptionView` 一一对应，别改名字。
 */

/** 后端能直接拿去建 http.Client 的协议。 */
export const DIALABLE_KINDS = ['http', 'https', 'socks5', 'socks5h'] as const

/** 需要本地 mihomo/clash 起端口才能用的客户端型协议。 */
export const CLIENT_KINDS = [
  'vless',
  'vmess',
  'trojan',
  'hysteria2',
  'ss',
  'anytls',
] as const

export interface ProxyEndpoint {
  id: number
  name: string
  kind: string
  /** 脱敏地址：`scheme://***@host:port`，账号密码不回传。 */
  url?: string
  region: string
  subscription_id: number
  enabled: boolean
  weight: number
  fail_count: number
  last_error: string
  /** 有 url 才为 true（vless 等客户端节点没有可直接 dial 的地址）。 */
  dialable: boolean
  /** 客户端节点的 uri 前缀（`vless://`、`ss://` 等），用于展示。 */
  raw_uri_prefix?: string
  last_ok_at?: string
  created_at: string
  updated_at: string
}

export interface ProxySubscription {
  id: number
  name: string
  /** 脱敏订阅链接：只留 `scheme://host`。 */
  url: string
  format: string
  node_count: number
  last_error: string
  enabled: boolean
  last_sync_at?: string
  created_at: string
  updated_at: string
}

export interface ProxyPoolSummary {
  endpoint_total: number
  endpoint_enabled: number
  /** 可直接用于上游请求的出口数 = 实际能放大的额度倍数。 */
  usable_url_count: number
  subscription_total: number
  /** 客户端型节点数（vless/vmess/trojan/ss…）。 */
  client_node_count: number
}

export interface ApiEnvelope<T> {
  success: boolean
  message?: string
  data: T
}

export interface CreateEndpointPayload {
  name?: string
  url: string
}

export interface TestEndpointResult {
  ok: boolean
  ip?: string
  error?: string
}

export interface SyncSubscriptionResult {
  node_count?: number
  format?: string
}

export interface CreateSubscriptionPayload {
  name?: string
  url: string
  format?: 'auto' | 'clash' | 'base64' | 'uri-list'
}

/** 协议 → 中文标签 / 徽章配色。 */
export const KIND_LABELS: Record<string, string> = {
  http: 'HTTP',
  https: 'HTTPS',
  socks5: 'SOCKS5',
  socks5h: 'SOCKS5H',
  vless: 'VLESS',
  vmess: 'VMess',
  trojan: 'Trojan',
  hysteria2: 'Hysteria2',
  ss: 'Shadowsocks',
  anytls: 'AnyTLS',
}

export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? (kind ? kind.toUpperCase() : '—')
}

export function isDialableKind(kind: string): boolean {
  return (DIALABLE_KINDS as readonly string[]).includes(kind)
}

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
 * Imported nodes are inventory, not verified unique exit IPs.
 * Each account retains its selected endpoint until it becomes unusable.
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
  user_id?: number
  is_owner?: boolean
  name: string
  kind: string
  /** 脱敏地址：`scheme://***@host:port`，账号密码不回传。 */
  url?: string
  region: string
  subscription_id: number
  source?: string
  enabled: boolean
  public?: boolean
  weight: number
  latency_ms?: number
  fail_count: number
  last_error: string
  /** 有 url 或内置 mihomo 支持的客户端节点为 true。 */
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
  /** 已用流量（字节），来自订阅头 subscription-userinfo。 */
  traffic_used?: number
  /** 总流量（字节），0 或缺失表示机场未上报。 */
  traffic_total?: number
  /** 到期时间（RFC3339），机场未上报则为空。 */
  expire_at?: string
  last_error: string
  enabled: boolean
  public?: boolean
  last_sync_at?: string
  created_at: string
  updated_at: string
}

export interface ProxyPoolSummary {
  imported_total?: number
  enabled?: number
  loaded_this_process?: number
  probed_healthy?: number
  unknown?: number
  failed?: number
  disabled?: number
  inventory_scope?: 'database_visible_to_user'
  loaded_scope?: 'this_process_visible_to_user'
  inventory_remaining?: number
  list_limit?: number
  runtime_preload_limit?: number
  endpoint_total: number
  endpoint_enabled: number
  /** Configured dialable URLs; not a probe result or unique exit-IP count. */
  usable_url_count: number
  subscription_total: number
  /** 客户端型节点数（vless/vmess/trojan/ss…）。 */
  client_node_count: number
  public_node_count?: number
  haizhu_node_count?: number
}

export interface ProxyRuntimeStatus {
  available: boolean
  running: boolean
  binary: string
  proxies: number
  public_count?: number
  selected_node?: string
  last_error: string
  started_at: number
  port: number
  has_listener: boolean
  proxy_url: string
  nodes: string[]
}

export interface ApiEnvelope<T> {
  success: boolean
  message?: string
  data: T
}

export interface CreateEndpointPayload {
  name?: string
  url: string
  public?: boolean
}

export interface TestEndpointResult {
  ok: boolean
  ip?: string
  latency_ms?: number
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
  public?: boolean
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

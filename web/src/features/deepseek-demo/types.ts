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
export type DemoRole = 'user' | 'assistant'

export type DemoMessageStatus = 'streaming' | 'complete' | 'error' | 'stopped'

export interface DemoMessage {
  id: string
  role: DemoRole
  content: string
  /** 推理型模型（R1 系）返回的思考过程，折叠展示。 */
  reasoning?: string
  status: DemoMessageStatus
  error?: string
  createdAt: number
  /** 首字延迟与总耗时，仅用于气泡下方的状态行。 */
  latencyMs?: number
}

export interface DemoConversation {
  id: string
  title: string
  model: string
  group: string
  messages: DemoMessage[]
  createdAt: number
  updatedAt: number
}

export interface DemoModelOption {
  label: string
  value: string
}

export interface DemoGroupOption {
  label: string
  value: string
  desc?: string
}

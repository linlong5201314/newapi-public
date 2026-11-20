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

/** A composer attachment already read into memory for multimodal chat. */
export interface DemoAttachment {
  id: string
  name: string
  kind: 'image' | 'text'
  dataUrl: string
  /** Present for text attachments: the decoded file body. */
  textContent?: string
}

/** A generated image rendered inside an assistant message. */
export interface DemoImage {
  url: string
  kind: 'url' | 'b64'
}

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
  /** 用户消息随附的附件（图片或文本）。 */
  attachments?: DemoAttachment[]
  /** 图像生成回复渲染的图片。 */
  image?: DemoImage
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

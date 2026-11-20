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
import { nanoid } from 'nanoid'

import type { ContentPart } from '@/features/playground/types'

import type { DemoAttachment } from './types'

export const MAX_ATTACHMENTS = 3
export const MAX_ATTACHMENT_BYTES = 2 * 1024 * 1024

const ACCEPTED_TEXT_EXTENSIONS = ['.md', '.markdown', '.json', '.csv', '.txt']
const ACCEPTED_TEXT_MIMES = new Set(['application/json', 'text/csv'])

/** input[type=file] accept list the Android file chooser policy understands. */
export const ATTACHMENT_ACCEPT = [
  'image/*',
  'text/*',
  '.md',
  '.markdown',
  '.json',
  '.csv',
].join(',')

export type AttachmentRejectReason = 'type' | 'size' | 'count'

export interface RejectedAttachment {
  name: string
  reason: AttachmentRejectReason
}

export interface ReadAttachmentsResult {
  added: DemoAttachment[]
  rejected: RejectedAttachment[]
}

function isImageFile(file: File): boolean {
  return file.type.startsWith('image/')
}

function isTextFile(file: File): boolean {
  if (file.type.startsWith('text/')) return true
  if (ACCEPTED_TEXT_MIMES.has(file.type)) return true
  const lower = file.name.toLowerCase()
  return ACCEPTED_TEXT_EXTENSIONS.some((ext) => lower.endsWith(ext))
}

function readFile(file: File, mode: 'dataURL' | 'text'): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.addEventListener('load', () => resolve(String(reader.result)))
    reader.addEventListener('error', () =>
      reject(reader.error ?? new Error('read failed'))
    )
    if (mode === 'dataURL') {
      reader.readAsDataURL(file)
    } else {
      reader.readAsText(file)
    }
  })
}

export async function readDemoAttachments(
  files: Iterable<File>,
  existing: DemoAttachment[]
): Promise<ReadAttachmentsResult> {
  const result: ReadAttachmentsResult = { added: [], rejected: [] }
  let count = existing.length
  for (const file of files) {
    if (count >= MAX_ATTACHMENTS) {
      result.rejected.push({ name: file.name, reason: 'count' })
      continue
    }
    if (!isImageFile(file) && !isTextFile(file)) {
      result.rejected.push({ name: file.name, reason: 'type' })
      continue
    }
    if (file.size > MAX_ATTACHMENT_BYTES) {
      result.rejected.push({ name: file.name, reason: 'size' })
      continue
    }
    const dataUrl = await readFile(file, 'dataURL')
    if (isImageFile(file)) {
      result.added.push({
        id: nanoid(),
        name: file.name,
        kind: 'image',
        dataUrl,
      })
    } else {
      result.added.push({
        id: nanoid(),
        name: file.name,
        kind: 'text',
        dataUrl,
        textContent: await readFile(file, 'text'),
      })
    }
    count += 1
  }
  return result
}

/**
 * Build the OpenAI multimodal message content for a user turn. Plain text is
 * returned unchanged so text-only conversations keep the exact legacy shape.
 */
export function buildAttachmentContentParts(
  text: string,
  attachments: DemoAttachment[]
): string | ContentPart[] {
  if (attachments.length === 0) return text
  const parts: ContentPart[] = [{ type: 'text', text }]
  for (const attachment of attachments) {
    if (attachment.kind === 'image') {
      parts.push({ type: 'image_url', image_url: { url: attachment.dataUrl } })
    } else {
      parts.push({
        type: 'text',
        text: `Attached file "${attachment.name}":\n${attachment.textContent ?? ''}`,
      })
    }
  }
  return parts
}

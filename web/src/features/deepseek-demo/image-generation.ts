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
import { getFreshAuthHeaders } from '@/lib/api'

import type { DemoImage } from './types'

/**
 * Image generation goes through the playground relay rather than the raw
 * `/v1/images/generations` route: the `/v1` router is gated by TokenAuth
 * (relay `sk-` API keys only), while the demo page holds a dashboard session
 * (short-lived access token). `/pg/**` is registered under UserAuth and uses
 * the same session headers as the chat completions demo.
 */
export const IMAGE_GENERATIONS_ENDPOINT = '/pg/images/generations'

interface GenerateImageOptions {
  model: string
  group?: string
  prompt: string
  signal?: AbortSignal
}

interface ImageResponseItem {
  url?: string
  b64_json?: string
}

interface ImageGenerationResponse {
  data?: ImageResponseItem[]
  error?: { message?: string }
}

export async function generateDemoImage(
  options: GenerateImageOptions
): Promise<DemoImage> {
  const headers = await getFreshAuthHeaders()
  const response = await fetch(IMAGE_GENERATIONS_ENDPOINT, {
    method: 'POST',
    headers,
    signal: options.signal,
    body: JSON.stringify({
      model: options.model,
      group: options.group,
      prompt: options.prompt,
      n: 1,
      size: '1024x1024',
    }),
  })
  const body = (await response.json()) as ImageGenerationResponse
  if (!response.ok) {
    throw new Error(
      body?.error?.message ||
        `Image generation failed (HTTP ${response.status})`
    )
  }
  const item = body.data?.[0]
  if (item?.url) return { kind: 'url', url: item.url }
  if (item?.b64_json) {
    return { kind: 'b64', url: `data:image/png;base64,${item.b64_json}` }
  }
  throw new Error('The image response did not include an image.')
}

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
 * Conservative client-side capability lists. Unknown models default to
 * text-only: the image action stays hidden and attachments stay disabled.
 */
const IMAGE_MODEL_MARKERS = [
  'dall-e',
  'gpt-image',
  'qwen-image',
  'flux',
  'seedream',
  'stable-diffusion',
  'midjourney',
  'mj-',
] as const

const VISION_MODEL_MARKERS = [
  'gpt-4o',
  'gpt-4.1',
  'vision',
  'qwen-vl',
  'qvq',
  'gemini',
  'claude-3',
  'claude-sonnet-4',
  'claude-opus-4',
  'doubao-vision',
] as const

function matchesMarker(modelId: string, markers: readonly string[]): boolean {
  const id = modelId.trim().toLowerCase()
  if (!id) return false
  return markers.some((marker) => id.includes(marker))
}

export function isImageModel(modelId: string): boolean {
  return matchesMarker(modelId, IMAGE_MODEL_MARKERS)
}

export function isVisionModel(modelId: string): boolean {
  return matchesMarker(modelId, VISION_MODEL_MARKERS)
}

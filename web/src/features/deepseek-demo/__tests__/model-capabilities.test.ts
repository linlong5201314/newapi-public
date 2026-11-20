import { describe, expect, it } from 'vitest'

import { isImageModel, isVisionModel } from '../model-capabilities'

describe('model capability detection', () => {
  it('treats known image families as image models regardless of case', () => {
    expect(isImageModel('dall-e-3')).toBe(true)
    expect(isImageModel('gpt-image-1')).toBe(true)
    expect(isImageModel('qwen-image-plus')).toBe(true)
    expect(isImageModel('flux-schnell')).toBe(true)
    expect(isImageModel('seedream-4-0')).toBe(true)
    expect(isImageModel('stable-diffusion-xl')).toBe(true)
    expect(isImageModel('midjourney-v6')).toBe(true)
    expect(isImageModel('MJ-Imagine')).toBe(true)
  })

  it('keeps text-only models out of the image family', () => {
    expect(isImageModel('deepseek-chat')).toBe(false)
    expect(isImageModel('gpt-4o')).toBe(false)
    expect(isImageModel('claude-3-5-sonnet')).toBe(false)
    expect(isImageModel('')).toBe(false)
  })

  it('treats known vision families as attachment-capable regardless of case', () => {
    expect(isVisionModel('gpt-4o')).toBe(true)
    expect(isVisionModel('gpt-4o-mini')).toBe(true)
    expect(isVisionModel('qwen-vl-max')).toBe(true)
    expect(isVisionModel('gemini-2.5-pro')).toBe(true)
    expect(isVisionModel('claude-3-opus')).toBe(true)
    expect(isVisionModel('doubao-vision-pro')).toBe(true)
    expect(isVisionModel('llama-3.2-11b-vision')).toBe(true)
  })

  it('keeps text-only models out of the vision family', () => {
    expect(isVisionModel('deepseek-chat')).toBe(false)
    expect(isVisionModel('dall-e-3')).toBe(false)
    expect(isVisionModel('')).toBe(false)
  })
})

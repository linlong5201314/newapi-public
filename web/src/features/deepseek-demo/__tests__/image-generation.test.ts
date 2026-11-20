import { afterEach, describe, expect, it, vi } from 'vitest'

import { generateDemoImage } from '../image-generation'

vi.mock('@/lib/api', () => ({
  getFreshAuthHeaders: async () => ({
    'Content-Type': 'application/json',
    Authorization: 'Bearer session-access-token',
  }),
}))

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('image generation request', () => {
  it('posts the OpenAI image payload to the playground relay with session auth', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          created: 1,
          data: [{ url: 'https://cdn.example/cat.png' }],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } }
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    const result = await generateDemoImage({
      model: 'dall-e-3',
      group: 'team',
      prompt: 'a cat',
    })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/pg/images/generations')
    expect(init.method).toBe('POST')
    expect(init.headers).toMatchObject({
      Authorization: 'Bearer session-access-token',
    })
    expect(JSON.parse(String(init.body))).toEqual({
      model: 'dall-e-3',
      group: 'team',
      prompt: 'a cat',
      n: 1,
      size: '1024x1024',
    })
    expect(result).toEqual({ kind: 'url', url: 'https://cdn.example/cat.png' })
  })

  it('renders base64 payloads as data URLs', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ data: [{ b64_json: 'QUJD' }] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      )
    )

    const result = await generateDemoImage({
      model: 'gpt-image-1',
      prompt: 'a dog',
    })
    expect(result).toEqual({
      kind: 'b64',
      url: 'data:image/png;base64,QUJD',
    })
  })

  it('surfaces the server error message for failed generations', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: { message: 'quota exhausted' } }), {
          status: 429,
          headers: { 'Content-Type': 'application/json' },
        })
      )
    )

    await expect(
      generateDemoImage({ model: 'dall-e-3', prompt: 'x' })
    ).rejects.toThrow('quota exhausted')
  })
})

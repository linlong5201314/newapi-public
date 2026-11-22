import { describe, expect, it } from 'vitest'

import {
  buildAttachmentContentParts,
  MAX_ATTACHMENT_BYTES,
  MAX_ATTACHMENTS,
  readDemoAttachments,
} from '../attachments'

function textFile(name: string, size: number, type = 'text/plain'): File {
  return new File(['x'.repeat(size)], name, { type })
}

describe('demo attachment validation', () => {
  it('rejects an unsupported file type before reading', async () => {
    const result = await readDemoAttachments(
      [new File(['x'], 'archive.zip', { type: 'application/zip' })],
      []
    )
    expect(result.added).toHaveLength(0)
    expect(result.rejected).toHaveLength(1)
    expect(result.added[0]?.kind).toBeUndefined()
    expect(result.rejected[0]?.name).toBe('archive.zip')
  })

  it('rejects a file larger than the per-file budget', async () => {
    const result = await readDemoAttachments(
      [textFile('big.txt', MAX_ATTACHMENT_BYTES + 1)],
      []
    )
    expect(result.added).toHaveLength(0)
    expect(result.rejected).toHaveLength(1)
  })

  it('rejects files once the attachment budget is full', async () => {
    const existing = [
      {
        id: 'a1',
        name: 'one.txt',
        kind: 'text' as const,
        dataUrl: 'data:text/plain;base64,eA==',
        textContent: 'x',
      },
      {
        id: 'a2',
        name: 'two.txt',
        kind: 'text' as const,
        dataUrl: 'data:text/plain;base64,eA==',
        textContent: 'x',
      },
      {
        id: 'a3',
        name: 'three.txt',
        kind: 'text' as const,
        dataUrl: 'data:text/plain;base64,eA==',
        textContent: 'x',
      },
    ]
    expect(existing).toHaveLength(MAX_ATTACHMENTS)
    const result = await readDemoAttachments(
      [textFile('four.txt', 4)],
      existing
    )
    expect(result.added).toHaveLength(0)
    expect(result.rejected).toHaveLength(1)
  })

  it('reads a markdown file into a text attachment', async () => {
    const file = new File(['# hello'], 'notes.md', { type: 'text/markdown' })
    const result = await readDemoAttachments([file], [])
    expect(result.rejected).toHaveLength(0)
    expect(result.added).toHaveLength(1)
    expect(result.added[0]).toMatchObject({
      name: 'notes.md',
      kind: 'text',
      textContent: '# hello',
    })
  })

  it('reads an image file into an image attachment with a data URL', async () => {
    const file = new File(['fake-png-bytes'], 'pic.png', { type: 'image/png' })
    const result = await readDemoAttachments([file], [])
    expect(result.rejected).toHaveLength(0)
    expect(result.added).toHaveLength(1)
    expect(result.added[0]?.kind).toBe('image')
    expect(result.added[0]?.dataUrl.startsWith('data:image/png;base64,')).toBe(
      true
    )
  })
})

describe('multimodal message construction', () => {
  it('returns the plain text when there are no attachments', () => {
    expect(buildAttachmentContentParts('Hello', [])).toBe('Hello')
  })

  it('appends an image_url data URL part for image attachments', () => {
    const parts = buildAttachmentContentParts('Look', [
      {
        id: 'a1',
        name: 'pic.png',
        kind: 'image',
        dataUrl: 'data:image/png;base64,QUJD',
      },
    ])
    expect(parts).toEqual([
      { type: 'text', text: 'Look' },
      {
        type: 'image_url',
        image_url: { url: 'data:image/png;base64,QUJD' },
      },
    ])
  })

  it('inlines a text attachment as a labeled text block', () => {
    const parts = buildAttachmentContentParts('Summarize', [
      {
        id: 'a1',
        name: 'notes.md',
        kind: 'text',
        dataUrl: 'data:text/markdown;base64,IyBoZWxsbw==',
        textContent: '# hello',
      },
    ])
    expect(parts).toEqual([
      { type: 'text', text: 'Summarize' },
      { type: 'text', text: 'Attached file "notes.md":\n# hello' },
    ])
  })
})

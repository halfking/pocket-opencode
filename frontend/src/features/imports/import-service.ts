import { assetStore } from '../../native/asset-store'
import { parseEnex, type EvernoteNote } from './evernote-parser'

export interface ImportSummary {
  imported: number
  skipped: number
  attachments: number
}

export async function importEnex(xml: string, workspaceId = 'default'): Promise<ImportSummary> {
  const notes = parseEnex(xml)
  let imported = 0
  let skipped = 0
  let attachments = 0

  const seen = new Set<string>()
  for (const note of notes) {
    if (seen.has(note.enexId)) {
      skipped += 1
      continue
    }
    seen.add(note.enexId)

    const duplicate = await assetStore.findByMeta({
      workspaceId,
      kind: 'note',
      source: 'enex_import',
      key: 'enexId',
      value: note.enexId,
    })
    if (duplicate) {
      skipped += 1
      continue
    }

    const asset = await assetStore.upsert({
      workspaceId,
      kind: 'note',
      title: note.title || 'Evernote note',
      bodyText: note.contentMarkdown,
      metaJson: JSON.stringify({
        enexId: note.enexId,
        originalCreated: note.createdAt,
        originalUpdated: note.updatedAt,
        tags: note.tags,
        author: note.author,
        sourceUrl: note.sourceUrl,
      }),
      source: 'enex_import',
      syncMode: 'e2ee_local_first',
    })

    for (const resource of note.resources) {
      if (resource.data) {
        // assetStore 当前以字符串 blob 为抽象边界；保留 base64 原文可无损支持二进制附件。
        await assetStore.addBlob(asset.id, resource.data.replace(/\s/g, ''), {
          kind: resource.mime,
          hash: resource.hash,
        })
        attachments += 1
      }
    }
    imported += 1
  }

  return { imported, skipped, attachments }
}

function decodeBase64(value: string): string | null {
  try {
    if (typeof atob === 'function') return atob(value.replace(/\s/g, ''))
    return null
  } catch {
    return null
  }
}

export function readImportFile(file: File): Promise<string> {
  return file.text()
}

export type { EvernoteNote }

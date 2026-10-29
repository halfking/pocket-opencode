import { assetStore } from '../../native/asset-store'
import { parseEnex, type EvernoteNote } from './evernote-parser'
import { localDB } from '../../native/local-db'
import { captureNotesEpoch } from '../../native/portal-sync/epochs'
import { portalNotesStore } from '../../native/portal-sync/storage'

export interface ImportSummary {
  imported: number
  skipped: number
  attachments: number
}

export async function importEnex(xml: string, workspaceId = 'default'): Promise<ImportSummary> {
  const db = localDB.bindOperation(localDB.captureOperation({ kind: 'local-edit' }))
  const syncEpoch = captureNotesEpoch(db.owner)
  if (db.owner && workspaceId !== db.owner.workspaceId) throw new Error('Import workspace differs from the verified owner')
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
    }, db)
    if (duplicate && !await portalNotesStore(db).find('asset', duplicate.id)) {
      // A legacy row is not proof that the active account owns the original.
      skipped += 1
      continue
    }

    const asset = duplicate ?? await assetStore.upsert({
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
      syncEpoch,
    }, db)

    const blobs = await assetStore.listBlobs(asset.id, db)
    for (const resource of note.resources) {
      if (resource.data && !blobs.some(blob => blob.hash === resource.hash && blob.kind === resource.mime)) {
        // assetStore 当前以字符串 blob 为抽象边界；保留 base64 原文可无损支持二进制附件。
        await assetStore.addBlob(asset.id, resource.data.replace(/\s/g, ''), {
          kind: resource.mime,
          hash: resource.hash,
        }, db)
        attachments += 1
      }
    }
    if (duplicate) skipped += 1
    else imported += 1
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

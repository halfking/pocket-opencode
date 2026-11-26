import { Capacitor } from '@capacitor/core'
import { Directory, Encoding, Filesystem } from '@capacitor/filesystem'
import { arrayBufferToBase64 } from '../../utils/base64'
import type { BackupSource } from './archive'
import { backupStagePath, type DatabaseStage } from './native-database'

export const mediaRoots = ['notes', 'recordings'] as const
export type MediaRoot = typeof mediaRoots[number]

function missing(error: unknown): boolean {
  return (error as { code?: string })?.code === 'OS-PLUG-FILE-0008'
}

export async function backupStat(path: string, directory: Directory) {
  try { return await Filesystem.stat({ path, directory }) } catch (error) {
    if (missing(error)) return null
    throw error
  }
}

export async function removeBackupPath(path: string, directory: Directory, tree = false): Promise<void> {
  try {
    if (tree) await Filesystem.rmdir({ path, directory, recursive: true })
    else await Filesystem.deleteFile({ path, directory })
  } catch (error) {
    if (!missing(error)) throw error
  }
}

export async function readBackupText(path: string, directory: Directory): Promise<string | null> {
  try {
    const { data } = await Filesystem.readFile({ path, directory, encoding: Encoding.UTF8 })
    if (typeof data !== 'string') throw new Error('Invalid local backup state file')
    return data
  } catch (error) {
    if (missing(error)) return null
    throw error
  }
}

export async function openCacheSource(path: string): Promise<BackupSource> {
  const stat = await Filesystem.stat({ path, directory: Directory.Cache })
  if (stat.type !== 'file') throw new Error('Invalid staged backup source')
  return {
    size: stat.size,
    read: async (offset, length) => {
      const { data } = await Filesystem.readFile({ path, directory: Directory.Cache, offset, length })
      if (typeof data !== 'string') throw new Error('Invalid native backup byte stream')
      return Uint8Array.from(atob(data), char => char.charCodeAt(0)).buffer
    },
  }
}

export function cacheSink(path: string) {
  let created = false
  const write = async (bytes: ArrayBuffer) => {
    const options = { path, directory: Directory.Cache, data: arrayBufferToBase64(bytes) }
    if (created) await Filesystem.appendFile(options)
    else {
      await Filesystem.writeFile({ ...options, recursive: true })
      created = true
    }
  }
  return { write, close: async () => { if (!created) await write(new ArrayBuffer(0)) } }
}

export async function cacheRecordingBlob(path: string, mimeType: string, size: number): Promise<Blob> {
  if (size === 0) return new Blob([], { type: mimeType })
  // Use a media extension so the iOS Capacitor asset handler can map file bytes.
  const { uri } = await Filesystem.getUri({ path, directory: Directory.Cache })
  const response = await fetch(Capacitor.convertFileSrc(uri))
  // The iOS handler returns a native URLResponse for mapped media and WebKit
  // exposes its body with status zero. Require the exact authenticated length.
  if (!response.ok && response.status !== 0) throw new Error('Unable to read a staged recording')
  const blob = await response.blob()
  if (blob.size !== size) throw new Error('The staged recording changed')
  return blob.slice(0, blob.size, mimeType)
}

export function cacheMediaPath(stage: DatabaseStage, path: string): string {
  return `${backupStagePath(stage)}/media/${path}`
}

/** Copy while the LocalDB queue prevents changes to the set of committed pointers. */
export async function captureBackupMedia(stage: DatabaseStage, assertCurrent: () => void): Promise<string[]> {
  const captured: string[] = []
  async function visit(path: string) {
    const entries = (await Filesystem.readdir({ path, directory: Directory.Data })).files
    for (const entry of entries) {
      assertCurrent()
      const relative = `${path}/${entry.name}`
      if (!/^[A-Za-z0-9._-]{1,255}$/.test(entry.name) || entry.name === '.' || entry.name === '..') {
        throw new Error('Unexpected name in local backup media')
      }
      if (entry.type === 'directory') await visit(relative)
      else {
        const to = cacheMediaPath(stage, relative)
        const parent = to.slice(0, to.lastIndexOf('/'))
        await Filesystem.mkdir({ path: parent, directory: Directory.Cache, recursive: true }).catch(error => {
          if ((error as { code?: string })?.code !== 'OS-PLUG-FILE-0010') throw error
        })
        await Filesystem.copy({ from: relative, to, directory: Directory.Data, toDirectory: Directory.Cache })
        const [after, copied] = await Promise.all([
          Filesystem.stat({ path: relative, directory: Directory.Data }),
          Filesystem.stat({ path: to, directory: Directory.Cache }),
        ])
        if (entry.size !== after.size || entry.mtime !== after.mtime || copied.size !== entry.size) {
          throw new Error('A recording or note file changed during the backup')
        }
        captured.push(relative)
      }
    }
  }
  for (const root of mediaRoots) {
    const stat = await backupStat(root, Directory.Data)
    if (!stat) continue
    if (stat.type !== 'directory') throw new Error('Invalid backup media directory')
    await visit(root)
  }
  assertCurrent()
  return captured.sort()
}

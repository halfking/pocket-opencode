import { BackupError, type BackupEntry, type BackupManifest, type BackupOwner, validateManifest } from './manifest.ts'

const MAGIC = new TextEncoder().encode('POCKET\x00\x01')
const SALT_BYTES = 16
const NONCE_PREFIX_BYTES = 8
const MANIFEST_LENGTH_OFFSET = MAGIC.length + SALT_BYTES + NONCE_PREFIX_BYTES
const HEADER_BYTES = MANIFEST_LENGTH_OFFSET + 4
const GCM_TAG_BYTES = 16
const CHUNK_BYTES = 1024 * 1024
const MAX_MANIFEST_BYTES = 2 * CHUNK_BYTES
// At most 64 GiB per derived archive key, and well below nonce counter wrap.
const MAX_DATA_CHUNKS = 65_536

type Read = (offset: number, length: number) => Promise<ArrayBuffer>
type Write = (bytes: ArrayBuffer) => Promise<void>

interface EntrySource {
  entry: BackupEntry
  read: Read
}

export interface BackupSource {
  size: number
  read: Read
}

/** All writes go to private staging. Only commit may expose the candidate. */
export interface BackupStaging {
  open(entry: BackupEntry): Promise<{ write: Write; close(): Promise<void> }>
  commit(): Promise<void>
  abort(): Promise<void>
}

async function keyFor(header: Uint8Array<ArrayBuffer>, password: string, usage: 'encrypt' | 'decrypt'): Promise<CryptoKey> {
  const material = await crypto.subtle.importKey('raw', new TextEncoder().encode(password), 'PBKDF2', false, ['deriveKey'])
  return crypto.subtle.deriveKey(
    { name: 'PBKDF2', salt: header.subarray(MAGIC.length, MAGIC.length + SALT_BYTES), iterations: 600_000, hash: 'SHA-256' },
    material, { name: 'AES-GCM', length: 256 }, false, [usage],
  )
}

function frame(header: Uint8Array<ArrayBuffer>, ordinal: number): AesGcmParams {
  const iv = new Uint8Array(12)
  iv.set(header.subarray(MAGIC.length + SALT_BYTES, MANIFEST_LENGTH_OFFSET))
  new DataView(iv.buffer).setUint32(NONCE_PREFIX_BYTES, ordinal)
  return { name: 'AES-GCM', iv, additionalData: header, tagLength: 128 }
}

function archiveLength(entries: BackupEntry[], manifestLength: number): number {
  let chunks = 0
  let size = HEADER_BYTES + manifestLength + GCM_TAG_BYTES
  for (const entry of entries) {
    chunks += Math.ceil(entry.size / CHUNK_BYTES)
    if (chunks > MAX_DATA_CHUNKS) throw new BackupError('format')
    size += entry.size
  }
  return size + chunks * GCM_TAG_BYTES
}

async function readExact(read: Read, offset: number, length: number): Promise<ArrayBuffer> {
  const bytes = await read(offset, length)
  if (bytes.byteLength !== length) throw new BackupError('source')
  return bytes
}

/**
 * Header: magic/version, salt, IV prefix, encrypted manifest's plaintext length
 * (uint32 big endian). Each record is ciphertext with a 128-bit GCM tag, starting
 * with the manifest and followed by fixed-size data chunks for its ordered entries.
 * The authenticated manifest determines every boundary and the exact file length.
 */
export async function writeBackup(owner: BackupOwner, entries: EntrySource[], password: string, write: Write): Promise<void> {
  if (!password) throw new BackupError('authentication')
  const sources = entries.map(({ entry, read }) => ({ entry, read }))
  const manifest = validateManifest({ owner, entries: sources.map(source => source.entry) })
  const encoded = new TextEncoder().encode(JSON.stringify(manifest))
  if (encoded.length > MAX_MANIFEST_BYTES) throw new BackupError('format')
  archiveLength(manifest.entries, encoded.length)

  const header = new Uint8Array(HEADER_BYTES)
  header.set(MAGIC)
  crypto.getRandomValues(header.subarray(MAGIC.length, MANIFEST_LENGTH_OFFSET))
  new DataView(header.buffer).setUint32(MANIFEST_LENGTH_OFFSET, encoded.length)
  const key = await keyFor(header, password, 'encrypt')
  let ordinal = 0

  await write(header.slice().buffer)
  await write(await crypto.subtle.encrypt(frame(header, ordinal++), key, encoded))
  for (const [index, entry] of manifest.entries.entries()) {
    for (let offset = 0; offset < entry.size; offset += CHUNK_BYTES) {
      const bytes = await readExact(sources[index].read, offset, Math.min(CHUNK_BYTES, entry.size - offset))
      await write(await crypto.subtle.encrypt(frame(header, ordinal++), key, bytes))
    }
  }
}

export function backupBlobSource(blob: Blob): BackupSource {
  return { size: blob.size, read: (offset, length) => blob.slice(offset, offset + length).arrayBuffer() }
}

export async function restoreBackup(
  source: BackupSource,
  password: string,
  owner: BackupOwner,
  stage: (manifest: BackupManifest) => Promise<BackupStaging>,
): Promise<void> {
  if (!password) throw new BackupError('authentication')
  const expectedOwner = { ...owner }
  const size = source.size
  if (!Number.isSafeInteger(size) || size < HEADER_BYTES + GCM_TAG_BYTES) throw new BackupError('format')
  const header = new Uint8Array(await readExact(source.read, 0, HEADER_BYTES))
  if (!MAGIC.every((byte, index) => header[index] === byte)) throw new BackupError('format')
  const manifestLength = new DataView(header.buffer).getUint32(MANIFEST_LENGTH_OFFSET)
  if (manifestLength < 1 || manifestLength > MAX_MANIFEST_BYTES || HEADER_BYTES + manifestLength + GCM_TAG_BYTES > size) {
    throw new BackupError('format')
  }

  const key = await keyFor(header, password, 'decrypt')
  let ordinal = 0
  let offset = HEADER_BYTES
  async function decrypt(length: number): Promise<ArrayBuffer> {
    if (offset + length + GCM_TAG_BYTES > size) throw new BackupError('format')
    const ciphertext = await readExact(source.read, offset, length + GCM_TAG_BYTES)
    offset += ciphertext.byteLength
    try {
      return await crypto.subtle.decrypt(frame(header, ordinal++), key, ciphertext)
    } catch {
      throw new BackupError('authentication')
    }
  }

  const bytes = await decrypt(manifestLength)
  let parsed: unknown
  try { parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) } catch {
    throw new BackupError('format')
  }
  const manifest = validateManifest(parsed)
  if (archiveLength(manifest.entries, manifestLength) !== size) throw new BackupError('format')
  if (manifest.owner.userId !== expectedOwner.userId || manifest.owner.workspaceId !== expectedOwner.workspaceId) {
    throw new BackupError('owner')
  }

  const target = await stage({ owner: { ...manifest.owner }, entries: manifest.entries.map(entry => ({ ...entry })) })
  try {
    for (const entry of manifest.entries) {
      const output = await target.open({ ...entry })
      for (let remaining = entry.size; remaining > 0; remaining -= CHUNK_BYTES) {
        await output.write(await decrypt(Math.min(CHUNK_BYTES, remaining)))
      }
      await output.close()
    }
    await target.commit()
  } catch (error) {
    try { await target.abort() } catch (abortError) {
      throw Object.assign(new Error('Could not clean up local backup staging'), { errors: [error, abortError] })
    }
    throw error
  }
}

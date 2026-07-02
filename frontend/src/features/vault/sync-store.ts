/**
 * sync-store.ts — 🦞 龙虾云同步：与 pocketd /api/vault/sync 交互
 *
 * 核心铁律：只传密文 blob，服务端零知识。
 * 上传：vault-store.exportEncryptedBlob() → POST /api/vault/sync
 * 下载：GET /api/vault/sync/latest → vault-store.importEncryptedBlob()
 *
 * 冲突策略（MVP）：本地覆盖云端（last-write-wins）。
 * 将来可改 CRDT 或版本合并。
 */
import { http } from '../../api/http'
import * as vaultStore from './vault-store'

export interface SyncResult {
  action: 'upload' | 'download' | 'none'
  entries: number
  version: number
}

/**
 * 上传本地 vault 到云端（加密 blob）。
 */
export async function uploadSync(): Promise<SyncResult> {
  const { blob, version } = await vaultStore.exportEncryptedBlob()
  await http('/api/vault/sync', {
    method: 'POST',
    body: JSON.stringify({ blob, version }),
  })
  const count = await vaultStore.countEntries()
  return { action: 'upload', entries: count, version }
}

/**
 * 从云端下载 vault 到本地（解密后替换本地数据）。
 * 注意：这会用云端数据覆盖本地！如果本地有新条目，应先 uploadSync。
 */
export async function downloadSync(): Promise<SyncResult> {
  const resp = await http<{ blob: string; version: number }>(
    '/api/vault/sync/latest', { method: 'GET' },
  )
  const count = await vaultStore.importEncryptedBlob(resp.blob)
  return { action: 'download', entries: count, version: resp.version }
}

/**
 * 智能同步：先比较本地和云端版本，决定上传还是下载。
 * MVP 策略：本地有数据就上传（push-first），本地空就下载。
 */
export async function smartSync(): Promise<SyncResult> {
  const localCount = await vaultStore.countEntries()

  // 先检查云端有没有数据
  let cloudVersion = 0
  try {
    const resp = await http<{ blob: string; version: number } | null>(
      '/api/vault/sync/latest', { method: 'GET' },
    )
    if (resp) cloudVersion = resp.version
  } catch {
    // 云端无数据（404），继续
  }

  if (localCount > 0) {
    // 本地有数据 → 上传覆盖云端
    return uploadSync()
  } else if (cloudVersion > 0) {
    // 本地空，云端有 → 下载
    return downloadSync()
  }
  return { action: 'none', entries: 0, version: 0 }
}

/**
 * 获取云端版本列表（用于"恢复历史版本"功能）。
 */
export async function listVersions(): Promise<{ version: number; createdAt: number }[]> {
  const resp = await http<{ versions: { version: number; createdAt: number }[] }>(
    '/api/vault/sync/versions', { method: 'GET' },
  )
  return resp.versions || []
}

/**
 * assets.ts — S0-C Lobster Vault 服务端镜像同步 API client.
 *
 * 对接后端 POST /api/assets/sync（push + pull 合并端点）。
 *
 * 注意：本文件只管「云端同步」。本地读写请用 native/asset-store.ts 的
 * assetStore（local SQLite）。同步流程：
 *   1. 调 assetStore.listDirty() 拿本地改动
 *   2. 把改动加密后调 assetsApi.sync() 上传，同时拉取其他设备的改动
 *   3. 拉回的 mirror 解密后写回 assetStore，并 markSynced
 *
 * 见 native/asset-store.ts 末尾的 syncAssets() 流程编排（待 F0.4 接入）。
 */
import { http } from './http'

/** 服务端加密镜像行（客户端不可解密 cipher_blob，但本地有对应明文）。 */
export interface AssetMirror {
  id: string
  workspace_id: string
  kind: string
  client_rev: number
  server_rev: number
  cipher_title?: string
  cipher_blob: string
  deleted_at: number
  updated_at: number
}

export interface PushResult {
  asset_id: string
  server_rev: number
  conflict?: boolean
  prev_blob?: string
}

export interface SyncRequest {
  since: number
  pushes: AssetMirror[]
}

export interface SyncResponse {
  latest_server_rev: number
  pulled: AssetMirror[]
  push_results: PushResult[]
}

export const assetsApi = {
  /**
   * 同步：push 本地 dirty 改动 + pull 其他设备的增量。
   * since = 上次同步拿到的 latest_server_rev。
   */
  sync: (req: SyncRequest) =>
    http<SyncResponse>('/api/assets/sync', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
}

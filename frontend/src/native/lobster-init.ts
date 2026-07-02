/**
 * lobster-init.ts — 🦞 龙虾启动初始化
 *
 * 启动顺序（隐私优先）：
 *   1. 用户提供主密码（首次 setup，后续 unlock）
 *   2. 主密码派生两个用途：
 *      a) localDB 的 SQLCipher 加密密钥
 *      b) crypto 共享 AES-GCM key（vault/email 凭证加密）
 *   3. 初始化 localDB（建表）
 *   4. 加载向量索引到内存
 *
 * 本文件导出 initLobster()，由 App.vue 在用户输入主密码后调用。
 */
import { localDB } from './local-db'
import { vectorIndex } from './vector'
import { initAppCrypto } from './crypto'

export interface InitStatus {
  ready: boolean
  step: 'idle' | 'crypto' | 'db' | 'vectors' | 'done'
}

let _ready = false

/** 是否已完成初始化。 */
export function isLobsterReady(): boolean {
  return _ready
}

/**
 * 用主密码初始化整个龙虾硬壳。
 * @param masterPassword 用户主密码（Keystore 派生，或首次设置）
 */
export async function initLobster(masterPassword: string): Promise<void> {
  if (_ready) return

  // 1. 初始化共享加密 key
  await initAppCrypto(masterPassword)

  // 2. 初始化加密数据库（masterPassword 同时作为 SQLCipher 密钥）
  await localDB.init(masterPassword)

  // 3. 加载向量索引到内存（后台，不阻塞主流程也行，但 MVP 同步加载更简单）
  try {
    await vectorIndex.load()
  } catch (e) {
    console.warn('[lobster] 向量索引加载失败（首次启动正常）:', e)
  }

  _ready = true
}

/** 锁定龙虾（退出登录 / 后台切换时），关闭 DB 连接。 */
export async function lockLobster(): Promise<void> {
  // localDB 目前是单例连接，App 生命周期内常驻。
  // 锁定主要靠清除内存中的主密码引用（crypto key 不可主动清除，
  // 但页面刷新即丢失）。生产环境配合 cap-keystore lock() 实现真正锁定。
  _ready = false
}

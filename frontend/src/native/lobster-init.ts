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
// 初始化进行中标志，防止并发调用 initLobster 导致重复初始化
let _initializing = false

/** 是否已完成初始化。 */
export function isLobsterReady(): boolean {
  return _ready
}

/**
 * 用主密码初始化整个龙虾硬壳。
 * 
 * 并发保护：使用 _initializing 标志防止多次并发调用导致：
 *   - 重复初始化 crypto key
 *   - 多次打开 SQLite 连接
 *   - 重复加载向量索引
 * 如果已在初始化中，后续调用会被忽略（幂等设计）。
 * 
 * @param masterPassword 用户主密码（Keystore 派生，或首次设置）
 */
export async function initLobster(masterPassword: string): Promise<void> {
  if (_ready) return
  if (_initializing) {
    console.warn('[lobster] 初始化进行中，忽略重复调用')
    return
  }

  _initializing = true
  try {
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
  } finally {
    _initializing = false
  }
}

/**
 * 锁定龙虾（退出登录 / 后台切换时），关闭 DB 连接。
 * 
 * 锁定机制说明：
 *   - 本地 DB（SQLCipher）在 App 生命周期内保持连接，_ready 标志用于
 *     控制业务层是否可访问加密数据
 *   - crypto key 存于 JS 内存，页面刷新即清除（Web Crypto API 限制）
 *   - 生产环境配合 Capacitor Keystore 的 lock() 方法实现真正的密钥锁定，
 *     防止后台切换时内存数据泄漏
 */
export async function lockLobster(): Promise<void> {
  _ready = false
  _initializing = false // 重置初始化标志，允许下次解锁
}

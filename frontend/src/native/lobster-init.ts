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
import { ref, type Ref } from 'vue'
import { localDB } from './local-db'
import { vectorIndex } from './vector'
import { initAppCrypto, resetCryptoKey } from './crypto'

export interface InitStatus {
  ready: boolean
  step: 'idle' | 'crypto' | 'db' | 'vectors' | 'done'
}

const _ready = ref(false)
// 初始化进行中标志，防止并发调用 initLobster 导致重复初始化
let _initializing = false
// 2026-10-06 新增：进行中的 init promise。
// 为什么需要它：原来第二次并发调用只 console.warn 后**立刻 return**，
// 对「防止重复初始化」是对的，但**对调用方**，「被忽略」等于
// 「我以为 init 完了，可以继续读库了」⇒ 于是撞上 requireReady() 抛错。
// 现在并发调用 await 同一个 promise：**仍然只初始化一次（幂等性不变）**，
// 但调用方拿到的是「真的就绪了」这个事实。
let _initPromise: Promise<void> | null = null

/**
 * 解锁状态（响应式）。computed/watch 等响应式上下文必须读它，
 * 才能在 initLobster/lockLobster 后自动重算；命令式检查用 isLobsterReady()。
 */
export const lobsterReady: Readonly<Ref<boolean>> = _ready

/** 是否已完成初始化。 */
export function isLobsterReady(): boolean {
  return _ready.value
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
  if (_ready.value) return
  if (_initPromise) {
    // 并发调用：await 同一 in-flight，而不是「忽略」。
    // 仍然只初始化一次（下面那段不会被第二次进入）。
    return _initPromise
  }

  _initializing = true
  const p = (async () => {
    // 0. 2026-10-06 安全修复：**先校验主密码，再碰任何加密状态**。
    //    原顺序是 initAppCrypto → localDB.init，而 initAppCrypto 只做 PBKDF2 派生、
    //    对任何字符串都成功 ⇒ 错口令会先用错口令把 AES key 写进模块级 cryptoKey，
    //    才在后面抛错。放到最前面，错口令就完全碰不到 crypto 层。
    //    真机实证（emulator-5554，A/B/C 三轮对照）：
    //      正确口令 → 离开解锁页；错误口令 → **同样**离开解锁页；
    //      空口令 → 被 unlockSubmitMode 挡住（证明判据非恒真）。
    //    根因三处：crypto.ts:53 只派生不比对、local-db.ts 用「setEncryptionSecret
    //    抛不抛」推断口令一致、插件 Database.java:245 的 open() 取的是**已存的旧 secret**。
    if (!(await localDB.verifyMasterPassword(masterPassword))) {
      throw new Error('主密码不正确')
    }

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

    _ready.value = true
  })()
  _initPromise = p
  try {
    await p
  } finally {
    _initializing = false
    _initPromise = null
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
  _ready.value = false
  _initializing = false // 重置初始化标志，允许下次解锁

  // 关键：清除共享 AES-GCM key。否则锁定后（_ready=false）业务层调用
  // encryptString/decryptString 仍可解密 vault/email 凭证，构成数据泄漏。
  // resetCryptoKey() 把 cryptoKey 置 null，getCryptoKey() 此后会抛错。
  // 注意：不强制关闭 DB 连接（localDB.close 不存在/会导致后续解锁问题），
  // 但清掉 cryptoKey 已足以阻止 vault/email 凭证解密。
  resetCryptoKey()
}

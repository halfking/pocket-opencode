/**
 * BiometricAuth plugin TypeScript bindings（Android 原生指纹登录绑定桥）。
 *
 * 原生实现：frontend/android/.../plugins/BiometricAuthPlugin.java。
 * 语义：绑定 = 指纹验证后用 AndroidKeyStore AES-GCM 加密存储登录凭据；
 * 登录 = 再次指纹验证后解密返回凭据（仅用于调 /api/auth/login）。
 * 主密码另存 master_blob：写入在密码解锁成功后静默完成，读取必须先过
 * BiometricPrompt，供冷启动解锁屏免密认证。
 *
 * Web / iOS 平台无此插件：registerPlugin 的调用会 reject（"not implemented"），
 * 调用方用 isSupported() 做门控，UI 按"不支持"降级。
 */
import { Capacitor, registerPlugin as capRegisterPlugin } from '@capacitor/core'

export interface BiometricAuthPlugin {
  /** 设备是否已录入指纹/人脸（BIOMETRIC_WEAK 且可用） */
  isAvailable(): Promise<{ available: boolean; code?: number }>
  /** 本机是否已绑定登录凭据 */
  hasCredential(): Promise<{ has: boolean }>
  /** 绑定：弹指纹验证后加密存储（username/password 均不得含 \u0000） */
  saveCredential(opts: { username: string; password: string; reason?: string }): Promise<void>
  /** 指纹验证通过后返回存储的凭据 */
  getCredential(opts?: { reason?: string }): Promise<{ username: string; password: string }>
  /** 解绑（同时清除登录凭据与主密码密文） */
  deleteCredential(): Promise<void>
  /** 本机是否已保存主密码密文（用于解锁屏免密认证） */
  hasMasterSecret(): Promise<{ has: boolean }>
  /** 密码解锁成功后静默加密主密码；不弹 BiometricPrompt */
  saveMasterSecret(opts: { password: string }): Promise<void>
  /** 指纹/人脸通过后返回主密码 */
  getMasterSecret(opts?: { reason?: string }): Promise<{ password: string }>
}

let _plugin: BiometricAuthPlugin | null = null
let _loading: Promise<void> | null = null

// 注意：registerPlugin 返回的 Proxy 对任意属性访问（含 then）都会生成插件调用，
// 因此绝不能从 async 函数直接 return 这个代理（会被当 thenable 采用，
// 触发 "BiometricAuth.then() is not implemented"）。这里只把加载过程做成
// Promise，插件实例始终同步传递。
async function ensureLoaded(): Promise<void> {
  if (_plugin !== null || !Capacitor.isNativePlatform()) return
  if (!_loading) {
    _loading = (async () => {
      try {
        _plugin = (capRegisterPlugin as <T>(name: string) => T)('BiometricAuth')
      } catch {
        /* 保持 null：调用方按不支持降级 */
      }
    })()
  }
  await _loading
}

/** 在插件可用时执行 fn（插件实例同步传入，避免 thenable 陷阱）；不可用返回 fallback。 */
async function withPlugin<T>(fn: (p: BiometricAuthPlugin) => Promise<T>, fallback: T): Promise<T> {
  await ensureLoaded()
  if (!_plugin) return fallback
  try {
    return await fn(_plugin)
  } catch {
    return fallback
  }
}

/** 当前平台是否有原生 BiometricAuth 插件（Web 恒为 false）。 */
export function isBiometricPluginSupported(): boolean {
  return Capacitor.isNativePlatform()
}

/** 设备是否已录入生物特征（无插件/查询失败返回 false）。 */
export function isBiometricAvailable(): Promise<boolean> {
  return withPlugin(async p => !!(await p.isAvailable()).available, false)
}

/** 本机是否已绑定指纹登录凭据（无插件/失败返回 false）。 */
export function hasBiometricCredential(): Promise<boolean> {
  return withPlugin(async p => !!(await p.hasCredential()).has, false)
}

/** 绑定（静默尽力而为；失败返回 false，不抛出）。 */
export function bindBiometricCredential(
  username: string,
  password: string,
  reason = '绑定指纹登录',
): Promise<boolean> {
  return withPlugin(
    async p => {
      await p.saveCredential({ username, password, reason })
      return true
    },
    false,
  )
}

/**
 * 设置页绑定：失败要抛给调用方（取消 / 未录入 / 密码无效），不能静默。
 */
export async function bindBiometricLogin(
  username: string,
  password: string,
  reason = '绑定指纹登录',
): Promise<void> {
  await ensureLoaded()
  if (!_plugin) throw new Error('biometric not supported on this platform')
  await _plugin.saveCredential({ username, password, reason })
}

/**
 * 指纹验证后取回凭据；失败抛原始错误（调用方提示并降级到密码登录）。
 * 注意不走 withPlugin 的吞错路径——调用方需要区分"取消/失败"。
 */
export async function getBiometricCredential(
  reason = '使用指纹登录',
): Promise<{ username: string; password: string }> {
  await ensureLoaded()
  if (!_plugin) throw new Error('biometric not supported on this platform')
  return _plugin.getCredential({ reason })
}

/** 解绑（尽力而为）。 */
export async function unbindBiometricCredential(): Promise<void> {
  await withPlugin(async p => {
    await p.deleteCredential()
  }, undefined)
}

/** 本机是否已保存主密码密文（无插件/失败返回 false）。 */
export function hasMasterSecret(): Promise<boolean> {
  return withPlugin(async p => !!(await p.hasMasterSecret()).has, false)
}

/** 密码解锁成功后静默写入主密码密文（尽力而为）。 */
export function persistMasterSecret(password: string): Promise<boolean> {
  if (!password) return Promise.resolve(false)
  return withPlugin(
    async p => {
      await p.saveMasterSecret({ password })
      return true
    },
    false,
  )
}

/**
 * 已绑定登录生物认证时，把刚验证过的主密码写入本机密文。
 * 失败不抛：下次密码解锁再试。
 */
export async function persistMasterSecretIfBound(password: string): Promise<void> {
  try {
    if (await isBiometricAvailable() && await hasBiometricCredential()) {
      await persistMasterSecret(password)
    }
  } catch {
    /* 下次密码解锁再试 */
  }
}

/**
 * 指纹/人脸通过后取回主密码；失败抛原始错误（取消/未写入需由调用方处理）。
 */
export async function getMasterSecret(
  reason = '使用指纹或人脸解锁',
): Promise<{ password: string }> {
  await ensureLoaded()
  if (!_plugin) throw new Error('biometric not supported on this platform')
  return _plugin.getMasterSecret({ reason })
}

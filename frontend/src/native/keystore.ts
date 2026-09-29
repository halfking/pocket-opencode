/**
 * cap-keystore plugin TypeScript bindings.
 *
 * This file declares the interface the Android Capacitor plugin must
 * implement. The native side (Kotlin) lives in the android module and is
 * registered via Capacitor's @CapacitorPlugin. On the web (PWA / browser
 * dev), these calls throw because there is no Keystore — the UI gates the
 * vault feature on isVaultInitialized() availability.
 *
 * To register the plugin after implementing it natively:
 *   import { registerPlugin } from '@capacitor/core'
 *   export const keystore = registerPlugin<CapKeystorePlugin>('Keystore')
 *
 * For now we use a stub that throws until the native plugin is built, so
 * the rest of the app compiles and the feature degrades gracefully.
 */

export interface VaultEntryMeta {
  id: string
  title: string
  category?: string
  username?: string
  url?: string
  updatedAt: string
}

export interface VaultEntry extends VaultEntryMeta {
  password: string
  notes?: string
  totpSecret?: string
  customFields?: { key: string; value: string }[]
  createdAt: string
}

export interface StrengthResult {
  score: 0 | 1 | 2 | 3 | 4
  feedback: string
}

export interface CapKeystorePlugin {
  isVaultInitialized(): Promise<boolean>
  setupMasterPassword(password: string): Promise<void>
  unlockWithBiometric(): Promise<void>
  unlockWithPassword(password: string): Promise<boolean>
  lock(): Promise<void>
  listEntries(): Promise<VaultEntryMeta[]>
  getEntry(id: string): Promise<VaultEntry>
  saveEntry(entry: Partial<VaultEntry> & { title: string }): Promise<string>
  deleteEntry(id: string): Promise<void>
  generatePassword(opts: {
    length: number
    upper?: boolean
    lower?: boolean
    digits?: boolean
    symbols?: boolean
  }): Promise<string>
  evaluateStrength(password: string): Promise<StrengthResult>
}

class StubKeystore implements CapKeystorePlugin {
  private unsupported = () =>
    Promise.reject(new Error('cap-keystore plugin not available on this platform'))

  isVaultInitialized = () => this.unsupported()
  setupMasterPassword = () => this.unsupported()
  unlockWithBiometric = () => this.unsupported()
  unlockWithPassword = () => this.unsupported()
  lock = () => this.unsupported()
  listEntries = () => this.unsupported()
  getEntry = () => this.unsupported()
  saveEntry = () => this.unsupported()
  deleteEntry = () => this.unsupported()
  generatePassword = () => this.unsupported()
  evaluateStrength = () => this.unsupported()
}

// Lazy: try to register the native plugin, fall back to a stub.
//
// BUG-G (2026-09-30)：Capacitor 的 registerPlugin() 返回的是**带 .then 的
// thenable 代理**。若把该代理从 async 函数直接 return（或作为 .then() 回调的
// 返回值），JS 的 promise 决议会去调它的 .then()，而未实现的原生插件会抛
//   Error: "Keystore.then()" is not implemented on android
// 于是：
//   1) 抛出未捕获异常，掩盖真实原因；
//   2) 下面的 StubKeystore 永远不会被启用（registerPlugin 不抛异常，只返回
//      代理），"优雅降级"的设计意图形同虚设。
// 修法：一律用非 thenable 的盒子 { value } 装载，async 函数只返回盒子。
type KeystoreBox = { value: CapKeystorePlugin }

let _impl: KeystoreBox | null = null
async function load(): Promise<KeystoreBox> {
  if (_impl) return _impl
  let impl: CapKeystorePlugin
  try {
    const cap = await import('@capacitor/core')
    impl = (cap.registerPlugin as <T>(name: string) => T)('Keystore') as CapKeystorePlugin
  } catch {
    impl = new StubKeystore()
  }
  _impl = { value: impl }
  return _impl
}

/** Public facade used by api/vault.ts. Each method loads lazily. */
export const keystore: CapKeystorePlugin = new Proxy(
  {} as CapKeystorePlugin,
  {
    get(_t, prop: keyof CapKeystorePlugin) {
      // 注意 box.value 而非 box：代理本身是 thenable，直接透传会再次触发 .then()
      return (...args: unknown[]) =>
        load().then((box) => (box.value[prop] as Function)(...args))
    },
  },
)

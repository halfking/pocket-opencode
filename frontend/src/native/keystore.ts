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
let _impl: CapKeystorePlugin | null = null
async function load(): Promise<CapKeystorePlugin> {
  if (_impl) return _impl
  try {
    const cap = await import('@capacitor/core')
    _impl = (cap.registerPlugin as <T>(name: string) => T)('Keystore') as CapKeystorePlugin
  } catch {
    _impl = new StubKeystore()
  }
  return _impl!
}

/** Public facade used by api/vault.ts. Each method loads lazily. */
export const keystore: CapKeystorePlugin = new Proxy(
  {} as CapKeystorePlugin,
  {
    get(_t, prop: keyof CapKeystorePlugin) {
      return (...args: unknown[]) =>
        load().then((impl) => (impl[prop] as Function)(...args))
    },
  },
)

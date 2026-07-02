/**
 * Password vault API — talks to the cap-keystore Capacitor plugin for local
 * crypto and to pocketd only for encrypted-sync transport. The server never
 * sees plaintext. See docs/2026-07-02-password-vault-design.md.
 *
 * NOTE: all decrypt happens in the native plugin; this module is a thin
 * facade. When the native plugin is absent (web/PWA), methods throw so the
 * UI can gate the vault feature.
 */
import type {
  VaultEntry,
  VaultEntryMeta,
} from '../native/keystore'

export type VaultCategory = 'login' | 'card' | 'note' | 'identity'

export interface PasswordStrength {
  score: 0 | 1 | 2 | 3 | 4
  feedback: string
}

export interface GenerateOptions {
  length: number
  upper?: boolean
  lower?: boolean
  digits?: boolean
  symbols?: boolean
}

export const vaultApi = {
  // Delegated to the native plugin; imported lazily so the module loads on web.
  async isInitialized(): Promise<boolean> {
    const { keystore } = await import('../native/keystore')
    return keystore.isVaultInitialized()
  },
  async setupMasterPassword(password: string): Promise<void> {
    const { keystore } = await import('../native/keystore')
    return keystore.setupMasterPassword(password)
  },
  async unlockWithBiometric(): Promise<void> {
    const { keystore } = await import('../native/keystore')
    return keystore.unlockWithBiometric()
  },
  async unlockWithPassword(password: string): Promise<boolean> {
    const { keystore } = await import('../native/keystore')
    return keystore.unlockWithPassword(password)
  },
  async lock(): Promise<void> {
    const { keystore } = await import('../native/keystore')
    return keystore.lock()
  },
  async listEntries(): Promise<VaultEntryMeta[]> {
    const { keystore } = await import('../native/keystore')
    return keystore.listEntries()
  },
  async getEntry(id: string): Promise<VaultEntry> {
    const { keystore } = await import('../native/keystore')
    return keystore.getEntry(id)
  },
  async saveEntry(entry: Omit<VaultEntry, 'id' | 'createdAt' | 'updatedAt'> & { id?: string }): Promise<string> {
    const { keystore } = await import('../native/keystore')
    return keystore.saveEntry(entry)
  },
  async deleteEntry(id: string): Promise<void> {
    const { keystore } = await import('../native/keystore')
    return keystore.deleteEntry(id)
  },
  async generatePassword(opts: GenerateOptions): Promise<string> {
    const { keystore } = await import('../native/keystore')
    return keystore.generatePassword(opts)
  },
  async evaluateStrength(password: string): Promise<PasswordStrength> {
    const { keystore } = await import('../native/keystore')
    const r = await keystore.evaluateStrength(password)
    return { score: r.score, feedback: r.feedback }
  },
}

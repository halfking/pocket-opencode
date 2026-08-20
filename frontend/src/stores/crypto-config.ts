import { defineStore } from 'pinia'
import { ref } from 'vue'

export type FieldEncryptionMode = 'disabled' | 'enabled'

export interface CryptoConfig {
  fieldEncryption: FieldEncryptionMode
  hasMasterPassword: boolean
  passwordHint: string | null
  updatedAt: number
}

const STORAGE_KEY = 'pocket_crypto_cfg'

const DEFAULT_CONFIG: CryptoConfig = {
  fieldEncryption: 'disabled',
  hasMasterPassword: false,
  passwordHint: null,
  updatedAt: 0,
}

function loadConfig(): CryptoConfig {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { ...DEFAULT_CONFIG }
    return { ...DEFAULT_CONFIG, ...JSON.parse(raw) }
  } catch {
    return { ...DEFAULT_CONFIG }
  }
}

export const useCryptoConfig = defineStore('crypto-config', () => {
  const cfg = ref<CryptoConfig>(loadConfig())

  function save() {
    cfg.value.updatedAt = Date.now()
    localStorage.setItem(STORAGE_KEY, JSON.stringify(cfg.value))
  }

  function setFieldEncryption(mode: FieldEncryptionMode) {
    cfg.value.fieldEncryption = mode
    save()
  }

  function setMasterPassword(hint: string | null = null) {
    cfg.value.hasMasterPassword = true
    cfg.value.passwordHint = hint
    save()
  }

  function resetMasterPassword() {
    cfg.value.hasMasterPassword = false
    cfg.value.passwordHint = null
    save()
  }

  function shouldEncryptField(): boolean {
    return cfg.value.fieldEncryption === 'enabled'
  }

  return {
    cfg,
    setFieldEncryption,
    setMasterPassword,
    resetMasterPassword,
    shouldEncryptField,
  }
})

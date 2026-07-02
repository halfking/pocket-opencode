/**
 * crypto.ts — 🦞 共享加密：主密码派生的 AES-GCM key
 *
 * 所有需要加密的本地 store（vault、email 凭证）共用一个由用户主密码
 * 派生的 AES-GCM key。App 启动时调 initAppCrypto(masterPassword) 初始化。
 */
let cryptoKey: CryptoKey | null = null

export function isCryptoReady(): boolean {
  return cryptoKey !== null
}

/** 用主密码初始化 AES-GCM key（PBKDF2 派生）。App 启动时调一次。 */
export async function initAppCrypto(masterPassword: string): Promise<void> {
  const enc = new TextEncoder()
  const keyMaterial = await crypto.subtle.importKey(
    'raw', enc.encode(masterPassword), 'PBKDF2', false, ['deriveKey'],
  )
  cryptoKey = await crypto.subtle.deriveKey(
    { name: 'PBKDF2', salt: enc.encode('lobster-vault-salt'), iterations: 100000, hash: 'SHA-256' },
    keyMaterial,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt', 'decrypt'],
  )
}

/** 获取共享 key。未初始化时抛错。 */
export function getCryptoKey(): CryptoKey {
  if (!cryptoKey) throw new Error('crypto 未初始化，请先调用 initAppCrypto()')
  return cryptoKey
}

/** AES-GCM 加密，返回 base64（iv 前缀 + 密文）。 */
export async function encryptString(plain: string): Promise<string> {
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const cipher = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv }, getCryptoKey(), new TextEncoder().encode(plain),
  )
  const combined = new Uint8Array(iv.length + cipher.byteLength)
  combined.set(iv, 0)
  combined.set(new Uint8Array(cipher), iv.length)
  return btoa(String.fromCharCode(...combined))
}

/** AES-GCM 解密 base64（iv 前缀 + 密文）。 */
export async function decryptString(b64: string): Promise<string> {
  const combined = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
  const iv = combined.slice(0, 12)
  const cipher = combined.slice(12)
  const plain = await crypto.subtle.decrypt({ name: 'AES-GCM', iv }, getCryptoKey(), cipher)
  return new TextDecoder().decode(plain)
}

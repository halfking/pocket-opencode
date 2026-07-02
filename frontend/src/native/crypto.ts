/**
 * crypto.ts — 🦞 共享加密：主密码派生的 AES-GCM key
 *
 * 所有需要加密的本地 store（vault、email 凭证）共用一个由用户主密码
 * 派生的 AES-GCM key。App 启动时调 initAppCrypto(masterPassword) 初始化。
 *
 * 安全改进（第六轮审计）：
 * - 随机 salt 替代静态 salt，每个用户/设备独立，防止彩虹表攻击
 * - salt 存储在 localStorage（不敏感，可明文存），首次初始化时生成
 */
let cryptoKey: CryptoKey | null = null

const SALT_STORAGE_KEY = 'pocket_crypto_salt'

/**
 * 获取或生成密钥派生 salt。
 * 首次调用时生成 16 字节随机 salt 并存储到 localStorage（base64 编码）。
 * 后续调用从 localStorage 读取。
 */
function getOrGenerateSalt(): Uint8Array {
  const stored = localStorage.getItem(SALT_STORAGE_KEY)
  if (stored) {
    // 从 base64 解码
    const binaryString = atob(stored)
    const bytes = new Uint8Array(binaryString.length)
    for (let i = 0; i < binaryString.length; i++) {
      bytes[i] = binaryString.charCodeAt(i)
    }
    return bytes
  }

  // 首次使用：生成 16 字节随机 salt
  const salt = crypto.getRandomValues(new Uint8Array(16))
  
  // 保存到 localStorage（base64 编码）
  const binaryString = String.fromCharCode(...salt)
  localStorage.setItem(SALT_STORAGE_KEY, btoa(binaryString))
  
  return salt
}

export function isCryptoReady(): boolean {
  return cryptoKey !== null
}

/** 用主密码初始化 AES-GCM key（PBKDF2 派生 + 随机 salt）。App 启动时调一次。 */
export async function initAppCrypto(masterPassword: string): Promise<void> {
  const enc = new TextEncoder()
  const keyMaterial = await crypto.subtle.importKey(
    'raw', enc.encode(masterPassword), 'PBKDF2', false, ['deriveKey'],
  )
  
  // 使用随机 salt（每个用户/设备独立）
  const salt = getOrGenerateSalt()
  
  cryptoKey = await crypto.subtle.deriveKey(
    { name: 'PBKDF2', salt: salt as BufferSource, iterations: 100000, hash: 'SHA-256' },
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

/**
 * 清除内存中的共享 AES-GCM key。
 *
 * 用于 lockLobster()：锁定后 cryptoKey 被置 null，后续任何
 * encryptString/decryptString 调用都会因 getCryptoKey() 抛错而失败，
 * 防止锁定状态下仍然能解密 vault/email 凭证。页面刷新同样会清除
 * （Web Crypto API key 不持久化），此函数提供主动清除能力。
 */
export function resetCryptoKey(): void {
  cryptoKey = null
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

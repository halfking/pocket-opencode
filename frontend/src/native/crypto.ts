/**
 * crypto.ts — 🦞 共享加密：主密码派生的 AES-GCM key
 *
 * 所有需要加密的本地 store（vault、email 凭证）共用一个由用户主密码
 * 派生的 AES-GCM key。App 启动时调 initAppCrypto(masterPassword) 初始化。
 *
 * 安全改进（第六轮审计）：
 * - 随机 salt 替代静态 salt，每个用户/设备独立，防止彩虹表攻击
 * - salt 存储在 localStorage（不敏感，可明文存），首次初始化时生成
 *
 * 向后兼容（第七轮审计修复）：
 * - 自动检测旧静态 salt 加密的数据，尝试用旧 key 解密
 * - 成功解密后控制台警告，提示用户可选迁移
 */
let cryptoKey: CryptoKey | null = null
let legacyCryptoKey: CryptoKey | null = null  // 用于解密旧数据

const SALT_STORAGE_KEY = 'pocket_crypto_salt'
const LEGACY_SALT = 'lobster-vault-salt'  // 第六轮之前使用的静态 salt

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
  
  // 同时派生旧 key（用于向后兼容旧数据解密）
  legacyCryptoKey = await crypto.subtle.deriveKey(
    { name: 'PBKDF2', salt: enc.encode(LEGACY_SALT), iterations: 100000, hash: 'SHA-256' },
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
  legacyCryptoKey = null
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

/** 
 * AES-GCM 解密 base64（iv 前缀 + 密文）。
 * 
 * 向后兼容：先尝试用新 salt 派生的 key 解密，失败后尝试旧静态 salt 的 key。
 * 如果检测到旧数据，会在控制台输出警告（便于追踪迁移进度）。
 */
export async function decryptString(b64: string): Promise<string> {
  const combined = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
  const iv = combined.slice(0, 12)
  const cipher = combined.slice(12)
  
  try {
    // 先尝试用新 key 解密（快速路径）
    const plain = await crypto.subtle.decrypt({ name: 'AES-GCM', iv }, getCryptoKey(), cipher)
    return new TextDecoder().decode(plain)
  } catch (newKeyError) {
    // 新 key 失败，尝试用旧静态 salt 派生的 key
    if (!legacyCryptoKey) {
      throw newKeyError  // 没有旧 key，直接抛原始错误
    }
    
    try {
      const plain = await crypto.subtle.decrypt({ name: 'AES-GCM', iv }, legacyCryptoKey, cipher)
      
      // 成功用旧 key 解密，输出警告
      console.warn(
        '[crypto] 检测到使用旧静态 salt 加密的数据。' +
        '数据仍可正常使用，但建议重新加密以提高安全性。' +
        '（此警告每次解密旧数据时出现，可忽略）'
      )
      
      return new TextDecoder().decode(plain)
    } catch (legacyKeyError) {
      // 两种 key 都失败，抛出原始错误（新 key 的错误更相关）
      throw newKeyError
    }
  }
}

/**
 * BiometricPrompt 错误码（androidx.biometric.BiometricPrompt）。
 * 设置页用来区分：用户取消、未录入（才去系统设置）、其它失败。
 */
export function isBiometricUserCancel(message: string): boolean {
  return /\bbiometric error (5|10|13)\b/.test(message)
}

/** 硬件不可用 / 未录入指纹或人脸，只能去系统设置录入。 */
export function needsBiometricEnrollment(message: string): boolean {
  return /\bbiometric error (1|11|12)\b/.test(message)
    || /not supported on this platform/.test(message)
}

/** 已绑定登录指纹，但本机还没有主密码密文（升级路径）。 */
export function isMissingMasterSecret(message: string): boolean {
  return /no master secret/.test(message)
}

export function loginDisplayName(raw: string): string {
  const text = raw.trim()
  if (!text) return ''
  if (text.startsWith('{')) {
    try {
      const username = JSON.parse(text)?.username
      if (typeof username === 'string' && username.trim()) return username.trim()
    } catch {
      /* 不是 JSON，当作用户名原文 */
    }
  }
  return text
}

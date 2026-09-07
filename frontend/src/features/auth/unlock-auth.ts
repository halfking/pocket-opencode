/**
 * 主密码解锁屏策略：已绑定生物认证时允许空密码点认证。
 * 密码优先；取消系统弹窗后仍可输入主密码。
 */
export type UnlockSubmitMode = 'biometric' | 'password' | 'need-password'

export function unlockSubmitMode(input: {
  password: string
  biometricBound: boolean
}): UnlockSubmitMode {
  if (input.password.trim()) return 'password'
  if (input.biometricBound) return 'biometric'
  return 'need-password'
}

export function unlockButtonEnabled(input: {
  password: string
  biometricBound: boolean
  loading: boolean
}): boolean {
  if (input.loading) return false
  return unlockSubmitMode(input) !== 'need-password'
}

export function unlockButtonLabel(input: {
  loading: boolean
  biometricBound: boolean
  password: string
}): string {
  if (input.loading) return '认证中...'
  if (input.biometricBound && !input.password.trim()) return '认证'
  return '解锁'
}

export function unlockHint(biometricBound: boolean): string {
  if (biometricBound) {
    return '已绑定指纹或人脸。可直接点认证，也可取消后输入主密码。'
  }
  return '检测到已有登录态，但本地加密库未解锁。请重新输入主密码以访问本地数据。'
}

export function unlockPasswordPlaceholder(biometricBound: boolean): string {
  return biometricBound ? '可留空，点认证使用指纹或人脸' : '输入主密码解锁'
}

/**
 * 运行时权限下一步动作。
 *
 * Android 语义：
 *   - prompt / prompt-with-rationale：还可以再弹系统申请窗
 *   - denied：用户勾了「不再询问」或二次拒绝，只能去系统设置
 * 禁止后必须还能「重新申请」，不能第一次拒绝就锁死或强跳设置。
 */
export type PermissionStatus = 'granted' | 'denied' | 'prompt' | 'prompt-with-rationale' | 'unavailable'

export type PermissionAction = 'none' | 'request' | 'open-settings'

export function canRequestPermissionAgain(status: PermissionStatus): boolean {
  return status === 'prompt' || status === 'prompt-with-rationale'
}

export function nextPermissionAction(status: PermissionStatus): PermissionAction {
  if (status === 'granted' || status === 'unavailable') return 'none'
  if (canRequestPermissionAgain(status)) return 'request'
  return 'open-settings'
}

/**
 * useBiometricStatus — 生物识别（指纹/人脸）功能状态。
 *
 * Android 上指纹和人脸共用一个系统能力（BiometricPrompt / WebAuthn platform authenticator），
 * 不构成独立的运行时权限，因此这里不是"申请权限"，而是查询"已注册多少生物凭据"。
 *
 * 数据源：GET /api/auth/biometric/credentials（后端在 server_biometric.go 已实现）。
 * 失败时静默归零，不阻塞渲染。
 */
import { ref } from 'vue'
import { api } from '../api/client'

export type BiometricAvailability =
  | 'unknown'        // 尚未查询
  | 'loading'        // 查询中
  | 'ready'          // 已认证，credentials 可读
  | 'unauthenticated'// 未登录 / token 过期，服务器 401
  | 'unavailable'    // 网络 / 服务器错误，不可用

const availability = ref<BiometricAvailability>('unknown')
const credentialCount = ref(0)
const lastError = ref('')

export function useBiometricStatus() {
  /** 从服务器拉取已注册凭据数量。 */
  async function refresh(): Promise<void> {
    availability.value = 'loading'
    lastError.value = ''
    try {
      const list = await api.listBiometricCredentials()
      credentialCount.value = Array.isArray(list) ? list.length : 0
      availability.value = 'ready'
    } catch (err: any) {
      const status = err?.status ?? err?.statusCode ?? 0
      const msg = err?.message || String(err)
      // 401 视为未登录，可用但无凭据可读
      if (status === 401 || /401|unauthorized|未登录/i.test(msg)) {
        credentialCount.value = 0
        availability.value = 'unauthenticated'
        lastError.value = ''
      } else {
        credentialCount.value = 0
        availability.value = 'unavailable'
        lastError.value = msg
      }
    }
  }

  return { availability, credentialCount, lastError, refresh }
}

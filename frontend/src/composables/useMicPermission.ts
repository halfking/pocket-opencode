/**
 * useMicPermission — 麦克风权限探测与申请（会议/语音输入共用）。
 *
 * 真机上 WebView 的 getUserMedia 需要 app 已持有 RECORD_AUDIO 运行时权限。
 * MainActivity 已在启动时主动请求该权限；这里在录音前做一次"可用性探测"，
 * 把失败归因为可读的状态，供 UI 给出"去设置"等引导。
 *
 * 用法：
 *   const mic = useMicPermission()
 *   const ok = await mic.ensure()
 *   if (!ok) { 提示 mic.deniedLabel.value / 引导去设置 }
 */
import { ref } from 'vue'

export type MicState = 'unknown' | 'granted' | 'denied' | 'unavailable'

const state = ref<MicState>('unknown')

/** 可读的状态文案 */
const deniedLabel = ref('')

/** 探测麦克风权限：调用 getUserMedia 并立即释放流，仅用于触发/检测授权。 */
async function probe(): Promise<boolean> {
  if (!navigator.mediaDevices || typeof navigator.mediaDevices.getUserMedia !== 'function') {
    state.value = 'unavailable'
    deniedLabel.value = '当前环境不支持录音（getUserMedia 不可用）'
    return false
  }
  try {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    // 立即释放，不占用麦克风
    stream.getTracks().forEach((t) => t.stop())
    state.value = 'granted'
    deniedLabel.value = ''
    return true
  } catch (e: any) {
    const name = e?.name || ''
    if (name === 'NotAllowedError' || name === 'SecurityError') {
      state.value = 'denied'
      deniedLabel.value = '麦克风权限被拒绝，请在系统设置中授权后重试'
    } else if (name === 'NotFoundError' || name === 'OverconstrainedError') {
      state.value = 'unavailable'
      deniedLabel.value = '未找到可用的麦克风设备'
    } else {
      state.value = 'unavailable'
      deniedLabel.value = '麦克风不可用：' + (e?.message || name || '未知错误')
    }
    return false
  }
}

export function useMicPermission() {
  /** 确保已授权；未授权时尝试触发一次申请并返回结果。 */
  async function ensure(): Promise<boolean> {
    if (state.value === 'granted') return true
    return probe()
  }

  /** 重新探测（用户从设置回来后调用）。 */
  async function recheck(): Promise<boolean> {
    return probe()
  }

  return {
    state,
    deniedLabel,
    ensure,
    recheck,
  }
}

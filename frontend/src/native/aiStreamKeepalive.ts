/**
 * aiStreamKeepalive.ts — AI 流后台保活的前端桥（M5/T2，2026-09-10）。
 *
 * 设计动机（详见 docs/design/2026-09-09-ai-async-background-survival.md §D5）：
 * AiStreamRuntime 把流所有权上移到进程级后，"切走不中断"在 Android 上还差最后
 * 一环——WebView 进程切后台后会被 Doze/低功耗调度冻结网络栈。本模块在
 * 「runtime 有活跃流 && App 已切后台」时拉起原生 AiStreamService（dataSync 型
 * 前台服务 + ai_stream_sync 通知），回前台或流结束后停掉。
 *
 * 决策表（syncKeepalive）：
 *   activeCount > 0 && hub.isHidden()  → start（幂等：同方向命令不重发）
 *   其余情况                            → stop
 *   start 保持中但 activeCount 变化     → update（刷新通知文案）
 *
 * 平台策略：仅 Android Capacitor 注入真实桥；Web / iOS 检测后置 null，全部 no-op
 * （iOS 走 BackgroundModes + silent push，见 T1/T4）。测试用 setKeepaliveBridge /
 * setKeepaliveStatsProvider 注入假实现，不碰真实网络与原生层。
 */

import { appLifecycleHub, type LifecycleEvent } from './appLifecycleHub.ts'
import { aiStreamRuntime } from './aiStreamRuntime.ts'

export interface KeepaliveState {
  running: boolean
  /** Android 13+ 通知权限；<13 恒 true。被拒时服务照起、仅通知不显示。 */
  permGranted?: boolean
}

/** 原生桥面（registerPlugin 注入）；与 AiStreamKeepalivePlugin.java 一一对应。 */
export interface KeepaliveBridge {
  start(opts: { activeCount: number; text?: string }): Promise<KeepaliveState>
  stop(): Promise<KeepaliveState>
  update?(opts: { activeCount: number; text?: string }): Promise<KeepaliveState>
  isRunning?(): Promise<KeepaliveState>
}

/** 测试注入口：undefined = 尚未探测（首次调用时按平台探测）。 */
let _bridge: KeepaliveBridge | null | undefined

export function setKeepaliveBridge(bridge: KeepaliveBridge | null): void {
  _bridge = bridge
}

/** 测试注入口：覆盖 aiStreamRuntime.getStats()（测试里不起真流）。 */
let _statsProvider: () => { activeCount: number } = () => aiStreamRuntime.getStats()

export function setKeepaliveStatsProvider(provider: () => { activeCount: number }): void {
  _statsProvider = provider
}

/**
 * 桥初始化：只写模块级 _bridge，**绝不把插件 proxy 当返回值**。
 * ⚠️ thenable 陷阱（2026-09-10 Android 实测）：Capacitor 插件 proxy 的 get trap
 * 对未知属性（含 .then）reject「not implemented」；async 函数返回 proxy 后，
 * 调用方 await 时 Promise 解约会做 thenable 检查 → 必然触发。所以本函数返回
 * void，调用方直接读 _bridge。
 */
async function initBridge(): Promise<void> {
  if (_bridge !== undefined) return
  try {
    const core = await import('@capacitor/core')
    if (core.Capacitor.getPlatform() !== 'android') {
      _bridge = null
      return
    }
    _bridge = core.registerPlugin<KeepaliveBridge>('AiStreamKeepalive')
  } catch {
    // Web / Node 测试环境：无 @capacitor/core 或无原生桥
    _bridge = null
  }
}

let started = false
let unsubLifecycle: (() => void) | null = null
let syncTimer: ReturnType<typeof setInterval> | null = null
/** 上一次成功下发的命令；start/stop 幂等去重用。 */
let lastCommand: 'start' | 'stop' | null = null
/** 上一次下发时的活跃流数；变更时触发 update 刷新通知。 */
let lastSyncedActive = 0

/** 主入口：main.ts 调一次。之后 lifecycle 事件 + 30s 心跳自动同步。 */
export function startAiStreamKeepalive(): void {
  if (started) return
  started = true
  unsubLifecycle = appLifecycleHub.on((evt) => {
    void syncKeepalive(evt)
  })
  // 心跳兜底：hidden 期间流自然结束（activeCount→0）没有事件可听，靠心跳停服。
  syncTimer = setInterval(() => {
    void syncKeepalive()
  }, 30_000)
}

export function stopAiStreamKeepalive(): void {
  if (!started) return
  unsubLifecycle?.()
  unsubLifecycle = null
  if (syncTimer !== null) {
    clearInterval(syncTimer)
    syncTimer = null
  }
  started = false
}

/** 核心决策：见文件头决策表。evt 仅用于日志语境，决策只看 stats + hub。 */
export async function syncKeepalive(_evt?: LifecycleEvent): Promise<void> {
  await initBridge()
  const bridge = _bridge ?? null
  if (!bridge) return
  const { activeCount } = _statsProvider()
  const wantUp = activeCount > 0 && appLifecycleHub.isHidden()

  if (wantUp) {
    if (lastCommand === 'start') {
      // 仍在保活：活跃数变了才 update（刷新通知文案），否则 no-op。
      if (activeCount !== lastSyncedActive && bridge.update) {
        try {
          await bridge.update({ activeCount })
          lastSyncedActive = activeCount
        } catch (err) {
          // eslint-disable-next-line no-console
          console.error('[aiStreamKeepalive] update failed', err)
        }
      }
      return
    }
    try {
      const res = await bridge.start({ activeCount })
      lastCommand = 'start'
      lastSyncedActive = activeCount
      if (res && res.permGranted === false) {
        // eslint-disable-next-line no-console
        console.warn('[aiStreamKeepalive] 通知权限被拒：前台服务已起，但常驻通知不可见')
      }
    } catch (err) {
      // 失败不置 lastCommand：下个心跳重试
      // eslint-disable-next-line no-console
      console.error('[aiStreamKeepalive] start failed', err)
    }
    return
  }

  if (lastCommand === 'stop') return
  try {
    await bridge.stop()
    lastCommand = 'stop'
    lastSyncedActive = 0
  } catch (err) {
    // eslint-disable-next-line no-console
    console.error('[aiStreamKeepalive] stop failed', err)
  }
}

/** 测试用：重置模块级去重状态（bridge / stats provider 不动）。 */
export function resetKeepaliveForTest(): void {
  lastCommand = null
  lastSyncedActive = 0
}

/**
 * appLifecycleHub.ts — 把 DOM/Capacitor 的生命周期事件收口为 4 个语义事件。
 *
 * 设计动机（2026-09-09）：原本每个 AI 流自己监听 visibilitychange / appStateChange，
 * 且对 "hidden" 的语义各取所需——有的暂停轮询、有的 abort 流、有的忽略。本模块做
 * 唯一权威解释，下游（aiStreamRuntime 等）只关心语义，不关心 DOM 还是 Capacitor。
 *
 * 语义事件：
 *   - 'hidden'  切到后台（visibilitychange→hidden，或 Capacitor App isActive=false）
 *   - 'visible' 切回前台（visibilitychange→visible，或 Capacitor App isActive=true）
 *   - 'frozen'  页面进入冻结（PWA 桌面 / iOS Safari 'freeze' 事件）
 *   - 'resumed' 页面从冻结恢复（PWA 桌面 / iOS Safari 'resume' 事件）
 *
 * 单元测试可注入 platform（setPlatform）模拟 DOM/Capacitor；Node 测试场景下
 * 不 start() 即可，所有派发器都 no-op。
 */

export type LifecycleEvent = 'hidden' | 'visible' | 'frozen' | 'resumed'

type Listener = (event: LifecycleEvent) => void

export interface LifecyclePlatform {
  /** 注入 document；测试场景下可替换为 null。 */
  document: Document | null
  /** 注入 window；测试场景下可替换为 null。 */
  window: Window | null
  /** 注入 Capacitor App 加载器；测试场景下永远 reject 即跳过原生通道。 */
  loadCapacitorApp(): Promise<{ addListener(name: 'appStateChange', cb: (s: { isActive: boolean }) => void): Promise<{ remove(): Promise<void> }> }>
}

/** 单例：main.ts 启动一次，runtime 全局订阅。 */
class AppLifecycleHub {
  private listeners: Listener[] = []
  private started = false
  private platform: LifecyclePlatform | null = null
  private capacitorSub: { remove(): Promise<void> } | null = null
  private lastState: LifecycleEvent = 'visible'
  private frozenState = false

  /** 测试/特殊场景下注入平台实现；未注入时使用真实 DOM + Capacitor 动态加载。 */
  setPlatform(platform: LifecyclePlatform): void {
    if (this.started) {
      throw new Error('AppLifecycleHub.setPlatform must be called before start()')
    }
    this.platform = platform
  }

  start(): void {
    if (this.started) return
    this.started = true

    const platform = this.platform ?? this.detectPlatform()

    if (platform.window) {
      const w = platform.window
      const onVis = () => {
        if (platform.document?.visibilityState === 'hidden') {
          this.emit('hidden')
        } else if (platform.document?.visibilityState === 'visible') {
          this.emit('visible')
        }
      }
      w.addEventListener('visibilitychange', onVis)
      this.cleanupFns.push(() => w.removeEventListener('visibilitychange', onVis))

      // PWA 桌面 / iOS Safari 在长时间后台时派发 freeze；resume 是解冻。
      const onFreeze = () => {
        this.frozenState = true
        this.emit('frozen')
      }
      const onResume = () => {
        this.frozenState = false
        this.emit('resumed')
      }
      w.addEventListener('freeze', onFreeze)
      w.addEventListener('resume', onResume)
      this.cleanupFns.push(() => w.removeEventListener('freeze', onFreeze))
      this.cleanupFns.push(() => w.removeEventListener('resume', onResume))

      // pagehide 视作 hidden（兜底：部分浏览器/系统 visibilitychange 不一定触发）。
      const onPageHide = () => this.emit('hidden')
      w.addEventListener('pagehide', onPageHide)
      this.cleanupFns.push(() => w.removeEventListener('pagehide', onPageHide))
    }

    void this.registerCapacitor(platform)
  }

  stop(): void {
    if (!this.started) return
    for (const off of this.cleanupFns) off()
    this.cleanupFns = []
    void this.capacitorSub?.remove()
    this.capacitorSub = null
    this.started = false
  }

  /** 订阅事件；返回反订阅函数。同一事件可被多次订阅。 */
  on(listener: Listener): () => void {
    this.listeners.push(listener)
    return () => {
      this.listeners = this.listeners.filter((l) => l !== listener)
    }
  }

  /** 当前状态查询（供运行时决策，例如 "流是否在后台跑着"）。 */
  isHidden(): boolean {
    return this.lastState === 'hidden' || this.lastState === 'frozen'
  }

  isFrozen(): boolean {
    return this.frozenState
  }

  /** 仅测试用：手动派发事件。frozen/resumed 同时更新 frozenState 标志。 */
  emit(event: LifecycleEvent): void {
    this.lastState = event
    if (event === 'frozen') this.frozenState = true
    else if (event === 'resumed') this.frozenState = false
    for (const l of this.listeners) {
      try {
        l(event)
      } catch (err) {
        // 单个订阅方异常不应阻断其他订阅方
        // eslint-disable-next-line no-console
        console.error('[appLifecycleHub] listener error', err)
      }
    }
  }

  private cleanupFns: Array<() => void> = []

  private detectPlatform(): LifecyclePlatform {
    return {
      // 全局类型上必有；测试场景通过 setPlatform 覆盖。
      document: typeof document !== 'undefined' ? document : null,
      window: typeof window !== 'undefined' ? window : null,
      loadCapacitorApp: async () => {
        const mod = await import('@capacitor/app')
        return mod.App
      },
    }
  }

  private async registerCapacitor(platform: LifecyclePlatform): Promise<void> {
    try {
      const App = await platform.loadCapacitorApp()
      const sub = await App.addListener('appStateChange', (state: { isActive: boolean }) => {
        this.emit(state.isActive ? 'visible' : 'hidden')
      })
      this.capacitorSub = sub
    } catch {
      // Web / 测试环境：忽略；visibilitychange 已经覆盖
    }
  }
}

/** 进程级单例；HMR 兼容（dev 模式下模块可能被重新执行）。 */
const HUB_KEY = '__openpocket_appLifecycleHub__'
type GlobalWithHub = typeof globalThis & { [HUB_KEY]?: AppLifecycleHub }
const g = globalThis as GlobalWithHub
export const appLifecycleHub: AppLifecycleHub = g[HUB_KEY] ?? (g[HUB_KEY] = new AppLifecycleHub())

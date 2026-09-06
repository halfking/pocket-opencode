/**
 * useElapsedNow.ts — 「距 X 的时长」文本的自适应节拍（ISSUES #20 周期性重渲染修复）。
 *
 * 背景：会话页头部的时长副标题（"运行中 · 42s"/"空闲 · 5 分钟"）此前由固定
 * 1s setInterval 驱动。formatStatusElapsed 的显示粒度只有两档——前 60 秒按秒
 * （"42s"），之后按分钟/小时——因此 1s tick 在会话绝大部分时间里是在做无用
 * 重渲染：空闲会话（无任何时长显示）也每秒重绘头部，构成周期性 DOM 重渲染
 * 源，移动端点击/自动化偶发落空且白耗电。
 *
 * 策略（elapsedTickInterval 纯函数）：
 *   - 任一基准在 60s 秒级显示窗口内 → 1s 节拍（文本真的每秒在变）；
 *   - 有基准但都过了秒级窗口 → 15s 节拍（分钟粒度文本，15s 跳一次足够）；
 *   - 没有任何有效基准（空闲会话）→ 完全不启定时器，零重渲染。
 *   - document.hidden 时暂停，回前台立即补跳一次（后台跳了也没人看）。
 *
 * 用法：
 *   const now = useElapsedNow(() => [props.lastEventAt, props.approvalFirstSeenAt])
 *   const text = computed(() => formatStatusElapsed(now.value - base))
 */
import { ref, watch, onMounted, onUnmounted, type Ref } from 'vue'

/** formatStatusElapsed 秒级显示的窗口宽度（<60s 显示 "42s"，之后分钟粒度）。 */
export const ELAPSED_FRESH_WINDOW_MS = 60_000

/**
 * 纯策略：给定一组时长基准（epoch ms；null/NaN = 无），返回当前需要的
 * 刷新周期 ms；不需要任何定时器时返回 null。
 */
export function elapsedTickInterval(
  bases: Array<number | null>,
  now: number,
  freshMs = 1_000,
  staleMs = 15_000,
): number | null {
  let hasFiniteBase = false
  for (const b of bases) {
    if (b === null || !Number.isFinite(b)) continue
    hasFiniteBase = true
    if (now - b < ELAPSED_FRESH_WINDOW_MS) return freshMs
  }
  return hasFiniteBase ? staleMs : null
}

export interface UseElapsedNowOptions {
  /** 秒级窗口内的刷新周期，默认 1000。 */
  freshMs?: number
  /** 超过秒级窗口后的刷新周期，默认 15000。 */
  staleMs?: number
}

/**
 * 自适应"当前时间"时钟。`basesAt` 返回时长基准数组（响应式读取在调用方闭包
 * 内完成）；基准变化、跨过秒级窗口边界、页面显隐时自动重调节拍。
 */
export function useElapsedNow(
  basesAt: () => Array<number | null>,
  opts: UseElapsedNowOptions = {},
): Ref<number> {
  const freshMs = opts.freshMs ?? 1_000
  const staleMs = opts.staleMs ?? 15_000

  const now = ref(Date.now())
  let timer: ReturnType<typeof setInterval> | null = null
  /** 当前生效的周期；与需求周期一致时不重建定时器（避免无谓的 clear/set）。 */
  let activeInterval: number | null = null

  function schedule(): void {
    const needed = elapsedTickInterval(basesAt(), Date.now(), freshMs, staleMs)
    if (needed === activeInterval) return
    if (timer !== null) {
      clearInterval(timer)
      timer = null
    }
    activeInterval = needed
    if (needed === null) return
    timer = setInterval(() => {
      now.value = Date.now()
      // 跨过 60s 边界后自动降频；基准消失时彻底停表
      schedule()
    }, needed)
  }

  function onVisible(): void {
    if (document.visibilityState !== 'visible') return
    now.value = Date.now()
    schedule()
  }
  function onHidden(): void {
    if (document.visibilityState !== 'hidden') return
    if (timer !== null) {
      clearInterval(timer)
      timer = null
    }
    activeInterval = null
  }

  watch(basesAt, () => schedule())

  onMounted(() => {
    schedule()
    document.addEventListener('visibilitychange', onVisible)
    document.addEventListener('visibilitychange', onHidden)
  })
  onUnmounted(() => {
    if (timer !== null) clearInterval(timer)
    timer = null
    activeInterval = null
    document.removeEventListener('visibilitychange', onVisible)
    document.removeEventListener('visibilitychange', onHidden)
  })

  return now
}

/**
 * useCountUp — 数字平滑插值
 *
 * 适用：从 0 / 上次数 动画变到目标值。requestAnimationFrame 驱动，按帧 tween；
 * 结束时使用 ease-out 让数字快到慢收敛，指数 easeOutCubic。
 *
 * 选项：
 *  - duration   ms 动画时长，默认 800ms
 *  - decimals   小数位数，默认 0（整数）
 *  - prefix / suffix 前缀后缀
 *  - easing     (t: number) => number 自定义，缺省 easeOutCubic
 *
 * 返回 { display, start, stop }：
 *   display 是 ref<string>，随时可直接喂到模板
 *   start(target) 重启动画；stop() 取消
 *
 * 注意：
 *   - 偏好运动用户（prefers-reduced-motion）会自动把 duration 设为 0
 *     直接跳到目标值，减少眩晕——这是被 OS WebView / Android Accessibility
 *     强烈建议打开的特性。
 *   - 组件 unmount 时会 onScopeDispose 收尾，不会泄漏 rAF。
 */
import { ref, onScopeDispose, type Ref } from 'vue'

export interface CountUpOptions {
  duration?: number
  decimals?: number
  prefix?: string
  suffix?: string
  easing?: (t: number) => number
}

export interface CountUpReturn {
  display: Ref<string>
  start: (target: number) => void
  stop: () => void
}

export function useCountUp(initial = 0, options: CountUpOptions = {}): CountUpReturn {
  const {
    duration = 800,
    decimals = 0,
    prefix = '',
    suffix = '',
    easing = easeOutCubic,
  } = options

  // 尊重系统级减少动画偏好
  const reduceMotion =
    typeof window !== 'undefined' &&
    window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  const dur = reduceMotion ? 0 : duration

  const display = ref<string>(format(initial, decimals, prefix, suffix))
  let raf = 0
  let startedAt = 0
  let from = initial
  let to = initial
  let active = false

  function format(v: number, dec: number, pre: string, suf: string) {
    if (!Number.isFinite(v)) return `${pre}0${suf}`
    // toFixed + 移除尾随 0 —— 但保留 decimals=0 时的整数语义
    const s = v.toFixed(dec)
    return `${pre}${s}${suf}`
  }

  function step(now: number) {
    if (!active) return
    const elapsed = now - startedAt
    const t = dur === 0 ? 1 : Math.min(1, elapsed / dur)
    const eased = easing(t)
    const value = from + (to - from) * eased
    display.value = format(value, decimals, prefix, suffix)
    if (t < 1) {
      raf = requestAnimationFrame(step)
    } else {
      active = false
      raf = 0
    }
  }

  function start(target: number) {
    cancelAnimationFrame(raf)
    from = parseFloat(display.value) || 0
    to = target
    startedAt = performance.now()
    active = true
    raf = requestAnimationFrame(step)
  }

  function stop() {
    cancelAnimationFrame(raf)
    raf = 0
    active = false
  }

  onScopeDispose(stop)

  return { display, start, stop }
}

export function easeOutCubic(t: number) {
  return 1 - Math.pow(1 - t, 3)
}

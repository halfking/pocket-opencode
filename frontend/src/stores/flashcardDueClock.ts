/**
 * flashcardDueClock.ts — 闪卡「到期」判据用的**响应式时钟**。
 *
 * BUG-AS（2026-10-01 静态取证，handoff §4.55）：
 *   `dueByDeck` / `deckSummaries` / `dueCardsForDeck` 三个 computed 都拿
 *   `nowSec()` 当到期判据，而原来的 `nowSec()` 读的是 `Date.now()`。
 *   Vue 的 computed **只在响应式依赖变化时重算**，`Date.now()` 不是任何 ref，
 *   同一文件里也没有任何 setInterval 推进时间。
 *   ⇒ 卡片在页面打开期间跨过到期时刻，到期数不重算，「开始复习」也一直置灰，
 *     直到别的依赖动了（增删改卡片 / refresh 整体替换数组 / 进出页面重新挂载）。
 *
 * 这里把时间本身变成响应式值：
 *   - `dueNowSec()` 返回 ref 的当前值 ⇒ 依赖它的 computed 会随 tick 推进重算；
 *   - `liveNowSec()` 直接读 `Date.now()`，给**记录时间戳**的场景用
 *     （enqueue 的 enqueuedAt、review 的 reviewedAt 等），
 *     避免它们被 tick 间隔拖成最多 30 秒的旧值。
 *
 * 两个可调参数集中在下面，行为取舍集中在一处：
 *   - `TICK_MS`：重算频率。30s 对「今天还剩几张要背」这个量级够用；
 *     改小会让所有依赖 dueNowSec 的 computed 更频繁重算（列表越长代价越高）。
 *   - 页面不可见（`document.hidden`）时**暂停** tick：省电，也避免后台无意义重算。
 *     回到前台会立刻补一次 tick，不用等下一个周期。
 *
 * 纯模块，无浏览器专属依赖（`document` 有则用、无则退化为不暂停），
 * 所以能被 `node --test` 直接 import 做单测。
 */
import { ref } from 'vue'

/** tick 间隔（毫秒）。到期数是「今天还剩几张」量级的信息，不需要秒级刷新。 */
export const TICK_MS = 30_000

/** 响应式的秒级时间戳。 */
const dueNowRef = ref(liveNowSec())

/** 真实时间（秒）。给记录时间戳用，不参与响应式。 */
export function liveNowSec(): number {
  return Math.floor(Date.now() / 1000)
}

/**
 * 到期判据用的秒级时间。**依赖它才能被响应式追踪**——
 * 这是 BUG-AS 的修复点：以前这里是 liveNowSec()，computed 永远等不到时间变化。
 */
export function dueNowSec(): number {
  return dueNowRef.value
}

let timer: ReturnType<typeof setInterval> | null = null
let started = false

/** 页面是否不可见。无 document（SSR / 单测）时按「可见」处理，不暂停。 */
function isHidden(): boolean {
  return typeof document !== 'undefined' && document.hidden === true
}

function tick() {
  // 后台不推进：定时器在后台照跑不误（WebView 不会被挂起），
  // 不挡掉就是白白重算。回前台由 onVisibility 立刻补一次，不影响界面正确性。
  if (isHidden()) return
  dueNowRef.value = liveNowSec()
}

function onVisibility() {
  if (typeof document === 'undefined') return
  if (!document.hidden) tick() // 回到前台立刻补一次，不用等下一个周期
}

/**
 * 启动 tick。幂等——重复调用不会起多个定时器。
 * 放在 store 创建路径上（模块加载即启动不合适：那会在没用到闪卡时也一直跑）。
 */
export function startDueClock(): void {
  if (started) return
  if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
    document.addEventListener('visibilitychange', onVisibility)
  }
  timer = setInterval(tick, TICK_MS)
  started = true
}

/** 停掉 tick 并摘监听。仅测试与热重载需要。 */
export function stopDueClock(): void {
  if (!started) return
  if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
  if (typeof document !== 'undefined' && typeof document.removeEventListener === 'function') {
    document.removeEventListener('visibilitychange', onVisibility)
  }
  started = false
}

/**
 * 测试用：把响应式时间直接拨到某个秒值。
 * 生产代码不该调用——它绕过 tick 机制，会掩盖「时间到底谁在推进」这个问题。
 */
export function __setDueNowSecForTest(v: number): void {
  dueNowRef.value = v
}

/**
 * 测试用：直接执行一次与 setInterval 回调完全相同的逻辑。
 * 用来证明「定时器真的会把 ref 推进」，而不是只测手动赋值。
 * 生产代码不该调用。
 */
export function __tickDueClockForTest(): void {
  tick()
}

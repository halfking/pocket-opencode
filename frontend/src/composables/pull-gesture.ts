/**
 * 下拉/上拉手势的物理计算（纯函数，无 DOM 依赖，可直接 node --test）。
 *
 * 为什么要单独抽出来：旧实现把阻尼系数 0.5 直接写在 touchmove 里，
 * 结果手感是「线性拉一段然后突然停住」——手指还在动，内容不动了。
 * 根因是**阻尼是常数**：真实的手势阻尼应该随位移递减（越拉越沉），
 * 且到达阈值后应该明显「吃劲」，让用户知道再拉也不会有额外收益。
 *
 * 这里的曲线参考 iOS UIScrollView 的 rubber band：
 *   f(x) = (1 - 1/(x·c/d + 1)) · d
 * 其中 d 是维度、c 是常数（iOS 取 0.55）。该函数的性质正是我们要的：
 *   - f(0) = 0，斜率 c（起手跟手，不迟钝）；
 *   - f'(x) 单调递减（越拉越沉，有「拉不到底」的预期）；
 *   - f(∞) = d（有硬上限，不会把内容拽出屏幕）。
 */

/**
 * 起手响应系数：内容跟随手指的初始比例。
 *
 * 取 0.75 而不是 1.0：完全 1:1 会让下拉显得「生硬、没有阻力」，
 * 而 0.5（旧实现的常数阻尼）又明显滞后于手指。
 */
export const PULL_RESPONSE = 0.75

/** 橡胶带上限倍率：位移趋近 threshold×该值后不再增长。 */
export const MAX_PULL_RATIO = 1.6

/**
 * 位移映射：把手指原始位移映射成内容位移。
 *
 * 曲线是**指数逼近** f(x) = D·(1 - e^(-k·x/D))，而不是 iOS 的
 * rubber band f(x) = (1 - 1/(x·c/d + 1))·d。后者在这里不可用：
 * 它的渐近线就是 dimension，而触发阈值必须小于渐近线（否则永远够不到），
 * 一旦把 dimension 设成「阈值以上的上限」，起手段就变成
 * D·c/d ≈ 1.6，手指刚动内容就窜出去，突兀感比旧实现更差；
 * 反过来把 dimension 设成阈值本身，渐近线又正好等于阈值，需要拉 291px
 * 才能触发刷新，比旧实现的 120px 退步得离谱。
 *
 * 指数曲线的两个极限正好对上需求：
 *   - f'(0) = PULL_RESPONSE = 0.75：起手略慢于手指，有「拉得动」的阻尼感；
 *   - f(∞) = D = threshold × MAX_PULL_RATIO：硬上限明确，用户知道到顶了。
 * 且 f 在 x = 1.25·threshold 附近穿过 threshold，与旧实现的触发行程持平。
 *
 * @param rawDelta 手指位移（px，向下为正）
 * @param dimension 渐近上限，通常传 maxPull
 * @param k 起手响应系数
 */
export function pullOffset(rawDelta: number, dimension: number, k: number = PULL_RESPONSE): number {
  if (rawDelta <= 0 || dimension <= 0) return 0
  return dimension * (1 - Math.exp((-k * rawDelta) / dimension))
}

/** 橡胶带映射别名：按阈值推出上限后再映射。 */
export function rubberBand(rawDelta: number, threshold: number): number {
  return pullOffset(rawDelta, threshold * MAX_PULL_RATIO)
}

/** 下拉位移：硬上限锁在 threshold × MAX_PULL_RATIO。 */
export function pullDistanceFor(rawDelta: number, threshold: number): number {
  return rubberBand(rawDelta, threshold)
}

/** 触发进度 0~1（用于图标旋转、透明度、文案切换）。 */
export function pullProgress(offset: number, threshold: number): number {
  if (threshold <= 0) return 0
  return Math.max(0, Math.min(1, offset / threshold))
}

/** 是否越过阈值（松手即刷新）。用 >= 保证「刚好到」也算达成。 */
export function shouldTrigger(offset: number, threshold: number): boolean {
  return offset >= threshold
}

/**
 * 箭头旋转角：随进度从 0°（向下）转到 180°（向上）。
 * 过阈值后继续转一点点到 200°，给「已就绪」的过冲反馈。
 */
export function arrowRotation(offset: number, threshold: number): number {
  const p = pullProgress(offset, threshold)
  return p >= 1 ? 200 : p * 180
}

/**
 * 指示器缩放：0.6 → 1 随进度线性，略微用 easeOut 曲线让前半程更快显形。
 */
export function indicatorScale(offset: number, threshold: number): number {
  const p = pullProgress(offset, threshold)
  // easeOutCubic：起手迅速显形，后段收敛，避免小位移时几乎看不见。
  const eased = 1 - Math.pow(1 - p, 3)
  return 0.6 + 0.4 * eased
}

/**
 * 提示文案。与旧实现相比新增「已达阈值」与「刷新中」的区分。
 */
export type PullHint = 'pull' | 'release' | 'refreshing'

export function pullHint(offset: number, threshold: number, refreshing: boolean): PullHint {
  if (refreshing) return 'refreshing'
  return shouldTrigger(offset, threshold) ? 'release' : 'pull'
}

export function pullHintText(hint: PullHint): string {
  switch (hint) {
    case 'refreshing':
      return '正在同步邮件…'
    case 'release':
      return '松开立即同步'
    default:
      return '下拉同步邮件'
  }
}

/**
 * 刷新中内容应该停在哪里：停在阈值位置而不是 0，
 * 否则指示器会在刷新过程中突然消失。留 1.15 倍阈值让箭头有余量旋转。
 */
export const REFRESH_HOLD_RATIO = 1.15

export function refreshHoldOffset(threshold: number): number {
  return threshold * REFRESH_HOLD_RATIO
}

/**
 * 甩动速度（px/ms），用于「快速下拉直接触发刷新」的判定。
 * 返回 null 表示采样不足。
 */
export function flingVelocity(
  samples: Array<{ y: number; t: number }>,
  now: number,
  windowMs = 100,
): number | null {
  if (samples.length < 2) return null
  const recent = samples.filter((s) => now - s.t <= windowMs)
  if (recent.length < 2) return null
  const first = recent[0]
  const last = recent[recent.length - 1]
  const dt = last.t - first.t
  if (dt <= 0) return null
  return (last.y - first.y) / dt
}

/**
 * 是否因「快速下甩」提前触发刷新（还没到阈值就松手）。
 *
 * 阈值取 0.45 px/ms：约等于 220ms 内拉 100px，是一次明确的下甩手势，
 * 又不至于把「慢慢拉到一半松手」误判成刷新。
 */
export const FLING_TRIGGER_VELOCITY = 0.45

export function shouldTriggerByFling(
  offset: number,
  threshold: number,
  velocity: number | null,
): boolean {
  if (velocity === null) return false
  // 只有在「已经拉了相当一段」的前提下才认甩动，避免轻轻一抖就刷新。
  if (offset < threshold * 0.4) return false
  return velocity >= FLING_TRIGGER_VELOCITY
}

/** 清空采样（手势结束/取消时调用），避免下一次手势用到上次的点。 */
export function resetSamples(samples: Array<{ y: number; t: number }>): void {
  samples.length = 0
}

/** 记录一个采样点，并裁掉超出窗口的旧点（防止数组无限增长）。 */
export function pushSample(
  samples: Array<{ y: number; t: number }>,
  y: number,
  now: number,
  windowMs = 100,
): void {
  samples.push({ y, t: now })
  while (samples.length > 2 && now - samples[0].t > windowMs) samples.shift()
}

<template>
  <div class="pull-to-refresh" ref="containerRef">
    <!--
      指示器：绝对定位在顶部，随下拉「揭开」。
      旧实现用 height 撑开 + translateY(-100%)，指示器出现时会跳一下
      （高度变了再位移，两帧之间不连续）。这里改成固定满高 + 位移揭开，
      揭开过程与手指 1:1 对齐，不存在跳帧。

      静止时必须**完全藏在内容之上**（2026-10-03 模拟器 API 35 实测修复）：
      原式 `INDICATOR_HEIGHT - pullDistance` 在 pullDistance=0 时得 +56px，
      方向是反的——把指示器从「藏在顶部」推到了内容区内部，于是
      「下拉同步邮件」以 0.25 不透明度常驻在筛选 chip 行上面叠着
      （实测 rect: 指示器 top 165 / bottom 221，.work-filters top 152 /
      bottom 214，168px 宽的 x 区间几乎完全重叠）。
    -->
    <div
      class="refresh-indicator"
      :class="{ 'refresh-indicator--ready': hint === 'release' }"
      :style="{
        transform: `translate3d(0, ${indicatorOffset}px, 0)`,
        opacity: indicatorOpacity,
      }"
    >
      <div class="refresh-backdrop" :style="{ opacity: backdropOpacity }"></div>
      <div
        class="refresh-icon"
        :class="{ 'refresh-icon--spinning': isRefreshing, 'refresh-icon--ready': hint === 'release' }"
        :style="{
          transform: `rotate(${arrowRotation}deg) scale(${indicatorScale})`,
        }"
      >
        <!--
          图标名必须与 material-symbols-outlined span **同一行**：
          子集构建脚本的插值规则要求两者同行，跨行写法扫不到，
          字体会缺字（渲染成字面文本）。
        -->
        <span class="material-symbols-outlined" aria-hidden="true">{{ isRefreshing ? 'progress_activity' : 'keyboard_arrow_down' }}</span>
      </div>
      <div class="refresh-text" :class="{ 'refresh-text--ready': hint === 'release' }">
        {{ pullHintText(hint) }}
      </div>
    </div>

    <!--
      内容层：跟手期间**不加 transition**（跟手必须 1:1，加了过渡就变滞后），
      松手回弹时才挂上弹簧曲线。这是手感的核心区别。

      ⚠️ 下面这四个 **@touch\* 绑定不是冗余，删掉整套下拉刷新就彻底失效**
      （2026-10-06 设备实跑发现）。本组件的 `handleTouchStart` / `handleTouchMove` /
      `handleTouchEnd` **定义了却从未绑到任何元素**——`onMounted` 只绑了 `scroll`。
      后果：橡皮筋、阈值、甩动判定、触觉、指示器三态全是死代码，
      「正在同步邮件… / 松开立即同步」永远不会出现，用户永远拉不动。
      而 27 条 `continuousList` 单测 + `pull-gesture` 数学单测**全绿**：
      它们测的是函数算得对不对，没人测「函数有没有被接上」。
      ⇒ 设备判据：scripts/device-matrix.mjs 的 UI-07b（先自证手势真的送达了组件，
        再谈后面两条），静态门禁：src/components/__tests__/handler-wiring.test.mjs。

      注意：滚动监听只在这里用 addEventListener 挂一次。旧实现同时写了
      `@scroll="onContentScroll"` 和 addEventListener，等于每帧回调两次——
      滚动跟手的 delta 被重复上报，chrome 位移翻倍、掉帧。已去掉模板绑定。
    -->
    <div
      class="refresh-content"
      :class="{ 'refresh-content--settling': settling }"
      ref="contentRef"
      @touchstart="handleTouchStart"
      @touchmove="handleTouchMove"
      @touchend="handleTouchEnd"
      @touchcancel="resetGesture"
      :style="{
        transform: `translate3d(0, ${pullDistance}px, 0)`,
        transition: settling ? `transform ${SETTLE_MS}ms var(--ease-spring, cubic-bezier(0.22, 1, 0.36, 1))` : 'none',
      }"
    >
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, inject, onMounted, onUnmounted, watch } from 'vue'
import { SCROLL_CHROME_KEY } from '@/composables/scroll-chrome'
import { scrollEdgeFlags } from '@/composables/useScrollHideChrome'
import { haptic } from '@/composables/useHaptics'
import {
  arrowRotation,
  flingVelocity,
  indicatorScale,
  pullDistanceFor,
  pullHint,
  pullHintText,
  pullProgress,
  pushSample,
  refreshHoldOffset,
  resetSamples,
  shouldTrigger,
  shouldTriggerByFling,
} from '@/composables/pull-gesture'

export interface PullToRefreshProps {
  onRefresh: () => Promise<void>
  threshold?: number
  disabled?: boolean
}

const props = withDefaults(defineProps<PullToRefreshProps>(), {
  threshold: 60,
  disabled: false,
})

const scrollChrome = inject(SCROLL_CHROME_KEY, null)

const containerRef = ref<HTMLElement>()
const contentRef = ref<HTMLElement>()
const pullDistance = ref(0)
/** 回弹/刷新中：内容层才允许挂 CSS 过渡。 */
const settling = ref(false)
const isPulling = ref(false)
const isRefreshing = ref(false)

/** 回弹动画时长，与 pull-gesture 的曲线配套（略长于 --duration-base 才像弹簧）。 */
const SETTLE_MS = 320
/** 指示器自身高度：比阈值略大，箭头旋转有地方转。 */
const INDICATOR_HEIGHT = 56

let lastScrollTop = 0
/** 本次下拉是否已触发过阈值触觉（每次下拉只震一次） */
let thresholdCrossed = false
/** 甩动采样点，用于「快速下甩提前触发」 */
const samples: Array<{ y: number; t: number }> = []

const hint = computed(() => pullHint(pullDistance.value, props.threshold, isRefreshing.value))
const progress = computed(() => pullProgress(pullDistance.value, props.threshold))
/**
 * 指示器位移：让它「藏」在上方，随下拉逐步落进可视区。
 *
 * 刻意不写死 -100%：指示器满高 INDICATOR_HEIGHT，用百分比在高度随内容
 * 变化的容器里会和实际像素脱节。
 *
 * 方向（2026-10-03 模拟器 API 35 实测修复）：静止时必须是**负值**——
 * 指示器整块退到容器顶边之上，与内容零重叠；拉到 INDICATOR_HEIGHT 时
 * 恰好位移 0、完整落进内容让出的那道缝。原式 `INDICATOR_HEIGHT - d`
 * 在 d=0 时得 +56（把自己推进内容区），在 d=56 时得 0（只到顶边），
 * 两端都错，是位移方向写反了。超拉时钳在 0，不再继续下压。
 */
const indicatorOffset = computed(() =>
  Math.min(0, Math.max(pullDistance.value, 0) - INDICATOR_HEIGHT),
)
/**
 * 不透明度：静止 0（完全不可见），随进度升到 1。
 *
 * 原来写死 `min(1, 0.25 + progress*0.75)`，也就是**静止时也留 0.25**——
 * 那是上面那个方向 bug 的放大器：位置错了还被这条下限「保证」看得见，
 * 于是叠在 chip 上的半透明文字成了常驻。位置修好后下限必须一并去掉，
 * 否则静止时仍会有一层 25% 的残影压在内容上。
 */
const indicatorOpacity = computed(() => Math.min(1, progress.value * 1.4))
/** 背景渐变只在拉开后出现，避免顶部长期糊着一层色。 */
const backdropOpacity = computed(() => Math.max(0, (pullDistance.value - 8) / props.threshold))

function onContentScroll() {
  const el = contentRef.value
  if (!el) return
  const top = el.scrollTop
  // 回顶按钮等消费方订阅滚动位置。
  emit('scroll-position', top)
  if (!scrollChrome?.enabled.value) return
  const delta = top - lastScrollTop
  lastScrollTop = top
  scrollChrome.reportScroll({ scrollTop: top, delta, ...scrollEdgeFlags(el, top) })
}

const emit = defineEmits<{
  (e: 'scroll-position', top: number): void
}>()

/**
 * 暴露滚动容器，供消费方做「回顶」等程序化滚动。
 *
 * 暴露的是 `contentRef` 这个 Ref 本身：Vue 在 defineExpose 时会自动解包，
 * 消费方通过组件实例拿到的就是裸 HTMLElement|null（见 typecheck 校验）。
 */
defineExpose({ scrollEl: contentRef })

onMounted(() => {
  contentRef.value?.addEventListener('scroll', onContentScroll, { passive: true })
})

onUnmounted(() => {
  contentRef.value?.removeEventListener('scroll', onContentScroll)
})

// disabled 变化时若正处于下拉中途，直接收手，避免手势「卡住」在拉伸态。
watch(
  () => props.disabled,
  (now) => {
    if (now && isPulling.value) resetGesture()
  },
)

function resetGesture() {
  isPulling.value = false
  thresholdCrossed = false
  resetSamples(samples)
  settleTo(0)
}

/** 回到某个位移（挂上过渡 → 改值 → 过渡结束后解挂）。 */
function settleTo(target: number) {
  settling.value = true
  pullDistance.value = target
  window.setTimeout(() => {
    settling.value = false
  }, SETTLE_MS)
}

const handleTouchStart = (e: TouchEvent) => {
  if (props.disabled || isRefreshing.value) return

  // 只在页面顶部时允许下拉
  const scrollTop = contentRef.value?.scrollTop || 0
  if (scrollTop > 0) return

  // 触摸开始即解除过渡：这一帧之后的内容位移必须严格跟手。
  settling.value = false
  startY = e.touches[0].clientY
  isPulling.value = true
  thresholdCrossed = false
  resetSamples(samples)
  pushSample(samples, startY, performance.now())
}

let startY = 0

const handleTouchMove = (e: TouchEvent) => {
  if (!isPulling.value || props.disabled || isRefreshing.value) return

  const currentY = e.touches[0].clientY
  const deltaY = currentY - startY

  // 手指往上走（deltaY ≤ 0）时**不改位移**，但要重置起点。
  // 否则一次手势里先下拉再上滑，起点还停在最初那点，
  // 上滑回 0 之后继续往下拉会算出一个巨大的 deltaY，凭空把列表拽下来。
  if (deltaY <= 0) {
    startY = currentY
    if (pullDistance.value > 0) {
      // 上滑过程中先无过渡收回，跟手优先于动画。
      settling.value = false
      pullDistance.value = 0
    }
    return
  }

  // 阻止默认滚动：列表已在顶部、用户继续下拉时，浏览器会同时做原生
  // overscroll（下拉刷新/回弹），与本组件的位移叠加成「页面跟着一起动」。
  // touch-action: pan-y 下 touchmove 仍是可取消的（只有 passive 监听才不可取消，
  // 模板上的 @touchmove 默认非 passive），所以这里 preventDefault 有效。
  if (e.cancelable) e.preventDefault()

  pushSample(samples, currentY, performance.now())
  pullDistance.value = pullDistanceFor(deltaY, props.threshold)

  // 过阈值轻触觉一次（原生顺滑度审计 P0 #4；松手才触发，这里只是预告）
  if (!thresholdCrossed && pullDistance.value >= props.threshold) {
    thresholdCrossed = true
    haptic('light')
  }
}

const handleTouchEnd = async () => {
  if (!isPulling.value || props.disabled) return

  isPulling.value = false
  const velocity = flingVelocity(samples, performance.now())
  resetSamples(samples)
  const offset = pullDistance.value

  const fire = shouldTrigger(offset, props.threshold) || shouldTriggerByFling(offset, props.threshold, velocity)

  if (!fire) {
    settleTo(0)
    return
  }

  isRefreshing.value = true
  // 刷新中停在阈值之上：指示器不会在刷新过程中缩回去消失。
  settleTo(refreshHoldOffset(props.threshold))
  haptic('medium')

  try {
    await props.onRefresh()
  } catch (error) {
    console.error('[PullToRefresh] 刷新失败', error)
  } finally {
    isRefreshing.value = false
    settleTo(0)
  }
}
</script>

<style scoped>
.pull-to-refresh {
  position: relative;
  overflow: hidden;
  height: 100%;
}

.refresh-indicator {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 56px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  z-index: 1;
  pointer-events: none;
  /* 位移揭开：位移由脚本逐帧写入，这里不挂 transform 过渡，
     否则又变成滞后。 */
  will-change: transform;
}

/* 顶部渐变：拉开越多越实，给「下面有东西要进来」的暗示。 */
.refresh-backdrop {
  position: absolute;
  inset: 0;
  background: linear-gradient(
    to bottom,
    var(--bg-card) 0%,
    color-mix(in srgb, var(--bg-card) 82%, transparent) 70%,
    transparent 100%
  );
  pointer-events: none;
}

.refresh-icon {
  position: relative;
  font-size: 22px;
  line-height: 1;
  color: var(--text-tertiary, var(--text-muted));
  transition: color var(--duration-fast) var(--ease-out);
  will-change: transform;
}

/* 达标后换成品牌色：颜色本身就是「可以松手了」的反馈。 */
.refresh-icon--ready {
  color: var(--brand-primary);
}

/*
 * 刷新中：进度图标自带旋转动画。CSS animation 在层叠里压过内联 style，
 * 所以脚本写的 rotate 在这一帧不生效，两者不会互相打架。
 */
.refresh-icon--spinning {
  color: var(--brand-primary);
  animation: pull-spin 900ms linear infinite;
}

@keyframes pull-spin {
  to {
    transform: rotate(360deg);
  }
}

.refresh-text {
  position: relative;
  font-size: var(--text-2xs);
  line-height: 1.2;
  color: var(--text-muted);
  font-weight: var(--font-weight-medium);
  transition: color var(--duration-fast) var(--ease-out);
  white-space: nowrap;
}

.refresh-text--ready {
  color: var(--brand-primary);
}

.refresh-content {
  height: 100%;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
  /*
   * touch-action 必须是 **pan-y**，不能是 pan-x。
   *
   * 之前写成 pan-x 是想「禁掉纵向平移、让 touchmove 的 preventDefault 生效」，
   * 但那等于直接禁用了本元素的纵向滚动——列表会彻底滑不动，是比原生下拉
   * 刷新更严重的回归。touch-action 的语义是「**允许**浏览器处理哪些手势」，
   * 写 pan-y 表示纵向仍由浏览器滚动；下拉刷新靠 touchstart 时记录
   * scrollTop===0 + touchmove 里 e.preventDefault() 实现：
   * 列表不在顶部时脚本根本不调 preventDefault，纵向滚动照常交给浏览器。
   *
   * overscroll-behavior: contain 阻止滚动链冒泡到外层页面，
   * 避免列表滚到边界时带动整个页面一起动。
   */
  touch-action: pan-y;
  overscroll-behavior: contain;
  will-change: transform;
}
</style>

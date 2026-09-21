<template>
  <div
    ref="elRef"
    class="progress-ring"
    :class="{ 'progress-ring--indeterminate': indeterminate }"
    :style="{ width: size + 'px', height: size + 'px' }"
    role="progressbar"
    :aria-valuenow="Math.round(value)"
    :aria-valuemin="0"
    :aria-valuemax="100"
  >
    <svg
      class="progress-ring__svg"
      :viewBox="`0 0 ${viewBox} ${viewBox}`"
      :width="size"
      :height="size"
    >
      <!-- 背景轨 -->
      <circle
        class="progress-ring__track"
        :cx="center"
        :cy="center"
        :r="radius"
        :stroke-width="stroke"
        fill="none"
      />
      <!-- 进度弧：stroke-dashoffset 从 circumference → circumference*(1-p) -->
      <circle
        class="progress-ring__bar"
        :cx="center"
        :cy="center"
        :r="radius"
        :stroke-width="stroke"
        :stroke-linecap="linecap"
        fill="none"
        :stroke="color"
        :stroke-dasharray="circumference"
        :stroke-dashoffset="dashOffset"
        :transform="`rotate(-90 ${center} ${center})`"
      />
    </svg>
    <!-- 中心内容（slot） -->
    <div v-if="$slots.default" class="progress-ring__label">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch, onBeforeUnmount } from 'vue'

export interface ProgressRingProps {
  /** 0..100 */
  value: number
  /** 直径 px，默认 64 */
  size?: number
  /** 环宽 px，默认 6 */
  stroke?: number
  /** 配色，缺省取 brand */
  color?: string
  /** 弧端 cap，缺省 round，rect 看起来更工业 */
  linecap?: 'round' | 'butt' | 'square'
  /** true 时不显示具体进度，做「indeterminate」无限扫描 */
  indeterminate?: boolean
  /** 动画时长 ms（value → value 间的插值），仅在非 indeterminate 时生效 */
  duration?: number
}

const props = withDefaults(defineProps<ProgressRingProps>(), {
  size: 64,
  stroke: 6,
  color: 'var(--brand-primary, #667eea)',
  linecap: 'round',
  indeterminate: false,
  duration: 600,
})

const elRef = ref<HTMLElement>()

const viewBox = 100
const center = viewBox / 2
const radius = computed(() => center - props.stroke / 2 - 1)
const circumference = computed(() => 2 * Math.PI * radius.value)

const animatedValue = ref(0)
let raf = 0
let startedAt = 0
let fromVal = 0
let toVal = 0

function startTween(from: number, to: number, dur: number) {
  cancelAnimationFrame(raf)
  fromVal = from
  toVal = to
  startedAt = performance.now()
  const step = (now: number) => {
    const t = dur === 0 ? 1 : Math.min(1, (now - startedAt) / dur)
    // ease-out quartic
    const eased = 1 - Math.pow(1 - t, 4)
    animatedValue.value = fromVal + (toVal - fromVal) * eased
    if (t < 1) raf = requestAnimationFrame(step)
    else raf = 0
  }
  raf = requestAnimationFrame(step)
}

/** 当前弧长占 circumference 的比例 */
const progress = computed(() => {
  if (props.indeterminate) {
    // indeterminate 时让 dashOffset 用时间相位来画一个绕圈扫动的弧
    return (animatedIndeterminateOffset.value % 1)
  }
  return clamp(animatedValue.value, 0, 100) / 100
})

const dashOffset = computed(() => circumference.value * (1 - progress.value))

/* indeterminate 扫描动画 —— 持续旋转 dashOffset，给视觉反馈「正在跑」 */
const animatedIndeterminateOffset = ref(0)
let indTimer = 0
function startIndeterminate() {
  if (!props.indeterminate) return
  const start = performance.now()
  const tick = () => {
    if (!props.indeterminate) return
    const elapsed = (performance.now() - start) / 1000
    // 每 1.4s 一圈；弧长 35%，移动 100%
    const phase = (elapsed % 1.4) / 1.4
    animatedIndeterminateOffset.value = phase
    indTimer = requestAnimationFrame(tick)
  }
  tick()
}

watch(
  () => props.value,
  (newVal, oldVal) => {
    if (oldVal === undefined) return
    startTween(oldVal, newVal, props.duration)
  },
)

watch(
  () => props.indeterminate,
  () => {
    if (props.indeterminate) startIndeterminate()
  },
)

onMounted(() => {
  if (props.indeterminate) startIndeterminate()
  else startTween(0, props.value, 0) // first render at exact value
})

onBeforeUnmount(() => {
  cancelAnimationFrame(raf)
  cancelAnimationFrame(indTimer)
})

function clamp(v: number, min: number, max: number) {
  return Math.max(min, Math.min(max, v))
}
</script>

<style scoped>
.progress-ring {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  vertical-align: middle;
  flex-shrink: 0;
}

.progress-ring__svg {
  overflow: visible;
}

.progress-ring__track {
  stroke: var(--border, rgba(0, 0, 0, 0.08));
}

.progress-ring__bar {
  /* 过渡只用于非 indeterminate，value 变化时提供 0.4s 平滑 */
  transition: stroke-dashoffset var(--duration-fast, 200ms) cubic-bezier(0.16, 1, 0.3, 1);
}

.progress-ring--indeterminate .progress-ring__bar {
  transition: none;
  animation: progress-ring-sweep 1.4s cubic-bezier(0.65, 0.05, 0.36, 1) infinite;
}

.progress-ring__label {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-variant-numeric: tabular-nums;
  font-weight: var(--font-weight-semibold, 600);
  font-size: 0.85em;
  color: var(--text-primary, #0a0a0a);
  pointer-events: none;
}

@keyframes progress-ring-sweep {
  0% {
    stroke-dasharray: 0 100;
    stroke-dashoffset: 0;
  }
  50% {
    stroke-dasharray: 35 65;
    stroke-dashoffset: -35;
  }
  100% {
    stroke-dasharray: 35 65;
    stroke-dashoffset: -100;
  }
}

/* 暗色模式微调 */
:global([data-theme='dark']) .progress-ring__track {
  stroke: rgba(255, 255, 255, 0.08);
}
</style>

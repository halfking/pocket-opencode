<template>
  <span ref="elRef" class="animated-number" :class="{ 'is-pulsing': pulsing }">
    {{ display }}<span v-if="trailingUnit" class="animated-number__unit">{{ trailingUnit }}</span>
  </span>
</template>

<script setup lang="ts">
import { ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { useCountUp } from '@/composables/useCountUp'

export interface AnimatedNumberProps {
  value: number
  decimals?: number
  prefix?: string
  suffix?: string
  duration?: number
  /** 单位串，例如 'ms' / '%' / '次' —— 显示在数字后面。 */
  unit?: string
  /** 数值变化时是否附加 1.2× 微脉冲（缩放），用作「数字刚更新」的视觉反馈。 */
  pulse?: boolean
}

const props = withDefaults(defineProps<AnimatedNumberProps>(), {
  decimals: 0,
  prefix: '',
  suffix: '',
  duration: 800,
  unit: '',
  pulse: false,
})

const elRef = ref<HTMLElement>()

const { display, start } = useCountUp(0, {
  decimals: props.decimals,
  prefix: props.prefix,
  suffix: props.suffix,
  duration: props.duration,
})

const pulsing = ref(false)
let pulseTimer = 0

watch(
  () => props.value,
  (newVal, oldVal) => {
    if (oldVal === undefined) return
    start(newVal)
    if (props.pulse) {
      pulsing.value = true
      clearTimeout(pulseTimer)
      pulseTimer = window.setTimeout(() => (pulsing.value = false), 240)
    }
  },
)

onMounted(() => {
  // 首次渲染先把数字跳到初始值（不做动画）
  display.value = formatInitial(props.value)
})

onBeforeUnmount(() => {
  clearTimeout(pulseTimer)
})

function formatInitial(v: number) {
  if (!Number.isFinite(v)) return `${props.prefix}0${props.suffix}`
  return `${props.prefix}${v.toFixed(props.decimals)}${props.suffix}`
}

const trailingUnit = props.unit
</script>

<style scoped>
.animated-number {
  font-variant-numeric: tabular-nums;
  /* 数字等宽，避免动画期间水平抖动 */
  display: inline-flex;
  align-items: baseline;
  transition: color var(--duration-fast) var(--ease-out);
}

.animated-number.is-pulsing {
  animation: animated-pulse 240ms var(--ease-out);
  color: var(--brand-primary, #667eea);
}

.animated-number__unit {
  margin-left: 0.18em;
  font-size: 0.7em;
  opacity: 0.6;
  font-weight: var(--font-weight-medium, 500);
}

@keyframes animated-pulse {
  0% {
    transform: scale(1);
  }
  40% {
    transform: scale(1.18);
  }
  100% {
    transform: scale(1);
  }
}
</style>

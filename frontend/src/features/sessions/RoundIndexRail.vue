<script setup lang="ts">
/**
 * RoundIndexRail — 会话请求列表左缘的提示词快速索引条（2026-09-08 会话详情改版）。
 *
 * 类似书籍侧边索引 / 编辑器 minimap：
 *   - 每根条 = 一轮用户提示词（groupMessagesIntoRounds 的轮序）；
 *   - 条高 ∝ 提示词字数（等比分配 + 上下限钳制，长 prompt 一眼可辨）；
 *   - 贴窗口左缘、与内容区等高（父级 body-row 拉伸）；
 *   - 点按跳到该轮；按住上下滑动 scrub 连续定位（touch-action:none + 指针捕获），
 *     拖动中浮出「轮号 + 提示词摘录」气泡；
 *   - 当前轮高亮（activeIndex 由父级按滚动位置推导）。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { truncate } from './useSessionEvents'

export interface RoundIndexItem {
  /** 轮号（1-based，与 round_index 契约同规则）。 */
  index: number
  /** 该轮用户提示词原文（条高权重 + 拖动摘录）。 */
  text: string
}

const props = defineProps<{
  rounds: RoundIndexItem[]
  activeIndex: number
}>()

const emit = defineEmits<{ (e: 'seek', index: number): void }>()

// ── 条高分配：权重 = max(字数, 下限)，按 rail 可视高均摊，钳制 [MIN, MAX] ──
const BAR_GAP = 3
const MIN_BAR = 14
const MAX_BAR = 120
const WEIGHT_FLOOR = 24

const railEl = ref<HTMLElement | null>(null)
const railInner = ref(0)
let ro: ResizeObserver | null = null

onMounted(() => {
  const el = railEl.value
  if (!el) return
  const measure = () => {
    railInner.value = el.clientHeight
  }
  measure()
  ro = new ResizeObserver(measure)
  ro.observe(el)
})
onBeforeUnmount(() => ro?.disconnect())

const barHeights = computed<number[]>(() => {
  const n = props.rounds.length
  if (n === 0 || railInner.value <= 0) return []
  const weights = props.rounds.map((r) => Math.max(r.text.trim().length, WEIGHT_FLOOR))
  const totalW = weights.reduce((s, w) => s + w, 0)
  const usable = Math.max(railInner.value - BAR_GAP * (n - 1), MIN_BAR * n)
  const clamped = weights.map((w) => Math.min(MAX_BAR, Math.max(MIN_BAR, (usable * w) / totalW)))
  const sum = clamped.reduce((s, h) => s + h, 0)
  if (sum > usable) {
    const k = usable / sum
    return clamped.map((h) => Math.max(10, Math.floor(h * k)))
  }
  return clamped.map(Math.floor)
})

// ── 指针 scrub：按下选中 → 移动连续定位 → 抬起结束 ──
const scrubbing = ref(false)
const scrubBarIndex = ref(-1)

function barIndexFromY(clientY: number): number {
  const el = railEl.value
  if (!el) return -1
  const top = clientY - el.getBoundingClientRect().top
  let acc = 0
  for (let i = 0; i < barHeights.value.length; i++) {
    acc += barHeights.value[i] + BAR_GAP
    if (top <= acc - BAR_GAP / 2) return i
  }
  return barHeights.value.length - 1
}

function onBarPointerDown(e: PointerEvent, i: number) {
  scrubbing.value = true
  scrubBarIndex.value = i
  railEl.value?.setPointerCapture(e.pointerId)
  emit('seek', props.rounds[i].index)
}

function onRailPointerMove(e: PointerEvent) {
  if (!scrubbing.value) return
  const i = barIndexFromY(e.clientY)
  if (i >= 0 && i !== scrubBarIndex.value) {
    scrubBarIndex.value = i
    emit('seek', props.rounds[i].index)
  }
}

function onScrubEnd() {
  scrubbing.value = false
  scrubBarIndex.value = -1
}

/** 拖动气泡：贴着当前条垂直居中。 */
const scrubTip = computed(() => {
  const i = scrubBarIndex.value
  if (!scrubbing.value || i < 0 || barHeights.value.length === 0) return null
  let center = 0
  for (let k = 0; k < i; k++) center += barHeights.value[k] + BAR_GAP
  center += barHeights.value[i] / 2
  const round = props.rounds[i]
  return {
    top: center,
    label: `第 ${round.index} 轮`,
    text: truncate(round.text, 42) || '（空提示词）',
  }
})
</script>

<template>
  <nav
    ref="railEl"
    class="round-rail"
    aria-label="提示词快速索引"
    @pointermove="onRailPointerMove"
    @pointerup="onScrubEnd"
    @pointercancel="onScrubEnd"
  >
    <button
      v-for="(r, i) in rounds"
      :key="r.index"
      type="button"
      class="rail-bar"
      :class="{
        active: r.index === activeIndex,
        scrubbing: scrubbing && scrubBarIndex === i,
      }"
      :style="{ height: (barHeights[i] ?? MIN_BAR) + 'px' }"
      :aria-label="`第 ${r.index} 轮：${truncate(r.text, 24) || '空提示词'}`"
      :aria-current="r.index === activeIndex ? 'true' : undefined"
      @pointerdown="onBarPointerDown($event, i)"
      @click="emit('seek', r.index)"
    ></button>

    <!-- 拖动定位气泡 -->
    <div v-if="scrubTip" class="rail-tip" :style="{ top: scrubTip.top + 'px' }" aria-hidden="true">
      <span class="tip-label">{{ scrubTip.label }}</span>
      <span class="tip-text">{{ scrubTip.text }}</span>
    </div>
  </nav>
</template>

<style scoped>
.round-rail {
  position: relative;
  flex: 0 0 auto;
  align-self: stretch;
  width: 18px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 3px;
  padding: var(--space-2) 0;
  background: var(--bg-base);
  border-right: 1px solid var(--border);
  touch-action: none;
  user-select: none;
  -webkit-user-select: none;
  overflow: hidden;
}

.rail-bar {
  flex: 0 0 auto;
  width: 5px;
  min-height: 10px;
  padding: 0;
  border: none;
  border-radius: 3px;
  background: var(--border);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out), width var(--duration-fast) var(--ease-out);
}
.rail-bar:hover {
  background: var(--text-muted);
}
.rail-bar.active {
  background: var(--brand-primary);
  width: 7px;
}
.rail-bar.scrubbing {
  background: var(--brand-primary);
  outline: 2px solid color-mix(in srgb, var(--brand-primary) 30%, transparent);
}

/* 拖动气泡：锚在条左内侧、垂直居中于当前条 */
.rail-tip {
  position: absolute;
  left: 24px;
  transform: translateY(-50%);
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-width: min(240px, 60vw);
  padding: 6px 10px;
  border-radius: 8px;
  background: rgba(23, 23, 28, 0.92);
  color: #fff;
  pointer-events: none;
  z-index: var(--z-popover, 30);
  box-shadow: var(--shadow-lg);
}
.tip-label {
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  color: rgba(255, 255, 255, 0.72);
}
.tip-text {
  font-size: var(--text-sm);
  line-height: 1.4;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
</style>

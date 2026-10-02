<!--
  SourceFilterBar — 来源筛选 chips 条（2026-10-03 全局 IA 重组）。

  为什么单独抽一个组件：
  重组后「笔记」和「消息」两个一级 tab 都是「**顶部 chips 切来源 + 下方一条统一流**」
  的同构布局。chips 如果各写一份，样式和交互会随时间漂移——用户在两个 tab 之间
  来回切时会感到「这是两个不同的 App」。这里把一次交互、一套样式钉死成一份实现。

  形态取舍（对比过三种）：
  - 分段控件（segmented）：来源数 ≤3 且互斥时视觉更整齐，但 chips 能带**计数角标**，
    「未读 12 / 订阅 3」这类信息在分段控件里放不下，会被逼到标题栏，反而更乱。
  - 横向滚动 chips：来源数会增长（笔记这边将来还有白板/剪藏），固定 3 列分段
    撑不住。横向滚动在 360dp 上实测可用，且不换行、不占纵向空间。
  - 下拉菜单：省空间但每次筛选多一次点击，与「一屏处理完」的诉求相反。

  Accessibility:
  - <div role="group"> 包裹，aria-label 由调用方给（"笔记来源" / "消息来源"）。
  - 每个 chip 是 toggle button，用 aria-pressed 表达选中态而不是 aria-selected
    ——aria-selected 只对 tab / option 角色合法，用在普通 button 上会让部分
    读屏器播报为空。
  - 选中项滚动进可视区（scrollIntoView on active），从深链进来时当前项可能在屏外。
-->
<template>
  <div class="src-bar" role="group" :aria-label="ariaLabel">
    <div class="src-track">
      <button
        v-for="opt in options"
        :key="opt.value"
        ref="chipEls"
        type="button"
        class="src-chip"
        :class="{ active: opt.value === modelValue }"
        :aria-pressed="opt.value === modelValue"
        :data-testid="`src-chip-${opt.value}`"
        @click="$emit('update:modelValue', opt.value)"
      >
        <span class="chip-label">{{ opt.label }}</span>
        <span
          v-if="typeof opt.count === 'number' && opt.count > 0"
          class="chip-count"
          :class="{ strong: opt.emphasis }"
        >{{ opt.count > 99 ? '99+' : opt.count }}</span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'

export interface SourceOption {
  value: string
  label: string
  /** 未处理条数；0 / undefined 时不渲染角标。 */
  count?: number
  /** 角标用强调色（高优先级来源，如重要邮件）。 */
  emphasis?: boolean
}

const props = withDefaults(
  defineProps<{
    modelValue: string
    options: SourceOption[]
    ariaLabel?: string
  }>(),
  { ariaLabel: '' },
)

defineEmits<{ 'update:modelValue': [value: string] }>()

const chipEls = ref<HTMLElement[]>([])

/** 选中项滚进可视区：从深链（如 /notes?source=meeting）进来时当前 chip 可能在屏外。 */
watch(
  () => props.modelValue,
  async () => {
    await nextTick()
    const active = chipEls.value.find((el) => el.classList.contains('active'))
    active?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  },
  { immediate: true },
)
</script>

<style scoped>
.src-bar {
  width: 100%;
  /* 负 margin 让 chips 贴到屏幕左右缘，滚动区能露出半颗 chip 提示「右边还有」——
     没有这个视觉暗示，用户不知道横向可滚。 */
  margin: 0 calc(-1 * var(--space-3));
  padding: 0 var(--space-3);
}

.src-track {
  display: flex;
  gap: var(--space-2);
  overflow-x: auto;
  scrollbar-width: none;
  -webkit-overflow-scrolling: touch;
  /* 只做横向滚动：竖向溢出说明布局被内容撑坏了，静默裁掉比露出滚动条更隐蔽 */
  overflow-y: hidden;
  padding: var(--space-1) 0;
  scroll-padding-inline: var(--space-3);
}

.src-track::-webkit-scrollbar {
  display: none;
}

.src-chip {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  flex: 0 0 auto;
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-full);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-secondary);
  font-size: var(--text-sm);
  line-height: 1.6;
  white-space: nowrap;
  cursor: pointer;
  transition:
    background var(--duration-fast) var(--ease-out),
    color var(--duration-fast) var(--ease-out),
    border-color var(--duration-fast) var(--ease-out);
}

.src-chip:active {
  transform: scale(0.96);
}

.src-chip.active {
  background: var(--brand-primary);
  border-color: var(--brand-primary);
  color: var(--text-inverse, #fff);
}

.chip-count {
  min-width: 18px;
  padding: 0 5px;
  border-radius: var(--radius-full);
  background: var(--bg-subtle);
  color: var(--text-secondary);
  font-size: var(--text-2xs);
  line-height: 18px;
  text-align: center;
  font-variant-numeric: tabular-nums;
}

.src-chip.active .chip-count {
  background: rgba(255, 255, 255, 0.24);
  color: var(--text-inverse, #fff);
}

.chip-count.strong {
  background: var(--danger-bg);
  color: var(--danger);
}
</style>

<!--
  ViewSegmentBar — 「同一个 tab 里换一种看法」的分段控件（M3 SegmentedButton）。

  ## 为什么不用 SourceFilterBar

  这两个东西长得像，用途完全不同，混用会给出错误的交互暗示：

  - SourceFilterBar 是**筛选器**：底下永远是同一条流，chips 换的是「流里看哪几类」。
    它能带未读角标，因为角标表达的是「这一类还剩多少没处理」。
  - ViewSegmentBar 是**视图切换**：底下是两种形态完全不同的东西（一条倒序时间线
    vs 一张 42 格月视图 + 当日议程）。这里没有「筛选」语义，也不该有未读角标——
    在月视图上挂一个「未读 12」的角标，用户会去找「点它能过滤什么」，找不到。

  换句话说：chips 回答「看哪部分」，segment 回答「看什么」。混成一个控件时
  用户只能靠猜，而猜错一次的成本是「以为日历被邮件的筛选条件影响」。

  ## 形态取舍

  不做成第三种可能——把日历塞进 SourceFilterBar 当第 4 个 chip：那样点下去
  底下的流不变形状，月视图无处安放，等于骗用户。与其做个假的入口，不如让
  两种形态在**同一层**上明确互斥。

  选横向等分（每段 flex:1）而不是按文字宽度自适应：两段文案长短在 9 种语言里
  差异很大（德语 „Zeitleiste" 比中文「时间线」长一倍以上），等分才能保证
  任何语言下两段都点得到、都不换行。

  Accessibility:
  - role="tablist" / role="tab" + aria-selected：这是真的「在同容器里切面板」，
    与 tab-panel 语义吻合（aria-pressed 用在普通 button 上会让部分读屏器播报为空，
    见 SourceFilterBar 注释里的同一条教训）。
  - 键盘可达性用真 <button> 免费获得，不补 tabindex / keydown。
-->
<template>
  <div class="seg-bar" role="tablist" :aria-label="ariaLabel">
    <button
      v-for="opt in options"
      :key="opt.value"
      type="button"
      role="tab"
      class="seg"
      :class="{ active: opt.value === modelValue }"
      :aria-selected="opt.value === modelValue"
      :data-testid="`seg-${opt.value}`"
      @click="$emit('update:modelValue', opt.value)"
    >
      <span v-if="opt.icon" class="material-symbols-outlined seg-icon" aria-hidden="true">{{ opt.icon }}</span>
      <span class="seg-label">{{ opt.label }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
export interface ViewSegmentOption {
  value: string
  label: string
  /** material-symbols-outlined 字形名；给图标只是辅助，含义由文字承担。 */
  icon?: string
}

withDefaults(
  defineProps<{
    modelValue: string
    options: ViewSegmentOption[]
    ariaLabel?: string
  }>(),
  { ariaLabel: '' },
)

defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<style scoped>
/*
  自己带水平内边距，不指望父级给。
  注入点 #app-chrome-sub（.chrome-sub）**没有**内边距——SourceFilterBar 之所以能
  负 margin 出血到屏幕两侧，是它拿「负边距 + 等量 padding」换来的，
  而那个 padding 只补内容、不补控件本身的背景与圆角。分段控件如果照抄负边距，
  它的胶囊形轨道两端会被屏幕切掉。
*/
.seg-bar {
  display: flex;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-3);
  margin-bottom: var(--space-2);
  border-radius: var(--radius-full);
  background: var(--bg-subtle);
}

.seg {
  /* 等分而不是按文案宽度自适应：9 种语言下两段文案长度差异很大，
     自适应会让长文案的段在 360dp 上换行或挤掉另一段的触摸热区。 */
  flex: 1 1 0;
  min-width: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  height: 32px;
  padding: 0 var(--space-2);
  border: none;
  border-radius: var(--radius-full);
  background: transparent;
  color: var(--text-secondary);
  font-size: var(--text-sm);
  font-weight: 500;
  line-height: 1.6;
  white-space: nowrap;
  overflow: hidden;
  cursor: pointer;
  position: relative;
  transition:
    background var(--duration-fast) var(--ease-out),
    color var(--duration-fast) var(--ease-out);
}

/* M3 state-layer：按下时用 currentColor 叠一层半透明，不改背景色 */
.seg:active::before {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  background: currentColor;
  opacity: 0.1;
  pointer-events: none;
}

.seg.active {
  background: var(--brand-primary);
  color: var(--text-inverse);
  font-weight: var(--font-weight-semibold);
}

.seg-label {
  overflow: hidden;
  text-overflow: ellipsis;
}

.seg-icon {
  font-size: var(--text-smd);
  line-height: 1;
}
</style>

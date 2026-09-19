<template>
  <div class="fold-aware-layout" :data-posture="posture" :data-spanning="spanning">
    <!--
      Always render both slots; CSS picks which one is visible based on
      (device-posture: folded) / (screen-spanning: single-fold-vertical) etc.
      Render is one-off — non-foldable devices (no foldable mql ever matches)
      fall through to show #inner only.
    -->
    <section class="outer-pane" aria-label="折叠外屏（约 6.5 寸）">
      <slot name="outer" />
    </section>
    <section class="inner-pane" aria-label="展开内屏（约 8 寸以上）">
      <slot name="inner" />
    </section>
  </div>
</template>

<script setup lang="ts">
/**
 * FoldAwareLayout — 折叠屏 slot-based 布局壳。
 *
 * 两个具名 slot：
 *   - #outer 折叠外屏（约 6.5 寸，紧凑布局）
 *   - #inner 展开内屏（约 8 寸以上，完整布局）
 *
 * 检测策略（自顶向下回退）：
 *   1. useDevicePosture() — 基于 W3C Device Posture API / viewport segments，
 *      在 Chrome / Edge 等支持 foldable viewport segments 的浏览器上工作。
 *   2. CSS `screen-spanning: single-fold-vertical|horizontal` — W3C 草案
 *      Media Queries Level 5 媒体查询；Chromium 已实现。
 *   3. 若两者都不支持（普通手机 / iOS / WebView），仅渲染 #inner。
 *
 * 本组件不强行 hide 另一侧 slot —— CSS 媒体查询在非折叠屏上永远不会匹配，
 * 所以 #outer 默认 display: none；只有 #inner 可见。
 *
 * 嵌入位置：由 subagent B 的 view 视情况选择包裹（如 FlashcardReviewView 在
 * 内屏展示完整四档评分按钮，外屏只展示卡片 + 下一张按钮）。
 */
import { computed } from 'vue'
import { useDevicePosture } from '../../../composables/useDevicePosture'

const { posture, segments } = useDevicePosture()

const spanning = computed(() => {
  const segs = segments.value
  if (segs.length < 2) return 'none'
  // 两个等宽竖向段 → 典型竖向折叠（vivo X Fold5、Pixel Fold 等）
  if (segs[0].y === segs[1].y && Math.abs(segs[0].width - segs[1].width) < 2) return 'single-fold-vertical'
  // 两个等高横向段 → 横向折叠（Galaxy Z Flip 等翻盖机）
  if (segs[0].x === segs[1].x && Math.abs(segs[0].height - segs[1].height) < 2) return 'single-fold-horizontal'
  return 'multi'
})

defineExpose({ posture, spanning })
</script>

<style scoped>
.fold-aware-layout {
  display: block;
  min-height: 0;
  width: 100%;
}

.outer-pane {
  display: none;
}

.inner-pane {
  display: block;
}

/* ——— foldable 渲染分支 ———
 * 1) W3C device-posture API（Chrome/Edge foldable preview）
 *    folded posture: 显示外屏；其它显示内屏。
 * 2) viewport segments: 两个 segments → 折叠中，按 `spanning` 决定内外屏。
 * 3) 屏幕跨越媒体查询（screen-spanning CSS）：draft spec，Chromium 已支持。
 */
@media (device-posture: folded) {
  .fold-aware-layout .outer-pane { display: block; }
  .fold-aware-layout .inner-pane { display: none; }
}

@media (vertical-viewport-segments: 2), (horizontal-viewport-segments: 2) {
  .fold-aware-layout .outer-pane { display: block; }
  .fold-aware-layout .inner-pane { display: none; }
}

/* screen-spanning CSS — 兜底分支。Chromium 已实现 single-fold-* */
@supports (screen-spanning: single-fold-vertical) {
  @media (screen-spanning: single-fold-vertical) {
    .fold-aware-layout .outer-pane { display: block; }
    .fold-aware-layout .inner-pane { display: none; }
  }
  @media (screen-spanning: single-fold-horizontal) {
    .fold-aware-layout .outer-pane { display: block; }
    .fold-aware-layout .inner-pane { display: none; }
  }
  @media (screen-spanning: none) {
    .fold-aware-layout .outer-pane { display: none; }
    .fold-aware-layout .inner-pane { display: block; }
  }
}
</style>
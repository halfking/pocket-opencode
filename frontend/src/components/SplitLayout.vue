<template>
  <div class="split-layout" :style="splitVars">
    <aside class="master-pane">
      <slot name="master" />
    </aside>
    <section class="detail-pane">
      <slot name="detail" />
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useDevicePosture } from '../composables/useDevicePosture'

// 折叠屏铰链对齐：垂直铰链时，detail pane 从铰线右缘起步
const { hingeRect, hingeOrientation } = useDevicePosture()
const splitVars = computed(() => {
  if (hingeOrientation.value !== 'vertical' || !hingeRect.value) return {}
  // master 宽度收敛到铰线左侧空间；detail 从铰线右侧起步
  return {
    '--fold-master-width': `${hingeRect.value.x}px`,
  }
})
</script>

<style scoped>
.split-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  min-height: 0;
  width: 100%;
  gap: var(--space-3);
}

.master-pane,
.detail-pane {
  min-width: 0;
}

.detail-pane {
  display: none;
}

@media (min-width: 840px) {
  .split-layout {
    grid-template-columns: minmax(260px, 0.38fr) minmax(0, 0.62fr);
    align-items: start;
  }
  /* 折叠屏垂直铰链：让 master 宽度贴合铰链左侧，detail 自然从铰链右侧起步，
     避免任何内容延伸至铰线下。 */
  .split-layout[style*="--fold-master-width"] {
    grid-template-columns: var(--fold-master-width) minmax(0, 1fr);
    column-gap: 0;
  }

  .detail-pane {
    display: block;
    min-height: 100%;
  }
}

@media (min-width: 1280px) {
  .split-layout {
    grid-template-columns: minmax(300px, 0.34fr) minmax(0, 0.66fr);
    gap: var(--space-4);
  }
}
</style>

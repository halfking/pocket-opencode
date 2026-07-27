<template>
  <div class="split-layout">
    <aside class="master-pane">
      <slot name="master" />
    </aside>
    <section class="detail-pane">
      <slot name="detail" />
    </section>
  </div>
</template>

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

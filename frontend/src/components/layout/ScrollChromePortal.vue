<template>
  <Teleport v-if="enabled" to="#app-chrome-sub">
    <div class="chrome-sub-panel">
      <slot />
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const enabled = computed(() => route.meta.hideAppHeader !== true)
</script>

<style scoped>
.chrome-sub-panel {
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}
</style>

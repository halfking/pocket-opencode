<template>
  <!-- KeepAlive 下组件停用时 Teleport DOM 不会自动摘除，会让上一页的
       chips/工具栏残留在新页面上方。失活时移入隐藏容器（slot 状态保留）。 -->
  <Teleport v-if="enabled" :to="active ? '#app-chrome-sub' : HIDDEN_TARGET">
    <div class="chrome-sub-panel">
      <slot />
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onActivated, onDeactivated, ref } from 'vue'
import { useRoute } from 'vue-router'

/** 失活页 chrome 的隐藏 parking 容器（模块级唯一，body 直挂 display:none）。 */
const HIDDEN_TARGET = '#app-chrome-sub-hidden'
function ensureHiddenTarget() {
  if (typeof document === 'undefined') return
  if (!document.getElementById(HIDDEN_TARGET.slice(1))) {
    const el = document.createElement('div')
    el.id = HIDDEN_TARGET.slice(1)
    el.style.display = 'none'
    document.body.appendChild(el)
  }
}
ensureHiddenTarget()

const route = useRoute()
const enabled = computed(
  () => route.meta.showTopBar !== false && route.meta.hideAppHeader !== true,
)

const active = ref(true)
onActivated(() => { active.value = true })
onDeactivated(() => { active.value = false })
</script>

<style scoped>
.chrome-sub-panel {
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}
</style>

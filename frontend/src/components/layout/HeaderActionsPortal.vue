<!--
  HeaderActionsPortal — 页面把"标题栏右侧操作"注入到 AppLayout 全局 top-bar。
  目的是消灭页面自绘 header（双标题栏 bug），让所有页面的"返回/标题/右侧操作"
  都收敛到唯一壳层。

  用法（页面内）：
    <HeaderActionsPortal>
      <button class="header-action-btn" @click="onEdit">编辑</button>
    </HeaderActionsPortal>

  样式约定（不强写进组件，但页面应遵守）：
    - 所有按钮 44×44 触摸热区，或 min-height: 36px 文字按钮
    - 图标按钮用 material-symbols-outlined, 20px
    - 最多 2 个 icon 按钮 + 1 个文字按钮，超出收进 ⋮ overflow

  渲染条件：
    - showTopBar !== false 且 hideAppHeader !== true
    - 注入点 #app-header-actions 在 AppLayout 内随壳层挂载/卸载出现/消失。
-->
<template>
  <Teleport v-if="enabled && mountAvailable" to="#app-header-actions">
    <slot />
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const mountAvailable = ref(false)
const enabled = computed(() =>
  route.meta.showTopBar !== false && route.meta.hideAppHeader !== true,
)

onMounted(() => {
  mountAvailable.value = Boolean(document.getElementById('app-header-actions'))
})
</script>

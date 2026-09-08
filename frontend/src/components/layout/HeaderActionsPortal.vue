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

  KeepAlive：缓存页失活时 Teleport DOM 不会自动摘除，上一页的标题栏按钮会
  残留到新页面。用动态 to 把失活页的内容移入隐藏容器（slot 状态保留、
  不破坏 .header-actions > * 直接子元素样式）。
-->
<template>
  <Teleport v-if="enabled && mountAvailable" :to="active ? '#app-header-actions' : HIDDEN_TARGET">
    <slot />
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onActivated, onDeactivated, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'

/** 失活页按钮的隐藏 parking 容器（模块级唯一，body 直挂 display:none）。 */
const HIDDEN_TARGET = '#app-header-actions-hidden'
function ensureHiddenTarget() {
  if (typeof document === 'undefined') return
  if (!document.getElementById(HIDDEN_TARGET.slice(1))) {
    const el = document.createElement('div')
    el.id = HIDDEN_TARGET.slice(1)
    el.style.display = 'none'
    document.body.appendChild(el)
  }
}

const route = useRoute()
const mountAvailable = ref(false)
const enabled = computed(() =>
  route.meta.showTopBar !== false && route.meta.hideAppHeader !== true,
)

const active = ref(true)
onActivated(() => { active.value = true })
onDeactivated(() => { active.value = false })

onMounted(() => {
  ensureHiddenTarget()
  mountAvailable.value = Boolean(document.getElementById('app-header-actions'))
})
</script>

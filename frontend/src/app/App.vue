<template>
  <div id="app">
    <!--
      ✅ 修复：用 AppLayout 包裹 router-view，让共享的 TopBar + BottomNav 全局生效。
      否则每个 view 都要自己实现顶栏/底栏，会出现重复 UI 或不一致（如之前的
      任务/会话/实例/设置 旧 4模块 Tab 遮住了设计的 5模块 BottomNav）。
    -->
    <AppLayout>
      <router-view />
    </AppLayout>
    <UpdateChecker ref="updateChecker" />
    <!-- 全局确认弹窗：useConfirm().confirm() 的唯一渲染挂载点 -->
    <ConfirmDialog />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import AppLayout from './AppLayout.vue'
import UpdateChecker from '../components/UpdateChecker.vue'
import ConfirmDialog from '../components/base/ConfirmDialog.vue'
import { useSwipeBack } from '../composables/useSwipeBack'
import { useStatusBar } from '../composables/useStatusBar'

const updateChecker = ref<InstanceType<typeof UpdateChecker> | null>(null)

// Phase 4.3: 全局挂载左缘右滑返回手势（仅 route.meta.canGoBack 启用）
useSwipeBack({ edgeWidth: 24, thresholdRatio: 0.3, velocityThreshold: 0.4 })

// 状态栏控制权绑定到 App 生命周期：进入页面 start（注册主题监听），
// 卸载时 stop（清除监听），避免热更新 / 测试时累积孤儿监听器。
const statusBar = useStatusBar()
onMounted(() => {
  statusBar.start()
  console.log('OpenCode Pocket Mobile Started')
})
onBeforeUnmount(() => {
  statusBar.stop()
})
</script>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

html {
  height: 100%;
}

body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
    "Helvetica Neue", Arial, sans-serif;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  /* 走 token 以跟随亮/暗皮肤（styles.css 有同款规则，此处去硬编码） */
  background: var(--bg-base, #f7f7fa);
  color: var(--text-primary, #0a0a0a);
  height: 100%;
  overflow: hidden;
}

#app {
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

input, textarea, select, button {
  font-family: inherit;
}

input:focus, textarea:focus, select:focus {
  outline: none;
}

/* 滚动条样式 */
::-webkit-scrollbar {
  width: 6px;
  height: 6px;
}

::-webkit-scrollbar-thumb {
  background: var(--border-strong, #d1d5db);
  border-radius: 3px;
}

::-webkit-scrollbar-thumb:hover {
  background: var(--text-muted, #a3a3a3);
}

/* 触摸反馈 */
button:active {
  opacity: 0.8;
}
</style>

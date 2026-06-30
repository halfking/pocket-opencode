<template>
  <div id="app">
    <router-view />
    <UpdateChecker ref="updateChecker" />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import UpdateChecker from '../components/UpdateChecker.vue'

const updateChecker = ref<InstanceType<typeof UpdateChecker> | null>(null)

onMounted(() => {
  // 应用启动时自动检查更新
  console.log('OpenCode Pocket Mobile Started')
})
</script>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, 
    "Helvetica Neue", Arial, sans-serif;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  background: #f5f7fa;
}

#app {
  min-height: 100vh;
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
  background: #ccc;
  border-radius: 3px;
}

::-webkit-scrollbar-thumb:hover {
  background: #999;
}

/* 触摸反馈 */
button:active {
  opacity: 0.8;
}
</style>

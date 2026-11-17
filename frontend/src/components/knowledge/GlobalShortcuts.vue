<template>
  <div>
    <!-- 快捷键帮助对话框 -->
    <KeyboardShortcutsHelp
      :show="showHelp"
      @close="showHelp = false"
    />
  </div>
</template>

<script setup lang="ts">
/**
 * GlobalShortcuts - 全局快捷键集成组件
 * 
 * 在 App.vue 中引入此组件即可启用全局快捷键
 */

import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useKeyboardShortcuts } from '@/composables/useKeyboardShortcuts'
import KeyboardShortcutsHelp from './KeyboardShortcutsHelp.vue'

const router = useRouter()
const { register } = useKeyboardShortcuts()

// 状态
const showHelp = ref(false)

// 定义快捷键
onMounted(() => {
  // Cmd/Ctrl+K: 全局搜索
  register({
    key: 'k',
    meta: true,  // Mac: Cmd, Windows: Ctrl (自动适配)
    description: '全局搜索',
    category: '导航',
    handler: () => {
      router.push('/knowledge/search')
      // 触发搜索输入框聚焦
      setTimeout(() => {
        const searchInput = document.querySelector<HTMLInputElement>('.search-input, input[type="search"]')
        searchInput?.focus()
      }, 100)
    }
  })

  // Cmd/Ctrl+N: 快速创建笔记
  register({
    key: 'n',
    meta: true,
    description: '创建新笔记',
    category: '编辑',
    handler: () => {
      router.push('/knowledge/notes/new')
    }
  })

  // Cmd/Ctrl+P: 命令面板
  register({
    key: 'p',
    meta: true,
    description: '打开命令面板',
    category: '导航',
    handler: () => {
      // TODO: 打开命令面板
      console.log('Command palette not implemented yet')
      alert('命令面板功能即将推出')
    }
  })

  // Cmd/Ctrl+?: 快捷键帮助
  register({
    key: '?',
    meta: true,
    description: '显示快捷键帮助',
    category: '帮助',
    handler: () => {
      showHelp.value = true
    }
  })

  // 单独的 ? 键也可以打开帮助（不在输入框中时）
  register({
    key: '?',
    description: '显示快捷键帮助',
    category: '帮助',
    handler: () => {
      showHelp.value = true
    }
  })

  // ESC: 关闭当前弹窗/返回
  register({
    key: 'Escape',
    description: '关闭弹窗或返回',
    category: '导航',
    handler: () => {
      // 如果有打开的对话框，关闭它
      if (showHelp.value) {
        showHelp.value = false
        return
      }
      // 否则尝试返回上一页
      if (window.history.length > 1) {
        router.back()
      }
    }
  })

  // Cmd/Ctrl+S: 保存（如果在编辑器中）
  register({
    key: 's',
    meta: true,
    description: '保存当前笔记',
    category: '编辑',
    handler: () => {
      // 触发保存事件
      const saveEvent = new CustomEvent('global-save')
      window.dispatchEvent(saveEvent)
    }
  })

  // Cmd/Ctrl+B: 加粗
  register({
    key: 'b',
    meta: true,
    description: '加粗文本',
    category: '编辑',
    handler: () => {
      document.execCommand('bold', false)
    }
  })

  // Cmd/Ctrl+I: 斜体
  register({
    key: 'i',
    meta: true,
    description: '斜体文本',
    category: '编辑',
    handler: () => {
      document.execCommand('italic', false)
    }
  })

  // Cmd/Ctrl+/: 显示/隐藏侧边栏
  register({
    key: '/',
    meta: true,
    description: '切换侧边栏',
    category: '视图',
    handler: () => {
      const sidebarEvent = new CustomEvent('toggle-sidebar')
      window.dispatchEvent(sidebarEvent)
    }
  })
})
</script>

<style scoped>
/* 无需样式 */
</style>

/**
 * useKeyboardShortcuts - 全局快捷键管理
 * 
 * 功能:
 * - Cmd/Ctrl+K: 全局搜索
 * - Cmd/Ctrl+N: 快速创建笔记
 * - Cmd/Ctrl+P: 命令面板
 * - Cmd/Ctrl+?: 快捷键帮助
 */

import { onMounted, onUnmounted, ref } from 'vue'

export interface ShortcutAction {
  key: string
  ctrl?: boolean
  alt?: boolean
  shift?: boolean
  meta?: boolean
  description: string
  handler: () => void
  category?: string
}

export interface ShortcutCategory {
  name: string
  shortcuts: ShortcutAction[]
}

export function useKeyboardShortcuts() {
  const isEnabled = ref(true)
  const shortcuts = ref<Map<string, ShortcutAction>>(new Map())

  /**
   * 注册快捷键
   */
  const register = (action: ShortcutAction) => {
    const key = buildKey(action)
    shortcuts.value.set(key, action)
  }

  /**
   * 注销快捷键
   */
  const unregister = (action: ShortcutAction) => {
    const key = buildKey(action)
    shortcuts.value.delete(key)
  }

  /**
   * 构建快捷键唯一标识
   */
  const buildKey = (action: ShortcutAction): string => {
    const parts: string[] = []
    if (action.ctrl) parts.push('ctrl')
    if (action.alt) parts.push('alt')
    if (action.shift) parts.push('shift')
    if (action.meta) parts.push('meta')
    parts.push(action.key.toLowerCase())
    return parts.join('+')
  }

  /**
   * 从键盘事件构建键标识
   */
  const buildKeyFromEvent = (e: KeyboardEvent): string => {
    const parts: string[] = []
    if (e.ctrlKey) parts.push('ctrl')
    if (e.altKey) parts.push('alt')
    if (e.shiftKey) parts.push('shift')
    if (e.metaKey) parts.push('meta')
    parts.push(e.key.toLowerCase())
    return parts.join('+')
  }

  /**
   * 检查是否应该忽略快捷键
   */
  const shouldIgnore = (e: KeyboardEvent): boolean => {
    if (!isEnabled.value) return true

    const target = e.target as HTMLElement
    const tagName = target.tagName.toLowerCase()
    
    // 在输入框、文本域等元素中，某些快捷键应该被忽略
    if (['input', 'textarea', 'select'].includes(tagName)) {
      // 但 Cmd+K 和 Cmd+P 等全局快捷键不应被忽略
      const key = buildKeyFromEvent(e)
      if (key === 'meta+k' || key === 'ctrl+k' || 
          key === 'meta+p' || key === 'ctrl+p') {
        return false
      }
      return true
    }

    // 在 contenteditable 元素中也应忽略
    if (target.isContentEditable) {
      const key = buildKeyFromEvent(e)
      if (key === 'meta+k' || key === 'ctrl+k' || 
          key === 'meta+p' || key === 'ctrl+p') {
        return false
      }
      return true
    }

    return false
  }

  /**
   * 键盘事件处理器
   */
  const handleKeyDown = (e: KeyboardEvent) => {
    if (shouldIgnore(e)) return

    const key = buildKeyFromEvent(e)
    const action = shortcuts.value.get(key)

    if (action) {
      e.preventDefault()
      e.stopPropagation()
      action.handler()
    }
  }

  /**
   * 启用快捷键
   */
  const enable = () => {
    isEnabled.value = true
  }

  /**
   * 禁用快捷键
   */
  const disable = () => {
    isEnabled.value = false
  }

  /**
   * 获取所有快捷键（按分类）
   */
  const getAllShortcuts = (): ShortcutCategory[] => {
    const categories: Map<string, ShortcutAction[]> = new Map()

    shortcuts.value.forEach((action) => {
      const category = action.category || '其他'
      if (!categories.has(category)) {
        categories.set(category, [])
      }
      categories.get(category)!.push(action)
    })

    return Array.from(categories.entries()).map(([name, shortcuts]) => ({
      name,
      shortcuts
    }))
  }

  /**
   * 格式化快捷键显示
   */
  const formatShortcut = (action: ShortcutAction): string => {
    const parts: string[] = []
    const isMac = navigator.platform.toUpperCase().indexOf('MAC') >= 0

    if (action.meta) parts.push(isMac ? '⌘' : 'Ctrl')
    if (action.ctrl && !action.meta) parts.push('Ctrl')
    if (action.alt) parts.push(isMac ? '⌥' : 'Alt')
    if (action.shift) parts.push(isMac ? '⇧' : 'Shift')
    parts.push(action.key.toUpperCase())

    return parts.join(isMac ? '' : '+')
  }

  // 挂载时添加事件监听
  onMounted(() => {
    document.addEventListener('keydown', handleKeyDown)
  })

  // 卸载时移除事件监听
  onUnmounted(() => {
    document.removeEventListener('keydown', handleKeyDown)
  })

  return {
    register,
    unregister,
    enable,
    disable,
    isEnabled,
    getAllShortcuts,
    formatShortcut
  }
}

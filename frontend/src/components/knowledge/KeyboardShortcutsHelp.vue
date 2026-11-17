<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="show" class="modal-overlay" @click="handleClose">
        <div class="help-dialog" @click.stop>
          <!-- 头部 -->
          <div class="dialog-header">
            <h2>⌨️ 快捷键</h2>
            <button @click="handleClose" class="close-btn" aria-label="关闭">✕</button>
          </div>

          <!-- 内容 -->
          <div class="dialog-body">
            <div
              v-for="category in categories"
              :key="category.name"
              class="shortcut-category"
            >
              <h3 class="category-name">{{ category.name }}</h3>
              <div class="shortcuts-list">
                <div
                  v-for="(shortcut, index) in category.shortcuts"
                  :key="index"
                  class="shortcut-item"
                >
                  <span class="shortcut-description">{{ shortcut.description }}</span>
                  <kbd class="shortcut-keys">{{ formatShortcut(shortcut) }}</kbd>
                </div>
              </div>
            </div>
          </div>

          <!-- 底部 -->
          <div class="dialog-footer">
            <p class="footer-hint">按 <kbd>ESC</kbd> 或 <kbd>?</kbd> 关闭此窗口</p>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, watch, onMounted, onUnmounted } from 'vue'
import { useKeyboardShortcuts } from '@/composables/useKeyboardShortcuts'

interface Props {
  show: boolean
}

const props = defineProps<Props>()
const emit = defineEmits<{
  close: []
}>()

const { getAllShortcuts, formatShortcut } = useKeyboardShortcuts()

const categories = computed(() => getAllShortcuts())

const handleClose = () => {
  emit('close')
}

// ESC 键关闭
const handleKeyDown = (e: KeyboardEvent) => {
  if (props.show && (e.key === 'Escape' || e.key === '?')) {
    e.preventDefault()
    handleClose()
  }
}

onMounted(() => {
  document.addEventListener('keydown', handleKeyDown)
})

onUnmounted(() => {
  document.removeEventListener('keydown', handleKeyDown)
})
</script>

<style scoped>
.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 2000;
  padding: 1rem;
}

.help-dialog {
  background: white;
  border-radius: 0.75rem;
  box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.1);
  max-width: 700px;
  width: 100%;
  max-height: 80vh;
  display: flex;
  flex-direction: column;
}

/* 头部 */
.dialog-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 1.5rem;
  border-bottom: 1px solid #e5e7eb;
}

.dialog-header h2 {
  margin: 0;
  font-size: 1.25rem;
  font-weight: 600;
  color: #111827;
}

.close-btn {
  background: none;
  border: none;
  font-size: 1.5rem;
  color: #6b7280;
  cursor: pointer;
  padding: 0;
  width: 2rem;
  height: 2rem;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 0.375rem;
  transition: all 0.2s;
}

.close-btn:hover {
  background-color: #f3f4f6;
  color: #111827;
}

/* 主体 */
.dialog-body {
  flex: 1;
  overflow-y: auto;
  padding: 1.5rem;
}

.shortcut-category {
  margin-bottom: 2rem;
}

.shortcut-category:last-child {
  margin-bottom: 0;
}

.category-name {
  margin: 0 0 1rem 0;
  font-size: 0.875rem;
  font-weight: 600;
  color: #6b7280;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.shortcuts-list {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.shortcut-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0.75rem 1rem;
  background-color: #f9fafb;
  border-radius: 0.5rem;
  transition: background-color 0.2s;
}

.shortcut-item:hover {
  background-color: #f3f4f6;
}

.shortcut-description {
  font-size: 0.9375rem;
  color: #111827;
}

.shortcut-keys {
  font-family: var(--font-mono);
  font-size: 0.875rem;
  color: #374151;
  background-color: white;
  border: 1px solid #d1d5db;
  border-radius: 0.375rem;
  padding: 0.25rem 0.5rem;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
}

/* 底部 */
.dialog-footer {
  padding: 1rem 1.5rem;
  border-top: 1px solid #e5e7eb;
  background-color: #f9fafb;
}

.footer-hint {
  margin: 0;
  font-size: 0.8125rem;
  color: #6b7280;
  text-align: center;
}

.footer-hint kbd {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  background-color: white;
  border: 1px solid #d1d5db;
  border-radius: 0.25rem;
  padding: 0.125rem 0.375rem;
  margin: 0 0.125rem;
}

/* 过渡动画 */
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.3s;
}

.modal-enter-active .help-dialog,
.modal-leave-active .help-dialog {
  transition: transform 0.3s, opacity 0.3s;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-from .help-dialog,
.modal-leave-to .help-dialog {
  transform: scale(0.95);
  opacity: 0;
}

/* 响应式 */
@media (max-width: 768px) {
  .shortcut-item {
    flex-direction: column;
    align-items: flex-start;
    gap: 0.5rem;
  }
}
</style>

<!--
  ConfirmDialog — 全局唯一确认弹窗（替代 window.confirm）。
  用法：模板中放置一次，然后 const ok = await confirmRef.ask({...}) 或
  配合 useConfirm() composable 以 Promise 方式调用。

  破坏型操作传 danger: true（确认键呈红色）。
-->
<template>
  <Dialog
    :model-value="state.visible"
    :title="state.title"
    size="small"
    :confirm-text="state.confirmText"
    :cancel-text="state.cancelText"
    :confirm-button-variant="state.danger ? 'danger' : 'primary'"
    :loading="state.loading"
    close-on-overlay
    @update:model-value="handleVisibility"
    @confirm="handleConfirm"
    @cancel="handleCancel"
  >
    <p class="confirm-message">{{ state.message }}</p>
  </Dialog>
</template>

<script setup lang="ts">
import { reactive, onMounted, onUnmounted } from 'vue'
import Dialog from './Dialog.vue'
import { registerConfirmHandler, unregisterConfirmHandler } from '../../composables/useConfirm'

export interface ConfirmOptions {
  title?: string
  message: string
  confirmText?: string
  cancelText?: string
  /** 破坏性操作传 true，确认按钮变红 */
  danger?: boolean
  loading?: boolean
}

interface ConfirmState extends Required<Omit<ConfirmOptions, 'loading'>> {
  visible: boolean
  loading: boolean
}

const state = reactive<ConfirmState>({
  visible: false,
  title: '确认操作',
  message: '',
  confirmText: '确定',
  cancelText: '取消',
  danger: false,
  loading: false,
})

let resolver: ((v: boolean) => void) | null = null

/**
 * 打开确认框并等待用户选择。
 * 同时只允许一个确认请求；重复调用会先把上一个 resolve(false)。
 */
function ask(options: ConfirmOptions): Promise<boolean> {
  if (resolver) {
    resolver(false)
    resolver = null
  }
  state.title = options.title ?? '确认操作'
  state.message = options.message
  state.confirmText = options.confirmText ?? '确定'
  state.cancelText = options.cancelText ?? '取消'
  state.danger = options.danger ?? false
  state.loading = options.loading ?? false
  state.visible = true
  return new Promise<boolean>((resolve) => {
    resolver = resolve
  })
}

function settle(value: boolean) {
  const r = resolver
  resolver = null
  state.visible = false
  r?.(value)
}

function handleConfirm() {
  settle(true)
}
function handleCancel() {
  settle(false)
}
function handleVisibility(v: boolean) {
  if (!v) settle(false)
}

onMounted(() => registerConfirmHandler(ask))
onUnmounted(() => {
  settle(false)
  unregisterConfirmHandler(ask)
})

defineExpose({ ask })
</script>

<style scoped>
.confirm-message {
  margin: 0;
  color: var(--color-text-secondary);
  font-size: var(--text-md);
  line-height: 1.6;
  word-break: break-word;
  white-space: pre-wrap;
}
</style>

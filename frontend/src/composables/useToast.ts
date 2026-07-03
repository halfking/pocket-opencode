/**
 * useToast - Toast 组件的 Composition API
 * 
 * 使用方式：
 *   const toast = useToast()
 *   toast.success('操作成功')
 *   toast.error('操作失败')
 */

import { createApp, h } from 'vue'
import Toast, { type ToastProps } from '@/components/base/Toast.vue'

interface ToastOptions extends Omit<ToastProps, 'message'> {
  message: string
}

class ToastManager {
  private container: HTMLDivElement | null = null

  private getContainer(): HTMLDivElement {
    if (!this.container) {
      this.container = document.createElement('div')
      this.container.id = 'toast-container'
      document.body.appendChild(this.container)
    }
    return this.container
  }

  show(options: ToastOptions) {
    const container = this.getContainer()
    const mountNode = document.createElement('div')
    container.appendChild(mountNode)

    const onClose = () => {
      app.unmount()
      container.removeChild(mountNode)
      options.onClose?.()
    }

    const app = createApp({
      render() {
        return h(Toast, {
          ...options,
          onClose,
        })
      },
    })

    app.mount(mountNode)
  }

  success(message: string, options?: Partial<ToastOptions>) {
    this.show({
      message,
      type: 'success',
      ...options,
    })
  }

  error(message: string, options?: Partial<ToastOptions>) {
    this.show({
      message,
      type: 'error',
      ...options,
    })
  }

  warning(message: string, options?: Partial<ToastOptions>) {
    this.show({
      message,
      type: 'warning',
      ...options,
    })
  }

  info(message: string, options?: Partial<ToastOptions>) {
    this.show({
      message,
      type: 'info',
      ...options,
    })
  }
}

const toastManager = new ToastManager()

export function useToast() {
  return toastManager
}

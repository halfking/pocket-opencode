/**
 * 原生运行时权限（相机 / 相册等）的检查与再次申请。
 * 状态以 AppSettings 插件为准，不走 getUserMedia，避免把「未授权」误标成「不支持」。
 */
import { computed, ref, type Ref } from 'vue'
import { Capacitor } from '@capacitor/core'
import { useAppSettings, type RuntimePermissionName } from './useAppSettings'
import { canRequestPermissionAgain, type PermissionStatus } from './permission-action'
import { permissionStatusClass, permissionStatusLabel } from './permission-settings'

export type NativePermissionState = PermissionStatus | 'unknown'

interface NativePermissionStore {
  state: Ref<NativePermissionState>
  canRequestAgain: Ref<boolean>
}

const stores = new Map<RuntimePermissionName, NativePermissionStore>()

function storeOf(name: RuntimePermissionName): NativePermissionStore {
  let store = stores.get(name)
  if (!store) {
    store = {
      state: ref<NativePermissionState>('unknown'),
      canRequestAgain: ref(true),
    }
    stores.set(name, store)
  }
  return store
}

function applyStatus(store: NativePermissionStore, status: PermissionStatus) {
  store.state.value = status
  store.canRequestAgain.value = canRequestPermissionAgain(status)
}

export function useNativePermission(name: RuntimePermissionName) {
  const appSettings = useAppSettings()
  const store = storeOf(name)
  const label = computed(() => permissionStatusLabel(store.state.value, store.canRequestAgain.value))
  const stateClass = computed(() => permissionStatusClass(store.state.value))

  async function recheck(): Promise<NativePermissionState> {
    if (!Capacitor.isNativePlatform()) {
      store.state.value = 'unavailable'
      store.canRequestAgain.value = false
      return store.state.value
    }
    const result = await appSettings.checkPermission(name)
    if (result) applyStatus(store, result.status)
    return store.state.value
  }

  async function ensure(): Promise<NativePermissionState> {
    if (store.state.value === 'granted') return 'granted'
    if (!Capacitor.isNativePlatform()) {
      store.state.value = 'unavailable'
      store.canRequestAgain.value = false
      return store.state.value
    }
    const result = await appSettings.requestPermission(name)
    if (result) applyStatus(store, result.status)
    return store.state.value
  }

  return {
    state: store.state,
    canRequestAgain: store.canRequestAgain,
    label,
    stateClass,
    recheck,
    ensure,
  }
}

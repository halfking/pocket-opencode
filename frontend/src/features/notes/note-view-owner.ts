import { nextTick, onBeforeUnmount, watch } from 'vue'
import { useAuthStore } from '../../stores/auth'
import { markListDirty } from '../../composables/list-scene-store'

/** Revoke view results synchronously, including cached KeepAlive views and A→B→A. */
export function useNotesOwnerView(clear: () => void, reload: () => unknown, listScope?: string, remoteReload?: () => unknown, eventName = 'openpocket:portal-notes-applied') {
  const auth = useAuthStore()
  let epoch = {}
  const capture = () => epoch
  const isCurrent = (stamp: object) => stamp === epoch && auth.activationStatus === 'ready'
  const refresh = (read = reload) => {
    const stamp = epoch
    void nextTick(async () => {
      if (!isCurrent(stamp)) return
      try { await read() } catch (error) {
        if (isCurrent(stamp)) console.warn('[notes-view] 当前视图刷新失败：', error)
      }
    })
  }
  watch(() => [auth.activationVersion, auth.userId, auth.workspaceId, auth.activationStatus], () => {
    epoch = {}
    clear()
    if (listScope) markListDirty(listScope)
    refresh()
  }, { flush: 'sync' })
  const remote = () => {
    if (!listScope && !remoteReload) return
    if (listScope) markListDirty(listScope)
    refresh(remoteReload ?? reload)
  }
  if (typeof window !== 'undefined') window.addEventListener(eventName, remote)
  onBeforeUnmount(() => { epoch = {}; if (typeof window !== 'undefined') window.removeEventListener(eventName, remote) })
  return { capture, isCurrent }
}

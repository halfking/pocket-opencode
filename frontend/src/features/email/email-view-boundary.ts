import { computed, nextTick, ref, watch } from 'vue'

const MAIL_VIEWS = new Set(['EmailInboxView', 'InvoiceListView', 'EmailSummaryView'])
export function isMailViewPath(path: string): boolean {
  return path === '/email' || path.startsWith('/email/')
}

/** Evict only mail views, including inactive KeepAlive entries, on identity changes. */
export function useEmailViewBoundary(
  auth: { activationStatus: string; activationVersion: number; userId: string; workspaceId: string },
  cacheNames: string[],
) {
  const resetting = ref(false)
  let generation = 0
  watch(() => [auth.activationStatus, auth.activationVersion, auth.userId, auth.workspaceId], () => {
    const ticket = ++generation
    resetting.value = true
    // Let KeepAlive observe the exclusion before the next owner can reuse a route.
    void nextTick(() => { if (ticket === generation) resetting.value = false })
  }, { flush: 'sync' })
  const mailReady = computed(() => auth.activationStatus === 'ready' && !resetting.value)
  return {
    cacheNames: computed(() => mailReady.value ? cacheNames : cacheNames.filter(name => !MAIL_VIEWS.has(name))),
    canRender: (path: string) => !isMailViewPath(path) || mailReady.value,
    viewKey: (path: string) => isMailViewPath(path) ? JSON.stringify([path, auth.activationVersion, auth.userId, auth.workspaceId]) : path,
  }
}

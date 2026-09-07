import { onMounted, onUnmounted, ref, watch } from 'vue'

/** 列表底部交叉观察：露出时拉下一页。 */
export function useListSentinel(loadMore: () => void | Promise<void>) {
  const moreEl = ref<HTMLElement | null>(null)
  let obs: IntersectionObserver | null = null
  onMounted(() => {
    obs = new IntersectionObserver((ents) => {
      if (ents.some((e) => e.isIntersecting)) void loadMore()
    }, { rootMargin: '120px' })
  })
  watch(moreEl, (el, prev) => {
    if (!obs) return
    if (prev) obs.unobserve(prev)
    if (el) obs.observe(el)
  })
  onUnmounted(() => obs?.disconnect())
  return { moreEl }
}

<template>
  <component :is="tag" ref="rootRef" class="stagger-list">
    <slot />
  </component>
</template>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, nextTick, watchEffect } from 'vue'

export interface StaggerListProps {
  /** 容器 tag（默认 div，可换 ul/ol/section 等） */
  tag?: string
  /** 每项延迟基数，单位 ms，默认 50ms */
  step?: number
  /** 首项延迟 */
  initial?: number
  /** 单一动画时长 */
  duration?: number
  /** 动画距离，px */
  distance?: number
  /** true 时停留在视口才动 */
  whenInView?: boolean
  /** 与 view 的 IntersectionObserver root 选择 */
  rootMargin?: string
  /** 自定义 threshold */
  threshold?: number
  /** 触发后多久重置（默认不重置，进入即触发） */
  retriggerOnKey?: boolean
}

const props = withDefaults(defineProps<StaggerListProps>(), {
  tag: 'div',
  step: 50,
  initial: 60,
  duration: 380,
  distance: 14,
  whenInView: true,
  rootMargin: '0px 0px -10% 0px',
  threshold: 0.05,
  retriggerOnKey: false,
})

const rootRef = ref<HTMLElement>()

let mounted = false
let key = 0
let observer: IntersectionObserver | null = null

watchEffect(() => {
  if (props.retriggerOnKey) key++
})

function reveal() {
  const root = rootRef.value
  if (!root) return
  // 应用到所有直接子元素：slot 的所有 .stagger-item 直接 children。
  // 这里用 :scope > * 抓全部直系子元素（包括 transition group 内）。
  const children = Array.from(root.children) as HTMLElement[]
  const reduceMotion =
    typeof window !== 'undefined' &&
    window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

  children.forEach((child, idx) => {
    if (reduceMotion) {
      child.style.opacity = '1'
      child.style.transform = 'none'
      child.style.transition = 'none'
      return
    }
    const delay = props.initial + idx * props.step
    child.style.transition = `opacity ${props.duration}ms cubic-bezier(0.16, 1, 0.3, 1) ${delay}ms, transform ${props.duration}ms cubic-bezier(0.16, 1, 0.3, 1) ${delay}ms`
    // 触发在下一帧设置初值然后变到末值
    requestAnimationFrame(() => {
      child.style.opacity = '1'
      child.style.transform = 'translateY(0) scale(1)'
    })
    // 标记用过的 class，避免重复
    child.classList.add('stagger-list__item--revealed')
  })
}

function prepareChildren() {
  const root = rootRef.value
  if (!root) return
  const children = Array.from(root.children) as HTMLElement[]
  for (const child of children) {
    if (child.classList.contains('stagger-list__item--revealed')) continue
    child.style.opacity = '0'
    child.style.transform = `translateY(${props.distance}px) scale(0.985)`
    child.style.willChange = 'opacity, transform'
  }
}

onMounted(async () => {
  await nextTick()
  const root = rootRef.value
  if (!root) return

  prepareChildren()
  if (!props.whenInView) {
    reveal()
    return
  }

  if (!('IntersectionObserver' in window)) {
    reveal()
    return
  }
  observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) {
          reveal()
          observer?.disconnect()
          observer = null
          break
        }
      }
    },
    { rootMargin: props.rootMargin, threshold: props.threshold },
  )
  observer.observe(root)
})

onBeforeUnmount(() => {
  observer?.disconnect()
})
</script>

<style scoped>
.stagger-list {
  /* 容器自身无样式，子项由 JS 注入透明度+位移 */
  display: contents;
}

/* display:contents 让 :scope > * 仍然以容器为根布局，不引入额外 flex/grid */
:global(.stagger-list__item--revealed) {
  /* 占位，让 reveal() 后的元素过渡干净；具体动效由 JS 注入 */
}
</style>

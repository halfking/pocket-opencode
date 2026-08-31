<!--
  BottomSheet — 统一的 L2 浮层容器。
  默认从底部出现；placement="left" 时作为全高侧边抽屉出现。
  所有业务选择器、上下文菜单、表单抽屉都应复用此组件。
-->
<template>
  <Teleport to="body">
    <Transition :name="placement === 'left' ? 'side-sheet' : 'bottom-sheet'">
      <div
        v-if="visible"
        class="bottom-sheet-overlay"
        role="presentation"
        @click="handleOverlayClick"
      >
        <section
          ref="panelEl"
          class="bottom-sheet"
          :class="sheetClasses"
          :style="panelStyle"
          role="dialog"
          aria-modal="true"
          :aria-label="ariaLabel"
          :aria-labelledby="labelledby"
          tabindex="-1"
          @click.stop
          @keydown.esc="handleEscape"
          @touchstart="handleTouchStart"
          @touchmove="handleTouchMove"
          @touchend="handleTouchEnd"
        >
          <div v-if="placement === 'bottom'" class="sheet-handle" aria-hidden="true">
            <div class="handle-bar" />
          </div>

          <div v-if="title || $slots.header" class="sheet-header">
            <slot name="header">
              <h3 class="sheet-title">{{ title }}</h3>
            </slot>
            <button
              v-if="closable"
              ref="closeButtonEl"
              class="sheet-close"
              type="button"
              :aria-label="t('common.close')"
              @click="handleClose"
            >
              <span class="material-symbols-outlined" aria-hidden="true">close</span>
            </button>
          </div>

          <div class="sheet-body">
            <slot />
          </div>

          <div v-if="$slots.footer" class="sheet-footer">
            <slot name="footer" />
          </div>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBodyScrollLock } from '../../composables/useBodyScrollLock'

const { t } = useI18n()

export interface BottomSheetProps {
  modelValue?: boolean
  /** Legacy alias retained for MeetingMetaSheet/SpeakerLabelSheet callers. */
  open?: boolean
  title?: string
  height?: 'auto' | 'half' | 'full'
  placement?: 'bottom' | 'left'
  closable?: boolean
  closeOnOverlay?: boolean
  swipeable?: boolean
  ariaLabel?: string
  labelledby?: string
}

const props = withDefaults(defineProps<BottomSheetProps>(), {
  modelValue: false,
  open: undefined,
  title: '',
  height: 'auto',
  placement: 'bottom',
  closable: true,
  closeOnOverlay: true,
  swipeable: true,
  ariaLabel: undefined,
  labelledby: undefined,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'close'): void
}>()

const visible = ref(Boolean(props.modelValue || props.open))
const dragOffset = ref(0)
const startY = ref(0)
const isDragging = ref(false)
const panelEl = ref<HTMLElement | null>(null)
const closeButtonEl = ref<HTMLButtonElement | null>(null)
const scrollLock = useBodyScrollLock()

const sheetClasses = computed(() => [
  `bottom-sheet--${props.height}`,
  `bottom-sheet--${props.placement}`,
])
const panelStyle = computed(() => {
  if (!isDragging.value || props.placement !== 'bottom') return undefined
  return { transform: `translateY(${dragOffset.value}px)` }
})

watch(
  () => [props.modelValue, props.open],
  async ([modelValue, open]) => {
    // `open` is a legacy alias; when supplied it must win over the default false modelValue.
    const val = Boolean(open !== undefined ? open : modelValue)
    visible.value = val
    if (val) {
      scrollLock.acquire()
      await nextTick()
      ;(closeButtonEl.value || panelEl.value)?.focus()
    } else {
      scrollLock.release()
      dragOffset.value = 0
      isDragging.value = false
    }
  },
  { immediate: true },
)

const handleClose = () => {
  if (!visible.value) return
  if (!visible.value) return
  visible.value = false
  emit('update:modelValue', false)
  emit('close')
}

const handleOverlayClick = () => {
  if (props.closeOnOverlay) handleClose()
}

const handleEscape = () => handleClose()

const handleTouchStart = (e: TouchEvent) => {
  if (!props.swipeable || props.placement !== 'bottom') return
  const target = e.target as HTMLElement
  if (!target.closest('.sheet-handle') && !target.closest('.sheet-header')) return
  startY.value = e.touches[0].clientY
  isDragging.value = true
}

const handleTouchMove = (e: TouchEvent) => {
  if (!isDragging.value || !props.swipeable) return
  const deltaY = e.touches[0].clientY - startY.value
  if (deltaY > 0) {
    dragOffset.value = deltaY
    e.preventDefault()
  }
}

const handleTouchEnd = () => {
  if (!isDragging.value || !props.swipeable) return
  isDragging.value = false
  if (dragOffset.value > 150) handleClose()
  else dragOffset.value = 0
}

onBeforeUnmount(() => {
  scrollLock.release()
})
</script>

<style scoped>
.bottom-sheet-overlay {
  position: fixed;
  inset: 0;
  background: var(--color-bg-overlay);
  backdrop-filter: blur(4px);
  z-index: var(--z-sheet);
  display: flex;
  align-items: flex-end;
}

.bottom-sheet {
  width: 100%;
  max-height: 90vh;
  background: var(--color-bg-surface);
  border-radius: var(--radius-xl) var(--radius-xl) 0 0;
  box-shadow: 0 -4px 20px rgba(0, 0, 0, 0.1);
  display: flex;
  flex-direction: column;
  transition: transform var(--duration-base) var(--ease-out);
  outline: none;
}

.bottom-sheet--auto { max-height: 80vh; }
.bottom-sheet--half { height: 50vh; }
.bottom-sheet--full { height: 90vh; }

.bottom-sheet--left {
  align-self: stretch;
  width: min(88vw, 360px);
  max-width: 100%;
  max-height: none;
  height: 100%;
  border-radius: 0 var(--radius-xl) var(--radius-xl) 0;
  padding-top: var(--app-safe-top);
  padding-right: 0;
  padding-bottom: var(--app-safe-bottom);
  padding-left: env(safe-area-inset-left, 0px);
}
.bottom-sheet--left .sheet-body {
  min-height: 0;
  padding-bottom: var(--space-6);
}

.sheet-handle {
  padding: var(--space-3) 0;
  display: flex;
  justify-content: center;
  cursor: grab;
  user-select: none;
  flex: 0 0 auto;
}
.sheet-handle:active { cursor: grabbing; }
.handle-bar {
  width: 40px;
  height: 4px;
  background: var(--color-border);
  border-radius: var(--radius-full);
}

.sheet-header {
  position: relative;
  padding: var(--space-4) var(--space-6);
  border-bottom: 1px solid var(--color-border);
  flex: 0 0 auto;
}
.sheet-title {
  font-size: 18px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin: 0;
  padding-right: var(--space-8);
}
.sheet-close {
  position: absolute;
  top: 50%;
  right: var(--space-4);
  transform: translateY(-50%);
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: none;
  border: none;
  border-radius: var(--radius-md);
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
}
.sheet-close:hover { background: var(--color-bg-hover); color: var(--color-text-primary); }
.sheet-close .material-symbols-outlined { font-size: 20px; }

.sheet-body {
  padding: var(--space-6);
  overflow-y: auto;
  flex: 1 1 auto;
  overscroll-behavior: contain;
}
.sheet-footer {
  padding: var(--space-4) var(--space-6);
  border-top: 1px solid var(--color-border);
  flex: 0 0 auto;
}

.bottom-sheet-enter-active,
.bottom-sheet-leave-active,
.side-sheet-enter-active,
.side-sheet-leave-active { transition: opacity var(--duration-base) var(--ease-out); }
.bottom-sheet-enter-active .bottom-sheet,
.bottom-sheet-leave-active .bottom-sheet,
.side-sheet-enter-active .bottom-sheet,
.side-sheet-leave-active .bottom-sheet { transition: transform var(--duration-base) var(--ease-out); }
.bottom-sheet-enter-from,
.bottom-sheet-leave-to,
.side-sheet-enter-from,
.side-sheet-leave-to { opacity: 0; }
.bottom-sheet-enter-from .bottom-sheet,
.bottom-sheet-leave-to .bottom-sheet { transform: translateY(100%); }
.side-sheet-enter-from .bottom-sheet,
.side-sheet-leave-to .bottom-sheet { transform: translateX(-100%); }

@media (prefers-reduced-motion: reduce) {
  .bottom-sheet,
  .bottom-sheet-enter-active,
  .bottom-sheet-leave-active,
  .side-sheet-enter-active,
  .side-sheet-leave-active { transition: none; }
}

.sheet-body,
.sheet-footer {
  padding-bottom: calc(var(--space-6) + var(--app-safe-bottom));
}
</style>

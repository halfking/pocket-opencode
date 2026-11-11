<!--
  LiveSubtitleOverlay — 实时字幕悬浮窗（Cicada 启发）
  
  特性：
  - 实时显示会议转写片段
  - 可选双语字幕（原文 + 翻译）
  - 悬浮置顶（Android 悬浮窗 / iOS PiP / Web fixed）
  - 自动滚动，保留最近 N 条
  - 支持拖拽定位、字号调整
-->
<template>
  <Teleport to="body">
    <div
      v-if="store.visible"
      ref="overlayRef"
      class="live-subtitle-overlay"
      :class="{ pinned: store.pinned, dragging: isDragging }"
      :style="overlayStyle"
      @touchstart="onDragStart"
      @touchmove="onDragMove"
      @touchend="onDragEnd"
      @mousedown="onDragStart"
      @mousemove="onDragMove"
      @mouseup="onDragEnd"
    >
      <!-- 标题栏 -->
      <div class="overlay-header" @click.stop>
        <span class="overlay-title">实时字幕</span>
        <div class="overlay-actions">
          <button
            v-if="store.translationEnabled"
            class="btn-icon"
            type="button"
            :aria-label="store.translationEnabled ? '关闭翻译' : '开启翻译'"
            @click="toggleTranslation"
          >
            <span class="material-symbols-outlined">
              {{ store.translationEnabled ? 'translate' : 'translate_off' }}
            </span>
          </button>
          <button
            class="btn-icon"
            type="button"
            aria-label="字号"
            @click="cycleFontSize"
          >
            <span class="material-symbols-outlined">format_size</span>
          </button>
          <button
            class="btn-icon"
            type="button"
            :aria-label="store.pinned ? '取消置顶' : '置顶'"
            @click="togglePin"
          >
            <span class="material-symbols-outlined">
              {{ store.pinned ? 'push_pin' : 'push_pin_outlined' }}
            </span>
          </button>
          <button
            class="btn-icon btn-close"
            type="button"
            aria-label="关闭"
            @click="close"
          >
            <span class="material-symbols-outlined">close</span>
          </button>
        </div>
      </div>

      <!-- 字幕内容区 -->
      <div ref="contentRef" class="overlay-content" :class="`font-${store.fontSize}`">
        <TransitionGroup name="subtitle-fade">
          <div
            v-for="seg in recentSegments"
            :key="seg.id"
            class="subtitle-line"
            :class="{ current: seg.id === currentSegmentId }"
          >
            <span class="subtitle-speaker">{{ seg.speaker || '未知' }}</span>
            <div class="subtitle-text-block">
              <p class="subtitle-text-primary">{{ seg.text }}</p>
              <p v-if="seg.translation" class="subtitle-text-secondary">
                {{ seg.translation }}
              </p>
              <span v-else-if="store.translationEnabled && seg.translating" class="subtitle-translating">
                翻译中...
              </span>
            </div>
          </div>
        </TransitionGroup>
        <div v-if="recentSegments.length === 0" class="subtitle-empty">
          等待字幕...
        </div>
      </div>

      <!-- 状态栏 -->
      <div class="overlay-footer">
        <span class="status-text">
          {{ store.segments.length }} 条片段
          <span v-if="store.translationEnabled" class="status-lang">
            → {{ targetLangName }}
          </span>
        </span>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue'
import { useLiveSubtitleStore } from '../stores/liveSubtitle'
import { useHaptics } from '../../../composables/useHaptics'

const store = useLiveSubtitleStore()
const haptics = useHaptics()

const overlayRef = ref<HTMLElement | null>(null)
const contentRef = ref<HTMLElement | null>(null)
const isDragging = ref(false)
const dragStart = ref({ x: 0, y: 0, offsetX: 0, offsetY: 0 })

// 最近 N 条字幕（避免无限增长）
const MAX_VISIBLE = 10
const recentSegments = computed(() => 
  store.segments.slice(-MAX_VISIBLE)
)

// 当前正在说话的片段（最后一条）
const currentSegmentId = computed(() => 
  store.segments.length > 0 ? store.segments[store.segments.length - 1].id : null
)

// 目标语言名称
const langNames: Record<string, string> = {
  zh: '中文', en: 'English', ja: '日本語', ko: '한국어',
  es: 'Español', fr: 'Français', de: 'Deutsch'
}
const targetLangName = computed(() => langNames[store.targetLang] || store.targetLang)

// 悬浮窗样式
const overlayStyle = computed(() => ({
  left: `${store.position.x}px`,
  top: `${store.position.y}px`,
  zIndex: store.pinned ? 10000 : 1000,
}))

// 拖拽处理
function onDragStart(e: MouseEvent | TouchEvent) {
  if (!overlayRef.value) return
  isDragging.value = true
  const clientX = 'touches' in e ? e.touches[0].clientX : e.clientX
  const clientY = 'touches' in e ? e.touches[0].clientY : e.clientY
  dragStart.value = {
    x: clientX,
    y: clientY,
    offsetX: store.position.x,
    offsetY: store.position.y,
  }
  if ('touches' in e) e.preventDefault()
}

function onDragMove(e: MouseEvent | TouchEvent) {
  if (!isDragging.value) return
  const clientX = 'touches' in e ? e.touches[0].clientX : e.clientX
  const clientY = 'touches' in e ? e.touches[0].clientY : e.clientY
  const dx = clientX - dragStart.value.x
  const dy = clientY - dragStart.value.y
  store.position.x = Math.max(0, Math.min(window.innerWidth - 300, dragStart.value.offsetX + dx))
  store.position.y = Math.max(0, Math.min(window.innerHeight - 200, dragStart.value.offsetY + dy))
  if ('touches' in e) e.preventDefault()
}

function onDragEnd() {
  isDragging.value = false
  store.savePosition()
}

// 控制操作
function toggleTranslation() {
  store.translationEnabled = !store.translationEnabled
  haptics.impact('light')
  if (store.translationEnabled) {
    // 为已有片段请求翻译
    store.translateAllPending()
  }
}

function togglePin() {
  store.pinned = !store.pinned
  haptics.impact('light')
}

function cycleFontSize() {
  const sizes = ['small', 'medium', 'large'] as const
  const idx = sizes.indexOf(store.fontSize)
  store.fontSize = sizes[(idx + 1) % sizes.length]
  haptics.impact('light')
}

function close() {
  store.visible = false
  haptics.impact('light')
}

// 自动滚动到最新字幕
watch(() => store.segments.length, async () => {
  await nextTick()
  if (contentRef.value) {
    contentRef.value.scrollTop = contentRef.value.scrollHeight
  }
})
</script>

<style scoped>
.live-subtitle-overlay {
  position: fixed;
  width: 360px;
  max-width: 90vw;
  max-height: 400px;
  background: rgba(0, 0, 0, 0.92);
  backdrop-filter: blur(12px);
  border-radius: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
  display: flex;
  flex-direction: column;
  user-select: none;
  touch-action: none;
  transition: box-shadow 0.2s;
}

.live-subtitle-overlay.pinned {
  box-shadow: 0 0 0 2px var(--accent-9), 0 8px 32px rgba(0, 0, 0, 0.4);
}

.live-subtitle-overlay.dragging {
  cursor: move;
  opacity: 0.9;
}

.overlay-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.1);
  cursor: move;
}

.overlay-title {
  font-size: 13px;
  font-weight: 600;
  color: rgba(255, 255, 255, 0.9);
}

.overlay-actions {
  display: flex;
  gap: 4px;
}

.btn-icon {
  width: 28px;
  height: 28px;
  border-radius: 6px;
  border: none;
  background: transparent;
  color: rgba(255, 255, 255, 0.7);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-icon:hover {
  background: rgba(255, 255, 255, 0.1);
  color: rgba(255, 255, 255, 0.95);
}

.btn-icon:active {
  transform: scale(0.95);
}

.btn-close:hover {
  background: var(--danger);
  color: white;
}

.overlay-content {
  flex: 1;
  overflow-y: auto;
  padding: 12px;
  scroll-behavior: smooth;
}

.subtitle-line {
  margin-bottom: 16px;
  animation: slideIn 0.3s ease-out;
}

.subtitle-line.current {
  animation: pulse 1.5s ease-in-out infinite;
}

@keyframes slideIn {
  from {
    opacity: 0;
    transform: translateY(10px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.7; }
}

.subtitle-speaker {
  display: inline-block;
  font-size: 11px;
  font-weight: 600;
  color: var(--accent-11);
  background: var(--accent-3);
  padding: 2px 8px;
  border-radius: 4px;
  margin-bottom: 6px;
}

.subtitle-text-block {
  margin-top: 4px;
}

.subtitle-text-primary {
  font-size: 15px;
  line-height: 1.5;
  color: rgba(255, 255, 255, 0.95);
  margin: 0 0 6px 0;
}

.subtitle-text-secondary {
  font-size: 13px;
  line-height: 1.4;
  color: rgba(255, 255, 255, 0.65);
  font-style: italic;
  margin: 0;
}

.subtitle-translating {
  font-size: 12px;
  color: rgba(255, 255, 255, 0.45);
}

.subtitle-empty {
  text-align: center;
  padding: 40px 20px;
  color: rgba(255, 255, 255, 0.4);
  font-size: 14px;
}

/* 字号变体 */
.font-small .subtitle-text-primary { font-size: 13px; }
.font-small .subtitle-text-secondary { font-size: 11px; }

.font-medium .subtitle-text-primary { font-size: 15px; }
.font-medium .subtitle-text-secondary { font-size: 13px; }

.font-large .subtitle-text-primary { font-size: 18px; }
.font-large .subtitle-text-secondary { font-size: 15px; }

.overlay-footer {
  padding: 8px 12px;
  border-top: 1px solid rgba(255, 255, 255, 0.1);
}

.status-text {
  font-size: 11px;
  color: rgba(255, 255, 255, 0.5);
}

.status-lang {
  color: var(--accent-11);
  font-weight: 600;
}

/* 过渡动画 */
.subtitle-fade-enter-active,
.subtitle-fade-leave-active {
  transition: all 0.3s ease;
}

.subtitle-fade-enter-from {
  opacity: 0;
  transform: translateY(10px);
}

.subtitle-fade-leave-to {
  opacity: 0;
  transform: translateX(-20px);
}
</style>

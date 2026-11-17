<template>
  <div class="three-layer-transcript" @scroll="onScroll" ref="containerRef">
    <!-- New content badge when user scrolled up -->
    <div v-if="showNewContentBadge" class="new-content-badge" @click="scrollToBottom">
      <span>📥 新内容</span>
    </div>

    <!-- Layer 3: Final refined transcript (after recording ends) -->
    <div v-if="finalSegments.length > 0" class="layer-3-section">
      <div class="layer-banner final">
        <span class="icon">✨</span>
        <span>高精度全文转写</span>
      </div>
      <TranscriptSegmentVue
        v-for="segment in finalSegments"
        :key="segment.id"
        :segment="segment"
        :editable="true"
        @edit="onEdit"
      />
    </div>

    <!-- Layer 2: Confirmed partial segments (VAD detected sentence boundaries) -->
    <div v-if="partialSegments.length > 0" class="layer-2-section">
      <TranscriptSegmentVue
        v-for="segment in partialSegments"
        :key="segment.id"
        :segment="segment"
        :editable="true"
        :show-confidence="segment.confidence < 0.8"
        @edit="onEdit"
      />
    </div>

    <!-- Layer 1: Real-time interim text (gray, updating every 500ms) -->
    <div v-if="interimSegment" class="layer-1-section">
      <div class="interim-container">
        <span class="interim-text">{{ interimSegment.text }}</span>
        <span class="cursor-blink">|</span>
      </div>
    </div>

    <!-- Empty state -->
    <div v-if="!interimSegment && partialSegments.length === 0 && finalSegments.length === 0" class="empty-state">
      <span class="icon">🎤</span>
      <p>开始录音后，实时字幕将显示在这里</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { ThreeLayerRenderer, SmartScroller, type TranscriptSegment as TranscriptSegmentType } from './three-layer-renderer'
import TranscriptSegmentVue from './TranscriptSegment.vue'

interface Props {
  isRecording: boolean
}

const props = defineProps<Props>()
const emit = defineEmits<{
  edit: [segmentId: string, newText: string]
}>()

// Renderer instances
const renderer = new ThreeLayerRenderer()
const scroller = new SmartScroller()

// Refs
const containerRef = ref<HTMLElement>()
const showNewContentBadge = ref(false)

// Computed segments by layer
const finalSegments = computed(() => 
  renderer.render().filter(s => s.layer === 3)
)

const partialSegments = computed(() => 
  renderer.render().filter(s => s.layer === 2)
)

const interimSegment = computed(() => 
  renderer.render().find(s => s.layer === 1) || null
)

// Public API for parent components
function addInterim(text: string, confidence: number = 0.5) {
  const now = Date.now()
  renderer.addInterim({
    id: `interim-${now}`,
    text,
    confidence,
    startMs: now,
    endMs: now,
  })
}

function confirmPartial(segment: Omit<TranscriptSegmentType, 'layer' | 'editable'>) {
  renderer.confirmPartial(segment)
  
  // Auto-scroll if user hasn't manually scrolled
  if (scroller.shouldAutoScroll()) {
    scrollToBottom()
  } else {
    showNewContentBadge.value = true
  }
}

function upgradeToFinal(segments: Omit<TranscriptSegmentType, 'layer' | 'editable'>[]) {
  renderer.upgradeToFinal(segments)
  scrollToBottom()
}

function clear() {
  renderer.clear()
}

// Scroll handling
function onScroll() {
  const el = containerRef.value
  if (!el) return

  const scrollTop = el.scrollTop
  const maxScroll = el.scrollHeight - el.clientHeight

  scroller.checkUserScroll(scrollTop, maxScroll)
  
  // Hide badge when near bottom
  if (maxScroll - scrollTop < 100) {
    showNewContentBadge.value = false
  }
}

function scrollToBottom() {
  const el = containerRef.value
  if (!el) return

  el.scrollTo({
    top: el.scrollHeight,
    behavior: 'smooth'
  })

  scroller.resetToAuto()
  showNewContentBadge.value = false
}

// Edit handling
function onEdit(segmentId: string, newText: string) {
  const success = renderer.editSegment(segmentId, newText)
  if (success) {
    emit('edit', segmentId, newText)
  }
}

// Auto-scroll on new interim text
watch(() => interimSegment.value, (newVal) => {
  if (newVal && scroller.shouldAutoScroll()) {
    scrollToBottom()
  }
})

// Expose public methods
defineExpose({
  addInterim,
  confirmPartial,
  upgradeToFinal,
  clear,
  getFullText: () => renderer.getFullText(),
  render: () => renderer.render(),
})
</script>

<style scoped>
.three-layer-transcript {
  position: relative;
  flex: 1;
  overflow-y: auto;
  padding: 16px;
  background: #f9fafb;
}

.new-content-badge {
  position: sticky;
  top: 16px;
  left: 50%;
  transform: translateX(-50%);
  z-index: var(--z-base);
  background: #3b82f6;
  color: white;
  padding: 8px 16px;
  border-radius: 20px;
  cursor: pointer;
  box-shadow: 0 4px 6px rgba(0, 0, 0, 0.1);
  transition: all 0.2s;
  display: inline-block;
  margin: 0 auto 16px;
}

.new-content-badge:hover {
  background: #2563eb;
  transform: translateX(-50%) scale(1.05);
}

.layer-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  border-radius: 8px;
  margin-bottom: 16px;
  font-weight: 600;
}

.layer-banner .icon {
  font-size: 20px;
}

.layer-1-section {
  margin-top: 16px;
}

.interim-container {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 12px 16px;
  background: white;
  border-radius: 8px;
  border-left: 3px solid #9ca3af;
}

.interim-text {
  color: #9ca3af;
  font-style: italic;
  animation: fadeIn 0.3s ease-in;
}

.cursor-blink {
  color: #9ca3af;
  animation: blink 1s infinite;
}

@keyframes fadeIn {
  from {
    opacity: 0;
    transform: translateY(-4px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@keyframes blink {
  0%, 100% { opacity: 1; }
  50% { opacity: 0; }
}

.layer-2-section,
.layer-3-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 300px;
  color: #6b7280;
}

.empty-state .icon {
  font-size: 48px;
  margin-bottom: 16px;
}

.empty-state p {
  font-size: var(--text-lg);
}
</style>

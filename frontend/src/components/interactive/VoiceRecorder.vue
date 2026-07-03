<template>
  <div class="voice-recorder">
    <button
      :class="recorderClasses"
      @mousedown="startRecording"
      @mouseup="stopRecording"
      @mouseleave="handleMouseLeave"
      @touchstart.prevent="startRecording"
      @touchend.prevent="stopRecording"
    >
      <!-- 脉冲动画背景 -->
      <div v-if="isRecording" class="pulse-ring"></div>
      <div v-if="isRecording" class="pulse-ring pulse-ring--delayed"></div>
      
      <!-- 图标 -->
      <svg class="recorder-icon" viewBox="0 0 24 24" fill="none">
        <path
          v-if="!isRecording"
          d="M12 14a3 3 0 0 0 3-3V5a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3z"
          fill="currentColor"
        />
        <rect
          v-else
          x="8"
          y="8"
          width="8"
          height="8"
          rx="2"
          fill="currentColor"
        />
        <path
          d="M19 11a7 7 0 0 1-14 0M12 17v4M8 21h8"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
        />
      </svg>
    </button>

    <!-- 提示文字 -->
    <div class="recorder-hint">
      {{ isRecording ? '松开结束' : '长按录音' }}
    </div>

    <!-- 录音时长 -->
    <div v-if="isRecording" class="recorder-duration">
      {{ formattedDuration }}
    </div>

    <!-- 实时转写文本浮层 -->
    <Transition name="transcript">
      <div v-if="isRecording && transcript" class="transcript-overlay">
        <div class="transcript-header">
          <span class="transcript-indicator">🔴 识别中...</span>
        </div>
        <div class="transcript-text">{{ transcript }}</div>
      </div>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onUnmounted } from 'vue'

export interface VoiceRecorderProps {
  onStart?: () => void
  onStop?: (duration: number) => void
  onTranscript?: (text: string) => void
}

const props = defineProps<VoiceRecorderProps>()

const isRecording = ref(false)
const duration = ref(0)
const transcript = ref('')
let durationTimer: ReturnType<typeof setInterval> | null = null
let longPressTimer: ReturnType<typeof setTimeout> | null = null

const recorderClasses = computed(() => {
  return [
    'recorder-button',
    {
      'recorder-button--recording': isRecording.value,
    },
  ]
})

const formattedDuration = computed(() => {
  const mins = Math.floor(duration.value / 60)
  const secs = duration.value % 60
  return `${mins.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`
})

const startRecording = () => {
  // 长按检测（避免误触）
  longPressTimer = setTimeout(() => {
    isRecording.value = true
    duration.value = 0
    transcript.value = ''
    
    // 触觉反馈
    if (navigator.vibrate) {
      navigator.vibrate(50)
    }
    
    // 开始计时
    durationTimer = setInterval(() => {
      duration.value++
      
      // 模拟实时转写（实际应该调用 STT API）
      if (duration.value % 2 === 0) {
        transcript.value += '测试转写文本...'
      }
    }, 1000)
    
    props.onStart?.()
  }, 200) // 200ms 长按触发
}

const stopRecording = () => {
  if (longPressTimer) {
    clearTimeout(longPressTimer)
    longPressTimer = null
  }
  
  if (!isRecording.value) return
  
  // 触觉反馈
  if (navigator.vibrate) {
    navigator.vibrate(50)
  }
  
  // 停止计时
  if (durationTimer) {
    clearInterval(durationTimer)
    durationTimer = null
  }
  
  const finalDuration = duration.value
  
  isRecording.value = false
  
  props.onStop?.(finalDuration)
  
  // 延迟清空转写文本
  setTimeout(() => {
    transcript.value = ''
  }, 500)
}

const handleMouseLeave = () => {
  // 鼠标离开时也停止录音（长按模式）
  if (isRecording.value) {
    stopRecording()
  }
}

onUnmounted(() => {
  if (durationTimer) {
    clearInterval(durationTimer)
  }
  if (longPressTimer) {
    clearTimeout(longPressTimer)
  }
})
</script>

<style scoped>
.voice-recorder {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-4);
}

.recorder-button {
  position: relative;
  width: 80px;
  height: 80px;
  border-radius: 50%;
  background: var(--gradient-primary);
  border: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all var(--duration-base) var(--ease-spring);
  box-shadow: var(--shadow-lg);
  user-select: none;
  -webkit-tap-highlight-color: transparent;
}

.recorder-button:hover:not(.recorder-button--recording) {
  transform: scale(1.05);
}

.recorder-button:active:not(.recorder-button--recording) {
  transform: scale(0.95);
}

.recorder-button--recording {
  background: var(--color-error);
  transform: scale(1.2);
  animation: pulse 2s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% {
    box-shadow: 0 0 0 0 rgba(239, 68, 68, 0.4);
  }
  50% {
    box-shadow: 0 0 0 20px rgba(239, 68, 68, 0);
  }
}

.recorder-icon {
  width: 32px;
  height: 32px;
  color: white;
  transition: all var(--duration-base) var(--ease-out);
}

.pulse-ring {
  position: absolute;
  width: 100%;
  height: 100%;
  border-radius: 50%;
  background: var(--color-error);
  opacity: 0.3;
  animation: pulse-ring 1.5s ease-out infinite;
}

.pulse-ring--delayed {
  animation-delay: 0.75s;
}

@keyframes pulse-ring {
  0% {
    transform: scale(1);
    opacity: 0.3;
  }
  100% {
    transform: scale(1.5);
    opacity: 0;
  }
}

.recorder-hint {
  font-size: 14px;
  color: var(--color-text-secondary);
  font-weight: var(--font-weight-medium);
  transition: all var(--duration-base) var(--ease-out);
}

.recorder-duration {
  font-size: 20px;
  font-weight: var(--font-weight-bold);
  color: var(--color-error);
  font-variant-numeric: tabular-nums;
}

/* 转写文本浮层 */
.transcript-overlay {
  position: fixed;
  bottom: 150px;
  left: var(--space-4);
  right: var(--space-4);
  background: rgba(0, 0, 0, 0.85);
  backdrop-filter: blur(10px);
  border-radius: var(--radius-lg);
  padding: var(--space-4);
  color: white;
  max-height: 200px;
  overflow-y: auto;
  box-shadow: var(--shadow-xl);
}

.transcript-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
}

.transcript-indicator {
  font-size: 12px;
  color: var(--color-error);
  font-weight: var(--font-weight-medium);
  animation: blink 1.5s ease-in-out infinite;
}

@keyframes blink {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}

.transcript-text {
  font-size: 14px;
  line-height: 1.5;
  color: rgba(255, 255, 255, 0.9);
}

/* 转写浮层动画 */
.transcript-enter-active,
.transcript-leave-active {
  transition: all var(--duration-base) var(--ease-out);
}

.transcript-enter-from {
  opacity: 0;
  transform: translateY(20px);
}

.transcript-leave-to {
  opacity: 0;
  transform: translateY(10px) scale(0.95);
}
</style>

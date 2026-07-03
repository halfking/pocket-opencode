<template>
  <div class="voice-command-assistant">
    <!-- 语音按钮 -->
    <button
      :class="['voice-fab', { 'voice-fab--active': isListening }]"
      @mousedown="handleStart"
      @mouseup="handleStop"
      @mouseleave="handleStop"
      @touchstart.prevent="handleStart"
      @touchend.prevent="handleStop"
    >
      <div v-if="isListening" class="pulse-ring"></div>
      <div v-if="isListening" class="pulse-ring pulse-ring--delayed"></div>
      <span class="voice-icon">{{ isListening ? '🎤' : '🎙️' }}</span>
    </button>

    <!-- 语音识别浮层 -->
    <Transition name="voice-panel">
      <div v-if="isListening || showResult" class="voice-panel">
        <!-- 状态指示 -->
        <div class="panel-header">
          <span v-if="isListening" class="status-text">🔴 正在听...</span>
          <span v-else class="status-text">✓ 识别完成</span>
          <button class="panel-close" @click="handleClose">✕</button>
        </div>

        <!-- 实时转写 -->
        <div class="transcription">
          <p v-if="transcription" class="transcription-text">
            {{ transcription }}
          </p>
          <p v-else class="transcription-placeholder">
            请说话...
          </p>
        </div>

        <!-- 语音命令建议 -->
        <div v-if="!isListening && suggestions.length > 0" class="suggestions">
          <h4 class="suggestions-title">快捷命令</h4>
          <div class="suggestion-chips">
            <button
              v-for="suggestion in suggestions"
              :key="suggestion"
              class="suggestion-chip"
              @click="handleSuggestion(suggestion)"
            >
              {{ suggestion }}
            </button>
          </div>
        </div>

        <!-- 命令结果 -->
        <div v-if="commandResult" class="command-result">
          <div class="result-icon">{{ commandResult.icon }}</div>
          <div class="result-text">{{ commandResult.text }}</div>
        </div>
      </div>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

export interface VoiceCommandAssistantProps {
  onCommand?: (command: string, params?: any) => void
  onTranscription?: (text: string) => void
}

const props = defineProps<VoiceCommandAssistantProps>()

const isListening = ref(false)
const showResult = ref(false)
const transcription = ref('')
const commandResult = ref<{ icon: string; text: string } | null>(null)
const holdTimer = ref<ReturnType<typeof setTimeout> | null>(null)
const mode = ref<'recording' | 'command'>('recording')

// 语音命令建议
const suggestions = ref([
  '打开笔记',
  '搜索邮件',
  '今天有什么安排',
  '总结最近的会议',
  '创建新任务',
])

// 语音命令解析规则
const commandPatterns = [
  { pattern: /^(打开|进入|查看)(.+)$/, action: 'navigate' },
  { pattern: /^搜索(.+)$/, action: 'search' },
  { pattern: /^创建(.+)$/, action: 'create' },
  { pattern: /^总结(.+)$/, action: 'summarize' },
  { pattern: /^(.+)有什么(.+)$/, action: 'query' },
]

const handleStart = () => {
  // 长按 500ms 进入命令模式，否则是录音模式
  holdTimer.value = setTimeout(() => {
    mode.value = 'command'
    startListening()
  }, 500)
}

const handleStop = () => {
  if (holdTimer.value) {
    clearTimeout(holdTimer.value)
    holdTimer.value = null
  }

  if (isListening.value) {
    stopListening()
  } else {
    // 短按：录音模式
    mode.value = 'recording'
    startListening()
  }
}

const startListening = () => {
  isListening.value = true
  transcription.value = ''
  commandResult.value = null
  showResult.value = true

  // 模拟语音识别（实际应该调用 STT API）
  setTimeout(() => {
    if (mode.value === 'command') {
      transcription.value = '打开笔记'
    } else {
      transcription.value = '今天讨论了项目进度和时间安排...'
    }
  }, 1000)

  // 触觉反馈
  if (navigator.vibrate) {
    navigator.vibrate(50)
  }
}

const stopListening = () => {
  isListening.value = false

  // 触觉反馈
  if (navigator.vibrate) {
    navigator.vibrate(50)
  }

  // 解析命令
  if (mode.value === 'command') {
    parseAndExecuteCommand(transcription.value)
  } else {
    // 录音模式：触发回调
    props.onTranscription?.(transcription.value)
    setTimeout(() => {
      showResult.value = false
    }, 2000)
  }
}

const parseAndExecuteCommand = (text: string) => {
  console.log('[VoiceCommand] 解析命令:', text)

  for (const { pattern, action } of commandPatterns) {
    const match = text.match(pattern)
    if (match) {
      executeCommand(action, match)
      return
    }
  }

  // 未识别的命令
  commandResult.value = {
    icon: '❓',
    text: '未识别的命令，请重试',
  }

  setTimeout(() => {
    showResult.value = false
  }, 2000)
}

const executeCommand = (action: string, params: RegExpMatchArray) => {
  console.log('[VoiceCommand] 执行命令:', action, params)

  switch (action) {
    case 'navigate':
      const target = params[2]
      commandResult.value = {
        icon: '✓',
        text: `正在打开 ${target}`,
      }
      props.onCommand?.('navigate', { target })
      break

    case 'search':
      const keyword = params[1]
      commandResult.value = {
        icon: '🔍',
        text: `搜索: ${keyword}`,
      }
      props.onCommand?.('search', { keyword })
      break

    case 'create':
      const type = params[1]
      commandResult.value = {
        icon: '✓',
        text: `创建 ${type}`,
      }
      props.onCommand?.('create', { type })
      break

    case 'summarize':
      const content = params[1]
      commandResult.value = {
        icon: '🤖',
        text: `正在总结 ${content}`,
      }
      props.onCommand?.('summarize', { content })
      break

    case 'query':
      commandResult.value = {
        icon: '🔍',
        text: `正在查询...`,
      }
      props.onCommand?.('query', { text: params[0] })
      break
  }

  setTimeout(() => {
    showResult.value = false
  }, 2000)
}

const handleSuggestion = (suggestion: string) => {
  transcription.value = suggestion
  parseAndExecuteCommand(suggestion)
}

const handleClose = () => {
  isListening.value = false
  showResult.value = false
  transcription.value = ''
  commandResult.value = null
}
</script>

<style scoped>
.voice-command-assistant {
  position: relative;
}

.voice-fab {
  position: fixed;
  right: var(--space-4);
  bottom: calc(80px + var(--space-4)); /* 避开底部导航 */
  width: 64px;
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--gradient-primary);
  color: white;
  border: none;
  border-radius: 50%;
  font-size: 32px;
  box-shadow: var(--shadow-xl);
  cursor: pointer;
  z-index: 1000;
  transition: all var(--duration-base) var(--ease-spring);
  user-select: none;
}

.voice-fab:hover {
  transform: scale(1.05);
}

.voice-fab:active {
  transform: scale(0.95);
}

.voice-fab--active {
  background: var(--color-error);
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
    transform: scale(1.8);
    opacity: 0;
  }
}

.voice-icon {
  position: relative;
  z-index: 1;
}

.voice-panel {
  position: fixed;
  bottom: calc(80px + 80px); /* 底部导航 + FAB */
  right: var(--space-4);
  width: calc(100vw - var(--space-8));
  max-width: 400px;
  background: rgba(0, 0, 0, 0.95);
  backdrop-filter: blur(20px);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-2xl);
  overflow: hidden;
  z-index: 999;
}

.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4);
  border-bottom: 1px solid rgba(255, 255, 255, 0.1);
}

.status-text {
  font-size: 14px;
  font-weight: var(--font-weight-medium);
  color: white;
}

.panel-close {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(255, 255, 255, 0.1);
  border: none;
  border-radius: var(--radius-full);
  color: white;
  font-size: 16px;
  cursor: pointer;
}

.transcription {
  padding: var(--space-6);
  min-height: 80px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.transcription-text {
  font-size: 16px;
  line-height: 1.6;
  color: white;
  margin: 0;
  text-align: center;
}

.transcription-placeholder {
  font-size: 14px;
  color: rgba(255, 255, 255, 0.5);
  margin: 0;
}

.suggestions {
  padding: var(--space-4);
  border-top: 1px solid rgba(255, 255, 255, 0.1);
}

.suggestions-title {
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  color: rgba(255, 255, 255, 0.6);
  margin: 0 0 var(--space-2) 0;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.suggestion-chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.suggestion-chip {
  padding: 6px 12px;
  background: rgba(255, 255, 255, 0.1);
  border: 1px solid rgba(255, 255, 255, 0.2);
  border-radius: var(--radius-full);
  color: white;
  font-size: 13px;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.suggestion-chip:hover {
  background: rgba(255, 255, 255, 0.2);
}

.command-result {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-4);
  background: rgba(255, 255, 255, 0.05);
  border-top: 1px solid rgba(255, 255, 255, 0.1);
}

.result-icon {
  font-size: 24px;
}

.result-text {
  flex: 1;
  font-size: 14px;
  color: white;
}

/* 动画 */
.voice-panel-enter-active,
.voice-panel-leave-active {
  transition: all var(--duration-base) var(--ease-out);
}

.voice-panel-enter-from {
  opacity: 0;
  transform: translateY(20px) scale(0.95);
}

.voice-panel-leave-to {
  opacity: 0;
  transform: translateY(10px) scale(0.98);
}
</style>

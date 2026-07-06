<script setup lang="ts">
/**
 * SessionConversationView — 主题任务 / 会话实时对话视图
 *
 * 路由：/sessions/:id?instance_id=xxx&title=xxx
 *
 * 功能：
 *  - 拉取历史消息 + 订阅 SSE 流式接收
 *  - 底部输入区发送 prompt
 *  - 流式增量实时渲染
 *  - Stop 按钮中断 agent
 *  - 自动滚动到底部（用户上滚时暂停）
 */
import { onMounted, onBeforeUnmount, ref, nextTick, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSessionStore } from '../../stores/session'

const route = useRoute()
const router = useRouter()
const store = useSessionStore()

const sessionID = computed(() => route.params.id as string)
const instanceID = computed(() => (route.query.instance_id as string) || localStorage.getItem('selected_instance_id') || '')
const initialTitle = computed(() => (route.query.title as string) || '')

const inputText = ref('')
const sending = ref(false)
const isRecording = ref(false)
let mediaRecorder: MediaRecorder | null = null
let audioChunks: Blob[] = []
const messagesEl = ref<HTMLElement | null>(null)
const autoScroll = ref(true)

const selectedInstance = computed(() => {
  try {
    const raw = localStorage.getItem('selected_instance')
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
})

const sessionTitle = computed(() => {
  if (store.title) return store.title
  if (initialTitle.value) return initialTitle.value
  // 用 ID 截断作为 fallback
  return sessionID.value.slice(0, 8)
})

onMounted(async () => {
  if (!instanceID.value) {
    // 没有 instance — 回到实例选择
    router.replace('/instances')
    return
  }
  await store.open(sessionID.value, instanceID.value, initialTitle.value)
  await nextTick()
  scrollToBottom(true)
})

onBeforeUnmount(() => {
  store.close()
})

async function scrollToBottom(force = false) {
  if (!autoScroll.value && !force) return
  await nextTick()
  if (messagesEl.value) {
    messagesEl.value.scrollTop = messagesEl.value.scrollHeight
  }
}

// 用户上滚 → 暂停自动滚动；触底 → 恢复
function onScroll() {
  if (!messagesEl.value) return
  const el = messagesEl.value
  const distanceToBottom = el.scrollHeight - el.scrollTop - el.clientHeight
  autoScroll.value = distanceToBottom < 50
}

async function send() {
  const text = inputText.value.trim()
  if (!text || sending.value) return
  sending.value = true
  inputText.value = ''
  try {
    await store.sendPrompt(text)
    autoScroll.value = true
    await nextTick()
    scrollToBottom(true)
  } finally {
    sending.value = false
  }
}

// ── Voice Recording ──
async function toggleVoice() {
  if (isRecording.value) {
    stopRecording()
  } else {
    await startRecording()
  }
}

async function startRecording() {
  try {
    const stream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, sampleRate: 16000 },
    })
    mediaRecorder = new MediaRecorder(stream)
    audioChunks = []
    mediaRecorder.ondataavailable = (e) => {
      if (e.data.size > 0) audioChunks.push(e.data)
    }
    mediaRecorder.onstop = async () => {
      stream.getTracks().forEach((t) => t.stop())
      // STT: try local sherpa-onnx first, fallback to cloud
      try {
        const blob = new Blob(audioChunks, { type: 'audio/webm' })
        const url = URL.createObjectURL(blob)
        // Placeholder: use sttApi.transcribe(url) when available
        // For now, insert a placeholder
        inputText.value = '[语音输入完成，请编辑后发送]'
      } catch (e) {
        console.error('STT failed:', e)
      }
    }
    mediaRecorder.start()
    isRecording.value = true
  } catch (e) {
    console.error('Microphone access denied:', e)
  }
}

function stopRecording() {
  if (mediaRecorder && mediaRecorder.state !== 'inactive') {
    mediaRecorder.stop()
  }
  isRecording.value = false
}

async function stop() {
  await store.interrupt()
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
  }
}

// 自动跟随流式输出
const lastMsgId = computed(() => store.messages[store.messages.length - 1]?.id)
import { watch } from 'vue'
watch(
  () => [store.messages.length, lastMsgId.value, store.lastMessage?.text?.length],
  () => {
    scrollToBottom()
  },
)

function goBack() {
  if (window.history.length > 1) {
    router.back()
  } else {
    router.push('/ai')
  }
}
</script>

<template>
  <div class="session-view">
    <!-- Top Bar -->
    <header class="top-bar">
      <button class="back-btn" @click="goBack" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <div class="title-block">
        <div class="title">{{ sessionTitle }}</div>
        <div class="subtitle">
          <span class="status-dot" :class="store.status"></span>
          <span class="status-text">
            {{
              store.status === 'streaming'
                ? '生成中…'
                : store.status === 'error'
                ? '出错'
                : store.status === 'completed'
                ? '完成'
                : '空闲'
            }}
          </span>
          <span v-if="selectedInstance?.displayName" class="instance-tag">
            · {{ selectedInstance.displayName }}
          </span>
        </div>
      </div>
      <button v-if="store.isStreaming" class="stop-btn" @click="stop" aria-label="停止">
        <span class="material-symbols-outlined">stop_circle</span>
      </button>
      <div v-else class="top-spacer"></div>
    </header>

    <!-- Messages -->
    <main ref="messagesEl" class="messages" @scroll="onScroll">
      <div v-if="store.messages.length === 0" class="empty">
        <div class="empty-icon">💬</div>
        <p class="empty-text">开始一个新的对话</p>
        <p class="empty-hint">在下方输入框输入你的问题或任务</p>
      </div>

      <div
        v-for="msg in store.messages"
        :key="msg.id"
        class="message"
        :class="['role-' + msg.role, { streaming: msg.streaming }]"
      >
        <!-- User message -->
        <template v-if="msg.role === 'user'">
          <div class="bubble user-bubble">{{ msg.text }}</div>
        </template>

        <!-- Assistant message -->
        <template v-else-if="msg.role === 'assistant'">
          <div class="avatar assistant-avatar">AI</div>
          <div class="bubble assistant-bubble">
            <div v-if="msg.text" class="text-content">
              {{ msg.text }}<span v-if="msg.streaming" class="caret">▍</span>
            </div>
            <div v-if="msg.content" class="content-list">
              <div
                v-for="(c, i) in msg.content"
                :key="i"
                class="content-item"
                :class="'content-' + c.type"
              >
                <template v-if="c.type === 'tool'">
                  <details class="tool-card" :open="c.state === 'running'">
                    <summary>
                      <span class="tool-icon">🔧</span>
                      <span class="tool-name">{{ c.name }}</span>
                      <span class="tool-state" :class="'state-' + c.state">
                        {{
                          c.state === 'running' ? '执行中'
                          : c.state === 'completed' ? '完成'
                          : c.state === 'error' ? '失败'
                          : '等待'
                        }}
                      </span>
                    </summary>
                    <div v-if="c.input" class="tool-section">
                      <div class="tool-section-title">输入</div>
                      <pre>{{ JSON.stringify(c.input, null, 2) }}</pre>
                    </div>
                    <div v-if="c.output" class="tool-section">
                      <div class="tool-section-title">输出</div>
                      <pre>{{ JSON.stringify(c.output, null, 2) }}</pre>
                    </div>
                    <div v-if="c.error" class="tool-section error">
                      <div class="tool-section-title">错误</div>
                      <pre>{{ c.error }}</pre>
                    </div>
                  </details>
                </template>
              </div>
            </div>
          </div>
        </template>

        <!-- System message -->
        <template v-else>
          <div class="bubble system-bubble">{{ msg.text }}</div>
        </template>
      </div>

      <!-- Scroll-to-bottom button -->
      <button
        v-if="!autoScroll && store.messages.length > 3"
        class="scroll-bottom-btn"
        @click="scrollToBottom(true)"
        aria-label="滚动到底部"
      >
        <span class="material-symbols-outlined">arrow_downward</span>
      </button>
    </main>

    <!-- Error banner -->
    <div v-if="store.errorMessage" class="error-banner">
      {{ store.errorMessage }}
    </div>

    <!-- Input bar -->
    <footer class="input-bar">
      <textarea
        v-model="inputText"
        class="input"
        :placeholder="isRecording ? '🎙 录音中...' : '输入消息…'"
        rows="1"
        @keydown="onKeydown"
        :disabled="sending || isRecording"
      ></textarea>
      <button
        class="voice-btn"
        :class="{ recording: isRecording }"
        @click="toggleVoice"
        aria-label="语音"
      >
        {{ isRecording ? '⏹' : '🎙' }}
      </button>
      <button
        class="send-btn"
        :disabled="!inputText.trim() || sending"
        @click="send"
        aria-label="发送"
      >
        <span class="material-symbols-outlined">send</span>
      </button>
    </footer>
  </div>
</template>

<style scoped>
.session-view {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--bg, #fafbfc);
  /* iOS safe area */
  padding-top: env(safe-area-inset-top);
}

/* Top Bar */
.top-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  background: var(--surface, #fff);
  border-bottom: 1px solid var(--border, #e5e7eb);
  -webkit-app-region: drag;
}
.back-btn,
.stop-btn,
.top-spacer {
  flex: 0 0 auto;
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: transparent;
  border: none;
  cursor: pointer;
  color: var(--text, #111827);
}
.back-btn:hover,
.stop-btn:hover {
  background: var(--hover, #f3f4f6);
}
.stop-btn {
  color: #ef4444;
}
.title-block {
  flex: 1 1 auto;
  min-width: 0;
}
.title {
  font-size: 16px;
  font-weight: 600;
  color: var(--text, #111827);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.subtitle {
  font-size: 12px;
  color: var(--text-secondary, #6b7280);
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 2px;
}
.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #10b981;
  display: inline-block;
}
.status-dot.streaming {
  background: #3b82f6;
  animation: pulse 1.5s ease-in-out infinite;
}
.status-dot.error {
  background: #ef4444;
}
@keyframes pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.5; transform: scale(1.4); }
}
.instance-tag {
  color: var(--text-tertiary, #9ca3af);
  font-size: 11px;
}

/* Messages */
.messages {
  flex: 1 1 auto;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
  overscroll-behavior-y: contain;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  scroll-behavior: smooth;
}
.empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  color: var(--text-secondary, #6b7280);
  text-align: center;
  padding: 40px;
}
.empty-icon { font-size: 48px; margin-bottom: 12px; }
.empty-text { font-size: 18px; font-weight: 500; margin: 0 0 4px; color: var(--text, #111827); }
.empty-hint { font-size: 14px; margin: 0; }
.message {
  display: flex;
  gap: 8px;
  max-width: 90%;
  animation: message-in 200ms ease-out;
}
@keyframes message-in {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}
.message.role-user {
  align-self: flex-end;
  flex-direction: row-reverse;
}
.message.role-assistant {
  align-self: flex-start;
}
.message.role-system {
  align-self: center;
  max-width: 100%;
}
.avatar {
  flex: 0 0 auto;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: linear-gradient(135deg, #667eea, #764ba2);
  color: white;
  font-size: 11px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: 4px;
}
.bubble {
  padding: 10px 14px;
  border-radius: 16px;
  font-size: 15px;
  line-height: 1.5;
  word-break: break-word;
  white-space: pre-wrap;
  position: relative;
}
.user-bubble {
  background: var(--primary, #3b82f6);
  color: white;
  border-bottom-right-radius: 4px;
}
.assistant-bubble {
  background: var(--surface, #fff);
  color: var(--text, #111827);
  border: 1px solid var(--border, #e5e7eb);
  border-bottom-left-radius: 4px;
}
.system-bubble {
  background: var(--surface-variant, #f3f4f6);
  color: var(--text-secondary, #6b7280);
  font-size: 13px;
  padding: 6px 12px;
}
.caret {
  display: inline-block;
  margin-left: 1px;
  color: var(--primary, #3b82f6);
  animation: blink 1s steps(1) infinite;
}
@keyframes blink {
  50% { opacity: 0; }
}
.content-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 8px;
}
.tool-card {
  background: var(--surface-variant, #f9fafb);
  border: 1px solid var(--border, #e5e7eb);
  border-radius: 8px;
  padding: 8px 12px;
  font-size: 13px;
}
.tool-card summary {
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: 6px;
  list-style: none;
}
.tool-card summary::-webkit-details-marker {
  display: none;
}
.tool-icon { font-size: 14px; }
.tool-name { font-weight: 600; font-family: monospace; }
.tool-state {
  margin-left: auto;
  padding: 2px 8px;
  border-radius: 10px;
  font-size: 11px;
  font-weight: 500;
}
.tool-state.state-running { background: #dbeafe; color: #1e40af; }
.tool-state.state-completed { background: #d1fae5; color: #065f46; }
.tool-state.state-error { background: #fee2e2; color: #991b1b; }
.tool-state.state-pending { background: #f3f4f6; color: #6b7280; }
.tool-section {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px dashed var(--border, #e5e7eb);
}
.tool-section.error { color: #991b1b; }
.tool-section-title { font-size: 11px; font-weight: 600; color: var(--text-secondary, #6b7280); margin-bottom: 4px; }
.tool-section pre {
  margin: 0;
  font-size: 12px;
  font-family: 'SF Mono', Menlo, monospace;
  white-space: pre-wrap;
  word-break: break-all;
  background: rgba(0, 0, 0, 0.03);
  padding: 6px 8px;
  border-radius: 4px;
}

/* Scroll-to-bottom button */
.scroll-bottom-btn {
  position: absolute;
  bottom: 80px;
  right: 16px;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--surface, #fff);
  border: 1px solid var(--border, #e5e7eb);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-secondary, #6b7280);
}

/* Error banner */
.error-banner {
  flex: 0 0 auto;
  background: #fee2e2;
  color: #991b1b;
  padding: 8px 16px;
  font-size: 13px;
  text-align: center;
  border-top: 1px solid #fecaca;
}

/* Input bar */
.input-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: flex-end;
  gap: 8px;
  padding: 12px 16px;
  padding-bottom: calc(12px + env(safe-area-inset-bottom));
  background: var(--surface, #fff);
  border-top: 1px solid var(--border, #e5e7eb);
}
.input {
  flex: 1 1 auto;
  resize: none;
  max-height: 200px;
  padding: 10px 14px;
  border: 1px solid var(--border, #e5e7eb);
  border-radius: 20px;
  font-size: 15px;
  line-height: 1.5;
  font-family: inherit;
  background: var(--surface-variant, #f9fafb);
  color: var(--text, #111827);
  outline: none;
  transition: border-color 150ms;
}
.input:focus {
  border-color: var(--primary, #3b82f6);
  background: var(--surface, #fff);
}
.send-btn {
  flex: 0 0 auto;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--primary, #3b82f6);
  color: white;
  border: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 150ms;
}
.send-btn:disabled {
  background: var(--border, #e5e7eb);
  color: var(--text-tertiary, #9ca3af);
  cursor: not-allowed;
}
.send-btn:not(:disabled):active {
  transform: scale(0.95);
}
.voice-btn {
  flex: 0 0 auto;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--surface-variant, #f3f4f6);
  color: var(--text-secondary, #6b7280);
  border: none;
  font-size: 18px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 150ms;
}
.voice-btn.recording {
  background: var(--error, #ef4444);
  color: #fff;
  animation: pulse-voice 1s infinite;
}
@keyframes pulse-voice {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.7; transform: scale(1.05); }
}
.voice-btn:active {
  transform: scale(0.9);
}
.material-symbols-outlined {
  font-family: 'Material Symbols Outlined', 'Material Icons';
  font-weight: normal;
  font-style: normal;
  font-size: 20px;
  line-height: 1;
}
</style>
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
import { onMounted, onBeforeUnmount, ref, nextTick, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useSessionStore, type ToolContent } from '../../stores/session'
import { useVoiceInput } from '../../composables/useVoiceInput'
import { createScrollHideChrome } from '../../composables/useScrollHideChrome'

const route = useRoute()
const router = useRouter()
const store = useSessionStore()
const { isRecording, isTranscribing, sttError, startRecording, stopRecording } = useVoiceInput()

const sessionID = computed(() => route.params.id as string)
const instanceID = computed(() => (route.query.instance_id as string) || localStorage.getItem('selected_instance_id') || '')
const initialTitle = computed(() => (route.query.title as string) || '')

const inputText = ref('')
const sending = ref(false)
const messagesEl = ref<HTMLElement | null>(null)
const headerRef = ref<HTMLElement | null>(null)
const chromeHeight = ref(56)
const autoScroll = ref(true)
const expandedMessages = ref<Set<string>>(new Set())
const COLLAPSE_LINE_THRESHOLD = 20

const chrome = createScrollHideChrome(() => chromeHeight.value)
const chromeShellStyle = computed(() => ({
  transform: `translate3d(0, -${chrome.hiddenOffset.value}px, 0)`,
}))

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
    router.replace('/instances')
    return
  }
  chromeHeight.value = headerRef.value?.offsetHeight ?? 56
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

let lastMsgScrollTop = 0
function onScroll() {
  if (!messagesEl.value) return
  const el = messagesEl.value
  const delta = el.scrollTop - lastMsgScrollTop
  lastMsgScrollTop = el.scrollTop
  chrome.reportScroll({ scrollTop: el.scrollTop, delta })

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
    const text = await stopRecording()
    if (text) inputText.value = text
  } else {
    await startRecording()
  }
}

// ── Markdown rendering ──
function renderMarkdown(text: string): string {
  const out = marked.parse(text, { async: false })
  const html = typeof out === 'string' ? out : ''
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS: [
      'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
      'p', 'br', 'strong', 'em', 'u', 'del', 's',
      'a', 'ul', 'ol', 'li', 'blockquote', 'pre', 'code',
      'table', 'thead', 'tbody', 'tr', 'th', 'td', 'hr', 'div', 'span',
    ],
    ALLOWED_ATTR: ['href', 'target', 'rel', 'class'],
  })
}

function lineCount(text: string): number {
  return text.split('\n').length
}

function isLongMessage(msg: { text: string }): boolean {
  return lineCount(msg.text) > COLLAPSE_LINE_THRESHOLD
}

function isCollapsed(msg: { id: string; text: string }): boolean {
  return isLongMessage(msg) && !expandedMessages.value.has(msg.id)
}

function toggleCollapse(id: string) {
  const s = new Set(expandedMessages.value)
  if (s.has(id)) s.delete(id)
  else s.add(id)
  expandedMessages.value = s
}

function truncateOutput(val: unknown, maxLen = 300): string {
  const str = typeof val === 'string' ? val : JSON.stringify(val, null, 2)
  if (str.length <= maxLen) return str
  return str.slice(0, maxLen) + '…'
}

function toolDuration(c: ToolContent): string {
  // 工具卡片暂无精确耗时字段，用状态作占位
  if (c.state === 'running') return '执行中'
  if (c.state === 'completed') return '已完成'
  if (c.state === 'error') return '失败'
  return '等待'
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
    <div
      ref="headerRef"
      class="chrome-shell"
      :class="{ 'is-snapping': chrome.snapping }"
      :style="chromeShellStyle"
    >
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
    </div>

    <!-- Messages -->
    <main
      ref="messagesEl"
      class="messages"
      :style="{ paddingTop: chromeHeight + 'px' }"
      @scroll="onScroll"
    >
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
            <div v-if="msg.text" class="text-content" :class="{ collapsed: isCollapsed(msg) }">
              <div class="md-body" v-html="renderMarkdown(msg.text)" />
              <span v-if="msg.streaming" class="caret">▍</span>
            </div>
            <button
              v-if="isLongMessage(msg) && !msg.streaming"
              class="collapse-btn"
              @click="toggleCollapse(msg.id)"
            >
              {{ isCollapsed(msg) ? '展开全部' : '收起' }}
            </button>
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
                      <span class="tool-duration">{{ toolDuration(c) }}</span>
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
                      <pre>{{ truncateOutput(c.input) }}</pre>
                    </div>
                    <div v-if="c.output" class="tool-section">
                      <div class="tool-section-title">输出</div>
                      <pre>{{ truncateOutput(c.output) }}</pre>
                    </div>
                    <div v-if="c.error" class="tool-section error">
                      <div class="tool-section-title">错误</div>
                      <pre>{{ truncateOutput(c.error) }}</pre>
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
      <div v-if="isRecording" class="recording-strip">
        <span class="rec-dot" />
        <span class="rec-bars"><i /><i /><i /><i /><i /></span>
        <span class="rec-label">录音中…</span>
      </div>
      <div v-else-if="isTranscribing" class="recording-strip transcribing">
        <span class="rec-label">转写中…</span>
      </div>
      <div v-if="sttError" class="stt-error">{{ sttError }}</div>
      <div class="input-row">
      <textarea
        v-model="inputText"
        class="input"
        :placeholder="isRecording ? '🎙 录音中...' : isTranscribing ? '转写中...' : '输入消息…'"
        rows="1"
        @keydown="onKeydown"
        :disabled="sending || isRecording || isTranscribing"
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
        :disabled="!inputText.trim() || sending || isTranscribing"
        @click="send"
        aria-label="发送"
      >
        <span class="material-symbols-outlined">send</span>
      </button>
      </div>
    </footer>
  </div>
</template>

<style scoped>
.session-view {
  display: flex;
  flex-direction: column;
  height: 100dvh;
  background: var(--bg-base);
}

.chrome-shell {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: 20;
  will-change: transform;
  padding-top: env(safe-area-inset-top);
  background: var(--bg-card);
}

.chrome-shell.is-snapping {
  transition: transform 280ms cubic-bezier(0.32, 0.72, 0, 1);
}

/* Top Bar */
.top-bar {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
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
  color: var(--text-primary);
}
.back-btn:hover,
.stop-btn:hover {
  background: var(--bg-subtle);
}
.stop-btn {
  color: var(--danger);
}
.title-block {
  flex: 1 1 auto;
  min-width: 0;
}
.title {
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.subtitle {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-top: 2px;
}
.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--success);
  display: inline-block;
}
.status-dot.streaming {
  background: var(--info);
  animation: pulse 1.5s ease-in-out infinite;
}
.status-dot.error {
  background: var(--danger);
}
@keyframes pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.5; transform: scale(1.4); }
}
.instance-tag {
  color: var(--text-muted);
  font-size: var(--text-xs);
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
  color: var(--text-secondary);
  text-align: center;
  padding: var(--space-6);
}
.empty-icon { font-size: 48px; margin-bottom: var(--space-3); }
.empty-text { font-size: var(--text-xl); font-weight: var(--font-weight-medium); margin: 0 0 4px; color: var(--text-primary); }
.empty-hint { font-size: var(--text-base); margin: 0; color: var(--text-muted); }
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
  background: var(--brand-gradient);
  color: var(--text-inverse);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: 4px;
}
.bubble {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-lg);
  font-size: var(--text-base);
  line-height: 1.5;
  word-break: break-word;
  position: relative;
}
.user-bubble {
  background: var(--brand-primary);
  color: var(--text-inverse);
  border-bottom-right-radius: var(--radius-sm);
  white-space: pre-wrap;
}
.assistant-bubble {
  background: var(--bg-card);
  color: var(--text-primary);
  border: 1px solid var(--border);
  border-bottom-left-radius: var(--radius-sm);
}
.system-bubble {
  background: var(--bg-subtle);
  color: var(--text-secondary);
  font-size: var(--text-sm);
  padding: var(--space-1) var(--space-3);
  white-space: pre-wrap;
}
.caret {
  display: inline-block;
  margin-left: 1px;
  color: var(--brand-primary);
  animation: blink 1s steps(1) infinite;
}
@keyframes blink {
  50% { opacity: 0; }
}

/* Markdown body */
.md-body :deep(p) { margin: 0 0 var(--space-2); }
.md-body :deep(p:last-child) { margin-bottom: 0; }
.md-body :deep(pre) {
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: var(--space-2);
  overflow-x: auto;
  font-size: var(--text-sm);
  margin: var(--space-2) 0;
}
.md-body :deep(code) {
  font-family: 'SF Mono', Menlo, monospace;
  font-size: var(--text-sm);
  background: var(--bg-subtle);
  padding: 1px 4px;
  border-radius: 3px;
}
.md-body :deep(pre code) {
  background: none;
  padding: 0;
}
.text-content.collapsed {
  max-height: calc(1.5em * 20);
  overflow: hidden;
  position: relative;
}
.text-content.collapsed::after {
  content: '';
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  height: 40px;
  background: linear-gradient(transparent, var(--bg-card));
}
.collapse-btn {
  display: block;
  margin-top: var(--space-1);
  padding: 2px 0;
  font-size: var(--text-xs);
  color: var(--brand-primary);
  background: none;
  border: none;
  cursor: pointer;
  font-weight: var(--font-weight-medium);
}
.content-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 8px;
}
.tool-card {
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
}
.tool-card summary {
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: var(--space-1);
  list-style: none;
}
.tool-card summary::-webkit-details-marker {
  display: none;
}
.tool-icon { font-size: var(--text-base); }
.tool-name { font-weight: var(--font-weight-semibold); font-family: monospace; }
.tool-duration {
  font-size: var(--text-xs);
  color: var(--text-muted);
}
.tool-state {
  margin-left: auto;
  padding: 2px var(--space-2);
  border-radius: var(--radius-full);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-medium);
}
.tool-state.state-running { background: var(--info-bg); color: var(--info); }
.tool-state.state-completed { background: var(--success-bg); color: var(--success); }
.tool-state.state-error { background: var(--danger-bg); color: var(--danger); }
.tool-state.state-pending { background: var(--bg-subtle); color: var(--text-muted); }
.tool-section {
  margin-top: var(--space-2);
  padding-top: var(--space-2);
  border-top: 1px dashed var(--border);
}
.tool-section.error { color: var(--danger); }
.tool-section-title { font-size: var(--text-xs); font-weight: var(--font-weight-semibold); color: var(--text-secondary); margin-bottom: 4px; }
.tool-section pre {
  margin: 0;
  font-size: var(--text-sm);
  font-family: 'SF Mono', Menlo, monospace;
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--overlay-subtle);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
}

/* Scroll-to-bottom button */
.scroll-bottom-btn {
  position: absolute;
  bottom: 80px;
  right: var(--space-4);
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--bg-card);
  border: 1px solid var(--border);
  box-shadow: var(--shadow-sm);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-secondary);
}

/* Error banner */
.error-banner {
  flex: 0 0 auto;
  background: var(--danger-bg);
  color: var(--danger);
  padding: var(--space-2) var(--space-4);
  font-size: var(--text-sm);
  text-align: center;
  border-top: 1px solid var(--border);
}

/* Input bar */
.input-bar {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-4);
  padding-bottom: calc(var(--space-3) + env(safe-area-inset-bottom));
  background: var(--bg-card);
  border-top: 1px solid var(--border);
}
.input-row {
  display: flex;
  align-items: flex-end;
  gap: var(--space-2);
}
.recording-strip {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  color: var(--danger);
  padding: 0 var(--space-1);
}
.rec-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--danger);
  animation: pulse 1s infinite;
}
.rec-bars {
  display: flex;
  align-items: center;
  gap: 2px;
  height: 14px;
}
.rec-bars i {
  display: block;
  width: 2px;
  height: 4px;
  background: var(--danger);
  border-radius: 1px;
  animation: wave 0.8s ease-in-out infinite;
}
.rec-bars i:nth-child(2) { animation-delay: 0.1s; }
.rec-bars i:nth-child(3) { animation-delay: 0.2s; }
.rec-bars i:nth-child(4) { animation-delay: 0.3s; }
.rec-bars i:nth-child(5) { animation-delay: 0.4s; }
@keyframes wave {
  0%, 100% { height: 4px; }
  50% { height: 14px; }
}
.stt-error {
  font-size: var(--text-xs);
  color: var(--danger);
  padding: 0 var(--space-1);
}
.input {
  flex: 1 1 auto;
  resize: none;
  max-height: 200px;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
  font-size: var(--text-base);
  line-height: 1.5;
  font-family: inherit;
  background: var(--bg-subtle);
  color: var(--text-primary);
  outline: none;
  transition: border-color 150ms;
}
.input:focus {
  border-color: var(--brand-primary);
  background: var(--bg-card);
}
.send-btn {
  flex: 0 0 auto;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--brand-primary);
  color: var(--text-inverse);
  border: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 150ms;
}
.send-btn:disabled {
  background: var(--border);
  color: var(--text-muted);
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
  background: var(--bg-subtle);
  color: var(--text-secondary);
  border: none;
  font-size: 18px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 150ms;
}
.voice-btn.recording {
  background: var(--danger);
  color: var(--text-inverse);
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
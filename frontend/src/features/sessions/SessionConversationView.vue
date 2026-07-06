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
import { useSessionStore } from '../../stores/session'
import { useVoiceRecording } from '../../composables/useVoiceRecording'
import { useToast } from '../../composables/useToast'
import { renderMarkdown } from '../../utils/markdown'

const route = useRoute()
const router = useRouter()
const store = useSessionStore()
const toast = useToast()

const sessionID = computed(() => route.params.id as string)
const instanceID = computed(() => (route.query.instance_id as string) || localStorage.getItem('selected_instance_id') || '')
const initialTitle = computed(() => (route.query.title as string) || '')

const inputText = ref('')
const sending = ref(false)
const messagesEl = ref<HTMLElement | null>(null)
const autoScroll = ref(true)

const { isRecording, transcribing, toggleRecording } = useVoiceRecording({
  onTranscribed(text) {
    // Append with a space if user already has text; otherwise replace.
    inputText.value = inputText.value
      ? `${inputText.value.trimEnd()} ${text}`
      : text
  },
  onError(msg) {
    toast.error(msg)
  },
})

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

// ── Voice Recording (via composable) ──
const toggleVoice = toggleRecording

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

// ── Markdown rendering (assistant messages) ──
// Local set tracks which long messages the user has expanded. We deliberately
// do NOT mutate the store's Message objects (Pinia prefers explicit state).
const LONG_LINE_THRESHOLD = 20
const PREVIEW_LINE_COUNT = 5
const PREVIEW_CHAR_LIMIT = 280
const expandedIds = ref<Set<string>>(new Set())

function isLong(msg: { text?: string }): boolean {
  if (!msg?.text) return false
  const lines = String(msg.text).split('\n').length
  return lines > LONG_LINE_THRESHOLD || msg.text.length > PREVIEW_CHAR_LIMIT * 2
}

function isExpanded(id: string): boolean {
  return expandedIds.value.has(id)
}

function previewText(text: string): string {
  const lines = text.split('\n').slice(0, PREVIEW_LINE_COUNT).join('\n')
  if (lines.length > PREVIEW_CHAR_LIMIT) {
    return lines.slice(0, PREVIEW_CHAR_LIMIT) + '…'
  }
  return lines + '…'
}

function renderedHtml(msg: { text?: string }): string {
  return renderMarkdown(msg.text || '')
}

function toggleExpanded(id: string) {
  const next = new Set(expandedIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expandedIds.value = next
}

// ── JSON pretty printing (with basic HTML escape) ──
// We tokenize carefully so quoted strings inside string values aren't
// accidentally re-colored. The strategy: alternate between string and
// non-string contexts starting from the first opening quote after `{`,
// `[`, or `,`.
function renderJson(value: any): string {
  let json: string
  try {
    json = JSON.stringify(value, null, 2)
  } catch {
    json = String(value)
  }
  // First, full HTML escape.
  let out = json
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')

  // Walk character-by-character to label only "real" JSON strings:
  // a `"` at the start, or after `:` / `[` / `,`, is a value/key opening.
  // Inside a string we just escape & keep raw. The next unescaped `"` closes it.
  let i = 0
  let result = ''
  let inStr = false
  while (i < out.length) {
    const ch = out[i]
    if (inStr) {
      if (ch === '\\' && i + 1 < out.length) {
        // pass through escape sequence verbatim
        result += ch + out[i + 1]
        i += 2
        continue
      }
      if (ch === '"') {
        inStr = false
        result += ch
        i++
        continue
      }
      result += ch
      i++
      continue
    }
    // not in string
    if (ch === '"') {
      // peek context — string is a JSON string if preceded by `{`, `[`, `,`, or `:`
      const prev = result.trimEnd().slice(-1)
      const isJsonString = prev === '{' || prev === '[' || prev === ',' || prev === ':'
      inStr = true
      if (isJsonString) {
        // decide color: if previous non-whitespace char is `:`, this is a value; else a key
        const trimmed = result.trimEnd()
        const isValue = trimmed.endsWith(':')
        const cls = isValue ? 'json-str' : 'json-key'
        result += `<span class="${cls}">`
        // find closing quote
        let j = i + 1
        while (j < out.length) {
          const c = out[j]
          if (c === '\\' && j + 1 < out.length) { j += 2; continue }
          if (c === '"') break
          j++
        }
        result += out.slice(i, j + 1)
        result += '</span>'
        inStr = false
        i = j + 1
        continue
      } else {
        // not a JSON string (shouldn't happen after escape, but fallback)
        result += ch
        i++
      }
      continue
    }
    result += ch
    i++
  }
  out = result

  // Color numbers, booleans, null at value positions (after `:` or `[` or `,`).
  out = out.replace(/([\[\,:]\s*)(-?\d+\.?\d*(?:[eE][+-]?\d+)?)\b/g, '$1<span class="json-num">$2</span>')
  out = out.replace(/([\[\,:]\s*)(true|false|null)\b/g, '$1<span class="json-bool">$2</span>')
  return out
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`
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
            <div v-if="msg.text" class="text-content markdown-body">
              <span v-if="isLong(msg)" class="caret-line"> </span>
              <div v-if="!isExpanded(msg.id) && isLong(msg)" class="collapsed">
                {{ previewText(msg.text) }}
              </div>
              <!-- eslint-disable-next-line vue/no-v-html -->
              <div
                v-else
                class="rendered"
                v-html="renderedHtml(msg)"
              ></div>
              <span v-if="msg.streaming" class="caret">▍</span>
              <button
                v-if="isLong(msg) && !msg.streaming"
                class="expand-btn"
                @click="toggleExpanded(msg.id)"
              >
                {{ isExpanded(msg.id) ? '收起' : '展开全部' }}
              </button>
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
                      <span v-if="c.durationMs" class="tool-duration">
                        {{ formatDuration(c.durationMs) }}
                      </span>
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
                      <!-- eslint-disable-next-line vue/no-v-html -->
                      <pre v-html="renderJson(c.input)"></pre>
                    </div>
                    <div v-if="c.output" class="tool-section">
                      <div class="tool-section-title">输出</div>
                      <!-- eslint-disable-next-line vue/no-v-html -->
                      <pre v-html="renderJson(c.output)"></pre>
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
        :placeholder="isRecording ? '🎙 录音中...' : transcribing ? '识别中...' : '输入消息…'"
        rows="1"
        @keydown="onKeydown"
        :disabled="sending || isRecording || transcribing"
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
  background: var(--bg-base);
  padding-top: env(safe-area-inset-top);
}

/* Top Bar */
.top-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2-5) var(--space-3);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}
.back-btn,
.stop-btn,
.top-spacer {
  flex: 0 0 auto;
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-full);
  background: transparent;
  border: none;
  cursor: pointer;
  color: var(--text-primary);
}
.back-btn:active,
.stop-btn:active {
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
  font-size: var(--text-md);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.subtitle {
  font-size: var(--text-xs);
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
  padding: var(--space-3);
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
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
.empty-icon { font-size: 40px; margin-bottom: var(--space-3); }
.empty-text { font-size: var(--text-lg); font-weight: var(--font-weight-medium); margin: 0 0 var(--space-1); color: var(--text-primary); }
.empty-hint { font-size: var(--text-sm); margin: 0; color: var(--text-muted); }
.message {
  display: flex;
  gap: var(--space-2);
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
  width: 26px;
  height: 26px;
  border-radius: var(--radius-full);
  background: var(--brand-gradient);
  color: var(--text-inverse);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: var(--space-1);
}
.bubble {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-lg);
  font-size: var(--text-base);
  line-height: 1.5;
  word-break: break-word;
  white-space: pre-wrap;
  position: relative;
}
.user-bubble {
  background: var(--brand-primary);
  color: var(--text-inverse);
  border-bottom-right-radius: var(--radius-sm);
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
  padding: var(--space-1) var(--space-2-5);
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
.content-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-top: var(--space-2);
}
.tool-card {
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-2-5);
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
.tool-icon { font-size: var(--text-sm); }
.tool-name { font-weight: var(--font-weight-semibold); font-family: monospace; font-size: var(--text-sm); }
.tool-state {
  margin-left: auto;
  padding: 2px var(--space-2);
  border-radius: var(--radius-full);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-medium);
}
.tool-state.state-running { background: rgba(59, 130, 246, 0.12); color: var(--info); }
.tool-state.state-completed { background: rgba(16, 185, 129, 0.12); color: var(--success); }
.tool-state.state-error { background: rgba(239, 68, 68, 0.12); color: var(--danger); }
.tool-state.state-pending { background: var(--bg-subtle); color: var(--text-muted); }
.tool-section {
  margin-top: var(--space-2);
  padding-top: var(--space-2);
  border-top: 1px dashed var(--border);
}
.tool-section.error { color: var(--danger); }
.tool-section-title { font-size: var(--text-xs); font-weight: var(--font-weight-semibold); color: var(--text-secondary); margin-bottom: var(--space-1); }
.tool-section pre {
  margin: 0;
  font-size: var(--text-xs);
  font-family: 'SF Mono', Menlo, monospace;
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--bg-subtle);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
}

/* Scroll-to-bottom button */
.scroll-bottom-btn {
  position: absolute;
  bottom: 80px;
  right: var(--space-3);
  width: 32px;
  height: 32px;
  border-radius: var(--radius-full);
  background: var(--bg-card);
  border: 1px solid var(--border);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-secondary);
}

/* Error banner */
.error-banner {
  flex: 0 0 auto;
  background: rgba(239, 68, 68, 0.1);
  color: var(--danger);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  text-align: center;
  border-top: 1px solid rgba(239, 68, 68, 0.2);
}

/* Input bar */
.input-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: flex-end;
  gap: var(--space-2);
  padding: var(--space-2-5) var(--space-3);
  padding-bottom: calc(var(--space-2-5) + env(safe-area-inset-bottom));
  background: var(--bg-card);
  border-top: 1px solid var(--border);
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
.input::placeholder {
  color: var(--text-muted);
}
.input:focus {
  border-color: var(--brand-primary);
  background: var(--bg-card);
}
.send-btn {
  flex: 0 0 auto;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-full);
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
  background: var(--bg-subtle);
  color: var(--text-muted);
  cursor: not-allowed;
}
.send-btn:not(:disabled):active {
  transform: scale(0.95);
}
.voice-btn {
  flex: 0 0 auto;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-full);
  background: var(--bg-subtle);
  color: var(--text-secondary);
  border: none;
  font-size: 16px;
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

/* ── Markdown rendering ── */
.markdown-body {
  font-size: var(--text-base);
  line-height: 1.5;
}
.markdown-body .rendered,
.markdown-body .collapsed {
  white-space: normal;
}
.markdown-body p {
  margin: 0 0 var(--space-2) 0;
}
.markdown-body p:last-child {
  margin-bottom: 0;
}
.markdown-body h1,
.markdown-body h2,
.markdown-body h3,
.markdown-body h4 {
  margin: var(--space-3) 0 var(--space-2) 0;
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  line-height: 1.3;
}
.markdown-body h1 { font-size: var(--text-xl); }
.markdown-body h2 { font-size: var(--text-lg); }
.markdown-body h3 { font-size: var(--text-md); }
.markdown-body h4 { font-size: var(--text-base); }
.markdown-body ul,
.markdown-body ol {
  margin: var(--space-2) 0;
  padding-left: var(--space-5);
}
.markdown-body li {
  margin: var(--space-1) 0;
}
.markdown-body code {
  font-family: 'SF Mono', Menlo, monospace;
  font-size: var(--text-sm);
  background: var(--bg-subtle);
  padding: 1px 5px;
  border-radius: var(--radius-sm);
  color: var(--text-primary);
}
.markdown-body pre {
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-3);
  overflow-x: auto;
  margin: var(--space-2) 0;
}
.markdown-body pre code {
  background: transparent;
  padding: 0;
  font-size: var(--text-xs);
  color: var(--text-primary);
}
.markdown-body blockquote {
  border-left: 3px solid var(--brand-primary);
  padding-left: var(--space-3);
  margin: var(--space-2) 0;
  color: var(--text-secondary);
}
.markdown-body a {
  color: var(--brand-primary);
  text-decoration: none;
}
.markdown-body a:hover {
  text-decoration: underline;
}
.markdown-body table {
  border-collapse: collapse;
  margin: var(--space-2) 0;
  width: 100%;
  font-size: var(--text-sm);
}
.markdown-body th,
.markdown-body td {
  border: 1px solid var(--border);
  padding: var(--space-1) var(--space-2);
  text-align: left;
}
.markdown-body th {
  background: var(--bg-subtle);
  font-weight: var(--font-weight-semibold);
}
.markdown-body hr {
  border: none;
  border-top: 1px solid var(--border);
  margin: var(--space-3) 0;
}
.markdown-body .collapsed {
  color: var(--text-secondary);
}
.expand-btn {
  display: inline-block;
  margin-top: var(--space-2);
  padding: var(--space-1) var(--space-2);
  background: transparent;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: var(--text-xs);
  color: var(--brand-primary);
  cursor: pointer;
  font-weight: var(--font-weight-semibold);
}
.expand-btn:active {
  background: var(--bg-subtle);
}

/* ── Tool duration & JSON syntax ── */
.tool-duration {
  margin-left: var(--space-2);
  font-size: var(--text-xs);
  color: var(--text-muted);
  font-family: monospace;
}
.tool-section pre :deep(.json-key) { color: var(--brand-primary); }
.tool-section pre :deep(.json-str) { color: var(--success); }
.tool-section pre :deep(.json-num) { color: var(--warning); }
.tool-section pre :deep(.json-bool) { color: var(--info); }
</style>
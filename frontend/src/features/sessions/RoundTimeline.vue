<script setup lang="ts">
/**
 * RoundTimeline — 会话工作台的事件流按轮折叠时间线（设计方案 v2 §4.3-2）。
 *
 * 轮边界 = 用户 prompt；编号 = 1-based 用户消息序数（与契约 round_index 同规则，
 * 分组逻辑在 useSessionEvents.groupMessagesIntoRounds，纯函数可测）。
 *
 * 交互：
 *   - 每轮头部 = 轮号 + round.completed 摘要（一句话结论 + `+x/-y · n 文件` +
 *     状态色点）；无事件时头部显示该轮首条用户消息截断；
 *   - 默认只展开最新轮（用户显式操作后记住选择）；自动滚底由父级的
 *     .messages 容器负责（autoScroll 机制不变：用户上滑阅读时不打断）；
 *   - 折叠轮显示「查看过程（N 个事件）」入口；
 *   - 工具输出截断 2 行 + 点击展开 + 复制按钮；diff 输出交给 DiffBlock
 *     （P2 纪律：不回退为内联全量渲染）。
 */
import { computed, reactive, ref } from 'vue'
import { renderMarkdown } from '../../utils/markdown'
import { useSpeech } from '../../composables/useSpeech'
import { useToast } from '../../composables/useToast'
import DiffBlock from '../../components/business/DiffBlock.vue'
import JsonBlock from '../../components/base/JsonBlock.vue'
import { extractDiffText } from '../../utils/diffParse.ts'
import {
  countRoundEvents,
  groupMessagesIntoRounds,
  roundSummaryFallback,
  truncate,
  type RoundCompletedData,
  type TimelineMessageLike,
} from './useSessionEvents'

const props = defineProps<{
  messages: TimelineMessageLike[]
  /** round_index → round.completed 数据（事件可用时非空）。 */
  rounds: Map<number, RoundCompletedData>
}>()

const toast = useToast()
const { supported: speechSupported, speakingId, speak: speakMessage } = useSpeech()

const groups = computed(() => groupMessagesIntoRounds(props.messages))

// 用户显式展开/折叠选择（轮 index → 是否展开）；未设置的轮走默认：仅最新轮展开。
const overrides = reactive(new Map<number, boolean>())

function isExpanded(index: number, isLast: boolean): boolean {
  return overrides.get(index) ?? isLast
}

function toggleRound(index: number, isLast: boolean): void {
  overrides.set(index, !isExpanded(index, isLast))
}

function roundData(index: number): RoundCompletedData | null {
  return props.rounds.get(index) ?? null
}

/** 轮头部摘要：事件结论优先，降级为该轮首条用户消息截断。 */
function headerSummary(index: number): string {
  const data = roundData(index)
  if (data) return truncate(data.summary, 80)
  const group = groups.value.find((g) => g.index === index)
  if (!group) return ''
  const user = group.messages.find((m) => m.role === 'user')
  return user ? truncate(user.text, 80) || roundSummaryFallback(group) : roundSummaryFallback(group)
}

function statusDotClass(index: number): string {
  const data = roundData(index)
  if (!data) return 'dot-none'
  if (data.status === 'completed') return 'dot-completed'
  if (data.status === 'error') return 'dot-error'
  return 'dot-cancelled'
}

// ── 长消息折叠（迁移自 SessionConversationView，不 mutate store 对象） ──
const LONG_LINE_THRESHOLD = 20
const PREVIEW_LINE_COUNT = 5
const PREVIEW_CHAR_LIMIT = 280
const expandedIds = ref<Set<string>>(new Set())

function isLong(msg: TimelineMessageLike): boolean {
  if (!msg?.text) return false
  const lines = String(msg.text).split('\n').length
  return lines > LONG_LINE_THRESHOLD || msg.text.length > PREVIEW_CHAR_LIMIT * 2
}

function isMsgExpanded(id: string): boolean {
  return expandedIds.value.has(id)
}

function previewText(text: string): string {
  const lines = text.split('\n').slice(0, PREVIEW_LINE_COUNT).join('\n')
  if (lines.length > PREVIEW_CHAR_LIMIT) {
    return lines.slice(0, PREVIEW_CHAR_LIMIT) + '…'
  }
  return lines + '…'
}

function renderedHtml(msg: TimelineMessageLike): string {
  return renderMarkdown(msg.text || '')
}

function toggleMsgExpanded(id: string): void {
  const next = new Set(expandedIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expandedIds.value = next
}

// ── 工具输出：截断 2 行 + 展开 + 复制 ──
const expandedTools = ref<Set<string>>(new Set())

function toolKey(msgId: string, idx: number): string {
  return `${msgId}:${idx}`
}

function isToolExpanded(key: string): boolean {
  return expandedTools.value.has(key)
}

function toggleToolExpanded(key: string): void {
  const next = new Set(expandedTools.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expandedTools.value = next
}

/** 折叠态的 2 行纯文本预览（diff 头 + 变更行；JSON 走 stringify）。 */
function outputPreview(output: unknown): string {
  const diff = extractDiffText(output)
  if (diff) {
    const m = diff.match(/^diff --git .*$/m)
    const fileLine = m ? `${m[0]}\n` : ''
    return truncate(`${fileLine}${diff.replace(/^diff --git .*$|^\+\+\+.*$|^---.*$|^@@.*$/gm, '').trim()}`, 160)
  }
  let text: string
  try {
    text = typeof output === 'string' ? output : JSON.stringify(output, null, 2) ?? ''
  } catch {
    text = String(output)
  }
  return truncate(text, 160)
}

function copyableText(output: unknown): string {
  const diff = extractDiffText(output)
  if (diff) return diff
  try {
    return typeof output === 'string' ? output : JSON.stringify(output, null, 2) ?? ''
  } catch {
    return String(output)
  }
}

async function copyOutput(output: unknown): Promise<void> {
  const text = copyableText(output)
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
    } else {
      // WebView 无 https clipboard 时的降级（P0 既有做法范围内）
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    toast.success('已复制')
  } catch {
    toast.error('复制失败')
  }
}

function hasToolValue(value: unknown): boolean {
  return value !== undefined && value !== null
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`
}

/** diff 文本按输出身份缓存（对象 WeakMap / 字符串有界 Map），迁移自旧视图。 */
const diffTextCache = new WeakMap<object, string | null>()
const stringDiffCache = new Map<string, boolean>()
function cachedDiffText(output: unknown): string | null {
  if (output && typeof output === 'object') {
    if (!diffTextCache.has(output)) diffTextCache.set(output, extractDiffText(output))
    return diffTextCache.get(output) ?? null
  }
  if (typeof output !== 'string') return null
  if (!stringDiffCache.has(output)) {
    if (stringDiffCache.size >= 256) stringDiffCache.clear()
    stringDiffCache.set(output, Boolean(extractDiffText(output)))
  }
  return stringDiffCache.get(output) ? output : null
}
</script>

<template>
  <div class="round-timeline">
    <section
      v-for="(group, gi) in groups"
      :key="group.index"
      class="round"
      :class="{ last: gi === groups.length - 1 }"
    >
      <button
        type="button"
        class="round-head"
        :aria-expanded="isExpanded(group.index, gi === groups.length - 1)"
        @click="toggleRound(group.index, gi === groups.length - 1)"
      >
        <span class="status-dot" :class="statusDotClass(group.index)" aria-hidden="true"></span>
        <span class="round-no">轮 {{ group.index }}</span>
        <span v-if="roundData(group.index)" class="round-changes">
          +{{ roundData(group.index)!.changes.added }}/-{{ roundData(group.index)!.changes.removed }} · {{ roundData(group.index)!.changes.files }} 文件
        </span>
        <span class="round-summary">{{ headerSummary(group.index) }}</span>
        <span class="chevron" aria-hidden="true">
          {{ isExpanded(group.index, gi === groups.length - 1) ? '▾' : '▸' }}
        </span>
      </button>

      <!-- 展开的轮：完整消息（用户 prompt → assistant 动作序列） -->
      <div v-if="isExpanded(group.index, gi === groups.length - 1)" class="round-body">
        <div
          v-for="msg in group.messages"
          :key="msg.id"
          class="message"
          :class="['role-' + msg.role, { streaming: msg.streaming }]"
        >
          <template v-if="msg.role === 'user'">
            <div class="bubble user-bubble">{{ msg.text }}</div>
          </template>

          <template v-else-if="msg.role === 'assistant'">
            <div class="avatar assistant-avatar">AI</div>
            <div class="bubble assistant-bubble">
              <div v-if="msg.text" class="text-content markdown-body">
                <div v-if="!isMsgExpanded(msg.id) && isLong(msg)" class="collapsed">
                  {{ previewText(msg.text) }}
                </div>
                <!-- eslint-disable-next-line vue/no-v-html -->
                <div v-else class="rendered" v-html="renderedHtml(msg)"></div>
                <span v-if="msg.streaming" class="caret">▍</span>
                <button
                  v-if="isLong(msg) && !msg.streaming"
                  class="inline-btn"
                  @click="toggleMsgExpanded(msg.id)"
                >
                  {{ isMsgExpanded(msg.id) ? '收起' : '展开全部' }}
                </button>
                <button
                  v-if="speechSupported && msg.text && !msg.streaming"
                  class="inline-btn"
                  :aria-label="speakingId === msg.id ? '停止朗读' : '朗读这条回复'"
                  :aria-pressed="speakingId === msg.id"
                  @click="speakMessage(msg.id, msg.text)"
                >
                  {{ speakingId === msg.id ? '⏹ 停止朗读' : '🔊 朗读' }}
                </button>
              </div>
              <div v-if="msg.content" class="content-list">
                <div
                  v-for="(c, ci) in msg.content"
                  :key="ci"
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
                      <div v-if="hasToolValue(c.input)" class="tool-section">
                        <div class="tool-section-title">输入</div>
                        <JsonBlock :data="c.input" />
                      </div>
                      <div v-if="hasToolValue(c.output)" class="tool-section">
                        <div class="tool-section-title-row">
                          <div class="tool-section-title">输出</div>
                          <button type="button" class="copy-btn" @click="copyOutput(c.output)">
                            复制
                          </button>
                        </div>
                        <!-- 折叠态：2 行纯文本预览；展开态：DiffBlock / 完整 JSON -->
                        <div
                          v-if="!isToolExpanded(toolKey(msg.id, ci))"
                          class="output-preview"
                          role="button"
                          tabindex="0"
                          @click="toggleToolExpanded(toolKey(msg.id, ci))"
                          @keydown.enter.prevent="toggleToolExpanded(toolKey(msg.id, ci))"
                        >
                          {{ outputPreview(c.output) }}
                        </div>
                        <template v-else>
                          <DiffBlock v-if="cachedDiffText(c.output)" :diff="cachedDiffText(c.output)!" />
                          <JsonBlock v-else :data="c.output" />
                          <button
                            type="button"
                            class="copy-btn block-copy"
                            @click="toggleToolExpanded(toolKey(msg.id, ci))"
                          >
                            收起输出
                          </button>
                        </template>
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

          <template v-else>
            <div class="bubble system-bubble">{{ msg.text }}</div>
          </template>
        </div>
      </div>

      <!-- 折叠轮：过程事件入口 -->
      <button
        v-else
        type="button"
        class="round-toggle"
        @click="toggleRound(group.index, gi === groups.length - 1)"
      >
        查看过程（{{ countRoundEvents(group) }} 个事件）
      </button>
    </section>
  </div>
</template>

<style scoped>
.round-timeline {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

/* ── 轮头部（热区 ≥44px，无 :hover 依赖） ── */
.round {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.round-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 44px; /* 触摸热区 ≥44px */
  padding: var(--space-1) var(--space-2);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
  text-align: left;
  width: 100%;
  color: var(--text-primary);
}
.round-head:active {
  background: var(--bg-subtle);
}
.status-dot {
  flex: 0 0 auto;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-muted);
}
.status-dot.dot-completed { background: var(--success); }
.status-dot.dot-error { background: var(--danger); }
.status-dot.dot-cancelled { background: var(--warning, #f59e0b); }
.status-dot.dot-none { background: var(--text-muted); }
.round-no {
  flex: 0 0 auto;
  font-weight: var(--font-weight-semibold);
  white-space: nowrap;
}
.round-changes {
  flex: 0 0 auto;
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.round-summary {
  flex: 1 1 auto;
  min-width: 0;
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.chevron {
  flex: 0 0 auto;
  color: var(--text-muted);
}

.round-toggle {
  align-self: flex-start;
  min-height: 44px; /* 触摸热区 ≥44px */
  padding: var(--space-1) var(--space-3);
  border: 1px dashed var(--border);
  border-radius: var(--radius-full);
  background: transparent;
  color: var(--brand-primary);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
}
.round-toggle:active {
  background: var(--bg-subtle);
}

/* ── 轮内消息（样式迁移自 SessionConversationView） ── */
.round-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
}
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
.inline-btn {
  display: inline-block;
  margin-top: var(--space-2);
  margin-right: var(--space-2);
  padding: var(--space-1) var(--space-2);
  background: transparent;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: var(--text-xs);
  color: var(--brand-primary);
  font-weight: var(--font-weight-semibold);
  min-height: 32px;
}
.inline-btn:active {
  background: var(--bg-subtle);
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
  min-height: 44px; /* summary 热区 ≥44px */
}
.tool-card summary::-webkit-details-marker {
  display: none;
}
.tool-icon { font-size: var(--text-sm); }
.tool-name {
  font-weight: var(--font-weight-semibold);
  font-family: monospace;
  font-size: var(--text-sm);
}
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
.tool-duration {
  margin-left: var(--space-2);
  font-size: var(--text-xs);
  color: var(--text-muted);
  font-family: monospace;
}
.tool-section {
  margin-top: var(--space-2);
  padding-top: var(--space-2);
  border-top: 1px dashed var(--border);
}
.tool-section.error { color: var(--danger); }
.tool-section-title {
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  color: var(--text-secondary);
  margin-bottom: var(--space-1);
}
.tool-section-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.tool-section-title-row .tool-section-title {
  margin-bottom: 0;
}
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

/* 折叠态输出预览：2 行截断（设计 §4.3-2） */
.output-preview {
  margin-top: var(--space-1);
  padding: var(--space-1) var(--space-2);
  background: var(--bg-subtle);
  border-radius: var(--radius-sm);
  font-size: var(--text-xs);
  font-family: 'SF Mono', Menlo, monospace;
  color: var(--text-secondary);
  white-space: pre-line;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  cursor: pointer;
  min-height: 44px; /* 预览热区 ≥44px，点击展开完整输出 */
}
.copy-btn {
  flex: 0 0 auto;
  min-height: 32px;
  padding: var(--space-1) var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
  color: var(--brand-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
}
.copy-btn:active {
  background: var(--bg-subtle);
}
.block-copy {
  margin-top: var(--space-1);
}

/* ── Markdown 渲染（迁移） ── */
.markdown-body {
  font-size: var(--text-base);
  line-height: 1.5;
}
.markdown-body .rendered,
.markdown-body .collapsed {
  white-space: normal;
}
.markdown-body :deep(p) { margin: 0 0 var(--space-2) 0; }
.markdown-body :deep(p:last-child) { margin-bottom: 0; }
.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4) {
  margin: var(--space-3) 0 var(--space-2) 0;
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  line-height: 1.3;
}
.markdown-body :deep(h1) { font-size: var(--text-xl); }
.markdown-body :deep(h2) { font-size: var(--text-lg); }
.markdown-body :deep(h3) { font-size: var(--text-md); }
.markdown-body :deep(h4) { font-size: var(--text-base); }
.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  margin: var(--space-2) 0;
  padding-left: var(--space-5);
}
.markdown-body :deep(li) { margin: var(--space-1) 0; }
.markdown-body :deep(code) {
  font-family: 'SF Mono', Menlo, monospace;
  font-size: var(--text-sm);
  background: var(--bg-subtle);
  padding: 1px 5px;
  border-radius: var(--radius-sm);
  color: var(--text-primary);
}
.markdown-body :deep(pre) {
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-3);
  overflow-x: auto;
  margin: var(--space-2) 0;
}
.markdown-body :deep(pre code) {
  background: transparent;
  padding: 0;
  font-size: var(--text-xs);
  color: var(--text-primary);
}
.markdown-body :deep(blockquote) {
  border-left: 3px solid var(--brand-primary);
  padding-left: var(--space-3);
  margin: var(--space-2) 0;
  color: var(--text-secondary);
}
.markdown-body :deep(a) {
  color: var(--brand-primary);
  text-decoration: none;
}
.markdown-body :deep(table) {
  border-collapse: collapse;
  margin: var(--space-2) 0;
  width: 100%;
  font-size: var(--text-sm);
}
.markdown-body :deep(th),
.markdown-body :deep(td) {
  border: 1px solid var(--border);
  padding: var(--space-1) var(--space-2);
  text-align: left;
}
.markdown-body :deep(th) {
  background: var(--bg-subtle);
  font-weight: var(--font-weight-semibold);
}
.markdown-body :deep(hr) {
  border: none;
  border-top: 1px solid var(--border);
  margin: var(--space-3) 0;
}
.markdown-body .collapsed {
  color: var(--text-secondary);
}
</style>

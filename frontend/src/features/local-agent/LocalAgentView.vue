<!--
  LocalAgentView — 手机端内置本地智能体(openhands 事件时间线 × pi skills/experts/tools)。

  布局:单列时间线(用户气泡 / 助手分段 / 工具卡 / 计划卡)+ 底部 composer。
  重交互(审批条、专家/技能选择)都是流内/贴底卡片,不做浮层——移动端惯例
  对齐 openhands 移动策略(聊天单列,面板即页面)。
-->
<template>
  <div class="local-agent">
    <!-- 顶部工具行:会话切换 / 新任务 / 专家 / 技能 -->
    <div class="toolbar">
      <select
        class="session-select"
        :value="store.activeId ?? ''"
        aria-label="切换任务会话"
        @change="onSessionChange"
      >
        <option v-for="s in store.sessions" :key="s.id" :value="s.id">{{ s.title }}</option>
        <option v-if="!store.sessions.length" value="">新任务</option>
      </select>
      <button class="chip-btn" type="button" @click="store.newSession(expert)">
        <span class="material-symbols-outlined" aria-hidden="true">add</span>新任务
      </button>
      <button class="chip-btn" type="button" @click="showPickers = !showPickers">
        <span class="material-symbols-outlined" aria-hidden="true">tune</span>{{ expertName }}
      </button>
    </div>

    <div v-if="showPickers" class="pickers">
      <p class="picker-label">专家</p>
      <div class="chip-row" role="radiogroup" aria-label="选择专家">
        <button
          v-for="e in store.experts"
          :key="e.name"
          class="chip"
          :class="{ active: expert === e.name }"
          type="button"
          role="radio"
          :aria-checked="expert === e.name"
          @click="expert = e.name"
        >
          {{ e.name === 'general' ? '通用' : expertLabel(e.name) }}
        </button>
      </div>
      <p class="picker-label">技能(可多选,随消息注入)</p>
      <div class="chip-row">
        <button
          v-for="sk in store.skills"
          :key="sk.name"
          class="chip"
          :class="{ active: pickedSkills.has(sk.name) }"
          type="button"
          :aria-pressed="pickedSkills.has(sk.name)"
          @click="toggleSkill(sk.name)"
        >
          {{ sk.name }}
        </button>
      </div>
      <p class="picker-desc">{{ expertDesc }}</p>
    </div>

    <!-- 事件时间线 -->
    <div ref="timelineEl" class="timeline" aria-live="polite">
      <p v-if="!timeline.length" class="empty">
        让本地智能体帮你算数、查信息、整理文件。试试:<br />「现在几点?帮我算 23×7+128」<br />「把「今天买牛奶」存到 notes/todo.md」
      </p>
      <template v-for="(it, i) in timeline" :key="i">
        <div v-if="it.kind === 'user'" class="row user">
          <div class="bubble user-bubble">{{ it.text }}</div>
        </div>
        <div v-else-if="it.kind === 'assistant'" class="row assistant">
          <div class="bubble ai-bubble" :class="{ interim: it.interim }">{{ displayText(it) }}</div>
        </div>
        <div v-else-if="it.kind === 'tool'" class="row">
          <ToolCallCard :item="it" />
        </div>
        <div v-else-if="it.kind === 'plan'" class="row">
          <PlanCard :item="it" />
        </div>
        <p v-else-if="it.kind === 'system'" class="system-note">{{ it.text }}</p>
      </template>
      <div v-if="statusLabel" class="row status-row">
        <span class="status-pill" :class="`p-${store.activeSession?.status}`">{{ statusLabel }}</span>
      </div>
    </div>

    <!-- 审批条(medium/high 工具执行前) -->
    <ApprovalBar
      v-if="store.pendingApproval"
      :approval="store.pendingApproval"
      @respond="(allow) => store.respondApproval(allow)"
    />

    <!-- Composer -->
    <div class="composer">
      <textarea
        v-model="draft"
        class="draft"
        rows="1"
        :placeholder="placeholder"
        :disabled="store.running && !store.pendingApproval"
        @keydown.enter.exact.prevent="onEnter"
      />      <button
        v-if="store.running"
        class="send-btn stop"
        type="button"
        aria-label="停止"
        @click="store.stop()"
      >
        <span class="material-symbols-outlined" aria-hidden="true">stop_circle</span>
      </button>
      <button
        v-else
        class="send-btn"
        type="button"
        aria-label="发送"
        :disabled="!draft.trim()"
        @click="send()"
      >
        <span class="material-symbols-outlined" aria-hidden="true">send</span>
      </button>
    </div>
    <!--
      键盘提示从 placeholder 挪到这里（2026-10-03）。它在 360px 视口里被截断，
      而这里有整行宽度。执行中时不再重复「Enter 发送」——那时输入框是禁用的。
    -->
    <p v-if="!store.running" class="key-hint">{{ COMPOSER_TEXT.keyHint }}</p>
    <p v-if="usageText" class="usage">{{ usageText }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useLocalAgentStore } from './agentStore'
import ToolCallCard from './ToolCallCard.vue'
import ApprovalBar from './ApprovalBar.vue'
import PlanCard from './PlanCard.vue'
import type { TimelineItem } from '../../localagent/runtime.ts'

const store = useLocalAgentStore()
const draft = ref('')
const expert = ref('general')
const pickedSkills = ref(new Set<string>())
const showPickers = ref(false)
const timelineEl = ref<HTMLElement | null>(null)

onMounted(() => {
  store.init()
})

const timeline = computed(() => store.activeSession?.timeline ?? [])

const expertName = computed(() => (expert.value === 'general' ? '通用' : expertLabel(expert.value)))

const expertDesc = computed(() => store.experts.find((e) => e.name === expert.value)?.description ?? '')

function expertLabel(name: string): string {
  const map: Record<string, string> = {
    general: '通用',
    'trip-planner': '行程规划',
    'notes-writer': '文书整理',
    'quick-calc': '速算换算',
  }
  return map[name] ?? name
}

// 2026-10-03：placeholder 原来把键盘提示也塞了进去
// （「给本地智能体下达任务(Enter 发送,Shift+Enter 换行)」，31 个字符）。
// 真机 CSS 视口只有 360px，那行字在输入框里被截到句子中间，看着像排版坏了。
//
// 现在 placeholder 只说「做什么」，键盘提示移到输入框下方的 hint 行
// —— 那是它该待的地方：提示语属于辅助信息，不该挤占输入框里最显眼的位置。
//
// 刻意**不**为此引入 vue-i18n：本组件通篇是硬编码中文（expertLabel 的本地
// map 也是这么写的），单独给两行文案接 i18n 会造成「一半多语言、一半不」的
// 更糟状态。真正要修的是整个组件的本地化，那是另一件事。
const COMPOSER_TEXT = {
  running: '任务执行中…',
  placeholder: '给本地智能体下达任务',
  keyHint: 'Enter 发送 · Shift+Enter 换行',
} as const

const placeholder = computed(() => (store.running ? COMPOSER_TEXT.running : COMPOSER_TEXT.placeholder))

const statusLabel = computed(() => {
  const s = store.activeSession?.status
  switch (s) {
    case 'thinking':
      return '思考中…'
    case 'tool_running':
      return '执行工具…'
    case 'waiting_approval':
      return '等待确认'
    case 'error':
      return '已中断'
    case 'aborted':
      return '已停止'
    default:
      return ''
  }
})

const usageText = computed(() => {
  const u = store.activeSession?.usage
  if (!u || (u.promptTokens === 0 && u.completionTokens === 0)) return ''
  return `本会话 tokens:输入 ${u.promptTokens} · 输出 ${u.completionTokens}`
})

/** 流式尾巴隐藏尾部协议围栏(模型边流边输出 ```json 块时,不把协议暴露给用户)。 */
function displayText(it: TimelineItem): string {
  const text = it.text ?? ''
  if (!it.interim) return text
  const idx = text.lastIndexOf('```')
  return idx >= 0 ? text.slice(0, idx) : text
}

function onSessionChange(e: Event) {
  const v = (e.target as HTMLSelectElement).value
  if (v) store.selectSession(v)
}

function toggleSkill(name: string) {
  const next = new Set(pickedSkills.value)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  pickedSkills.value = next
}

function onEnter(e?: KeyboardEvent) {
  // 中文输入法组合期间按 Enter 是「确认候选词」,不能当发送( Otherwise 选字即发出)。
  if (e && (e.isComposing || e.keyCode === 229)) return
  if (store.running) return
  send()
}

function send() {
  const text = draft.value.trim()
  if (!text) return
  draft.value = ''
  store.send(text, { expert: expert.value, skills: [...pickedSkills.value] })
  scrollToBottom()
}

function scrollToBottom() {
  void nextTick(() => {
    // 用瞬时滚动:text_delta 每帧都会触发,smooth 动画互相打断会卡顿。
    timelineEl.value?.scrollTo({ top: timelineEl.value.scrollHeight })
  })
}

watch(timeline, () => scrollToBottom(), { deep: true })
watch(() => store.activeId, () => {
  showPickers.value = false
  scrollToBottom()
})
</script>

<style scoped>
.local-agent {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.toolbar {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--border);
  background: var(--bg-card);
}
.session-select {
  flex: 1;
  min-width: 0;
  height: 32px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-subtle);
  color: var(--text-primary);
  font-size: var(--text-smd);
  padding: 0 var(--space-2);
  text-overflow: ellipsis;
}
.chip-btn {
  display: flex;
  align-items: center;
  gap: 2px;
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
  background: var(--bg-subtle);
  color: var(--text-secondary);
  font-size: var(--text-sm);
  white-space: nowrap;
  cursor: pointer;
}
.chip-btn .material-symbols-outlined { font-size: var(--text-lg); }

.pickers {
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--border);
  background: var(--bg-card);
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.picker-label { margin: 0; font-size: var(--text-2xs); color: var(--text-muted); }
.picker-desc { margin: 0; font-size: var(--text-sm); color: var(--text-secondary); }
.chip-row { display: flex; flex-wrap: wrap; gap: 6px; }
.chip {
  padding: 4px 12px;
  border-radius: var(--radius-full);
  border: 1px solid var(--border);
  background: var(--bg-subtle);
  color: var(--text-secondary);
  font-size: var(--text-sm);
  cursor: pointer;
}
.chip.active {
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  border-color: var(--brand-primary, #4c8dff);
  color: var(--brand-primary, #4c8dff);
}

.timeline {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-3);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.empty {
  margin: auto;
  text-align: center;
  color: var(--text-muted);
  font-size: var(--text-smd);
  line-height: 2;
}
.row { display: flex; flex-direction: column; }
.row.user { align-items: flex-end; }
.row.assistant { align-items: flex-start; }

.bubble {
  max-width: 86%;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  font-size: var(--text-base);
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}
.user-bubble {
  background: var(--brand-primary, #4c8dff);
  color: #fff;
  border-bottom-right-radius: var(--radius-sm);
}
.ai-bubble {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-bottom-left-radius: var(--radius-sm);
}
.ai-bubble.interim { color: var(--text-secondary); }

.status-row { align-items: center; }
.system-note {
  margin: 0 auto;
  text-align: center;
  font-size: var(--text-sm);
  color: var(--danger, #e5484d);
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-3);
  max-width: 90%;
}
.status-pill {
  font-size: var(--text-2xs);
  color: var(--text-muted);
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  padding: 2px 10px;
  border-radius: var(--radius-full);
}
.status-pill.p-error, .status-pill.p-aborted { color: var(--danger, #e5484d); }

.composer {
  display: flex;
  align-items: flex-end;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3) var(--space-3);
  border-top: 1px solid var(--border);
  background: var(--bg-card);
}
.draft {
  flex: 1;
  min-height: 40px;
  max-height: 120px;
  resize: none;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
  color: var(--text-primary);
  font-size: var(--text-base);
  padding: var(--space-2) var(--space-3);
  line-height: 1.5;
}
.send-btn {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  border: none;
  background: var(--brand-primary, #4c8dff);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex-shrink: 0;
}
.send-btn:disabled { opacity: 0.4; }
.send-btn.stop { background: var(--danger, #e5484d); }

.usage {
  margin: 0;
  padding: 0 var(--space-3) var(--space-1);
  text-align: right;
  font-size: var(--text-xs);
  color: var(--text-muted);
  background: var(--bg-card);
}

/* 键盘提示（2026-10-03 从 placeholder 挪到这里）。
   左对齐、极淡、整行宽度 —— 它是辅助信息，不该和 placeholder 抢同一个位置。 */
.key-hint {
  margin: 0;
  padding: 0 var(--space-3) var(--space-1);
  font-size: var(--text-xs);
  color: var(--text-muted);
  background: var(--bg-card);
}
</style>

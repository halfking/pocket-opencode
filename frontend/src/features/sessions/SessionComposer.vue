<script lang="ts">
/**
 * SessionComposerTarget — 契约 §4 冻结的目标描述（targets 可切换模式）。
 * 独立 <script> 块以便对外导出类型（script setup 内不允许 export）。
 */
export interface SessionComposerTarget {
  id: string
  label: string
}
</script>

<script setup lang="ts">
/**
 * SessionComposer — 会话输入系统（P1，契约 §4 / 设计 v2 §4.4；P1.5 界面减负改造）。
 *
 * 两种目标模式：
 *   - 固定模式（P1 工作台）：sessionId + sessionLabel；P1.5 起目标 chip 不再
 *     常驻（会话标识由工作台头部承担），输入行以 bolt 快速指令按钮开场；
 *   - 可切换模式（契约就绪）：targets + modelTarget，chip 点击弹出切换面板，
 *     选中经 update:target 上抛（v-model 用法见契约注记：prop 名冻结为
 *     modelTarget，父组件用 :model-target + @update:target 绑定）。
 *
 * 行为纪律：
 *   - 指令模板收进 bolt 快速指令面板（P1.5：原常驻 chips 行释放整行高度），
 *     面板内一点即发（emit send + 清草稿），仅"停下"先二次确认；
 *   - voice/STT 转写只入草稿可编辑（追加，不直发）；
 *   - 草稿按会话存 SQLite（500ms 防抖），send 时清除；
 *   - 发送按钮 44px 热区贴右下，容器 safe-area 适配。
 *
 * 本组件由 A（SessionConversationView）挂载，接线由主代理完成。
 */
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useVoiceRecording } from '../../composables/useVoiceRecording'
import { useToast } from '../../composables/useToast'
import { useConfirm } from '../../composables/useConfirm'
import { BottomSheet } from '../../components'
import {
  QUICK_COMMANDS,
  appendToDraft,
  applyInitialText,
  shouldConfirmCommand,
  truncateChipLabel,
  useSessionDrafts,
  type QuickCommand,
} from './useSessionDrafts'

const props = withDefaults(
  defineProps<{
    /** 固定目标模式：当前会话（草稿 key）。 */
    sessionId: string
    /** chip 文案；缺省用 sessionId 截断。 */
    sessionLabel?: string
    /** 可切换目标模式（P1 仅契约就绪）。 */
    targets?: SessionComposerTarget[]
    /** targets 模式下当前选中目标 id（v-model:target 的受控值）。 */
    modelTarget?: string
    /** 外部 sending 等禁用。 */
    disabled?: boolean
    /** ?prompt= 深链一次性预填（追加，不覆盖已有输入）。 */
    initialText?: string
  }>(),
  {
    sessionLabel: '',
    disabled: false,
    initialText: '',
  },
)

const emit = defineEmits<{
  (e: 'send', text: string): void
  (e: 'update:target', id: string): void
}>()

// ── 目标解析（固定 / 可切换两模式统一为 activeTargetId） ──
const hasTargets = computed(() => (props.targets?.length ?? 0) > 0)

const activeTargetId = computed(() => {
  if (!hasTargets.value) return props.sessionId
  const matched = props.targets?.find((t) => t.id === props.modelTarget)
  return matched ? matched.id : (props.targets?.[0]?.id ?? props.sessionId)
})

const activeTargetLabel = computed(
  () =>
    props.targets?.find((t) => t.id === activeTargetId.value)?.label ||
    props.sessionLabel ||
    props.sessionId,
)

const targetChipLabel = computed(() => truncateChipLabel(activeTargetLabel.value))

// ── 草稿（key 跟随当前目标：固定模式 = sessionId，targets 模式 = 选中目标） ──
const drafts = useSessionDrafts({ sessionId: () => activeTargetId.value })
const draftText = drafts.text

// initialText 一次性预填：watch immediate，仅首个非空值生效
let initialTextApplied = false
watch(
  () => props.initialText,
  (value) => {
    if (initialTextApplied || !value) return
    initialTextApplied = true
    draftText.value = applyInitialText(draftText.value, value)
  },
  { immediate: true },
)

// ── 发送 / 指令模板 ──
function send(): void {
  if (props.disabled) return
  const value = draftText.value.trim()
  if (!value) return
  emit('send', value)
  // 契约 §4：emit('send') 时组件内已清草稿
  void drafts.clear()
}

// P1.5：模板 chips 收进快速指令面板（释放常驻整行；面板行内文案完整可读，
// "停下"二次确认纪律不变）
const quickPanelVisible = ref(false)

async function onCommand(cmd: QuickCommand): Promise<void> {
  if (props.disabled) return
  // 仅"停下"先二次确认（统一走全局 ConfirmDialog）
  if (shouldConfirmCommand(cmd) && !(await confirm({ title: cmd.label, message: cmd.confirmText ?? '', confirmText: '确认', danger: true }))) return
  quickPanelVisible.value = false
  emit('send', cmd.message)
  void drafts.clear()
}

// ── 目标切换面板（targets 模式） ──
const targetPickerVisible = ref(false)

function openTargetPicker(): void {
  if (!hasTargets.value || props.disabled) return
  targetPickerVisible.value = true
}

function selectTarget(id: string): void {
  targetPickerVisible.value = false
  if (!hasTargets.value || id === activeTargetId.value) return
  emit('update:target', id)
}

// ── 语音（复用现有 composable；转写入草稿可编辑，不直发） ──
const toast = useToast()
const { confirm } = useConfirm()
const { isRecording, transcribing, toggleRecording } = useVoiceRecording({
  onTranscribed: (text) => {
    draftText.value = appendToDraft(draftText.value, text)
  },
  onError: (message) => {
    toast.error(message)
  },
})

// ── 输入框状态 ──
const inputDisabled = computed(() => props.disabled || isRecording.value || transcribing.value)
const canSend = computed(() => !props.disabled && draftText.value.trim() !== '')
const placeholder = computed(() =>
  isRecording.value ? '🎙 录音中...' : transcribing.value ? '识别中...' : '输入消息…',
)

// textarea 自适应行高（1-4 行）：JS 撑高 + CSS max-height 截断
const textareaEl = ref<HTMLTextAreaElement | null>(null)

async function autoResize(): Promise<void> {
  const el = textareaEl.value
  if (!el) return
  await nextTick()
  el.style.height = 'auto'
  el.style.height = `${el.scrollHeight}px`
}

watch(draftText, () => {
  void autoResize()
})
onMounted(() => {
  void autoResize()
})

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
  }
}
</script>

<template>
  <div class="composer" :class="{ disabled: props.disabled }">
    <!-- 输入行：快速指令(bolt) + textarea + voice + send（44px 热区贴右下拇指区）
         P1.5：固定目标模式下目标 chip 不再渲染（会话标识由工作台头部承担），
         指令模板 chips 常驻行收进 bolt 面板，释放整行高度；targets 可切换
         模式保留 chip（切换职责，契约 §4 就绪）。 -->
    <div class="input-row">
      <button
        type="button"
        class="quick-btn"
        :disabled="props.disabled"
        aria-haspopup="dialog"
        :aria-expanded="quickPanelVisible"
        aria-label="快速指令"
        @click="quickPanelVisible = true"
      >
        <span class="material-symbols-outlined">bolt</span>
      </button>

      <button
        v-if="hasTargets"
        type="button"
        class="target-chip switchable"
        :disabled="props.disabled"
        aria-label="切换目标会话"
        @click="openTargetPicker"
      >
        <span class="material-symbols-outlined chip-icon">forum</span>
        <span class="target-label">{{ targetChipLabel }}</span>
        <span class="material-symbols-outlined chip-icon">expand_more</span>
      </button>

      <textarea
        ref="textareaEl"
        v-model="draftText"
        class="input"
        rows="1"
        :placeholder="placeholder"
        :disabled="inputDisabled"
        @keydown="onKeydown"
      ></textarea>

      <button
        type="button"
        class="voice-btn"
        :class="{ recording: isRecording }"
        :disabled="props.disabled"
        :aria-label="isRecording ? '停止录音' : '语音输入'"
        @click="toggleRecording"
      >
        {{ isRecording ? '⏹' : '🎙' }}
      </button>

      <button
        type="button"
        class="send-btn"
        :disabled="!canSend"
        aria-label="发送"
        @click="send"
      >
        <span class="material-symbols-outlined">send</span>
      </button>
    </div>

    <!-- 快速指令面板（P1.5：模板 chips 收纳处；文案完整可读，"停下"保留二次确认） -->
    <BottomSheet v-model="quickPanelVisible" title="快速指令">
      <div class="quick-list" role="menu" aria-label="指令模板">
        <button
          v-for="cmd in QUICK_COMMANDS"
          :key="cmd.label"
          type="button"
          class="quick-item"
          :class="{ danger: shouldConfirmCommand(cmd) }"
          role="menuitem"
          :disabled="props.disabled"
          @click="onCommand(cmd)"
        >
          <span class="material-symbols-outlined item-icon" aria-hidden="true">
            {{ cmd.icon ?? 'bolt' }}
          </span>
          <span class="item-label">{{ cmd.label }}</span>
        </button>
      </div>
    </BottomSheet>

    <!-- 目标切换面板（仅 targets 模式；复用工程既有 BottomSheet） -->
    <BottomSheet v-model="targetPickerVisible" title="切换目标会话">
      <div class="target-list">
        <button
          v-for="t in props.targets ?? []"
          :key="t.id"
          type="button"
          class="target-option"
          :class="{ active: t.id === activeTargetId }"
          :aria-pressed="t.id === activeTargetId"
          @click="selectTarget(t.id)"
        >
          <span class="material-symbols-outlined option-icon">{{
            t.id === activeTargetId ? 'check_circle' : 'radio_button_unchecked'
          }}</span>
          <span class="option-label">{{ t.label }}</span>
        </button>
      </div>
    </BottomSheet>
  </div>
</template>

<style scoped>
.composer {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  /* 刘海屏既有方案沿用：底部安全区（设计 §4.4-4） */
  padding-bottom: calc(var(--space-2-5) + env(safe-area-inset-bottom));
  background: var(--bg-card);
  border-top: 1px solid var(--border);
}

/* ── 快速指令按钮（原模板 chips 行收敛为一个入口，释放整行高度） ── */
.quick-btn {
  flex: 0 0 auto;
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--radius-full);
  background: var(--brand-bg);
  color: var(--brand-primary);
  cursor: pointer;
  transition: transform var(--duration-fast) var(--ease-out);
}
.quick-btn:not(:disabled):active {
  transform: scale(0.92);
}
.quick-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* ── 输入行 ── */
.input-row {
  display: flex;
  align-items: flex-end;
  gap: var(--space-2);
}

.target-chip {
  flex: 0 0 auto;
  max-width: 132px;
  min-height: 44px;
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 0 var(--space-2-5);
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
  background: var(--brand-bg);
  color: var(--brand-primary);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
  cursor: default;
}
.target-chip.switchable {
  cursor: pointer;
}
.target-chip:disabled {
  opacity: 0.6;
}
.target-chip:disabled.switchable {
  cursor: not-allowed;
}
.target-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.chip-icon {
  font-size: 16px;
}

.input {
  flex: 1 1 auto;
  min-width: 0;
  resize: none;
  /* 自适应 1-4 行：JS 撑高，4 行封顶（line-height 1.5 × 4 + 上下 padding） */
  max-height: calc(var(--text-base) * 1.5 * 4 + var(--space-2) * 2);
  overflow-y: auto;
  padding: var(--space-2) var(--space-2-5);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  font-size: var(--text-base);
  line-height: 1.5;
  font-family: inherit;
  background: var(--bg-subtle);
  color: var(--text-primary);
  outline: none;
  transition: border-color var(--duration-fast) var(--ease-out);
}
.input::placeholder {
  color: var(--text-muted);
}
.input:focus {
  border-color: var(--brand-primary);
  background: var(--bg-card);
}
.input:disabled {
  opacity: 0.6;
}

/* ── 语音 / 发送按钮：44px 热区，贴右下拇指区 ── */
.voice-btn,
.send-btn {
  flex: 0 0 auto;
  width: 44px;
  height: 44px;
  border: none;
  border-radius: var(--radius-full);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  font-size: 16px;
  transition: transform var(--duration-fast) var(--ease-out);
}
.voice-btn {
  background: var(--bg-subtle);
  color: var(--text-secondary);
}
.voice-btn.recording {
  background: var(--danger);
  color: var(--text-inverse);
  animation: pulse-voice 1s infinite;
}
@keyframes pulse-voice {
  0%,
  100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.7;
    transform: scale(1.05);
  }
}
.voice-btn:disabled,
.send-btn:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}
.voice-btn:not(:disabled):active,
.send-btn:not(:disabled):active {
  transform: scale(0.95);
}
.send-btn {
  background: var(--brand-primary);
  color: var(--text-inverse);
}
.send-btn:disabled {
  background: var(--bg-subtle);
  color: var(--text-muted);
}

.material-symbols-outlined {
  font-family: 'Material Symbols Outlined', 'Material Icons';
  font-weight: normal;
  font-style: normal;
  font-size: 20px;
  line-height: 1;
}

/* ── 目标切换面板列表（targets 模式）+ 快速指令面板列表 ── */
.target-list,
.quick-list {
  display: flex;
  flex-direction: column;
}
.quick-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  min-height: 48px; /* 面板行热区 > 44px */
  padding: var(--space-2) var(--space-2);
  background: transparent;
  border: none;
  border-bottom: 1px solid var(--border);
  color: var(--text-primary);
  font-size: var(--text-base);
  font-weight: var(--font-weight-medium);
  text-align: left;
  cursor: pointer;
}
.quick-item:last-child {
  border-bottom: none;
}
.quick-item:active {
  background: var(--bg-subtle);
}
.quick-item:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
/* "停下"：破坏性指令用既有 danger 语义色 */
.quick-item.danger {
  color: var(--danger);
}
.item-icon {
  flex: 0 0 auto;
  font-size: 22px;
  color: var(--text-secondary);
}
.quick-item.danger .item-icon {
  color: var(--danger);
}
.item-label {
  flex: 1 1 auto;
}
.target-option {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: 44px;
  padding: var(--space-2) var(--space-1);
  background: transparent;
  border: none;
  border-bottom: 1px solid var(--border);
  color: var(--text-primary);
  font-size: var(--text-base);
  text-align: left;
  cursor: pointer;
}
.target-option:last-child {
  border-bottom: none;
}
.target-option:active {
  background: var(--bg-subtle);
}
.target-option.active {
  color: var(--brand-primary);
  font-weight: var(--font-weight-semibold);
}
.option-icon {
  flex: 0 0 auto;
}
.option-label {
  flex: 1 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>

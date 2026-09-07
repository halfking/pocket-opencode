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
 * SessionComposer — 会话输入面板（契约 §4；2026-09-08 会话详情页改版）。
 *
 * 形态（改版）：页面默认不显示输入框，本组件渲染为右下 FAB 唤起的
 * 「会话操作 + 消息输入」浮动卡片（收起/展开与滚动隐藏由父级驱动，
 * 经 collapse 事件回收为 FAB）。
 *
 * 快捷指令（改版）：
 *   - PRIMARY_QUICK_COMMANDS（继续 / 提交代码并推送合并到主分支 /
 *     开启Goal模式 / 拉取最新代码）= 输入框底部工具行**最左侧**的
 *     44×44 方形图形按钮（经 UnifiedComposer 的 tools-left-prefix 插槽注入）；
 *   - 其余指令（停下/总结当前进展/跑测试/忽略错误继续）收进「更多指令」面板，
 *     面板行内文案完整可读；仅"停下"先二次确认（纪律不变）。
 *
 * 行为纪律（不变）：
 *   - voice/STT 转写只入草稿可编辑（追加，不直发）；
 *   - 草稿按会话存 SQLite（500ms 防抖），send 时清除；
 *   - targets 可切换模式仅契约就绪（chip 行按需渲染）。
 */
import { computed, ref, watch } from 'vue'
import { useConfirm } from '../../composables/useConfirm'
import { BottomSheet, UnifiedComposer } from '../../components'
import {
  PRIMARY_QUICK_COMMANDS,
  SECONDARY_QUICK_COMMANDS,
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
    liveRecording?: boolean
  }>(),
  {
    sessionLabel: '',
    disabled: false,
    initialText: '',
    liveRecording: false,
  },
)

const emit = defineEmits<{
  (e: 'send', text: string): void
  (e: 'update:target', id: string): void
  (e: 'live-record'): void
  (e: 'collapse'): void
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

/** 统一输入组件提交入口（契约不变：send(text)）。 */
function onComposerSubmit(payload: { text: string }): void {
  draftText.value = payload.text
  send()
}

// 快捷指令：primary 方形按钮一点即发；其余在「更多指令」面板
// （面板行文案完整可读；"停下"二次确认纪律不变）
const quickSheetVisible = ref(false)

async function onCommand(cmd: QuickCommand): Promise<void> {
  if (props.disabled) return
  // 仅"停下"先二次确认（统一走全局 ConfirmDialog）
  if (shouldConfirmCommand(cmd) && !(await confirm({ title: cmd.label, message: cmd.confirmText ?? '', confirmText: '确认', danger: true }))) return
  quickSheetVisible.value = false
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

// ── 确认弹窗（"停下"指令二次确认） ──
const { confirm } = useConfirm()
</script>

<template>
  <div class="composer-card" :class="{ disabled: props.disabled }">
    <!-- 收起把手：点按把输入面板收回为右下 FAB（热区经 ::after 扩展到 ~44px） -->
    <button
      type="button"
      class="dock-handle"
      aria-label="收起输入面板"
      @click="emit('collapse')"
    >
      <span class="material-symbols-outlined handle-icon" aria-hidden="true">keyboard_arrow_down</span>
    </button>

    <!-- targets 可切换模式（契约保留）：目标 chip 行（固定目标模式不渲染） -->
    <div v-if="hasTargets" class="ctx-row">
      <button
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
    </div>

    <!-- 统一输入：宽文本区（可全屏）+ 工具行。
         快捷指令经 tools-left-prefix 注入工具行最左侧（44×44 方形）。 -->
    <UnifiedComposer
      v-model="draftText"
      placeholder="输入消息…（Enter 发送，Shift+Enter 换行）"
      :enable="{ voice: true, image: false, camera: false, file: false, agent: false, optimize: true, liveRecord: true }"
      :submitting="props.disabled"
      :live-recording="props.liveRecording"
      submit-label="发送"
      @submit="onComposerSubmit"
      @live-record="emit('live-record')"
    >
      <template #tools-left-prefix>
        <div class="qc-strip" role="group" aria-label="会话快捷指令">
          <button
            v-for="cmd in PRIMARY_QUICK_COMMANDS"
            :key="cmd.label"
            type="button"
            class="qc-btn"
            :disabled="props.disabled"
            :aria-label="cmd.label"
            :title="cmd.label"
            @click="onCommand(cmd)"
          >
            <span class="material-symbols-outlined" aria-hidden="true">{{ cmd.icon ?? 'bolt' }}</span>
          </button>
          <button
            type="button"
            class="qc-btn qc-more"
            :disabled="props.disabled"
            aria-label="更多指令"
            title="更多指令"
            aria-haspopup="dialog"
            :aria-expanded="quickSheetVisible"
            @click="quickSheetVisible = true"
          >
            <span class="material-symbols-outlined" aria-hidden="true">bolt</span>
          </button>
        </div>
      </template>
    </UnifiedComposer>

    <!-- 更多指令面板（非 primary 收纳处；文案完整可读，"停下"保留二次确认） -->
    <BottomSheet v-model="quickSheetVisible" title="更多指令">
      <div class="quick-list" role="menu" aria-label="指令模板">
        <button
          v-for="cmd in SECONDARY_QUICK_COMMANDS"
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
/* ── 浮动卡片（父级 dock 负责外边距与滚动隐藏位移） ── */
.composer-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: 0 var(--space-2-5) var(--space-2);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-lg);
}
.composer-card.disabled {
  opacity: 0.85;
}

/* ── 收起把手（视觉 26px；::after 把热区纵向扩到 ~44px） ── */
.dock-handle {
  position: relative;
  flex: 0 0 auto;
  height: 26px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
}
.dock-handle::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  top: -9px;
  bottom: -9px;
}
.dock-handle:active {
  color: var(--text-secondary);
}
.handle-icon {
  font-size: 22px;
}

/* ── targets 模式目标 chip（契约保留；固定目标模式不渲染） ── */
.ctx-row {
  display: flex;
  align-items: center;
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
.target-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.chip-icon {
  font-size: 16px;
}

/* ── 快捷指令：工具行最左侧的 44×44 方形按钮条（横向可滚，mic/全屏不被挤走） ── */
.qc-strip {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  overflow-x: auto;
  scrollbar-width: none;
  padding-right: 2px;
}
.qc-strip::-webkit-scrollbar {
  display: none;
}
.qc-btn {
  flex: 0 0 auto;
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--border);
  border-radius: 12px; /* 方形圆角：与气泡语言区分的"工具"质感 */
  background: var(--bg-subtle);
  color: var(--text-secondary);
  cursor: pointer;
  transition:
    transform var(--duration-fast) var(--ease-out),
    background var(--duration-fast) var(--ease-out),
    color var(--duration-fast) var(--ease-out),
    border-color var(--duration-fast) var(--ease-out);
}
.qc-btn .material-symbols-outlined {
  font-size: 22px;
}
.qc-btn:not(:disabled):active {
  transform: scale(0.9);
  background: var(--brand-bg);
  color: var(--brand-primary);
  border-color: color-mix(in srgb, var(--brand-primary) 35%, transparent);
}
.qc-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.qc-more {
  background: var(--brand-bg);
  border-color: transparent;
  color: var(--brand-primary);
}

.material-symbols-outlined {
  font-family: 'Material Symbols Outlined', 'Material Icons';
  font-weight: normal;
  font-style: normal;
  font-size: 20px;
  line-height: 1;
}

/* ── 更多指令面板 / 目标切换面板列表 ── */
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

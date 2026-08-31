<!--
  UnifiedComposer — 全站统一输入组件（标准 / 全屏双模式）。

  布局标准（用户定稿）：
    ┌────────────────────────────────────┐
    │  宽大自适应 textarea（可编辑/可复制） │
    ├────────────────────────────────────┤
    │ ⛶ 🎙 🖼 📷 📎 │ [角色chip] [✨优化] [发送] │  ← 独立工具行
    └────────────────────────────────────┘

  - 标准模式：自适应增高（上限 40vh 后滚动）
  - 全屏模式：文章编辑式整页覆盖（大字号 + 字数统计 + 同一工具行）
  - 多模态：语音(STT) / 图片 / 拍照(@capacitor/camera) / 文件
  - 专家角色：内嵌 AgentSelectorSheet，选中经 update:agentId 上抛
  - AI 优化：经 llm-bff 流式润色草稿并回填，不自动提交
-->
<template>
  <div class="uc" :class="{ 'uc--single': singleLine }">
    <!-- 待发送图片缩略图条 -->
    <div v-if="attachments.length" class="uc-attach-strip">
      <div v-for="(a, i) in attachments" :key="i" class="uc-thumb">
        <img :src="a.dataUrl" :alt="a.name" />
        <button class="uc-thumb-del" type="button" :aria-label="`移除图片 ${i + 1}`" @click="attachment.remove(i)">×</button>
      </div>
    </div>

    <textarea
      :value="modelValue"
      class="uc-input"
      :class="{ 'uc-input--fs': false }"
      :rows="singleLine ? 1 : 3"
      :placeholder="placeholder"
      @input="onInput"
      @paste="onPaste"
      @keydown="onKeydown"
    ></textarea>

    <!-- 工具行：多模态 + 全屏 | 角色 + AI优化 + 提交（独立成行） -->
    <div class="uc-toolbar">
      <div class="uc-tools-left">
        <button
          v-if="allowFullscreen && !singleLine"
          class="uc-tool"
          type="button"
          aria-label="全屏编辑"
          @click="openFullscreen"
        >
          <span class="material-symbols-outlined" aria-hidden="true">open_in_full</span>
        </button>
        <button
          v-if="enable.voice"
          class="uc-tool"
          :class="{ 'uc-tool--rec': isRecording }"
          type="button"
          :aria-label="isRecording ? '结束录音' : '语音输入'"
          :disabled="isTranscribing"
          @click="onMic"
        >
          <span class="material-symbols-outlined" aria-hidden="true">{{ isRecording ? 'stop_circle' : 'mic' }}</span>
        </button>
        <button v-if="enable.image" class="uc-tool" type="button" aria-label="选择图片" @click="imageInput?.click()">
          <span class="material-symbols-outlined" aria-hidden="true">image</span>
        </button>
        <button v-if="enable.camera" class="uc-tool" type="button" aria-label="拍照" @click="onCamera">
          <span class="material-symbols-outlined" aria-hidden="true">photo_camera</span>
        </button>
        <button v-if="enable.file" class="uc-tool" type="button" aria-label="选择文件" @click="fileInput?.click()">
          <span class="material-symbols-outlined" aria-hidden="true">attach_file</span>
        </button>
        <span v-if="isTranscribing" class="uc-hint">转写中…</span>
      </div>

      <div class="uc-tools-right">
        <button
          v-if="enable.agent"
          class="uc-chip"
          :class="{ 'uc-chip--on': !!agent }"
          type="button"
          :aria-label="agent ? '切换角色' : '选择角色'"
          @click="agentSheetOpen = true"
        >
          <span class="uc-chip-emoji">{{ agent?.emoji || '👤' }}</span>
          <span class="uc-chip-label">{{ agent ? agent.name : '角色' }}</span>
        </button>
        <button
          v-if="enable.optimize"
          class="uc-opt"
          :class="{ 'uc-opt--working': isOptimizing }"
          type="button"
          :aria-label="isOptimizing ? '优化中' : 'AI 优化'"
          :disabled="!canOptimize || isOptimizing"
          @click="onOptimize"
        >
          <span class="material-symbols-outlined" aria-hidden="true">{{ isOptimizing ? 'hourglass_top' : 'auto_awesome' }}</span>
          <span class="uc-opt-label">{{ isOptimizing ? '优化中' : '优化' }}</span>
        </button>
        <slot name="submit">
          <button
            class="uc-submit"
            type="button"
            :aria-label="submitLabel"
            :disabled="!canSubmit"
            @click="onSubmit"
          >
            <span class="material-symbols-outlined" aria-hidden="true">send</span>
            <span v-if="!singleLine" class="uc-submit-label">{{ submitLabel }}</span>
          </button>
        </slot>
      </div>
    </div>

    <!-- 隐藏的 Web 文件选择（图片 / 通用文件） -->
    <input
      ref="imageInput"
      type="file"
      accept="image/*"
      multiple
      class="uc-file-hidden"
      @change="onPickImages"
    />
    <input ref="fileInput" type="file" multiple class="uc-file-hidden" @change="onPickFiles" />

    <!-- 角色选择：复用 ai-chat 的 AgentSelectorSheet -->
    <AgentSelectorSheet :show="agentSheetOpen" :current-agent-id="agentId" @update:show="agentSheetOpen = $event" @select="onSelectAgent" @clear="onClearAgent" />
  </div>

  <!-- 全屏"文章编辑"模式 -->
  <Teleport to="body">
    <div v-if="fullscreen" class="uc-fs" role="dialog" aria-modal="true" aria-label="全屏编辑">
      <header class="uc-fs-head">
        <span class="uc-fs-title">{{ placeholder || '编辑' }}</span>
        <span class="uc-fs-count">{{ charCount }} 字</span>
        <button class="uc-fs-collapse" type="button" aria-label="退出全屏" @click="closeFullscreen">
          <span class="material-symbols-outlined" aria-hidden="true">close_fullscreen</span>
        </button>
      </header>
      <textarea
        :value="modelValue"
        class="uc-fs-input"
        :placeholder="placeholder"
        @input="onInput"
        @paste="onPaste"
      ></textarea>
      <div class="uc-toolbar uc-fs-toolbar">
        <div class="uc-tools-left">
          <button
            v-if="enable.voice"
            class="uc-tool"
            :class="{ 'uc-tool--rec': isRecording }"
            type="button"
            :aria-label="isRecording ? '结束录音' : '语音输入'"
            :disabled="isTranscribing"
            @click="onMic"
          >
            <span class="material-symbols-outlined" aria-hidden="true">{{ isRecording ? 'stop_circle' : 'mic' }}</span>
          </button>
          <button v-if="enable.image" class="uc-tool" type="button" aria-label="选择图片" @click="imageInput?.click()">
            <span class="material-symbols-outlined" aria-hidden="true">image</span>
          </button>
          <button v-if="enable.camera" class="uc-tool" type="button" aria-label="拍照" @click="onCamera">
            <span class="material-symbols-outlined" aria-hidden="true">photo_camera</span>
          </button>
          <button v-if="enable.file" class="uc-tool" type="button" aria-label="选择文件" @click="fileInput?.click()">
            <span class="material-symbols-outlined" aria-hidden="true">attach_file</span>
          </button>
          <span v-if="isTranscribing" class="uc-hint">转写中…</span>
        </div>
        <div class="uc-tools-right">
          <button
            v-if="enable.agent"
            class="uc-chip"
            :class="{ 'uc-chip--on': !!agent }"
            type="button"
            @click="agentSheetOpen = true"
          >
            <span class="uc-chip-emoji">{{ agent?.emoji || '👤' }}</span>
            <span class="uc-chip-label">{{ agent ? agent.name : '角色' }}</span>
          </button>
          <button
            v-if="enable.optimize"
            class="uc-opt"
            :class="{ 'uc-opt--working': isOptimizing }"
            type="button"
            :disabled="!canOptimize || isOptimizing"
            @click="onOptimize"
          >
            <span class="material-symbols-outlined" aria-hidden="true">{{ isOptimizing ? 'hourglass_top' : 'auto_awesome' }}</span>
            <span class="uc-opt-label">{{ isOptimizing ? '优化中' : '优化' }}</span>
          </button>
          <slot name="submit">
            <button class="uc-submit" type="button" :disabled="!canSubmit" @click="onSubmit">
              <span class="material-symbols-outlined" aria-hidden="true">send</span>
              <span class="uc-submit-label">{{ submitLabel }}</span>
            </button>
          </slot>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, onBeforeUnmount, useSlots, nextTick } from 'vue'
import { useVoiceInput } from '../../composables/useVoiceInput'
import { useAttachments } from '../../composables/useAttachments'
import { useCameraCapture } from '../../composables/useCameraCapture'
import { usePromptOptimizer } from '../../composables/usePromptOptimizer'
import { useChatAgentStore } from '../../stores/chatAgentStore'
import { useBodyScrollLock } from '../../composables/useBodyScrollLock'
import AgentSelectorSheet from '../../features/ai-chat/AgentSelectorSheet.vue'

export interface UnifiedComposerEnable {
  voice?: boolean
  image?: boolean
  camera?: boolean
  file?: boolean
  agent?: boolean
  optimize?: boolean
}

const props = withDefaults(
  defineProps<{
    modelValue: string
    placeholder?: string
    /** 各能力开关（默认全开；场景按需关闭）。 */
    enable?: UnifiedComposerEnable
    /** 是否允许全屏编辑（多行正文默认允许）。 */
    allowFullscreen?: boolean
    /** 单行紧凑变体（标题类输入：无全屏、行高小、Enter 直接提交）。 */
    singleLine?: boolean
    /** Enter 直接提交（对话类 true；笔记正文 false）。 */
    submitOnEnter?: boolean
    agentId?: string
    submitLabel?: string
    /** 提交禁用的外部强制态（如流式生成中）。 */
    submitting?: boolean
  }>(),
  {
    placeholder: '',
    enable: () => ({}),
    allowFullscreen: true,
    singleLine: false,
    submitOnEnter: true,
    agentId: undefined,
    submitLabel: '发送',
    submitting: false,
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'update:agentId', value: string | undefined): void
  (e: 'submit', payload: { text: string; images: string[] }): void
  (e: 'optimized'): void
}>()

const slots = useSlots()

const enable = computed(() => {
  const e = props.enable ?? {}
  return {
    voice: e.voice ?? true,
    image: e.image ?? true,
    camera: e.camera ?? true,
    file: e.file ?? true,
    agent: e.agent ?? true,
    optimize: e.optimize ?? true,
  }
})

// ---- 多模态 ----
const attachment = useAttachments()
const attachments = attachment.attachments
const imageInput = ref<HTMLInputElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)

const { isRecording, isTranscribing, toggleRecording } = useVoiceInput()
const { pickImage } = useCameraCapture()
const { optimize: runOptimize, abort: abortOptimize, isOptimizing } = usePromptOptimizer()

// ---- 角色 ----
const agentStore = useChatAgentStore()
const agentSheetOpen = ref(false)
const agent = computed(() => (props.agentId ? agentStore.getAgent(props.agentId) : null))

function onSelectAgent(a: { id: string }) {
  emit('update:agentId', a.id)
}
function onClearAgent() {
  emit('update:agentId', undefined)
}

// ---- 文本 ----
const fullscreen = ref(false)
const scrollLock = useBodyScrollLock()
const charCount = computed(() => props.modelValue.length)
const canSubmit = computed(
  () => !props.submitting && (props.modelValue.trim().length > 0 || attachments.value.length > 0),
)
const canOptimize = computed(() => props.modelValue.trim().length > 0)

function onInput(e: Event) {
  emit('update:modelValue', (e.target as HTMLTextAreaElement).value)
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && props.submitOnEnter && !e.shiftKey) {
    e.preventDefault()
    onSubmit()
  }
}

/** 在光标处插入文本（语音转写 / 文件引用）。 */
function insertAtCursor(text: string) {
  const active = document.activeElement as HTMLTextAreaElement | null
  const els = Array.from(document.querySelectorAll<HTMLTextAreaElement>('.uc-input, .uc-fs-input'))
  const target = active && els.includes(active) ? active : els[0]
  if (!target) {
    emit('update:modelValue', props.modelValue + text)
    return
  }
  const start = target.selectionStart ?? props.modelValue.length
  const end = target.selectionEnd ?? props.modelValue.length
  const next = props.modelValue.slice(0, start) + text + props.modelValue.slice(end)
  emit('update:modelValue', next)
  nextTick(() => {
    target.focus()
    const pos = start + text.length
    target.setSelectionRange(pos, pos)
  })
}

async function onMic() {
  const text = await toggleRecording()
  if (text) insertAtCursor(text)
}

async function onCamera() {
  const shot = await pickImage('camera')
  if (shot) attachment.addDataUrl(shot.dataUrl, shot.name)
}

function onPickImages(e: Event) {
  const input = e.target as HTMLInputElement
  attachment.addFiles(Array.from(input.files ?? []))
  input.value = ''
}

/** 通用文件：MVP 以「引用」形式插入文本，由场景自行消费。 */
function onPickFiles(e: Event) {
  const input = e.target as HTMLInputElement
  const names = Array.from(input.files ?? []).map((f) => f.name)
  input.value = ''
  if (names.length) insertAtCursor(names.map((n) => `[📎 ${n}]`).join(' '))
}

function onPaste(e: ClipboardEvent) {
  if (enable.value.image && attachment.addFromClipboard(e)) {
    // 图片粘贴已处理；阻止把文件名文本一起贴入
    e.preventDefault()
  }
}

// ---- AI 优化（流式回填，不自动提交） ----
function onOptimize() {
  runOptimize(props.modelValue, {
    onDelta: (acc) => emit('update:modelValue', acc),
    onDone: () => emit('optimized'),
  })
}

// ---- 全屏 ----
function openFullscreen() {
  fullscreen.value = true
  scrollLock.acquire()
}
function closeFullscreen() {
  fullscreen.value = false
  scrollLock.release()
}

function onSubmit() {
  if (!canSubmit.value) return
  emit('submit', { text: props.modelValue, images: attachment.imageUrls() })
}

/** 供场景在发送成功后清理草稿与附件。 */
function reset() {
  emit('update:modelValue', '')
  attachment.clear()
}

defineExpose({ reset, insertAtCursor, openFullscreen, closeFullscreen, submit: onSubmit, canSubmit })

onBeforeUnmount(() => {
  abortOptimize()
  if (fullscreen.value) scrollLock.release()
})
</script>

<style scoped>
.uc {
  display: flex;
  flex-direction: column;
  gap: var(--space-2, 8px);
  width: 100%;
}

/* 宽大输入区：可编辑、可复制、自适应增高 */
.uc-input {
  width: 100%;
  min-height: 88px;
  max-height: 40vh;
  padding: var(--space-3, 12px) var(--space-4, 16px);
  font-size: 16px;
  line-height: 1.6;
  font-family: inherit;
  color: var(--color-text-primary);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg, 12px);
  resize: vertical;
  overflow-y: auto;
  -webkit-user-select: text;
  user-select: text;
}
.uc-input:focus {
  outline: none;
  border-color: var(--color-primary, #4f6ef7);
}
.uc--single .uc-input {
  min-height: 44px;
  max-height: 120px;
  padding: var(--space-2, 8px) var(--space-3, 12px);
  resize: none;
}

/* 附件缩略图条 */
.uc-attach-strip {
  display: flex;
  gap: var(--space-2, 8px);
  overflow-x: auto;
  padding-bottom: 2px;
}
.uc-thumb {
  position: relative;
  flex: 0 0 auto;
  width: 56px;
  height: 56px;
  border-radius: var(--radius-md, 8px);
  overflow: hidden;
  border: 1px solid var(--color-border);
}
.uc-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.uc-thumb-del {
  position: absolute;
  top: 0;
  right: 0;
  width: 20px;
  height: 20px;
  border: none;
  border-radius: 0 0 0 var(--radius-md, 8px);
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  font-size: 14px;
  line-height: 20px;
  cursor: pointer;
}

/* 独立工具行 */
.uc-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2, 8px);
  min-height: 44px;
}
.uc-tools-left {
  display: flex;
  align-items: center;
  gap: var(--space-1, 4px);
  overflow-x: auto;
  scrollbar-width: none;
}
.uc-tools-left::-webkit-scrollbar { display: none; }
.uc-tools-right {
  display: flex;
  align-items: center;
  gap: var(--space-2, 8px);
  flex: 0 0 auto;
}

.uc-tool {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--radius-full, 999px);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: background var(--duration-fast, 0.15s) ease-out;
}
.uc-tool:active { background: var(--color-bg-hover); }
.uc-tool:disabled { opacity: 0.45; cursor: default; }
.uc-tool--rec {
  background: rgba(239, 68, 68, 0.12);
  color: #ef4444;
  animation: uc-pulse 1.2s ease-in-out infinite;
}
@keyframes uc-pulse {
  0%, 100% { box-shadow: 0 0 0 0 rgba(239, 68, 68, 0.35); }
  50% { box-shadow: 0 0 0 8px rgba(239, 68, 68, 0); }
}
.uc-tool .material-symbols-outlined { font-size: 22px; }

.uc-hint {
  font-size: 12px;
  color: var(--color-text-tertiary);
  white-space: nowrap;
}

/* 角色chip */
.uc-chip {
  display: flex;
  align-items: center;
  gap: 4px;
  max-width: 160px;
  height: 36px;
  padding: 0 var(--space-3, 12px);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-full, 999px);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  font-size: 13px;
  cursor: pointer;
}
.uc-chip--on {
  border-color: var(--color-primary, #4f6ef7);
  color: var(--color-primary, #4f6ef7);
}
.uc-chip-emoji { font-size: 15px; }
.uc-chip-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* AI 优化按钮 */
.uc-opt {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 36px;
  padding: 0 var(--space-3, 12px);
  border: none;
  border-radius: var(--radius-full, 999px);
  background: rgba(124, 108, 245, 0.12);
  color: #7c6cf5;
  font-size: 13px;
  cursor: pointer;
}
.uc-opt:disabled { opacity: 0.45; cursor: default; }
.uc-opt--working { animation: uc-pulse 1.2s ease-in-out infinite; }
.uc-opt .material-symbols-outlined { font-size: 18px; }

/* 提交按钮 */
.uc-submit {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 40px;
  padding: 0 var(--space-4, 16px);
  border: none;
  border-radius: var(--radius-full, 999px);
  background: var(--color-primary, #4f6ef7);
  color: #fff;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
}
.uc-submit:disabled { opacity: 0.45; cursor: default; }
.uc-submit .material-symbols-outlined { font-size: 18px; }
.uc--single .uc-submit { width: 40px; padding: 0; justify-content: center; }

.uc-file-hidden { display: none; }

/* 全屏“文章编辑”模式 */
.uc-fs {
  position: fixed;
  inset: 0;
  z-index: var(--z-sheet, 1000);
  display: flex;
  flex-direction: column;
  background: var(--color-bg-surface);
}
.uc-fs-head {
  display: flex;
  align-items: center;
  gap: var(--space-3, 12px);
  padding: var(--space-3, 12px) var(--space-4, 16px);
  padding-top: calc(var(--space-3, 12px) + env(safe-area-inset-top, 0px));
  border-bottom: 1px solid var(--color-border);
  flex: 0 0 auto;
}
.uc-fs-title {
  flex: 1;
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.uc-fs-count {
  font-size: 12px;
  color: var(--color-text-tertiary);
}
.uc-fs-collapse {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--radius-md, 8px);
  background: none;
  color: var(--color-text-secondary);
  cursor: pointer;
}
.uc-fs-input {
  flex: 1 1 auto;
  width: 100%;
  padding: var(--space-4, 16px);
  font-size: 17px;
  line-height: 1.8;
  font-family: inherit;
  color: var(--color-text-primary);
  background: transparent;
  border: none;
  outline: none;
  resize: none;
  -webkit-user-select: text;
  user-select: text;
}
.uc-fs-toolbar {
  flex: 0 0 auto;
  padding: var(--space-2, 8px) var(--space-3, 12px);
  padding-bottom: calc(var(--space-2, 8px) + env(safe-area-inset-bottom, 0px));
  border-top: 1px solid var(--color-border);
}

@media (prefers-reduced-motion: reduce) {
  .uc-tool--rec, .uc-opt--working { animation: none; }
}
</style>

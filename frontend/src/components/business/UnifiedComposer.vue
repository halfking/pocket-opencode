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
  - 专家角色：内嵌 AgentSelectorSheet，选中经 update:agentId 上抛；
    未选角色时 chip 显示专家人员图标 support_agent（2026-09-05 起，
    替代原 👤 emoji；已选角色仍展示该角色自己的 emoji 头像）
  - AI 优化：经 llm-bff 流式润色草稿并回填，不自动提交
-->
<template>
  <div ref="ucEl" class="uc" :class="{ 'uc--single': singleLine }">
    <!-- 待发送图片缩略图条 -->
    <div v-if="attachments.length" class="uc-attach-strip">
      <div v-for="(a, i) in attachments" :key="i" class="uc-thumb">
        <img :src="a.dataUrl" :alt="a.name" />
        <button class="uc-thumb-del" type="button" :aria-label="`移除图片 ${i + 1}`" @click="attachment.remove(i)">×</button>
      </div>
    </div>

    <textarea
      ref="inputEl"
      :value="modelValue"
      class="uc-input"
      :class="{ 'uc-input--fs': false }"
      :rows="singleLine ? 1 : 3"
      :placeholder="placeholder"
      @input="onInput"
      @paste="onPaste"
      @keydown="onKeydown"
    ></textarea>

    <!-- 工具行：多模态 + 全屏 | 角色 + AI优化 + 提交（独立成行）。
         tools-left-prefix：场景注入的常驻按钮（如会话快捷指令），置于工具行最左 -->
    <div class="uc-toolbar">
      <div class="uc-tools-left">
        <slot name="tools-left-prefix" />
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
          :class="{ 'uc-tool--rec': recActive }"
          type="button"
          :aria-label="micAria"
          :disabled="isTranscribing"
          @click="onMic"
          @pointerdown="onMicDown"
          @pointerup="onMicUp"
          @pointercancel="onMicCancel"
        >
          <span class="material-symbols-outlined" aria-hidden="true">{{ recActive ? 'stop_circle' : 'mic' }}</span>
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
          <span v-if="sttError" class="uc-hint uc-hint--err">{{ sttError }}</span>
          <span v-if="optimizeRetryHint" class="uc-hint">{{ optimizeRetryHint }}</span>
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
          <span v-if="agent" class="uc-chip-emoji" aria-hidden="true">{{ agent.emoji }}</span>
          <span v-else class="material-symbols-outlined uc-chip-icon" aria-hidden="true">support_agent</span>
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
          <slot name="tools-left-prefix" />
          <button
            v-if="enable.voice"
            class="uc-tool"
            :class="{ 'uc-tool--rec': recActive }"
            type="button"
            :aria-label="micAria"
            :disabled="isTranscribing"
            @click="onMic"
            @pointerdown="onMicDown"
            @pointerup="onMicUp"
            @pointercancel="onMicCancel"
          >
            <span class="material-symbols-outlined" aria-hidden="true">{{ recActive ? 'stop_circle' : 'mic' }}</span>
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
          <span v-if="sttError" class="uc-hint uc-hint--err">{{ sttError }}</span>
          <span v-if="optimizeRetryHint" class="uc-hint">{{ optimizeRetryHint }}</span>
        </div>
        <div class="uc-tools-right">
          <button
            v-if="enable.agent"
            class="uc-chip"
            :class="{ 'uc-chip--on': !!agent }"
            type="button"
            @click="agentSheetOpen = true"
          >
            <span v-if="agent" class="uc-chip-emoji" aria-hidden="true">{{ agent.emoji }}</span>
            <span v-else class="material-symbols-outlined uc-chip-icon" aria-hidden="true">support_agent</span>
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
import { ref, computed, onBeforeUnmount, onMounted, useSlots, nextTick } from 'vue'
import { useAutoGrowTextarea } from '../../composables/useAutoGrowTextarea'
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
  /** 点按开始会话实时录音；长按仍走语音输入草稿。 */
  liveRecord?: boolean
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
    /** 会话实时录音进行中（红点，与短语音输入互斥展示）。 */
    liveRecording?: boolean
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
    liveRecording: false,
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'update:agentId', value: string | undefined): void
  (e: 'submit', payload: { text: string; images: string[] }): void
  (e: 'optimized'): void
  (e: 'live-record'): void
}>()

const slots = useSlots()

/* 自适应增高：实现文件头「标准模式：自适应增高，上限 40vh 后滚动」承诺。
 * 机制与三类调用点（手动输入 / 程序化改值 / 首帧）都在
 * composables/useAutoGrowTextarea.ts，那里有可跑的用例守着；
 * 上限数字留在 CSS（标准 40vh / 紧凑 30vh），JS 不复制一份。 */
const inputEl = ref<HTMLTextAreaElement | null>(null)
const { onInput: autoGrowOnInput } = useAutoGrowTextarea(() => props.modelValue, inputEl)

/* 把「自己占据的整条底部带」发布成 --composer-inset，供 Toast 避让。
 *
 * 为什么需要：Toast 的 bottom 只认 --bottom-chrome-height（底部 tabbar），
 * 而本组件是**停靠在 tabbar 之上**的输入区，比 tabbar 高得多。实测
 * /ai-chat 上错误 toast 正好压住整条工具行（全屏/麦克风/相机/附件/角色/
 * 优化）——而麦克风是这个 App 的主打能力之一，被一条 3 秒的提示盖住不该
 * 算可接受。
 *
 * 为什么在 JS 里量而不是纯 CSS：输入框是**内容驱动高度**的（autoGrow），
 * 高度随内容变，CSS 侧无从得知当前是多少。这里用 ResizeObserver 跟着
 * autoGrow 的每次高度变化重新发布，零布局抖动。
 *
 * 为什么发布的是「带」而不是「自身高度」（2026-10-03 设备实测打回过一次）：
 * 调用方的 .composer 包裹层有 padding 8px 12px + padding-bottom
 * calc(8px + safe) + 1px 上边框，而 .uc 自身不含这些。发自身高度的话
 * toast 底边落在 255px，输入区顶边实际在 290px，仍压住工具行 35px。
 * 改成量「#app 底边 → .uc 顶边」这条整条带（含 tabbar）后是 281px，
 * toast 落在 295px，比输入区顶边还高 5px。
 *
 * 为什么以 #app 底边为基准而不是 innerHeight：#app 高度是
 * calc(100% - var(--kb-inset))，键盘弹起时它的底边与输入区同步上移，
 * 两者相减与键盘无关。于是这里量到的是「不含键盘的带高」，键盘那一份
 * 交给 toast 自己加 --kb-inset，不会重复计算。
 *
 * 没有本组件的页面读不到这个变量，var() 回落到 0px，toast 行为不变。
 */
const ucEl = ref<HTMLElement | null>(null)
let ucObserver: ResizeObserver | null = null
onMounted(() => {
  const el = ucEl.value
  if (!el) return
  const publish = () => {
    const root = document.getElementById('app')
    if (!root) return
    const band = root.getBoundingClientRect().bottom - el.getBoundingClientRect().top
    document.documentElement.style.setProperty(
      '--composer-inset',
      `${Math.max(0, Math.round(band))}px`,
    )
  }
  publish()
  if (typeof ResizeObserver !== 'undefined') {
    ucObserver = new ResizeObserver(publish)
    ucObserver.observe(el)
  }
  // ResizeObserver 只在「盒子尺寸」变化时回调，不含位移。唯一会让本组件
  // 在尺寸不变的情况下下移的场景是 AppLayout 滚动联动隐藏 tabbar
  // （.composer 上的 transform/margin 变化），此时发布值偏大，toast 会
  // 站得比需要的位置更高——偏高的方向是安全方向（宁可空一段也不压控件），
  // 且 tabbar 归位后几何自动回到已发布的值，故不额外挂滚动监听。
})
onBeforeUnmount(() => {
  ucObserver?.disconnect()
  ucObserver = null
  // 离开带输入区的页面后必须清零，否则 toast 会被一个已不存在的元素顶高。
  document.documentElement.style.setProperty('--composer-inset', '0px')
})

function onInput(e: Event) {
  autoGrowOnInput(e)
  emit('update:modelValue', (e.target as HTMLTextAreaElement).value)
}

const enable = computed(() => {
  const e = props.enable ?? {}
  return {
    voice: e.voice ?? true,
    image: e.image ?? true,
    camera: e.camera ?? true,
    file: e.file ?? true,
    agent: e.agent ?? true,
    optimize: e.optimize ?? true,
    liveRecord: e.liveRecord ?? false,
  }
})

// ---- 多模态 ----
const attachment = useAttachments()
const attachments = attachment.attachments
const imageInput = ref<HTMLInputElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)

const { isRecording, isTranscribing, sttError, startRecording, stopRecording, toggleRecording } = useVoiceInput()
const recActive = computed(() => isRecording.value || props.liveRecording)
const micAria = computed(() => {
  if (props.liveRecording) return '结束实时录音'
  if (isRecording.value) return '结束语音输入'
  if (enable.value.liveRecord) return '点按开始实时录音，长按语音输入'
  return '语音输入'
})

let pressTimer: ReturnType<typeof setTimeout> | null = null
let didLongPress = false

function onMicCancel() {
  if (pressTimer) { clearTimeout(pressTimer); pressTimer = null }
}

function onMicDown() {
  if (!enable.value.liveRecord || props.liveRecording || isRecording.value) return
  didLongPress = false
  pressTimer = setTimeout(() => {
    didLongPress = true
    void startRecording()
  }, 400)
}

async function onMicUp() {
  if (!enable.value.liveRecord) return
  onMicCancel()
  if (didLongPress || isRecording.value) {
    const text = await stopRecording()
    if (text) insertAtCursor(text)
    didLongPress = false
    return
  }
  emit('live-record')
}

async function onMic() {
  if (enable.value.liveRecord) return
  const text = await toggleRecording()
  if (text) insertAtCursor(text)
}
const { pickImage } = useCameraCapture()
const { optimize: runOptimize, isOptimizing, optimizeRetryHint } = usePromptOptimizer()

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

function onKeydown(e: KeyboardEvent) {
  // e.repeat：长按/输入法（如 Gboard 语音听写收尾）合成的重复 Enter 只应提交
  // 一次——第二发会被发送侧 isStreaming/canSubmit 挡住，但直接过滤更明确
  // （runbook §16.6-1「气泡入列表但流未发出」的一次性观察，防御双通道竞态）。
  if (e.repeat) return
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

defineExpose({ reset, insertAtCursor, openFullscreen, closeFullscreen, submit: onSubmit, canSubmit, attachments })

onBeforeUnmount(() => {
  // M1 契约(usePromptOptimizer 头注释):组件 unmount 不 abort 优化流 ——
  // 流所有权归 aiStreamRuntime,自然跑完;取消只属于用户显式操作。
  if (pressTimer) clearTimeout(pressTimer)
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
  font-size: var(--text-lg);
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
  /* 紧凑变体也按内容长高，但留更浅的上限——标题类输入正常 1-2 行，
     真写成多行时仍要让用户看得见（原先 120px ≈ 5 行就封顶并转内部滚动，
     「内容看不全」的高发点）。超限后由 overflow-y 接管滚动。 */
  max-height: 30vh;
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
  color: var(--text-inverse);
  font-size: var(--text-base);
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
  background: var(--danger-bg);
  color: var(--danger);
  animation: uc-pulse 1.2s ease-in-out infinite;
}
@keyframes uc-pulse {
  0%, 100% { box-shadow: 0 0 0 0 rgba(239, 68, 68, 0.35); }
  50% { box-shadow: 0 0 0 8px rgba(239, 68, 68, 0); }
}
.uc-tool .material-symbols-outlined { font-size: 22px; }

.uc-hint {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  white-space: nowrap;
}
.uc-hint--err { color: var(--danger, #ef4444); }

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
  font-size: var(--text-smd);
  cursor: pointer;
}
.uc-chip--on {
  border-color: var(--color-primary, #4f6ef7);
  color: var(--color-primary, #4f6ef7);
}
.uc-chip-emoji { font-size: var(--text-md); }
/* 未选角色时的专家人员图标（与全 App Material Symbols 图标语言统一） */
.uc-chip-icon { font-size: 17px; flex-shrink: 0; }
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
  background: var(--brand-bg);
  color: var(--brand-primary);
  font-size: var(--text-smd);
  cursor: pointer;
}
.uc-opt:disabled { opacity: 0.45; cursor: default; }
.uc-opt--working { animation: uc-pulse 1.2s ease-in-out infinite; }
.uc-opt .material-symbols-outlined { font-size: var(--text-xl); }

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
  color: var(--text-inverse);
  font-size: var(--text-base);
  font-weight: 600;
  cursor: pointer;
}
.uc-submit:disabled { opacity: 0.45; cursor: default; }
.uc-submit .material-symbols-outlined { font-size: var(--text-xl); }
.uc--single .uc-submit { width: 40px; padding: 0; justify-content: center; }

.uc-file-hidden { display: none; }

/* 全屏“文章编辑”模式 */
.uc-fs {
  position: fixed;
  /* 底边随 --kb-inset 抬升：键盘弹起时整页编辑器贴住键盘上沿（input 工具行可见） */
  inset: 0 0 var(--kb-inset, 0px) 0;
  z-index: var(--z-sheet);
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
  font-size: var(--text-md);
  font-weight: 600;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.uc-fs-count {
  font-size: var(--text-sm);
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

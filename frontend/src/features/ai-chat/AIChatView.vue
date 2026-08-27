<!--
  AIChatView — 豆包式 AI 对话。
  - 多轮会话（本地存储）、模型选择、流式输出、参数调节
  - 「对比模式」：同一问题并行发给多个模型，分栏比较
  - 每条回答可「用另一模型检查 / 优化」
  路由：/ai-chat
-->
<template>
  <div class="ai-chat">
    <!-- 顶部栏 -->
    <header class="top-bar">
      <button class="icon-btn" :aria-label="drawerOpen ? '关闭会话列表' : '打开会话列表'" @click="toggleDrawer">
        <span class="material-symbols-outlined">menu</span>
      </button>
      <div class="top-title">
        <span class="title-text">对话</span>
      </div>
      <!-- 右侧控制组 -->
      <div class="top-controls">
        <!-- 会话选择 -->
        <button class="ctrl-btn" aria-label="选择会话" @click="toggleDrawer">
          <span class="material-symbols-outlined">chat_bubble</span>
          <span class="ctrl-label">{{ active?.title || '新对话' }}</span>
        </button>
        <!-- 角色选择 -->
        <button class="ctrl-btn" aria-label="选择角色" @click="openAgentSheet">
          <span class="material-symbols-outlined">{{
            currentAgent ? 'person' : 'psychology'
          }}</span>
          <span class="ctrl-label">{{
            currentAgent ? currentAgent.name : '选择角色'
          }}</span>
        </button>
        <!-- 对比模式 -->
        <button class="icon-btn" :class="{ active: compareMode }" aria-label="切换对比模式" @click="onToggleCompare">
          <span class="material-symbols-outlined">balance</span>
        </button>
        <!-- 参数设置 -->
        <button class="icon-btn" aria-label="参数设置" @click="settingsOpen = true">
          <span class="material-symbols-outlined">tune</span>
        </button>
        <!-- 账户设置 -->
        <button class="icon-btn" aria-label="设置" @click="$router.push('/settings')">
          <span class="material-symbols-outlined">settings</span>
        </button>
      </div>
    </header>

    <!-- 对比模式选中的模型条 -->
    <div v-if="compareMode" class="compare-strip">
      <span class="cs-label">对比：</span>
      <span v-for="m in compareModels" :key="m" class="cs-chip">{{ m }}</span>
      <button class="cs-edit" @click="modelSheetOpen = true">编辑</button>
    </div>

    <!-- 消息区 -->
    <main ref="scrollEl" class="msg-area">
      <div v-if="turns.length === 0" class="empty">
        <div class="empty-emoji">💬</div>
        <p class="empty-title">开始你的 AI 对话</p>
        <p class="empty-sub">支持多轮会话、模型对比与跨模型优化。先从下方输入一个问题吧。</p>
        <div class="empty-suggestions">
          <button v-for="q in suggestions" :key="q" class="sug" @click="onSend(q)">{{ q }}</button>
        </div>
      </div>

      <template v-for="(turn, ti) in turns" :key="ti">
        <!-- 用户气泡（可含图片附件） -->
        <div class="row user">
          <div class="bubble user-bubble">
            <div v-if="turn.user.images?.length" class="bubble-images">
              <img v-for="(img, ii) in turn.user.images" :key="ii" :src="img" class="bubble-img" alt="附件图片" />
            </div>
            {{ turn.user.content }}
          </div>
        </div>
        <!-- 助手回答：单条 or 多模型对比 -->
        <div v-if="turn.answers.length > 1" class="compare-grid">
          <article
            v-for="a in turn.answers"
            :key="a.id"
            class="compare-card"
          >
            <header class="cc-head">
              <span class="cc-model">{{ a.model || '模型' }}</span>
              <span v-if="a.streaming" class="cc-live">生成中…</span>
            </header>
            <div class="bubble ai-bubble" v-html="rendered(a)"></div>
            <footer v-if="a.usage" class="usage">≈ {{ a.usage.total_tokens }} tokens</footer>
            <div v-if="a.error" class="msg-error">{{ a.error }}</div>
            <div class="msg-actions">
              <button class="act" @click="copy(a)">复制</button>
              <button class="act" @click="openOptimize(a)">优化</button>
              <button class="act" @click="regenerate(a.id)">重生成</button>
            </div>
          </article>
        </div>
        <div v-else-if="turn.answers.length === 1" class="row ai">
          <div class="bubble ai-bubble" v-html="rendered(turn.answers[0])"></div>
          <div v-if="turn.answers[0].usage" class="usage-row">≈ {{ turn.answers[0].usage?.total_tokens }} tokens</div>
          <div v-if="turn.answers[0].error" class="msg-error">{{ turn.answers[0].error }}</div>
          <div class="msg-actions">
            <button class="act" @click="copy(turn.answers[0])">复制</button>
            <button class="act" @click="openOptimize(turn.answers[0])">优化</button>
            <button class="act" @click="regenerate(turn.answers[0].id)">重生成</button>
          </div>
        </div>
      </template>

      <div v-if="isStreaming" class="typing">
        <span class="dot"></span><span class="dot"></span><span class="dot"></span>
      </div>
    </main>

    <!-- 输入区 -->
    <footer class="composer">
      <!-- 待发送图片缩略图 -->
      <div v-if="pendingImages.length" class="attach-strip">
        <div v-for="(img, i) in pendingImages" :key="i" class="attach-thumb">
          <img :src="img" alt="待发送图片" />
          <button class="attach-del" :aria-label="`移除图片 ${i + 1}`" @click="removeImage(i)">×</button>
        </div>
        <div v-if="pendingImages.length" class="attach-hint">将使用视觉模型：{{ visionModelLabel }}</div>
      </div>
      <div class="composer-row">
        <button class="icon-btn attach-btn" aria-label="添加图片" @click="fileInput?.click()">
          <span class="material-symbols-outlined">image</span>
        </button>
        <input
          ref="fileInput"
          type="file"
          accept="image/*"
          multiple
          class="file-input"
          @change="onPickImages"
        />
        <button
          class="icon-btn attach-btn"
          :class="{ recording: isRecording }"
          :aria-label="isRecording ? '结束录音' : '语音输入'"
          :disabled="isTranscribing"
          @click="onMic"
        >
          <span class="material-symbols-outlined">{{ isRecording ? 'stop_circle' : 'mic' }}</span>
        </button>
        <textarea
          ref="inputEl"
          v-model="draft"
          class="composer-input"
          rows="1"
          :placeholder="composerPlaceholder"
          @input="autoGrow"
          @keydown.enter.exact.prevent="onSend()"
        ></textarea>
        <button
          v-if="isStreaming"
          class="send-btn stop"
          aria-label="停止生成"
          @click="stop"
        >
          <span class="material-symbols-outlined">stop</span>
        </button>
        <button
          v-else
          class="send-btn"
          :disabled="!canSend"
          aria-label="发送"
          @click="onSend()"
        >
          <span class="material-symbols-outlined">send</span>
        </button>
      </div>
    </footer>

    <!-- 会话抽屉 -->
    <div v-if="drawerOpen" class="drawer-mask" @click.self="drawerOpen = false">
      <aside class="drawer">
        <div class="drawer-head">
          <span>会话</span>
          <button class="icon-btn" aria-label="新建对话" @click="newConversation">
            <span class="material-symbols-outlined">add</span>
          </button>
        </div>
        <div class="conv-list">
          <div
            v-for="c in conversations"
            :key="c.id"
            class="conv-item"
            :class="{ active: c.id === activeId }"
            @click="selectConversation(c.id)"
          >
            <div class="conv-main">
              <div class="conv-title">{{ c.title }}</div>
              <div class="conv-meta">{{ c.mode === 'compare' ? '对比' : (c.model || '—') }} · {{ formatTime(c.updatedAt) }}</div>
            </div>
            <button class="conv-del" aria-label="删除会话" @click.stop="confirmDelete(c)">
              <span class="material-symbols-outlined">delete</span>
            </button>
          </div>
          <div v-if="conversations.length === 0" class="conv-empty">还没有会话</div>
        </div>
      </aside>
    </div>

    <!-- 模型选择 / 对比选择 sheet -->
    <div v-if="modelSheetOpen" class="sheet-mask" @click.self="modelSheetOpen = false">
      <div class="sheet">
        <div class="sheet-head">
          <span>{{ compareMode ? '选择对比模型（可多选）' : '选择模型' }}</span>
          <button class="icon-btn" aria-label="关闭" @click="modelSheetOpen = false">
            <span class="material-symbols-outlined">close</span>
          </button>
        </div>
        <div v-if="modelsLoading" class="sheet-state">模型加载中…</div>
        <div v-else-if="models.length === 0" class="sheet-state">
          未获取到模型，请先在「设置 → AI 网关」配置网关密钥。
          <button class="link-btn" @click="retryModels">重试</button>
        </div>
        <div v-else class="model-list">
          <!-- auto：交给网关智能路由（结合下方"模态默认模型"与网关任务识别） -->
          <label v-if="!compareMode" class="model-item" :class="{ checked: tempSelection.includes(AUTO) }">
            <input
              type="checkbox"
              class="model-check"
              :checked="tempSelection.includes(AUTO)"
              @change="onModelCheck(AUTO, $event)"
            />
            <span class="model-name">auto · 智能路由</span>
            <span class="model-current">{{ autoHint }}</span>
          </label>
          <label
            v-for="m in models"
            :key="m"
            class="model-item"
            :class="{ checked: isModelChecked(m) }"
          >
            <input
              type="checkbox"
              class="model-check"
              :checked="isModelChecked(m)"
              @change="onModelCheck(m, $event)"
            />
            <span class="model-name">{{ m }}</span>
            <span class="modality-badge" :data-mod="modalityOf(m)">{{ modalityOf(m) }}</span>
            <span v-if="!compareMode && active && active.model === m" class="model-current">当前</span>
          </label>
        </div>
        <button class="sheet-confirm" @click="applyModelSelection">确定</button>
      </div>
    </div>

    <!-- 优化：选择模型 sheet -->
    <div v-if="optimizeOpen" class="sheet-mask" @click.self="optimizeOpen = false">
      <div class="sheet">
        <div class="sheet-head">
          <span>用哪个模型检查并优化？</span>
          <button class="icon-btn" aria-label="关闭" @click="optimizeOpen = false">
            <span class="material-symbols-outlined">close</span>
          </button>
        </div>
        <div v-if="models.length === 0" class="sheet-state">暂无可用模型</div>
        <div v-else class="model-list">
          <button
            v-for="m in models"
            :key="m"
            class="model-item plain"
            @click="doOptimize(m)"
          >
            <span class="model-name">{{ m }}</span>
          </button>
        </div>
      </div>
    </div>

    <!-- 参数设置 sheet -->
    <div v-if="settingsOpen" class="sheet-mask" @click.self="settingsOpen = false">
      <div class="sheet">
        <div class="sheet-head">
          <span>对话参数</span>
          <button class="icon-btn" aria-label="关闭" @click="settingsOpen = false">
            <span class="material-symbols-outlined">close</span>
          </button>
        </div>

        <div class="field">
          <div class="field-label">温度（创造性）· {{ settings.temperature.toFixed(1) }}</div>
          <input
            type="range"
            min="0"
            max="2"
            step="0.1"
            v-model.number="settings.temperature"
            @change="saveSettings"
          />
          <div class="field-hint">越低越稳定严谨，越高越发散有创意。</div>
        </div>

        <div class="field">
          <div class="field-label">最大输出 Token · {{ settings.maxTokens }}</div>
          <input
            type="number"
            min="256"
            max="8192"
            step="256"
            v-model.number="settings.maxTokens"
            class="num-input"
            @change="saveSettings"
          />
        </div>

        <!-- 当前角色 -->
        <div class="field">
          <div class="field-label">当前角色</div>
          <div v-if="currentAgent" class="agent-card">
            <div class="agent-card-header">
              <span class="agent-emoji">{{ currentAgent.emoji || '🤖' }}</span>
              <div class="agent-card-info">
                <div class="agent-card-name">{{ currentAgent.name }}</div>
                <div class="agent-card-desc">{{ currentAgent.description }}</div>
              </div>
            </div>
            <button class="agent-change-btn" @click="openAgentSheet">切换角色</button>
          </div>
          <div v-else class="no-agent">
            <p>未选择角色，将使用下方的全局系统提示词</p>
            <button class="agent-select-btn" @click="openAgentSheet">选择角色</button>
          </div>
          <button class="library-link" @click="goToLibrary">浏览完整角色库 →</button>
        </div>

        <div class="field">
          <div class="field-label">系统提示词（无角色时兜底）</div>
          <textarea
            v-model="settings.systemPrompt"
            class="sys-input"
            rows="3"
            placeholder="例如：你是一个严谨的中文助手，回答需给出依据。"
            @change="saveSettings"
          ></textarea>
        </div>

        <div class="field">
          <div class="field-label">默认模型</div>
          <select v-model="settings.defaultModel" class="sel-input" @change="saveSettings">
            <option value="auto">auto · 智能路由</option>
            <option v-for="m in models" :key="m" :value="m">{{ m }}</option>
            <option v-if="models.length === 0" value="">（未加载）</option>
          </select>
        </div>

        <!-- 按模态默认模型：会话模型为 auto 时，按消息模态选用；
             每个模态也可再选 auto（网关智能路由 + 任务识别兜底） -->
        <div class="field">
          <div class="field-label">模态默认模型（会话模型为 auto 时生效）</div>
          <div v-for="mk in MODALITY_KEYS" :key="mk" class="modality-row">
            <span class="modality-name">{{ MODALITY_LABELS[mk] }}</span>
            <select
              :value="settings.modelByModality[mk]"
              class="sel-input modality-sel"
              @change="onModalityDefault(mk, $event)"
            >
              <option value="auto">auto · 智能路由</option>
              <option v-for="m in modelsForModality(mk)" :key="m" :value="m">{{ m }}</option>
              <option v-for="m in otherModels(mk)" :key="'o-' + m" :value="m">{{ m }}（其他模态）</option>
            </select>
          </div>
          <div class="field-hint">
            模态目录来自网关（未配置网关节点时按命名推断）。文本=普通对话，图像=发图时自动切换的视觉模型。
          </div>
        </div>

        <button class="sheet-confirm" @click="settingsOpen = false">完成</button>
      </div>
    </div>
  </div>

  <!-- 角色选择器 -->
  <AgentSelectorSheet
    :show="agentSheetOpen"
    :current-agent-id="active?.agentId"
    @update:show="agentSheetOpen = $event"
    @select="onSelectAgent"
    @clear="onClearAgent"
  />
</template>

<script setup lang="ts">
import { ref, computed, onMounted, nextTick, watch } from 'vue'
import {
  useAIChatStore,
  MODALITY_KEYS,
  MODALITY_LABELS,
  type ChatMsg,
  type ModalityKey,
} from './aiChatStore'
import { renderMarkdown } from '../../utils/markdown'
import { useToast } from '../../composables/useToast'
import { useVoiceInput } from '../../composables/useVoiceInput'
import { useChatAgentStore } from '../../stores/chatAgentStore'
import AgentSelectorSheet from './AgentSelectorSheet.vue'

const store = useAIChatStore()
const toast = useToast()
const agentStore = useChatAgentStore()

// 语音输入：复用全局 STT（本地 sherpa 优先，云转写兜底），转写结果追加到输入框。
const { isRecording, isTranscribing, sttError, startRecording, stopRecording } = useVoiceInput()

const AUTO = 'auto'
const draft = ref('')
const inputEl = ref<HTMLTextAreaElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
const pendingImages = ref<string[]>([])
const agentSheetOpen = ref(false)
const scrollEl = ref<HTMLElement | null>(null)
const modelSheetOpen = ref(false)
const optimizeOpen = ref(false)
const optimizeTarget = ref<string | null>(null)
// 模型选择 sheet 的临时选中态
const tempSelection = ref<string[]>([])

// 与后端 /api/llm/stream 的校验保持一致：单条最多 4 张、单张 ≤ 4MB（前端再
// 压一档，避免 data URL 接近 6MB 上限被拒）。
const MAX_IMAGES = 4
const MAX_IMAGE_BYTES = 4 << 20

const suggestions = [
  '用一句话解释什么是大模型',
  '帮我写一封请假邮件',
  '给我三个提升专注力的方法',
]

// ---- store 透传 ----
const active = computed(() => store.active)
const conversations = computed(() => store.conversations)
const activeId = computed(() => store.activeId)
const models = computed(() => store.models)
const modelsLoading = computed(() => store.modelsLoading)
const compareMode = computed(() => store.compareMode)
const compareModels = computed(() => store.compareModels)
const settings = computed(() => store.settings)
const isStreaming = computed(() => store.isStreaming)
const drawerOpen = computed({
  get: () => store.drawerOpen,
  set: (v) => (store.drawerOpen = v),
})
const settingsOpen = computed({
  get: () => store.settingsOpen,
  set: (v) => (store.settingsOpen = v),
})

const canSend = computed(
  () => (draft.value.trim().length > 0 || pendingImages.value.length > 0) && !isStreaming.value,
)
const composerPlaceholder = computed(() => {
  if (isRecording.value) return '正在录音，再点麦克风结束…'
  if (isTranscribing.value) return '转写中…'
  if (compareMode.value) return '向所选模型并行提问…（Enter 发送）'
  return '输入消息，Enter 发送，Shift+Enter 换行'
})

// 当前会话绑定的角色
const currentAgent = computed(() => {
  if (!active.value?.agentId) return null
  return agentStore.getAgent(active.value.agentId)
})

function openAgentSheet() {
  agentSheetOpen.value = true
  // 确保角色列表已加载
  if (agentStore.agents.length === 0) {
    agentStore.loadAgents()
  }
}

function onSelectAgent(agent: any) {
  if (!active.value) return
  active.value.agentId = agent.id
  store.persist()
  toast.success(`已切换到「${agent.name}」`)
}

function onClearAgent() {
  if (!active.value) return
  active.value.agentId = undefined
  store.persist()
  toast.success('已清除角色')
}

function goToLibrary() {
  settingsOpen.value = false
  window.location.hash = '#/agents'
  // 或用 router.push (需先 import)
}

/** 发图时实际会用的视觉模型（会话手动选了模型则显示它）。 */
const visionModelLabel = computed(() => {
  const conv = active.value
  if (conv?.model && conv.model !== AUTO) return conv.model
  return settings.value.modelByModality.vision || AUTO
})

/** auto 选项的副标签：一眼看到当前两个关键模态的默认。 */
const autoHint = computed(() => {
  const m = settings.value.modelByModality
  return `文本 ${m.text || AUTO} · 图像 ${m.vision || AUTO}`
})

function modalityOf(m: string) {
  return store.modalityOf(m)
}

/** 某模态"推荐"的模型（目录模态匹配优先，命名启发式兜底）。 */
function modelsForModality(mk: ModalityKey): string[] {
  return models.value.filter((m) => store.modalityOf(m) === mk)
}

/** 不属于该模态、但兜底可选的模型（排在推荐之后）。 */
function otherModels(mk: ModalityKey): string[] {
  return models.value.filter((m) => store.modalityOf(m) !== mk).slice(0, 30)
}

function onModalityDefault(mk: ModalityKey, e: Event) {
  const value = (e.target as HTMLSelectElement).value
  store.setSettings({
    modelByModality: { ...settings.value.modelByModality, [mk]: value },
  })
}

// 把消息列表按「用户 + 其后助手回答」聚合成 turn，便于对比渲染。
interface Turn { user: ChatMsg; answers: ChatMsg[] }
const turns = computed<Turn[]>(() => {
  const conv = active.value
  if (!conv) return []
  const out: Turn[] = []
  let user: ChatMsg | null = null
  let answers: ChatMsg[] = []
  const flush = () => {
    if (user) out.push({ user, answers })
    user = null
    answers = []
  }
  for (const m of conv.messages) {
    if (m.role === 'user') {
      flush()
      user = m
      answers = []
    } else if (m.role === 'assistant') {
      answers.push(m)
    }
  }
  flush()
  return out
})

function rendered(m: ChatMsg): string {
  if (m.error) return `<span class="err-text">${escapeHtml(m.error)}</span>`
  const html = renderMarkdown(m.content)
  return m.streaming ? html + '<span class="caret">▍</span>' : html
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// ---- 生命周期 ----
onMounted(() => {
  store.init()
  nextTick(autoGrow)
})

// 新消息或流式内容变化时自动滚到底
watch(
  () => [active.value?.messages.length, active.value?.messages.map((m) => m.content.length).join(',')],
  () => scrollToBottom(),
  { deep: false },
)
function scrollToBottom() {
  nextTick(() => {
    const el = scrollEl.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

// ---- 输入处理 ----
function autoGrow() {
  const el = inputEl.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 140) + 'px'
}

function onSend(text?: string) {
  const value = (text ?? draft.value).trim()
  const images = text ? [] : [...pendingImages.value]
  if (!value && images.length === 0) return
  if (store.models.length === 0) {
    toast.error('请先在「设置 → AI 网关」配置网关密钥')
    settingsOpen.value = true
    return
  }
  store.send(value || '（请描述这张图片）', images)
  if (!text) {
    draft.value = ''
    pendingImages.value = []
  }
  nextTick(() => {
    autoGrow()
    scrollToBottom()
  })
}

// ---- 图片附件 ----
function onPickImages(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = '' // 允许重复选择同一张
  for (const f of files) {
    if (pendingImages.value.length >= MAX_IMAGES) {
      toast.error(`最多 ${MAX_IMAGES} 张图片`)
      break
    }
    if (!f.type.startsWith('image/')) continue
    if (f.size > MAX_IMAGE_BYTES) {
      toast.error(`「${f.name}」超过 4MB，已跳过`)
      continue
    }
    const reader = new FileReader()
    reader.onload = () => {
      if (typeof reader.result === 'string') pendingImages.value.push(reader.result)
    }
    reader.readAsDataURL(f)
  }
}

function removeImage(i: number) {
  pendingImages.value.splice(i, 1)
}

// ---- 语音输入 ----
async function onMic() {
  if (isTranscribing.value) return
  if (isRecording.value) {
    const text = await stopRecording()
    if (text && text.trim()) {
      draft.value = draft.value ? `${draft.value} ${text.trim()}` : text.trim()
      nextTick(() => {
        autoGrow()
        inputEl.value?.focus()
      })
    } else if (sttError.value) {
      toast.error('语音识别失败：' + sttError.value)
    }
    return
  }
  const ok = await startRecording()
  if (!ok && sttError.value) {
    toast.error('无法开始录音：' + sttError.value)
  }
}

function stop() {
  store.stop()
}

// ---- 会话抽屉 ----
function toggleDrawer() {
  store.drawerOpen = !store.drawerOpen
}
function selectConversation(id: string) {
  store.selectConversation(id)
}
function newConversation() {
  store.newConversation()
  draft.value = ''
}
function confirmDelete(c: { id: string; title: string }) {
  if (confirm(`删除会话「${c.title}」？此操作不可撤销。`)) {
    store.deleteConversation(c.id)
  }
}

// ---- 模型选择 ----
function openModelSheet() {
  tempSelection.value = compareMode.value
    ? [...compareModels.value]
    : active.value?.model
      ? [active.value.model]
      : []
  modelSheetOpen.value = true
}
function isModelChecked(m: string): boolean {
  return tempSelection.value.includes(m)
}
function onModelCheck(m: string, e: Event) {
  const checked = (e.target as HTMLInputElement).checked
  if (compareMode.value) {
    if (checked && !tempSelection.value.includes(m)) tempSelection.value.push(m)
    if (!checked) tempSelection.value = tempSelection.value.filter((x) => x !== m)
  } else {
    tempSelection.value = [m]
  }
}
function applyModelSelection() {
  if (compareMode.value) {
    if (tempSelection.value.length === 0) {
      toast.error('请至少选择一个对比模型')
      return
    }
    store.setCompareModels([...tempSelection.value])
  } else {
    const m = tempSelection.value[0]
    if (m && active.value) {
      active.value.model = m
      store.setSettings({ defaultModel: m })
    }
  }
  modelSheetOpen.value = false
}function retryModels() {
  modelSheetOpen.value = false
  store.loadModels()
}

// ---- 对比模式 ----
function onToggleCompare() {
  store.toggleCompareMode()
  if (compareMode.value && compareModels.value.length === 0 && models.value.length > 0) {
    // 默认选前两个模型
    store.setCompareModels(models.value.slice(0, 2))
  }
}

// ---- 优化 ----
function openOptimize(m: ChatMsg) {
  optimizeTarget.value = m.id
  optimizeOpen.value = true
}
function doOptimize(model: string) {
  if (optimizeTarget.value) store.optimize(optimizeTarget.value, model)
  optimizeOpen.value = false
  optimizeTarget.value = null
  nextTick(scrollToBottom)
}

function regenerate(id: string) {
  store.regenerate(id)
}

function copy(m: ChatMsg) {
  if (!m.content) return
  navigator.clipboard?.writeText(m.content).then(
    () => toast.success('已复制'),
    () => toast.error('复制失败'),
  )
}

function saveSettings() {
  store.setSettings({ ...settings.value })
}

function formatTime(ts: number): string {
  const d = new Date(ts)
  const now = new Date()
  const sameDay = d.toDateString() === now.toDateString()
  if (sameDay) return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' })
}
</script>

<style scoped>
.ai-chat {
  display: flex;
  flex-direction: column;
  height: 100dvh;
  background: var(--bg-base);
}

/* 顶部栏 */
.top-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  padding-top: calc(8px + env(safe-area-inset-top));
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}
.icon-btn {
  width: 36px;
  height: 36px;
  border: none;
  background: transparent;
  color: var(--text-primary);
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex-shrink: 0;
}
.icon-btn:active { background: var(--bg-subtle); }
.icon-btn.active { color: var(--brand-primary); }
.top-title {
  flex: 1;
  display: flex;
  align-items: center;
  min-width: 0;
}
.title-text {
  font-size: 16px;
  font-weight: 600;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 右侧控制组 */
.top-controls {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}
.ctrl-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 10px;
  border: 1px solid var(--border);
  background: var(--bg-base);
  color: var(--text-primary);
  border-radius: 16px;
  font-size: 12px;
  cursor: pointer;
  max-width: 120px;
  white-space: nowrap;
  overflow: hidden;
}
.ctrl-btn:active { background: var(--bg-subtle); }
.ctrl-btn .material-symbols-outlined {
  font-size: 16px;
  flex-shrink: 0;
}
.ctrl-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 400;
}

/* 对比条 */
.compare-strip {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  background: var(--bg-subtle);
  border-bottom: 1px solid var(--border);
  overflow-x: auto;
}
.cs-label { font-size: 11px; color: var(--text-secondary); flex: none; }
.cs-chip {
  flex: none;
  font-size: 10px;
  padding: 2px 8px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 999px;
  color: var(--text-primary);
}
.cs-edit {
  flex: none;
  font-size: 11px;
  color: var(--brand-primary);
  background: none;
  border: none;
  cursor: pointer;
}

/* 消息区 */
.msg-area {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
  padding: 12px 0 8px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.row { display: flex; padding: 0 12px; }
.row.user { justify-content: flex-end; }
.row.ai { justify-content: flex-start; flex-direction: column; align-items: flex-start; }

.bubble {
  max-width: 100%;
  padding: 10px 14px;
  border-radius: 14px;
  font-size: 15px;
  line-height: 1.6;
  word-break: break-word;
  white-space: normal;
}
.user-bubble {
  background: var(--brand-primary, #4c8dff);
  color: #fff;
  border-bottom-right-radius: 4px;
}
.ai-bubble {
  background: var(--bg-card);
  color: var(--text-primary);
  border: 1px solid var(--border);
  border-bottom-left-radius: 4px;
}
.ai-bubble :deep(pre) {
  background: var(--bg-subtle);
  padding: 10px;
  border-radius: 8px;
  overflow-x: auto;
  font-size: 13px;
}
.ai-bubble :deep(code) {
  font-family: 'SF Mono', Menlo, monospace;
  font-size: 13px;
}
.ai-bubble :deep(p) { margin: 6px 0; }
.ai-bubble :deep(ul), .ai-bubble :deep(ol) { padding-left: 20px; margin: 6px 0; }
.caret { animation: blink 1s step-end infinite; color: var(--brand-primary); }
@keyframes blink { 50% { opacity: 0; } }

.usage-row, .usage {
  font-size: 10px;
  color: var(--text-muted);
  margin: 3px 2px 0;
}
.msg-error {
  font-size: 12px;
  color: var(--danger);
  margin-top: 3px;
}
.err-text { color: var(--danger); }

.msg-actions {
  display: flex;
  gap: 10px;
  margin: 5px 2px 0;
}
.act {
  font-size: 11px;
  color: var(--text-secondary);
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
}
.act:active { color: var(--brand-primary); }

/* 对比分栏 */
.compare-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  width: 100%;
  padding: 0 12px;
}
.compare-card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 8px;
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.cc-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 5px;
}
.cc-model {
  font-size: 11px;
  font-weight: 600;
  color: var(--brand-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cc-live { font-size: 9px; color: var(--warning); flex: none; }
.compare-card .ai-bubble { border: none; padding: 0; background: transparent; max-width: 100%; }
.compare-card .msg-actions { flex-wrap: wrap; }

/* 空状态 */
.empty {
  margin: auto;
  text-align: center;
  padding: 30px 20px;
  max-width: 100%;
}
.empty-emoji { font-size: 40px; }
.empty-title { font-size: 17px; font-weight: 600; margin: 8px 0 4px; color: var(--text-primary); }
.empty-sub { font-size: 13px; color: var(--text-secondary); line-height: 1.5; }
.empty-suggestions { display: flex; flex-direction: column; gap: 8px; margin-top: 16px; }
.sug {
  font-size: 13px;
  padding: 10px 14px;
  border-radius: 10px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
  cursor: pointer;
  text-align: left;
}
.sug:active { background: var(--bg-subtle); }

/* typing */
.typing { display: flex; gap: 4px; padding: 4px 6px; margin: 0 12px; }
.dot {
  width: 6px; height: 6px; border-radius: 50%;
  background: var(--text-secondary);
  animation: bounce 1.2s infinite;
}
.dot:nth-child(2) { animation-delay: 0.2s; }
.dot:nth-child(3) { animation-delay: 0.4s; }
@keyframes bounce { 0%, 60%, 100% { transform: translateY(0); opacity: 0.5; } 30% { transform: translateY(-5px); opacity: 1; } }

/* 输入区 */
.composer {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px 12px;
  padding-bottom: calc(8px + env(safe-area-inset-bottom));
  background: var(--bg-card);
  border-top: 1px solid var(--border);
}
.composer-row {
  display: flex;
  align-items: flex-end;
  gap: 6px;
}
.attach-btn {
  flex: 0 0 auto;
  margin-bottom: 1px;
}
.attach-btn.recording {
  color: var(--danger);
  animation: mic-pulse 1.2s ease-in-out infinite;
}
@keyframes mic-pulse {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(1.15); }
}
.file-input {
  display: none;
}

/* 待发送图片缩略图 */
.attach-strip {
  display: flex;
  align-items: center;
  gap: 6px;
  overflow-x: auto;
  padding: 2px 0;
}
.attach-thumb {
  position: relative;
  flex: none;
  width: 50px;
  height: 50px;
  border-radius: 8px;
  overflow: hidden;
  border: 1px solid var(--border);
}
.attach-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.attach-del {
  position: absolute;
  top: 0;
  right: 0;
  width: 18px;
  height: 18px;
  border: none;
  border-radius: 0 0 0 8px;
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
}
.attach-hint {
  flex: none;
  font-size: 10px;
  color: var(--text-muted);
  white-space: nowrap;
}

/* 气泡内图片 */
.bubble-images {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 6px;
  margin-bottom: 8px;
}
.bubble-img {
  width: 100%;
  max-height: 180px;
  object-fit: cover;
  border-radius: 10px;
  background: rgba(0, 0, 0, 0.08);
}

/* 模态徽标 & 设置行 */
.modality-badge {
  flex: none;
  font-size: 10px;
  padding: 2px 7px;
  border-radius: 999px;
  background: var(--bg-subtle);
  color: var(--text-secondary);
  border: 1px solid var(--border);
}
.modality-badge[data-mod='vision'] { color: var(--brand-primary); border-color: color-mix(in srgb, var(--brand-primary) 40%, transparent); }
.modality-badge[data-mod='audio'] { color: var(--warning); border-color: color-mix(in srgb, var(--warning) 40%, transparent); }
.modality-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}
.modality-name {
  flex: 0 0 64px;
  font-size: 12px;
  color: var(--text-secondary);
}
.modality-sel { flex: 1; }

.composer-input {
  flex: 1;
  resize: none;
  border: 1px solid var(--border);
  background: var(--bg-base);
  color: var(--text-primary);
  border-radius: 16px;
  padding: 10px 14px;
  font-size: 14px;
  line-height: 1.5;
  max-height: 120px;
  outline: none;
}
.composer-input:focus { border-color: var(--brand-primary); }
.send-btn {
  flex: 0 0 auto;
  width: 38px;
  height: 38px;
  border: none;
  border-radius: 50%;
  background: var(--brand-primary, #4c8dff);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}
.send-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.send-btn.stop { background: var(--danger); }

/* 抽屉 */
.drawer-mask {
  position: fixed; inset: 0;
  background: rgba(0, 0, 0, 0.4);
  z-index: 40;
  display: flex;
}
.drawer {
  width: 75%;
  max-width: 300px;
  height: 100%;
  background: var(--bg-card);
  display: flex;
  flex-direction: column;
  animation: slide-in 0.2s ease;
}
@keyframes slide-in { from { transform: translateX(-100%); } to { transform: translateX(0); } }
.drawer-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 14px 16px; font-size: 15px; font-weight: 600;
  border-bottom: 1px solid var(--border);
}
.conv-list { flex: 1; overflow-y: auto; padding: 6px; }
.conv-item {
  display: flex; align-items: center; gap: 8px;
  padding: 10px; border-radius: 8px; cursor: pointer;
}
.conv-item.active { background: var(--bg-subtle); }
.conv-main { flex: 1; min-width: 0; }
.conv-title {
  font-size: 13px; font-weight: 500; color: var(--text-primary);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.conv-meta { font-size: 11px; color: var(--text-muted); margin-top: 2px; }
.conv-del { color: var(--text-muted); background: none; border: none; cursor: pointer; }
.conv-del:active { color: var(--danger); }
.conv-empty { text-align: center; color: var(--text-muted); padding: 30px; font-size: 13px; }

/* sheet 通用 */
.sheet-mask {
  position: fixed; inset: 0;
  background: rgba(0, 0, 0, 0.4);
  z-index: 50;
  display: flex; align-items: flex-end;
}
.sheet {
  width: 100%;
  background: var(--bg-card);
  border-radius: 14px 14px 0 0;
  padding: 14px 16px calc(16px + env(safe-area-inset-bottom));
  max-height: 75vh;
  overflow-y: auto;
  animation: sheet-up 0.22s ease;
}
@keyframes sheet-up { from { transform: translateY(100%); } to { transform: translateY(0); } }
.sheet-head {
  display: flex; align-items: center; justify-content: space-between;
  font-size: 15px; font-weight: 600; margin-bottom: 10px;
}
.sheet-state { font-size: 13px; color: var(--text-secondary); padding: 8px 0; line-height: 1.5; }
.link-btn { color: var(--brand-primary); background: none; border: none; cursor: pointer; margin-left: 6px; }

.model-list { display: flex; flex-direction: column; gap: 3px; margin-bottom: 10px; }
.model-item {
  display: flex; align-items: center; gap: 8px;
  padding: 10px 12px; border-radius: 8px; cursor: pointer;
  border: 1px solid transparent;
}
.model-item.checked { border-color: var(--brand-primary); background: color-mix(in srgb, var(--brand-primary) 8%, transparent); }
.model-item.plain { justify-content: flex-start; border: 1px solid var(--border); }
.model-check { width: 16px; height: 16px; accent-color: var(--brand-primary); }
.model-name { flex: 1; font-size: 13px; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.model-current { font-size: 10px; color: var(--brand-primary); }
.sheet-confirm {
  width: 100%; padding: 12px; border: none; border-radius: 999px;
  background: var(--brand-primary, #4c8dff); color: #fff; font-size: 15px; font-weight: 600;
  cursor: pointer;
}

/* 设置字段 */
.field { margin-bottom: 16px; }
.field-label { font-size: 13px; font-weight: 600; color: var(--text-secondary); margin-bottom: 6px; }
.field-hint { font-size: 11px; color: var(--text-muted); margin-top: 4px; line-height: 1.4; }
.field input[type='range'] { width: 100%; accent-color: var(--brand-primary); }
.num-input, .sys-input, .sel-input {
  width: 100%; padding: 9px 12px; font-size: 14px;
  background: var(--bg-base); color: var(--text-primary);
  border: 1px solid var(--border); border-radius: 8px; outline: none;
}
.sys-input { resize: vertical; font-family: inherit; }
.num-input:focus, .sys-input:focus, .sel-input:focus { border-color: var(--brand-primary); }

/* 角色卡片 */
.agent-card {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 12px;
  background: var(--bg-secondary);
}
.agent-card-header {
  display: flex;
  gap: 12px;
  margin-bottom: 12px;
}
.agent-emoji {
  font-size: 32px;
  line-height: 1;
  flex-shrink: 0;
}
.agent-card-info {
  flex: 1;
  min-width: 0;
}
.agent-card-name {
  font-size: 15px;
  font-weight: 600;
  margin-bottom: 4px;
}
.agent-card-desc {
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.4;
}
.agent-change-btn {
  width: 100%;
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-base);
  font-size: 14px;
  cursor: pointer;
}
.no-agent {
  text-align: center;
  padding: 16px;
  color: var(--text-secondary);
  font-size: 14px;
}
.no-agent p {
  margin: 0 0 12px 0;
}
.agent-select-btn {
  padding: 10px 20px;
  border: 1px solid var(--brand-primary);
  border-radius: 8px;
  background: var(--bg-base);
  color: var(--brand-primary);
  font-size: 14px;
  font-weight: 500;
  cursor: pointer;
}

.library-link {
  width: 100%;
  margin-top: 12px;
  padding: 10px;
  background: transparent;
  border: none;
  font-size: 13px;
  color: var(--brand-primary);
  cursor: pointer;
  text-align: center;
}

.library-link:hover {
  text-decoration: underline;
}
</style>

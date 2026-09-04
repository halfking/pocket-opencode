<!--
  AIChatView — 豆包式 AI 对话。
  - 多轮会话（本地存储）、模型选择、流式输出、参数调节
  - 「对比模式」：同一问题并行发给多个模型，分栏比较
  - 每条回答可「用另一模型检查 / 优化」
  路由：/ai-chat
-->
<template>
  <div class="ai-chat">
    <!-- 顶部"会话/角色/操作"栏：合并到 AppLayout 单层标题栏的右侧 chrome
         (HeaderActionsPortal)，左侧 ≡/返回由壳层提供；消灭原双层顶栏。 -->
    <HeaderActionsPortal>
      <button
        v-if="conversations.length > 1 || active"
        class="chat-convo-btn"
        type="button"
        :aria-label="'切换会话'"
        @click="toggleDrawer"
      >
        <span class="material-symbols-outlined" aria-hidden="true">chat_bubble</span>
        <span class="convo-label">{{ active?.title || '新对话' }}</span>
      </button>
      <button
        class="chat-icon-btn"
        :class="{ active: compareMode }"
        type="button"
        aria-label="切换对比模式"
        @click="onToggleCompare"
      >
        <span class="material-symbols-outlined" aria-hidden="true">balance</span>
      </button>
      <button
        class="chat-icon-btn"
        type="button"
        aria-label="对话参数"
        @click="settingsOpen = true"
      >
        <span class="material-symbols-outlined" aria-hidden="true">tune</span>
      </button>
      <button
        class="chat-icon-btn"
        type="button"
        aria-label="新建对话"
        @click="newConversation"
      >
        <span class="material-symbols-outlined" aria-hidden="true">add</span>
      </button>
    </HeaderActionsPortal>

    <!-- 第二层"角色 / 模型"信息行：内联 chip，点击切换。
         把"角色选择"从原顶栏下放到此行，腾出标题栏空间。 -->
    <div class="context-row">
      <button
        class="chip"
        type="button"
        :aria-label="'切换角色'"
        @click="openAgentSheet"
      >
        <span class="material-symbols-outlined chip-icon" aria-hidden="true">{{
          currentAgent ? 'person' : 'psychology'
        }}</span>
        <span class="chip-label">{{ currentAgent ? currentAgent.name : '选择角色' }}</span>
      </button>
      <button
        class="chip ghost"
        type="button"
        :aria-label="'切换模型'"
        @click="openModelSheet"
      >
        <span class="material-symbols-outlined chip-icon" aria-hidden="true">model_training</span>
        <span class="chip-label">{{ modelChipLabel }}</span>
      </button>
    </div>

    <!-- 对比模式选中的模型条 -->
    <div v-if="compareMode" class="compare-strip">
      <span class="cs-label">对比：</span>
      <span v-for="m in compareModels" :key="m" class="cs-chip">{{ m }}</span>
      <button class="cs-edit" @click="modelSheetOpen = true">编辑</button>
    </div>

    <!-- 消息区 -->
    <main ref="scrollEl" class="msg-area" @scroll="onMsgScroll">
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
            <!-- auto 回退重试进度：正文到达前/后都以一行灰色小字透出 -->
            <div v-if="a.retryHint" class="msg-retry">{{ a.retryHint }}</div>
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
          <!-- auto 回退重试进度：正文到达前/后都以一行灰色小字透出 -->
          <div v-if="turn.answers[0].retryHint" class="msg-retry">{{ turn.answers[0].retryHint }}</div>
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

    <!-- 输入区：统一输入组件（宽文本区 + 多模态/角色/优化/提交独立工具行）。
         滚动联动：随 tabbar 一起下移（--bottom-chrome-hide，跟手 1:1）；
         吸附落定为全隐后负 margin 把槽位让给消息区（离散切换 + 过渡）。 -->
    <footer
      ref="composerEl"
      class="composer"
      :class="{ snapping: chromeSnapping, 'chrome-hidden': composerHidden }"
      :inert="composerInert"
    >
      <UnifiedComposer
        ref="composerRef"
        v-model="draft"
        :placeholder="composerPlaceholder"
        :agent-id="active?.agentId"
        :submitting="isStreaming"
        :enable="{ voice: true, image: true, camera: true, file: false, agent: true, optimize: true }"
        @update:agent-id="onComposerAgent"
        @submit="onComposerSubmit"
      >
        <template #submit>
          <button
            v-if="isStreaming"
            class="send-btn stop"
            type="button"
            aria-label="停止生成"
            @click="stop"
          >
            <span class="material-symbols-outlined">stop</span>
          </button>
          <button
            v-else
            class="send-btn"
            type="button"
            aria-label="发送"
            :disabled="!composerCanSubmit"
            @click="composerRef?.submit()"
          >
            <span class="material-symbols-outlined">send</span>
          </button>
        </template>
      </UnifiedComposer>
    </footer>

    <!-- 会话抽屉：统一使用公共 BottomSheet 侧边布局 -->
    <BottomSheet
      v-model="drawerOpen"
      placement="left"
      title="会话"
      aria-label="会话列表"
    >
      <template #header>
        <h3 class="sheet-title">会话</h3>
        <button class="sheet-header-action" type="button" aria-label="新建对话" @click="newConversation">
          <span class="material-symbols-outlined" aria-hidden="true">add</span>
        </button>
      </template>
      <div class="drawer-tabs" role="tablist">
        <button
          :class="['drawer-tab', { active: drawerTab === 'active' }]"
          role="tab"
          :aria-selected="drawerTab === 'active'"
          @click="drawerTab = 'active'"
        >活跃 ({{ activeConversations.length }})</button>
        <button
          :class="['drawer-tab', { active: drawerTab === 'archived' }]"
          role="tab"
          :aria-selected="drawerTab === 'archived'"
          @click="drawerTab = 'archived'"
        >归档 ({{ archivedConversations.length }})</button>
      </div>
      <div class="conv-list">
        <div
          v-for="c in displayedConversations"
          :key="c.id"
          class="conv-item"
          :class="{ active: c.id === activeId }"
          @click="selectConversation(c.id)"
        >
          <div class="conv-main">
            <div class="conv-title">{{ c.title }}</div>
            <div class="conv-meta">
              <span>{{ c.mode === 'compare' ? '对比' : (c.model || '—') }}</span>
              <span v-if="c.archivedAt"> · 已归档</span>
              <span> · {{ formatTime(c.archivedAt || c.updatedAt) }}</span>
            </div>
          </div>
          <button
            v-if="drawerTab === 'archived'"
            class="conv-act"
            aria-label="恢复会话"
            @click.stop="restoreConversation(c.id)"
          >
            <span class="material-symbols-outlined">unarchive</span>
          </button>
          <button
            v-else
            class="conv-act"
            aria-label="归档会话"
            @click.stop="archiveConversation(c.id)"
          >
            <span class="material-symbols-outlined">archive</span>
          </button>
          <button class="conv-act danger" aria-label="删除会话" @click.stop="confirmDelete(c)">
            <span class="material-symbols-outlined">delete</span>
          </button>
        </div>
        <div v-if="displayedConversations.length === 0" class="conv-empty">
          {{ drawerTab === 'archived' ? '归档区暂无会话' : '还没有会话' }}
        </div>
      </div>
    </BottomSheet>

    <!-- 模型选择 / 对比选择：统一使用公共 BottomSheet -->
    <BottomSheet
      v-model="modelSheetOpen"
      :title="compareMode ? '选择对比模型（可多选）' : '选择模型'"
      height="full"
    >
      <div v-if="modelsLoading" class="sheet-state">模型加载中…</div>
      <div v-else-if="models.length === 0" class="sheet-state">
        未获取到模型，请先在「设置 → AI 网关」配置网关密钥。
        <button class="link-btn" @click="retryModels">重试</button>
      </div>
      <div v-else class="model-list">
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
      <template #footer>
        <button class="sheet-confirm" type="button" @click="applyModelSelection">确定</button>
      </template>
    </BottomSheet>

    <!-- 优化：选择模型 -->
    <BottomSheet v-model="optimizeOpen" title="用哪个模型检查并优化？">
      <div v-if="models.length === 0" class="sheet-state">暂无可用模型</div>
      <div v-else class="model-list">
        <button
          v-for="m in models"
          :key="m"
          class="model-item plain"
          type="button"
          @click="doOptimize(m)"
        >
          <span class="model-name">{{ m }}</span>
        </button>
      </div>
    </BottomSheet>

    <!-- 参数设置 -->
    <BottomSheet v-model="settingsOpen" title="对话参数" height="full">

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
              <span class="agent-emoji">{{ currentAgent.emoji || '👤' }}</span>
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

        <button class="sheet-confirm" type="button" @click="settingsOpen = false">完成</button>
    </BottomSheet>
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
import { ref, computed, inject, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  useAIChatStore,
  MODALITY_KEYS,
  MODALITY_LABELS,
  type ChatMsg,
  type ModalityKey,
} from './aiChatStore'
import { renderMarkdown } from '../../utils/markdown'
import { useToast } from '../../composables/useToast'
import { useConfirm } from '../../composables/useConfirm'
import { useChatAgentStore } from '../../stores/chatAgentStore'
import { bindScrollHideChrome } from '../../composables/useScrollHideChrome'
import { SCROLL_CHROME_KEY } from '../../composables/scroll-chrome'
import AgentSelectorSheet from './AgentSelectorSheet.vue'
import BottomSheet from '../../components/base/BottomSheet.vue'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import UnifiedComposer from '../../components/business/UnifiedComposer.vue'

const store = useAIChatStore()
const router = useRouter()
const route = useRoute()
const toast = useToast()
const { confirm } = useConfirm()
const agentStore = useChatAgentStore()

// 统一输入组件引用（提交/重置/内部 canSubmit）
const composerRef = ref<InstanceType<typeof UnifiedComposer> | null>(null)
const composerCanSubmit = computed(() => composerRef.value?.canSubmit ?? false)

/* ── 滚动联动底部 chrome：composer 随 tabbar 下移隐藏/上滑唤出 ──
   滚动增量上报给壳层引擎；composer 高度（textarea 自增高会变）经
   ResizeObserver 上报参与隐藏距离。 */
const chromeCtx = inject(SCROLL_CHROME_KEY, null)
const chromeSnapping = chromeCtx?.snapping ?? ref(false)
const composerHidden = chromeCtx?.hidden ?? ref(false)
const composerEl = ref<HTMLElement | null>(null)
// 绑定吸附落定态：跟手过程（hiddenOffset 临时峰值）不应让 inert 闪烁，
// 仅在引擎判定全隐后整体从 Tab 焦点链路中切出。
const composerInert = computed(() => composerHidden.value)

// 距底 < 50px 才自动跟随：用户上翻阅读历史时，流式输出不打扰
const autoScroll = ref(true)
function onMsgScroll() {
  const el = scrollEl.value
  if (!el) return
  autoScroll.value = el.scrollHeight - el.scrollTop - el.clientHeight < 50
}

const AUTO = 'auto'
const draft = ref('')
const agentSheetOpen = ref(false)
const scrollEl = ref<HTMLElement | null>(null)
const modelSheetOpen = ref(false)
const optimizeOpen = ref(false)
const optimizeTarget = ref<string | null>(null)
const drawerTab = ref<'active' | 'archived'>('active')
// 模型选择 sheet 的临时选中态
const tempSelection = ref<string[]>([])

// 与后端 /api/llm/stream 的校验保持一致（统一由 UnifiedComposer 的
// useAttachments 执行：4 张 / 单张 4MB / 总 32MB）。

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

const activeConversations = computed(() =>
  [...conversations.value].filter((c) => !c.archivedAt).sort((a, b) => b.updatedAt - a.updatedAt),
)
const archivedConversations = computed(() =>
  [...conversations.value].filter((c) => c.archivedAt).sort((a, b) => (b.archivedAt ?? 0) - (a.archivedAt ?? 0)),
)
const displayedConversations = computed(() =>
  drawerTab.value === 'archived' ? archivedConversations.value : activeConversations.value,
)

function archiveConversation(id: string) {
  store.archiveConversation(id)
}
function restoreConversation(id: string) {
  store.restoreConversation(id)
}
const drawerOpen = computed({
  get: () => store.drawerOpen,
  set: (v) => (store.drawerOpen = v),
})
const settingsOpen = computed({
  get: () => store.settingsOpen,
  set: (v) => (store.settingsOpen = v),
})

const composerPlaceholder = computed(() => {
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

/** 统一输入组件工具行内的角色 chip 同步到当前会话。 */
function onComposerAgent(agentId: string | undefined) {
  if (!active.value) return
  active.value.agentId = agentId
  store.persist()
  const name = agentId ? agentStore.getAgent(agentId)?.name : ''
  toast.success(agentId ? `已切换到「${name}」` : '已清除角色')
}

function goToLibrary() {
  settingsOpen.value = false
  router.push('/agents')
}

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
let unbindChromeScroll: (() => void) | null = null
let composerRO: ResizeObserver | null = null
onMounted(() => {
  store.init()
  syncRouteTitle()
  // 从 AI 工具页「快速提问」跳转进来时预填草稿（无会话场景的新对话入口）。
  const prefill = route.query.prompt
  if (typeof prefill === 'string' && prefill.trim()) {
    draft.value = prefill.trim()
  }
  if (scrollEl.value && chromeCtx) {
    unbindChromeScroll = bindScrollHideChrome(scrollEl.value, chromeCtx)
  }
  const comp = composerEl.value
  if (comp && chromeCtx) {
    const measure = () => {
      chromeCtx.bottomInsetHeight.value = comp.offsetHeight
    }
    measure()
    composerRO = new ResizeObserver(measure)
    composerRO.observe(comp)
  }
})
onUnmounted(() => {
  unbindChromeScroll?.()
  composerRO?.disconnect()
  if (chromeCtx) chromeCtx.bottomInsetHeight.value = 0
})

/**
 * 标题合并：原"对话"标题 + 当前会话标题 = 单层完整标题（用户要求"第二层放第一层"）。
 * 通过修改 route.meta.title 让 AppLayout 渲染时同步显示。
 * 注意：vue-router 每次导航都会从路由记录重新 merge meta，因此无需在卸载时
 * "恢复"标题——卸载时 route 已指向新路由，恢复动作反而会污染落地页标题。
 */
const ORIGINAL_TITLE = '对话'
function syncRouteTitle() {
  const conv = store.active
  const t = conv?.title?.trim()
  route.meta.title = t ? `${ORIGINAL_TITLE} · ${t}` : ORIGINAL_TITLE
}
watch(() => store.active?.title, syncRouteTitle, { immediate: true })

/** 模型 chip 标签：会话模型 > 默认模型 > auto */
const modelChipLabel = computed(() => {
  const m = active.value?.model
  if (m && m !== AUTO) return m
  return settings.value.defaultModel || AUTO
})

// 新消息或流式内容变化时自动滚到底（仅当用户本就停在底部附近）
watch(
  () => [active.value?.messages.length, active.value?.messages.map((m) => m.content.length).join(',')],
  () => scrollToBottom(),
  { deep: false },
)
function scrollToBottom(force = false) {
  if (!force && !autoScroll.value) return
  // 程序化滚动：抑制上报，避免滚动事件被引擎误判为用户上滑而隐藏输入区
  chromeCtx?.suppress()
  nextTick(() => {
    const el = scrollEl.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

// ---- 输入处理 ----

/** 空态建议问题直发（无附件）。 */
function onSend(text?: string) {
  const value = (text ?? draft.value).trim()
  if (!value) return
  if (store.models.length === 0) {
    toast.error('请先在「设置 → AI 网关」配置网关密钥')
    settingsOpen.value = true
    return
  }
  store.send(value, [])
  if (!text) draft.value = ''
  chromeCtx?.reveal()
  scrollToBottom(true)
}

/** 统一输入组件提交（文本 + 图片附件）。 */
function onComposerSubmit(payload: { text: string; images: string[] }) {
  if (store.models.length === 0) {
    toast.error('请先在「设置 → AI 网关」配置网关密钥')
    settingsOpen.value = true
    return
  }
  store.send(payload.text.trim() || '（请描述这张图片）', payload.images)
  composerRef.value?.reset()
  chromeCtx?.reveal()
  scrollToBottom(true)
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
async function confirmDelete(c: { id: string; title: string }) {
  if (await confirm({ title: '删除会话', message: `删除会话「${c.title}」？此操作不可撤销。`, confirmText: '删除', danger: true })) {
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
  height: 100%;
  background: var(--bg-base);
}

/* 输入区图标按钮（加图/麦克风）基础形态：原顶栏 .icon-btn 样式随顶栏删除，
   但 composer 仍在使用，这里保留等价定义。 */
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

/* 顶栏由 AppLayout 提供；这里只渲染"会话名 / 角色 / 模型"chip 行 */
.context-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px var(--space-3);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  overflow-x: auto;
  scrollbar-width: none;
}
.context-row::-webkit-scrollbar { display: none; }

.chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 5px 10px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--bg-base);
  color: var(--text-primary);
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  flex-shrink: 0;
  white-space: nowrap;
  max-width: 50vw;
}
.chip:active { background: var(--bg-subtle); }
.chip.ghost { background: transparent; color: var(--text-secondary); }
.chip-icon { font-size: 14px; flex-shrink: 0; }
.chip-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

/* 注入 AppLayout header-actions 的按钮样式（与 AppLayout 默认 :deep 样式叠加，
   但我们要更紧凑、可显示文字标签）。 */
:deep(.chat-convo-btn) {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  max-width: 50vw;
  padding: 0 10px;
  height: 36px;
  border-radius: 999px;
  background: var(--bg-subtle);
  color: var(--text-primary);
  border: 1px solid var(--border);
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
  white-space: nowrap;
  overflow: hidden;
}
:deep(.chat-convo-btn:active) { background: var(--border); }
:deep(.chat-convo-btn .material-symbols-outlined) { font-size: 16px; flex-shrink: 0; }
:deep(.chat-convo-btn .convo-label) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

:deep(.chat-icon-btn.active) { color: var(--brand-primary); background: var(--brand-bg); }

/* 窄屏（≤380px）隐藏 chip 文字标签，只保留图标，腾出更多空间给标题 */
@media (max-width: 380px) {
  .context-row { padding: 6px var(--space-3); gap: 6px; }
  .chip-label { display: none; }
  :deep(.chat-convo-btn .convo-label) { display: none; }
}

/* 对比条 */
.compare-strip {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 12px;
  background: var(--bg-subtle);
  border-bottom: 1px solid var(--border);
  overflow-x: auto;
  font-family: var(--font-sans);
}
.cs-label {
  font-size: 12px;
  font-weight: 500;
  line-height: 1.2;
  color: var(--text-secondary);
  flex: none;
}
.cs-chip {
  flex: none;
  font-size: 12px;
  line-height: 1.2;
  font-weight: 500;
  padding: 4px 10px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 999px;
  color: var(--text-primary);
  white-space: nowrap;
}
.cs-edit {
  flex: none;
  font-size: 12px;
  line-height: 1.2;
  font-weight: 500;
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
  color: var(--text-inverse);
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
/* auto 回退重试进度提示（retry 帧）：灰色小字，风格同 msg-error 但不告警 */
.msg-retry {
  font-size: 12px;
  color: var(--text-muted);
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
  padding-bottom: calc(8px + var(--app-safe-bottom));
  background: var(--bg-card);
  border-top: 1px solid var(--border);
  /* 滚动联动：随 tabbar 一起下移（--bottom-chrome-hide 由 AppLayout 下发），
     跟手阶段纯 transform 不动布局。 */
  will-change: transform;
  transform: translate3d(0, var(--bottom-chrome-hide, 0px), 0);
}

/* 吸附落定为全隐：槽位让给消息区（离散切换，与吸附动画同步过渡） */
.composer.chrome-hidden {
  margin-bottom: calc(-1 * var(--bottom-chrome-inset, 0px));
}

/* 吸附阶段的过渡（跟手 1:1 时无过渡） */
.composer.snapping {
  transition:
    transform var(--duration-chrome) var(--ease-chrome),
    margin-bottom var(--duration-chrome) var(--ease-chrome);
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
  color: var(--text-inverse);
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
  background: var(--overlay-subtle);
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
  color: var(--text-inverse);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}
.send-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.send-btn.stop { background: var(--danger); }

/* 会话列表业务样式 */
.conv-list { flex: 1; overflow-y: auto; padding: 6px; }
.drawer-tabs {
  display: flex;
  gap: 0;
  padding: 6px 8px 0;
  border-bottom: 1px solid var(--border);
}
.drawer-tab {
  flex: 1;
  padding: 8px 4px;
  border: none;
  background: transparent;
  font-size: 13px;
  color: var(--text-secondary);
  border-bottom: 2px solid transparent;
  cursor: pointer;
}
.drawer-tab.active {
  color: var(--brand-primary);
  border-bottom-color: var(--brand-primary);
  font-weight: 600;
}
.sheet-header-action {
  position: absolute;
  top: 50%;
  right: 56px;
  transform: translateY(-50%);
  width: 44px;
  height: 44px;
  border: none;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}
.conv-item {
  display: flex; align-items: center; gap: 6px;
  padding: 10px; border-radius: 8px; cursor: pointer;
}
.conv-item.active { background: var(--bg-subtle); }
.conv-main { flex: 1; min-width: 0; }
.conv-title {
  font-size: 13px; font-weight: 500; color: var(--text-primary);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.conv-meta { font-size: 11px; color: var(--text-muted); margin-top: 2px; }
.conv-act {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  color: var(--text-muted);
  background: none;
  border: none;
  border-radius: 6px;
  cursor: pointer;
}
.conv-act .material-symbols-outlined { font-size: 18px; }
.conv-act:active { color: var(--brand-primary); background: var(--bg-subtle); }
.conv-act.danger:active { color: var(--danger); }
.conv-empty { text-align: center; color: var(--text-muted); padding: 30px; font-size: 13px; }

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
  background: var(--brand-primary, #4c8dff); color: var(--text-inverse); font-size: 15px; font-weight: 600;
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

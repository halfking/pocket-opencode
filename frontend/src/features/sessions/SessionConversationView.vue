<script setup lang="ts">
/**
 * SessionConversationView — 主题任务 / 会话实时对话视图（P1 会话工作台）
 *
 * 路由：/sessions/:id?instance_id=xxx&title=xxx
 *
 * P1 改造（设计方案 v2 §4.3）+ P1.5 界面减负：
 *  - 头部收敛（P1.5）：原 top-bar + SessionStatusBar 两行合一——
 *    [退出] [动态状态图标] 标题+信号副标题（状态·时长） [⋮]；
 *    壳层顶栏由路由 hideAppHeader 契约修复隐藏（AppLayout 此前未消费）；
 *  - 动态状态图标（SessionStatusBar，信号即界面 §2.2）：审批呼吸/运行旋转
 *    （单击=停止）/空闲播放（单击=继续），实例名等非实时信息收进 ⋮ 抽屉；
 *  - 轮次时间线（RoundTimeline）：事件流按轮折叠，轮摘要由 round.completed
 *    事件下发（§6.1），前端不再自行拼接；
 *  - 详情抽屉（SessionDetailDrawer，⋮ 触发）：实例信息 + 旧 opencode 详情页
 *    的统计与导出能力收敛于此；
 *  - 输入区（SessionComposer，契约 §4 固定目标模式）：快速指令面板 +
 *    语音转写入草稿 + SQLite 草稿持久化（C 交付，此处挂载接线）。
 *
 * 保留：ApprovalPanel / ApprovalBottomSheet（含服务端确认语义）、SSE 流式渲染、
 * 离线审批入队、自动滚底（用户上滚暂停）。
 */
import { onMounted, onBeforeUnmount, ref, nextTick, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSessionStore } from '../../stores/session'
import { useApprovalStore } from '../../stores/approval'
import { useToast } from '../../composables/useToast'
import { useFeatureFlag } from '../../config/featureFlags'
import { usePendingApprovals } from '../../composables/usePendingApprovals'
import { ApprovalBottomSheet, type ApprovalDecision } from '../../components'
import ApprovalPanel from './ApprovalPanel.vue'
import SessionStatusBar from './SessionStatusBar.vue'
import RoundTimeline from './RoundTimeline.vue'
import SessionDetailDrawer from './SessionDetailDrawer.vue'
import SessionComposer from './SessionComposer.vue'
import {
  deriveFallbackPhase,
  formatStatusElapsed,
  groupMessagesIntoRounds,
  roundSummaryFallback,
  sessionStatusLabel,
  statsFromMessages,
  statsFromRounds,
  useSessionEvents,
  type SessionStats,
} from './useSessionEvents'

const emit = defineEmits<{ close: [] }>()

const props = withDefaults(defineProps<{
  embedded?: boolean
  sessionId?: string
  instanceId?: string
  title?: string
}>(), {
  embedded: false,
  sessionId: '',
  instanceId: '',
  title: '',
})

const route = useRoute()
const router = useRouter()
const store = useSessionStore()
const approvalStore = useApprovalStore()
const toast = useToast()

const sessionID = computed(() => props.sessionId || (route.params.id as string) || '')
const instanceID = computed(() => props.instanceId || (route.query.instance_id as string) || localStorage.getItem('selected_instance_id') || '')
const initialTitle = computed(() => props.title || (route.query.title as string) || '')

const sending = ref(false)
const messagesEl = ref<HTMLElement | null>(null)
const autoScroll = ref(true)
const approvalPanelEl = ref<InstanceType<typeof ApprovalPanel> | null>(null)

/** ?prompt= 深链一次性预填（传入 SessionComposer 的 initialText）。 */
const composerInitialText = ref('')

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
  // Deep Link 参数（指挥中心/本地通知进入，设计方案 v2 §4.2-3/§4.2-5）：
  //   ?prompt=xxx     → 预填输入草稿，可编辑再发送（转写/指令不直发）
  //   ?approval=open  → 清除"已忽略"记录，强制弹出审批 Bottom Sheet
  applyDeepLinkQuery()
  await store.open(sessionID.value, instanceID.value, initialTitle.value)
  await nextTick()
  scrollToBottom(true)
  // 审批 Bottom Sheet（feature flag 暗Launch）：进入会话即查一次 pending 并轮询。
  if (approvalSheetEnabled) startApprovalPolling()
  // P1：session.activity / round.completed 事件订阅 + 快照追赶（§4.3-1/2）
  sessionEvents.startLive()
})

onBeforeUnmount(() => {
  stopApprovalPolling()
  sessionEvents.stopLive()
  store.close()
})

async function scrollToBottom(force = false) {
  if (!autoScroll.value && !force) return
  await nextTick()
  if (messagesEl.value) {
    messagesEl.value.scrollTop = messagesEl.value.scrollHeight
  }
}

/**
 * 处理 Deep Link 查询参数并从地址栏清除（避免刷新/回退重复触发）：
 *   prompt=xxx    → 预填草稿（不自动发送，保持"先入草稿可编辑"纪律）
 *   approval=open → 重置已忽略集合，让 pending 审批 Sheet 立即弹出
 */
function applyDeepLinkQuery() {
  const q = route.query
  const promptText = typeof q.prompt === 'string' ? q.prompt.trim() : ''
  if (promptText) {
    composerInitialText.value = promptText
  }
  if (q.approval === 'open') {
    dismissedApprovalIds.value = new Set()
  }
  if (promptText || q.approval !== undefined) {
    const nextQuery = { ...q }
    delete nextQuery.prompt
    delete nextQuery.approval
    router.replace({ query: nextQuery })
  }
}

// 用户上滚 → 暂停自动滚动；触底 → 恢复（RoundTimeline 新事件遵循同一纪律）
function onScroll() {
  if (!messagesEl.value) return
  const el = messagesEl.value
  const distanceToBottom = el.scrollHeight - el.scrollTop - el.clientHeight
  autoScroll.value = distanceToBottom < 50
}

/** SessionComposer @send（契约 §4）：组件内已清草稿，这里只走发送与滚动。 */
async function onComposerSend(text: string) {
  if (!text || sending.value) return
  sending.value = true
  try {
    await store.sendPrompt(text)
    autoScroll.value = true
    await nextTick()
    scrollToBottom(true)
  } finally {
    sending.value = false
  }
}

// ── P1 实时事件（session.activity / round.completed + 快照追赶） ──
const sessionEvents = useSessionEvents({
  sessionId: () => sessionID.value,
  instanceId: () => instanceID.value,
})

/** 事件不可用（断线/后端未上线）时从消息流降级推导，不白屏。 */
const fallbackPhase = computed(() =>
  deriveFallbackPhase({ streaming: store.isStreaming, messages: store.messages }),
)

const barPhase = computed(() => {
  if (sessionEvents.eventsAvailable.value && sessionEvents.activity.value) {
    return sessionEvents.activity.value.phase
  }
  return fallbackPhase.value
})

const barLastEventAt = computed(() => {
  if (sessionEvents.eventsAvailable.value && sessionEvents.activity.value) {
    return sessionEvents.activity.value.lastEventAt || null
  }
  const last = store.messages[store.messages.length - 1]
  return last ? last.time : null
})

/**
 * P1.5 头部副标题（信号文本 = 一句话 + 时长，设计 v2 §4.1）：
 * 与 SessionStatusBar 图标共用纯派生；1s tick 驱动时长。
 */
const nowTick = ref(Date.now())
let nowTimer: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  nowTimer = setInterval(() => {
    nowTick.value = Date.now()
  }, 1000)
})
onBeforeUnmount(() => {
  if (nowTimer !== null) clearInterval(nowTimer)
})

const statusSubtitle = computed(() => {
  const base = sessionStatusLabel({
    phase: barPhase.value,
    active: store.isStreaming,
    pendingCount: pendingApprovalCount.value,
  })
  const since =
    pendingApprovalCount.value > 0 && approvalFirstSeenAt.value !== null
      ? approvalFirstSeenAt.value
      : barLastEventAt.value
  const elapsed = formatStatusElapsed(since === null ? null : nowTick.value - since)
  return elapsed ? `${base} · ${elapsed}` : base
})

/** 审批待办（状态条 🔴 态）：ApprovalPanel 的数据源（权限 + 问答）。 */
const pendingApprovalCount = computed(
  () => approvalStore.permissions.length + approvalStore.questions.length,
)

/** 审批等待时长：客户端首见时间近似（P0 近似，与 useInstanceApprovals 同款）。 */
const approvalFirstSeen = ref<Map<string, number>>(new Map())
watch(
  () => [approvalStore.permissions, approvalStore.questions],
  () => {
    const now = Date.now()
    const next = new Map(approvalFirstSeen.value)
    for (const p of approvalStore.permissions) {
      if (!next.has(p.id)) next.set(p.id, now)
    }
    for (const q of approvalStore.questions) {
      if (!next.has(q.id)) next.set(q.id, now)
    }
    const alive = new Set([
      ...approvalStore.permissions.map((p) => p.id),
      ...approvalStore.questions.map((q) => q.id),
    ])
    for (const id of next.keys()) {
      if (!alive.has(id)) next.delete(id)
    }
    approvalFirstSeen.value = next
  },
  { deep: true },
)

const approvalFirstSeenAt = computed(() => {
  if (pendingApprovalCount.value === 0 || approvalFirstSeen.value.size === 0) return null
  return Math.min(...approvalFirstSeen.value.values())
})

/** 状态条 [查看]：重置已忽略记录让 Bottom Sheet 弹起；无 Sheet 时聚焦内联面板。 */
async function viewApprovals() {
  dismissedApprovalIds.value = new Set()
  if (!approvalSheetEnabled) {
    await nextTick()
    const el = approvalPanelEl.value?.$el as HTMLElement | undefined
    el?.scrollIntoView?.({ behavior: 'smooth', block: 'end' })
  }
}

// ── 详情抽屉（旧 SessionDetailView 统计/导出收敛，§4.3-3） ──
const detailVisible = ref(false)

/** 统计优先走 round.completed 事件累计；事件不可用降级为消息流 diff 推导。 */
const sessionStats = computed<SessionStats>(() => {
  if (sessionEvents.eventsAvailable.value && sessionEvents.roundsByIndex.value.size > 0) {
    return statsFromRounds(
      [...sessionEvents.roundsByIndex.value.values()],
      store.messages.length,
    )
  }
  return statsFromMessages(store.messages)
})

const drawerRounds = computed(() =>
  groupMessagesIntoRounds(store.messages).map((g) => ({
    index: g.index,
    data: sessionEvents.roundsByIndex.value.get(g.index) ?? null,
    fallbackSummary: roundSummaryFallback(g),
  })),
)

// ── Pending approvals（08 §3.3 / §4.5：服务端确认前不显示已批准） ──
const approvalSheetEnabled = useFeatureFlag('approval.bottom_sheet_v1')
const serverConfirmRequired = useFeatureFlag('approval.server_confirm_required')
const {
  pendingPermissions,
  loadError: approvalsError,
  refresh: refreshApprovals,
  reply: replyApproval,
  startPolling: startApprovalPolling,
  stopPolling: stopApprovalPolling,
} = usePendingApprovals({
  instanceId: () => instanceID.value,
  sessionId: () => sessionID.value,
})

/** 用户手动关闭过、且尚未做出决定的请求不再重复弹出。 */
const dismissedApprovalIds = ref<Set<string>>(new Set())
const approvalSubmitting = ref(false)
const approvalServerConfirmed = ref<boolean | null>(null)

const currentApproval = computed(
  () => pendingPermissions.value.find((p) => !dismissedApprovalIds.value.has(p.id)) ?? null,
)
const approvalSheetVisible = computed(() => approvalSheetEnabled && currentApproval.value !== null)
const approvalSheetModel = computed({
  get: () => approvalSheetVisible.value,
  set: (v: boolean) => {
    if (!v) dismissCurrentApproval()
  },
})

const approvalAction = computed(() =>
  currentApproval.value ? `调用工具：${currentApproval.value.action}` : '',
)
const approvalSource = computed(() =>
  currentApproval.value
    ? `${selectedInstance.value?.displayName || instanceID.value} · 会话 ${sessionID.value.slice(0, 8)}`
    : '',
)
const approvalScope = computed(() => (currentApproval.value?.resources ?? []).join(' · '))
const approvalDetails = computed(() => {
  const req = currentApproval.value
  if (!req) return ''
  const lines: string[] = []
  if (req.resources?.length) lines.push(`目标资源：\n${req.resources.join('\n')}`)
  if (req.save?.length) lines.push(`持久化范围（始终允许）：\n${req.save.join('\n')}`)
  return lines.join('\n\n')
})

function dismissCurrentApproval(): void {
  const req = currentApproval.value
  if (!req) return
  const next = new Set(dismissedApprovalIds.value)
  next.add(req.id)
  dismissedApprovalIds.value = next
  approvalServerConfirmed.value = null
}

async function onApprovalDecision(decision: ApprovalDecision): Promise<void> {
  const req = currentApproval.value
  if (!req || approvalSubmitting.value) return
  approvalSubmitting.value = true
  approvalServerConfirmed.value = null
  const status = await replyApproval(req.id, decision)
  approvalSubmitting.value = false

  if (status === 'confirmed') {
    approvalServerConfirmed.value = serverConfirmRequired ? true : null
    toast.success('已授权')
    setTimeout(dismissCurrentApproval, 800)
  } else if (status === 'queued-offline') {
    // 离线：已入待发送队列，服务端确认前不显示"已批准"（serverConfirmed=false）。
    approvalServerConfirmed.value = serverConfirmRequired ? false : null
    toast.info('当前离线，决定已保存，联网后自动发送')
    setTimeout(dismissCurrentApproval, 1600)
  } else if (status === 'conflict') {
    toast.error('该审批请求已过期或已在别处处理')
    dismissCurrentApproval()
  } else {
    // 发送失败：保留请求与 Sheet，可重试（08 §3.3）。
    toast.error('审批发送失败，请重试')
  }
}

async function stop() {
  await store.interrupt()
  toast.info('已停止，点击状态图标可继续')
}

/**
 * P1.5 动态状态图标·空闲态单击 = 继续（信号即入口）：
 * 走与 Composer @send 相同的发送路径（滚动跟随 + sending 防抖）。
 */
async function continueSession() {
  if (sending.value) return
  sending.value = true
  try {
    await store.sendPrompt('继续')
    autoScroll.value = true
    await nextTick()
    scrollToBottom(true)
  } finally {
    sending.value = false
  }
}

// 自动跟随流式输出（RoundTimeline 内部消息变化同样驱动此 watch）
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
  <div class="session-view" :class="{ embedded: props.embedded }">
    <!-- P1.5 头部收敛：[退出] [动态状态图标] 标题+信号副标题 [⋮]
         （原 top-bar + SessionStatusBar 两行合一；壳层顶栏由 hideAppHeader 修复隐藏；
         实例名等非实时信息收进 ⋮ 抽屉，不常驻副标题） -->
    <header class="top-bar">
      <!-- 嵌入双栏时底导隐藏，关闭按钮是详情态的唯一退出路径（08 §2.2）。 -->
      <button v-if="props.embedded" class="back-btn" @click="emit('close')" aria-label="关闭会话详情">
        <span class="material-symbols-outlined">close</span>
      </button>
      <button v-if="!props.embedded" class="back-btn" @click="goBack" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>

      <!-- 动态状态图标（信号即界面 §2.2）：审批呼吸 / 运行旋转（单击停止）/
           空闲播放（单击继续） -->
      <SessionStatusBar
        :phase="barPhase"
        :last-event-at="barLastEventAt"
        :active="store.isStreaming"
        :pending-count="pendingApprovalCount"
        :approval-first-seen-at="approvalFirstSeenAt"
        @stop="stop"
        @continue="continueSession"
        @view-approvals="viewApprovals"
      />

      <div class="title-block">
        <div class="title">{{ sessionTitle }}</div>
        <div class="subtitle">{{ statusSubtitle }}</div>
      </div>

      <button
        class="back-btn detail-btn"
        aria-label="更多会话信息"
        @click="detailVisible = true"
      >
        <span class="material-symbols-outlined">more_vert</span>
      </button>
    </header>

    <!-- Messages（轮次时间线，§4.3-2） -->
    <main ref="messagesEl" class="messages" @scroll="onScroll">
      <div v-if="store.messages.length === 0" class="empty">
        <div class="empty-icon">💬</div>
        <p class="empty-text">开始一个新的对话</p>
        <p class="empty-hint">在下方输入框输入你的问题或任务</p>
      </div>

      <RoundTimeline
        v-else
        :messages="store.messages"
        :rounds="sessionEvents.roundsByIndex.value"
      />

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

    <!-- 审批复核状态（拉取失败可重试，不打断会话） -->
    <div v-if="approvalSheetEnabled && approvalsError !== ''" class="approval-error" role="alert">
      <span>审批状态拉取失败</span>
      <button type="button" @click="refreshApprovals">重试</button>
    </div>

    <!-- 权限审批 Bottom Sheet（feature flag 暗Launch，08 §3.3） -->
    <ApprovalBottomSheet
      v-model:visible="approvalSheetModel"
      :action="approvalAction"
      :source="approvalSource"
      :scope="approvalScope"
      :details="approvalDetails"
      :submitting="approvalSubmitting"
      :server-confirmed="approvalServerConfirmed"
      @decision="onApprovalDecision"
    />

    <!-- P1.5 详情抽屉（⋮ 收纳：实例信息 + 统计 + 轮摘要 + 导出） -->
    <SessionDetailDrawer
      v-model:visible="detailVisible"
      :session-id="sessionID"
      :session-title="sessionTitle"
      :instance-name="selectedInstance?.displayName || ''"
      :instance-id="instanceID"
      :stats="sessionStats"
      :rounds="drawerRounds"
    />

    <!-- Human-in-the-loop 审批面板（权限/问答） -->
    <ApprovalPanel ref="approvalPanelEl" :instance-id="instanceID" :session-id="sessionID" />

    <!-- Input（SessionComposer，契约 §4 固定目标模式；@send 走 store.sendPrompt） -->
    <SessionComposer
      :session-id="sessionID"
      :session-label="sessionTitle"
      :disabled="sending"
      :initial-text="composerInitialText"
      @send="onComposerSend"
    />
  </div>
</template>

<style scoped>
.session-view {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--bg-base);
  /* 安全区唯一来源是 body 的 padding-top（styles.css）；此处再加会双重下移
     （真机实测标题距顶 90px，正确值 39px，P1.5+ 排查）。 */
}

.session-view.embedded {
  height: 100%;
  min-height: 0;
  border-left: 1px solid var(--border);
}

.session-view.embedded .top-bar {
  position: sticky;
  top: 0;
  z-index: var(--z-base);
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
.top-spacer {
  flex: 0 0 auto;
  width: 44px; /* P1.5：触摸热区 ≥44px（原 32px 偏差修正） */
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-full);
  background: transparent;
  border: none;
  cursor: pointer;
  color: var(--text-primary);
}
.back-btn:active {
  background: var(--bg-subtle);
}
.detail-btn {
  color: var(--text-secondary);
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
/* 信号副标题（状态 · 时长）：与左侧动态图标同一数据源 */
.subtitle {
  font-size: var(--text-xs);
  color: var(--text-secondary);
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-top: 2px;
  font-variant-numeric: tabular-nums;
}

/* Messages */
.messages {
  flex: 1 1 auto;
  min-height: 0;
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

/* Scroll-to-bottom button */
.scroll-bottom-btn {
  position: sticky;
  bottom: 8px;
  margin-left: auto;
  margin-right: var(--space-1);
  width: 44px;
  height: 44px;
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
  background: var(--danger-bg);
  color: var(--danger);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  text-align: center;
  border-top: 1px solid rgba(239, 68, 68, 0.2);
}

/* 审批复核失败提示（页面内 + 重试，08 §6） */
.approval-error {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-1-5, 6px) var(--space-3);
  background: var(--bg-subtle);
  color: var(--warning, #f59e0b);
  font-size: var(--text-xs);
}
.approval-error button {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
  color: var(--brand-primary);
  padding: 2px 10px;
  font-size: var(--text-xs);
  /* 触摸目标高度向 48px 靠拢（08 §5）。 */
  min-height: 32px;
}

/* Input（SessionComposer 自带样式，此处仅保留图标字体声明） */
.material-symbols-outlined {
  font-family: 'Material Symbols Outlined', 'Material Icons';
  font-weight: normal;
  font-style: normal;
  font-size: 20px;
  line-height: 1;
}
</style>

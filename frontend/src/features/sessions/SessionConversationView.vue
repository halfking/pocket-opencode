<script setup lang="ts">
/**
 * SessionConversationView — 主题任务 / 会话实时对话视图（P1 会话工作台）
 *
 * 路由：/sessions/:id?instance_id=xxx&title=xxx
 *
 * P1 改造（设计方案 v2 §4.3）+ P1.5 界面减负 + 2026-09-08 会话详情改版：
 *  - 头部收敛（P1.5）：[退出] [动态状态图标] 标题+信号副标题 [⋮]；
 *    壳层顶栏由路由 hideAppHeader 契约修复隐藏；
 *  - 输入系统（09-08 改版）：页面默认**不显示输入框**，右下角 FAB 唤起
 *    「会话操作 + 消息输入」浮动卡片（SessionComposer）；面板打开后随消息
 *    滚动 1:1 下移隐藏 / 下滑唤出（与 AI 列表页同款引擎；因路由 bottomNav:false，
 *    AppLayout 全局 chrome 不启用，此处自建 createScrollHideChrome 私有实例）；
 *  - 左缘提示词索引条（RoundIndexRail）：每根条 = 一轮用户提示词，
 *    条高 ∝ 提示词字数，点按 / 滑动 scrub 快速定位；
 *  - 右缘会话总结条（SessionSummaryRail）：浅黄纸面色条，点击展开
 *    会话统计 + 各轮摘要（可跳转）；
 *  - 轮次时间线（RoundTimeline）：事件流按轮折叠；详情抽屉（⋮）收纳
 *    实例信息 + 统计 + 导出；
 *  - 输入区快捷指令：primary 4 条为输入框工具行最左侧 44×44 方形按钮，
 *    其余收进「更多指令」面板（详见 SessionComposer）。
 *
 * 保留：ApprovalPanel / ApprovalBottomSheet（含服务端确认语义）、SSE 流式渲染、
 * 离线审批入队、自动滚底（用户上滚暂停）。
 */
import { onMounted, onBeforeUnmount, ref, nextTick, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSessionStore } from '../../stores/session'
import { useApprovalStore } from '../../stores/approval'
import { useToast } from '../../composables/useToast'
import { useElapsedNow } from '../../composables/useElapsedNow'
import { useFeatureFlag } from '../../config/featureFlags'
import { readSelectedInstance } from '../../config/selected-instance'
import { usePendingApprovals } from '../../composables/usePendingApprovals'
import { createScrollHideChrome, bindScrollHideChrome } from '../../composables/useScrollHideChrome'
import { ApprovalBottomSheet, type ApprovalDecision } from '../../components'
import ApprovalPanel from './ApprovalPanel.vue'
import SessionStatusBar from './SessionStatusBar.vue'
import RoundTimeline from './RoundTimeline.vue'
import RoundIndexRail from './RoundIndexRail.vue'
import SessionSummaryRail from './SessionSummaryRail.vue'
import SessionDetailDrawer from './SessionDetailDrawer.vue'
import SessionComposer from './SessionComposer.vue'
import SessionLiveRecordPanel from './SessionLiveRecordPanel.vue'
import { useSessionLiveRecord } from './useSessionLiveRecord'
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
const instanceID = computed(() => props.instanceId || (route.query.instance_id as string) || readSelectedInstance()?.id || '')
const initialTitle = computed(() => props.title || (route.query.title as string) || '')

const sending = ref(false)
const messagesEl = ref<HTMLElement | null>(null)
const autoScroll = ref(true)
const approvalPanelEl = ref<InstanceType<typeof ApprovalPanel> | null>(null)

/** ?prompt= 深链一次性预填（传入 SessionComposer 的 initialText）。 */
const composerInitialText = ref('')

const selectedInstance = computed(() => readSelectedInstance())

// ── 输入面板（2026-09-08 改版）：默认收起为右下 FAB，唤起后随滚动隐藏/唤出 ──
// 路由 bottomNav:false → 全局 chrome 引擎（AppLayout）不启用；本视图自建
// 同款引擎实例（与 AI 列表页同一交互规格：跟手 1:1 + 吸附 + 快甩），
// 隐藏距离 = 输入面板实高（textarea 自增高经 ResizeObserver 跟随）。
const composerOpen = ref(false)
const dockEl = ref<HTMLElement | null>(null)
const dockInset = ref(0)
const localChrome = createScrollHideChrome(() => dockInset.value)
const dockOffset = localChrome.hiddenOffset
const dockSnapping = localChrome.snapping
const dockFullyHidden = localChrome.hidden
const fabVisible = computed(() => !composerOpen.value || dockFullyHidden.value)

function openComposer() {
  composerOpen.value = true
  // FAB → 面板：从底部唤出（覆盖可能的滚动隐藏态）
  void nextTick(() => localChrome.reveal())
}

function collapseComposer() {
  composerOpen.value = false
  localChrome.reset()
}

// 聚焦输入框期间钉住展示（键盘在场时面板不能被滚走，与壳层 onContentFocusIn 同纪律）
function onDockFocusIn() {
  localChrome.setPinned(true)
}
function onDockFocusOut() {
  localChrome.setPinned(false)
}

// 面板挂/卸载时维护实高上报（隐藏吸附后负 margin 让位的依据）
let dockRO: ResizeObserver | null = null
watch(composerOpen, async (open) => {
  dockRO?.disconnect()
  dockRO = null
  if (!open) {
    dockInset.value = 0
    return
  }
  await nextTick()
  const el = dockEl.value
  if (!el) return
  dockInset.value = el.offsetHeight
  dockRO = new ResizeObserver(() => {
    dockInset.value = dockEl.value?.offsetHeight ?? 0
  })
  dockRO.observe(el)
})

const sessionTitle = computed(() => {
  if (store.title) return store.title
  if (initialTitle.value) return initialTitle.value
  // 用 ID 截断作为 fallback
  return sessionID.value.slice(0, 8)
})

const liveRecord = useSessionLiveRecord(() => sessionID.value, () => sessionTitle.value)

onMounted(async () => {
  if (!instanceID.value) {
    // 没有 instance — 回到实例选择
    router.replace('/instances')
    return
  }
  // Deep Link 参数（指挥中心/本地通知进入，设计方案 v2 §4.2-3/§4.2-5）：
  //   ?prompt=xxx     → 预填输入草稿，可编辑再发送（转写/指令不直发）；并唤起输入面板
  //   ?approval=open  → 清除"已忽略"记录，强制弹出审批 Bottom Sheet
  applyDeepLinkQuery()
  await store.open(sessionID.value, instanceID.value, initialTitle.value)
  await nextTick()
  scrollToBottom(true)
  // 滚动联动：输入面板隐藏/唤出引擎绑定到消息滚动容器（与 AI 列表页同款）
  if (messagesEl.value) {
    unbindLocalChrome = bindScrollHideChrome(messagesEl.value, localChrome)
  }
  // 审批 Bottom Sheet（feature flag 暗Launch）：进入会话即查一次 pending 并轮询。
  if (approvalSheetEnabled) startApprovalPolling()
  // P1：session.activity / round.completed 事件订阅 + 快照追赶（§4.3-1/2）
  sessionEvents.startLive()
})

let unbindLocalChrome: (() => void) | null = null

onBeforeUnmount(() => {
  stopApprovalPolling()
  sessionEvents.stopLive()
  unbindLocalChrome?.()
  dockRO?.disconnect()
  store.close()
})

async function scrollToBottom(force = false) {
  if (!autoScroll.value && !force) return
  await nextTick()
  // 程序化滚动：抑制本地引擎上报，避免流式跟滚被误判为用户上滑而隐藏输入面板
  localChrome.suppress()
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
    // 深链预填意图就是输入：唤起输入面板（默认收起态看不到草稿）
    composerOpen.value = true
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

// ── 左缘提示词索引（RoundIndexRail 数据 + 当前轮跟踪 + 定位） ──
const promptRounds = computed(() =>
  groupMessagesIntoRounds(store.messages).map((g) => ({
    index: g.index,
    text: g.messages.find((m) => m.role === 'user')?.text ?? '',
  })),
)

const activeRoundIndex = ref(1)

/** 视口上沿 1/3 处所在轮 = 当前轮（getBoundingClientRect 相对容器，避免 offsetParent 歧义）。 */
function updateActiveRound() {
  const el = messagesEl.value
  if (!el) return
  const sections = el.querySelectorAll<HTMLElement>('[data-round-index]')
  if (sections.length === 0) {
    activeRoundIndex.value = 1
    return
  }
  const probeY = el.getBoundingClientRect().top + el.clientHeight * 0.33
  let current = Number(sections[0].dataset.roundIndex) || 1
  for (const section of sections) {
    if (section.getBoundingClientRect().top > probeY) break
    current = Number(section.dataset.roundIndex) || current
  }
  activeRoundIndex.value = current
}

/** 索引条 / 总结面板点击：滚动定位到该轮（程序化滚动抑制引擎上报）。 */
function seekRound(index: number) {
  const el = messagesEl.value
  if (!el) return
  const section = el.querySelector<HTMLElement>(`[data-round-index="${index}"]`)
  if (!section) return
  autoScroll.value = false
  // smooth 滚动全程可能超 400ms：加长抑制窗口，避免定位滚动被误判为用户上滑
  localChrome.suppress(1200)
  const delta = section.getBoundingClientRect().top - el.getBoundingClientRect().top
  el.scrollTop += delta - 8
  activeRoundIndex.value = index
}

// 用户上滚 → 暂停自动滚动；触底 → 恢复（RoundTimeline 新事件遵循同一纪律）
function onScroll() {
  if (!messagesEl.value) return
  const el = messagesEl.value
  const distanceToBottom = el.scrollHeight - el.scrollTop - el.clientHeight
  autoScroll.value = distanceToBottom < 50
  updateActiveRound()
}

/** SessionComposer @send（契约 §4）：组件内已清草稿，这里只走发送与滚动。 */
async function onComposerSend(text: string) {
  if (!text || sending.value) return
  sending.value = true
  try {
    await store.sendPrompt(text)
    autoScroll.value = true
    localChrome.reveal()
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
 * 与 SessionStatusBar 图标共用纯派生；nowTick 由 useElapsedNow 自适应节拍
 * 驱动（ISSUES #20：时长文本只有前 60s 是秒级，之后分钟粒度；空闲会话
 * 完全不启定时器，消灭"每秒重绘头部"的周期性重渲染源）。
 */
const nowTick = useElapsedNow(() => [
  pendingApprovalCount.value > 0 ? approvalFirstSeenAt.value : null,
  barLastEventAt.value,
])

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
    localChrome.reveal()
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

    <!-- 内容区：左缘提示词索引条 | 消息流（轮次时间线） | 右缘会话总结条。
         rails 贴窗口边缘、与内容区等高（body-row 拉伸）。 -->
    <div class="body-row">
      <RoundIndexRail
        v-if="promptRounds.length > 0"
        :rounds="promptRounds"
        :active-index="activeRoundIndex"
        @seek="seekRound"
      />

      <!-- Messages（轮次时间线，§4.3-2） -->
      <main ref="messagesEl" class="messages" @scroll="onScroll">
        <div v-if="store.messages.length === 0" class="empty">
          <div class="empty-icon">💬</div>
          <p class="empty-text">开始一个新的对话</p>
          <p class="empty-hint">点击右下角按钮输入你的问题或任务</p>
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

      <SessionSummaryRail
        v-if="store.messages.length > 0"
        :stats="sessionStats"
        :rounds="drawerRounds"
        @seek="seekRound"
      />
    </div>

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

    <!-- 输入面板 dock（SessionComposer，契约 §4；默认收起为 FAB，
         打开后随滚动 1:1 下移隐藏 / 下滑唤出——与 AI 列表页同款引擎） -->
    <SessionLiveRecordPanel
      v-if="liveRecord.active.value"
      :recorder="liveRecord.recorder"
      :summary="liveRecord.summary"
      @stop="liveRecord.toggle"
    />
    <footer
      v-if="composerOpen"
      ref="dockEl"
      class="composer-dock"
      :class="{ snapping: dockSnapping, 'dock-hidden': dockFullyHidden }"
      :style="{ transform: `translate3d(0, ${dockOffset}px, 0)`, '--dock-inset': `${dockInset}px` }"
      :inert="dockFullyHidden"
      @focusin="onDockFocusIn"
      @focusout="onDockFocusOut"
    >
      <SessionComposer
        :session-id="sessionID"
        :session-label="sessionTitle"
        :disabled="sending"
        :initial-text="composerInitialText"
        :live-recording="liveRecord.recorder.isRecording.value"
        @send="onComposerSend"
        @live-record="liveRecord.toggle"
        @collapse="collapseComposer"
      />
    </footer>

    <!-- 右下浮动按钮：唤起「会话操作 + 消息输入」面板（面板收起或被滚动隐藏时显示） -->
    <Transition name="fab">
      <button
        v-if="fabVisible"
        type="button"
        class="composer-fab"
        aria-label="打开会话操作与消息输入"
        @click="openComposer"
      >
        <span class="material-symbols-outlined" aria-hidden="true">edit</span>
      </button>
    </Transition>
  </div>
</template>

<style scoped>
.session-view {
  position: relative; /* 右下 FAB 的定位基准 */
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

/* 内容区行：左缘提示词索引条 | 消息流 | 右缘会话总结条。
   rails 是 body-row 的首/末子元素 → 贴窗口边缘且与内容区等高。 */
.body-row {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
}

/* Messages */
.messages {
  flex: 1 1 auto;
  min-height: 0;
  min-width: 0;
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

/* ── 输入面板 dock：浮动卡片外边距 + 滚动联动位移。
   跟手阶段纯 transform 不动布局；吸附落定为全隐后负 margin 把槽位
   让给消息区（与 AIChatView .composer 同一套机制，引擎为本视图私有实例）。 */
.composer-dock {
  flex: 0 0 auto;
  margin: var(--space-2) var(--space-2) 0;
  padding-bottom: calc(var(--space-2) + var(--app-safe-bottom));
  will-change: transform;
}
.composer-dock.dock-hidden {
  margin-bottom: calc(-1 * var(--dock-inset, 0px));
}
.composer-dock.snapping {
  transition:
    transform var(--duration-chrome) var(--ease-chrome),
    margin-bottom var(--duration-chrome) var(--ease-chrome);
}

/* ── 右下 FAB：唤起输入面板（方形圆角，与 qc-btn 同一图标语言） ── */
.composer-fab {
  position: absolute;
  right: var(--space-4);
  bottom: calc(var(--app-safe-bottom) + var(--space-4));
  z-index: var(--z-fab, 60);
  width: 56px;
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 16px;
  background: var(--brand-gradient, var(--brand-primary));
  color: var(--text-inverse);
  cursor: pointer;
  box-shadow: var(--shadow-lg);
}
.composer-fab:active {
  transform: scale(0.92);
}
.composer-fab .material-symbols-outlined {
  font-size: 26px;
}
.fab-enter-active,
.fab-leave-active {
  transition:
    opacity var(--duration-fast) var(--ease-out),
    transform var(--duration-fast) var(--ease-out);
}
.fab-enter-from,
.fab-leave-to {
  opacity: 0;
  transform: scale(0.6) translateY(8px);
}
</style>

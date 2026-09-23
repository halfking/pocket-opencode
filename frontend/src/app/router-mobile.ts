import { createRouter, createWebHashHistory } from 'vue-router'

// ---- 主包瘦身（原生顺滑度审计 A3/P1 #7）----
// 仅保留首屏关键视图为静态 import：/ai（TasksView，默认入口）、/login（冷启动
// 未登录落点）、/servers（首启未配服务器落点）。其余 21 个视图全部路由级懒加载，
// 主包从 ~900KB 降到首屏所需（flashcards/pkm/marketplace 等本已懒加载）。

// 登录页（未登录冷启动落点）
import LoginView from '../features/auth/LoginView.vue'

// 服务器选择页（首启未配服务器落点）
import ServerSelectView from '../features/servers/ServerSelectView.vue'

// AI 工具控制默认入口（复用任务视图；/ai 为首屏）
import TasksView from '../features/tasks/TasksView.vue'

// 其余视图全部懒加载：仅在进入对应路由才下载
const InstanceListView = () => import('../features/instances/InstanceListView.vue')
const TaskDetailView = () => import('../features/tasks/TaskDetailView.vue')
const SessionWorkspaceView = () => import('../features/sessions/SessionWorkspaceView.vue')
const SettingsView = () => import('../features/settings/SettingsView.vue')

// ---- 新增个人助理模块（骨架） ----
const NoteListView = () => import('../features/notes/NoteListView.vue')
const NoteDetailView = () => import('../features/notes/NoteDetailView.vue')
const NoteEditView = () => import('../features/notes/NoteEditView.vue')
const EmailInboxView = () => import('../features/email/EmailInboxView.vue')
const EmailDetailView = () => import('../features/email/EmailDetailView.vue')
const EmailSummaryView = () => import('../features/email/EmailSummaryView.vue')
const EmailAccountSetup = () => import('../features/email/EmailAccountSetup.vue')
const EmailSettingsView = () => import('../features/email/EmailSettingsView.vue')
const EmailInvoiceListView = () => import('../features/email/InvoiceListView.vue')
const FinanceView = () => import('../features/finance/FinanceView.vue')
const VaultListView = () => import('../features/vault/VaultListView.vue')
const VaultEntryView = () => import('../features/vault/VaultEntryView.vue')
const ScheduledTaskListView = () => import('../features/scheduled-tasks/ScheduledTaskListView.vue')
const ScheduledTaskDetailView = () => import('../features/scheduled-tasks/ScheduledTaskDetailView.vue')
const ScheduledTaskEditView = () => import('../features/scheduled-tasks/ScheduledTaskEditView.vue')

// 通知中心(2026-09-20 通知体系 P1):inbox 列表 + 已读管理。
const NotificationsView = () => import('../features/notifications/NotificationsView.vue')

// Flashcards v1（契约 §2 + §4）：FSRS 驱动的间隔重复学习
// 路由级懒加载：仅在进入 /flashcards 才下载，减少首屏 JS 体积。
const FlashcardListView = () => import('../features/flashcards/FlashcardListView.vue')
const FlashcardDeckView = () => import('../features/flashcards/FlashcardDeckView.vue')
const FlashcardReviewView = () => import('../features/flashcards/FlashcardReviewView.vue')
const FlashcardEditView = () => import('../features/flashcards/FlashcardEditView.vue')

// S1.1 PKM 记事本（TipTap WYSIWYG + 双向链接，基于 S0-C assetStore）
// 路由级懒加载：TipTap ~200KB 只在进入 /pkm 时才下载，保持首屏精简。
const PkmTodayView = () => import('../features/pkm/PkmTodayView.vue')
const PkmNoteView = () => import('../features/pkm/PkmNoteView.vue')

// Phase 4: 移动分布式 AI 工作平台 · 市场三大入口（skill / agent / workflow）。
// 全部懒加载，避免首屏 JS 体积膨胀。
const SkillMarketView = () => import('../features/marketplace/SkillMarketView.vue')
const AgentMarketView = () => import('../features/marketplace/AgentMarketView.vue')
const WorkbuddyView = () => import('../features/marketplace/WorkbuddyView.vue')

// 🦞 守卫所需：登录态 + 龙虾初始化态
// PR4: 守卫逻辑已抽取到 ./routeGuards.ts；本文件保留路由表，避免在
// 创建 router 之前 import pinia/native 引发的副作用。

// 手机端内置本地智能体(pi 语义移植:skills+experts+tools,WebView 内循环)
const LocalAgentView = () => import('../features/local-agent/LocalAgentView.vue')

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/',
      redirect: '/ai'
    },
    // 个人助理 — AI 工具控制入口（任务聚合看板，复用 TasksView）
    {
      path: '/ai',
      name: 'ai',
      component: TasksView,
      meta: { requiresAuth: true, title: 'AI 工具', bottomNav: true, scrollMode: 'self' }
    },
    // 个人助理 — AI 对话（豆包式：多轮会话 / 模型选择 / 流式 / 对比优化）
    {
      path: '/ai-chat',
      name: 'ai-chat',
      component: () => import('../features/ai-chat/AIChatView.vue'),
      meta: { requiresAuth: true, title: '对话', bottomNav: true, scrollMode: 'self' }
    },
    // AI 对话 — 智能体角色库
    {
      path: '/agents',
      name: 'agent-library',
      component: () => import('../features/agents/AgentLibraryView.vue'),
      meta: { requiresAuth: true, title: '智能体', bottomNav: false, canGoBack: true }
    },
    // 本地智能体:手机端内置(pi 语义移植,WebView 内 agent 循环 + 技能/专家/工具)
    {
      path: '/local-agent',
      name: 'local-agent',
      component: LocalAgentView,
      meta: { requiresAuth: true, title: '本地智能体', bottomNav: false, canGoBack: true }
    },
    {
      path: '/agents/new',
      name: 'agent-new',
      component: () => import('../features/agents/AgentEditView.vue'),
      meta: { requiresAuth: true, title: '创建角色', bottomNav: false, canGoBack: true }
    },
    {
      path: '/agents/:agentId/edit',
      name: 'agent-edit',
      component: () => import('../features/agents/AgentEditView.vue'),
      meta: { requiresAuth: true, title: '编辑角色', bottomNav: false, canGoBack: true }
    },
    {
      path: '/agents/:agentId',
      name: 'agent-detail',
      component: () => import('../features/agents/AgentDetailView.vue'),
      meta: { requiresAuth: true, title: '角色详情', bottomNav: false, canGoBack: true }
    },
    // 个人助理 — 语音笔记
    {
      path: '/notes',
      name: 'notes',
      component: NoteListView,
      meta: { requiresAuth: true, requiresLobster: true, title: '笔记', bottomNav: true }
    },
    // 个人助理 — 新建笔记
    {
      path: '/notes/new',
      name: 'note-new',
      component: NoteEditView,
      meta: { requiresAuth: true, requiresLobster: true, title: '新建笔记', bottomNav: false, canGoBack: true }
    },
    // 个人助理 — 笔记详情
    {
      path: '/notes/:id',
      name: 'note-detail',
      component: NoteDetailView,
      meta: { requiresAuth: true, requiresLobster: true, title: '笔记详情', bottomNav: true, canGoBack: true }
    },
    // 个人助理 — 编辑笔记（/notes/:id/edit，id === 'new' 也走这里表示新建）
    {
      path: '/notes/:id/edit',
      name: 'note-edit',
      component: NoteEditView,
      meta: { requiresAuth: true, requiresLobster: true, title: '编辑笔记', bottomNav: false, canGoBack: true }
    },
    // 个人助理 — 邮箱助手
    {
      path: '/email',
      name: 'email',
      component: EmailInboxView,
      meta: { requiresAuth: true, requiresLobster: true, title: '邮箱', bottomNav: true }
    },
    // 邮箱 — 设置（账户/过滤策略/处理逻辑；须在 /email/:id 之前声明避免被吞）
    {
      path: '/email/settings',
      name: 'email-settings',
      component: EmailSettingsView,
      meta: { requiresAuth: true, requiresLobster: true, title: '邮箱设置', canGoBack: true, bottomNav: false, hideAppHeader: true, scrollMode: 'self' }
    },
    // 邮箱 — 发票自动整理（同样须在 /email/:id 之前声明）
    {
      path: '/email/invoices',
      name: 'email-invoices',
      component: EmailInvoiceListView,
      meta: { requiresAuth: true, requiresLobster: true, title: '发票整理', canGoBack: true, bottomNav: false }
    },
    // 记账（手动 + 笔记自动入账）
    {
      path: '/finance',
      name: 'finance',
      component: FinanceView,
      meta: { requiresAuth: true, title: '记账', canGoBack: true, bottomNav: false }
    },
    // 邮箱 — 账户路由必须在 /email/:id 之前，否则 accounts 会被当成邮件 id。
    {
      path: '/email/cleanup',
      name: 'email-cleanup',
      component: () => import('../features/email/EmailSpamCleanupView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '清理垃圾邮件', canGoBack: true, bottomNav: false, hideAppHeader: true, scrollMode: 'self' }
    },
    {
      path: '/email/accounts/new',
      name: 'email-account-add',
      component: () => import('../features/email/EmailAccountAddView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '新增邮箱账户', canGoBack: true, bottomNav: false, hideAppHeader: true, scrollMode: 'self' }
    },
    {
      path: '/email/accounts',
      name: 'email-accounts',
      component: EmailAccountSetup,
      meta: { requiresAuth: true, requiresLobster: true, title: '邮箱账户', canGoBack: true, bottomNav: false }
    },
    // 邮箱 — 每日摘要须在 /email/:id 之前，否则 summary 会被当成邮件 id。
    {
      path: '/email/summary',
      name: 'email-summary',
      component: EmailSummaryView,
      meta: { requiresAuth: true, requiresLobster: true, title: '每日摘要', canGoBack: true, bottomNav: false }
    },
    {
      path: '/email/summary/:date',
      name: 'email-summary-detail',
      component: EmailSummaryView,
      meta: { requiresAuth: true, requiresLobster: true, title: '摘要详情', canGoBack: true, bottomNav: false }
    },
    // 邮箱 — 邮件详情
    {
      path: '/email/:id',
      name: 'email-detail',
      component: EmailDetailView,
      meta: { requiresAuth: true, requiresLobster: true, title: '邮件详情', canGoBack: true, bottomNav: false }
    },
    // S2.3 联系人：从邮件/会议来源聚合的本地联系人
    {
      path: '/contacts',
      name: 'contacts',
      component: () => import('../features/contact/ContactListView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '联系人', bottomNav: false, canGoBack: true },
    },
    {
      path: '/contacts/:id',
      name: 'contact-detail',
      component: () => import('../features/contact/ContactDetailView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '联系人详情', bottomNav: false, canGoBack: true },
    },

    // 个人助理 — 密码箱
    {
      path: '/vault',
      name: 'vault',
      component: VaultListView,
      meta: { requiresAuth: true, requiresLobster: true, title: '密码箱', bottomNav: true }
    },
    // 密码箱 — 条目详情
    {
      path: '/vault/:id',
      name: 'vault-entry',
      component: VaultEntryView,
      meta: { requiresAuth: true, requiresLobster: true, title: '密码详情', canGoBack: true, bottomNav: false }
    },
    // 密码箱 — 编辑条目
    {
      path: '/vault/:id/edit',
      name: 'vault-entry-edit',
      component: VaultEntryView,
      meta: { requiresAuth: true, requiresLobster: true, title: '编辑密码', canGoBack: true, bottomNav: false }
    },
    // 个人助理 — PKM 记事本 Today 入口（双向链接 + Daily Note）
    {
      path: '/pkm/today',
      name: 'pkm-today',
      component: PkmTodayView,
      meta: { requiresAuth: true, requiresLobster: true, title: '笔记', bottomNav: true }
    },
    // PKM — 笔记编辑/新建（:id === 'new' 表示新建）
    {
      path: '/pkm/n/:id',
      name: 'pkm-note',
      component: PkmNoteView,
      meta: { requiresAuth: true, requiresLobster: true, title: '笔记', bottomNav: false, canGoBack: true }
    },
    // 2026-09-23 Phase 2：「学习」tab 聚合页 —— 合并 Flashcards + 笔记入口。
    // 详情仍走独立路由（/flashcards、/notes），深链与历史收藏不受影响。
    {
      path: '/study',
      name: 'study',
      component: () => import('../features/study/StudyHubView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '学习', bottomNav: true, scrollMode: 'self' }
    },
    // S2.2 会议记录：录音 → 转写 → AI 纪要 → Note/Task 沉淀
    {
      path: '/meetings',
      name: 'meetings',
      component: () => import('../features/meetings/MeetingListView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '会议', bottomNav: true, scrollMode: 'self' },
    },
    {
      path: '/meetings/new',
      name: 'meeting-new',
      component: () => import('../features/meetings/MeetingRecordView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '开始会议', bottomNav: false, canGoBack: true, scrollMode: 'self' },
    },
    {
      path: '/meetings/:id/record',
      name: 'meeting-record',
      redirect: (to) => ({
        name: 'meeting-detail',
        params: { id: to.params.id },
        query: { ...to.query, record: '1' },
      }),
    },
    {
      path: '/meetings/:id',
      name: 'meeting-detail',
      component: () => import('../features/meetings/MeetingDetailView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '会议详情', bottomNav: false, canGoBack: true, scrollMode: 'self' },
    },
    // RSS 订阅：源管理 + 信息流 + 详情 + 一键分享
    {
      path: '/rss',
      name: 'rss',
      component: () => import('../features/rss/RssListView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: 'RSS 订阅', bottomNav: true, scrollMode: 'self' },
    },
    {
      path: '/rss/add',
      name: 'rss-add',
      component: () => import('../features/rss/RssAddSource.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '添加订阅', bottomNav: false, canGoBack: true, scrollMode: 'self' },
    },
    {
      path: '/rss/items/:id',
      name: 'rss-item',
      component: () => import('../features/rss/RssItemDetail.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '条目详情', bottomNav: false, canGoBack: true, scrollMode: 'self' },
    },
    {
      path: '/login',
      name: 'login',
      component: LoginView,
      meta: { title: '登录', bottomNav: false, showTopBar: false }
    },
    {
      path: '/forgot-password',
      name: 'forgot-password',
      component: () => import('../features/auth/ForgotPasswordView.vue'),
      meta: { title: '忘记密码', bottomNav: false, showTopBar: false, canGoBack: true }
    },
    {
      path: '/register',
      name: 'register',
      component: () => import('../features/auth/RegisterView.vue'),
      meta: { title: '注册账号', bottomNav: false, showTopBar: false, canGoBack: true }
    },
    {
      path: '/auth/sso/callback',
      name: 'sso-callback',
      // Phase 1: RedClaw SSO 回调落点。浏览器从 IdP 跳到 openpocket 的
      // /api/auth/sso/callback，后端消费绑定 cookie 并换到平台 JWT 后，
      // 302 到本 SPA path（sso_code=... 或 error=...）；本页用 code 调
      // /api/auth/sso/exchange 换 token 后落 store，token 不走 URL。
      component: () => import('../features/auth/SsoCallbackView.vue'),
      meta: { title: 'SSO 登录中', bottomNav: false, showTopBar: false }
    },
    {
      path: '/servers',
      name: 'servers',
      component: ServerSelectView,
      meta: { title: '后端服务器', canGoBack: true, bottomNav: false, menu: false }
    },
    {
      // 2026-09-23 TabBar 4+1 重组 (Phase 1)：「更多」tab 聚合页。
      // 9 宫格主功能 + 设置与运维分组（替代原 SettingsMenuDrawer 入口）。
      path: '/more',
      name: 'more',
      component: () => import('../features/more/MoreHubView.vue'),
      meta: { requiresAuth: true, title: '更多', bottomNav: true, scrollMode: 'self' }
    },
    {
      path: '/instances',
      name: 'instances',
      component: InstanceListView,
      meta: { requiresAuth: true, title: '实例', bottomNav: true }
    },
    {
      path: '/tasks',
      name: 'tasks',
      component: TasksView,
      meta: { requiresAuth: true, title: '任务', bottomNav: true }
    },
    {
      path: '/tasks/:id',
      name: 'task-detail',
      component: TaskDetailView,
      meta: { requiresAuth: true, title: '任务详情', bottomNav: true, canGoBack: true }
    },
    {
      path: '/sessions',
      name: 'sessions',
      component: SessionWorkspaceView,
      meta: { requiresAuth: true, title: '会话', bottomNav: true, scrollMode: 'split' }
    },
    {
      // 通知中心(2026-09-20 通知体系 P1):铃铛入口/系统通知点击均落此页。
      path: '/notifications',
      name: 'notifications',
      component: NotificationsView,
      meta: { requiresAuth: true, title: '通知中心', bottomNav: false, canGoBack: true }
    },
    {
      // Phase V3: 实时会话对话视图（P1 会话工作台：状态条 + 轮次时间线 + 详情抽屉）
      path: '/sessions/:id',
      name: 'session-conversation',
      component: () => import('../features/sessions/SessionConversationView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '会话', bottomNav: false, canGoBack: true, hideAppHeader: true, scrollMode: 'self' }
    },
    {
      // P1 旧详情页收敛（设计方案 v2 §4.3-3）：features/opencode/SessionDetailView
      // 的旧路由 301 到会话工作台，保留 :id 与 query（instance_id/title 等）。
      // 统计/导出能力已迁入工作台的 SessionDetailDrawer；旧入口链
      // （opencode/SessionListView 的 router.push）随之收敛。
      path: '/opencode/sessions/:id',
      name: 'opencode-session-detail-legacy',
      redirect: (to) => ({ path: `/sessions/${to.params.id}`, query: to.query }),
    },
    {
      path: '/settings',
      name: 'settings',
      component: SettingsView,
      // menu:false — 设置页自身就在抽屉菜单的目标里，隐藏 ≡ 避免自引用冗余
      meta: { requiresAuth: true, title: '设置', bottomNav: true, menu: false, scrollMode: 'shell' }
    },
    {
      // Phase 5: LLM Gateway 配置编辑
      path: '/settings/llm-gateway',
      name: 'settings-llm-gateway',
      component: () => import('../features/settings/SettingsLLMGateway.vue'),
      meta: { requiresAuth: true, title: 'AI 模型', bottomNav: false, canGoBack: true, hideAppHeader: true, scrollMode: 'self' }
    },
    {
      // 系统权限与隐私：麦克风 / 通知 / 生物识别状态与申请入口（Android 优先）
      path: '/settings/permissions',
      name: 'settings-permissions',
      component: () => import('../features/settings/SettingsPermissionsView.vue'),
      meta: { requiresAuth: true, title: '权限与隐私', bottomNav: false, canGoBack: true, hideAppHeader: true }
    },
    // ---- Phase 4: 移动分布式 AI 工作平台 · 市场三大入口 ----
    {
      path: '/marketplace/skills',
      name: 'marketplace-skills',
      component: SkillMarketView,
      meta: { requiresAuth: true, title: '技能市场', bottomNav: false, canGoBack: true, hideAppHeader: true },
    },
    {
      path: '/marketplace/agents',
      name: 'marketplace-agents',
      component: AgentMarketView,
      meta: { requiresAuth: true, title: '智能体市场', bottomNav: false, canGoBack: true, hideAppHeader: true },
    },
    {
      path: '/marketplace/workbuddies',
      name: 'marketplace-workbuddies',
      component: WorkbuddyView,
      meta: { requiresAuth: true, title: '工作搭子', bottomNav: false, canGoBack: true, hideAppHeader: true },
    },
    {
      path: '/settings/scheduled-tasks',
      name: 'scheduled-tasks',
      component: ScheduledTaskListView,
      meta: { requiresAuth: true, title: '定时任务', bottomNav: false, canGoBack: true, hideAppHeader: true }
    },
    {
      path: '/settings/scheduled-tasks/new',
      name: 'scheduled-task-new',
      component: ScheduledTaskEditView,
      meta: { requiresAuth: true, title: '创建定时任务', bottomNav: false, canGoBack: true, hideAppHeader: true }
    },
    {
      path: '/settings/scheduled-tasks/:id/edit',
      name: 'scheduled-task-edit',
      component: ScheduledTaskEditView,
      meta: { requiresAuth: true, title: '编辑定时任务', bottomNav: false, canGoBack: true, hideAppHeader: true }
    },
    {
      path: '/settings/scheduled-tasks/:id',
      name: 'scheduled-task-detail',
      component: ScheduledTaskDetailView,
      meta: { requiresAuth: true, title: '定时任务详情', bottomNav: false, canGoBack: true, hideAppHeader: true }
    },
    // ---- Flashcards v1（FSRS 间隔重复）----
    // 路由顺序：list → new → notes/:noteId/edit → decks/:deckId → review
    // 这样动态段（:deckId/:noteId）不会被静态前缀吞掉。
    {
      path: '/flashcards',
      name: 'flashcards',
      component: FlashcardListView,
      meta: { requiresAuth: true, title: '闪卡', bottomNav: false, canGoBack: true, hideAppHeader: true }
    },
    {
      path: '/flashcards/new',
      name: 'flashcard-new',
      component: FlashcardEditView,
      meta: { requiresAuth: true, title: '新建卡片', bottomNav: false, canGoBack: true, hideAppHeader: true }
    },
    {
      path: '/flashcards/notes/:noteId/edit',
      name: 'flashcard-note-edit',
      component: FlashcardEditView,
      meta: { requiresAuth: true, title: '编辑卡片', bottomNav: false, canGoBack: true, hideAppHeader: true }
    },
    {
      path: '/flashcards/decks/:deckId',
      name: 'flashcard-deck',
      component: FlashcardDeckView,
      meta: { requiresAuth: true, title: '卡组', bottomNav: false, canGoBack: true, hideAppHeader: true }
    },
    {
      path: '/flashcards/decks/:deckId/review',
      name: 'flashcard-review',
      component: FlashcardReviewView,
      meta: { requiresAuth: true, title: '复习', bottomNav: false, canGoBack: true, hideAppHeader: true, scrollMode: 'self' }
    },
    {
      // Phase 4：牌组配置（Anki deck options 对齐）
      path: '/flashcards/decks/:deckId/options',
      name: 'flashcard-deck-options',
      component: () => import('../features/flashcards/DeckOptionsView.vue'),
      meta: { requiresAuth: true, title: '卡组设置', bottomNav: false, canGoBack: true, hideAppHeader: true }
    },
    // P3 — 成本与配额只读面板
    {
      path: '/cost',
      name: 'cost',
      component: () => import('../features/cost/CostQuotaView.vue'),
      meta: { requiresAuth: true, title: '成本与配额', bottomNav: false, canGoBack: true }
    },

    // ---- 网关运维控制面（llm-gateway-go 运行状态）----
    // 只要 requiresAuth：这些页面读的是网关运行状态，不碰本地加密库，
    // 所以不加 requiresLobster —— 否则主密码未解锁就看不了监控。
    {
      path: '/gateway',
      name: 'gateway-nodes',
      component: () => import('../features/gateway/GatewayNodeListView.vue'),
      meta: { requiresAuth: true, title: '网关节点', bottomNav: false, canGoBack: true }
    },
    {
      path: '/gateway/:nodeId',
      name: 'gateway-overview',
      component: () => import('../features/gateway/GatewayOverviewView.vue'),
      meta: { requiresAuth: true, title: '网关概览', bottomNav: false, canGoBack: true }
    },
    {
      path: '/gateway/:nodeId/providers',
      name: 'gateway-providers',
      component: () => import('../features/gateway/GatewayProvidersView.vue'),
      meta: { requiresAuth: true, title: '供应商', bottomNav: false, canGoBack: true }
    },
    {
      path: '/gateway/:nodeId/credentials',
      name: 'gateway-credentials',
      component: () => import('../features/gateway/GatewayCredentialsView.vue'),
      meta: { requiresAuth: true, title: '凭据', bottomNav: false, canGoBack: true }
    },
    {
      path: '/gateway/:nodeId/credentials/:credentialId',
      name: 'gateway-credential-detail',
      component: () => import('../features/gateway/GatewayCredentialDetailView.vue'),
      meta: { requiresAuth: true, title: '凭据详情', bottomNav: false, canGoBack: true }
    },
    {
      path: '/gateway/:nodeId/models',
      name: 'gateway-models',
      component: () => import('../features/gateway/GatewayModelsView.vue'),
      meta: { requiresAuth: true, title: '模型路由', bottomNav: false, canGoBack: true }
    },
    {
      // 可用模型目录（按家族/版本，含模态）：App 内「按模态默认模型」的数据源
      path: '/gateway/:nodeId/catalog',
      name: 'gateway-catalog',
      component: () => import('../features/gateway/GatewayAvailableModelsView.vue'),
      meta: { requiresAuth: true, title: '模型目录', bottomNav: false, canGoBack: true }
    },
    {
      // 路由配置：任务类型（任务识别）+ 默认路由 + 策略/精选模型
      path: '/gateway/:nodeId/routing-config',
      name: 'gateway-routing-config',
      component: () => import('../features/gateway/GatewayRoutingConfigView.vue'),
      meta: { requiresAuth: true, title: '路由配置', bottomNav: false, canGoBack: true }
    },
    {
      path: '/gateway/:nodeId/live',
      name: 'gateway-live',
      component: () => import('../features/gateway/GatewayLiveStreamView.vue'),
      meta: { requiresAuth: true, title: '实时请求', bottomNav: false, canGoBack: true }
    }
  ]
})

/**
 * Router Guard:
 *   1. 已登录访问 /login → 重定向到首页
 *   2. 需要登录的页面 → 未登录跳 /login
 *   3. 需要龙虾硬壳的页面：已登录但 Lobster 未就绪 → 跳 /login?unlock=1
 *
 * Phase 7:
 *   - Added syncFromStorage() to ensure auth state is current on each navigation
 *   - Fixed: Remove forced redirect to /login when Lobster not ready
 *   - Rationale: Lobster initialization may fail (native plugin issues), but user
 *     should still be able to navigate. Pages requiring Lobster will show appropriate
 *     error messages or fallback UI instead of forcing re-login.
 *
 * PR4 (optimization v4 / E1-S1):
 *   - Split guard into helper module (`./routeGuards.ts`) so the four
 *     outcomes (allow / login / unlock / block) are testable in isolation.
 *   - Replace `redirect` query with `returnTo` and preserve open-redirect
 *     safety by validating the path prefix.
 *   - Persist the last successful route under `pocket:lastRoute` for
 *     diagnostic / restore flows.
 */
import { runGuard } from './routeGuards'
import { beforeRouteTransition } from './routeTransition'

/**
 * "首页栈" 标记：用户在 BottomNav 根 tab 上点击进入子页面后，
 * sessionStorage 标记 pocket:navigatedFromHome = '1'，AppLayout.goBack
 * 据此决定 router.back()（回到首页根）vs router.push('/ai')（兜底）。
 * 直接通过 router.push 进入非首页也置位（避免 entry 空页）。
 */
const HOME_ROOTS = new Set(['/ai', '/tasks', '/ai-chat', '/notes', '/meetings', '/email', '/vault', '/pkm/today', '/instances', '/sessions', '/settings'])

router.beforeEach((to, from, next) => {
  if (typeof sessionStorage !== 'undefined') {
    if (HOME_ROOTS.has(to.path)) {
      sessionStorage.removeItem('pocket:navigatedFromHome')
    } else {
      sessionStorage.setItem('pocket:navigatedFromHome', '1')
    }
  }
  // 路由转场方向判定 + 滚动位置记忆（原生顺滑度审计 P0 #1 / P1 #9）；
  // 必须在守卫阶段同步写入——App.vue 渲染 <Transition> 时就要读方向。
  beforeRouteTransition(to.path, from.path, from.matched.length)
  runGuard(to, next)
})
export default router
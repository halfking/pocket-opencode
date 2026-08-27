import { createRouter, createWebHashHistory } from 'vue-router'

// 登录页
import LoginView from '../features/auth/LoginView.vue'

// 服务器选择页
import ServerSelectView from '../features/servers/ServerSelectView.vue'

// OpenCode 实例列表页
import InstanceListView from '../features/instances/InstanceListView.vue'

// 任务列表页（按分组）
import TasksView from '../features/tasks/TasksView.vue'

// 任务详情页
import TaskDetailView from '../features/tasks/TaskDetailView.vue'

// 会话列表页
import SessionWorkspaceView from '../features/sessions/SessionWorkspaceView.vue'

// 设置页
import SettingsView from '../features/settings/SettingsView.vue'

// ---- 新增个人助理模块（骨架） ----
// AI 工具控制默认入口（复用现有任务视图，可后续替换为聚合看板）
import NoteListView from '../features/notes/NoteListView.vue'
import NoteDetailView from '../features/notes/NoteDetailView.vue'
import NoteEditView from '../features/notes/NoteEditView.vue'
import EmailInboxView from '../features/email/EmailInboxView.vue'
import EmailDetailView from '../features/email/EmailDetailView.vue'
import EmailSummaryView from '../features/email/EmailSummaryView.vue'
import EmailAccountSetup from '../features/email/EmailAccountSetup.vue'
import VaultListView from '../features/vault/VaultListView.vue'
import VaultEntryView from '../features/vault/VaultEntryView.vue'
import MeetingListView from '../features/meetings/MeetingListView.vue'
import MeetingRecordView from '../features/meetings/MeetingRecordView.vue'
import MeetingDetailView from '../features/meetings/MeetingDetailView.vue'

// S1.1 PKM 记事本（TipTap WYSIWYG + 双向链接，基于 S0-C assetStore）
// 路由级懒加载：TipTap ~200KB 只在进入 /pkm 时才下载，保持首屏精简。
const PkmTodayView = () => import('../features/pkm/PkmTodayView.vue')
const PkmNoteView = () => import('../features/pkm/PkmNoteView.vue')

// 🦞 守卫所需：登录态 + 龙虾初始化态
// PR4: 守卫逻辑已抽取到 ./routeGuards.ts；本文件保留路由表，避免在
// 创建 router 之前 import pinia/native 引发的副作用。

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
      meta: { requiresAuth: true, title: 'AI 工具', bottomNav: true }
    },
    // 个人助理 — AI 对话（豆包式：多轮会话 / 模型选择 / 流式 / 对比优化）
    {
      path: '/ai-chat',
      name: 'ai-chat',
      component: () => import('../features/ai-chat/AIChatView.vue'),
      meta: { requiresAuth: true, title: '对话', bottomNav: true }
    },
    // AI 对话 — 智能体角色库
    {
      path: '/agents',
      name: 'agent-library',
      component: () => import('../features/agents/AgentLibraryView.vue'),
      meta: { requiresAuth: true, title: '智能体', bottomNav: false, canGoBack: true }
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
    // 邮箱 — 邮件详情
    {
      path: '/email/:id',
      name: 'email-detail',
      component: EmailDetailView,
      meta: { requiresAuth: true, requiresLobster: true, title: '邮件详情', canGoBack: true, bottomNav: false }
    },
    // 邮箱 — 每日摘要（列表 + 按日期详情，由组件内判断）
    {
      path: '/email/summary',
      name: 'email-summary',
      component: EmailSummaryView,
      meta: { requiresAuth: true, requiresLobster: true, title: '每日摘要', canGoBack: true }
    },
    {
      path: '/email/summary/:date',
      name: 'email-summary-detail',
      component: EmailSummaryView,
      meta: { requiresAuth: true, requiresLobster: true, title: '摘要详情', canGoBack: true, bottomNav: false }
    },
    // 邮箱 — 账户配置
    {
      path: '/email/accounts',
      name: 'email-accounts',
      component: EmailAccountSetup,
      meta: { requiresAuth: true, requiresLobster: true, title: '邮箱账户', canGoBack: true }
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
    // S2.2 会议记录：录音 → 转写 → AI 纪要 → Note/Task 沉淀
    {
      path: '/meetings',
      name: 'meetings',
      component: () => import('../features/meetings/MeetingListView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '会议', bottomNav: true },
    },
    {
      path: '/meetings/new',
      name: 'meeting-new',
      component: () => import('../features/meetings/MeetingRecordView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '开始会议', bottomNav: false, canGoBack: true },
    },
    {
      path: '/meetings/:id',
      name: 'meeting-detail',
      component: () => import('../features/meetings/MeetingDetailView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '会议详情', bottomNav: false, canGoBack: true },
    },
    {
      path: '/login',
      name: 'login',
      component: LoginView
    },
    {
      path: '/servers',
      name: 'servers',
      component: ServerSelectView,
      meta: { requiresAuth: true }
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
      meta: { requiresAuth: true, title: '会话', bottomNav: true }
    },
    {
      // Phase V3: 实时会话对话视图（P1 会话工作台：状态条 + 轮次时间线 + 详情抽屉）
      path: '/sessions/:id',
      name: 'session-conversation',
      component: () => import('../features/sessions/SessionConversationView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '会话', bottomNav: false, canGoBack: true, hideAppHeader: true }
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
      meta: { requiresAuth: true, title: '设置', bottomNav: true }
    },
    {
      // Phase 5: LLM Gateway 配置编辑
      path: '/settings/llm-gateway',
      name: 'settings-llm-gateway',
      component: () => import('../features/settings/SettingsLLMGateway.vue'),
meta: { requiresAuth: true, title: 'AI 模型', bottomNav: false, canGoBack: true, hideAppHeader: true }
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

router.beforeEach((to, from, next) => {
  // Phase 7: Auth sync still happens inside evaluateRoute via the
  // helper, so we delegate the whole decision tree.
  runGuard(to, next)
})

export default router
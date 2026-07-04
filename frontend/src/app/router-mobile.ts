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
import SessionListView from '../features/sessions/SessionListView.vue'

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
import ComingSoonView from '../features/common/ComingSoonView.vue'

// 🦞 守卫所需：登录态 + 龙虾初始化态
import { useAuthStore } from '../stores/auth'
import { isLobsterReady } from '../native/lobster-init'

/**
 * 判断某路由是否需要"龙虾硬壳已初始化"。
 * 笔记 / 邮箱 / 密码箱 / 会议记录这类本地存储相关页面都需要。
 */
function needsLobster(to: { path: string; meta: { requiresLobster?: boolean } }): boolean {
  if (to.meta.requiresLobster) return true
  // 兼容子路由（detail / edit 继承父级 lobster 需求）
  if (to.path.startsWith('/notes') || to.path.startsWith('/email') || to.path.startsWith('/vault') || to.path.startsWith('/meetings')) {
    return true
  }
  return false
}

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
    // 个人助理 — 会议记录（Phase 6A，占位避免死链）
    {
      path: '/meetings',
      name: 'meetings',
      component: ComingSoonView,
      props: { icon: '🎙️', title: '会议记录', desc: '录音转写、声纹识别、会议纪要生成。', phase: 'Phase 6A 开发中' },
      meta: { requiresAuth: true, requiresLobster: true, title: '会议', bottomNav: true },
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
      component: SessionListView,
      meta: { requiresAuth: true, title: '会话', bottomNav: true }
    },
    {
      // Phase V3: 实时会话对话视图
      path: '/sessions/:id',
      name: 'session-conversation',
      component: () => import('../features/sessions/SessionConversationView.vue'),
      meta: { requiresAuth: true, requiresLobster: true, title: '会话', bottomNav: false, canGoBack: true }
    },
    {
      path: '/settings',
      name: 'settings',
      component: SettingsView,
      meta: { requiresAuth: true, title: '设置', bottomNav: true }
    }
  ]
})

/**
 * 🦞 路由守卫：
 *   1. 已登录 + 已初始化但访问 /login → 跳 /ai，避免重复登录
 *   2. 需要登录的页面 → 未登录跳 /login
 *   3. 需要龙虾硬壳已初始化的页面（笔记/邮箱/密码箱/会议等本地存储相关）
 *      → 未初始化跳 /login（龙虾初始化由 LoginView 在用户输入主密码后触发）
 */
router.beforeEach((to, from, next) => {
  const auth = useAuthStore()

  // 1) 已登录 + 已初始化但访问 /login → 直接去首页
  if (to.path === '/login' && auth.isAuthenticated && isLobsterReady()) {
    return next('/ai')
  }

  // 2) 需要登录但未登录
  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return next('/login')
  }

  // 3) 需要龙虾初始化但未初始化（/notes /email /vault /meetings 等）
  if (needsLobster(to) && !isLobsterReady()) {
    return next('/login')
  }

  next()
})

export default router
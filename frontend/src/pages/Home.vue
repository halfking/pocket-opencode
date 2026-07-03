<template>
  <div class="home-page">
    <!-- 顶部欢迎区域 -->
    <header class="home-header">
      <div class="header-content">
        <div class="greeting">
          <h1>{{ greeting }}</h1>
          <p class="subtitle">{{ subtitle }}</p>
        </div>
        <div class="header-actions">
          <Button variant="ghost" size="small" @click="handleSettings">
            ⚙️
          </Button>
        </div>
      </div>
    </header>

    <!-- 下拉刷新容器 -->
    <PullToRefresh :on-refresh="handleRefresh">
      <div class="home-content">
        <!-- 快速操作区 -->
        <section class="quick-actions">
          <h2 class="section-title">快速操作</h2>
          <div class="actions-grid">
            <div class="action-item" @click="navigateTo('/notes/new')">
              <div class="action-icon">📝</div>
              <span class="action-label">新建笔记</span>
            </div>
            <div class="action-item" @click="navigateTo('/email')">
              <div class="action-icon">📧</div>
              <span class="action-label">查看邮箱</span>
            </div>
            <div class="action-item" @click="navigateTo('/ai')">
              <div class="action-icon">🤖</div>
              <span class="action-label">AI 助手</span>
            </div>
            <div class="action-item" @click="navigateTo('/vault')">
              <div class="action-icon">🔐</div>
              <span class="action-label">密码箱</span>
            </div>
          </div>
        </section>

        <!-- 最近会话区 -->
        <section class="recent-sessions">
          <div class="section-header">
            <h2 class="section-title">最近会话</h2>
            <Button variant="ghost" size="small" @click="navigateTo('/sessions')">
              查看全部
            </Button>
          </div>
          
          <div class="sessions-list">
            <InfiniteScroll
              ref="sessionsScrollRef"
              :on-load="loadMoreSessions"
              :distance="50"
              loading-text="加载会话..."
              no-more-text="没有更多会话"
            >
              <SessionCard
                v-for="session in sessions"
                :key="session.id"
                :session="session"
                @click="handleSessionClick"
              />
            </InfiniteScroll>
          </div>
        </section>

        <!-- 最近笔记区 -->
        <section class="recent-notes">
          <div class="section-header">
            <h2 class="section-title">最近笔记</h2>
            <Button variant="ghost" size="small" @click="navigateTo('/notes')">
              查看全部
            </Button>
          </div>
          
          <div class="notes-list">
            <NoteCard
              v-for="note in recentNotes"
              :key="note.id"
              :note="note"
              @click="handleNoteClick"
            />
          </div>
        </section>

        <!-- 最近邮件区 -->
        <section class="recent-emails">
          <div class="section-header">
            <h2 class="section-title">未读邮件</h2>
            <Button variant="ghost" size="small" @click="navigateTo('/email')">
              查看全部
            </Button>
          </div>
          
          <div class="emails-list">
            <EmailCard
              v-for="email in unreadEmails"
              :key="email.id"
              :email="email"
              @click="handleEmailClick"
            />
          </div>
        </section>
      </div>
    </PullToRefresh>

    <!-- 底部导航 -->
    <BottomNav
      :items="navItems"
      :active="activeNav"
      @change="handleNavChange"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import {
  Button,
  BottomNav,
  PullToRefresh,
  InfiniteScroll,
  SessionCard,
  NoteCard,
  EmailCard,
} from '@/components'
import type { Session } from '@/components'
import type { Note } from '@/components'
import type { Email } from '@/components'

const router = useRouter()

// 状态
const activeNav = ref('home')
const sessions = ref<Session[]>([])
const recentNotes = ref<Note[]>([])
const unreadEmails = ref<Email[]>([])
const sessionsScrollRef = ref()

// 会话分页
let sessionPage = 0
const SESSION_PAGE_SIZE = 10

// 计算属性
const greeting = computed(() => {
  const hour = new Date().getHours()
  if (hour < 6) return '夜深了'
  if (hour < 12) return '早上好'
  if (hour < 14) return '中午好'
  if (hour < 18) return '下午好'
  return '晚上好'
})

const subtitle = computed(() => {
  const date = new Date()
  const options: Intl.DateTimeFormatOptions = { 
    weekday: 'long', 
    month: 'long', 
    day: 'numeric' 
  }
  return date.toLocaleDateString('zh-CN', options)
})

// 底部导航项
const navItems = [
  { id: 'home', icon: '🏠', label: '主页' },
  { id: 'notes', icon: '📝', label: '笔记' },
  { id: 'ai', icon: '🤖', label: 'AI' },
  { id: 'email', icon: '📧', label: '邮箱' },
  { id: 'more', icon: '⋮', label: '更多' },
]

// 模拟数据
const mockSessions: Session[] = [
  {
    id: '1',
    title: 'AI 助手对话',
    lastMessage: '我可以帮你总结这次会议的重点内容',
    lastMessageTime: Date.now() - 1800000,
    unreadCount: 3,
    avatar: '🤖',
    type: 'ai',
  },
  {
    id: '2',
    title: '项目会议纪要',
    lastMessage: '会议记录已保存，需要我帮你整理吗？',
    lastMessageTime: Date.now() - 3600000,
    unreadCount: 0,
    avatar: '🎤',
    type: 'meeting',
  },
  {
    id: '3',
    title: '读书笔记整理',
    lastMessage: '已为你提取了第三章的关键观点',
    lastMessageTime: Date.now() - 7200000,
    unreadCount: 1,
    avatar: '📚',
    type: 'note',
  },
]

const mockNotes: Note[] = [
  {
    id: '1',
    title: '项目会议纪要',
    content: '今天讨论了项目进度和下一步计划，需要在本周完成架构设计...',
    domain: 'work',
    tags: ['会议', '项目'],
    audio_path: '/audio/123.wav',
    created_at: Date.now() - 3600000,
    updated_at: Date.now() - 3600000,
  },
  {
    id: '2',
    title: '读书笔记：原子习惯',
    content: '习惯是自我提升的复利。每天改善 1%，一年后你会好 37 倍...',
    domain: 'personal',
    tags: ['读书', '习惯'],
    audio_path: '/audio/456.wav',
    created_at: Date.now() - 86400000,
    updated_at: Date.now() - 86400000,
  },
]

const mockEmails: Email[] = [
  {
    id: '1',
    subject: '关于项目进度的讨论',
    snippet: '你好，我想和你讨论一下项目的当前进度和遇到的一些问题...',
    from_name: '张三',
    from_address: 'zhangsan@example.com',
    date: Date.now() - 7200000,
    is_read: false,
    is_starred: true,
    category: 'primary',
    importance: 'high',
    has_attachments: true,
  },
  {
    id: '2',
    subject: '会议邀请：周五团队同步',
    snippet: '请参加本周五下午 3 点的团队同步会议...',
    from_name: '李四',
    from_address: 'lisi@example.com',
    date: Date.now() - 14400000,
    is_read: false,
    is_starred: false,
    category: 'primary',
    importance: 'normal',
    has_attachments: false,
  },
]

// 方法
const navigateTo = (path: string) => {
  router.push(path)
}

const handleSettings = () => {
  router.push('/settings')
}

const handleRefresh = async () => {
  console.log('下拉刷新')
  await new Promise(resolve => setTimeout(resolve, 1500))
  // 重置数据
  sessionPage = 0
  sessions.value = []
  loadMoreSessions()
  loadMockData()
}

const loadMoreSessions = async () => {
  console.log('加载更多会话', sessionPage)
  await new Promise(resolve => setTimeout(resolve, 800))
  
  // 模拟加载更多会话
  const newSessions = mockSessions.map((session, index) => ({
    ...session,
    id: `session-${sessionPage * SESSION_PAGE_SIZE + index}`,
    lastMessageTime: session.lastMessageTime - sessionPage * 3600000,
  }))
  
  sessions.value.push(...newSessions)
  sessionPage++
  
  // 模拟：加载 3 次后没有更多数据
  if (sessionPage >= 3) {
    sessionsScrollRef.value?.setNoMore(true)
  }
}

const loadMockData = () => {
  recentNotes.value = mockNotes
  unreadEmails.value = mockEmails.filter(email => !email.is_read)
}

const handleSessionClick = (session: Session) => {
  console.log('点击会话:', session.id)
  // 根据会话类型导航到不同页面
  if (session.type === 'ai') {
    router.push('/ai')
  } else if (session.type === 'meeting') {
    router.push('/meetings')
  } else if (session.type === 'note') {
    router.push('/notes')
  }
}

const handleNoteClick = (note: Note) => {
  console.log('点击笔记:', note.id)
  router.push(`/notes/${note.id}`)
}

const handleEmailClick = (email: Email) => {
  console.log('点击邮件:', email.id)
  router.push(`/email/${email.id}`)
}

const handleNavChange = (id: string) => {
  activeNav.value = id
  if (id === 'home') {
    // 已经在主页
  } else if (id === 'notes') {
    router.push('/notes')
  } else if (id === 'ai') {
    router.push('/ai')
  } else if (id === 'email') {
    router.push('/email')
  } else if (id === 'more') {
    // 显示更多菜单
    console.log('显示更多菜单')
  }
}

// 初始化
onMounted(() => {
  loadMockData()
  loadMoreSessions()
})
</script>

<style scoped>
.home-page {
  min-height: 100vh;
  background: var(--color-bg-base);
  padding-bottom: 80px; /* 为底部导航留空间 */
}

.home-header {
  background: var(--gradient-primary);
  color: white;
  padding: var(--space-6) var(--space-4);
}

.header-content {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
}

.greeting h1 {
  margin: 0 0 var(--space-1) 0;
  font-size: 24px;
  font-weight: var(--font-weight-bold);
}

.subtitle {
  margin: 0;
  opacity: 0.9;
  font-size: 14px;
}

.header-actions {
  display: flex;
  gap: var(--space-2);
}

.home-content {
  padding: var(--space-4);
}

.section-title {
  font-size: 18px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin: 0 0 var(--space-4) 0;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: var(--space-4);
}

.section-header .section-title {
  margin: 0;
}

/* 快速操作区 */
.quick-actions {
  margin-bottom: var(--space-8);
}

.actions-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--space-4);
}

.action-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-4);
  background: var(--color-bg-elevated);
  border-radius: var(--radius-lg);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.action-item:hover {
  transform: translateY(-2px);
  box-shadow: var(--shadow-md);
}

.action-item:active {
  transform: scale(0.95);
}

.action-icon {
  font-size: 24px;
}

.action-label {
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  color: var(--color-text-secondary);
  text-align: center;
}

/* 会话列表 */
.recent-sessions {
  margin-bottom: var(--space-8);
}

.sessions-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

/* 笔记列表 */
.recent-notes {
  margin-bottom: var(--space-8);
}

.notes-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

/* 邮件列表 */
.recent-emails {
  margin-bottom: var(--space-8);
}

.emails-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
</style>

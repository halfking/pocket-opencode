<template>
  <div class="home-page">
    <DualScreenLayout
      ref="layoutRef"
      mode="horizontal"
      secondary-title="AI 助手"
      :default-ratio="0.65"
    >
      <!-- 主屏 -->
      <template #main>
        <div class="home-main">
          <!-- 搜索栏 -->
          <div class="home-header">
            <div class="greeting">
              <h2>{{ greeting }}</h2>
              <p>{{ currentDate }}</p>
            </div>
            <Input
              v-model="searchQuery"
              type="search"
              placeholder="搜索笔记、邮件、会议..."
              @input="handleSearch"
            />
          </div>

          <!-- 快捷卡片 -->
          <div class="quick-cards">
            <div class="quick-card" @click="handleQuickAction('note')">
              <span class="quick-icon">📝</span>
              <span class="quick-label">笔记</span>
              <span class="quick-count">{{ stats.notes }}</span>
            </div>
            <div class="quick-card" @click="handleQuickAction('email')">
              <span class="quick-icon">📧</span>
              <span class="quick-label">邮件</span>
              <span class="quick-count badge">{{ stats.unreadEmails }}</span>
            </div>
            <div class="quick-card" @click="handleQuickAction('meeting')">
              <span class="quick-icon">🎤</span>
              <span class="quick-label">会议</span>
              <span class="quick-count">{{ stats.meetings }}</span>
            </div>
          </div>

          <!-- 内容列表 -->
          <PullToRefresh :on-refresh="handleRefresh">
            <InfiniteScroll :on-load="loadMore">
              <div class="content-sections">
                <!-- 最近活动 -->
                <section class="content-section">
                  <div class="section-header">
                    <h3>🔥 最近活动</h3>
                    <button class="view-all">查看全部</button>
                  </div>
                  <div class="card-list">
                    <CompactCard
                      v-for="item in recentItems"
                      :key="item.id"
                      :icon="item.icon"
                      :title="item.title"
                      :time="item.time"
                      :preview="item.preview"
                      :tags="item.tags"
                      :action-icon="item.starred ? '⭐' : '☆'"
                      @click="handleItemClick(item)"
                      @action="toggleStar(item)"
                    >
                      <div class="item-detail">
                        <p>{{ item.content }}</p>
                        <div class="item-actions">
                          <Button size="small" variant="ghost">编辑</Button>
                          <Button size="small" variant="ghost">分享</Button>
                        </div>
                      </div>
                    </CompactCard>
                  </div>
                </section>

                <!-- 今日任务 -->
                <section class="content-section">
                  <div class="section-header">
                    <h3>✅ 今日任务</h3>
                  </div>
                  <div class="task-list">
                    <div
                      v-for="task in todayTasks"
                      :key="task.id"
                      class="task-item"
                      @click="toggleTask(task)"
                    >
                      <input
                        type="checkbox"
                        :checked="task.completed"
                        class="task-checkbox"
                      />
                      <span :class="['task-text', { completed: task.completed }]">
                        {{ task.text }}
                      </span>
                    </div>
                  </div>
                </section>
              </div>
            </InfiniteScroll>
          </PullToRefresh>
        </div>
      </template>

      <!-- 副屏 -->
      <template #secondary>
        <div class="home-secondary">
          <!-- AI 助手 -->
          <div class="ai-section">
            <div class="ai-avatar">🤖</div>
            <div class="ai-greeting">
              <p>{{ aiGreeting }}</p>
            </div>
            <div class="ai-suggestions">
              <button
                v-for="sug in aiSuggestions"
                :key="sug.id"
                class="ai-suggestion-btn"
                @click="handleAISuggestion(sug)"
              >
                {{ sug.text }}
              </button>
            </div>
          </div>

          <!-- 今日摘要 -->
          <div class="summary-section">
            <h4 class="section-title">📊 今日摘要</h4>
            <div class="summary-stats">
              <div class="stat-item">
                <span class="stat-value">{{ stats.notesCreated }}</span>
                <span class="stat-label">新建笔记</span>
              </div>
              <div class="stat-item">
                <span class="stat-value">{{ stats.meetingHours }}</span>
                <span class="stat-label">会议时长</span>
              </div>
              <div class="stat-item">
                <span class="stat-value">{{ stats.tasksCompleted }}</span>
                <span class="stat-label">完成任务</span>
              </div>
            </div>
          </div>

          <!-- 快捷操作 -->
          <div class="actions-section">
            <h4 class="section-title">⚡ 快捷操作</h4>
            <Button variant="primary" block @click="startVoiceNote">
              🎙️ 语音笔记
            </Button>
            <Button variant="secondary" block @click="startMeeting">
              🎤 开始会议
            </Button>
            <Button variant="secondary" block @click="createTask">
              ✅ 新建任务
            </Button>
          </div>
        </div>
      </template>
    </DualScreenLayout>

    <!-- 语音助手 -->
    <VoiceCommandAssistant
      @command="handleVoiceCommand"
      @transcription="handleTranscription"
    />

    <!-- 底部导航 -->
    <BottomNav
      :items="navItems"
      active="home"
      @change="handleNavChange"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import {
  Button,
  Input,
  BottomNav,
  PullToRefresh,
  InfiniteScroll,
} from '@/components'
import DualScreenLayout from '@/components/interactive/DualScreenLayout.vue'
import VoiceCommandAssistant from '@/components/interactive/VoiceCommandAssistant.vue'
import CompactCard from '@/components/base/CompactCard.vue'
import { useToast } from '@/composables/useToast'
import { wsHub } from '@/services/websocket-hub'

const router = useRouter()
const toast = useToast()

// 状态
const layoutRef = ref()
const searchQuery = ref('')

// 统计数据
const stats = ref({
  notes: 156,
  unreadEmails: 12,
  meetings: 8,
  notesCreated: 3,
  meetingHours: '2.5h',
  tasksCompleted: 5,
})

// 问候语
const greeting = computed(() => {
  const hour = new Date().getHours()
  if (hour < 12) return '早上好 👋'
  if (hour < 18) return '下午好 👋'
  return '晚上好 👋'
})

const currentDate = computed(() => {
  const date = new Date()
  const options: Intl.DateTimeFormatOptions = {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    weekday: 'long'
  }
  return date.toLocaleDateString('zh-CN', options)
})

const aiGreeting = ref('你好！今天有什么可以帮你的吗？')

const aiSuggestions = ref([
  { id: '1', text: '📝 总结今天的会议' },
  { id: '2', text: '📧 查看重要邮件' },
  { id: '3', text: '📊 生成周报' },
])

// 最近活动
const recentItems = ref([
  {
    id: '1',
    icon: '📝',
    title: '项目架构设计讨论',
    time: '30分钟前',
    preview: '团队讨论了新功能的架构方案...',
    content: '详细内容...',
    tags: ['会议', '架构'],
    starred: false,
  },
  {
    id: '2',
    icon: '📧',
    title: '客户反馈邮件',
    time: '1小时前',
    preview: '收到了关于产品体验的反馈...',
    content: '详细内容...',
    tags: ['邮件', '重要'],
    starred: true,
  },
])

// 今日任务
const todayTasks = ref([
  { id: '1', text: '完成架构文档', completed: true },
  { id: '2', text: '回复客户邮件', completed: false },
  { id: '3', text: '准备下周演示', completed: false },
])

// 底部导航
const navItems = [
  { id: 'home', icon: '🏠', label: '主页' },
  { id: 'notes', icon: '🎙️', label: '笔记' },
  { id: 'meetings', icon: '🎤', label: '会议' },
  { id: 'inbox', icon: '📨', label: '邮箱' },
  { id: 'more', icon: '⋮', label: '更多' },
]

// 初始化
onMounted(async () => {
  // 连接 WebSocket
  try {
    // await wsHub.connect('ws://localhost:8080/ws')
    console.log('[HomePage] WebSocket 已连接')
  } catch (error) {
    console.error('[HomePage] WebSocket 连接失败', error)
  }
})

// 交互处理
const handleSearch = () => {
  console.log('搜索:', searchQuery.value)
}

const handleRefresh = async () => {
  await new Promise(resolve => setTimeout(resolve, 1000))
  toast.success('刷新完成')
}

const loadMore = async () => {
  await new Promise(resolve => setTimeout(resolve, 1000))
}

const handleQuickAction = (type: string) => {
  router.push(`/${type}`)
}

const handleItemClick = (item: any) => {
  console.log('点击项目:', item)
}

const toggleStar = (item: any) => {
  item.starred = !item.starred
  toast.info(item.starred ? '已收藏' : '已取消收藏')
}

const toggleTask = (task: any) => {
  task.completed = !task.completed
}

const handleAISuggestion = (sug: any) => {
  toast.info(`执行: ${sug.text}`)
}

const startVoiceNote = () => {
  toast.info('启动语音笔记')
}

const startMeeting = () => {
  router.push('/meetings/new')
}

const createTask = () => {
  toast.info('创建新任务')
}

const handleVoiceCommand = (cmd: string, params?: any) => {
  console.log('[Voice]', cmd, params)
  toast.success(`执行命令: ${cmd}`)
}

const handleTranscription = (text: string) => {
  console.log('[Voice] 转写:', text)
}

const handleNavChange = (id: string) => {
  if (id !== 'home') {
    router.push(`/${id}`)
  }
}
</script>

<style scoped>
.home-page {
  height: 100vh;
  background: var(--color-bg-base);
}

.home-main {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.home-header {
  padding: var(--space-4);
  background: var(--color-bg-surface);
  border-bottom: 1px solid var(--color-border);
}

.greeting h2 {
  margin: 0 0 4px 0;
  font-size: 24px;
  font-weight: var(--font-weight-bold);
  color: var(--color-text-primary);
}

.greeting p {
  margin: 0 0 var(--space-4) 0;
  font-size: 14px;
  color: var(--color-text-secondary);
}

.quick-cards {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-4);
  overflow-x: auto;
}

.quick-card {
  flex-shrink: 0;
  width: 100px;
  padding: var(--space-4);
  background: var(--gradient-primary);
  border-radius: var(--radius-lg);
  color: white;
  text-align: center;
  cursor: pointer;
  transition: all var(--duration-base) var(--ease-out);
}

.quick-card:active {
  transform: scale(0.95);
}

.quick-icon {
  display: block;
  font-size: 32px;
  margin-bottom: var(--space-2);
}

.quick-label {
  display: block;
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  margin-bottom: 4px;
}

.quick-count {
  display: block;
  font-size: 18px;
  font-weight: var(--font-weight-bold);
}

.quick-count.badge {
  display: inline-block;
  padding: 2px 8px;
  background: rgba(255, 255, 255, 0.3);
  border-radius: var(--radius-full);
  font-size: 14px;
}

.content-sections {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-4);
}

.content-section {
  margin-bottom: var(--space-6);
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-3);
}

.section-header h3 {
  margin: 0;
  font-size: 16px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
}

.view-all {
  background: none;
  border: none;
  font-size: 13px;
  color: var(--color-primary);
  cursor: pointer;
}

.card-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.task-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.task-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3);
  background: var(--color-bg-surface);
  border-radius: var(--radius-md);
  cursor: pointer;
}

.task-checkbox {
  width: 20px;
  height: 20px;
  cursor: pointer;
}

.task-text {
  flex: 1;
  font-size: 14px;
  color: var(--color-text-primary);
}

.task-text.completed {
  text-decoration: line-through;
  color: var(--color-text-tertiary);
}

/* 副屏样式 */
.home-secondary {
  display: flex;
  flex-direction: column;
  gap: var(--space-6);
  height: 100%;
  overflow-y: auto;
}

.ai-section {
  text-align: center;
}

.ai-avatar {
  font-size: 48px;
  margin-bottom: var(--space-3);
}

.ai-greeting p {
  margin: 0 0 var(--space-4) 0;
  font-size: 14px;
  color: var(--color-text-secondary);
}

.ai-suggestions {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.ai-suggestion-btn {
  padding: var(--space-3);
  background: var(--color-bg-base);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  font-size: 13px;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.ai-suggestion-btn:hover {
  background: var(--color-primary);
  color: white;
  border-color: var(--color-primary);
}

.section-title {
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-secondary);
  margin: 0 0 var(--space-3) 0;
}

.summary-stats {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: var(--space-3);
}

.stat-item {
  text-align: center;
  padding: var(--space-3);
  background: var(--color-bg-base);
  border-radius: var(--radius-md);
}

.stat-value {
  display: block;
  font-size: 20px;
  font-weight: var(--font-weight-bold);
  color: var(--color-primary);
  margin-bottom: 4px;
}

.stat-label {
  display: block;
  font-size: 11px;
  color: var(--color-text-tertiary);
}

.actions-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.item-detail {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.item-actions {
  display: flex;
  gap: var(--space-2);
}
</style>

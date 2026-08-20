<template>
  <div class="optimized-demo">
    <!-- 双屏布局 -->
    <DualScreenLayout
      ref="layoutRef"
      :mode="layoutMode"
      secondary-title="AI 助手"
      :resizable="true"
    >
      <!-- 主屏：内容列表 -->
      <template #main>
        <div class="main-content">
          <!-- 顶部搜索栏 -->
          <div class="search-bar">
            <Input
              v-model="searchQuery"
              type="search"
              placeholder="搜索笔记、邮件..."
            />
            <button class="layout-toggle" @click="toggleLayoutMode">
              {{ layoutMode === 'horizontal' ? '⬌' : '⬍' }}
            </button>
          </div>

          <!-- 下拉刷新 + 无限滚动列表 -->
          <PullToRefresh :on-refresh="handleRefresh">
            <InfiniteScroll
              ref="scrollRef"
              :on-load="loadMore"
              :distance="50"
            >
              <!-- 分组折叠列表 -->
              <div v-for="group in groupedItems" :key="group.date" class="item-group">
                <div class="group-header" @click="toggleGroup(group.date)">
                  <span class="group-icon">{{ group.expanded ? '▼' : '▶' }}</span>
                  <span class="group-title">{{ group.title }}</span>
                  <span class="group-count">({{ group.items.length }})</span>
                </div>

                <Transition name="group-expand">
                  <div v-if="group.expanded" class="group-items">
                    <CompactCard
                      v-for="item in group.items"
                      :key="item.id"
                      :icon="item.icon"
                      :title="item.title"
                      :time="item.time"
                      :preview="item.preview"
                      :tags="item.tags"
                      :action-icon="item.starred ? '⭐' : '☆'"
                      @click="handleItemClick(item)"
                      @action="handleToggleStar(item)"
                    >
                      <!-- 展开内容 -->
                      <div class="item-detail">
                        <p>{{ item.content }}</p>
                        <div class="item-actions">
                          <Button size="small" variant="ghost">编辑</Button>
                          <Button size="small" variant="ghost">分享</Button>
                          <Button size="small" variant="ghost">删除</Button>
                        </div>
                      </div>
                    </CompactCard>
                  </div>
                </Transition>
              </div>
            </InfiniteScroll>
          </PullToRefresh>
        </div>
      </template>

      <!-- 副屏：AI 助手 + 快捷操作 -->
      <template #secondary>
        <div class="secondary-content">
          <!-- AI 对话区 -->
          <div class="ai-chat">
            <h4 class="section-title">💬 AI 对话</h4>
            <div class="chat-messages">
              <div
                v-for="msg in aiMessages"
                :key="msg.id"
                :class="['chat-message', `chat-message--${msg.role}`]"
              >
                <div class="message-avatar">{{ msg.role === 'user' ? '👤' : '🤖' }}</div>
                <div class="message-content">{{ msg.content }}</div>
              </div>
              <AIThinkingIndicator v-if="aiThinking" />
            </div>
          </div>

          <!-- 智能建议 -->
          <div class="smart-suggestions">
            <h4 class="section-title">💡 智能建议</h4>
            <div class="suggestion-cards">
              <Card
                v-for="suggestion in suggestions"
                :key="suggestion.id"
                variant="outlined"
                hoverable
                clickable
                @click="handleSuggestion(suggestion)"
              >
                <div class="suggestion-item">
                  <span class="suggestion-icon">{{ suggestion.icon }}</span>
                  <span class="suggestion-text">{{ suggestion.text }}</span>
                </div>
              </Card>
            </div>
          </div>

          <!-- 快捷操作 -->
          <div class="quick-actions">
            <h4 class="section-title">⚡ 快捷操作</h4>
            <div class="action-buttons">
              <Button variant="primary" block @click="handleQuickAction('note')">
                📝 新建笔记
              </Button>
              <Button variant="secondary" block @click="handleQuickAction('meeting')">
                🎤 开始会议
              </Button>
              <Button variant="secondary" block @click="handleQuickAction('task')">
                ✅ 添加任务
              </Button>
            </div>
          </div>

          <!-- 实时通知 -->
          <div v-if="notifications.length > 0" class="notifications">
            <h4 class="section-title">🔔 实时通知</h4>
            <div
              v-for="notif in notifications"
              :key="notif.id"
              class="notification-item"
            >
              <span class="notif-icon">{{ notif.icon }}</span>
              <span class="notif-text">{{ notif.text }}</span>
              <span class="notif-time">{{ notif.time }}</span>
            </div>
          </div>
        </div>
      </template>
    </DualScreenLayout>

    <!-- 语音命令助手 -->
    <VoiceCommandAssistant
      @command="handleVoiceCommand"
      @transcription="handleTranscription"
    />

    <!-- 底部导航 -->
    <BottomNav
      :items="navItems"
      :active="activeNav"
      @change="handleNavChange"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import {
  Button,
  Card,
  Input,
  BottomNav,
  PullToRefresh,
  InfiniteScroll,
  AIThinkingIndicator,
} from '@/components'
import DualScreenLayout from '@/components/interactive/DualScreenLayout.vue'
import VoiceCommandAssistant from '@/components/interactive/VoiceCommandAssistant.vue'
import CompactCard from '@/components/base/CompactCard.vue'
import { useToast } from '@/composables/useToast'
import { wsHub } from '@/services/websocket-hub'
import type { WSMessage } from '@/services/websocket-hub'

const toast = useToast()

// 状态
const layoutRef = ref()
const scrollRef = ref()
const layoutMode = ref<'horizontal' | 'vertical'>('horizontal')
const searchQuery = ref('')
const activeNav = ref('home')
const aiThinking = ref(false)

// 列表数据
const items = ref<Array<any>>([])
const expandedGroups = ref<Set<string>>(new Set(['today']))

// AI 对话
const aiMessages = ref([
  { id: '1', role: 'assistant', content: '你好！我是 AI 助手，有什么可以帮你的吗？' },
])

// 智能建议
const suggestions = ref([
  { id: '1', icon: '📝', text: '总结今天的会议' },
  { id: '2', icon: '📧', text: '查看未读邮件' },
  { id: '3', icon: '📊', text: '本周工作报告' },
])

// 实时通知
const notifications = ref([
  { id: '1', icon: '📧', text: '收到新邮件：项目更新', time: '刚刚' },
  { id: '2', icon: '🎤', text: '会议将在 10 分钟后开始', time: '2 分钟前' },
])

// 底部导航
const navItems = [
  { id: 'home', icon: '🏠', label: '主页' },
  { id: 'notes', icon: '🎙️', label: '笔记' },
  { id: 'meetings', icon: '🎤', label: '会议' },
  { id: 'inbox', icon: '📨', label: '邮箱' },
  { id: 'more', icon: '⋮', label: '更多' },
]

// 分组数据
const groupedItems = computed(() => {
  const groups = [
    {
      date: 'today',
      title: '今天',
      expanded: expandedGroups.value.has('today'),
      items: items.value.filter((item: any) => item.group === 'today'),
    },
    {
      date: 'yesterday',
      title: '昨天',
      expanded: expandedGroups.value.has('yesterday'),
      items: items.value.filter((item: any) => item.group === 'yesterday'),
    },
    {
      date: 'week',
      title: '本周',
      expanded: expandedGroups.value.has('week'),
      items: items.value.filter((item: any) => item.group === 'week'),
    },
  ]
  return groups.filter(g => g.items.length > 0)
})

// 初始化数据
const initData = () => {
  items.value = [
    {
      id: '1',
      group: 'today',
      icon: '📝',
      title: '项目会议纪要',
      time: '2h前',
      preview: '今天讨论了架构设计和时间安排...',
      content: '详细内容：今天上午的项目会议中，团队讨论了新功能的架构设计方案，确定了各模块的职责分工，并制定了详细的时间表。预计两周内完成核心功能开发。',
      tags: ['会议', '项目'],
      starred: false,
    },
    {
      id: '2',
      group: 'today',
      icon: '📧',
      title: '重要邮件：进度更新',
      time: '3h前',
      preview: '关于项目进度的讨论...',
      content: '张经理发来的邮件，询问当前项目进度，需要在明天下午前回复。',
      tags: ['邮件', '重要'],
      starred: true,
    },
    {
      id: '3',
      group: 'yesterday',
      icon: '🎤',
      title: '团队周会录音',
      time: '1d前',
      preview: '参与者：张三、李四、王五...',
      content: '上周的团队周会录音，时长 45 分钟，已自动转写并提取关键信息。',
      tags: ['会议', '录音'],
      starred: false,
    },
  ]
}

// WebSocket 连接
onMounted(async () => {
  initData()

  // 连接 WebSocket（模拟）
  // await wsHub.connect('ws://localhost:8080/ws')

  // 订阅消息
  wsHub.on('note_created', handleNoteCreated)
  wsHub.on('email_received', handleEmailReceived)
  wsHub.on('ai_completed', handleAICompleted)
})

onUnmounted(() => {
  wsHub.disconnect()
})

// WebSocket 消息处理
const handleNoteCreated = (message: WSMessage) => {
  console.log('[WS] 新笔记', message.data)
  toast.success('收到新笔记')
  // 增量添加到列表
  items.value.unshift(message.data)
}

const handleEmailReceived = (message: WSMessage) => {
  console.log('[WS] 新邮件', message.data)
  notifications.value.unshift({
    id: Date.now().toString(),
    icon: '📧',
    text: `新邮件：${message.data.subject}`,
    time: '刚刚',
  })
}

const handleAICompleted = (message: WSMessage) => {
  console.log('[WS] AI 完成', message.data)
  aiThinking.value = false
  aiMessages.value.push({
    id: Date.now().toString(),
    role: 'assistant',
    content: message.data.result,
  })
}

// 交互处理
const handleRefresh = async () => {
  await new Promise(resolve => setTimeout(resolve, 1000))
  toast.success('刷新完成')
}

const loadMore = async () => {
  await new Promise(resolve => setTimeout(resolve, 1000))
  // 加载更多数据...
}

const toggleGroup = (date: string) => {
  if (expandedGroups.value.has(date)) {
    expandedGroups.value.delete(date)
  } else {
    expandedGroups.value.add(date)
  }
}

const toggleLayoutMode = () => {
  layoutMode.value = layoutMode.value === 'horizontal' ? 'vertical' : 'horizontal'
}

const handleItemClick = (item: any) => {
  console.log('点击项目:', item)
}

const handleToggleStar = (item: any) => {
  item.starred = !item.starred
  toast.info(item.starred ? '已收藏' : '已取消收藏')
}

const handleSuggestion = (suggestion: any) => {
  toast.info(`执行：${suggestion.text}`)
  aiThinking.value = true
  
  setTimeout(() => {
    aiThinking.value = false
    aiMessages.value.push({
      id: Date.now().toString(),
      role: 'assistant',
      content: `已完成：${suggestion.text}`,
    })
  }, 2000)
}

const handleQuickAction = (type: string) => {
  toast.info(`创建新${type}`)
}

const handleVoiceCommand = (command: string, params?: any) => {
  console.log('[Voice] 命令:', command, params)
  toast.success(`执行命令：${command}`)
}

const handleTranscription = (text: string) => {
  console.log('[Voice] 转写:', text)
  toast.success('语音转写完成')
}

const handleNavChange = (id: string) => {
  activeNav.value = id
  toast.info(`切换到: ${navItems.find(item => item.id === id)?.label}`)
}
</script>

<style scoped>
.optimized-demo {
  height: 100vh;
  background: var(--color-bg-base);
}

.main-content {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.search-bar {
  display: flex;
  gap: var(--space-2);
  padding: var(--space-4);
  background: var(--color-bg-surface);
  border-bottom: 1px solid var(--color-border);
}

.layout-toggle {
  flex-shrink: 0;
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-bg-base);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  font-size: 18px;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.layout-toggle:hover {
  background: var(--color-primary);
  color: white;
  border-color: var(--color-primary);
}

.item-group {
  margin-bottom: var(--space-2);
}

.group-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-surface);
  cursor: pointer;
  user-select: none;
}

.group-icon {
  font-size: 12px;
  color: var(--color-text-tertiary);
  transition: transform var(--duration-fast) var(--ease-out);
}

.group-title {
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
}

.group-count {
  font-size: 12px;
  color: var(--color-text-tertiary);
}

.group-items {
  padding: 0 var(--space-2);
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

/* 副屏样式 */
.secondary-content {
  display: flex;
  flex-direction: column;
  gap: var(--space-6);
  height: 100%;
}

.section-title {
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-secondary);
  margin: 0 0 var(--space-3) 0;
}

.ai-chat {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 200px;
}

.chat-messages {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  overflow-y: auto;
}

.chat-message {
  display: flex;
  gap: var(--space-2);
  align-items: flex-start;
}

.message-avatar {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 20px;
}

.message-content {
  flex: 1;
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-base);
  border-radius: var(--radius-md);
  font-size: 13px;
  line-height: 1.5;
}

.chat-message--user .message-content {
  background: var(--color-primary);
  color: white;
}

.suggestion-cards {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.suggestion-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2);
  font-size: 13px;
}

.suggestion-icon {
  font-size: 18px;
}

.action-buttons {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.notifications {
  padding-top: var(--space-4);
  border-top: 1px solid var(--color-border);
}

.notification-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2);
  font-size: 12px;
  margin-bottom: var(--space-2);
}

.notif-icon {
  font-size: 16px;
}

.notif-text {
  flex: 1;
  color: var(--color-text-secondary);
}

.notif-time {
  color: var(--color-text-tertiary);
}

/* 动画 */
.group-expand-enter-active,
.group-expand-leave-active {
  transition: all var(--duration-base) var(--ease-out);
  overflow: hidden;
}

.group-expand-enter-from,
.group-expand-leave-to {
  opacity: 0;
  max-height: 0;
}

.group-expand-enter-to,
.group-expand-leave-from {
  opacity: 1;
  max-height: 1000px;
}
</style>

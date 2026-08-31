<template>
  <div class="demo-page">
    <!-- 顶部标题 -->
    <div class="page-header">
      <h1>Redclaw 组件展示</h1>
      <p class="subtitle">16 个组件全部就绪 🎉</p>
    </div>

    <!-- 下拉刷新容器 -->
    <PullToRefresh :on-refresh="handleRefresh">
      <div class="demo-sections">
        
        <!-- 基础组件区 -->
        <section class="demo-section">
          <h2 class="section-title">基础组件 (8/8)</h2>
          
          <!-- Button -->
          <div class="demo-block">
            <h3>Button 按钮</h3>
            <div class="button-group">
              <Button variant="primary" @click="showToast('success')">
                Primary
              </Button>
              <Button variant="secondary" @click="showToast('info')">
                Secondary
              </Button>
              <Button variant="ghost" @click="showToast('warning')">
                Ghost
              </Button>
              <Button variant="danger" @click="showToast('error')">
                Danger
              </Button>
            </div>
            <div class="button-group">
              <Button variant="primary" size="small">Small</Button>
              <Button variant="primary" size="medium">Medium</Button>
              <Button variant="primary" size="large">Large</Button>
            </div>
            <div class="button-group">
              <Button variant="primary" :loading="true">
                Loading...
              </Button>
              <Button variant="primary" :disabled="true">
                Disabled
              </Button>
            </div>
          </div>

          <!-- Card -->
          <div class="demo-block">
            <h3>Card 卡片</h3>
            <div class="card-group">
              <Card variant="default">
                <p>Default Card</p>
              </Card>
              <Card variant="outlined">
                <p>Outlined Card</p>
              </Card>
              <Card variant="elevated" hoverable>
                <p>Elevated Hoverable</p>
              </Card>
            </div>
          </div>

          <!-- Input -->
          <div class="demo-block">
            <h3>Input 输入框</h3>
            <div class="input-group">
              <Input
                v-model="inputValue"
                placeholder="输入一些文字..."
              />
              <Input
                type="search"
                placeholder="搜索..."
              />
              <Input
                v-model="inputValue"
                :error="true"
                placeholder="错误状态"
              />
            </div>
          </div>

          <!-- Dialog -->
          <div class="demo-block">
            <h3>Dialog 对话框</h3>
            <Button @click="dialogVisible = true">
              打开 Dialog
            </Button>
            <Dialog
              v-model="dialogVisible"
              title="提示"
              confirm-text="确定"
              cancel-text="取消"
            >
              <p>这是一个对话框示例，包含标题、内容和操作按钮。</p>
            </Dialog>
          </div>

          <!-- BottomSheet -->
          <div class="demo-block">
            <h3>BottomSheet 底部弹出</h3>
            <Button @click="bottomSheetVisible = true">
              打开 BottomSheet
            </Button>
            <BottomSheet
              v-model="bottomSheetVisible"
              title="选择操作"
              height="auto"
            >
              <div class="sheet-content">
                <Button variant="ghost" block @click="handleAction('分享')">
                  📤 分享
                </Button>
                <Button variant="ghost" block @click="handleAction('编辑')">
                  ✏️ 编辑
                </Button>
                <Button variant="ghost" block @click="handleAction('删除')">
                  🗑️ 删除
                </Button>
              </div>
            </BottomSheet>
          </div>

          <!-- Loading & Skeleton -->
          <div class="demo-block">
            <h3>Loading & Skeleton</h3>
            <div class="loading-group">
              <Loading size="small" text="加载中" />
              <Loading size="medium" text="加载中" />
              <Loading size="large" text="加载中" />
            </div>
            <Skeleton :count="2" :avatar="true" :rows="3" />
          </div>
        </section>

        <!-- 交互组件区 -->
        <section class="demo-section">
          <h2 class="section-title">交互组件 (5/5)</h2>

          <!-- VoiceRecorder -->
          <div class="demo-block">
            <h3>VoiceRecorder 语音录音</h3>
            <div class="recorder-wrapper">
              <VoiceRecorder
                @start="handleRecordStart"
                @stop="handleRecordStop"
              />
            </div>
          </div>

          <!-- SwipeableListItem -->
          <div class="demo-block">
            <h3>SwipeableListItem 滑动操作</h3>
            <SwipeableListItem
              :left-actions="leftActions"
              :right-actions="rightActions"
            >
              <Card>
                <p>← 左滑或右滑试试 →</p>
                <p style="font-size: 12px; color: var(--color-text-tertiary);">
                  左滑：收藏 | 右滑：删除
                </p>
              </Card>
            </SwipeableListItem>
          </div>

          <!-- InfiniteScroll 示例在下面的列表中 -->
        </section>

        <!-- 业务组件区 -->
        <section class="demo-section">
          <h2 class="section-title">业务组件 (4/5)</h2>

          <!-- NoteCard -->
          <div class="demo-block">
            <h3>NoteCard 笔记卡片</h3>
            <NoteCard
              :note="demoNote"
              @click="handleNoteClick"
            />
          </div>

          <!-- EmailCard -->
          <div class="demo-block">
            <h3>EmailCard 邮件卡片</h3>
            <EmailCard
              :email="demoEmail"
              @click="handleEmailClick"
            />
          </div>

          <!-- SessionCard -->
          <div class="demo-block">
            <h3>SessionCard 会话卡片</h3>
            <SessionCard
              :session="demoSession"
              @click="handleSessionClick"
            />
          </div>

          <!-- AIThinkingIndicator -->
          <div class="demo-block">
            <h3>AIThinkingIndicator AI 思考动画</h3>
            <AIThinkingIndicator text="AI 正在思考..." />
          </div>
        </section>

        <!-- 无限滚动列表示例 -->
        <section class="demo-section">
          <h2 class="section-title">InfiniteScroll 无限滚动</h2>
          <div class="infinite-list" style="height: 400px;">
            <InfiniteScroll
              ref="infiniteScrollRef"
              :on-load="loadMoreItems"
              :distance="50"
            >
              <div v-for="item in listItems" :key="item.id" class="list-item">
                <Card>
                  <h4>{{ item.title }}</h4>
                  <p>{{ item.description }}</p>
                </Card>
              </div>
            </InfiniteScroll>
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
import { ref } from 'vue'
import {
  Button,
  Card,
  Input,
  Dialog,
  BottomSheet,
  Loading,
  Skeleton,
  BottomNav,
  VoiceRecorder,
  SwipeableListItem,
  PullToRefresh,
  InfiniteScroll,
  NoteCard,
  EmailCard,
  SessionCard,
  AIThinkingIndicator,
} from '@/components'
import type { SwipeAction } from '@/components'
import { useToast } from '@/composables/useToast'

const toast = useToast()

// 状态
const inputValue = ref('')
const dialogVisible = ref(false)
const bottomSheetVisible = ref(false)
const activeNav = ref('home')
const infiniteScrollRef = ref()
const listItems = ref<Array<{ id: string; title: string; description: string }>>([])
let itemCounter = 0

// 底部导航项
const navItems = [
  { id: 'home', icon: '🏠', label: '主页' },
  { id: 'notes', icon: '🎙️', label: '笔记' },
  { id: 'meetings', icon: '🎤', label: '会议' },
  { id: 'inbox', icon: '📨', label: '邮箱' },
  { id: 'more', icon: '⋮', label: '更多' },
]

// 滑动操作
const leftActions: SwipeAction[] = [
  {
    id: 'star',
    icon: '⭐',
    label: '收藏',
    type: 'success',
    onAction: () => toast.success('已收藏'),
  },
]

const rightActions: SwipeAction[] = [
  {
    id: 'delete',
    icon: '🗑️',
    label: '删除',
    type: 'danger',
    onAction: () => toast.error('已删除'),
  },
]

// 示例数据
const demoNote = {
  id: '1',
  title: '项目会议纪要',
  content: '今天讨论了项目进度和下一步计划，需要在本周完成架构设计...',
  domain: 'work',
  tags: ['会议', '项目'],
  audio_path: '/audio/123.wav',
  created_at: Date.now() - 3600000,
  updated_at: Date.now() - 3600000,
}

const demoEmail = {
  id: '1',
  subject: '关于项目进度的讨论',
  snippet: '你好，我想和你讨论一下项目的当前进度和遇到的一些问题...',
  from_name: '张三',
  from_address: 'zhangsan@example.com',
  date: Date.now() - 7200000,
  is_read: false,
  is_starred: true,
  category: 'primary' as const,
  importance: 'high' as const,
  has_attachments: true,
}

const demoSession = {
  id: '1',
  title: 'AI 助手对话',
  lastMessage: '我可以帮你总结这次会议的重点内容',
  lastMessageTime: Date.now() - 1800000,
  unreadCount: 3,
  avatar: '🤖',
  type: 'ai' as const,
}

// 方法
const showToast = (type: 'success' | 'error' | 'warning' | 'info') => {
  const messages = {
    success: '操作成功！',
    error: '操作失败！',
    warning: '请注意！',
    info: '提示信息',
  }
  toast[type](messages[type])
}

const handleRefresh = async () => {
  console.log('下拉刷新')
  await new Promise(resolve => setTimeout(resolve, 1500))
  toast.success('刷新完成')
}

const handleAction = (action: string) => {
  bottomSheetVisible.value = false
  toast.info(`执行操作: ${action}`)
}

const handleRecordStart = () => {
  console.log('开始录音')
}

const handleRecordStop = (duration: number) => {
  console.log('录音结束，时长:', duration)
  toast.success(`录音完成，时长 ${duration} 秒`)
}

const handleNoteClick = () => {
  toast.info('点击了笔记卡片')
}

const handleEmailClick = () => {
  toast.info('点击了邮件卡片')
}

const handleSessionClick = () => {
  toast.info('点击了会话卡片')
}

const handleNavChange = (id: string) => {
  activeNav.value = id
  toast.info(`切换到: ${navItems.find(item => item.id === id)?.label}`)
}

const loadMoreItems = async () => {
  console.log('加载更多')
  await new Promise(resolve => setTimeout(resolve, 1000))
  
  const newItems = Array.from({ length: 10 }, (_, i) => ({
    id: `item-${itemCounter++}`,
    title: `列表项 ${itemCounter}`,
    description: `这是第 ${itemCounter} 个列表项的描述内容`,
  }))
  
  listItems.value.push(...newItems)
  
  // 模拟：加载 3 次后没有更多数据
  if (itemCounter >= 30) {
    infiniteScrollRef.value?.setNoMore(true)
  }
}
</script>

<style scoped>
.demo-page {
  min-height: 100vh;
  min-height: 100dvh;
  background: var(--color-bg-base);
  padding-bottom: 80px; /* 为底部导航留空间 */
}

.page-header {
  background: var(--gradient-primary);
  color: white;
  padding: var(--space-6) var(--space-4);
  text-align: center;
}

.page-header h1 {
  margin: 0 0 var(--space-2) 0;
  font-size: 24px;
  font-weight: var(--font-weight-bold);
}

.subtitle {
  margin: 0;
  opacity: 0.9;
  font-size: 14px;
}

.demo-sections {
  padding: var(--space-4);
}

.demo-section {
  margin-bottom: var(--space-8);
}

.section-title {
  font-size: 20px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin: 0 0 var(--space-4) 0;
  padding-bottom: var(--space-2);
  border-bottom: 2px solid var(--color-border);
}

.demo-block {
  margin-bottom: var(--space-6);
}

.demo-block h3 {
  font-size: 16px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-secondary);
  margin: 0 0 var(--space-3) 0;
}

.button-group {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
  margin-bottom: var(--space-3);
}

.card-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.input-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.loading-group {
  display: flex;
  gap: var(--space-6);
  align-items: center;
  justify-content: center;
  padding: var(--space-4) 0;
  margin-bottom: var(--space-4);
}

.recorder-wrapper {
  display: flex;
  justify-content: center;
  padding: var(--space-6) 0;
}

.sheet-content {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.infinite-list {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  overflow: hidden;
}

.list-item {
  padding: var(--space-2);
}

.list-item:not(:last-child) {
  border-bottom: 1px solid var(--color-border);
}
</style>

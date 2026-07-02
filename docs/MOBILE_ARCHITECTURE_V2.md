# OpenCode Pocket Mobile - 架构设计 V2

基于 OpenCode 最新源码分析（opencodenew），设计移动端管理系统。

## 核心目标

1. **高信息密度**: 在小屏幕上显示更多有效信息
2. **实时同步**: WebSocket 实时刷新会话/任务状态
3. **触控优化**: 按钮/输入区域适配手指操作
4. **语音优先**: 以语音输入为主要交互方式
5. **双屏支持**: 主屏任务列表 + 副屏会话详情

## 系统架构

### 1. 后端 API 层

基于 OpenCode 最新 API 设计：

```typescript
// backend/internal/opencode/mobile_api.go
package opencode

// 移动端优化的 API 服务
type MobileAPI struct {
    httpAdapter  *adapter.OpenCodeHTTPAdapter
    eventMgr     *EventStreamManager
    permMgr      *PermissionManager
    questionMgr  *QuestionManager
    wsHub        *WebSocketHub
}

// 轻量级会话列表（减少字段）
type MobileSessionListItem struct {
    ID          string    `json:"id"`
    Title       string    `json:"title"`
    ModelName   string    `json:"modelName"`
    Status      string    `json:"status"` // "idle", "busy", "retry"
    UpdatedAt   time.Time `json:"updatedAt"`
    Preview     string    `json:"preview"`      // 最后一条消息预览
    HasPending  bool      `json:"hasPending"`   // 是否有待审批
}

// 消息摘要（移动端展示）
type MobileMessage struct {
    ID        string                 `json:"id"`
    Type      string                 `json:"type"` // "user", "assistant", "system"
    Text      string                 `json:"text,omitempty"`
    Tools     []MobileToolExecution  `json:"tools,omitempty"`
    Reasoning *MobileReasoning       `json:"reasoning,omitempty"`
    Timestamp int64                  `json:"timestamp"`
}

type MobileToolExecution struct {
    ID      string `json:"id"`
    Name    string `json:"name"`
    Status  string `json:"status"`  // "running", "completed", "error"
    Summary string `json:"summary"` // 压缩的输出摘要
}

type MobileReasoning struct {
    ID       string `json:"id"`
    Summary  string `json:"summary"`  // 折叠的推理内容
    Expanded bool   `json:"expanded"`
}
```

### 2. WebSocket 实时同步

```typescript
// backend/internal/websocket/mobile_hub.go
type MobileWSHub struct {
    // 客户端连接池
    clients    map[*MobileClient]bool
    
    // 按会话ID分组的订阅
    sessionSubs map[string]map[*MobileClient]bool
    
    // 全局事件广播
    broadcast  chan MobileEvent
}

type MobileEvent struct {
    Type      string      `json:"type"`
    SessionID string      `json:"sessionId,omitempty"`
    Data      interface{} `json:"data"`
}

// 事件类型
const (
    EventSessionUpdated      = "session.updated"
    EventMessageAdded        = "message.added"
    EventPermissionAsked     = "permission.asked"
    EventQuestionAsked       = "question.asked"
    EventToolProgress        = "tool.progress"
    EventSessionStatusChange = "session.status.changed"
)
```

### 3. 语音交互服务

```typescript
// backend/internal/stt/mobile_voice.go
type MobileVoiceService struct {
    sttProvider STTProvider  // 语音识别
    ttsProvider TTSProvider  // 语音合成
}

// 语音输入处理
func (s *MobileVoiceService) ProcessVoiceInput(ctx context.Context, audio []byte) (string, error) {
    // 1. 语音识别
    text, err := s.sttProvider.Transcribe(ctx, audio)
    if err != nil {
        return "", err
    }
    
    // 2. 命令识别（中英文）
    if cmd := s.parseCommand(text); cmd != nil {
        return s.executeCommand(ctx, cmd)
    }
    
    // 3. 普通文本返回
    return text, nil
}

// 支持的语音命令
const (
    VoiceCommandApprove   = "批准|同意|允许|approve|allow"
    VoiceCommandReject    = "拒绝|不同意|reject|deny"
    VoiceCommandSwitch    = "切换到|打开|switch to|open"
    VoiceCommandPause     = "暂停|停止|pause|stop"
    VoiceCommandContinue  = "继续|恢复|continue|resume"
)
```

## 前端架构

### 4. 双屏布局方案

```typescript
// frontend/src/features/mobile/dual-screen/DualScreenManager.vue
<template>
  <div class="dual-screen-container" :class="screenMode">
    <!-- 主屏：任务列表 -->
    <div class="primary-screen">
      <TaskListView
        :tasks="tasks"
        :selected="selectedTask"
        @select="handleTaskSelect"
        @voice-input="handleVoiceInput"
      />
    </div>
    
    <!-- 副屏：会话详情 -->
    <div class="secondary-screen" v-if="isSecondaryScreenAvailable">
      <SessionDetailView
        :session-id="selectedSessionId"
        :messages="messages"
        :pending-approvals="pendingApprovals"
        @approve="handleApprove"
        @reject="handleReject"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
// 双屏检测
const isSecondaryScreenAvailable = computed(() => {
  // iOS: 使用 window.screen.internal API
  // Android: 使用 Presentation API
  return window.matchMedia('(display-mode: multi-screen)').matches
})

// 屏幕模式
const screenMode = computed(() => {
  if (isSecondaryScreenAvailable.value) {
    return 'dual-screen'
  }
  return 'single-screen'
})
</script>
```

### 5. 高密度信息展示

```vue
<!-- frontend/src/features/mobile/components/CompactSessionCard.vue -->
<template>
  <div class="compact-session-card" @click="$emit('select', session.id)">
    <!-- 顶部：标题 + 状态指示器 -->
    <div class="card-header">
      <div class="title-row">
        <span class="session-title">{{ session.title }}</span>
        <StatusBadge :status="session.status" />
      </div>
      <div class="meta-row">
        <ModelIcon :model="session.modelName" />
        <TimeAgo :timestamp="session.updatedAt" />
      </div>
    </div>
    
    <!-- 中部：消息预览 -->
    <div class="message-preview">
      {{ session.preview }}
    </div>
    
    <!-- 底部：快捷操作 + 待办指示 -->
    <div class="card-footer">
      <div class="quick-actions">
        <button v-if="session.hasPending" class="pending-badge" @click.stop="showApprovals">
          <AlertIcon />
          <span>{{ pendingCount }}</span>
        </button>
      </div>
      <SwipeActions>
        <template #left>
          <ActionButton icon="archive" label="归档" />
        </template>
        <template #right>
          <ActionButton icon="delete" label="删除" danger />
        </template>
      </SwipeActions>
    </div>
  </div>
</template>

<style scoped>
.compact-session-card {
  /* 卡片最小高度：88pt (适配拇指点击) */
  min-height: 88px;
  padding: 12px;
  border-radius: 12px;
  background: var(--card-bg);
  /* 滑动卡片效果 */
  transform: translateX(var(--swipe-offset, 0));
  transition: transform 0.2s ease-out;
}

.title-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.session-title {
  font-size: 16px;
  font-weight: 600;
  flex: 1;
  /* 单行截断 */
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.message-preview {
  font-size: 14px;
  color: var(--text-secondary);
  /* 最多显示 2 行 */
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  margin: 8px 0;
}

/* 触摸优化：最小点击区域 44x44pt */
.quick-actions button {
  min-width: 44px;
  min-height: 44px;
}
</style>
```

### 6. 语音交互组件

```vue
<!-- frontend/src/features/mobile/components/VoiceInput.vue -->
<template>
  <div class="voice-input-container">
    <!-- 语音按钮 -->
    <button
      class="voice-button"
      :class="{ recording: isRecording, processing: isProcessing }"
      @touchstart="startRecording"
      @touchend="stopRecording"
      @touchcancel="cancelRecording"
    >
      <MicIcon v-if="!isRecording" />
      <WaveformAnimation v-else />
    </button>
    
    <!-- 识别文本预览 -->
    <div v-if="transcript" class="transcript-preview">
      {{ transcript }}
    </div>
    
    <!-- 快捷命令提示 -->
    <div class="voice-commands-hint">
      <span>说 "批准" 或 "拒绝" 快速操作</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useVoiceRecorder } from '@/composables/useVoiceRecorder'
import { useVoiceCommands } from '@/composables/useVoiceCommands'

const { 
  isRecording, 
  isProcessing, 
  startRecording, 
  stopRecording, 
  cancelRecording,
  audioData
} = useVoiceRecorder()

const { 
  transcript, 
  command,
  sendVoiceInput 
} = useVoiceCommands()

// 录音结束后处理
watch(audioData, async (data) => {
  if (data) {
    isProcessing.value = true
    try {
      const result = await sendVoiceInput(data)
      
      // 如果是命令，直接执行
      if (result.isCommand) {
        await executeCommand(result.command)
      } else {
        // 普通文本，作为提示词发送
        emit('text-input', result.text)
      }
    } finally {
      isProcessing.value = false
    }
  }
})
</script>

<style scoped>
.voice-button {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  background: var(--primary-color);
  border: none;
  display: flex;
  align-items: center;
  justify-content: center;
  /* 触觉反馈 */
  -webkit-tap-highlight-color: transparent;
}

.voice-button.recording {
  background: var(--error-color);
  animation: pulse 1s infinite;
}

@keyframes pulse {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(1.1); }
}

.transcript-preview {
  margin-top: 12px;
  padding: 12px;
  background: var(--bg-secondary);
  border-radius: 8px;
  font-size: 14px;
}
</style>
```

### 7. 权限审批移动端优化

```vue
<!-- frontend/src/features/mobile/components/MobilePermissionPrompt.vue -->
<template>
  <!-- iOS 风格的底部工作表 -->
  <BottomSheet :visible="visible" @close="$emit('close')">
    <div class="permission-prompt">
      <!-- 权限类型图标 -->
      <div class="permission-icon">
        <component :is="getPermissionIcon(permission.action)" />
      </div>
      
      <!-- 权限信息 -->
      <div class="permission-info">
        <h3>{{ getPermissionTitle(permission.action) }}</h3>
        <p class="resource-path">{{ formatResource(permission.resources[0]) }}</p>
      </div>
      
      <!-- 详情折叠 -->
      <ExpandableDetails v-if="permission.metadata">
        <template #summary>查看详情</template>
        <template #content>
          <CodeBlock :code="permission.metadata.command" v-if="permission.action === 'bash'" />
          <DiffPreview :diff="permission.metadata.diff" v-if="permission.action === 'edit'" />
        </template>
      </ExpandableDetails>
      
      <!-- 操作按钮 -->
      <div class="action-buttons">
        <button class="action-btn primary" @click="handleApprove('once')">
          仅此一次
        </button>
        <button class="action-btn secondary" @click="handleApprove('always')">
          始终允许
        </button>
        <button class="action-btn danger" @click="handleReject">
          拒绝
        </button>
      </div>
      
      <!-- 滑动手势提示 -->
      <div class="gesture-hint">
        <span>👉 向右滑动批准</span>
        <span>👈 向左滑动拒绝</span>
      </div>
    </div>
  </BottomSheet>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useSwipeGesture } from '@/composables/useSwipeGesture'

const props = defineProps<{
  permission: PermissionRequest
  visible: boolean
}>()

const emit = defineEmits<{
  approve: [reply: 'once' | 'always']
  reject: []
  close: []
}>()

// 滑动手势支持
const { onSwipe } = useSwipeGesture({
  threshold: 100, // 滑动100px触发
  onSwipeRight: () => handleApprove('once'),
  onSwipeLeft: () => handleReject()
})

const getPermissionIcon = (action: string) => {
  const iconMap = {
    'bash': 'TerminalIcon',
    'edit': 'EditIcon',
    'read': 'FileIcon',
    'glob': 'SearchIcon',
    'external_directory': 'FolderIcon'
  }
  return iconMap[action] || 'QuestionIcon'
}

const getPermissionTitle = (action: string) => {
  const titleMap = {
    'bash': '执行命令',
    'edit': '编辑文件',
    'read': '读取文件',
    'glob': '搜索文件',
    'external_directory': '访问外部目录'
  }
  return titleMap[action] || '权限请求'
}

const formatResource = (resource: string) => {
  // 移动端截断长路径
  if (resource.length > 40) {
    const parts = resource.split('/')
    return `.../${parts[parts.length - 1]}`
  }
  return resource
}

const handleApprove = (reply: 'once' | 'always') => {
  emit('approve', reply)
  emit('close')
}

const handleReject = () => {
  emit('reject')
  emit('close')
}
</script>

<style scoped>
.permission-prompt {
  padding: 24px;
}

.permission-icon {
  width: 64px;
  height: 64px;
  margin: 0 auto 16px;
  border-radius: 50%;
  background: var(--primary-bg);
  display: flex;
  align-items: center;
  justify-content: center;
}

.permission-info h3 {
  font-size: 20px;
  font-weight: 600;
  text-align: center;
  margin-bottom: 8px;
}

.resource-path {
  font-family: var(--font-mono);
  font-size: 13px;
  color: var(--text-secondary);
  text-align: center;
  background: var(--bg-tertiary);
  padding: 8px 12px;
  border-radius: 6px;
  overflow-x: auto;
  white-space: nowrap;
}

.action-buttons {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-top: 24px;
}

.action-btn {
  min-height: 50px;
  border-radius: 12px;
  font-size: 16px;
  font-weight: 600;
  border: none;
  cursor: pointer;
  transition: all 0.2s;
}

.action-btn.primary {
  background: var(--primary-color);
  color: white;
}

.action-btn.secondary {
  background: var(--secondary-color);
  color: white;
}

.action-btn.danger {
  background: var(--error-color);
  color: white;
}

/* 点按反馈 */
.action-btn:active {
  transform: scale(0.98);
  opacity: 0.8;
}

.gesture-hint {
  display: flex;
  justify-content: space-around;
  margin-top: 16px;
  font-size: 12px;
  color: var(--text-tertiary);
}
</style>
```

### 8. 实时同步 Composable

```typescript
// frontend/src/composables/useRealtimeSync.ts
import { ref, onMounted, onUnmounted } from 'vue'
import { useWebSocket } from '@vueuse/core'

export function useRealtimeSync(sessionId?: string) {
  const wsUrl = computed(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const host = import.meta.env.VITE_API_HOST || window.location.host
    return `${protocol}//${host}/api/ws/mobile`
  })

  const { status, data, send, open, close } = useWebSocket(wsUrl.value, {
    autoReconnect: {
      retries: 10,
      delay: 1000,
      onFailed() {
        console.error('WebSocket 重连失败')
      }
    },
    heartbeat: {
      message: JSON.stringify({ type: 'ping' }),
      interval: 30000
    }
  })

  // 会话列表
  const sessions = ref<MobileSessionListItem[]>([])
  
  // 当前会话消息
  const messages = ref<MobileMessage[]>([])
  
  // 待审批项
  const pendingApprovals = ref<{
    permissions: PermissionRequest[]
    questions: QuestionRequest[]
  }>({
    permissions: [],
    questions: []
  })

  // 监听服务器事件
  watch(data, (rawData) => {
    if (!rawData) return
    
    try {
      const event: MobileEvent = JSON.parse(rawData)
      
      switch (event.type) {
        case 'session.updated':
          updateSession(event.data)
          break
          
        case 'message.added':
          if (event.sessionId === sessionId) {
            messages.value.push(event.data)
          }
          break
          
        case 'permission.asked':
          pendingApprovals.value.permissions.push(event.data)
          // 触发通知
          showNotification('权限请求', event.data.action)
          // 触发触觉反馈
          navigator.vibrate?.(200)
          break
          
        case 'question.asked':
          pendingApprovals.value.questions.push(event.data)
          showNotification('问题请求', event.data.questions[0].header)
          navigator.vibrate?.(200)
          break
          
        case 'tool.progress':
          updateToolProgress(event.data)
          break
          
        case 'session.status.changed':
          updateSessionStatus(event.sessionId, event.data.status)
          break
      }
    } catch (error) {
      console.error('处理 WebSocket 消息失败:', error)
    }
  })

  // 订阅会话
  const subscribe = (sessionId: string) => {
    send(JSON.stringify({
      type: 'subscribe',
      sessionId
    }))
  }

  // 取消订阅
  const unsubscribe = (sessionId: string) => {
    send(JSON.stringify({
      type: 'unsubscribe',
      sessionId
    }))
  }

  onMounted(() => {
    open()
    if (sessionId) {
      subscribe(sessionId)
    }
  })

  onUnmounted(() => {
    if (sessionId) {
      unsubscribe(sessionId)
    }
    close()
  })

  return {
    status,
    sessions,
    messages,
    pendingApprovals,
    subscribe,
    unsubscribe
  }
}
```

## 技术栈

### 后端
- **Go 1.21+**
- **Echo/Gin** - HTTP 框架
- **gorilla/websocket** - WebSocket 支持
- **SQLite** - 本地缓存
- **OpenCode HTTP Client** - 已实现的适配器

### 前端
- **Vue 3 + TypeScript**
- **Vite**
- **TailwindCSS** - 响应式样式
- **VueUse** - 组合式工具库
- **Capacitor** - 原生能力桥接
  - **@capacitor/haptics** - 触觉反馈
  - **@capacitor/speech-recognition** - 语音识别
  - **@capacitor/text-to-speech** - 语音合成
  - **@capacitor/screen-orientation** - 屏幕方向控制

## 下一步实现

1. ✅ 后端 HTTP 适配器（已完成）
2. ✅ 后端事件流管理器（已完成）
3. ✅ 后端权限/问答管理器（已完成）
4. ⏳ WebSocket Hub 实现
5. ⏳ 移动端 API 端点
6. ⏳ 语音识别服务集成
7. ⏳ 前端双屏布局组件
8. ⏳ 前端实时同步 composables
9. ⏳ 原生能力集成（Capacitor）

> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/2026-08-27-mobile-ux-design-v2.md`](2026-08-27-mobile-ux-design-v2.md)
> Do NOT use this doc for current implementation decisions.
> 移动端交互/布局/导航/UI 设计面已由 v2 统一设计方案取代。

# 📱 OpenCode Pocket 移动端交互优化方案

**版本**: v2.0  
**日期**: 2026-07-03  
**重点**: 小屏幕信息密度 + 语音优先 + 双屏支持 + WebSocket 实时

---

## 🎯 核心优化目标

### 1. 信息密度优化
- **卡片式折叠布局**：默认显示摘要，点击展开详情
- **分层信息架构**：主要信息优先，次要信息收起
- **智能省略**：根据屏幕宽度动态调整内容显示

### 2. 语音优先交互
- **全局语音按钮**：悬浮在右下角，随时可用
- **语音命令**：支持"打开笔记"、"搜索邮件"等快捷操作
- **多模态输入**：语音 + 手势 + 点击组合

### 3. 双屏模式
- **主屏**：核心内容（列表、详情）
- **副屏**：辅助信息（AI 助手、快捷操作、实时通知）
- **分屏切换**：左右滑动或按钮切换

### 4. WebSocket 实时更新
- **实时推送**：新消息、AI 处理结果即时显示
- **增量更新**：只更新变化的部分，不重新渲染整个列表
- **离线队列**：断网时缓存，恢复后自动同步

---

## 📐 信息密度优化设计

### 方案 1: 紧凑卡片模式

```
┌─────────────────────────────┐
│ 📝 项目会议 · 2小时前  [⭐] │ ← 一行显示标题+时间+操作
│ 讨论了架构设计和... [展开] │ ← 一行摘要+展开按钮
├─────────────────────────────┤
│ 📧 重要邮件 · 1天前    [📎] │
│ 关于项目进度的...     [展开] │
├─────────────────────────────┤
│ 🎤 团队会议 · 3小时    [▶] │
│ 参与者: 张三、李四    [展开] │
└─────────────────────────────┘
```

**特点**：
- 每个卡片高度减少 30%
- 屏幕可显示数量增加 50%
- 点击展开查看完整内容

### 方案 2: 分组折叠模式

```
▼ 今天 (5)
  📝 项目会议 · 2h前
  📧 重要邮件 · 3h前
  
▶ 昨天 (12)           ← 默认收起
  
▶ 本周 (45)           ← 默认收起

▼ AI 建议 (3)         ← 智能推荐
  💡 待办提醒
  📊 数据报告
```

**特点**：
- 时间分组自动折叠
- 只展开当前关注的分组
- AI 智能推荐置顶

### 方案 3: 多列紧凑模式（横屏）

```
┌─────────┬─────────┬─────────┐
│ 📝 会议  │ 📧 邮件  │ 🎤 录音  │
│ 2h前    │ 1d前    │ 3h前    │
│ [展开]  │ [展开]  │ [▶]     │
├─────────┼─────────┼─────────┤
│ 📝 笔记  │ 📧 通知  │ 🤖 AI   │
│ 5h前    │ 2d前    │ 进行中  │
│ [展开]  │ [展开]  │ [...]   │
└─────────┴─────────┴─────────┘
```

**特点**：
- 横屏时 2-3 列显示
- 适合平板或折叠屏
- 信息密度提升 200%

---

## 🎙️ 语音优先交互设计

### 全局语音助手

```
主界面布局：
┌─────────────────────────────┐
│ 🔍 [智能搜索框]        [☰] │ ← 顶部搜索+菜单
├─────────────────────────────┤
│                             │
│   [内容区域]                 │
│                             │
│                             │
│                             │
│                      [🎙️]  │ ← 悬浮语音按钮
└─────────────────────────────┘
│ [导航] [导航] [导航]         │ ← 底部导航
└─────────────────────────────┘
```

### 语音交互流程

**1. 快速录音模式**（当前实现）
```
长按 🎙️ → 开始录音
松开     → 停止录音 → 自动转写 → 创建笔记
```

**2. 语音命令模式**（新增）
```
点击 🎙️ → 进入命令模式 → 显示常用命令

用户说："打开笔记"
系统识别 → 跳转到笔记列表

用户说："搜索关于项目的邮件"
系统识别 → 执行搜索 → 显示结果

用户说："总结今天的会议"
系统识别 → 调用 AI → 展示摘要
```

**3. 连续对话模式**（新增）
```
长按 🎙️ 3秒 → 进入连续对话模式

用户："今天有什么重要的事情？"
AI："您有 2 个未读邮件和 1 个会议提醒"

用户："打开第一封邮件"
AI："正在打开..." → 显示邮件详情

用户："回复说下午三点可以"
AI："已生成回复草稿，请确认"
```

### 语音命令表

| 命令 | 动作 | 参数 |
|------|------|------|
| 打开 [类型] | 跳转页面 | 笔记、邮件、会议、设置 |
| 搜索 [关键词] | 全局搜索 | 任意文本 |
| 创建 [类型] | 新建条目 | 笔记、任务、提醒 |
| 总结 [内容] | AI 总结 | 今天、本周、会议 |
| 提醒我 [事项] | 创建提醒 | 任意事项 + 时间 |
| 拨打电话 | 拨号 | 联系人姓名 |

---

## 🖥️ 双屏模式设计

### 模式 1: 主副屏分屏

```
┌──────────────┬──────────────┐
│  主屏        │  副屏        │
│              │              │
│  笔记列表     │  AI 助手     │
│              │              │
│  📝 会议纪要  │  💡 智能建议  │
│  📝 项目计划  │              │
│  📝 学习笔记  │  🔔 实时通知  │
│              │              │
│              │  ⚡ 快捷操作  │
│              │   📝 新笔记   │
│              │   📧 新邮件   │
└──────────────┴──────────────┘
     60%            40%
```

**主屏**：
- 显示核心内容（列表、详情）
- 支持滚动、点击、长按

**副屏**：
- AI 助手对话
- 实时通知
- 快捷操作按钮
- 相关推荐

### 模式 2: 上下分屏

```
┌─────────────────────────────┐
│  主屏 - 笔记详情             │
│                             │
│  # 项目会议纪要              │
│                             │
│  今天讨论了架构设计...       │
│  ··· (滚动查看)             │
│                             │
├─────────────────────────────┤
│  副屏 - AI 助手              │
│                             │
│  💡 相关笔记推荐：           │
│  · 上次会议纪要              │
│  · 项目时间线                │
│                             │
│  🎤 [语音输入框]            │
└─────────────────────────────┘
     70%
     
     30%
```

**使用场景**：
- 查看详情时，副屏显示相关信息
- 语音输入时，副屏显示实时转写
- AI 处理时，副屏显示进度

### 模式 3: 悬浮窗模式

```
┌─────────────────────────────┐
│  主屏 - 邮件列表             │
│                             │
│  📧 重要邮件                 │
│  📧 工作通知        ╔═══════╗│
│  📧 订阅更新        ║ AI 🤖 ║│
│                    ║       ║│
│                    ║ 正在   ║│
│                    ║ 总结   ║│
│                    ║ 邮件   ║│
│                    ║ ...   ║│
│                    ╚═══════╝│
└─────────────────────────────┘
```

**特点**：
- AI 处理时显示悬浮窗
- 不遮挡主要内容
- 可拖动位置
- 点击展开完整结果

---

## 📡 WebSocket 实时更新设计

### WebSocket 架构

```typescript
// frontend/src/services/websocket-hub.ts

interface WSMessage {
  type: 'note_created' | 'email_received' | 'ai_completed'
  data: any
  timestamp: number
}

class WebSocketHub {
  private ws: WebSocket | null = null
  private reconnectAttempts = 0
  private maxReconnectAttempts = 5
  
  // 连接 WebSocket
  connect(url: string) {
    this.ws = new WebSocket(url)
    
    this.ws.onopen = () => {
      console.log('[WS] 连接成功')
      this.reconnectAttempts = 0
    }
    
    this.ws.onmessage = (event) => {
      const message: WSMessage = JSON.parse(event.data)
      this.handleMessage(message)
    }
    
    this.ws.onerror = (error) => {
      console.error('[WS] 连接错误', error)
    }
    
    this.ws.onclose = () => {
      console.log('[WS] 连接关闭，尝试重连...')
      this.reconnect()
    }
  }
  
  // 处理消息
  private handleMessage(message: WSMessage) {
    switch (message.type) {
      case 'note_created':
        // 增量添加到列表顶部
        this.prependToList('notes', message.data)
        break
        
      case 'email_received':
        // 显示实时通知
        this.showNotification('新邮件', message.data.subject)
        // 更新未读数
        this.updateUnreadCount('emails', +1)
        break
        
      case 'ai_completed':
        // AI 处理完成，更新结果
        this.updateAIResult(message.data)
        break
    }
  }
  
  // 自动重连
  private reconnect() {
    if (this.reconnectAttempts < this.maxReconnectAttempts) {
      this.reconnectAttempts++
      const delay = Math.min(1000 * Math.pow(2, this.reconnectAttempts), 30000)
      setTimeout(() => this.connect(), delay)
    }
  }
}
```

### 实时更新策略

**1. 增量更新**（推荐）
```typescript
// 只更新变化的项，不重新渲染整个列表
const handleNewNote = (note: Note) => {
  notes.value.unshift(note) // 添加到顶部
  
  // 如果列表太长，移除底部旧项
  if (notes.value.length > 100) {
    notes.value.pop()
  }
}
```

**2. 虚拟滚动**（大列表）
```typescript
// 只渲染可见区域的项，提升性能
import { useVirtualList } from '@vueuse/core'

const { list, containerProps, wrapperProps } = useVirtualList(
  notes,
  {
    itemHeight: 80,
    overscan: 10,
  }
)
```

**3. 防抖更新**（高频消息）
```typescript
// 合并短时间内的多次更新
const debouncedUpdate = debounce((items) => {
  batchUpdateList(items)
}, 300)
```

---

## 🎮 手势优化设计

### 单手操作优化

```
拇指热区（右手）：
┌─────────────────────────────┐
│ ⚫⚫⚫⚫           ⚪⚪⚪⚪ │ ← 难触达
│ ⚫⚫⚫⚫         ⚪⚪⚪⚪⚪ │
│ ⚫⚫⚫⚫       ⚪⚪⚪⚪⚪⚪ │
│ ⚫⚫⚫⚫     🟢🟢🟢🟢🟢🟢 │ ← 易触达
│ ⚫⚫⚫⚫   🟢🟢🟢🟢🟢🟢🟢 │
│ ⚫⚫⚫   🟢🟢🟢🟢🟢🟢🟢🟢 │
│ ⚫⚫   🟢🟢🟢🟢🟢🟢🟢🟢🟢 │
└─────────────────────────────┘
│ 🟢🟢🟢🟢🟢🟢🟢🟢🟢🟢🟢🟢 │ ← 最易触达
└─────────────────────────────┘

关键操作放在绿色区域：
- 底部导航
- 右下角语音按钮
- 右侧滑动手势
```

### 扩大触摸目标

```css
/* 最小触摸目标：56dp（比标准 48dp 更大） */
.touch-target {
  min-width: 56px;
  min-height: 56px;
  
  /* 实际视觉可以更小，用 padding 扩大点击区域 */
  padding: 8px;
}

/* 列表项高度增加，便于点击 */
.list-item {
  min-height: 72px; /* 比标准 48dp 高 */
  padding: 12px 16px;
}

/* 按钮间距增加，避免误触 */
.button-group button {
  margin: 0 8px; /* 至少 8px 间距 */
}
```

### 手势增强

| 手势 | 操作 | 反馈 |
|------|------|------|
| 双击卡片 | 快速展开/收起 | 弹簧动画 |
| 三指下拉 | 全局刷新 | 震动 + 提示 |
| 两指捏合 | 调整信息密度 | 实时预览 |
| 长按拖动 | 重新排序 | 拖动阴影 |
| 边缘滑入 | 快捷菜单 | 侧边栏滑出 |

---

## 🎨 UI 组件优化

### 紧凑型卡片组件

```vue
<template>
  <div
    :class="['compact-card', { expanded }]"
    @click="toggleExpand"
  >
    <!-- 紧凑模式：单行显示 -->
    <div v-if="!expanded" class="compact-view">
      <div class="card-header">
        <span class="icon">{{ icon }}</span>
        <span class="title">{{ title }}</span>
        <span class="time">{{ formattedTime }}</span>
        <button class="action-btn" @click.stop="handleAction">
          {{ actionIcon }}
        </button>
      </div>
      <div class="card-preview">
        {{ truncatedContent }}
      </div>
    </div>
    
    <!-- 展开模式：完整显示 -->
    <div v-else class="expanded-view">
      <div class="card-header">
        <span class="icon">{{ icon }}</span>
        <span class="title">{{ title }}</span>
        <button class="close-btn" @click.stop="toggleExpand">
          ✕
        </button>
      </div>
      <div class="card-content">
        <slot />
      </div>
      <div class="card-actions">
        <slot name="actions" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.compact-card {
  background: var(--color-bg-surface);
  border-radius: var(--radius-md);
  padding: 8px 12px; /* 减少 padding */
  transition: all var(--duration-base) var(--ease-out);
}

.compact-view {
  display: flex;
  flex-direction: column;
  gap: 4px; /* 减少间距 */
}

.card-header {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px; /* 减小字号 */
}

.card-preview {
  font-size: 13px;
  color: var(--color-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.expanded-view {
  padding: 12px;
}
</style>
```

---

## 🔔 实时通知设计

### 通知层级

```
优先级 1: 紧急通知（全屏弹窗）
┌─────────────────────────────┐
│         ⚠️ 紧急提醒           │
│                             │
│   会议将在 5 分钟后开始      │
│                             │
│   [立即加入]   [稍后提醒]    │
└─────────────────────────────┘

优先级 2: 重要通知（顶部横幅）
┌─────────────────────────────┐
│ 📧 重要邮件：项目进度更新 [→]│
├─────────────────────────────┤
│ (主内容区)                   │

优先级 3: 一般通知（右上角角标）
┌──────────────────────────┬──┐
│ 笔记列表                 │🔔│
│                         │3 │
├──────────────────────────┴──┤

优先级 4: 静默通知（仅更新数据）
```

---

## 📋 下一步实施计划

### Phase 1: 核心优化（本周）
1. 实现 WebSocketHub 服务
2. 创建紧凑型卡片组件
3. 优化触摸目标尺寸
4. 增强语音命令识别

### Phase 2: 双屏支持（下周）
5. 实现主副屏分屏布局
6. 创建 AI 助手副屏组件
7. 实现屏幕切换动画

### Phase 3: 性能优化（2 周）
8. 虚拟滚动大列表
9. 增量更新优化
10. 离线队列增强

---

**总结**：通过信息密度优化、语音优先、双屏支持和 WebSocket 实时更新，可以在小屏幕上提供更高效的交互体验。

下一步我们开始实现这些优化方案吗？

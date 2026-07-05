# Phase 7 开发计划

**开始日期**: 2026-07-06  
**基于版本**: Phase 6 (commit dcd0bde)  
**目标**: 修复已知问题 + 增强用户体验

---

## 🎯 Phase 7 目标

### 核心目标
1. 修复 Phase 6 遗留的已知问题
2. 提升 UI/UX 体验
3. 增强系统稳定性
4. 完善自动化测试

### 预期成果
- ✅ 前端状态管理问题修复
- ✅ UI 布局优化
- ✅ Backend API 改进
- ✅ 测试覆盖率提升到 85%+

---

## 📋 Phase 7 任务清单

### 优先级 1: 已知问题修复 (必须)

#### 7.1 前端 authStore 状态同步 🔴 BLOCKER
**问题**: 运行时修改 localStorage 不触发 Pinia reactive 更新

**影响**:
- 自动化测试需要完整登录流程
- 用户体验受影响（刷新后需重新登录）

**解决方案**:
```typescript
// stores/auth.ts
import { watch } from 'vue'

// 方案 A: 添加 storage event listener
window.addEventListener('storage', (e) => {
  if (e.key === 'pocket_token') {
    authStore.syncFromStorage()
  }
})

// 方案 B: 使用 Capacitor Preferences API
import { Preferences } from '@capacitor/preferences'

// 方案 C: 添加 syncFromStorage 方法
const authStore = defineStore('auth', {
  actions: {
    syncFromStorage() {
      this.token = localStorage.getItem(TOKEN_KEY) || ''
      this.user = localStorage.getItem(USER_KEY) || ''
    }
  }
})
```

**验收标准**:
- [ ] CDP 注入 token 后自动触发状态更新
- [ ] 刷新页面后保持登录状态
- [ ] 自动化测试可以通过 localStorage 设置 auth

**估时**: 2-3 小时

---

#### 7.2 BottomNav Sheet 布局优化 🟡 HIGH
**问题**: Sheet 第 3 列的 tile 被 FAB 物理遮挡（z-index 已修复但布局待优化）

**当前状态**:
```vue
<!-- 3 列 grid，第 3 列被 FAB 遮挡 -->
<div class="more-sheet-grid" style="grid-template-columns: repeat(3, 1fr)">
```

**解决方案**:
```vue
<!-- 方案 A: 改为 2 列布局 -->
<div class="more-sheet-grid" style="grid-template-columns: repeat(2, 1fr); gap: 16px;">

<!-- 方案 B: 添加 padding-bottom 避开 FAB -->
<div class="more-sheet-grid" style="padding-bottom: 80px;">

<!-- 方案 C: 使用 flex 布局 + 自动换行 -->
<div class="more-sheet-flex" style="display: flex; flex-wrap: wrap; gap: 16px;">
```

**推荐**: 方案 A (2 列布局) + 方案 B (padding-bottom)

**验收标准**:
- [ ] 所有 tile 完全可见
- [ ] 不被 FAB 遮挡
- [ ] 布局美观对称

**估时**: 1-2 小时

---

#### 7.3 Backend 任务 ID 自动生成 🟢 MEDIUM
**问题**: POST /api/tasks 需要客户端提供 ID

**当前代码**:
```go
if req.ID == "" || req.Title == "" {
    http.Error(w, "missing required fields", http.StatusBadRequest)
    return
}
```

**改进方案**:
```go
if req.ID == "" {
    req.ID = "task-" + uuid.New().String()
}
if req.Title == "" {
    http.Error(w, "title is required", http.StatusBadRequest)
    return
}
```

**验收标准**:
- [ ] 不提供 ID 时自动生成 UUID
- [ ] 提供 ID 时使用提供的 ID
- [ ] API 测试通过

**估时**: 1 小时

---

### 优先级 2: UI/UX 改进 (重要)

#### 7.4 登录页面增强 🟡 HIGH
**改进点**:
1. 添加记住密码功能
2. 添加登录加载状态
3. 改善错误提示 UI
4. 添加密码可见性切换

**实现**:
```vue
<!-- LoginView.vue -->
<template>
  <div class="login-form">
    <input v-model="username" placeholder="用户名" />
    <div class="password-field">
      <input 
        :type="showPassword ? 'text' : 'password'" 
        v-model="password" 
        placeholder="密码" 
      />
      <button @click="showPassword = !showPassword">
        {{ showPassword ? '👁️' : '👁️‍🗨️' }}
      </button>
    </div>
    <label>
      <input type="checkbox" v-model="rememberMe" />
      记住密码
    </label>
    <button 
      :disabled="loading || !username || !password"
      @click="handleLogin"
    >
      <span v-if="loading">登录中...</span>
      <span v-else>登录</span>
    </button>
    <p v-if="error" class="error-message">{{ error }}</p>
  </div>
</template>
```

**估时**: 2-3 小时

---

#### 7.5 任务卡片交互优化 🟢 MEDIUM
**改进点**:
1. 添加滑动删除
2. 添加拖拽排序
3. 添加优先级颜色标识
4. 改善状态标签显示

**实现**:
```vue
<!-- TaskCard.vue -->
<template>
  <div 
    class="task-card"
    :class="[`priority-${task.priority}`, `status-${task.status}`]"
    @touchstart="handleTouchStart"
    @touchmove="handleTouchMove"
    @touchend="handleTouchEnd"
  >
    <div class="task-header">
      <h3>{{ task.title }}</h3>
      <span class="priority-badge" :style="{ background: priorityColor }">
        {{ task.priority }}
      </span>
    </div>
    <p class="task-desc">{{ task.description }}</p>
    <div class="task-meta">
      <span class="status-badge">{{ task.status }}</span>
      <span class="time">{{ formatTime(task.createdAt) }}</span>
    </div>
  </div>
</template>

<style>
.task-card.priority-high { border-left: 4px solid #f44336; }
.task-card.priority-medium { border-left: 4px solid #ff9800; }
.task-card.priority-low { border-left: 4px solid #4caf50; }
</style>
```

**估时**: 3-4 小时

---

#### 7.6 任务筛选和搜索 🟢 MEDIUM
**功能**:
1. 按状态筛选 (active/completed/archived)
2. 按优先级筛选 (high/medium/low)
3. 按来源筛选 (local/opencode/acc)
4. 搜索任务标题

**实现**:
```vue
<!-- TasksView.vue -->
<template>
  <div class="tasks-view">
    <div class="filter-bar">
      <input 
        v-model="searchQuery" 
        placeholder="🔍 搜索任务..."
        class="search-input"
      />
      <div class="filter-chips">
        <button 
          v-for="status in ['all', 'active', 'completed']"
          :key="status"
          :class="{ active: filterStatus === status }"
          @click="filterStatus = status"
        >
          {{ statusLabels[status] }}
        </button>
      </div>
    </div>
    <div class="task-list">
      <TaskCard 
        v-for="task in filteredTasks" 
        :key="task.id" 
        :task="task" 
      />
    </div>
  </div>
</template>

<script setup>
const filteredTasks = computed(() => {
  let result = tasks.value
  
  // 搜索过滤
  if (searchQuery.value) {
    result = result.filter(t => 
      t.title.toLowerCase().includes(searchQuery.value.toLowerCase())
    )
  }
  
  // 状态过滤
  if (filterStatus.value !== 'all') {
    result = result.filter(t => t.status === filterStatus.value)
  }
  
  return result
})
</script>
```

**估时**: 3-4 小时

---

### 优先级 3: 测试增强 (重要)

#### 7.7 集成 Appium UI 测试 🟡 HIGH
**目标**: 替代不稳定的 CDP 自动化

**实现步骤**:
1. 安装 Appium
   ```bash
   npm install -g appium
   npm install -D webdriverio @wdio/cli
   ```

2. 配置 wdio.conf.js
   ```javascript
   export const config = {
     port: 4723,
     capabilities: [{
       platformName: 'Android',
       'appium:deviceName': 'emulator-5554',
       'appium:app': '/path/to/app-debug.apk',
       'appium:automationName': 'UiAutomator2'
     }],
     specs: ['./test/e2e/**/*.spec.js']
   }
   ```

3. 编写测试用例
   ```javascript
   // test/e2e/login.spec.js
   describe('Login Flow', () => {
     it('should login successfully', async () => {
       const usernameInput = await $('~username-input')
       await usernameInput.setValue('admin')
       
       const passwordInput = await $('~password-input')
       await passwordInput.setValue('admin')
       
       const loginBtn = await $('~login-button')
       await loginBtn.click()
       
       await browser.pause(2000)
       
       const tasksList = await $('~tasks-list')
       await expect(tasksList).toBeDisplayed()
     })
   })
   ```

**验收标准**:
- [ ] 端到端测试自动化运行
- [ ] 覆盖登录、任务创建、列表查看
- [ ] 测试稳定可靠

**估时**: 6-8 小时

---

#### 7.8 Backend 单元测试 🟢 MEDIUM
**目标**: 提升 Backend 测试覆盖率

**实现**:
```go
// backend/internal/server/server_test.go
package server

import (
    "testing"
    "net/http/httptest"
)

func TestHandleTasks_List(t *testing.T) {
    s := &Server{
        taskStore: &mockTaskStore{},
    }
    
    req := httptest.NewRequest("GET", "/api/tasks", nil)
    w := httptest.NewRecorder()
    
    s.handleTasks(w, req)
    
    if w.Code != 200 {
        t.Errorf("expected 200, got %d", w.Code)
    }
}

func TestHandleTasks_Create(t *testing.T) {
    s := &Server{
        taskStore: &mockTaskStore{},
    }
    
    body := `{"title":"Test Task","status":"active","priority":"high"}`
    req := httptest.NewRequest("POST", "/api/tasks", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()
    
    s.handleTasks(w, req)
    
    if w.Code != 201 {
        t.Errorf("expected 201, got %d", w.Code)
    }
}
```

**验收标准**:
- [ ] 核心 API handler 测试覆盖
- [ ] 边界情况测试
- [ ] 错误处理测试

**估时**: 4-5 小时

---

### 优先级 4: 新功能 (可选)

#### 7.9 任务标签系统 🔵 LOW
**功能**: 为任务添加标签，支持多标签筛选

**Schema 更新**:
```sql
CREATE TABLE task_tags (
  id SERIAL PRIMARY KEY,
  task_id VARCHAR(255) REFERENCES tasks(id),
  tag VARCHAR(100) NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_task_tags_task ON task_tags(task_id);
CREATE INDEX idx_task_tags_tag ON task_tags(tag);
```

**API**:
```
POST /api/tasks/:id/tags   - 添加标签
DELETE /api/tasks/:id/tags/:tag - 删除标签
GET /api/tasks?tags=tag1,tag2 - 按标签筛选
```

**估时**: 5-6 小时

---

#### 7.10 任务模板 🔵 LOW
**功能**: 预设任务模板，快速创建常用任务

**实现**:
```vue
<!-- TaskTemplates.vue -->
<template>
  <div class="templates">
    <h3>快速创建</h3>
    <div class="template-grid">
      <div 
        v-for="tpl in templates" 
        :key="tpl.id"
        class="template-card"
        @click="createFromTemplate(tpl)"
      >
        <span class="icon">{{ tpl.icon }}</span>
        <span class="name">{{ tpl.name }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
const templates = [
  { id: 1, name: '代码审查', icon: '👀', priority: 'high' },
  { id: 2, name: 'Bug 修复', icon: '🐛', priority: 'high' },
  { id: 3, name: '功能开发', icon: '✨', priority: 'medium' },
  { id: 4, name: '文档更新', icon: '📝', priority: 'low' }
]
</script>
```

**估时**: 3-4 小时

---

## 📊 Phase 7 时间规划

### Sprint 1 (Week 1): 已知问题修复
- Day 1-2: 7.1 authStore 状态同步 (2-3h)
- Day 2-3: 7.2 BottomNav 布局优化 (1-2h)
- Day 3: 7.3 任务 ID 自动生成 (1h)
- Day 4-5: 测试验证 + Bug 修复

**里程碑**: 所有 Phase 6 已知问题解决

---

### Sprint 2 (Week 2): UI/UX 改进
- Day 1-2: 7.4 登录页面增强 (2-3h)
- Day 3: 7.5 任务卡片优化 (3-4h)
- Day 4-5: 7.6 任务筛选搜索 (3-4h)

**里程碑**: UI/UX 显著提升

---

### Sprint 3 (Week 3): 测试增强
- Day 1-3: 7.7 Appium UI 测试 (6-8h)
- Day 4-5: 7.8 Backend 单元测试 (4-5h)

**里程碑**: 测试覆盖率 85%+

---

### Sprint 4 (Week 4): 新功能 (可选)
- Day 1-3: 7.9 任务标签系统 (5-6h)
- Day 4-5: 7.10 任务模板 (3-4h)

**里程碑**: 功能增强完成

---

## 🎯 Phase 7 验收标准

### 功能验收
- [ ] authStore 状态同步正常
- [ ] BottomNav 布局无遮挡
- [ ] 任务 ID 自动生成
- [ ] 登录页面体验优化
- [ ] 任务筛选搜索可用
- [ ] UI 测试自动化运行

### 质量标准
- [ ] 无新增 BLOCKER/CRITICAL bug
- [ ] 测试覆盖率 ≥ 85%
- [ ] 所有 API 响应时间 < 200ms
- [ ] UI 渲染流畅 (60fps)

### 文档标准
- [ ] API 文档更新
- [ ] 测试文档更新
- [ ] 用户手册更新
- [ ] 变更日志记录

---

## 🚀 立即开始

### 第一步：创建 Phase 7 开发分支
```bash
git checkout -b feat/phase7-improvements
```

### 第二步：开始任务 7.1 (authStore 状态同步)
优先级最高，影响最大

### 第三步：迭代开发
每完成一个任务：
1. 本地测试
2. 提交 commit
3. 更新文档
4. 标记完成

---

**准备好开始 Phase 7 了吗？** 🚀

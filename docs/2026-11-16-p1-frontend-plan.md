# 知识库集成 - P1 前端任务详细规划

**规划时间**: 2026-11-16  
**目标交付**: 2026-11-23（一周）  
**P1 任务数**: 8 项  
**预计工作量**: 18-22 小时

---

## 📋 P1 任务清单及优先级

### Task P1.1: SyncStatusIndicator.vue 组件 ⭐⭐⭐
**优先级**: 最高  
**工作量**: 1.5-2 小时  
**依赖**: 无  
**交付物**: 1 个 Vue 3 组件 + 1 个 Pinia store action

#### 需求分析
```vue
<!-- 显示位置：NoteEditor.vue 顶部 -->
<!-- 状态展示 -->
✅ 已同步 (绿色)         → 绿圈 + "已同步"
🔄 同步中 (蓝色)         → 旋转圈 + "同步中..."
⚠️  冲突 (橙色)          → 警告三角 + "有冲突"，点击解决
❌ 同步失败 (红色)       → 红叉 + "同步失败"，显示错误信息
📴 离线模式 (灰色)       → 灰圈 + "离线"

<!-- 交互 -->
- 单击显示详情：最后同步时间、memora ID、错误消息
- 冲突时显示"解决冲突"按钮，打开 ConflictResolver 模态框
- 鼠标悬停显示完整状态文本
```

#### 实现步骤
1. 在 `knowledgeStore` 中添加 sync 状态字段
2. 实现 `fetchSyncStatus()` action - 定期轮询 `/api/sync/status`
3. 创建 `SyncStatusIndicator.vue` 组件
   - Props: `noteId`, `syncStatus`
   - Emits: `conflict-detected` (触发 ConflictResolver)
4. 在 NoteEditor 中集成组件
5. 样式：Tailwind CSS，响应式设计

#### 关键代码片段
```typescript
// knowledgeStore.ts 新增
const syncStatus = ref({
  noteId: '',
  status: 'synced',      // 'pending'|'synced'|'conflict'|'error'|'offline'
  lastSynced: null,
  errorMessage: '',
  memoraId: ''
})

async function fetchSyncStatus(noteId) {
  try {
    const res = await api.get('/api/sync/status')
    syncStatus.value = {
      ...res.data,
      status: mapStatus(res.data.syncStatus)
    }
  } catch (err) {
    syncStatus.value.status = 'offline'
  }
}
```

---

### Task P1.2: ConflictResolver.vue 对话框 ⭐⭐⭐
**优先级**: 高  
**工作量**: 2.5-3 小时  
**依赖**: P1.1  
**交付物**: 1 个模态框组件 + API 集成

#### 需求分析
```
┌─────────────────────────────────────────────┐
│  笔记冲突解决       ✕  │
├─────────────────────────────────────────────┤
│  本地版本 (2026-11-16 14:30) │
│  ───────────────────────────────────────   │
│  Q3 预算讨论    │
│  今天和客户讨论了 Q3 预算，需要在周五前...  │
│    │
│  vs  远程版本 (2026-11-16 12:00, memora)   │
│  ───────────────────────────────────────   │
│  Q3 预算讨论    │
│  与客户讨论 Q3 预算分配...     │
│    │
│  [ 接受本地版本 ]  [ 接受远程版本 ]         │
│  [ 手动合并 ]      [ 取消 ]         │
└─────────────────────────────────────────────┘
```

#### 实现步骤
1. 创建 `ConflictResolver.vue` 模态框组件
   - Props: `noteId`, `localContent`, `remoteContent`, `localTime`, `remoteTime`
   - Emits: `resolve` ({ resolution: 'local'|'remote'|'merge' })
2. 分栏显示本地和远程内容
3. 四个操作按钮：
   - "接受本地版本" → POST /api/sync/resolve/{id} { resolution: 'local' }
   - "接受远程版本" → POST /api/sync/resolve/{id} { resolution: 'remote' }
   - "手动合并" → 显示代码编辑器，允许用户编辑合并结果
   - "取消" → 关闭对话框
4. 样式：深色/浅色模式支持，Markdown 渲染

#### API 集成
```typescript
// knowledgeStore.ts 新增
async function resolveConflict(noteId, resolution) {
  const res = await api.post(`/api/sync/resolve/${noteId}`, {
    resolution  // 'local' | 'remote' | 'merge'
  })
  // 更新 syncStatus
  syncStatus.value.status = 'synced'
  return res.data
}
```

---

### Task P1.3: NoteLinksPanel.vue 面板 ⭐⭐⭐
**优先级**: 高  
**工作量**: 2-3 小时  
**依赖**: 无（但搭配 NoteDetail 使用）  
**交付物**: 1 个侧边栏面板组件

#### 需求分析
```
NoteLinksPanel 在 NoteDetail 右侧显示

┌──────────────────────────┐
│  🔗 关联笔记 (3)   │
├──────────────────────────┤
│  📌 手动链接 (2)  │
│  ├─ Q3 预算概览          │
│  │  高度相关，由我手动    │
│  │  创建    │
│  └─ 客户沟通记录          │
│       │
│  🤖 AI 推荐 (1)   │
│  └─ 预算审批流程          │
│    87% 相关度 (内容相似)  │
│       │
│  ↩️  反向链接 (0)        │
│  (无笔记链接到这里)       │
└──────────────────────────┘
```

#### 实现步骤
1. 创建 `NoteLinksPanel.vue` 侧边栏组件
2. 三个 tab：ManualLinks / SmartLinks / BackLinks
3. 每个链接显示：
   - 目标笔记标题
   - 摘要/预览文本
   - 元数据（AI 置信度、创建时间）
4. 交互：
   - 单击链接跳转到目标笔记
   - 悬停显示完整预览
   - 删除链接按钮（仅 ManualLinks）
5. API: GET /api/notes/{id}/related

#### 数据结构（已有）
```typescript
interface NoteLink {
  id: string
  note: Note
  linkType: 'related'|'reference'|'parent'|'child'|'bidirectional'
  confidence?: number  // AI 推荐的置信度 0-1
  createdBy: 'user'|'ai'
  reason?: string     // "内容相似度高"
}
```

---

### Task P1.4: WikiLinks 前端渲染 ⭐⭐
**优先级**: 中-高  
**工作量**: 1-2 小时  
**依赖**: 无  
**交付物**: 1 个 Markdown 插件 + 编辑器集成

#### 需求分析
在 Markdown 编辑器中显示 `[[keyword]]` 语法：
```
原文本：
  参考 [[Q3 预算]] 和 [[审批流程]]

渲染后：
  参考 <a href="#note/note-123" class="wiki-link">Q3 预算</a> 
      和 <a href="#note/note-456" class="wiki-link">审批流程</a>

特性：
- 蓝链颜色（#3b82f6）
- 悬停显示笔记预览
- 不存在的链接显示灰色 + [+] 创建按钮
- Cmd/Ctrl+Click 在新标签打开
```

#### 实现步骤
1. 在 `frontend/src/utils/wikilinks.ts` 创建渲染函数
   ```typescript
   export function renderWikiLinks(content, noteMap) {
     // 使用正则匹配 [[...]]
     // 替换为 <a href="#note/ID" class="wiki-link">title</a>
   }
   ```
2. 集成到 NoteEditor Markdown 预览
3. 创建 WikiLink 样式组件
4. 快速链接创建（右键菜单或工具栏）

---

### Task P1.5: 智能推荐集成 ⭐⭐
**优先级**: 中  
**工作量**: 1-2 小时  
**依赖**: P1.3  
**交付物**: NoteDetail 中的推荐面板

#### 需求分析
在 NoteDetail 页面底部显示：
```
💡 相关推荐 (基于内容相似度)
  • 预算审批流程 (87% 相关)
  • Q2 预算总结 (75% 相关)
  • 财务管理政策 (62% 相关)
```

#### 实现步骤
1. NoteDetail 加载时调用 API: GET /api/notes/{id}/related
2. 从响应中提取 smartLinks[]
3. 按 confidence 排序
4. 显示前 5 个
5. 单击链接跳转

---

### Task P1.6: 快捷键系统 ⭐⭐
**优先级**: 中  
**工作量**: 2-3 小时  
**依赖**: 无  
**交付物**: 全局快捷键管理器 + 3 个快捷键

#### 需求分析
```
Cmd+K (Mac) / Ctrl+K (Win)  → 打开全局搜索框
Cmd+N (Mac) / Ctrl+N (Win)  → 创建新笔记
Cmd+P (Mac) / Ctrl+P (Win)  → 打开命令面板

快捷键帮助：Cmd+? 显示所有快捷键列表
```

#### 实现步骤
1. 创建 `frontend/src/composables/useKeyboardShortcuts.ts`
   ```typescript
   export function useKeyboardShortcuts() {
     const registerShortcut = (key, modifiers, callback) => {}
     const unregisterShortcut = (key) => {}
   }
   ```
2. 在 App.vue 中注册快捷键
3. 创建快捷键帮助对话框
4. 支持自定义快捷键（可选）

---

### Task P1.7: 批量操作 UI ⭐⭐
**优先级**: 中  
**工作量**: 3-4 小时  
**依赖**: 无  
**交付物**: 批量选择工具栏 + 3-4 个操作

#### 需求分析
```
NoteList 支持多选模式
┌─────────────────────────────────────────┐
│ ☑ 全选  [已选 5 条]       │
├─────────────────────────────────────────┤
│ ☐ Q3 预算讨论      │
│ ☑ 会议纪要：技术方案评审     │
│ ☑ 项目进度更新      │
│ ☐ 个人日志  │
│ ☑ 客户需求分析      │
│ ☑ 技术方案设计      │
││
│ 批量操作: [移动▼] [标签▼] [导出▼] [删除]│
└─────────────────────────────────────────┘
```

#### 实现步骤
1. 在 NoteList 中添加复选框
2. 工具栏操作：
   - "移动到笔记本" → 下拉菜单选择
   - "添加标签" → 多选标签对话框
   - "导出" → Markdown/PDF 格式选择
   - "删除" → 确认对话框
3. 后端 API（需扩展）：
   ```
   POST /api/notes/batch/move { noteIds, notebookId }
   POST /api/notes/batch/tags { noteIds, tags, action: 'add'|'remove' }
   ```

---

### Task P1.8: 保存搜索 UI ⭐
**优先级**: 低  
**工作量**: 1.5 小时  
**依赖**: 无  
**交付物**: 搜索栏中的"保存"按钮 + 已保存搜索面板

#### 需求分析
```
搜索栏右侧：
  [Q3 预算] [按标签 ▼] [日期范围 ▼] [💾 保存搜索] 

点击"保存搜索"：
  ┌─────────────────────┐
  │ 搜索名称     │
  │ [Q3 预算相关]       │
  │       │
  │ [ 保存 ] [ 取消 ]   │
  └─────────────────────┘

已保存搜索列表（侧边栏）：
  🔖 已保存的搜索
  • Q3 预算相关
  • 客户需求分类
  • 技术文档
```

#### 实现步骤
1. SearchBar 中添加"保存"按钮
2. 创建"保存搜索"对话框
3. 调用 POST /api/knowledge/search/saved
4. 在侧边栏显示已保存搜索列表
5. 单击加载搜索

---

## 🗂️ 文件结构

```
frontend/src/
├── components/knowledge/
│   ├── SyncStatusIndicator.vue      [P1.1]
│   ├── ConflictResolver.vue         [P1.2]
│   ├── NoteLinksPanel.vue    [P1.3]
│   └── WikiLink.vue   [P1.4] (如需)
│
├── pages/
│   └── NoteDetailPage.vue (修改：集成 NoteLinksPanel + 推荐)
│
├── composables/
│   ├── useKeyboardShortcuts.ts      [P1.6]
│   └── useWikiLinks.ts       [P1.4] (如需)
│
├── utils/
│   └── wikilinks.ts   [P1.4]
│
├── stores/
│   └── knowledgeStore.ts (修改：添加 sync 相关状态)
│
└── api/
    └── knowledge.ts (修改：添加新 API 方法)
```

---

## 🔄 实现顺序建议

### Week 1 Day 1-2: 基础同步 UI
```
Priority 1 (必做，解锁其他功能):
  ✅ SyncStatusIndicator.vue
  ✅ ConflictResolver.vue
  ✅ knowledgeStore 同步状态字段
  
Outcome: 用户能看到同步状态和冲突解决 UI
```

### Week 1 Day 3: 笔记关联 UI
```
Priority 2 (提升内容探索):
  ✅ NoteLinksPanel.vue
  ✅ NoteDetail 集成
  
Outcome: 用户能浏览笔记间的关联
```

### Week 1 Day 4: WikiLinks + 推荐
```
Priority 3 (丰富内容):
  ✅ WikiLinks 渲染
  ✅ 智能推荐面板
  
Outcome: 用户能看到自动链接和推荐
```

### Week 1 Day 5: 快捷键 + 其他
```
Priority 4 (提升工作效率):
  ✅ 快捷键系统
  ✅ 保存搜索 UI
  ✅ 代码审查 + 测试
  
Outcome: 完整 P1 交付
```

---

## 📊 验收标准

### 每个组件的验收标准

**SyncStatusIndicator**
- [ ] 显示 5 种状态正确
- [ ] 与 API 同步状态一致
- [ ] 冲突时打开 ConflictResolver
- [ ] 样式响应式

**ConflictResolver**
- [ ] 本地/远程内容并排显示
- [ ] 三个解决选项都能调用 API
- [ ] API 响应后自动关闭并更新状态
- [ ] 错误提示清晰

**NoteLinksPanel**
- [ ] 显示三种链接类型
- [ ] 链接跳转正常
- [ ] AI 推荐的置信度显示
- [ ] 删除链接有确认

**WikiLinks**
- [ ] `[[keyword]]` 正确渲染为蓝链
- [ ] 不存在的链接灰显
- [ ] 悬停显示预览
- [ ] 支持跳转

**快捷键**
- [ ] Cmd+K 打开搜索
- [ ] Cmd+N 创建笔记
- [ ] Cmd+P 打开命令面板
- [ ] 在输入框中不触发

---

## 💡 技术参考

### 已有的 Vue 3 + Pinia 模式
查看 `frontend/src/components/knowledge/NotebookTree.vue` 作为参考实现

### Tailwind CSS 样式
- 按钮：`btn btn-primary`, `btn btn-outline`
- 颜色：`text-green-600` (同步), `text-red-600` (错误)
- 响应式：`md:w-1/3 lg:w-1/4`

### API 调用模式
查看 `frontend/src/api/knowledge.ts` 中的现有方法

---

## 🚀 交付清单

**P1 全部完成时**:
- [ ] 8 个 Vue 3 组件（+ 模态框）
- [ ] 3 个 Pinia actions
- [ ] 6 个新 API 调用
- [ ] 单元测试（推荐）
- [ ] 集成测试
- [ ] UI 文档

**预计代码量**:
- 组件: ~2,500-3,000 行
- Store 修改: ~300 行
- API 修改: ~150 行
- 总计: ~3,000 行新代码

---

*规划完成时间: 2026-11-16 14:45*  
*推荐开始时间: 2026-11-17 09:00*  
*预期完成时间: 2026-11-23 18:00*

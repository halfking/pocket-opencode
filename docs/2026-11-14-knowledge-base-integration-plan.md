# 个人知识库集成方案与实施规划

**版本**: v1.0  
**日期**: 2026-11-14  
**状态**: 设计方案（待实施）

## 📋 目录

- [1. 执行摘要](#1-执行摘要)
- [2. 现状分析](#2-现状分析)
- [3. 业界最佳实践](#3-业界最佳实践)
- [4. 集成方案设计](#4-集成方案设计)
- [5. 实施规划](#5-实施规划)
- [6. 技术架构](#6-技术架构)
- [7. API 设计](#7-api-设计)
- [8. 前端交互设计](#8-前端交互设计)
- [9. 数据模型](#9-数据模型)
- [10. 实施路线图](#10-实施路线图)

---

## 1. 执行摘要

### 1.1 目标

将 openpocket 现有的笔记（notes）和记忆库（memora）系统升级为完整的个人知识库管理系统，提供类似 Evernote 的使用体验，包括：

- **统一的知识入口**：整合语音笔记、文本笔记、会议记录、邮件摘要等多源知识
- **强大的搜索能力**：全文搜索、标签过滤、时间范围、智能推荐
- **灵活的组织方式**：笔记本（notebook）、标签（tags）、文件夹（workspace）层级
- **便捷的更新操作**：快速编辑、版本历史、冲突检测（SSOT）
- **无缝的同步**：本地缓存 + 云端存储（memora）双向同步

### 1.2 核心价值

1. **知识沉淀**：将碎片化的笔记、记忆转化为结构化的知识库
2. **高效检索**：混合搜索（BM25 + 向量 + 图谱）快速定位所需信息
3. **智能关联**：AI 自动建立知识间的关联（smart links）
4. **持续增值**：通过 AI 分类、总结、提取行动项增强笔记价值

### 1.3 关键指标

| 指标 | 目标 |
|------|------|
| 搜索响应时间 | < 500ms |
| 笔记同步成功率 | > 99% |
| 离线可用性 | 100%（本地缓存）|
| 搜索相关性 | > 85% |
| 用户满意度 | > 4.5/5.0 |

---

## 2. 现状分析

### 2.1 已有能力

openpocket 已经具备良好的基础：

#### 2.1.1 Notes 模块（本地笔记）
- **位置**: `backend/internal/notes/` + `frontend/src/api/notes.ts`
- **功能**:
  - CRUD 操作（创建、读取、更新、删除）
  - 领域分类（work/study/life/idea）
  - 标签系统
  - 语音笔记录制与转写
  - 即时总结（AI 生成）
  - 行动项提取
  - 自动记账集成
- **存储**: SQLite（本地缓存）
- **缺陷**:
  - 列表视图只显示前 200 字（snippet）
  - 无笔记本（notebook）概念
  - 搜索能力较弱（仅本地 SQLite FTS）
  - 无版本历史管理

#### 2.1.2 Memora 集成（云端记忆库）
- **位置**: `backend/internal/memora/` + `backend/internal/server/server_memora.go`
- **功能**:
  - 混合搜索（BM25 + 向量 + RRF）
  - 笔记推送到云端
  - 批量同步（幂等，基于内容哈希）
  - 健康检查与统计
- **存储**: 远程 memora 服务（FastAPI + PostgreSQL + Qdrant）
- **优势**:
  - 强大的向量检索
  - AI 增强（分类、SSOT、图谱）
- **缺陷**:
  - 前端集成度低（仅有 API，无 UI）
  - 同步是单向的（openpocket → memora）
  - 无冲突解决 UI

#### 2.1.3 Kxmemory 契约
- **文档**: `docs/2026-07-02-kxmemory-api-contract.md`
- **定义**: pocketd ↔ kxmemory 的完整 API 契约
- **端点**:
  - `/v1/notes/classify` - AI 分类
  - `/v1/emails/classify` - 邮件分类
  - `/v1/emails/daily-summary` - 每日摘要
  - `/v1/meetings/summary` - 会议摘要
- **状态**: 部分实现（client.go 有 3 个方法）

### 2.2 缺失能力

对比 Evernote 等成熟知识管理工具，主要缺失：

1. **统一的知识视图**
   - 无统一入口查看所有知识（notes + memora + emails + meetings）
   - 各模块分散，用户心智负担重

2. **笔记本组织**
   - 缺少笔记本（Notebook）概念
   - 无法按项目/主题组织笔记

3. **高级搜索**
   - 无搜索过滤器（日期范围、标签组合、笔记本范围）
   - 无保存的搜索（saved searches）
   - 无搜索历史

4. **笔记编辑体验**
   - 无富文本编辑器
   - 无 Markdown 预览
   - 无附件支持

5. **协作与分享**
   - 无分享链接
   - 无协作编辑
   - 无评论功能

6. **版本与历史**
   - 虽有 version_number，但无版本对比 UI
   - 无恢复到历史版本功能

---

## 3. 业界最佳实践

### 3.1 高星开源项目分析

根据 GitHub 调研，以下项目提供了优秀的知识管理模式：

#### 3.1.1 Joplin（43k+ stars）
- **架构**: Electron + React Native，本地优先
- **特点**:
  - 端到端加密
  - Markdown 编辑器
  - 笔记本 + 标签双维度组织
  - 多端同步（WebDAV/Dropbox/OneDrive）
  - 插件系统
- **借鉴点**:
  - **笔记本层级**：采用笔记本 + 子笔记本的树形结构
  - **同步策略**：基于时间戳的冲突检测
  - **搜索**: SQLite FTS5 全文索引

#### 3.1.2 Trilium Notes（26k+ stars）
- **架构**: Node.js + SQLite，树形笔记
- **特点**:
  - 灵活的树形结构（一条笔记可有多个父节点）
  - 关系图谱可视化
  - 脚本化自动化
  - 属性系统（key-value metadata）
- **借鉴点**:
  - **克隆笔记**（一条笔记出现在多个位置）
  - **关系类型**：定义笔记间的关系（is-a, related-to, references）
  - **模板系统**：快速创建特定类型的笔记

#### 3.1.3 Logseq（31k+ stars）
- **架构**: Clojure + DataScript，大纲式笔记
- **特点**:
  - 双向链接（bi-directional links）
  - 块引用（block references）
  - 查询语言（DataLog）
  - 本地优先，文件即数据库
- **借鉴点**:
  - **双向链接**：[[关键词]] 自动建立关联
  - **每日笔记**：按日期自动组织
  - **查询能力**：灵活的数据检索

#### 3.1.4 AppFlowy（50k+ stars）
- **架构**: Flutter + Rust，Notion 替代品
- **特点**:
  - 数据库视图（表格、看板、日历）
  - 实时协作
  - 自托管
- **借鉴点**:
  - **多视图模式**：列表/卡片/表格切换
  - **属性系统**：自定义字段

### 3.2 推荐采用的设计模式

综合业界实践，推荐以下设计：

| 维度 | 采用方案 | 来源 |
|------|---------|------|
| **组织结构** | Notebook（笔记本）+ Tags（标签）双维度 | Joplin |
| **搜索策略** | FTS + Vector + Graph 混合搜索 | Memora（已有）|
| **同步模式** | 本地优先 + 后台同步 + 冲突检测 | Joplin + openpocket SSOT |
| **关联方式** | AI 自动 smart_links + 手动双向链接 | Trilium + Logseq |
| **数据模型** | 笔记 + 块（blocks）+ 关系（links）| Trilium |
| **编辑体验** | Markdown 编辑器 + 实时预览 | Joplin |
| **离线能力** | SQLite 本地缓存 + 增量同步 | 当前架构保持 |

---

## 4. 集成方案设计

### 4.1 总体架构

```
┌─────────────────────────────────────────────────────────────┐
│                    Frontend (Vue 3)                         │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  Knowledge Hub (统一知识入口)                          │  │
│  │  - 搜索栏（全局搜索）                                   │  │
│  │  - Notebooks 侧边栏                                    │  │
│  │  - 笔记列表（可过滤）                                   │  │
│  │  - 笔记详情（编辑器）                                   │  │
│  │  - 标签云 & 关系图谱                                    │  │
│  └──────────────────────────────────────────────────────┘  │
│           ↓ HTTP/WebSocket                                 │
└─────────────────────────────────────────────────────────────┘
              ↓
┌─────────────────────────────────────────────────────────────┐
│                  Backend (Go - pocketd)                     │
│  ┌────────────────────┐  ┌──────────────────────────────┐  │
│  │  Knowledge API     │  │  Sync Orchestrator           │  │
│  │  - Unified Search  │  │  - Local ↔ Memora 双向同步   │  │
│  │  - CRUD + Version  │  │  - 冲突检测与解决            │  │
│  │  - Notebooks       │  │  - 增量同步队列              │  │
│  │  - Smart Links     │  │  - 离线操作缓存              │  │
│  └────────────────────┘  └──────────────────────────────┘  │
│           ↓                        ↓                        │
│  ┌────────────────────┐  ┌──────────────────────────────┐  │
│  │  Local Store       │  │  Memora Client               │  │
│  │  (SQLite)          │  │  (HTTP Client)               │  │
│  │  - notes           │  │  → memora service            │  │
│  │  - notebooks       │  │                              │  │
│  │  - tags            │  │                              │  │
│  │  - note_links      │  │                              │  │
│  │  - sync_state      │  │                              │  │
│  └────────────────────┘  └──────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
              ↓ (当 memora 已配置时)
┌─────────────────────────────────────────────────────────────┐
│              Memora Service (FastAPI)                       │
│  - 向量检索 (Qdrant)                                        │
│  - 知识图谱 (Neo4j)                                         │
│  - AI 增强 (分类/SSOT/摘要)                                 │
│  - Notebook API (/api/v2/notebook/*)                       │
└─────────────────────────────────────────────────────────────┘
```

### 4.2 核心设计原则

1. **本地优先（Local-First）**
   - 所有操作先在本地 SQLite 完成
   - 后台异步同步到 memora（如已配置）
   - 离线完全可用

2. **优雅降级（Graceful Degradation）**
   - 未配置 memora 时，所有功能仍可用（仅限本地）
   - memora 不可达时，自动切换到本地模式

3. **双向同步（Bi-directional Sync）**
   - openpocket → memora：推送本地笔记
   - memora → openpocket：拉取云端更新（新增）
   - 冲突检测：基于内容哈希 + 时间戳

4. **统一抽象（Unified Abstraction）**
   - 前端只看到统一的 "Knowledge Item"
   - 来源透明（notes/memora/emails/meetings）
   - 统一的搜索、标签、笔记本接口

---

## 5. 实施规划

### 5.1 Phase 1：数据模型与 Backend 增强（2 周）

#### 5.1.1 数据库 Schema 扩展

**新增表**：

```sql
-- Notebooks（笔记本）
CREATE TABLE IF NOT EXISTS notebooks (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    workspace_id TEXT,
    name TEXT NOT NULL,
    parent_id TEXT,  -- 支持嵌套笔记本
    icon TEXT,
    color TEXT,
    sort_order INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (parent_id) REFERENCES notebooks(id) ON DELETE CASCADE
);
CREATE INDEX idx_notebooks_user_workspace ON notebooks(user_id, workspace_id);
CREATE INDEX idx_notebooks_parent ON notebooks(parent_id);

-- Notes 表增强（添加 notebook_id）
ALTER TABLE notes ADD COLUMN notebook_id TEXT REFERENCES notebooks(id);
CREATE INDEX idx_notes_notebook ON notes(notebook_id);

-- Note Links（笔记间关联）
CREATE TABLE IF NOT EXISTS note_links (
    id TEXT PRIMARY KEY,
    source_note_id TEXT NOT NULL,
    target_note_id TEXT NOT NULL,
    link_type TEXT NOT NULL,  -- 'related', 'reference', 'parent', 'child', 'bidirectional'
    confidence REAL DEFAULT 1.0,  -- AI 生成的链接有置信度
    created_by TEXT NOT NULL,  -- 'user' | 'ai'
    created_at INTEGER NOT NULL,
    FOREIGN KEY (source_note_id) REFERENCES notes(id) ON DELETE CASCADE,
    FOREIGN KEY (target_note_id) REFERENCES notes(id) ON DELETE CASCADE,
    UNIQUE(source_note_id, target_note_id, link_type)
);
CREATE INDEX idx_note_links_source ON note_links(source_note_id);
CREATE INDEX idx_note_links_target ON note_links(target_note_id);

-- Sync State（同步状态追踪）
CREATE TABLE IF NOT EXISTS sync_state (
    note_id TEXT PRIMARY KEY,
    memora_id TEXT,  -- memora 侧的 ID
    content_hash TEXT NOT NULL,  -- SHA256(content)
    last_synced_at INTEGER,
    sync_status TEXT NOT NULL,  -- 'pending', 'synced', 'conflict', 'error'
    error_message TEXT,
    version INTEGER DEFAULT 1,
    FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE
);
CREATE INDEX idx_sync_state_status ON sync_state(sync_status);

-- Saved Searches（保存的搜索）
CREATE TABLE IF NOT EXISTS saved_searches (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    query TEXT NOT NULL,
    filters TEXT,  -- JSON: {tags, notebook, dateRange, domain}
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_saved_searches_user ON saved_searches(user_id);
```

#### 5.1.2 Backend API 扩展

**新增端点**：

```go
// Notebooks API
GET    /api/notebooks                    // 列出所有笔记本（树形）
POST   /api/notebooks                    // 创建笔记本
GET    /api/notebooks/:id                // 笔记本详情
PUT    /api/notebooks/:id                // 更新笔记本
DELETE /api/notebooks/:id                // 删除笔记本
GET    /api/notebooks/:id/notes          // 笔记本内的笔记

// Enhanced Notes API
GET    /api/notes                        // 增强查询（支持 notebook_id, tags[], dateRange）
POST   /api/notes/:id/links              // 创建笔记关联
DELETE /api/note-links/:id               // 删除关联
GET    /api/notes/:id/related            // 获取相关笔记（AI + 手动链接）
GET    /api/notes/:id/versions           // 版本历史
POST   /api/notes/:id/revert/:version    // 恢复到某版本

// Unified Search API
GET    /api/knowledge/search             // 统一搜索（notes + memora + emails + meetings）
POST   /api/knowledge/search/saved       // 保存搜索
GET    /api/knowledge/search/saved       // 获取保存的搜索

// Sync API Enhancement
POST   /api/sync/pull                    // 从 memora 拉取更新（新增）
GET    /api/sync/status                  // 同步状态概览
POST   /api/sync/resolve/:noteId         // 解决冲突
```

#### 5.1.3 Sync Orchestrator 实现

```go
// backend/internal/sync/orchestrator.go
type SyncOrchestrator struct {
    notesStore    *notes.Store
    memoraClient  *memora.Client
    syncState     *SyncStateStore
    queue         *SyncQueue
}

// 后台同步协程
func (o *SyncOrchestrator) Run(ctx context.Context) {
    ticker := time.NewTicker(5 * time.Minute)
    for {
        select {
        case <-ticker.C:
            o.syncPendingNotes(ctx)
            o.pullMemoraUpdates(ctx)
        case <-ctx.Done():
            return
        }
    }
}

// 推送待同步的笔记
func (o *SyncOrchestrator) syncPendingNotes(ctx context.Context) error {
    pending, err := o.syncState.GetPending()
    if err != nil {
        return err
    }
    for _, noteID := range pending {
        if err := o.syncOne(ctx, noteID); err != nil {
            log.Printf("sync note %s failed: %v", noteID, err)
            o.syncState.MarkError(noteID, err.Error())
        }
    }
    return nil
}

// 拉取 memora 更新（双向同步）
func (o *SyncOrchestrator) pullMemoraUpdates(ctx context.Context) error {
    if o.memoraClient == nil {
        return nil  // memora 未配置
    }
    
    lastSync := o.syncState.GetLastPullTime()
    updates, err := o.memoraClient.GetUpdatedSince(ctx, lastSync)
    if err != nil {
        return err
    }
    
    for _, item := range updates {
        if err := o.mergeFromMemora(ctx, item); err != nil {
            log.Printf("merge from memora failed: %v", err)
        }
    }
    return nil
}
```

### 5.2 Phase 2：前端统一知识中心（2 周）

#### 5.2.1 新增页面组件

```
frontend/src/pages/
  KnowledgeHub.vue              # 统一知识入口
  NotebookView.vue              # 笔记本视图
  NoteEditor.vue                # 增强的笔记编辑器
  SearchResults.vue             # 搜索结果页
  KnowledgeGraph.vue            # 关系图谱可视化

frontend/src/components/knowledge/
  NotebookTree.vue              # 笔记本树
  NoteList.vue                  # 笔记列表
  TagCloud.vue                  # 标签云
  SearchBar.vue                 # 全局搜索栏
  SyncStatusIndicator.vue       # 同步状态指示器
  ConflictResolver.vue          # 冲突解决对话框
  NoteLinksPanel.vue            # 笔记关联面板
```

#### 5.2.2 核心 UI 流程

**搜索流程**：
1. 用户在搜索栏输入关键词
2. 防抖 300ms 后触发搜索
3. 并发调用：
   - 本地 SQLite FTS
   - Memora 混合搜索（如已配置）
4. 结果合并去重（按相关性排序）
5. 显示结果列表（标注来源）

**笔记编辑流程**：
1. 用户点击笔记进入编辑器
2. Markdown 编辑器（支持实时预览）
3. 自动保存（防抖 2s）
4. 保存后标记为待同步
5. 后台同步到 memora

**同步状态指示**：
- ✅ 已同步（绿色）
- 🔄 同步中（蓝色动画）
- ⚠️ 冲突（橙色，可点击解决）
- ❌ 同步失败（红色，显示错误）
- 📴 离线模式（灰色）

### 5.3 Phase 3：高级功能（2 周）

#### 5.3.1 双向链接

- 支持 `[[关键词]]` 语法自动建立链接
- 反向链接面板（哪些笔记链接到当前笔记）
- 孤立笔记检测

#### 5.3.2 智能推荐

- 基于当前笔记内容推荐相关笔记
- 调用 memora 的向量检索
- 显示在侧边栏

#### 5.3.3 批量操作

- 批量移动到笔记本
- 批量添加标签
- 批量导出（Markdown/PDF）

#### 5.3.4 快捷操作

- 键盘快捷键（Cmd+K 全局搜索）
- 快速笔记（Cmd+N）
- 命令面板（Cmd+P）

---

## 6. 技术架构

### 6.1 技术栈

| 层次 | 技术 | 用途 |
|------|------|------|
| **前端** | Vue 3 + TypeScript | UI 框架 |
| **编辑器** | Tiptap / Monaco Editor | Markdown 编辑 |
| **图谱** | D3.js / Cytoscape.js | 关系可视化 |
| **状态** | Pinia | 全局状态管理 |
| **后端** | Go 1.27+ | API 服务 |
| **本地存储** | SQLite | 离线数据 |
| **云端** | Memora (FastAPI) | AI 增强 + 向量检索 |
| **搜索** | SQLite FTS5 + Qdrant | 混合检索 |
| **同步** | 基于哈希的增量同步 | 数据一致性 |

### 6.2 关键技术决策

#### 6.2.1 为什么选择本地优先？

- **离线可用**：移动端网络不稳定
- **低延迟**：本地操作无网络往返
- **数据主权**：用户数据保留在本地
- **成本优化**：减少云端调用

#### 6.2.2 为什么选择 SQLite FTS5？

- **嵌入式**：无需独立搜索服务
- **高性能**：移动端可接受的性能
- **全文索引**：支持中文分词（使用 simple tokenizer）
- **成熟稳定**：SQLite 久经考验

#### 6.2.3 为什么选择双向同步？

- **多端协同**：用户可能在多设备编辑
- **云端备份**：防止数据丢失
- **AI 增强**：云端有更强的 AI 能力
- **协作预留**：未来可扩展协作功能

---

## 7. API 设计

### 7.1 统一搜索 API

#### Request

```http
GET /api/knowledge/search?q=关键词&source=all&notebook=xxx&tags=tag1,tag2&dateFrom=2026-01-01&dateTo=2026-12-31&limit=50&offset=0
```

**Query Parameters**:

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| q | string | 是 | 搜索关键词 |
| source | enum | 否 | 'all' \| 'notes' \| 'memora' \| 'emails' \| 'meetings'，默认 'all' |
| notebook | string | 否 | 笔记本 ID 过滤 |
| tags | string | 否 | 逗号分隔的标签列表 |
| domain | enum | 否 | 'work' \| 'study' \| 'life' \| 'idea' |
| dateFrom | string | 否 | 起始日期（ISO 8601）|
| dateTo | string | 否 | 结束日期（ISO 8601）|
| limit | int | 否 | 返回数量，默认 50 |
| offset | int | 否 | 分页偏移，默认 0 |

#### Response

```json
{
  "results": [
    {
      "id": "note-xxx",
      "type": "note",
      "source": "local",
      "title": "Q3 预算讨论",
      "snippet": "今天和客户讨论了 Q3 预算...",
      "highlights": ["<em>Q3</em>", "<em>预算</em>"],
      "notebook": {
        "id": "nb-xxx",
        "name": "工作笔记"
      },
      "tags": ["预算", "客户"],
      "domain": "work",
      "createdAt": "2026-11-01T10:30:00Z",
      "updatedAt": "2026-11-01T10:35:00Z",
      "score": 0.95
    },
    {
      "id": "mem-yyy",
      "type": "memory",
      "source": "memora",
      "title": "预算审批流程",
      "snippet": "公司预算审批需要三级审批...",
      "highlights": ["<em>预算</em>"],
      "tags": ["流程", "预算"],
      "createdAt": "2026-10-15T14:20:00Z",
      "score": 0.87
    }
  ],
  "total": 23,
  "limit": 50,
  "offset": 0,
  "searchTime": 234
}
```

### 7.2 Notebooks API

#### 创建笔记本

```http
POST /api/notebooks
Content-Type: application/json

{
  "name": "工作笔记",
  "parentId": null,
  "icon": "📁",
  "color": "#3498db"
}
```

#### Response

```json
{
  "id": "nb-xxx",
  "userId": "local",
  "workspaceId": null,
  "name": "工作笔记",
  "parentId": null,
  "icon": "📁",
  "color": "#3498db",
  "sortOrder": 0,
  "createdAt": "2026-11-14T10:00:00Z",
  "updatedAt": "2026-11-14T10:00:00Z",
  "noteCount": 0
}
```

### 7.3 Note Links API

#### 创建笔记关联

```http
POST /api/notes/note-xxx/links
Content-Type: application/json

{
  "targetNoteId": "note-yyy",
  "linkType": "related"
}
```

#### 获取相关笔记

```http
GET /api/notes/note-xxx/related
```

#### Response

```json
{
  "manualLinks": [
    {
      "id": "link-123",
      "note": {
        "id": "note-yyy",
        "title": "相关笔记",
        "snippet": "...",
        "tags": []
      },
      "linkType": "related",
      "createdBy": "user",
      "createdAt": "2026-11-14T10:00:00Z"
    }
  ],
  "smartLinks": [
    {
      "id": "link-456",
      "note": {
        "id": "note-zzz",
        "title": "AI 推荐笔记",
        "snippet": "...",
        "tags": []
      },
      "linkType": "related",
      "confidence": 0.82,
      "createdBy": "ai",
      "reason": "内容相似度高"
    }
  ],
  "backlinks": [
    {
      "id": "note-aaa",
      "title": "引用了当前笔记",
      "snippet": "参考 [[Q3 预算讨论]]",
      "createdAt": "2026-11-13T10:00:00Z"
    }
  ]
}
```

---

## 8. 前端交互设计

### 8.1 知识中心主页布局

```
┌─────────────────────────────────────────────────────────────┐
│  [🔍 搜索框]                       [新建] [同步状态: ✅]      │
├──────────┬──────────────────────────────────────────────────┤
│          │                                                   │
│ 📚 笔记本 │  📋 所有笔记                        [列表|卡片]  │
│          │                                                   │
│ 📁 工作   │  ┌─────────────────────────────────────────┐   │
│   └ 项目A │  │ Q3 预算讨论                     工作 💼 │   │
│ 📁 学习   │  │ 今天和客户讨论了 Q3 预算，需要在...     │   │
│ 📁 生活   │  │ #预算 #客户            2026-11-01 10:30  │   │
│          │  └─────────────────────────────────────────┘   │
│ 🏷️ 标签   │  ┌─────────────────────────────────────────┐   │
│ · 预算(23)│  │ 会议纪要：技术方案评审         工作 💼 │   │
│ · 客户(15)│  │ 讨论了新功能的技术实现方案...           │   │
│ · 学习(32)│  │ #技术 #评审            2026-11-01 14:00  │   │
│          │  └─────────────────────────────────────────┘   │
│ 🔗 关系图 │                                                   │
│          │  [ 加载更多... ]                                │
│          │                                                   │
└──────────┴───────────────────────────────────────────────────┘
```

### 8.2 笔记编辑器界面

```
┌─────────────────────────────────────────────────────────────┐
│ ← 返回   Q3 预算讨论                [保存] [•••]             │
├─────────────────────────────────────────────────────────────┤
│ 📁 工作笔记 / 项目A                                          │
│ 🏷️ #预算 #客户 #Q3              [添加标签]                  │
├───────────────────────────┬─────────────────────────────────┤
│                           │                                 │
│ # Q3 预算讨论             │  [ 预览 ]                       │
│                           │                                 │
│ ## 会议信息               │  Q3 预算讨论                    │
│ - 时间：2026-11-01        │  ═══════════════                │
│ - 参与：张经理、李总      │                                 │
│                           │  会议信息                       │
│ ## 讨论要点               │  • 时间：2026-11-01             │
│ 1. 预算总额确认           │  • 参与：张经理、李总           │
│ 2. 各部门分配             │                                 │
│                           │  讨论要点                       │
│ ## 行动项                 │  1. 预算总额确认                │
│ - [ ] 周五前提交预算表    │  2. 各部门分配                  │
│                           │                                 │
│ 相关笔记：[[预算审批流程]]│  行动项                         │
│                           │  ☐ 周五前提交预算表             │
├───────────────────────────┴─────────────────────────────────┤
│ 💡 相关推荐:                                                │
│   • 预算审批流程 (87% 相关)                                 │
│   • Q2 预算总结 (75% 相关)                                  │
├─────────────────────────────────────────────────────────────┤
│ 🔗 链接: 2 个  |  📅 创建: 2026-11-01  |  ✍️ 修改: 刚刚    │
└─────────────────────────────────────────────────────────────┘
```

### 8.3 搜索结果页

```
┌─────────────────────────────────────────────────────────────┐
│ [🔍 Q3 预算]                                   [保存搜索]   │
├─────────────────────────────────────────────────────────────┤
│ 筛选器: [所有来源 ▼] [所有笔记本 ▼] [标签 ▼] [日期 ▼]     │
├─────────────────────────────────────────────────────────────┤
│ 找到 23 个结果 (0.234 秒)                                   │
│                                                             │
│ 📝 Q3 预算讨论                           本地笔记  95% 匹配 │
│    今天和客户讨论了 <em>Q3</em> <em>预算</em>，需要...      │
│    📁 工作笔记 · #预算 #客户 · 2026-11-01                   │
│                                                             │
│ 🧠 预算审批流程                          Memora   87% 匹配 │
│    公司<em>预算</em>审批需要三级审批流程...                 │
│    #流程 #预算 · 2026-10-15                                │
│                                                             │
│ 📧 RE: Q3 预算确认                       邮件     82% 匹配 │
│    关于<em>Q3</em>的<em>预算</em>分配，我已经...            │
│    来自: 张经理 · 2026-10-20                               │
│                                                             │
│ 🎙️ 项目讨论会议                         会议     78% 匹配 │
│    会议中提到<em>Q3</em><em>预算</em>需要调整...            │
│    2026-10-18 14:00                                        │
│                                                             │
│ [ 加载更多... ]                                            │
└─────────────────────────────────────────────────────────────┘
```

---

## 9. 数据模型

### 9.1 核心实体关系

```
User
  └─ Workspace
       ├─ Notebook (树形结构)
       │    └─ Note
       │         ├─ Tags (多对多)
       │         ├─ NoteLinks (多对多，带类型)
       │         └─ SyncState (一对一)
       ├─ Email
       ├─ Meeting
       └─ SavedSearch
```

### 9.2 Note 实体增强

```go
type Note struct {
    // 基础字段（已有）
    ID          string    `json:"id"`
    UserID      string    `json:"userId"`
    WorkspaceID string    `json:"workspaceId,omitempty"`
    Title       string    `json:"title"`
    Content     string    `json:"content"`  // 完整正文
    Snippet     string    `json:"snippet"`  // 列表摘要
    
    // 组织字段（新增）
    NotebookID  string    `json:"notebookId,omitempty"`
    Notebook    *Notebook `json:"notebook,omitempty"`
    
    // 分类字段（已有）
    ContentType string    `json:"contentType"`  // voice|text|mixed
    Domain      string    `json:"domain,omitempty"`  // work|study|life|idea
    Tags        []string  `json:"tags,omitempty"`
    
    // 语音字段（已有）
    AudioPath      string `json:"audioPath,omitempty"`
    AudioDuration  int    `json:"audioDuration,omitempty"`
    CreatedByVoice bool   `json:"createdByVoice"`
    
    // 版本字段（已有）
    IsLatest      bool  `json:"isLatest"`
    VersionNumber int   `json:"versionNumber"`
    ParentID      string `json:"parentId,omitempty"`  // 版本链
    
    // 关联字段（新增）
    Links         []NoteLink `json:"links,omitempty"`
    BackLinks     []NoteLink `json:"backlinks,omitempty"`
    
    // 同步字段（新增）
    SyncState     *SyncState `json:"syncState,omitempty"`
    
    // 时间字段（已有）
    CreatedAt int64 `json:"createdAt"`
    UpdatedAt int64 `json:"updatedAt"`
}
```

### 9.3 Notebook 实体

```go
type Notebook struct {
    ID          string    `json:"id"`
    UserID      string    `json:"userId"`
    WorkspaceID string    `json:"workspaceId,omitempty"`
    Name        string    `json:"name"`
    ParentID    string    `json:"parentId,omitempty"`  // 支持嵌套
    Icon        string    `json:"icon,omitempty"`       // emoji
    Color       string    `json:"color,omitempty"`      // hex color
    SortOrder   int       `json:"sortOrder"`
    CreatedAt   int64     `json:"createdAt"`
    UpdatedAt   int64     `json:"updatedAt"`
    
    // 统计字段（动态计算）
    NoteCount   int       `json:"noteCount,omitempty"`
    Children    []Notebook `json:"children,omitempty"`  // 子笔记本
}
```

### 9.4 NoteLink 实体

```go
type NoteLink struct {
    ID           string  `json:"id"`
    SourceNoteID string  `json:"sourceNoteId"`
    TargetNoteID string  `json:"targetNoteId"`
    LinkType     string  `json:"linkType"`      // related|reference|parent|child|bidirectional
    Confidence   float64 `json:"confidence"`    // AI 生成的置信度
    CreatedBy    string  `json:"createdBy"`     // user|ai
    CreatedAt    int64   `json:"createdAt"`
    
    // 展开字段（查询时可选）
    TargetNote   *Note   `json:"targetNote,omitempty"`
    Reason       string  `json:"reason,omitempty"`  // AI 推荐理由
}
```

### 9.5 SyncState 实体

```go
type SyncState struct {
    NoteID       string `json:"noteId"`
    MemoraID     string `json:"memoraId,omitempty"`  // memora 侧 ID
    ContentHash  string `json:"contentHash"`          // SHA256
    LastSyncedAt int64  `json:"lastSyncedAt,omitempty"`
    SyncStatus   string `json:"syncStatus"`           // pending|synced|conflict|error
    ErrorMessage string `json:"errorMessage,omitempty"`
    Version      int    `json:"version"`
}
```

---

## 10. 实施路线图

### 10.1 时间线（6 周）

```
Week 1-2: Phase 1 - Backend 数据模型与 API
├─ W1: 数据库 Schema 设计与迁移
│  ├─ notebooks 表
│  ├─ note_links 表
│  ├─ sync_state 表
│  ├─ saved_searches 表
│  └─ notes 表增强（notebook_id）
│
└─ W2: Backend API 实现
   ├─ Notebooks API (CRUD)
   ├─ Enhanced Notes API
   ├─ Note Links API
   ├─ Unified Search API
   └─ Sync Orchestrator

Week 3-4: Phase 2 - Frontend 知识中心
├─ W3: 核心页面组件
│  ├─ KnowledgeHub.vue（主页）
│  ├─ NotebookView.vue
│  ├─ NoteEditor.vue（Markdown）
│  └─ SearchResults.vue
│
└─ W4: 交互组件与状态管理
   ├─ NotebookTree.vue
   ├─ TagCloud.vue
   ├─ SyncStatusIndicator.vue
   ├─ ConflictResolver.vue
   └─ Pinia Stores

Week 5-6: Phase 3 - 高级功能与优化
├─ W5: 高级功能
│  ├─ 双向链接（[[keyword]]）
│  ├─ 智能推荐
│  ├─ 批量操作
│  └─ 快捷键
│
└─ W6: 测试与优化
   ├─ 单元测试
   ├─ 集成测试
   ├─ 性能优化
   └─ 文档完善
```

### 10.2 里程碑

| 里程碑 | 交付物 | 验收标准 |
|--------|--------|---------|
| **M1: 数据层完成** | 数据库 Schema + Migration 脚本 | 所有表创建成功，索引正常 |
| **M2: API 层完成** | 所有后端 API 端点 | Postman 测试通过，响应时间 < 200ms |
| **M3: UI 原型** | 知识中心主要页面 | 可导航，布局合理，响应式 |
| **M4: 搜索可用** | 统一搜索功能 | 本地 + memora 搜索结果正确合并 |
| **M5: 同步可用** | 双向同步 | 离线编辑后联网自动同步，冲突正确检测 |
| **M6: Beta 发布** | 完整功能 | 通过 E2E 测试，用户可正常使用 |

### 10.3 风险与缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **SQLite FTS 中文分词效果差** | 搜索体验下降 | 中 | 1) 使用 simple tokenizer + 自定义分词<br/>2) 优先级提升 memora 搜索 |
| **Memora API 不稳定** | 同步失败 | 低 | 1) 本地优先架构保证离线可用<br/>2) 重试机制 + 错误队列 |
| **前端编辑器性能问题（大文档）** | 编辑卡顿 | 中 | 1) 虚拟滚动<br/>2) 懒加载预览<br/>3) Web Worker 渲染 |
| **双向同步冲突频繁** | 用户困扰 | 中 | 1) 智能合并策略<br/>2) 清晰的冲突 UI<br/>3) 一键接受本地/远程 |
| **移动端存储空间不足** | 同步失败 | 低 | 1) 可配置的缓存策略<br/>2) 自动清理旧版本 |

### 10.4 成功指标

| 指标 | 基线 | 目标 | 测量方法 |
|------|------|------|---------|
| **搜索准确率** | N/A | > 85% | 人工标注 100 个查询的相关性 |
| **搜索响应时间** | N/A | < 500ms | 后端 metrics（P95）|
| **同步成功率** | N/A | > 99% | sync_state 表统计 |
| **离线可用率** | N/A | 100% | 断网测试所有功能 |
| **崩溃率** | < 1% | < 0.5% | 埋点统计 |
| **用户活跃度** | 基线 | +30% | DAU 统计 |

---

## 11. 附录

### 11.1 参考文档

- [Joplin 架构文档](https://joplinapp.org/spec/)
- [Trilium API 设计](https://github.com/zadam/trilium/wiki/API-documentation)
- [Logseq 数据模型](https://docs.logseq.com/)
- [Memora API 契约](./2026-07-02-kxmemory-api-contract.md)
- [openpocket 功能清单](./2026-09-08-feature-inventory.md)

### 11.2 术语表

| 术语 | 定义 |
|------|------|
| **Notebook** | 笔记本，用于组织笔记的容器，支持嵌套 |
| **Note** | 笔记，知识的基本单元 |
| **Smart Links** | AI 自动生成的笔记间关联 |
| **Bidirectional Links** | 双向链接，`[[keyword]]` 语法 |
| **SSOT** | Single Source of Truth，单一事实来源，用于冲突检测 |
| **Memora** | 云端记忆库服务，提供 AI 增强和向量检索 |
| **Sync State** | 同步状态，追踪笔记的本地-云端同步情况 |
| **FTS** | Full-Text Search，全文搜索 |
| **RRF** | Reciprocal Rank Fusion，倒数排名融合算法 |

### 11.3 示例数据

**笔记示例（JSON）**：

```json
{
  "id": "note-2026111401",
  "userId": "local",
  "workspaceId": null,
  "notebookId": "nb-work",
  "title": "Q3 预算讨论",
  "content": "# Q3 预算讨论\n\n## 会议信息\n- 时间：2026-11-01 10:30\n- 参与：张经理、李总\n\n## 讨论要点\n1. 预算总额确认为 500 万\n2. 各部门分配比例\n\n## 行动项\n- [ ] 周五前提交预算表\n\n参考：[[预算审批流程]]",
  "snippet": "今天和客户讨论了 Q3 预算，需要在周五前确认...",
  "contentType": "mixed",
  "domain": "work",
  "tags": ["预算", "客户", "Q3"],
  "audioPath": "/data/audio/2026/11/note-2026111401.wav",
  "audioDuration": 180,
  "createdByVoice": true,
  "isLatest": true,
  "versionNumber": 1,
  "createdAt": 1730451000000,
  "updatedAt": 1730451000000,
  "syncState": {
    "noteId": "note-2026111401",
    "memoraId": "mem-xxx",
    "contentHash": "abc123...",
    "lastSyncedAt": 1730451000000,
    "syncStatus": "synced",
    "version": 1
  }
}
```

---

**文档结束**

*本文档由 openpocket 知识库集成项目组编写，版本 v1.0，2026-11-14*

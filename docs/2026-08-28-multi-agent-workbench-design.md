# 多智能体工作台设计方案（2026-08-28）

## 1. 数据模型

### 1.1 TypeScript (Frontend)

```typescript
// frontend/src/types/chatAgent.ts
export interface ChatAgent {
  id: string                  // 'engineering-ai-engineer'（文件名去 .md）
  name: string                // 'AI 工程师'（YAML name）
  description: string         // YAML description
  department: string          // 'engineering'（目录名）
  emoji?: string              // YAML emoji（'🤖'）
  color?: string              // YAML color（'purple'）
  systemPrompt: string        // Markdown 正文全文（去掉 YAML frontmatter）
  isBuiltin: boolean          // true=内置（agency-agents-zh），false=用户自定义
  createdAt: number
  updatedAt: number
}

export interface ChatAgentSyncPayload {
  version: number             // 乐观锁版本号
  agents: ChatAgent[]         // 仅 isBuiltin=false 的自定义角色
}
```

### 1.2 Go (Backend)

```go
// backend/internal/chatagent/agent.go
package chatagent

type Agent struct {
    ID           string `json:"id" db:"id"`
    WorkspaceID  string `json:"workspace_id" db:"workspace_id"`
    Name         string `json:"name" db:"name"`
    Description  string `json:"description" db:"description"`
    Department   string `json:"department" db:"department"`
    Emoji        string `json:"emoji" db:"emoji"`
    Color        string `json:"color" db:"color"`
    SystemPrompt string `json:"system_prompt" db:"system_prompt"`
    IsBuiltin    bool   `json:"is_builtin" db:"is_builtin"`
    CreatedAt    int64  `json:"created_at" db:"created_at"`
    UpdatedAt    int64  `json:"updated_at" db:"updated_at"`
}
```

### 1.3 数据库 Schema

#### 1.3.1 PostgreSQL Schema（Acc 云端模式）

```sql
CREATE TABLE IF NOT EXISTS chat_agents (
  id            TEXT PRIMARY KEY,
  workspace_id  TEXT NOT NULL,
  name          TEXT NOT NULL,
  description   TEXT NOT NULL DEFAULT '',
  department    TEXT NOT NULL,
  emoji         TEXT,
  color         TEXT,
  system_prompt TEXT NOT NULL,
  is_builtin    BOOLEAN NOT NULL DEFAULT false,
  created_at    BIGINT NOT NULL,
  updated_at    BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chat_agents_ws ON chat_agents(workspace_id);
CREATE INDEX IF NOT EXISTS idx_chat_agents_dept ON chat_agents(department);
CREATE INDEX IF NOT EXISTS idx_chat_agents_builtin ON chat_agents(is_builtin);
```

#### 1.3.2 SQLite Schema（单机离线模式）

```sql
CREATE TABLE IF NOT EXISTS chat_agents (
  id            TEXT PRIMARY KEY,
  workspace_id  TEXT NOT NULL,
  name          TEXT NOT NULL,
  description   TEXT NOT NULL DEFAULT '',
  department    TEXT NOT NULL,
  emoji         TEXT,
  color         TEXT,
  system_prompt TEXT NOT NULL,
  is_builtin    INTEGER NOT NULL DEFAULT 0,  -- SQLite 用 INTEGER 存布尔（0/1）
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chat_agents_ws ON chat_agents(workspace_id);
CREATE INDEX IF NOT EXISTS idx_chat_agents_dept ON chat_agents(department);
CREATE INDEX IF NOT EXISTS idx_chat_agents_builtin ON chat_agents(is_builtin);
```

**存储策略（`backend/cmd/pocketd/main.go:initChatAgentStores`）**：

1. **优先 PostgreSQL**（Acc 云端模式）：
   - PG Store + SyncStore（完整功能：CRUD + 跨设备同步）
   - PG Store + nil sync（CRUD 可用，sync 端点 503）

2. **降级 SQLite**（单机离线模式）：
   - 无 PG 或 PG init 失败 → `modernc.org/sqlite`（pure Go，no CGO）
   - SQLite Store + nil sync（CRUD 可用，280 个内置角色，sync 端点 503）
   - DB 路径：`{dataDir}/chat_agents.sqlite`（WAL 模式）

3. **完全失败**：
   - PG 与 SQLite 均失败 → nil store（所有 chatagent 端点返回 503）

参见 [生物识别认证与 SQLite 离线模式](./2026-08-28-biometric-auth-and-sqlite-fallback.md) 了解 SQLiteStore 实现细节。

### 1.4 前端数据模型扩展

```typescript
// Conversation 扩展
export interface Conversation {
  id: string
  title: string
  model: string
  mode: ChatMode
  messages: ChatMsg[]
  agentId?: string           // 👈 新增：绑定的智能体 id
  customSystemPrompt?: string // 👈 新增：会话级覆盖（最高优先级）
  createdAt: number
  updatedAt: number
}

// ChatSettings 扩展
export interface ChatSettings {
  temperature: number
  maxTokens: number
  systemPrompt: string       // 保留作为「无角色时」的兜底
  defaultModel: string
  modelByModality: Record<ModalityKey, string>
  defaultAgentId?: string    // 👈 新增：新建会话时默认角色
}
```

## 2. System Prompt 优先级（三层）

```
1. 会话的 customSystemPrompt（用户在会话设置里临时覆盖）
   ↓ 无
2. 会话绑定的 agent.systemPrompt（选角色后自动带入）
   ↓ 无
3. 全局 settings.systemPrompt（无角色时兜底，保持向后兼容）
   ↓ 无
4. 空（纯对话，无角色设定）
```

实现：`aiChatStore.buildRequestMessages()` 改为：
```typescript
function buildRequestMessages(conv: Conversation, ...): ChatMessage[] {
  const out: ChatMessage[] = []
  
  // 优先级：会话覆盖 > 角色 systemPrompt > 全局兜底
  let systemPrompt = ''
  if (conv.customSystemPrompt?.trim()) {
    systemPrompt = conv.customSystemPrompt.trim()
  } else if (conv.agentId) {
    const agent = agents.value.find(a => a.id === conv.agentId)
    if (agent) systemPrompt = agent.systemPrompt.trim()
  } else if (settings.value.systemPrompt.trim()) {
    systemPrompt = settings.value.systemPrompt.trim()
  }
  
  if (systemPrompt) {
    out.push({ role: 'system', content: systemPrompt })
  }
  
  // ... 其余历史消息
}
```

## 3. 后端 API 端点

### 3.1 本地 CRUD（SQLite）

```
GET    /api/chat-agents                    # 列出所有角色（内置 + 自定义）
       Query: ?department=engineering      # 可选部门筛选
       Response: { agents: Agent[] }

GET    /api/chat-agents/:id                # 单个角色详情
       Response: Agent

POST   /api/chat-agents                    # 创建自定义角色
       Body: { name, description, department, emoji?, color?, system_prompt }
       Response: Agent
       Audit: chat_agent.create

PUT    /api/chat-agents/:id                # 更新自定义角色（仅 isBuiltin=false）
       Body: 同 POST（局部更新）
       Response: Agent
       Audit: chat_agent.update

DELETE /api/chat-agents/:id                # 删除自定义角色（仅 isBuiltin=false）
       Response: { success: true }
       Audit: chat_agent.delete
```

### 3.2 Acc 云端同步（可选，PG）

```
POST   /api/chat-agents/sync/upload        # 上传自定义角色列表
       Body: { version: number, agents: Agent[] }
       Response: { version: number, uploaded_count: number }
       Audit: chat_agent.sync_upload
       存储：PostgreSQL chat_agent_sync 表（workspace_id + user_id 隔离）

GET    /api/chat-agents/sync/download      # 拉取云端角色列表
       Response: { version: number, agents: Agent[] }
       Audit: chat_agent.sync_download
```

PostgreSQL schema（Acc 云端）：
```sql
CREATE TABLE IF NOT EXISTS chat_agent_sync (
  workspace_id TEXT NOT NULL,
  user_id      TEXT NOT NULL,
  version      INTEGER NOT NULL,
  agents_json  TEXT NOT NULL,  -- JSON array of Agent
  updated_at   BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, user_id)
);
```

## 4. 内置角色导入（启动时自动）

### 4.1 解析器

```go
// backend/internal/chatagent/importer.go
package chatagent

import (
    "os"
    "path/filepath"
    "strings"
    "gopkg.in/yaml.v3"
)

type FrontMatter struct {
    Name        string `yaml:"name"`
    Description string `yaml:"description"`
    Emoji       string `yaml:"emoji"`
    Color       string `yaml:"color"`
}

// ParseAgentFile 从 agency-agents-zh/*.md 提取角色
func ParseAgentFile(path string) (*Agent, error) {
    raw, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    
    content := string(raw)
    // 提取 YAML frontmatter
    if !strings.HasPrefix(content, "---\n") {
        return nil, fmt.Errorf("missing YAML frontmatter")
    }
    
    parts := strings.SplitN(content[4:], "\n---\n", 2)
    if len(parts) != 2 {
        return nil, fmt.Errorf("invalid frontmatter format")
    }
    
    var fm FrontMatter
    if err := yaml.Unmarshal([]byte(parts[0]), &fm); err != nil {
        return nil, err
    }
    
    systemPrompt := strings.TrimSpace(parts[1])
    
    // 从路径提取 id 与 department
    base := filepath.Base(path)
    id := strings.TrimSuffix(base, ".md")
    dept := filepath.Base(filepath.Dir(path))
    
    return &Agent{
        ID:           id,
        WorkspaceID:  "", // 内置角色 workspace_id 为空（全局共享）
        Name:         fm.Name,
        Description:  fm.Description,
        Department:   dept,
        Emoji:        fm.Emoji,
        Color:        fm.Color,
        SystemPrompt: systemPrompt,
        IsBuiltin:    true,
        CreatedAt:    time.Now().Unix(),
        UpdatedAt:    time.Now().Unix(),
    }, nil
}

// ImportBuiltinAgents 从 agency-agents-zh/ 批量导入
func (s *Store) ImportBuiltinAgents(ctx context.Context, repoPath string) error {
    // 检查是否已导入过（表不为空）
    var count int
    if err := s.pool.QueryRow(ctx, 
        "SELECT COUNT(*) FROM chat_agents WHERE is_builtin = 1",
    ).Scan(&count); err != nil {
        return err
    }
    if count > 0 {
        log.Printf("[chatagent] %d builtin agents already imported, skipping", count)
        return nil
    }
    
    log.Printf("[chatagent] importing builtin agents from %s", repoPath)
    
    var agents []*Agent
    err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
        if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
            return nil
        }
        
        // 跳过非角色文件（README/CATALOG/AGENT-LIST 等）
        base := filepath.Base(path)
        if strings.ToUpper(base) == base || base == "UPSTREAM.md" {
            return nil
        }
        
        agent, err := ParseAgentFile(path)
        if err != nil {
            log.Printf("[chatagent] skip %s: %v", path, err)
            return nil
        }
        agents = append(agents, agent)
        return nil
    })
    if err != nil {
        return err
    }
    
    // 批量插入
    tx, err := s.pool.Begin(ctx)
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx)
    
    for _, a := range agents {
        if err := s.createTx(ctx, tx, a); err != nil {
            return fmt.Errorf("insert %s: %w", a.ID, err)
        }
    }
    
    if err := tx.Commit(ctx); err != nil {
        return err
    }
    
    log.Printf("[chatagent] imported %d builtin agents", len(agents))
    return nil
}
```

### 4.2 启动时自动导入

```go
// backend/cmd/pocketd/main.go
func main() {
    // ... 现有初始化 ...
    
    agentStore := chatagent.NewStore(pool)
    if err := agentStore.Init(ctx); err != nil {
        log.Fatalf("chatagent store init: %v", err)
    }
    
    // 启动时自动导入内置角色（agency-agents-zh 仓库路径从环境变量读取）
    if repoPath := os.Getenv("POCKET_AGENTS_REPO_PATH"); repoPath != "" {
        if err := agentStore.ImportBuiltinAgents(ctx, repoPath); err != nil {
            log.Printf("WARN: import builtin agents failed: %v", err)
        }
    } else {
        log.Printf("INFO: POCKET_AGENTS_REPO_PATH not set, builtin agents import skipped")
    }
    
    srv := server.New(cfg, ..., agentStore, ...)
    // ...
}
```

## 5. 前端 UI 扩展

### 5.1 智能体选择器（替代单纯的模型选择器）

**位置**：AIChatView 顶栏点击「选择角色」

**布局**：
```
┌─────────────────────────────────────┐
│ 选择智能体角色                       │
│ [搜索框: 按名称/部门/技能搜索]        │
├─────────────────────────────────────┤
│ 🔧 工程部 (35)                      │
│  🤖 AI 工程师                       │
│  🏗️  软件架构师                     │
│  ...                                │
│ 📢 营销部 (36)                      │
│  📱 抖音策略师                       │
│  🛍️  电商运营师                     │
│  ...                                │
│ ✨ 我的自定义 (3)                   │
│  💡 专属代码评审助手                 │
│  📝 技术文档写作专家                 │
│  ...                                │
│ [+ 创建自定义角色]                   │
└─────────────────────────────────────┘
```

点击角色 → `conv.agentId = selectedAgent.id`，顶栏显示角色 emoji + name。

### 5.2 角色库管理页（`/agents`）

**路由**：`/agents`（底部导航新增「🧠 智能体」Tab，或放在设置二级页）

**功能**：
- 按部门筛选（engineering/marketing/design/...）
- 卡片展示：emoji + name + description
- 点击卡片 → 详情页（显示完整 systemPrompt + 可「应用到当前会话」）
- 自定义角色 CRUD（新增/编辑/删除）
- 同步按钮（上传/下载自定义角色到 Acc 云端）

### 5.3 会话设置扩展

**位置**：AIChatView 设置 sheet

**新增卡片**：
```
┌─────────────────────────────────────┐
│ 当前角色                             │
│ 🤖 AI 工程师                         │
│ 精通机器学习模型开发与部署...         │
│                                     │
│ [切换角色] [临时覆盖 System Prompt]  │
└─────────────────────────────────────┘
```

「临时覆盖」打开文本框 → 修改后存 `conv.customSystemPrompt`。

## 6. 安全与隔离

| 维度 | 机制 |
|------|------|
| 认证 | 所有 `/api/chat-agents/*` 需 JWT |
| 租户隔离 | 查询时过滤 `workspace_id = 当前用户 workspace`；内置角色 `workspace_id = ''` 全局共享 |
| 内置角色保护 | `is_builtin = 1` 的角色拒绝 UPDATE/DELETE（返回 403） |
| 审计 | CREATE/UPDATE/DELETE 写 audit `chat_agent.{action}` |
| 配额 | 单 workspace 自定义角色上限 100（CREATE 时检查） |
| 注入防护 | systemPrompt 不做特殊字符转义（是功能而非漏洞，用户可写任意 prompt） |

## 7. 迁移路径（向后兼容）

### Phase 0（当前实现，2026-08-28）
- `Conversation` 无 `agentId`，`settings.systemPrompt` 全局唯一
- 前端 localStorage：`pocket:ai-chat:v1`

### Phase 1（多智能体落地，2026-08-29）
- 新增 `chat_agents` 表 + 内置角色导入（启动时自动）
- `Conversation` schema 兼容扩展（`agentId?` / `customSystemPrompt?` optional）
- 前端升级 localStorage version → `pocket:ai-chat:v2`（向下兼容 v1）
- 前端同时支持「无角色」与「有角色」：
  - 旧会话加载时 `agentId` 为空 → 沿用全局 `settings.systemPrompt`
  - 新会话默认绑定 `settings.defaultAgentId`（可选）

### Phase 2（Acc 同步上线，2026-09 或更晚）
- 后端 PostgreSQL 新增 `chat_agent_sync` 表
- 前端设置页增加「同步我的自定义角色」开关
- 上传：`POST /api/chat-agents/sync/upload` 含 version 乐观锁
- 下载：`GET /api/chat-agents/sync/download` → merge 到本地 SQLite

## 8. 实施清单

### 8.1 后端（3-4 小时）
- [ ] `internal/chatagent/` 新包：Agent struct + Store (SQLite CRUD)
- [ ] `internal/chatagent/importer.go`：ParseAgentFile + ImportBuiltinAgents
- [ ] `internal/server/server_chatagent.go`：5 个端点（GET/POST/PUT/DELETE + sync）
- [ ] `cmd/pocketd/main.go`：启动时调 ImportBuiltinAgents
- [ ] 单测：`chatagent/importer_test.go`（解析 1 个 .md 文件）+ `server/server_chatagent_test.go`（CRUD hermetic）

### 8.2 前端（4-5 小时）
- [ ] `types/chatAgent.ts`：ChatAgent interface
- [ ] `api/chatAgent.ts`：5 个 API 客户端
- [ ] `stores/chatAgentStore.ts`：Pinia store（本地 cache + CRUD + 按部门分组）
- [ ] `features/agents/AgentLibraryView.vue`：角色库浏览 + 自定义 CRUD
- [ ] `features/ai-chat/AgentSelectorSheet.vue`：角色选择 sheet（部门分组 + 搜索）
- [ ] `features/ai-chat/aiChatStore.ts`：扩展 Conversation/Settings schema + buildRequestMessages 三层优先级
- [ ] `features/ai-chat/AIChatView.vue`：顶栏改为「角色选择器」+ 设置 sheet 增加「当前角色」卡片

### 8.3 文档与测试（1-2 小时）
- [ ] `docs/2026-08-29-multi-agent-workbench.md`：架构/数据模型/迁移路径/安全边界
- [ ] 端到端验证：导入内置角色 → 前端选角色 → 发消息验证 system prompt 注入 → 创建自定义角色 → 切换角色

**总计**：8-11 小时（可分 2-3 个会话完成）

## 9. 配置

```bash
# .env.example 新增
POCKET_AGENTS_REPO_PATH=/path/to/agency-agents-zh  # 内置角色库路径（启动时导入）
```

如果路径未设置，启动时跳过导入（用户仍可手动创建自定义角色）。

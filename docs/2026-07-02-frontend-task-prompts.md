# 🤖 提示词集 v1 — 前端 App MVP 闭环

**生成日期**: 2026-07-02
**配套蓝图**: `docs/2026-07-02-frontend-app-blueprint-v1.md`
**项目根**: `/Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/`

> 6 个独立提示词，可并行分给不同 agent 或开发者。每个提示词都明确：
> - 任务范围（不要超出）
> - 必读文件
> - 必遵守的代码风格（不引入未使用的依赖）
> - 必产出文件清单
> - 自验命令

每个提示词都引用蓝图、明确边界、互不冲突。

---

## 提示词 1 — NoteDetailView + NoteEditView

**目标**: 实现笔记的详情展示 + 新建/编辑表单

**必读**:
- 蓝图 §3.1 笔记模块契约
- `frontend/src/features/notes/notes-store.ts`（已有 8 个方法，含 `getNote` / `updateNote` / `createNote`）
- `frontend/src/features/notes/NoteListView.vue`（已存在，作为参考风格）
- `frontend/src/native/vector.ts`（如需做相关推荐）

**实现要求**:

### NoteDetailView
- 路由: `/notes/:id`（在 `router-mobile.ts` 注册，包裹 `<AppLayout>`，`meta.bottomNav=true`）
- 数据源: 调 `notesStore.getNote(id)`（已是 Promise<LocalNote | null>）
- 显示:
  - 顶部: 标题（`title || content.slice(0,40)`）+ 返回按钮
  - 中部: Markdown 渲染的 `content`（用 `marked` 或 `markdown-it` —— **`npm install` 时只装 marked，其他不要**）
  - 元信息条: 域色标 + 创建时间 + tags（chips）+ 创建方式（语音/文字）
  - 关联推荐区: 调 `notesStore.searchSemantic(note.content, 5)` 排除自身 ID，显示前 4 条
  - 操作按钮: 编辑 / 删除 / 重新分类
- 编辑按钮跳 `/notes/:id/edit`
- 删除: confirm → `notesStore.deleteNote(id)` → router.back
- 重新分类: `http POST /api/notes/{id}/classify`（后端已注册但当前 stub），loading 提示

### NoteEditView
- 路由: `/notes/:id/edit`（编辑模式）和 `/notes/new`（新建模式，`id === 'new'` 时进入）
- 复用 `VoiceRecorderWidget`（在 `NoteListView` 用的那个）
- 表单字段: title, content (textarea 20 行), domain (4 个 chip: work/study/life/idea), tags (逗号分隔)
- 保存:
  - 新建: `notesStore.createNote({title, content, domain, tags, audioPath, audioDurationMs})`
  - 编辑: `notesStore.updateNote(id, {title, content, domain, tags})`
  - 保存成功 → router.push(`/notes/${id}`)
- 路由配置: `meta.canGoBack=true, bottomNav=false`（编辑时隐藏底部导航）
- 自动聚焦到 title 输入框

**必产出文件**:
- `frontend/src/features/notes/NoteDetailView.vue`
- `frontend/src/features/notes/NoteEditView.vue`
- 修改 `frontend/src/app/router-mobile.ts` 加两条路由

**npm 依赖**:
```bash
cd frontend && npm install marked
```
仅装 `marked`，不要装其他 markdown 库。

**自验命令**:
```bash
cd frontend && npx vite build 2>&1 | grep -E "built|error"
# 预期: ✓ built
```

**代码风格**:
- 全部用 `<script setup lang="ts">`
- 样式用 CSS variables（`var(--brand-primary)` 等），遵循 `frontend/src/styles/tokens.css`
- 不引入新 store；如需局部状态用 `ref()` / `reactive()`

---

## 提示词 2 — EmailDetailView + EmailSummaryView + EmailAccountSetup

**目标**: 实现邮件的详情、每日总结、账户配置三个 UI

**必读**:
- 蓝图 §3.2 邮件模块契约
- `frontend/src/features/email/emails-store.ts`（已有 8 个方法；**注意**: 当前**没有** `getEmail` / `getAccount` —— 见提示词 6 补全）
- `frontend/src/features/email/EmailInboxView.vue`（已存在）
- `frontend/src/api/email.ts`（后端云模式 API 客户端）
- `frontend/src/api/http.ts`（统一 fetch 包装）

**实现要求**:

### EmailDetailView
- 路由: `/email/:id`
- 数据: 调 `emailsStore.listEmails` 找到对应 ID（**当前 emails-store 没有 getEmail** —— 临时方案: `listEmails` + filter，等提示词 6 补 `getEmail` 后替换）
- 显示:
  - 顶部: 发件人（姓名 + 邮箱）+ 日期 + 主题
  - 主体: snippet + (如有) AI 摘要卡片
  - 元信息: category 标签 + importance 星标 + hasAttachments
  - 操作: 标已读 / 标星 / 转 todo（todo 暂时只是 alert）
- 页面打开时若 `m.isRead === false` 自动调 `emailsStore.markRead(id, true)`

### EmailSummaryView
- 路由: `/email/summary`（列表） + `/email/summary/:date`（详情）
- 列表: 调 `emailApi.listSummaries()`，显示日期 + 重要邮件数 + 摘要前 100 字
- 详情: 调 `emailApi.getSummary(date)`（**后端 endpoint 是 `/api/email/summaries/{date}`** —— 路径要对齐；先按蓝图，endpoint 缺失时返回 404 自行处理"暂未生成"提示）
- Markdown 渲染（复用 marked 库，提示词 1 装过）

### EmailAccountSetup
- 路由: `/email/accounts`（账户列表 + 添加）
- 列表: 调 `emailsStore.listAccounts()`，显示账户名 + 邮箱 + 上次同步时间
- 添加向导（点击"+"展开/弹窗）:
  - 4 个预设 IMAP 模板按钮: Gmail (imap.gmail.com:993) / QQ (imap.qq.com:993) / 163 (imap.163.com:993) / Outlook (outlook.office365.com:993)
  - 选模板后填: 显示名 / 邮箱地址 / 密码
  - "测试连接"按钮: 调 `emailApi.addAccount({...})` 然后 `emailApi.syncNow()`，loading + 成功/失败提示
  - 成功后调 `emailsStore.saveAccount()` 写本地
- 已有账户操作: 编辑 / 删除（调对应方法）
- **此页面前提**: 提示词 6 会补 `emailsStore.getAccount` —— 不要假设它已存在

**必产出文件**:
- `frontend/src/features/email/EmailDetailView.vue`
- `frontend/src/features/email/EmailSummaryView.vue`
- `frontend/src/features/email/EmailAccountSetup.vue`
- 修改 `frontend/src/app/router-mobile.ts` 加 4 条路由（detail / summary list / summary detail / accounts）

**自验命令**:
```bash
cd frontend && npx vite build 2>&1 | grep -E "built|error"
# 预期: ✓ built
```

**代码风格**: 同提示词 1。

---

## 提示词 3 — VaultEntryView（含复制 30s 自动清空）

**目标**: 密码箱条目详情 + 复制安全机制

**必读**:
- 蓝图 §3.3 密码箱契约
- `frontend/src/features/vault/vault-store.ts`（已有 8 个方法，含 `getEntry` / `saveEntry` / `deleteEntry`）
- `frontend/src/features/vault/VaultListView.vue`（已存在，作为参考）

**实现要求**:

### VaultEntryView
- 路由: `/vault/:id`（详情）和 `/vault/:id/edit`（编辑）
- 解密数据: 调 `vaultStore.getEntry(id)` → `VaultEntry { data: { password, notes, totpSecret, customFields } }`
- 显示:
  - 顶部: 图标（按 category）+ 标题（e.g. "GitHub"）+ 返回
  - 字段: title / username / url / password / notes
  - 密码字段默认遮罩为 `••••••••`，点击 👁 图标切换显示/遮罩
  - 密码字段右侧: 📋 复制按钮 → `navigator.clipboard.writeText(password)` + 30 秒倒计时（`setTimeout` + `clearInterval`），30 秒后自动 `navigator.clipboard.writeText('')`（用空字符串覆盖）+ 弹"已清空"提示
  - TOTP 字段（如有）: 显示当前 6 位动态码（用 `otpauth` 库或手写 HOTP/TOTP；**仅装 `otpauth`**）
  - 操作: 编辑 / 删除
- 删除: confirm → `vaultStore.deleteEntry(id)` → router.push('/vault')
- 编辑模式（`/vault/:id/edit`）: 表单预填 → 保存调 `vaultStore.saveEntry({id, ...})`

**安全细节**:
- 复制提示必须**醒目**（红色 toast），30 秒计时器在页面前台运行
- 页面 unmount / router 离开时清掉计时器 + 立即清空剪贴板

**npm 依赖**:
```bash
cd frontend && npm install otpauth
```

**必产出文件**:
- `frontend/src/features/vault/VaultEntryView.vue`（详情 + 编辑合一）
- 修改 `frontend/src/app/router-mobile.ts` 加 2 条路由

**自验命令**:
```bash
cd frontend && npx vite build 2>&1 | grep -E "built|error"
# 预期: ✓ built
```

**代码风格**: 同前。

---

## 提示词 4 — Router Guard 增强 + WS 事件路由层

**目标**: 加未登录/未初始化龙虾守卫；把 WS 事件分发到对应 store

**必读**:
- 蓝图 §5 实时事件 + §7 路由权限
- `frontend/src/app/router-mobile.ts`（`beforeEach` 已有 localStorage 检查）
- `frontend/src/api/websocket.ts`（已实现 `on` / `off` / `send`）
- `frontend/src/stores/auth.ts`（已有 `useAuthStore`）
- `frontend/src/native/lobster-init.ts`（已实现 `isLobsterReady()`）
- `frontend/src/features/notes/notes-store.ts`（已有 reload 方法逻辑）
- `frontend/src/features/email/emails-store.ts`
- `frontend/src/features/vault/vault-store.ts`

**实现要求**:

### 1. router guard 增强（`router-mobile.ts:beforeEach`）
```typescript
import { useAuthStore } from '@/stores/auth'
import { isLobsterReady } from '@/native/lobster-init'

router.beforeEach((to, from, next) => {
  const auth = useAuthStore()
  
  // 需要登录但未登录
  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return next('/login')
  }
  
  // 需要龙虾初始化但未初始化（/vault /notes /email /meetings /chat 都需要）
  if (to.meta.requiresLobster && !isLobsterReady()) {
    return next('/login')
  }
  
  next()
})
```
- 给相关路由加 `meta.requiresLobster = true`（`/notes`、`/email`、`/vault`、`/meetings`、未来 `/chat`）
- 已登录 + 已初始化但访问 `/login` 时跳到 `/ai`（避免重复登录）

### 2. WS 事件路由层（**新增** `frontend/src/services/ws-bus.ts`）
- 集中管理所有 WS 事件订阅，避免散落在各 view
- 暴露 `on(type, cb)` / `off(type, cb)` API
- 启动时在 `main.ts` 一次性订阅所有已知事件类型，路由到对应 store:
  - `note.created` → `notesStore.handleServerEvent(note)`（**store 新增此方法**，不重新插入，仅刷新缓存列表）
  - `email.classified` → `emailsStore.handleClassifiedEvent({email_id, category, importance, summary})`（更新内存中对应 email 的字段）
  - `vault.synced` → 不需要本地响应（用户主动触发）

### 3. 改造各 store 加 `handle*Event` 方法
- `notes-store.ts` 加 `handleServerEvent(note)`: 找到 `note.id` 对应的内存条目，更新字段
- `emails-store.ts` 加 `handleClassifiedEvent({email_id, category, importance, summary})`: 找到 `email_id` 对应的内存条目，更新
- 这些方法**不写磁盘**（本地数据已是最新），只更新内存

**必产出文件**:
- `frontend/src/services/ws-bus.ts`（新增）
- 修改 `frontend/src/app/router-mobile.ts`（guard + meta）
- 修改 `frontend/src/main.ts`（启动 ws-bus）
- 修改 `frontend/src/features/notes/notes-store.ts`（加 `handleServerEvent`）
- 修改 `frontend/src/features/email/emails-store.ts`（加 `handleClassifiedEvent`）
- 修改 `frontend/src/stores/auth.ts`（如需要）

**自验命令**:
```bash
cd frontend && npx vite build 2>&1 | grep -E "built|error"
# 预期: ✓ built
```

**关键原则**:
- 事件路由层**不引入新依赖**
- store 的 `handle*Event` 方法必须**幂等**（重复调用不报错）

---

## 提示词 5 — 后端补端点（notes 详情/分类、email summaries 详情、vault restore）

**目标**: 补齐蓝图 §2.2 列的"必须补"端点，让前端有 API 可调

**必读**:
- 蓝图 §2.2 必补清单
- `backend/internal/server/server.go` 的 `Handler()` 方法（注册路由的位置）
- `backend/internal/server/server_assistant.go`（已有相关 handler 风格）
- `backend/internal/notes/{note,store}.go` 和 `task/task.go` 等已有 store
- `backend/internal/vault/{store,model}.go`
- `backend/internal/email/{store,model}.go`

**实现要求**:

### 端点 1: `POST /api/notes/{id}/classify` — 触发单条 AI 分类
- 在 `server_assistant.go` 加 `handleNoteClassify` 方法
- 接受路径参数 `id`
- 复用现有 `classifyNoteAsync` 但改成同步（因为是手动触发，要等结果）
- 调 `s.kxmemory.ClassifyNote(...)`，回写 `s.notesStore.Upsert(...)` 分类结果
- 响应: `ClassifyNoteResponse`（kxmemory 类型）
- 路由: `mux.HandleFunc("/api/notes/{id}/classify", ...)`，handler 内从路径解析 id

### 端点 2: `GET /api/email/summaries/{date}` — 单日总结详情
- 在 `server_assistant.go` 加 `handleEmailSummaryDetail` 方法
- 路径: `/api/email/summaries/{date}` 形式
- 现有 `handleEmailSummaryOps` 是占位，重写它
- 从 email_store 查 `daily_summaries` 表（**当前 schema 没有这张表** —— 需要先在 `internal/email/store.go` 加 migrate:
  ```sql
  CREATE TABLE IF NOT EXISTS daily_summaries (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    summary_date TEXT NOT NULL,
    total_count INTEGER,
    important_count INTEGER,
    content TEXT NOT NULL,
    action_items JSONB,
    created_at BIGINT NOT NULL,
    UNIQUE(user_id, summary_date)
  );
  ```
- 调 `s.emailStore.GetSummaryByDate(ctx, userID, date)`（新增方法）
- 若不存在返回 404 + `{"error":"summary not generated yet"}`

### 端点 3: `POST /api/email/sync/status` — 抓取状态
- 简单的状态返回: 各账户 `last_synced_at` / `last_synced_uid` / 待抓取数估算
- 调 `s.emailStore.GetSyncStatus(ctx, userID)`（新增方法，返回 `[]AccountSyncStatus`）
- 响应: `{statuses: [{accountId, displayName, lastSyncedAt, lastSyncedUid, enabled, ...}]}`

### 端点 4: `POST /api/vault/sync/{version}/restore` — 恢复历史版本
- 在 `handleVaultSync` 的 switch 里加 case
- `r.Method == http.MethodPost && sub == "{version}/restore"`
- 解析 version（int）
- 调 `s.vaultStore.GetByVersion(ctx, userID, version)`（新增方法，返回 blob + 标记新 current）
- 成功后 WS 广播 `vault.restored`
- 响应: `{"ok": true, "version": N}`

### 端点 5（可选）: `GET /api/vault/sync/versions/{version}` — 单版本详情
- 返回该版本的 blob 完整内容（加密的）

**必产出文件**:
- 修改 `backend/internal/email/store.go`（migrate + 新增方法）
- 修改 `backend/internal/vault/store.go`（新增 `GetByVersion` / `MarkCurrent`）
- 修改 `backend/internal/server/server_assistant.go`（新增 handler）
- 修改 `backend/internal/server/server.go`（注册新路由）

**自验命令**:
```bash
cd backend && GOPROXY=https://goproxy.cn,direct go build ./... 2>&1 | head -5 && echo "BUILD OK"
GOPROXY=https://goproxy.cn,direct go test ./internal/server -run TestHealthz -count=1 2>&1 | tail -2
```

**端到端测试**（确保新路由可达）:
```bash
# 启动 pocketd
POCKET_HTTP_PORT=18099 /tmp/pocketd &
PID=$!
sleep 2
/usr/bin/curl -s -m 3 -w '\n[HTTP %{http_code}]\n' -X POST 'http://127.0.0.1:18099/api/notes/nonexistent/classify' | head
/usr/bin/curl -s -m 3 -w '\n[HTTP %{http_code}]\n' 'http://127.0.0.1:18099/api/email/summaries/2026-07-02' | head
kill $PID 2>/dev/null
```

**注意**:
- 不修改现有 schema（用 `IF NOT EXISTS`）
- 不破坏现有 handler 的行为
- 错误返回用现有的 `writeError` helper

---

## 提示词 6 — Store 补全 + kxmemory 触发

**目标**: 补全前端 store 缺失方法；让笔记/邮件创建后自动触发 kxmemory 分类

**必读**:
- 蓝图 §3.1 §3.2
- `frontend/src/features/notes/notes-store.ts`（已有 8 个方法）
- `frontend/src/features/email/emails-store.ts`（已有 8 个方法）
- `frontend/src/features/vault/vault-store.ts`（已有 8 个方法）
- `frontend/src/api/http.ts`

**实现要求**:

### 1. `emails-store.ts` 新增 3 个方法
```typescript
// 提示词 2 的 EmailDetailView 依赖
export async function getEmail(id: string): Promise<LocalEmail | null> {
  // 调 localDB.queryOne('SELECT * FROM local_emails WHERE id = ?', [id])
  // 返回单条
}

// 提示词 2 的 EmailAccountSetup 依赖
export async function getAccount(id: string): Promise<EmailAccount | null> {
  // 类似
}

export async function updateAccount(id: string, patch: Partial<EmailAccount>): Promise<void> {
  // localDB.run UPDATE
}
```

### 2. `vault-store.ts` 新增 1 个方法
```typescript
// 提示词 3 的编辑模式依赖
export async function updateEntry(
  id: string, 
  patch: Partial<VaultEntry>
): Promise<void> {
  // 类似 saveEntry 但只 UPDATE 不 INSERT
}
```

### 3. `notes-store.ts` 改造 createNote 增加 kxmemory 触发
在 `createNote` 末尾加（不影响主流程）:
```typescript
// 异步触发服务端 kxmemory 分类（可选，失败不报错）
http(`/api/notes/${n.id}/classify`, {method: 'POST'}).catch(() => {})
```

### 4. `emails-store.ts` 改造 upsertEmail 增加 kxmemory 触发
类似: 调 `/api/emails/{id}/classify`（**注意: 邮件分类走 `/api/emails/sync` 批量 endpoint，不是单条** —— 这里不要触发单条，让 handleEmailSync 批量处理）

### 5. `vault-store.ts` 加缓存计数 getter
```typescript
export function getCacheStats(): { vectorCount: number; ftsEnabled: boolean } {
  return {
    vectorCount: vectorIndex.size(),
    ftsEnabled: true,  // schema 总是创建 FTS5
  }
}
```
（用于将来的"本地存储使用情况"页）

### 6. (可选) `meetings-store.ts` 加 `getMeetingWithSegments(meetingId)`
合并查 meeting + segments 返回 `{meeting, segments}`

**必产出文件**:
- 修改 `frontend/src/features/notes/notes-store.ts`
- 修改 `frontend/src/features/email/emails-store.ts`
- 修改 `frontend/src/features/vault/vault-store.ts`
- （可选）修改 `frontend/src/features/meetings/meetings-store.ts`

**自验命令**:
```bash
cd frontend && npx vite build 2>&1 | grep -E "built|error"
# 预期: ✓ built
```

**代码风格**:
- 现有 `notes-store.ts` 已用 `localDB.queryOne<T>(sql, vals)` / `localDB.run(sql, vals)` 模式 —— 保持一致
- 字段映射（snake_case DB 列 → camelCase TS 字段）通过 `rowTo*` 私有函数（如 `rowToEmail`），新方法**也走这个映射**

**重要**: 不要修改现有签名 / 删除现有方法，只增量添加。

---

## 协调说明

- **提示词 1 / 2 / 3** 是并行前端 UI 任务（互不依赖）
- **提示词 4** 改路由 + 加 WS bus（依赖 1/2/3 完成的路由注册）
- **提示词 5** 改后端（前端调用 5 的端点才生效）
- **提示词 6** 改 store（2 / 3 的 UI 依赖 6 的新方法）

**建议执行顺序**:
1. 提示词 6（store 补全）最先做 —— 1/2/3 都需要
2. 提示词 1 / 2 / 3 并行（不同 view）
3. 提示词 5（后端端点）并行
4. 提示词 4（router + WS bus）最后（汇总）

完成时间预期:
- 提示词 6: 半天
- 提示词 1 + 2 + 3: 各 1 天（并行约 1.5 天）
- 提示词 5: 1 天
- 提示词 4: 0.5 天
- **总: 约 2.5 天**
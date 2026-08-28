# 生物识别认证与 SQLite 离线模式（2026-08-28）

本文档记录 WebAuthn 生物识别认证基础设施与 Chat Agent SQLite 离线存储的设计与实现。

## 1. 生物识别认证（BiometricStore）

### 1.1 动机

为支持未来的无密码登录与设备绑定，引入 WebAuthn 风格的生物识别认证基础设施。当前为 **P0 基础层**——仅提供凭据注册/存储/查询，**不含** 实际的 COSE 签名验证与 challenge-session 绑定（留给后续 sprint 接入 `identity-go` 的 WebAuthn helper）。

### 1.2 数据模型

#### 1.2.1 Go 结构（`backend/internal/auth/biometric.go`）

```go
type BiometricCredential struct {
    ID          string   `json:"id" db:"id"`                    // 凭据唯一标识（base64url，客户端生成）
    UserID      string   `json:"user_id" db:"user_id"`
    WorkspaceID string   `json:"workspace_id" db:"workspace_id"`
    DeviceName  string   `json:"device_name" db:"device_name"`  // 用户设置的设备名称
    PublicKey   []byte   `json:"public_key" db:"public_key"`    // COSE 公钥（DER/原始格式）
    Counter     int64    `json:"counter" db:"counter"`          // 防重放计数器
    Transports  []string `json:"transports,omitempty" db:"-"`   // ["internal", "usb", "nfc", "ble"]
    CreatedAt   int64    `json:"created_at" db:"created_at"`
    LastUsedAt  int64    `json:"last_used_at" db:"last_used_at"`
}

type BiometricStore struct {
    pool *pgxpool.Pool
}
```

#### 1.2.2 PostgreSQL Schema

```sql
CREATE TABLE IF NOT EXISTS biometric_credentials (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    device_name  TEXT NOT NULL DEFAULT '',
    public_key   BYTEA NOT NULL,
    counter      BIGINT NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL,
    last_used_at BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_biometric_user_ws 
    ON biometric_credentials(user_id, workspace_id);
```

**安全约束**：
- 不存储私钥（私钥留在用户设备的 Secure Enclave/TPM）
- `counter` 单调递增，防重放攻击
- `public_key` 用于后续验证客户端签名（当前未实现验证逻辑）

### 1.3 API 端点（`backend/internal/server/server_biometric.go`）

| 端点 | 方法 | 说明 | 状态 |
|------|------|------|------|
| `/api/auth/biometric/register/begin` | POST | 生成 challenge（300s TTL） | ✅ 实现 |
| `/api/auth/biometric/register/finish` | POST | 接收客户端凭据（id/publicKey/counter），存储到 DB | ✅ 实现（无签名验证） |
| `/api/auth/biometric/login/begin` | POST | 生成登录 challenge | ✅ 实现 |
| `/api/auth/biometric/login/finish` | POST | 验证签名并签发会话 token | ⚠️ **501 Not Implemented**（需 identity-go） |
| `/api/auth/biometric/credentials` | GET | 列出当前用户的所有凭据 | ✅ 实现 |
| `/api/auth/biometric/credentials/:id` | PATCH/DELETE | 重命名/删除凭据（带 ownership 校验） | ✅ 实现 |

**当前限制**：
- `register/finish`：接收凭据但**不验证** `clientDataJSON` 签名（仅 base64url 解码 + 存储）
- `login/finish`：返回 `501 Not Implemented`，占位符提示"需要 identity-go webauthn helper"

### 1.4 集成点

#### Server 注入（`backend/internal/server/server.go`）

```go
type Server struct {
    // ...
    biometricStore *auth.BiometricStore  // line 96
}

func (s *Server) SetBiometricStore(store *auth.BiometricStore) { s.biometricStore = store }
func (s *Server) BiometricStore() *auth.BiometricStore         { return s.biometricStore }
```

#### 路由注册（`backend/internal/server/server.go:Handler()`）

```go
mux.HandleFunc("/api/auth/biometric/register/begin",  s.requireAuth(s.handleBiometricRegisterBegin))
mux.HandleFunc("/api/auth/biometric/register/finish", s.requireAuth(s.handleBiometricRegisterFinish))
mux.HandleFunc("/api/auth/biometric/login/begin",     s.handleBiometricLoginBegin)
mux.HandleFunc("/api/auth/biometric/login/finish",    s.handleBiometricLoginFinish)
mux.HandleFunc("/api/auth/biometric/credentials",     s.requireAuth(s.handleBiometricCredentials))
mux.HandleFunc("/api/auth/biometric/credentials/",    s.requireAuth(s.handleBiometricCredentialOps))
```

**权限守卫**：
- 注册端点需 `requireAuth`（已登录用户绑定新设备）
- 登录端点无需 auth（用于替代密码登录）
- 凭据管理需 `requireAuth` + ownership 校验

### 1.5 测试覆盖（`backend/internal/auth/biometric_test.go` + `backend/internal/server/server_biometric_test.go`）

- `TestBiometricStoreErrorsOnNilPool`：nil pool 错误路径
- `TestNewChallengeID`：challenge base64url 长度（43 字符）、解码为 32 字节、唯一性
- 9 个 server handler 测试：405 method 拒绝、503 无 store、501 login finish、400 bad base64/empty id

**覆盖率**：基础路径 + 边界条件 ✅；签名验证逻辑 ❌（未实现）

---

## 2. Chat Agent SQLite 离线模式

### 2.1 动机

现有 `chatagent.Store` 依赖 PostgreSQL（Acc 云端模式），但：
- 单机部署/离线环境无 PG → chatagent 路由返回 503 → AI 对话无角色选择
- 用户反馈："启动 pocketd 后 Chat Agent 模块无任何日志"（被 `if pool != nil` 守卫跳过）

**SQLiteStore** 提供 **本地降级方案**：pure-Go SQLite（`modernc.org/sqlite`，no CGO），接口与 PG Store 平行，启用后 280 个内置角色立即可用。

### 2.2 数据模型（`backend/internal/chatagent/sqlite_store.go`）

```go
type SQLiteStore struct {
    db *sql.DB  // modernc.org/sqlite driver (pure Go, no CGO)
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
    dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
    db, err := sql.Open("sqlite", dsn)
    // ...
}
```

#### Schema（与 PG Store 对齐）

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
    is_builtin    INTEGER NOT NULL DEFAULT 0,  -- SQLite 用 INTEGER 存布尔
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chat_agents_ws ON chat_agents(workspace_id);
CREATE INDEX IF NOT EXISTS idx_chat_agents_dept ON chat_agents(department);
CREATE INDEX IF NOT EXISTS idx_chat_agents_builtin ON chat_agents(is_builtin);
```

**兼容性**：
- PG `JSONB` → SQLite `TEXT`（当前未用 JSON 操作符，全部序列化/反序列化）
- PG `BOOLEAN` → SQLite `INTEGER`（0/1）
- PG `BIGINT` → SQLite `INTEGER`（SQLite 的 INTEGER 是 8 字节有符号整数）

### 2.3 接口实现（`chatagent.StoreIface`）

`SQLiteStore` 实现完整的 `StoreIface` 契约（与 `Store` 平行）：

```go
type StoreIface interface {
    Init(ctx context.Context) error
    Create(ctx context.Context, a *Agent) error
    Get(ctx context.Context, workspaceID, id string) (*Agent, error)
    List(ctx context.Context, workspaceID, department string) ([]*Agent, error)
    Update(ctx context.Context, workspaceID string, a *Agent) error
    Delete(ctx context.Context, workspaceID, id string) error
    CountCustom(ctx context.Context, workspaceID string) (int, error)
    ImportBuiltinAgents(ctx context.Context, repoPath string) error
}

var _ StoreIface = (*Store)(nil)         // PG Store
var _ StoreIface = (*SQLiteStore)(nil)   // SQLite Store
```

**关键设计**：
- `nil slice → []*Agent{}`：保证 JSON 序列化为 `[]` 而非 `null`（与 PG Store 行为一致）
- `ImportBuiltinAgents`：复用 `importer.go` 的通用 `importBuiltin` 函数（PG/SQLite 共享解析逻辑）

### 2.4 Fallback 策略（`backend/cmd/pocketd/main.go:initChatAgentStores`）

```go
func initChatAgentStores(pool *pgxpool.Pool, dataDir string) (chatagent.StoreIface, *chatagent.SyncStore) {
    // 优先 PG（Acc 云端模式）
    if pool != nil {
        pgStore := chatagent.NewStore(pool)
        if err := pgStore.Init(context.Background()); err == nil {
            syncStore := chatagent.NewSyncStore(pool)
            if err := syncStore.Init(context.Background()); err != nil {
                log.Printf("WARN: chatagent sync init failed: %v (running without cloud sync)", err)
                return pgStore, nil  // PG Store + nil sync
            }
            return pgStore, syncStore  // PG Store + Acc sync
        }
    }

    // PG 不可用 → SQLite fallback（单机离线模式）
    dbPath := filepath.Join(dataDir, "chat_agents.sqlite")
    store, err := chatagent.NewSQLiteStore(dbPath)
    if err != nil {
        log.Printf("WARN: chatagent SQLite store init failed: %v", err)
        return nil, nil  // 两边都失败 → 路由返回 503
    }
    if err := store.Init(context.Background()); err != nil {
        log.Printf("WARN: chatagent SQLite schema init failed: %v", err)
        store.Close()
        return nil, nil
    }
    log.Printf("Chat Agent store using SQLite fallback at %s (no cloud sync)", dbPath)
    return store, nil  // SQLite Store + nil sync（云端同步端点将返回 503）
}
```

**降级矩阵**：

| PG 可用 | PG Init | SyncStore Init | 结果 |
|---------|---------|----------------|------|
| ✅ | ✅ | ✅ | PG Store + SyncStore（完整功能） |
| ✅ | ✅ | ❌ | PG Store + nil sync（CRUD 可用，sync 端点 503） |
| ❌ | - | - | SQLite Store + nil sync（本地模式） |
| ❌（SQLite 也失败） | - | - | nil + nil（所有 chatagent 端点 503） |

### 2.5 测试覆盖（`backend/internal/chatagent/sqlite_store_test.go`）

- `TestSQLiteStore_CRUD`：完整 CRUD + 内置角色保护（不可更新/删除 `is_builtin=true` 的角色）
- `TestSQLiteStore_ImportBuiltin`：解析 `.md` 仓库 + 幂等性（已导入时跳过）

**Hermetic 测试**：无外部依赖（使用 `t.TempDir()` 创建临时 SQLite DB），可在 CI 环境运行。

---

## 3. Importer 路径过滤（commit `6efb249`）

### 3.1 问题

`filepath.Walk` 遍历 `agency-agents-zh` 仓库时，会误把以下文件当作角色 `.md` 解析：
- `.github/ISSUE_TEMPLATE/*.md`（GitHub issue 模板）
- `node_modules/*/README.md`（npm 依赖文档）
- `.vscode/settings.json.md`（IDE 配置）

导致 `ParseAgentFile` 报错 "missing YAML frontmatter"，污染日志。

### 3.2 解决方案（`backend/internal/chatagent/importer.go`）

新增 **目录白名单过滤**：

```go
var skipDirs = map[string]struct{}{
    ".git":         {},
    ".github":      {},
    ".vscode":      {},
    ".idea":        {},
    "node_modules": {},
}

func isSkippedDir(path string) bool {
    for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
        if _, ok := skipDirs[seg]; ok {
            return true
        }
    }
    return false
}
```

在 `filepath.Walk` 回调中：

```go
if info.IsDir() {
    if isSkippedDir(path) {
        return filepath.SkipDir  // 整目录跳过，不继续遍历子树
    }
    return nil
}
```

### 3.3 测试覆盖（`backend/internal/chatagent/importer_test.go`）

- `TestIsSkippedDir`：表驱动测试，验证 `.github/workflows`、`foo/.idea/bar`、`node_modules/pkg` 均被跳过
- `TestImportBuiltin_SkipsAuxDirs`：端到端测试，构造临时仓库（含 `.github/README.md` + 正常角色），验证仅解析出正常角色

### 3.4 附带清理

- `backend/.gitignore`：新增排除 `pocketd` 二进制、`data/chat_agents.sqlite*` 运行时数据
- `backend/pocketd`：untrack（早先误加的 27MB 编译产物），实际文件已删除（`git rm --cached`）

---

## 4. 集成验证

### 4.1 构建 & 单元测试

```bash
# Backend
cd backend
go build ./...                                  # ✅ 通过
go test ./internal/auth/...                     # ✅ 2 个 biometric 测试通过
go test ./internal/server/...                   # ✅ 9 个 biometric handler 测试通过
go test ./internal/chatagent/... -short         # ✅ 6 个 hermetic 测试通过
                                                # ⚠️ PG Store 集成测试跳过（需 POCKET_POSTGRES_DSN）

# Frontend
cd frontend
npm run typecheck                               # ✅ vue-tsc 无错误
```

### 4.2 运行时冒烟（本地环境，无 PG）

```bash
# 启动 pocketd（无 PG 配置）
./backend/pocketd

# 预期日志：
# [pocketd] Chat Agent store using SQLite fallback at data/chat_agents.sqlite (no cloud sync)
# [chatagent] importing builtin agents from ...
# [chatagent] imported 280 builtin agents

# 验证：
curl http://localhost:8080/api/chat-agents                     # 200，返回 280 个内置角色
curl http://localhost:8080/api/chat-agents/sync/upload         # 503 "sync not configured (requires PostgreSQL)"
curl -X POST http://localhost:8080/api/auth/biometric/register/begin \
  -H "Cookie: pocket_session=..." -d '{}'                       # 200，返回 challenge
```

**服务降级**：
- Chat Agent CRUD：✅ 可用（SQLite）
- Chat Agent Sync：❌ 503（需 PG）
- Biometric Register：✅ 可用（生成 challenge，接收凭据）
- Biometric Login Finish：⚠️ 501 Not Implemented（占位符）

---

## 5. 后续工作

### 5.1 生物识别认证（P1 实现）

- [ ] 接入 `identity-go` 的 WebAuthn helper
  - Challenge-session 绑定（Redis/in-memory TTL map）
  - COSE 公钥解析与签名验证（ES256/RS256）
  - `authenticatorData` + `clientDataJSON` 完整性校验
- [ ] 实现 `handleBiometricLoginFinish`：验证通过后签发 `pocket_session` cookie
- [ ] 前端 WebAuthn 客户端（`navigator.credentials.create/get`）
- [ ] 审计：注册/登录/删除凭据事件

### 5.2 SQLite 存储优化（可选）

- [ ] 压测：SQLite WAL 模式在高并发下的性能（当前预期 <100 QPS）
- [ ] 备份策略：`sqlite3_backup` API 或 `.backup` 命令定期快照
- [ ] 迁移工具：SQLite → PG 数据迁移脚本（单机升级到 Acc 模式时需要）

### 5.3 文档完善

- [ ] 用户文档：如何启用生物识别登录（浏览器兼容性、安全提示）
- [ ] 运维文档：SQLite fallback 的备份/恢复流程
- [ ] API 文档：补充 biometric 端点的 OpenAPI spec

---

## 6. 安全审查清单

| 项目 | 状态 | 说明 |
|------|------|------|
| 私钥不存储 | ✅ | `BiometricCredential` 仅存 `public_key`，私钥留在客户端设备 |
| Counter 防重放 | ⚠️ | 字段已定义，验证逻辑未实现（P1） |
| Challenge TTL | ✅ | 300s 过期，存储在内存（`sync.Map`，无持久化泄漏） |
| Ownership 校验 | ✅ | 凭据删除/重命名前检查 `user_id` 匹配 |
| SQL 注入 | ✅ | 所有查询使用参数化（`$1`/`?` 占位符） |
| 路径注入 | ✅ | `importer.go` 使用 `filepath.Walk`（不接受用户输入路径） |
| Base64 解码 | ✅ | 使用 `base64.RawURLEncoding`（标准 WebAuthn 编码） |
| 错误信息泄漏 | ✅ | 用户不存在/凭据不存在返回通用 "not found"，不区分原因 |

**已知风险**：
- ⚠️ 当前 `register/finish` **不验证签名**，仅接受客户端声称的 `publicKey`。攻击者可伪造凭据注册请求。**必须在 P1 修复**。
- ⚠️ `login/finish` 未实现，无法实际登录。当前为占位符状态。

---

## 7. Git 提交历史

```
6efb249  fix(chatagent): importer 跳过 .github/.vscode/.idea/node_modules + 清理 tracked binary
         - 新增 skipDirs 白名单与 isSkippedDir 函数
         - 新增 TestIsSkippedDir + TestImportBuiltin_SkipsAuxDirs
         - backend/.gitignore：排除 pocketd 二进制、chat_agents.sqlite*
         - backend/pocketd：untrack（27MB 误提交的二进制）

57cafa3  feat(chatagent): SQLiteStore 离线模式 — 无 PG 也能用 AI 对话角色
         - 新增 sqlite_store.go（179 行）+ sqlite_store_test.go（148 行）
         - 实现完整 StoreIface：Init/CRUD/CountCustom/ImportBuiltinAgents
         - 编译期接口断言：var _ StoreIface = (*SQLiteStore)(nil)

34ba273  fix(server): 补齐 main 缺失类型——BiometricStore/StoreIface 与 stub handler (#12)
         - 新增 auth/biometric.go（197 行）+ auth/biometric_test.go（2 个测试）
         - 新增 server/server_biometric.go（255 行）+ server_biometric_test.go（9 个测试）
         - 新增 chatagent/store_iface.go（28 行）+ server/base64.go（10 行）
         - 修改 server/server.go：恢复 6 条 biometric 路由
         - 修改 chatagent/importer.go：移除 SQLiteStore 重载（main 未合入）
         - 修改 cmd/pocketd/main.go：initChatAgentStores 移除 SQLite fallback
```

**推送状态**：
- ✅ `34ba273` 已推送到 `origin/main`
- ⏳ `57cafa3` + `6efb249` 待推送（本次 PR）

---

## 8. 相关文档

- [AI 对话与网关管理（2026-08-27）](./2026-08-27-ai-chat-and-gateway-management.md)：LLM BFF + 多模态 + 网关管理扩展
- [多智能体工作台设计方案（2026-08-28）](./2026-08-28-multi-agent-workbench-design.md)：Chat Agent 数据模型 + CRUD API + Acc 云端同步
- [架构重构计划（2026-07-02）](./2026-07-02-app-architecture-refactoring-plan.md)：整体架构演进路线图

---

**文档版本**：2026-08-28  
**维护者**：zag-doc-governance agent  
**状态**：✅ 已实现（BiometricStore stub + SQLiteStore fallback）；⚠️ 签名验证未实现（P1 待接入 identity-go）

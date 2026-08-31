# 交接文档：Redclaw 移动端 + 邮箱注册/忘记密码/验证码登录

**交接时间**：2026-09-01 01:07  
**仓库**：opencode-pocket (official-deploy/services/opencode-pocket)  
**分支**：main @ `6d0e131`  
**状态**：✅ 调研与方案设计完成，等待新会话执行编码  
**下一步**：在新会话中按 Phase C（§3）逐项落地：品牌重命名 → 后端 DB/SMTP/handlers/RedClaw 镜像 → 前端 Tab + ForgotPasswordView → 联调证据

---

## 1. 本会话完成工作总览

### 1.1 现状调研（已完成）

- **登录链路**：单列密码 + 指纹。`frontend/src/features/auth/LoginView.vue`（449 行）调用 `POST /api/auth/login`（`backend/internal/server/server_assistant.go:94 handleAuthLogin`），写入 `useAuthStore` → `connectWs()` → `initLobster()` → `MasterPasswordDialog`（首次设置）。本地 `UserStore`（`backend/internal/auth/users.go`）仅有 `id/username/password_hash/role/created_at`，无 email 列。
- **RedClaw 集成现状**：仅在生物识别 `handleBiometricLoginFinish` 中调用 `redclawBridge.VerifyUser`（`backend/internal/server/server_biometric.go:303-333`）。容器侧 `redclaw-facade :27001`、`redclaw-authagent :27092`、`redclaw-api :27000` 等运行中但**所有 readyz 都报 `database: failed to connect to llm-gateway-pg`**（DNS `llm-gateway-pg` 在 `local-stack_redclaw-local-stack` 网络中无法解析）。
- **网络现状**：`opencode-pocket-local` 容器在 `opencode_pocket_local` + `r112_net`，**与 RedClaw 容器不在同一 Docker 网络**；从 `opencode-pocket-local` 内 `getent hosts redclaw-authagent` 返回 `bad address`。
- **SMTP 现状**：仓库**无系统级 SMTP 发送代码**。`net/smtp` 仅在 `backend/internal/server/smtp_send.go` 用于用户邮箱 IMAP 自动回复；`backend/internal/email/` 全部是 IMAP/OAuth 收件逻辑。
- **品牌散点**：`🦞 OpenCode Pocket` 出现在 LoginView.vue、App.vue、AppLayout.vue、utils/version.ts、ComponentDemo.vue、index.html、9 个 locale JSON、capacitor.config.ts、Info.plist、WebAuthn 默认 RP name。

### 1.2 用户决策（基于 AskUserQuestion）

| 决策点 | 选择 |
|---|---|
| RedClaw 范围 | 仅打通当前能力：pocketd 自管 register/OTP，RedClaw 端先占位（fail-soft） |
| 存储/发送 | **双写，本地为权威**：pocketd 本地表 + 镜像调用 RedClaw 占位 |
| 改名范围 | **仅改用户可见品牌**（不动 appId / 容器名 / 镜像名 / Android 包名 / iOS bundle） |
| 登录 UX | **Tab 切换**：密码登录 / 邮箱验证码登录 / 注册 + 独立 /forgot-password 页 |

### 1.3 方案文档（已完成并获批）

完整方案见 ExitPlanMode 输出。决策矩阵：见 `## 4. 关键决策与风险`。

---

## 2. 当前状态快照

### 2.1 Git 状态

```
M backend/internal/llmgateway/client.go
M backend/internal/opencode/config_writer.go
M backend/internal/server/llmbff_provider_adapters.go
M frontend/android/capacitor.settings.gradle
M frontend/android/variables.gradle
?? Dockerfile.frontend.local-rebuild
?? backend/internal/llmgateway/client_test.go
?? backend/internal/server/llmbff_provider_adapters_test.go
?? test-evidence/2026-09-01-auto-fallback-deploy/
```

新会话接手时**先 `git status` 确认这些未追踪/未提交修改**；如无冲突可继续推进；如有冲突优先 stash。

### 2.2 代码库关键文件（必读）

| 路径 | 行 | 用途 |
|---|---|---|
| `backend/internal/auth/users.go` | 30-102 | 本地用户表（bcrypt）—— Phase C2 需扩展 email 字段 |
| `backend/internal/auth/schema.go` | 1-25 | users DDL —— 需同步 ALTER TABLE + email_verification_codes 表 |
| `backend/internal/auth/identity.go` | 47-49 | `DefaultIssuerAllowlist` 已含 `redclaw` |
| `backend/internal/auth/jwt.go` | 1-80 | Signer —— 注册后签发复用 |
| `backend/internal/server/server.go` | 501-508 | 现有 `/api/auth/*` 路由注册位 |
| `backend/internal/server/server_assistant.go` | 85-161 | `handleAuthLogin` —— 注册/验证码登录后共享 JWT 签发逻辑 |
| `backend/internal/server/server_biometric.go` | 303-333 | RedClaw verify-user 调用样板 |
| `backend/internal/redclaw/client.go` | 18-40 | `NewClient(cfg)` + `doRequest` —— `auth_user.go` 镜像客户端复用 |
| `backend/internal/redclaw/auth.go` | 1-46 | TenantContext 提取 |
| `backend/internal/config/config.go` | 132-208 | `POCKET_*` 配置加载 —— 需补 SMTP / REDCLAW_AUTH_URL 块 |
| `backend/cmd/pocketd/main.go` | 60-100 | Server 装配 —— 注入新 SMTP client / AuthClient |
| `frontend/src/features/auth/LoginView.vue` | 1-449 | 登录入口 —— Phase C6 重构为 3 Tab |
| `frontend/src/features/auth/MasterPasswordDialog.vue` | 全文 | 主密码对话框 —— 复用 |
| `frontend/src/stores/auth.ts` | 18-82 | `useAuthStore.setAuth*` —— 共享登录链路 |
| `frontend/src/api/http.ts` | 全文 | 通用 http 调用 —— Phase C6 新增 `auth.ts` |
| `frontend/src/app/router-mobile.ts` | 103-280 | 路由表（含 `requiresLobster`）—— 新增 `/forgot-password` |
| `frontend/src/locales/zh-CN.json` | 1-4616 | 最大 locale —— 新增 auth.* key |
| `frontend/src/locales/en-US.json` | 1-3343 | 第二大 locale —— 同步新增 |

### 2.3 依赖与环境

- **后端**：`go 1.25+`；`pgx/v5`、`bcrypt`、`golang-jwt/jwt/v5`、`labstack/echo/v4`、`gorilla/websocket`、`go-webauthn/webauthn`、`emersion/go-imap`、`lib/pq`。SMTP 用 stdlib `net/smtp`，**无新增依赖**。
- **前端**：`vue 3.5+`、`vue-router`、`pinia`、`vue-i18n`、`@capacitor/*`。无新增依赖。
- **本地容器**：`opencode-pocket-local` 在 `opencode_pocket_local` + `r112_net`；要打 RedClaw 必须把对应容器加入 `local-stack_redclaw-local-stack` 或用 `host.docker.internal:27092`。
- **SMTP 联调**：先 `POCKET_SMTP_DEBUG_ECHO=true` 直接看 `debug_code`，等链路通了再配真实 SMTP。

---

## 3. 遗留问题与边界

### 3.1 已知阻塞点

1. **RedClaw readyz 全绿之前不能真正双写**：本方案镜像路径默认 disabled（`POCKET_REDCLAW_AUTH_URL` 未配置则跳过）。需要修复 RedClaw 的 PG DNS 解析（让 `llm-gateway-pg` 在 `local-stack_redclaw-local-stack` 网络中可解析，或改 RedClaw compose 用 `host.docker.internal:5432`）后才能启用。
2. **跨网络访问**：当前 `opencode-pocket-local` 在 `r112_net`，RedClaw 在 `local-stack_redclaw-local-stack`。Phase C8 联调时需二选一：(a) 把 `opencode-pocket-local` 加到 `local-stack_redclaw-local-stack`，或 (b) 通过 `host.docker.internal:27092` 访问。
3. **当前 m=5 个未提交修改**：与本次任务无关，接手后先确认是否需要 stash/保留。

### 3.2 技术债务

- `frontend/src/features/auth/LoginView.vue` 已 449 行，Phase C6 重构后会涨到 ~600 行；可拆出 `<TabAuthForms>` 子组件（**非必须**，建议放后续 sprint）。
- i18n 仅补 zh-CN/en-US，其他 7 种 locale 保留硬编码中文 fallback（不影响功能）。
- 当前 `users.email` 列允许 NULL（旧用户无邮箱）。Phase C2 `InsertUser` 加 `email` 参数时旧调用点必须保持兼容（指纹登录、dev-auth 路径不强制 email）。

### 3.3 设计决策记录

- **密码强度**：≥8 位 + 至少 1 数字 + 1 字母（与现有 `users.go:47` 保持一致，并补字符类型校验）。
- **验证码**：6 位数字，TTL 5 分钟，bcrypt hash 存，单次使用，错误信息统一为「验证码错误或已过期」。
- **频控**：同邮箱 + 同 purpose 60 秒 1 次 / 每天 10 次。
- **恒返回 200**：`send-code` 不暴露邮箱是否注册；`forgot-password` 成功后也不直接登录（强制重新走密码 / 验证码登录）。
- **JWT 兼容**：`register` / `code-login` 复用 `handleAuthLogin` 的 `SignWithWorkspace` + shadow 记录 + `EnsureDefaultWorkspace` 全链路，**不引入新的 token 格式**。

---

## 4. 下一步行动计划（Phase C 详细清单）

### C1 — 品牌重命名（用户可见部分）
- 改 `frontend/src/features/auth/LoginView.vue:6-7,8,193`：`🦞 OpenCode Pocket` → `🔴 Redclaw`，副标题 `RedClaw 移动端 / Mobile Client`
- 改 `frontend/src/app/App.vue:35` console 文案
- 改 `frontend/src/app/AppLayout.vue:124` fallback title
- 改 `frontend/src/utils/version.ts:6` `name: 'Redclaw Mobile'`
- 改 `frontend/src/pages/ComponentDemo.vue:5` `<h1>Redclaw 组件展示</h1>`
- 改 `frontend/index.html:9,13` description / title
- 改 `frontend/src/locales/*.json` 9 个文件的 `title` + `versionFootnote`
- 改 `backend/internal/config/config.go:116` `WebAuthnRPDisplayName` 默认值注释
- 改 `backend/internal/auth/webauthn_verifier.go:112` 注释默认值
- 改 `backend/internal/auth/webauthn_verifier_test.go:18,32,39` 测试用例默认值
- **不动**：`capacitor.config.ts:4-5` `appId/appName`、`frontend/android/app/build.gradle` `namespace/applicationId`、所有 Java 源目录、容器名、镜像名
- 验证：`grep -r "OpenCode Pocket" --include="*.{vue,ts,go,json,html,gradle}" -l` 只剩 `docs/` 下历史报告
- 截图存 `test-evidence/2026-09-01-redclaw-rebrand/`

### C2 — 后端 DB / 模型
- `backend/internal/auth/users.go`：User 加 `Email string` + `EmailVerified bool`；`InsertUser(ctx, u, password, email)` 接受 email；新增 `GetUserByEmail(ctx, email)` / `GetUserByUsernameOrEmail(ctx, ident)` / `UpdatePasswordByEmail(ctx, email, newHash)`
- `backend/internal/auth/schema.go`：补两套 DDL（pg + sqlite）
  ```sql
  ALTER TABLE users ADD COLUMN IF NOT EXISTS email TEXT;
  ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT FALSE;
  CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_uq ON users (LOWER(email));
  CREATE TABLE IF NOT EXISTS email_verification_codes (
      id INTEGER PRIMARY KEY AUTOINCREMENT,    -- pg 用 BIGSERIAL
      email TEXT NOT NULL,
      purpose TEXT NOT NULL,
      code_hash TEXT NOT NULL,
      expires_at TIMESTAMPTZ NOT NULL,         -- sqlite 用 DATETIME
      used_at TIMESTAMPTZ,
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      request_ip TEXT
  );
  CREATE INDEX IF NOT EXISTS evc_lookup ON email_verification_codes (LOWER(email), purpose, expires_at DESC);
  ```
- `backend/internal/config/config.go`：加 `SMTPHost/SMTPPort/SMTPUser/SMTPPassword/SMTPFrom/SMTPTLSMode/SMTPDebugEcho` + `RedClawAuthURL/RedClawAuthSecret/RedClawAuthTimeoutSec`
- 单测：扩 `users_test.go`（email case）

### C3 — SMTP + 验证码存储
- 新建 `backend/internal/notify/smtp.go`：最小封装，纯文本 + HTML；STARTTLS / 直接 TLS / 明文三档；返回 error
- 新建 `backend/internal/notify/smtp_test.go`：用本地 `net/smtp` mock server 验证 Send/Auth/STARTTLS
- 新建 `backend/internal/auth/email_codes.go`：
  - `type CodePurpose string` 常量：`register / reset / login`
  - `func Generate(ctx, email, purpose, requestIP) (plaintext string, ttlSec int, err error)`：生成 6 位、bcrypt hash、rate-limit 60s/daily 10、写入库
  - `func Verify(ctx, email, purpose, plaintext) error`：原子 mark used（`UPDATE ... WHERE used_at IS NULL AND expires_at > NOW()`）
  - 单测覆盖：TTL、reuse、邮箱大小写、rate-limit

### C4 — 新 handlers
- 新建 `backend/internal/server/server_auth_extended.go`：
  - `handleAuthSendCode(w, r)`：解析 `{email, purpose}`，恒返回 200，rate-limit 触发 429；调 `auth.Generate`；调 `notify.Send`（失败仅 log，`debug_code` 仍可回显）
  - `handleAuthRegister(w, r)`：`{email, code, username, password}` → `auth.Verify` → 校验密码强度 + 用户名唯一 + email 唯一 → `userStore.InsertUser(..., email)` → `EnsureDefaultWorkspace` → `jwtSigner.SignWithWorkspace` → `RecordShadow` → `redclawAuth.Register(...)`（fail-soft）→ 200 `{token, user, user_id, workspace_id}`
  - `handleAuthCodeLogin(w, r)`：`{email, code}` → `auth.Verify` → `userStore.GetUserByEmail` → `EnsureDefaultWorkspace` → `jwtSigner.SignWithWorkspace` → `RecordShadow` → 200
  - `handleAuthForgotPassword(w, r)`：`{email, code, new_password}` → `auth.Verify` → bcrypt 新密码 → `userStore.UpdatePasswordByEmail` → 200 `{ok: true}`（不返回 token）
  - `handleAuthResetPassword(w, r)`（可选）：`requireAuth` 保护，`{old_password, new_password}` 改密码
- `backend/internal/server/server.go:501` 旁追加：
  ```go
  mux.HandleFunc("/api/auth/send-code", s.handleAuthSendCode)
  mux.HandleFunc("/api/auth/register", s.handleAuthRegister)
  mux.HandleFunc("/api/auth/code-login", s.handleAuthCodeLogin)
  mux.HandleFunc("/api/auth/forgot-password", s.handleAuthForgotPassword)
  mux.HandleFunc("/api/auth/reset-password", s.requireAuth(s.handleAuthResetPassword))
  ```
- 共享 helper：抽 `signInUser(ctx, w, r, userID, username)` 把 `EnsureDefaultWorkspace + SignWithWorkspace + RecordShadow` 合并到 `server_assistant.go`
- 集成测试 `server_auth_extended_test.go`：覆盖 200/400/401/409/429 + RedClaw fail-soft 路径

### C5 — RedClaw 镜像客户端
- 新建 `backend/internal/redclaw/auth_user.go`：
  ```go
  type AuthClient struct{ c *Client }                    // 复用 Client.doRequest
  func NewAuthClient(cfg ClientConfig) (*AuthClient, error)
  func (a *AuthClient) Register(ctx, req RegisterRequest) error   // POST /api/v1/auth/register
  func (a *AuthClient) VerifyCode(ctx, req VerifyCodeRequest) error
  func (a *AuthClient) ForgotPassword(ctx, req ForgotPasswordRequest) error
  ```
- `cfg.BaseURL == ""` 时 `NewAuthClient` 返回 `nil, nil`（调用方 nil-safe）
- `cmd/pocketd/main.go`：装配到 `Server.redclawAuth *redclaw.AuthClient`；所有 mirror 调用包 `defer recover() + log.Warn`
- `healthz` 探测：`AuthClient.New` 后调用 `Health()` 失败则 `log.Warn("redclaw auth disabled: ...") + s.redclawAuth = nil`
- 单测：`auth_user_test.go` 用 httptest 覆盖 happy/error/timeout

### C6 — 前端 LoginView 重构 + Tab
- 改 `frontend/src/features/auth/LoginView.vue`：3 Tab（密码 / 验证码 / 注册），共享 `doLogin` 链路（`auth.setAuth*` → `connectWs` → `initLobster` → `MasterPasswordDialog`）
- 底部加 `<router-link to="/forgot-password">忘记密码？</router-link>`
- 新建 `frontend/src/api/auth.ts`：
  ```ts
  export async function sendCode(email: string, purpose: 'register'|'reset'|'login'): Promise<{ ok, ttl_sec, debug_code? }>
  export async function registerUser(body): Promise<{ token, user, user_id?, workspace_id? }>
  export async function codeLogin(email, code): Promise<…>
  export async function forgotPassword(email, code, new_password): Promise<{ ok: true }>
  export async function resetPassword(old_password, new_password): Promise<{ ok: true }>
  ```
- 复用 `useAuthStore.setAuthWithWorkspace`（已有 `auth.ts:32-47`）+ `connectWs()` + `initLobster()`
- i18n：新增 `auth.login.* / auth.codeLogin.* / auth.register.* / auth.errors.*` 到 `zh-CN.json` 和 `en-US.json`，其余 7 个 locale 暂保留中文硬编码（fallback 不崩）

### C7 — 前端 ForgotPasswordView
- 新建 `frontend/src/features/auth/ForgotPasswordView.vue`：三段式 stepper
  - step 1：输入邮箱 + 「发送验证码」（按钮 60s 倒计时）
  - step 2：输入 6 位验证码 + 新密码 + 确认密码
  - step 3：成功提示 + 「返回登录」
- `frontend/src/app/router-mobile.ts` 新增：
  ```ts
  {
    path: '/forgot-password',
    component: () => import('@/features/auth/ForgotPasswordView.vue'),
    meta: { title: '忘记密码', requiresAuth: false, requiresLobster: false },
  }
  ```

### C8 — 联调与证据
- 启动 `pocketd`：`POCKET_DEV_AUTH=true POCKET_SMTP_DEBUG_ECHO=true ./pocketd`
- curl 测试矩阵（8 条）：
  - `POST /api/auth/send-code` 正常 → 200 + `debug_code`
  - `POST /api/auth/send-code` 60s 内同邮箱 → 429
  - `POST /api/auth/register` 错误 code → 400
  - `POST /api/auth/register` 正确 code → 200 + token
  - `POST /api/auth/code-login` 正确 → 200
  - `POST /api/auth/forgot-password` 正确 → 200
  - `POST /api/auth/login` 旧密码登录 → 200（回归）
  - `POST /api/auth/biometric/login/finish` → 200（生物识别回归）
- Android 模拟器截图 4 张：登录页 3 Tab、忘记密码页 → `test-evidence/2026-09-01-redclaw-mobile-auth/`
- 写 `docs/handoff/2026-09-01-redclaw-mobile-auth-summary.md` 验收报告

---

## 5. 关键决策与风险

### 5.1 架构决策

| 决策 | 理由 | 风险 | 缓解 |
|---|---|---|---|
| 双写，本地为权威 | RedClaw 端能力未补齐；本地让用户立即跑通 | 后续切流需数据回填 | 保留 `POCKET_REDCLAW_AUTH_URL` 开关，RedClaw 端补齐后切流 |
| SMTP 用 stdlib `net/smtp` | 0 新依赖，Go 1.25 已有 | 仅文本邮件 | multipart/alternative 包装 HTML |
| ALTER TABLE users ADD COLUMN | 兼容现有 `UserStore`，本地登录链路不丢 | sqlite/pg DDL 双写 | `schema.go` 两套 schema |
| 不动 appId / 容器名 | 改名范围收敛，不破坏部署 | iOS bundle 仍为老 ID（UI 看不到） | 用户接受 |
| 仅 zh-CN/en-US 加 i18n key | 首次工作量收敛 | 其余 7 种语言硬编码中文 | 后续 sprint 补 |
| RedClaw 镜像默认 disabled | 当前 RedClaw readyz 不通 | 不会真正双写 | 等 readyz 修好后开启 |
| `forgot-password` 不直返 token | 强制重新登录，避免绕过 MFA/生物识别 | 用户体验多一步 | 验证码登录补足 |

### 5.2 风险与缓解

- **PII / 邮件泄漏**：`send-code` 必须恒返回 200，不暴露邮箱是否注册；`forgot-password` 也恒返回 200。`verify-code` 错误信息统一为「验证码错误或已过期」。
- **bcrypt cost**：注册和验证码 hash 用 `bcrypt.DefaultCost`，与现有 `users.go:50` 一致；不要提升到 12+，注册路径慢。
- **rate-limit 边界**：60 秒 1 次基于"上次成功发送时间"，10 次/天基于"今天 0 点以来成功发送次数"。失败请求不计入（避免攻击者通过错误请求挤掉正常用户）。
- **token 与 shadow**：`code-login` 与 `register` 必须 `RecordShadow("pocket", userID, wsID, username, email)`，保证跨项目互信可被识别。
- **并发注册**：同一邮箱被同时注册两次，靠 `users_email_lower_uq` 唯一索引兜底（pg 上 23505 unique violation 翻译为 409 Conflict）。
- **localStorage 兼容**：旧的 `pocket_token` + `pocket_user` 保留不变，新字段 `pocket_email` 在 `setAuthWithWorkspace` 后追加写入。

---

## 6. 参考资料与链接

### 6.1 仓库内文档

- 现有 handoff 模板：[`handoff/2026-08-29-00-35-biometric-auth-cross-module-requirements.md`](file:///Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/handoff/2026-08-29-00-35-biometric-auth-cross-module-requirements.md)
- RedClaw 集成层映射：[`docs/redclaw-mapping/00-layer-map.md`](file:///Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/docs/redclaw-mapping/00-layer-map.md)（交叉参考）
- RedClaw 集成测试报告：[`docs/redclaw-mapping/99-final-report-updated.md`](file:///Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/docs/redclaw-mapping/99-final-report-updated.md)
- 现有 README（部署模式速查）：[`README.md`](file:///Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/README.md)

### 6.2 关键代码锚点

- `backend/internal/auth/users.go:30-102` — 本地 UserStore + bcrypt
- `backend/internal/auth/identity.go:47-49` — issuer allowlist 已含 `redclaw`
- `backend/internal/auth/jwt.go:1-80` — JWT Signer
- `backend/internal/server/server_assistant.go:85-161` — `handleAuthLogin` 现有实现
- `backend/internal/server/server.go:501-508` — `/api/auth/*` 路由注册位
- `backend/internal/redclaw/client.go:18-40` — `NewClient` + `doRequest`（复用）
- `backend/internal/redclaw/auth.go:14-46` — TenantContext（参考鉴权模式）
- `backend/internal/config/config.go:132-208` — `POCKET_*` 加载范式
- `backend/cmd/pocketd/main.go:60-100` — Server 装配 + DI 范式
- `frontend/src/features/auth/LoginView.vue:1-449` — 登录入口
- `frontend/src/stores/auth.ts:18-82` — `setAuth*`
- `frontend/src/api/http.ts` — 通用 http（仿写 `auth.ts`）

### 6.3 外部依赖

- RedClaw 仓库：`/Users/xutaohuang/workspace/FreshLab/RedClaw2/`（auth-agent 端无 register / OTP 能力，需要等实现）
- `local-stack_redclaw-local-stack` 网络上 `llm-gateway-pg` DNS 不通 —— 修复后才能开启双写
- 移动端桥接 Capacitor `com.kaixuan.opencode.pocket` —— 本次**不动**

### 6.4 技能参考

- handoff 技能 SKILL.md：
  - `/Users/xutaohuang/.zcode/skills/handoff/SKILL.md`
  - `/Users/xutaohuang/.agents/skills/handoff/SKILL.md`

---

## 7. 元信息

- **规划时间**：2026-09-01 01:07
- **会话 ID**：（待新会话接力时填）
- **会话类型**：方案设计 + handoff 创建
- **交接方**：本会话（agent）→ 新会话（handoff 接力）
- **下游协调**：RedClaw 仓库（FreshLab/RedClaw2）需在 readyz 修复后补 register / OTP 能力；本任务不阻塞其修复
- **文档维护**：本 handoff 由新会话执行完 Phase C 后，将验收报告追加到 §4 末尾并归档到 `docs/handoff/2026-09-01-redclaw-mobile-auth-summary.md`

**EOF**
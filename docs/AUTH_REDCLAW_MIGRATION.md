# openpocket → RedClaw 认证迁移说明

> 编制日期:2026-09-04  
> 状态:**一期切换已落地**(代码 + 配置 + 文档 + 测试)  
> 适用版本:openpocket backend `c1ea05d` 起,frontend `dfa7069` 起

## 0. 变更摘要

| 维度 | 切换前 | 切换后 |
|---|---|---|
| JWT 签发 | openpocket 本地 `auth.Signer` (HS256) | **RedClaw Admin**(`platform-go` admin 端口) |
| 用户表 | `users` 表 + bcrypt 本地存储 | **RedClaw 权威** + openpocket 影子表 |
| 登录入口 | 密码 / 邮箱验证码 / WebAuthn | 密码 / 邮箱验证码 / WebAuthn + **企业 SSO** |
| 登出 | 仅清 localStorage | **清 localStorage + 撤销 RedClaw session** |
| 改密 | 本地 userStore | **代理到 RedClaw `/api/v1/auth/change-password`** |
| 跨项目互信 | identity-go + `pocket` issuer | 不变(已包含 `redclaw` issuer) |
| Dev 旁路 | `POCKET_DEV_AUTH=true` 本地 admin | 保留(RedClaw 不可达时降级) |
| 一键回滚 | 不支持 | `POCKET_AUTH_LEGACY_ONLY=true` |

## 1. 架构总览

```
┌──────────────────┐  POST /api/v1/auth/login  ┌────────────────────────┐
│  openpocket      │ ────────────────────────► │  RedClaw Admin         │
│  (pocketd)       │ ◄──── {token, employee} ─│  /api/v1/auth/login    │
│                  │                           │  (员工号+密码)         │
│  代理 handler    │  GET /api/v1/auth/me      │  + 持久化 Session      │
│  5 个端点        │ ────────────────────────► │  + 铸造平台 JWT (2h)  │
│                  │ ◄───── { employee:{} } ── │                        │
└──────────────────┘                           └────────────────────────┘
        │
        │ 鉴权统一走 identity-go 多 issuer 验证
        │ (IDENTITY_SHARED_SECRET + IDENTITY_ISSUER_ALLOWLIST)
        │ "redclaw" 已在默认 allowlist
        ▼
   现有所有 /api/* 受保护路由不变
```

## 2. 鉴权路径优先级(自上而下)

```
incoming HTTP request
  ├─ if Authorization: Bearer <jwt>
  │   └─ auth.VerifyToken()
  │        ├─ 1. identity-go 多 issuer 校验(IDENTITY_SHARED_SECRET 已配)
  │        │     • iss ∈ {"redclaw", "memora", "llm-gateway", "pocket", "acc"}
  │        │     • aud == "pocket-api"
  │        │     • HMAC-SHA256 验签通过
  │        ├─ 2. legacy fallback: auth.Signer.Parse(secret == POCKET_JWT_SECRET)
  │        │     • 仅当 POCKET_AUTH_LEGACY_ONLY=true 时仍会签发新 token
  │        └─ 任一通过 → 注入 authClaims 到 context
  └─ 401 invalid or expired token
```

## 3. 一键回滚

紧急情况下,设 `POCKET_AUTH_LEGACY_ONLY=true` 即可完全回退:

```bash
# 1. 停 pocketd
docker compose -f deploy/bin/docker-compose.opp.yml down

# 2. 编辑 .env.local 加 POCKET_AUTH_LEGACY_ONLY=true
echo "POCKET_AUTH_LEGACY_ONLY=true" >> config/.env.local

# 3. 重启
docker compose -f deploy/bin/docker-compose.opp.yml up -d
```

回滚后的行为:
- 登录走 `internal/auth/users.go` 本地 bcrypt 校验
- `/api/auth/login` 不再调 RedClaw
- `/api/auth/logout` 仍可用,但仅清 localStorage(本地 JWT 无撤销)
- 旧有 WebAuthn / 邮箱验证码流程完全保持
- `RequireAuth` 中间件继续走 identity-go,但因为 legacy 模式下会自签 token,`iss == "pocket"` 路径也仍可走通

## 4. 降级矩阵(RedClaw 不可达时)

| 入口 | 行为 |
|---|---|
| `POST /api/auth/login` | 返回 503 `redclaw auth unavailable, please retry`(仅当 `POCKET_DEV_AUTH=true` 时降级到 admin/Veritrans&9527) |
| `POST /api/auth/send-code` | **保持本地** — RedClaw 暂无对应端点,继续走本地 `codeStore` |
| `POST /api/auth/register` | **保持本地** — 同上 |
| `POST /api/auth/code-login` | **保持本地** — 同上 |
| `POST /api/auth/forgot-password` | **保持本地** — 同上 |
| `POST /api/auth/reset-password` | RedClaw 不可达 → 503(legacy 模式则走本地 userStore) |
| `POST /api/auth/logout` | 网络/5xx → 503,但前端已 catch 失败,本地状态仍清 |
| `GET /api/auth/me` | RedClaw 不可达 → 503 |
| `GET /api/auth/sso/login` | RedClaw 不可达 → 503;前端 LoginView 探测时关闭 SSO 按钮 |

## 5. 影子表语义(本地 5 张表的现状)

| 表 | 切换前 | 切换后 | 写入路径 |
|---|---|---|---|
| `users` | 权威源 | **影子** — 仅供 WebAuthn 凭据绑定、邮箱验证码查询 | 本地 register/code-login(保留) |
| `email_codes` | 权威源 | **影子** — 本地 register/code-login/forgot 继续用 | 本地发送/校验 |
| `workspaces` | 权威源 | **影子** — RedClaw 用户登录后调 `EnsureDefaultWorkspace` 创建/查找 | `internal/identity/store.go` |
| `workspace_members` | 权威源 | **影子** — owner 仍是本地邀请,invitee 仍由本地管理 | `internal/identity/store.go` |
| `identity_shadow` | 已存在 | **新增 `provider='redclaw'` 行** | `auth.RecordShadow()` |

> 切换不删除这些表;`DROP TABLE` 是后续 PR 的事,本期保留以保证回滚路径仍能跑。

## 6. 配置矩阵

| 变量 | 默认 | 必填(非 legacy) | 说明 |
|---|---|---|---|
| `POCKET_REDCLAW_ADMIN_URL` | 空 | ✅ | RedClaw Admin 后端,如 `http://redclaw-admin:28081` |
| `POCKET_REDCLAW_ADMIN_SECRET` | 空 | ✅ | HS256 共享密钥,≥ 32 字节 |
| `POCKET_REDCLAW_AUTH_AGENT_URL` | 空 | ❌ | OIDC SSO 入口;空 = 与 Admin 同址 |
| `POCKET_REDCLAW_ADMIN_TIMEOUT_SEC` | 10 | ❌ | HTTP 超时 |
| `POCKET_REDCLAW_SSO_ENABLED` | false | ❌ | true 时 LoginView 多出"企业 SSO"按钮 |
| `POCKET_AUTH_LEGACY_ONLY` | false | ❌ | **紧急回滚**开关 |
| `POCKET_DEV_AUTH` | false | ❌ | dev 旁路(RedClaw 不可达时降级) |
| `POCKET_JWT_SECRET` | dev 默认 | ❌ | legacy 模式 / identity-go fallback 用 |
| `POCKET_REDCLAW_AUTH_URL` | 空 | ❌ | RedClaw auth-agent 镜像客户端(保留,用于未来功能) |
| `POCKET_REDCLAW_AUTH_SECRET` | 空 | ❌ | 同上 |

`config.Validate()` 在生产模式下(非 legacy)强制检查:
- `POCKET_REDCLAW_ADMIN_URL` 非空
- `POCKET_REDCLAW_ADMIN_SECRET` ≥ 32 字节
- 任一不满足 → `log.Fatal` 启动失败

## 7. 新增/修改的 HTTP 端点

### 7.1 一期新增

| Method | Path | 说明 |
|---|---|---|
| `POST` | `/api/auth/logout` | 撤销 RedClaw session;401 视作幂等成功 |
| `GET`  | `/api/auth/me` | 拉取当前 RedClaw employee 画像(需 `requireAuth`) |
| `GET`  | `/api/auth/sso/login` | 拿 RedClaw OIDC 跳转 URL;`?state=&redirect_url=` |
| `GET`  | `/api/auth/sso/callback` | RedClaw 回调入口;302 到 SPA `/auth/sso/callback?token=...` |

### 7.2 一期改造(主路径切换到 RedClaw)

| Method | Path | 切换前 | 切换后 |
|---|---|---|---|
| `POST` | `/api/auth/login` | 本地 `users` 表 bcrypt 校验 → `jwtSigner.SignWithWorkspace` | RedClaw `/api/v1/auth/login` 透传 token(legacy/dev 旁路保留) |
| `POST` | `/api/auth/reset-password` | 本地 userStore 改密 | RedClaw `/api/v1/auth/change-password`(legacy 保留) |

### 7.3 一期保持不变(本地流程,等 RedClaw 暴露对应端点后切)

| Method | Path | 说明 |
|---|---|---|
| `POST` | `/api/auth/send-code` | 本地 `codeStore` + 镜像到 RedClaw |
| `POST` | `/api/auth/register` | 本地 + 镜像 |
| `POST` | `/api/auth/code-login` | 本地 + 镜像 |
| `POST` | `/api/auth/forgot-password` | 本地 + 镜像 |

## 8. SSO 完整流程(浏览器视角)

```
┌──────────┐    1. 点 SSO 按钮            ┌──────────┐
│ 浏览器   │ ──────────────────────────► │ openpocket│
│ (SPA)    │   fetchSsoLoginUrl(state)  │  frontend │
│          │ ◄── {url: "https://..."} ── │           │
│          │                              └──────────┘
│          │ 2. window.location = url
│          │
│          │ 3. 跳到 RedClaw Auth Agent    ┌──────────┐
│          │ ────────────────────────────►│ RedClaw  │
│          │  redirect_url=https://app/   │ Auth Agent│
│          │           api/auth/sso/cb    │          │
│          │                              └──────────┘
│          │ 4. Auth Agent 重定向到 IdP
│          │ ────────────────────────────►┌──────────┐
│          │                              │ IdP      │
│          │ ◄── 用户登录 ──              │ (OIDC)   │
│          │ 5. IdP 回调 RedClaw          └──────────┘
│          │     RedClaw 铸平台 JWT (2h)
│          │
│          │ 6. RedClaw 302 →             ┌──────────┐
│          │    openpocket/api/auth/sso/cb│ openpocket│
│          │ ────────────────────────────►│  backend │
│          │                              │  (pocketd)│
│          │    backend handleAuthSsoCb   │          │
│          │    校验 state、调 AdminAuthClient.SsoCallback
│          │    302 → /auth/sso/callback?token=...&user=...&user_id=...
│          │
│          │ 7. 浏览器被 302 到 SPA       ┌──────────┐
│          │ ────────────────────────────►│ SsoCallback│
│          │                              │ View.vue │
│          │    落 store、fetchMe 拉画像  │          │
│          │    router.replace('/ai')     │          │
│          │ ◄─── 进入首页 ───────────    └──────────┘
└──────────┘
```

### 8.1 state 防 CSRF

- 前端在 sessionStorage 落 `pocket_sso_state = "sso-..." + ts`
- 透传给 RedClaw;RedClaw 回调时原样回传
- SsoCallbackView 校验 `state === sessionStorage.pocket_sso_state`,不等则报错

### 8.2 token 注入方式

后端 302 用 query 参数注入 token(非 fragment),因为:
- openpocket 用 vue-router history 模式,fragment 不会被后端看到
- 但 fragment 会被 vue-router 看到,适合前端路由处理;这里用 query 是为了简化,后端能用 `r.URL.Query()` 直接读

> 安全:query 会被浏览器历史 / 代理日志记录。RedClaw 颁发的 JWT 2h 短期,泄露窗口可控。若需更严格可改 fragment 模式。

## 9. 跨项目 token 互信

未变:`internal/auth/identity.go` 的 `VerifyToken` 已支持多 issuer 优先,`redclaw` 已在 `DefaultIssuerAllowlist()`:

```go
return []string{"redclaw", "memora", "llm-gateway", "pocket", "acc"}
```

`IDENTITY_SHARED_SECRET` 与 `POCKET_REDCLAW_ADMIN_SECRET` 应**取自同一密钥源**(`/identity-shared-secret`),以保证 RedClaw 颁发的 token 在 openpocket 端可校验。

## 10. 测试覆盖

| 包 | 覆盖 |
|---|---|
| `internal/redclaw/admin_auth_client_test.go` | 15 用例:Login/Me/ChangePassword/Logout/SsoLoginURL/SsoCallback 全 happy/4xx/5xx/网络/空参/timeout/tenant_mismatch |
| `internal/auth/*_test.go` | 既有 7 个测试文件,无回归(单跑 `./internal/auth/` 通过) |
| `internal/identity/*_test.go` | 既有,无回归 |
| `internal/server/auth_*_test.go` | C8 系列需要 PG(预先存在的依赖),本地无 PG 时仍 FAIL,与本 PR 无关 |

## 11. 部署

### 11.1 环境变量(`.env.local` 样例)

```bash
# RedClaw 认证主权威源(一期必填)
POCKET_REDCLAW_ADMIN_URL=https://redclaw-admin.internal:28081
POCKET_REDCLAW_ADMIN_SECRET=$(cat /run/secrets/identity-shared-secret)
POCKET_REDCLAW_AUTH_AGENT_URL=https://redclaw-auth-agent.internal:28082
POCKET_REDCLAW_SSO_ENABLED=true

# 老本地 JWT(仅 legacy 回滚路径使用,生产必须保持默认)
POCKET_JWT_SECRET=$(cat /run/secrets/pocket-jwt-secret)

# 紧急回滚开关
POCKET_AUTH_LEGACY_ONLY=false
```

### 11.2 启动顺序

1. 启动 RedClaw Admin + Auth Agent(参考 `RedClaw/deploy/`)
2. 启动 openpocket backend `pocketd` — 若 `POCKET_REDCLAW_ADMIN_URL` 不可达且 `POCKET_AUTH_LEGACY_ONLY=false`,启动会 `log.Fatal`
3. 启动 openpocket frontend(由 `docker-compose.opp.yml` 编排)
4. 用户登录页看到 "密码 / 邮箱验证码 / 企业 SSO" 三个入口

### 11.3 健康检查

- `GET /api/auth/me` 401 → RedClaw 不可达
- `GET /api/auth/sso/login?state=probe` 200 → RedClaw SSO 启用
- `GET /api/auth/sso/login?state=probe` 404 → RedClaw SSO 未启用

## 12. 待办(下一期)

- [ ] WebAuthn assertion 上送 RedClaw 验证(等 RedClaw 暴露 challenge 端点)
- [ ] RedClaw SAML 路由(等 RedClaw 实现)
- [ ] RedClaw send-code / register / code-login 端点对接(目前走本地影子)
- [ ] 切换成功后清理 users / email_codes 表(先做数据迁移脚本)
- [ ] token 自动续期:前端在 1h50m 时静默 `me` 一次,401 才跳登录页

## 13. 变更 commit 列表

```
1. feat(redclaw): add AdminAuthClient for RedClaw /api/v1/auth/* and SSO  (11b7388)
2. refactor(auth): proxy login/reset/logout/me/sso to RedClaw admin        (c1ea05d)
3. docs(env): document RedClaw admin auth envs in .env.example             (dd7257c)
4. feat(frontend): RedClaw SSO tab + logout/me/sso API + sso callback      (dfa7069)
5. docs: AUTH_REDCLAW_MIGRATION.md                                          (本文件)
```

# Handoff: pocket ↔ RedClaw SSO state 合约错配（回调链未闭环）

> **日期**: 2026-09-05
> **来源**: openpocket 认证体系审计（对照 docs/AUTH_REDCLAW.md）
> **状态**: pocket 侧已落地（方案 C' + P1-2 修复，见 §6）；RedClaw 侧协同项（方案 A 升级、本地栈 IdP）待办
> **严重级**: P1（SSO 功能链路断裂风险 + CSRF 防线弱于文档）

## 1. 结论（TL;DR）

1. **RedClaw auth-agent 侧实现与文档完全一致且安全**：`LoginURL` 用
   `randomToken(32)` 生成 CSPRNG state + 16 字节 nonce，`replayGuard`
   5 分钟 TTL、单次原子消费、常量时间 nonce 比较
   （`services/platform-go/internal/authagent/sso/{sso.go,replay.go}`）。
2. **但 pocket 侧的 SSO 集成与该实现存在结构性错配**：pocket 前端生成的
   state 传到 auth-agent `/sso/login` 后被**忽略**（handler 只读 `origin`，
   见 `handlers.go:19-34`），auth-agent 自行生成 state 并由 IdP 原样带回。
   因此 pocket 收到的回调 state 必然是 auth-agent 的 state，而不是
   sessionStorage 里前端自留的那份。
3. **IT 证据自证未闭环**：`test-evidence/redclaw-auth-it-2026-09-05/REPORT.md`
   R5 仅验证了 `/sso/login` URL 生成；第 55 行明确记录
   "SSO 完整回调链未测：本机无 OIDC IdP 容器"。RedClaw 本地栈
   `deploy/local-stack/config/deploy.env` 也未配置 `OIDC_REDIRECT_URL`。

## 2. 错配细节

pocket 现有流程（`server_auth_extended.go:handleAuthSsoLogin/Callback` +
`admin_auth_client.go:SsoLoginURL/SsoCallback`）：

```
前端生成 state X → sessionStorage
→ GET /api/auth/sso/login?state=X
→ 返回 {authagent}/api/v1/sso/login?state=X     ← auth-agent 忽略 X
→ auth-agent 生成 state Y → 302 IdP（state=Y）
→ IdP 回调 redirect_uri（本地栈未配置，指向不明）
→ pocket /api/auth/sso/callback?code&state=?
→ 透传给 auth-agent /sso/callback               ← 只认 auth-agent 自发的 state
→ 302 SPA，回显收到的 state
→ SsoCallbackView 与 sessionStorage 的 X 严格比对  ← 永远不等（收到的是 Y）
```

两种 redirect_uri 情形都走不通：

- **redirect_uri → auth-agent**：auth-agent 直接消费 state 并把 JSON 返回给
  浏览器，pocket 完全不在环内（pocket SSO 死路）。
- **redirect_uri → pocket**：pocket 透传 `state=Y` 能通过 auth-agent 校验，
  但最后前端 `X ≠ Y` 严格比对必然失败（pocket SSO 死路）。

## 3. 可选修复方案（需两仓库对齐）

**方案 A（推荐，RedClaw 侧小改）**：auth-agent `/sso/login` 接受可选
`external_state`（或 `relay_state`）参数，LoginURL 原样存入 replayGuard
entry，`/sso/callback` 校验自身 state 后把它回显到给 pocket 的响应/重定向中。
pocket 侧保持现有"前端生成 + 严格比对"的纵深防御。改动集中、语义清晰。

**方案 B（仅 pocket 侧，弱化纵深）**：承认 state 所有权归 auth-agent，pocket
前端取消 sessionStorage 严格比对（CSRF 由 auth-agent 的 replayGuard 兜底，
code 只能被 auth-agent 消费一次，攻击者拿不到 token）。pocket 侧检查降级为
"state 存在性校验"。无需 RedClaw 改动，但失去 pocket 域的 CSRF 纵深。

**方案 C（重排流程，pocket 侧中改）**：pocket 不再把前端 state 传给
auth-agent，改为 pocket 后端在 `/api/auth/sso/login` 时生成 state、短期
缓存、auth-agent 回调后由 pocket 后端比对（文档 §6.3.2 的服务端防重放）。
仍需 redirect_uri 指向 pocket，且要和 auth-agent 确认"透传 code+state 再交
换"路径（当前 `/sso/callback` 是 GET 且无鉴权，见 P1-2 关联项）。

## 4. 关联发现（同审计产出）

- **P1-2 token 经 URL 下发**：`handleAuthSsoCallback` 用 302 query 传
  `token=...`，进浏览器历史/访问日志。建议改一次性 code 换 token
  （与上面任一方案同批做）。
- **pocket 侧已落地的配套修复（本次提交）**：生物识别登录在
  "RedClaw 认证为主权威源但 gateway bridge 未配置"时改为 fail-closed；
  镜像客户端共享密钥必填；legacy `Parse` 收紧（HS256-only + iss/aud +
  30s leeway）；`POCKET_JWT_SECRET` 死默认值修复；
  `IDENTITY_SHARED_SECRET` 缺失时启动告警（本地 token 不受 RedClaw 撤销
  覆盖）。

## 5. 验收标准

- [x] pocket 侧落地（本仓库可独立完成的部分，见 §6）；
      RedClaw 侧方案 A（external_state 透传）仍待两仓库对齐后升级；
- [ ] 本地栈配置 `OIDC_REDIRECT_URL` 指向 pocket `/api/auth/sso/callback`
      + casdoor IdP 容器（RedClaw 仓库侧）；
- [ ] IT 补测完整链路：login → IdP → callback → 铸 token → SPA 落地 →
      logout 撤销，证据归档 `test-evidence/`（依赖上一项的 IdP 容器；
      本仓库已有 fake auth-agent 的 handler 级全链路测试
      `backend/internal/server/auth_sso_test.go` 覆盖同等断言）。

## 6. 2026-09-05 落地记录（pocket 侧）

RedClaw 仓库不在本机、方案 A 需其配合，故按 §3 方案 C 的思路做 pocket
侧可独立落地的变体（**方案 C'**），并同批修掉 P1-2。核心：承认 state
所有权归 auth-agent（吸收方案 B 的结论），pocket 的 CSRF 绑定改由
**服务端持有的浏览器绑定**承担，而不是比对 IdP 带回的 state。

新流程（`server_auth_extended.go` + `auth_sso_state.go`）：

```
前端不再生成 state
→ GET /api/auth/sso/login
  ← pocket 生成 32B 随机 nonce → pending 表（10min TTL）
  ← 落 HttpOnly+SameSite=Lax cookie pocket_sso_txn（Path=/api/auth/sso/）
  ← 返回 {authagent}/api/v1/sso/login?state=<nonce>&redirect_url=...
     （nonce 现被 auth-agent 忽略；方案 A 落地后可无缝升级为端到端比对）
→ auth-agent 生成 state Y → 302 IdP（state=Y）
→ IdP 回调 redirect_uri（须指向 pocket /api/auth/sso/callback）
→ pocket /api/auth/sso/callback?code&state=Y
  ← 校验并单次消费绑定 cookie（缺失/重放 → 302 SPA error=sso_session）
  ← 透传 code+state=Y 给 auth-agent（由其 replayGuard 校验）
  ← 签发一次性 sso_code（90s TTL，内存表）
  ← 302 SPA /auth/sso/callback?sso_code=...（token 不再进 URL，P1-2 修复）
→ SPA POST /api/auth/sso/exchange {code}
  ← {token, user, user_id, workspace_id}（code 单次消费，重放 401）
```

改动清单：

- `backend/internal/server/auth_sso_state.go`（新增）：pending nonce 表 +
  一次性交换 code 表（进程内存、容量上限、TTL、单次消费）。
- `backend/internal/server/server_auth_extended.go`：三个 handler 重写，
  失败统一 302 SPA 稳定错误码（`sso_session/sso_idp/sso_invalid/
  sso_upstream/sso_no_user`），审计 `auth.sso.callback/exchange`。
- `backend/internal/server/server.go`：注册 `/api/auth/sso/exchange`；
  Server 装配两个内存表。
- `backend/internal/redclaw/admin_auth_client.go`：`SsoLoginURL` 空 state
  时省略参数（当前传 nonce，保留透传位）。
- 前端 `LoginView.vue` / `SsoCallbackView.vue` / `api/auth.ts` /
  `router-mobile.ts`：移除 sessionStorage state 生成与严格比对；回调页改
  为 exchange 换 token + `history.replaceState` 清 URL。
- 测试：`auth_sso_test.go`（store 单元 + fake auth-agent 全链路 6 个用例：
  login 签发、callback 消费/重放拒绝、URL 无 token、exchange 单次、
  错误路径、开关关闭 404）。

安全语义（如实记录）：

- 绑定 cookie 使冷启动回调、重放回调无法完成登录；auth-agent 的
  replayGuard 保证 code 只能被消费一次、token 只会交给透传方。
- login-CSRF（攻击者把自己 IdP 会话的 code+state 注入受害者浏览器）在
  不改造 auth-agent 的前提下无法彻底根除——根治需方案 A 的 external_state
  端到端绑定。cookie 绑定提高了攻击门槛（要求受害者近期在 pocket 域
  发起过 SSO 且 nonce 未被消费），作为过渡纵深。
- 存储为进程内存表：多副本部署时 login 与 callback 必须落在同一实例
  （当前 compose 单实例部署不受影响）；丢失的最坏后果是用户重试登录。

### 6.1 落地后审计修正（同日）

自查发现并修复 6 项：

1. LoginView 每次加载都调 `/api/auth/sso/login` 探测启用状态——铸 nonce
   + 落 cookie 的副作用探测，且旧签名参数误入 `redirect_url`。新增
   零副作用 `GET /api/auth/sso/status` 专供探测。
2. pending nonce 表无 per-IP 限额，单 IP 可在 TTL 内灌满全局表（4096）
   使所有 SSO 登录 500。补 per-IP 并发上限 32（`ssoTxnPerIPCap`）。
3. IdP 回调 `error` 参数（用户可控）未清洗进日志与审计 detail，存在
   控制字符注入面。统一过 `sanitizeAuditDetail`。
4. `AdminAuthClient.SsoCallback` 缺 AuthAgentURL→AdminURL 回落
   （SsoLoginURL 有，构造函数里 authURL 算完即丢），单进程部署下
   callback 会拼出相对 URL。收敛为构造期解析的 `authBase` 共用。
5. 测试用 `New()` 起 hub goroutine（泄漏），改 `newServer(startHubs=false)`。
6. 绑定 cookie 非法路径未清 cookie，补 `clearSsoTxnCookie`。

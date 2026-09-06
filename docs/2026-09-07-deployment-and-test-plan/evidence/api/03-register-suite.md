# 注册与认证流程补测（2026-09-07，PG 模式）

环境：一次性测试库 pocket-e2e-pg:15434/pocket_e2e（schema opencode_pocket），POCKET_SMTP_DEBUG_ECHO=true。
主证据：`../api/01-auth-suite.md` §Register suite / §Register suite 续。

| # | 用例 | 结果 |
|---|---|---|
| R1 | send-code（debug 回显） | ✅ 200 {debug_code, ttl 300} |
| R2 | register 新用户 | ✅ 200 {token, workspace_id: ws_user-e2e-user@example.com} |
| R3 | /me（新用户） | ✅ 200 role=user |
| R4 | 注册用户 login | 首测 401 → **发现并修复 bug**（见下）→ ✅ 200 auth_method=legacy |
| R5 | 错误密码 login | ✅ 401 |
| R6 | 重复邮箱注册 | ✅ 409 邮箱已被注册（用 bcrypt 夹具验证码绕过 send-code 频控） |
| R7 | 弱密码注册 | ✅ 400 密码至少 8 位 |
| R8 | 数据闭环 | ✅ users 表落库：e2euser / e2e-user@example.com / verified=t / role=user |
| R9 | admin dev 旁路回归 | ✅ 200 |

## 发现并修复的 Bug（register 200 → login 401）

`handleAuthLogin` 路径 2（POCKET_DEV_AUTH=true）未命中 dev 凭据时直接 401 return，
**不再落回路径 3 的本地 users 表**——dev 模式下所有注册用户永远无法登录。
修复：去掉早退 401，未命中 dev 旁路则继续走 legacy users 表（`server_assistant.go`）。

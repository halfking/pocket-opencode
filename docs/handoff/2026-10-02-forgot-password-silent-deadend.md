# 忘记密码：链路本身是好的，但当前部署下静默地把用户卡死

日期：2026-10-02
范围：`/forgot-password`（`POST /api/auth/send-code` + `POST /api/auth/forgot-password`）
结论：**修一个真缺陷（静默失败）+ 撤回我自己一个错误假设**

---

## 0. 先撤回：上轮我怀疑的「forgotPassword 无人调用、功能损坏」是错的

上轮读到 `frontend/src/api/auth.ts` 的 `resetPassword` 没有任何调用方，
而 `features/auth/ForgotPasswordView.vue` 明明存在，于是我怀疑「忘记密码
功能静默损坏」。**这个判断是错的。**

`resetPassword`（`POST /api/auth/reset-password`，已登录改密）和
`forgotPassword`（`POST /api/auth/forgot-password`，未登录重置）是两个
不同端点。忘记密码走的是后者，链路完整：

- 后端 `server.go:687` 已注册路由
- `handleAuthForgotPassword` 实现完整：校验密码强度 → 验证验证码 → 改密 → 恒返 200
- 前端 `auth.ts:66 forgotPassword` 被 `ForgotPasswordView.vue:161` 正常调用
- 登录页 `LoginView.vue:108` 有「忘记密码？」入口
- 路由 `router-mobile.ts:327` 已注册

`resetPassword` 无人调用是**已知并备案**的欠账，不是新 bug：
`frontend/scripts/dead-api-baseline.json` 的 `auth.ts:resetPassword`
明确列在「允许存在的『导出了但无人调用』符号」里，且卡口是棘轮（只许减少）。

端到端实测（`scripts/verify-forgot-password-e2e.mjs`，跑在 :8098 隔离实例）：

```
PASS  2.send-code(register)  status=200
PASS  3.register             status=200
PASS  5.forgot-password      status=200 body={"ok":true}
PASS  6.NEG-CTRL old password rejected  status=401
PASS  7.new password accepted           status=200
```

第 6 步是决定性的：没有它，第 5 步的 200 只能证明「handler 返回了」，
不能证明「密码真的改了」。

---

## 1. 真缺陷：三重阻断，且第一重是静默的

### 阻断一：部署没配 SMTP，验证码根本不会发出去

`backend/internal/notify/smtp.go:40` —— `NewClient()` 在 `Host` 为空时返回
`nil`。`server_auth_extended.go` 的 `sendCodeEmail` 遇到 `smtpClient == nil`
只 `log.Printf` 就返回，不发信。

而 `handleAuthSendCode` 依然恒返 200 `{"ok":true,"ttl_sec":300}`。
这是**刻意的防枚举设计**（不能让攻击者通过响应差异判断邮箱是否注册），
不能改。

### 阻断二：旧前端把「没发出去」显示成「已发送」

`ForgotPasswordView.vue` 的 `requestCode()` 收到 200 就 `step.value = 2`，
推进到「输入验证码」。于是：

- 用户看到界面从第 1 步跳到第 2 步，没有任何错误提示
- 用户永远等不到邮件（因为压根没发）
- 用户只会以为是自己邮箱的问题，或者以为要等很久
- 界面上唯一的提示是「没收到？重新发送」，会把人引向无限重试

**这是静默失败，不是「稍后重试」能解决的**。这是本次修的东西。

### 阻断三：operator 的 admin 账号根本没有邮箱

```
      id              | username |       email        | email_verified | role
----------------------+----------+--------------------+----------------+-------
 user-admin           | admin    |                    | f              | admin
```

`users.email` 为空、`email_verified = false`。即使把 SMTP 配好，
operator 自己的账号也走不通忘记密码。

**这条我没有动** —— 改用户数据需要明确授权。

---

## 2. 修法

### 后端：响应里声明投递能力

`handleAuthSendCode` 的响应增加 `delivery` 字段：

- `delivery: "smtp"` —— 本部署有邮件通道
- `delivery: "none"` —— 没配 SMTP，验证码只入库

**不破坏防枚举**：`delivery` 描述的是「这台机器有没有邮件通道」，
是全局部署事实，与该邮箱是否已注册完全无关。三条恒返 200 的分支
（正常、邮箱格式非法、生成失败）都带上了该字段。

实测 before/after：

```
before: {"debug_code":"764749","ok":true,"ttl_sec":300}                  ← 无 delivery
after:  {"debug_code":"764749","delivery":"none","ok":true,"ttl_sec":300}
```

### 前端：死路就别把人推进去

判定抽成纯函数 `frontend/src/features/auth/code-delivery.ts`
的 `judgeCodeDelivery()`，视图调用它。规则：

| delivery | debug_code | 结果 | 理由 |
|---|---|---|---|
| `smtp` / 缺失 | 任意 | 推进 | 老后端不返回该字段，不能误伤已部署实例 |
| `none` | 有 | 推进 | dev 模式（`POCKET_SMTP_DEBUG_ECHO`），流程仍走得通 |
| `none` | 无 | **拦住 + 报错** | 生产形态的死路 |

顺带修了 `ForgotPasswordView.vue` 副标题里一句不实的话：
原文写「密码统一由 RedClaw 管理」，但当前部署根本没配 RedClaw Admin，
走的是 legacy/dev 本地路径。

---

## 3. 验证

### 端到端（`scripts/verify-forgot-password-deadend.mjs`，跑在 :8097 生产形态实例）

```
PASS  A.send-code still 200 (anti-enumeration preserved)  status=200
PASS  B.delivery === "none"                               delivery="none"
PASS  C.no debug_code (production shape)                  debug_code=undefined
PASS  D.frontend gate BLOCKS the stepper                  advance=false
PASS  D.error is user-facing and actionable
```

D 导入的是**视图自己用的那个模块**，不是重写一遍，所以验的是真实决策路径。

### 负控对照

| 负控手法 | 期望 | 实测 |
|---|---|---|
| 前端 `judgeCodeDelivery` 还原成修复前（永远推进） | 精确转红 | 4 例中 1 例转红，其余 3 例（断言放行的）保持绿 |
| 后端响应去掉 `delivery` 字段 | 精确转红 | 3 子用例中 2 个 delivery 断言转红，防枚举那条保持绿 |

两轮负控都只让「目标断言」转红，说明测试有判别力，不是无差别失败。

### 回归

- 前端 `npm run gates` → **exit 0**（含新加的 `test:auth`，已接进 gates 链）
- 后端 `go build -p 1 ./...` → exit 0
- 后端 `go test -p 2 -count=1 -skip TestDiag ./...`（带真 `POCKET_TEST_POSTGRES_DSN`）→ **exit 0，零 FAIL**

新测试：
- `frontend/src/features/auth/__tests__/code-delivery.test.mjs`（4 例，已接进 `gates`）
- `backend/internal/server/server_auth_sendcode_delivery_test.go`（3 子用例）

---

## 4. 仍然没有解决的（需要人）

1. **真机未验证**。本次修复只做了宿主侧 HTTP 端到端 + 单元/负控，
   **没有在 `192.168.31.19` 上看过一眼**。自 16:40 起该机 adbd 无响应，
   宿主侧手段已用尽。
2. **admin 账号无邮箱**，忘记密码对 operator 依然不可用。
3. **设置页没有「修改密码」入口**。`resetPassword` 后端与客户端都就绪，
   只差 UI；补 UI 属新功能，需授权。
4. **SMTP 未配置**。即使前两条都解决，不配 SMTP 仍然收不到邮件。

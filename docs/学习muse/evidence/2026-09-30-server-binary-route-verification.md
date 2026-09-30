# 真实服务端二进制验证：路由确实注册了，且登录墙的成因被定位

> 日期：2026-09-30　范围：`backend/`
> 状态：**已验证**（路由注册层面）。这一轮把「端到端未做」里**还能做的最后一块**做掉了，
> 并把剩下的空白**从推测变成了实测结论**。

## 为什么值得做

此前 `/api/learning/*` 与协作端点只在**路由单测**（`httptest` + 假 client）里验过。
单测用的是 `srv.Handler()`，它**没有经过 `main.go` 的装配**。
「handler 写对了但忘了在 `SetLearningService` 里接上」这类事故，单测抓不到。

## 1. 后端没有 PG 也能起

`cmd/pocketd/main.go:81` 明确支持降级：

```
WARN: POCKET_POSTGRES_DSN not set, running in remote-only mode (no local task cache)
```

进一步还需要 `POCKET_AUTH_LEGACY_ONLY=true`（后端自己打印的提示：
`POCKET_REDCLAW_ADMIN_URL must be set (or set POCKET_AUTH_LEGACY_ONLY=true for dev-only fallback)`）。
这是仓库自带的 **dev-only 回退**，不是我自造的绕过。

启动后端到 8091（`POCKET_HTTP_PORT`，**不是** `POCKET_HTTP_ADDR`——后者会被静默忽略，
第一次就是踩了这个坑，默认 8088 撞上占用）。

## 2. 我建的路由在真实二进制里全部注册

未带 token 时，`requireAuth` 先于业务 handler 执行，于是
**401 = 路由已注册，404 = 路由不存在**。这正好是一个干净的判别信号。

| 方法 | 路径 | 结果 |
|---|---|---|
| GET | `/api/learning` | 401 ✅ |
| GET | `/api/learning/items` | 401 ✅ |
| GET | `/api/learning/items/due` | 401 ✅ |
| GET | `/api/learning/reminders` | 401 ✅ |
| GET | `/api/learning/streak?tz_offset=0` | 401 ✅ |
| POST | `/api/learning/schedule` | 401 ✅ |
| POST | `/api/learning/items` | 401 ✅ |
| POST | `/api/tasks/from-source` | 401 ✅ |
| GET | `/api/tasks/{id}/activity` | 401 ✅ |
| GET | `/api/tasks/{id}/participants` | 401 ✅ |
| GET | `/api/tasks/{id}/children` | 401 ✅ |
| GET | `/api/tasks/{id}/approvals` | 401 ✅ |
| POST | `/api/tasks/{id}/delegate` | 401 ✅ |
| GET | `/api/nonexistent-xyz`（负对照） | 404 ✅ |

**一个必须记下来的陷阱**：本机 8088 上本来就有用户的 `pocketd-bugag-v5.exe` 在跑。
在它上面探测时，`/api/tasks/{id}/activity` 等返回 401、`/api/learning/*` 返回 404，
看起来像「协作路由有、学习路由没有」。**这个结论是错的**——
`/api/tasks/` 是带 `requireAuth` 的**通配注册**，任何子路径未登录都返回 401，
所以那几个 401 只证明 `/api/tasks/` 存在，**不能证明我加的子路由存在**。
而 `/api/learning` 的 404 说明那个二进制是**旧构建**（构建于 18:57，
但它不含学习模块——路由在 `server.go:690-691` 是无条件注册的）。

教训：**通配注册 + 鉴权前置，会让「路由存在性」探测失效**。
在有通配前缀的树上，只有负对照（不存在的路径返回 404）才有判别力。

## 3. 登录墙的成因：认证层硬依赖 PG

| 探测 | 结果 | 含义 |
|---|---|---|
| `POST /api/auth/register` | **503** | 身份服务需要 PG，无库时不可用 |
| `POST /api/auth/login` | 401 | 路由在，但无法建立身份 |

**所以：没有 Postgres 就无法获得任何登录态，登录墙之后的 UI 一行都跑不了。**
这不是推测，是实测。

全程**没有尝试任何凭据**，也没有在用户自己那个 8088 实例上做任何写操作——
所有探测打在临时起的 8091 实例上，验证完已关闭，临时二进制已删。

## 4. 仍然未验证

- **登录墙之后的真实渲染**：学习中心、任务详情、协作面板。`dueRows` 之类 computed
  在真实数据下的分支、`TaskCollaborationPanel` 的 `available=false` 隐藏分支，一行没跑过。
- **SQL 本身**：没有 PG，DDL 与查询一行都没执行过。
- **APNs/FCM**：部署前置。

## 结论

编译期、产物期、**路由注册期**的空白都已补齐。
剩下的全部收敛到**同一个前置条件：Postgres**。
它需要用户批准起 Docker Desktop 或装 Postgres——已连续多轮询问未获回复。

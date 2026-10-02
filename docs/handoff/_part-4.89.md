
# §4.89 32 个探针里 API 族先跑起来：5 个只读脚本产出真证据，并追出两条线索（一条是死路）

> 承 §4.88。§4.88 把 32 个脚本的**口令来源**修好了，但它们多数还**硬编码 8088**——
> 那个端口现在没有服务在跑。所以「语法通过」≠「能跑」：逐个实跑会全部撞 ECONNREFUSED，
> 看起来像「脚本坏了」，其实是**地址写死**。本轮把 API 族改成走 env 并**真的跑起来**。

## §4.89.0 结论

- **16 个脚本的 API base 改成环境变量**（`POCKET_API_HOST` / `POCKET_API_PORT`），
  约定与仓库里已有的 env 版（`probe-instances-api.mjs`）一致。
- **5 个只读脚本实跑通过（全部 exit=0），产出真实证据**；其中 2 个还带出了新线索。
- 追查两条线索：① `GET /api/flashcards?since=0` 返回 **`{"cards":null,"decks":null}`**
  ——**不是缺陷**（前端有 `Array.isArray` 兜底），但**没有那些兜底就会崩**；
  ② `default` vs `ws_user-admin` 数据孤岛 —— **§4.26.2 已记录的旧项**，不是新发现。
- **7 个会写数据的脚本一律没跑**（`vault/sync`、`llm-gateway/nodes`、`email/accounts`、
  `emails/sync`、`marketplace/submit`、`finance`）——它们写的是**与另一会话共享的开发库**。

## §4.89.1 必须先分清两族：CDP 端口 ≠ API 端口

清点时我一开始把两族混为一谈了。实测形态：

| 族 | 形态 | 数量 | 该怎么修 |
|---|---|---|---|
| **CDP / 设备族** | `const PORT = process.env.POCKET_CDP_PORT \|\| '92xx'` | 多数 | 迁 `lib/adb-cdp.mjs`（`forward tcp:0`）——**就是 §4.86 那 138 处的本体** |
| **API 族** | `const HOST = '127.0.0.1'; const PORT = 8088` 或内联 `port:8088` | 16 | 改成 `POCKET_API_HOST` / `POCKET_API_PORT` |

判据用**字面量 8088** 区分两族，因为 CDP 那批写的是 92xx。
**混在一起改会把「CDP 端口」也改成 API 端口** —— 那是把好实现换成瞎实现。

> 顺带纠正清点脚本自己的一个错：它把 `lib/dev-pass.mjs` 和 `migrate-dev-pass.mjs`
> 也算进了「已迁移脚本」，因为这两处的**文档/常量里含同样的 import 字符串**。
> 实际是 32 个，不是清点报出的 34 个。

## §4.89.2 实跑的 5 个（全部只读、全部 exit=0）

跑之前先用一条**区分得开**的判据把脚本分成读写两族。第一版判据写错了：
`\b(POST|PUT|PATCH|DELETE)\b` 把**登录**也算成写方法 ⇒ 16 个全标「有写方法」。
改成「写方法的路径里排除 `auth/login`」才分清：9 个只读、7 个写。

```
probe-marketplace-404      exit=0
  /api/marketplace/agents     无 token 401 · 带 token 404  {"error":"not found"}
  /api/marketplace/skills     无 token 401 · 带 token 404
  /api/marketplace/installs   无 token 401 · 带 token 404

probe-marketplace-agents   exit=0
  401  不带 token                    /api/marketplace/agents
  404  带 token                      /api/marketplace/agents
  404  阴性对照（随机路径）            /api/marketplace/definitely-not-a-route
  200  同族端点（对照）                /api/marketplace/packages
  结论：不带 token 的 401 只是鉴权层，不是路由结论。

probe-login-paths         exit=0
  4 条登录路径全 200，auth_method=dev-bypass workspace_id=ws_user-admin

diag-workspace-claim      exit=0
  连续 3 次全新登录：workspace 稳定 ws_user-admin

probe-flashcards-api      exit=0
  GET /api/flashcards?since=0&limit=200 → 200 {"cards":null,"decks":null,...}
  GET /api/flashcards/notes?since=0     → 200 {"notes":null,...}
  GET /api/flashcards/decks             → 404
```

> `probe-marketplace-agents.mjs` 在 §4.88 之前是**完全死**的（拿不到 token），
> 现在它给出的是这条争议最干净的四重对照。**把探针修活，证据的质量会自己上去。**

## §4.89.3 线索一：`cards: null` / `decks: null` —— 查到底，**不是缺陷**

看着就不对：契约测试（`services/__tests__/flashcards.contract.test.ts:171`）写的是数组，
实测却是 `null`。链路查到底：

1. `flashcards_handler.go:68-70` 把 store 的返回值直接塞进 `map[string]interface{}`；
2. `flashcards/store.go:385` 是 `var out []*Card` —— **nil slice**，零行时保持 nil；
3. `encoding/json` 把 **nil slice 序列化成 `null`**（Go 的经典坑）。

**但它不会出事**，因为前端早就防了（`services/flashcards.ts` 的 `pullCards`）：

```ts
cards: Array.isArray(body.cards) ? body.cards : [],
decks: Array.isArray(body.decks) ? body.decks : [],
notes: Array.isArray(body.notes) ? body.notes : [],
```

⇒ **判定为契约味道，不是活 bug**。本轮**不动**后端：改了是对共享代码的无谓行为变更，
而客户端已经能扛；而且**没有那些 `Array.isArray` 兜底的客户端会直接崩在 `.map()` 上** ——
这才是值得写进 handoff 的部分。

`GET /api/flashcards/decks → 404` 同理**正确**：前端用的是
`POST /api/flashcards/decks`（建卡组）与 `GET /api/flashcards/decks/:id/due?now=`，
裸 `GET /decks` 没给 id，item handler 落 default → 404。

## §4.89.4 线索二：`default` 数据孤岛 —— **旧项，不是新的**

`diag-workspace-claim.mjs` 报「API 侧 `ws_user-admin` vs 设备上 App 的 token 是 `default`
⇒ 两个数据孤岛（真缺陷）」。

查 handoff：**§4.26.2 已经记过同一件事**（「App 与 API 可能在两个不同的 workspace（数据孤岛）」），
且与 `identity.EnsureDefaultWorkspace` 的 `ws_<userID>` 约定有关。**不是本轮新发现。**

而且这条输出**不算新鲜证据**：脚本里那句「来自上一轮诊断解出的 JWT payload」说明它
**复用的是上一轮解出的 payload**，不是这次真机上读回来的。按「不可归因的结果不报结论」，
本轮只记为「已知仍开放，待设备侧重新取 token 核实」。

## §4.89.5 本轮遗留

- **7 个写数据的脚本仍没跑**：`probe-vault-api`、`probe-vault-sync-empty-blob`、
  `probe-gateway-nodes-api`、`probe-email-account-api`、`probe-email-sync-honesty`、
  `verify-bug-z`、`diag-finance-workspace` / `diag-finance-samescope` /
  `verify-finance-writepath`。它们的 base 已改 env，**但**要跑得先决定
  「往与另一会话共享的开发库写测试数据」的策略（建议：起一个**独立 PG** 或
  用完即删且自证删干净）。
- **CDP / 设备族（约 14 个）仍硬编码 92xx 端口**，且没实跑——它们依赖设备 + 前向通道。
  这批与 §4.86 的 138 处是同一件事。
- `handleGetInstanceTasks` 疑似死 handler（§4.88.3），未修。
- §4.88.5 其余遗留照旧：22 处硬编码 `MASTER` 兜底；BUG-V11 需生产 env 授权；
  `POCKET_PROD_PASS` 缺失；`_login.yaml` 孤儿；BUG-AX 设备侧负控、闪卡两入口渲染/点击、
  会议写入设备侧持久化、「tap 报 COMPLETED 但没反应」坐标对账、`:param` 模板、
  gateway 六页；待产品定范围 9 项。**「打通所有的功能点」仍不成立。**

### 下一轮建议的第一件事

先定「写路径脚本能不能往共享开发库写」这个前置（它是 7 个脚本 + 一堆功能点写路径的
共同阻塞），再把 CDP 族迁到 `lib/adb-cdp.mjs` 并逐个实跑。

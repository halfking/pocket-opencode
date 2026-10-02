
## §4.113 找到不动 `adb reverse` 也能让真机打到隔离库的办法，并让 3 个 finance 脚本真跑

§4.112 说「设备 forward 指向并发会话的后端，改它是共享可变状态，动手前必须先确认对方没在跑」。
这轮找到了**不碰它**的办法，于是这个卡点解除了。

### §4.113.1 解法：App 的后端地址是 `localStorage` 里一个可改的键

`frontend/src/config/api-base.ts` 是 API 基址的 SSOT，优先级
**localStorage 覆盖 > VITE_API_BASE > 同源**。关键在规则 1 与规则 2 的分工，
源码注释写得很明确：

> 构建默认值：Capacitor 壳上**且**是 loopback 时丢弃——真机不可达……
> **用户显式填的地址不受影响**，因为 `adb reverse` 开发流确实需要用户主动指定 localhost。

⇒ 设备上的 App 之所以打到 18099，是**用户显式填的**，不是构建烘进去的。
实测（只读 CDP）：

```
origin            = https://localhost
pocket_api_base   = "http://127.0.0.1:18099"
pocket_token 长度 = 291
最近的 /api/ 请求： http://127.0.0.1:18099/api/finance、/api/redclaw/health、…
```

那么换一个地址行不行？隔离后端 18101 绑在 `::`（全接口），主机 WLAN 是 `192.168.31.20`，
设备 `192.168.31.19` 同网段。从**页面里**实测（只发 GET，不写任何数据）：

```
fetch http://192.168.31.20:18101/healthz  →  {"ok":true,"status":200,"body":"ok"}
```

**混合内容没有被拦**（Capacitor WebView 允许 cleartext）。所以：

| 方案 | 动的东西 | 谁会受影响 |
|---|---|---|
| 改 `adb reverse` | 设备↔主机的端口映射 | **并发会话**（它就靠这个打自己的后端） |
| **改 `localStorage.pocket_api_base`** | 这台设备上这个 App 的一个键 | 只有下一个用这个 App 的人，且**可原样写回** |

选了后者。`scripts/run-device-against-isolated.mjs` 的纪律：
动手前先探后端可达（连不上就不改，免得把 App 指到虚空）→ **原值原样记录** →
改指向 → 跑脚本 → `finally` 写回，外加 `unhandledRejection` / `uncaughtException` 两个钩子
一起兜（只写在 happy path 上，就会「跑失败就把 App 留在隔离库上」，
让下一个人莫名打到一个空库 —— 与 BUG-V10/V14 同一类）。

### §4.113.2 动手前先证明设备空闲，且分清「有人在用」与「App 轮询」

`scripts/probe-device-idle.mjs`：连续采样 `/api/` 请求条数。
12 秒窗口 0 增长；但 60 秒窗口 +2，于是判红。**不能就此断定有人在驱设备** ——
`/api/redclaw/health`、`/api/scheduled-tasks?since=…` 这类很可能是 App 自带轮询器。
`scripts/probe-device-request-cadence.mjs` 把 URL 逐条打出来区分二者，
结果 45 秒窗口 **0 条新增，完全静止**。

顺带白拿了一份**这个 App 真实会打的端点清单**（Performance 资源表反推，44 个去重端点）：
`/api/finance` `/api/finance/stats` `/api/flashcards` `/api/flashcards/notes`
`/api/learning/*` `/api/llm-gateway/nodes` `/api/rss/*` `/api/emails*` `/api/tasks`
`/api/marketplace/packages` `/api/scheduled-tasks` …

### §4.113.3 verify-finance-writepath.mjs：真机 **26/26 通过**

对着隔离后端（`POCKET_API_PORT=18101` + `POCKET_PG_SCHEMA=opencode_pocket_verify`
+ `POCKET_EXPECT_ORIGIN=https://localhost`）实跑，全绿。关键几条：

```
PASS  API 播种成功（2xx，拿到 id）  — status=201 id=txn_1790962273121029200
PASS  播种后 PG 行数 +1             — 0 -> 1
PASS  读路径：API 播种的记录出现在 UI 列表里 — ↑UI测试-¥11.11 … SEED-273012
PASS  点「记账」后预览出现            — "支出 · 交通 · ¥97.77 确认入账 取消"
PASS  解析接口 2xx                  — status=200
PASS  **直接查 PG** 确认真的写进去了   — 1 -> 2
PASS  PG 最新一条的备注来自 UI 输入的自然语言原文 — note="打车花了 97.77 元"
PASS  POST /api/finance 非 4xx/5xx  — status=201
PASS  界面给出成功反馈（toast）        — toasts=["已入账"]
PASS  ⚠️ 没有失败类反馈与成功类反馈并存
PASS  ⚠️ 没出现「PG 未变却说成功」的假成功 — saidOk=true PG 1->2
PASS  列表回显 / 统计联动（本月支出 -¥108.88）/ 删除生效（2 -> 1）/ 对照组 SEED 仍在
PASS  无未捕获 JS 异常                — 0 条
[cleanup:normal] 删除 SEED -> 204，PG 终值 = 0
26/26 通过
```

跑完三件事同时成立：**共享库 66 表逐表一致**、**隔离库 finance 归零**、
`pocket_api_base` 已写回 `"http://127.0.0.1:18099"`、`adb reverse` 仍是
`host-25 tcp:18099 tcp:18099`（我一个字节没动）。

#### 第一次跑 20/26 —— 6 条 FAIL 全是我 runner 的 bug，不是产品缺陷

头一版 runner 传了 `POCKET_API_PORT` 却**漏了 `POCKET_PG_SCHEMA`**，
finance 脚本的 `SCHEMA` 于是落回默认的 `opencode_pocket`（共享库），
而 App 写的是隔离库 ⇒ 6 条「直接查 PG」判据全红，其中一条打印成
`⚠️ 没出现「PG 未变却说成功」的假成功 — saidOk=true PG 0->0`
——**看起来像一条很严重的真缺陷**。

那条判据其实是对的：它看到的确实是「UI 报成功、它查的库没变」。
**判据红不一定是产品坏了，先问「我查的是不是同一个库」。**
补上 `POCKET_PG_SCHEMA` 后同一脚本 26/26。

同时修掉 runner 两处会掩盖结论的地方：`execFileSync` 在子进程非 0 时会 throw，
不拆开就会把子脚本失败报成 runner 失败；以及子脚本 exit=1 被吞掉、
外层只把自己的 exitCode 带出去（管道里于是显示成 `EXIT=0`）。

### §4.113.4 BUG-V17：diag-finance-workspace 的「App 看不到 SEED」结论是错的

`diag-finance-workspace.mjs` 里那行「App token 调 list」用的是
**相对路径** `fetch('/api/finance')`。Capacitor 壳的 origin 是 `https://localhost`，
相对路径解析到 `https://localhost/api/finance` → 命中本地 index.html → 返回 HTML：

```
App token 调 list = {"error":"SyntaxError: Unexpected token '<', \"<!doctype \"…"}
=== 判定 ===
作用域一致但 App 看不到 SEED —— 需要继续查服务端 ListScoped 过滤或 App 的 fetch
```

两处都错：

1. 真实 App 走的是 `api/http.ts` 的 `${resolveRuntimeApiBase()}${path}`，
   即**绝对 base + 相对 path**。脚本测的那次请求**不是 App 发的请求**。
2. `admin token` 直调同一个接口是 200/hasSeed=true —— 数据在库、作用域也对。

按 `api-base.ts` 的解析顺序改掉（localStorage 覆盖优先，空串=同源），
并把「实际用的 URL」和 Content-Type 一起回传 +
`text/html` 时响亮报 `RETURNED_HTML` 而不是继续往下推。修后实测：

```
App token 调 list = {"url":"http://192.168.31.20:18101/api/finance","status":200,
                     "count":1,"hasSeed":true,"notes":["DIAG-SEED-491547"]}
   [自检] App 侧请求用的是绝对 base = YES
=== 判定 ===
App 其实能看到 SEED —— 读路径 FAIL 是时序/等待问题，不是作用域问题
```

⇒ §4.26.2 那个「`default` vs `ws_user-admin` 数据孤岛」**大半是探针自己造出来的**：
一个用相对路径的假请求 + 一次播种作用域错配。两侧都用 `ws_user-admin` 时一切正常。
这一条待办可以从「数据孤岛」降级为「时序/等待问题」继续查。

`diag-finance-samescope.mjs` 同一轮也自证了这一点：它原本记录的 FAIL 原因正是
「跨工作区错配（测试播 ws_user-admin / App 看 default）」，改成同作用域后读路径正常。

### §4.113.5 本轮我自己的两次失误

- `/^\\//.test(...)` 在正则字面量里被斜杠截断，`.` 之后报 `Unexpected token '.'`。
  改用 `startsWith('/')`。**在会被程序再读一遍的文本里，别嵌套你正在用的分隔符**
  （与 Go 块注释里写 `*/`、`.mjs` 模板串里写反引号是同一类）。
- 第一次写 BUG-V17 修复时，`/^\\//` 那行同时被我自己的**自检判据**忽略了 ——
  自检在 `ev()` 里，没在 Node 侧；`node --check` 才抓到。
  **语法检查和语义自证是两道闸，不能只留一道。**

### §4.113.6 这一节没有解决什么

- 设备侧其余 CDP 族（约 14 个）**尚未**逐个实跑，只是把通道打开了。
- BUG-AX 设备侧负控、闪卡两入口的**点击**、会议写入设备侧持久化，仍未做。
- `default` vs `ws_user-admin` 的**时序/等待**问题需要单独复现，不是本节能结的。

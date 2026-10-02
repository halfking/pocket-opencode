
# §4.85 https 设备侧回归：脚本一直是死的，根因是生产 CORS 白名单缺 Capacitor 壳 origin（本轮）

> 承 §4.84。本轮把「https 设备侧端到端回归」这项遗留从「未做」变成
> **已做完、并且挖出一个真实的生产缺陷**。

## §4.85.0 结论

- **BUG-V10**：`scripts/verify-https-prod.mjs` 自 `b6187bc1` 起**必然跑不完第三步**，
  且失败时会把设备上的 `pocket_api_base` **永久留在生产地址**。已修。
- **BUG-V11（本轮最有价值的发现）**：生产 `POCKET_ALLOWED_ORIGINS`
  **缺 Capacitor 壳 origin**，导致 **Android App 完全无法访问生产**。
  此前 handoff 记的「设备侧受阻于环境（不是代码）」是**误判**——
  设备的网络、DNS、TLS 全部正常。

## §4.85.1 BUG-V10：这条回归脚本从安全整改之后就一直是死的

### 1a. `adminPass` 被删掉、调用点被漏掉

`scripts/verify-https-prod.mjs` 第 89 行写 `password: adminPass`，
而**全文没有任何 `adminPass` 的声明**。页内求值抛 `ReferenceError`，
`ev()` 返回 `exceptionDetails` → 脚本走 `exit 7`。

来源查清了（`git log -- scripts/verify-https-prod.mjs`）：

```
a5a435b7  test(https): 生产 https 路径回归 —— 服务端侧已验证，设备侧受阻于环境（不是代码）
b6187bc1  fix(security): dev 旁路移除硬编码 admin 口令 + 卡口补 password-literal 规则
```

`b6187bc1` 把硬编码口令从 8 个文件里清掉（`repohygiene/secrets_test.go` 的
`password-literal` 规则要求），却**漏了这个调用点**。

⇒ 与 **BUG-V5** 同一类：安全整改删掉一个**字面量**，某个消费者仍按名字找它，
而消费者的报错指向的是一个已经不存在的东西。
（BUG-V5 是 `start-local-backend.ps1` 去正则抠已删除的 `devPass = "…"`。）

**为什么一直没被发现**：那次提交的说明写的是「设备侧受阻于环境」。
一条必然 `exit 7` 的脚本，被归因成了环境问题。**「受阻于环境」是一个能吸收
任何失败的解释**——它让死脚本看起来像外部约束。

修法：口令只从环境取（`POCKET_PROD_PASS`，回落 `POCKET_AUTH_PASS`），
**缺口令时在碰设备之前就停**（`exit 8`）。
负控：不给口令跑 → `exit 8`，且 `adb forward --list` 为空（没碰设备）。

### 1b. 失败路径不还原覆盖值（这个后果比报错本身危险）

原脚本是顺序直线代码：覆盖写在第 89 行、**还原在第 148 行**，
中间第 137 行有 `process.exit(7)`。一旦链路探针失败，
设备上的 `pocket_api_base` 就**永久留在 `https://pocket.itestu.cn`** ——
下一个跑真机的人会以为自己在测本地后端，实际在打**生产**。

改成 `restoreBase()` + 幂等标志，`exit(6)` 与 `exit(7)` 两条失败路径都调用它；
再加 `process.on('exit')` 在没还原时报警。

### 1c. 判据恒真：无论结果如何都 `exit(0)`

原末尾只打 ✅/❌ 然后**无条件 `exit(0)`** —— 全链路失败也会被任何自动化
跑当成成功。改成 `allOk ? 0 : 1`。

### 1d. 硬编码固定 CDP 端口

`const PORT = process.env.POCKET_CDP_PORT || '9472'` + `forward tcp:${PORT}`。
固定端口是**共享可变状态**（同机还有别的会话在驱同一台设备），
撞上就抛 10048 而报错指向装置。改成 `tcp:0`（同 BUG-V9 的修法）。

## §4.85.2 BUG-V11：生产 CORS 白名单缺 Capacitor 壳 origin

### 现象

用 `scripts/probe-https-device.mjs`（**不需要任何凭据**的只读探针，
只用 `localStorage.pocket_api_base` 覆盖，不重打 APK）：

```
原 pocket_api_base = "http://127.0.0.1:18099"
已写入 = https://pocket.itestu.cn
  ❌ /healthz           Failed to fetch
  ❌ /api/tasks         Failed to fetch
  ❌ backup /healthz    Failed to fetch
已还原 pocket_api_base = "http://127.0.0.1:18099"
```

### 关键一步：**区分「没外网」与「CORS 被拦」**

`Failed to fetch` 在 WebView 里**既可能是没有外网，也可能是 CORS 被拦**，
两者报错字面完全一样。绝不能混为一谈。用**设备 shell 的 curl** 判别
（它不受 CORS 约束）：

```
https://pocket.itestu.cn/healthz  -> 200  text/plain; charset=utf-8   1.13s
https://pocket.itestu.cn/api/tasks -> 401  application/json            0.09s
https://pocket.kxpms.cn/healthz    -> 200  text/plain                  0.07s
ping 223.5.5.5 -> 2 packets transmitted, 2 received, 0% packet loss, 32ms
```

⇒ **网络、DNS、TLS 全部正常。失败特定于 WebView 的 `fetch` ⇒ CORS。**

### 定位到具体哪一条规则

`GET /api/tasks` 带 `Origin: https://localhost`：

```
HTTP/1.1 401 Unauthorized
Access-Control-Allow-Headers: Content-Type, Authorization
Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS
Access-Control-Max-Age: 3600
（没有 Access-Control-Allow-Origin）
```

**CORS 预检 `OPTIONS` → `403 Forbidden`**（`X-Pocket-Upstream` 显示它转到了上游，
是上游/中间件拒的，不是 nginx 拦的）。

App 的 `http()` 每个请求都带 `Authorization: Bearer` ⇒ **不是 CORS 简单请求**
⇒ 浏览器必先发预检 ⇒ 预检 403 ⇒ 真实请求根本发不出去 ⇒ `Failed to fetch`。

### 枚举各 Origin（`scripts/probe-prod-cors-origins.mjs`，从真机 shell 发起）

| Origin | 状态 | `Access-Control-Allow-Origin` |
|---|---|---|
| `https://pocket.itestu.cn` | 401 | **回显自身** ✅ |
| `https://localhost` | 401 | **缺失** ❌ |
| `capacitor://localhost` | 401 | **缺失** ❌ |
| `http://localhost` | 401 | **缺失** ❌ |
| `http://127.0.0.1:4175` | 401 | **缺失** ❌ |
| `https://evil.example`（对照） | 401 | 缺失（安全侧正常） |

⇒ 生产白名单里只有它自己的域，**三个 Capacitor 壳 origin 全都不在**。

### 代码侧核对：后端逻辑是对的，配置缺项

- `server.go:1136-1144` `corsMiddleware`：`origin != "" && originChecker(r)`
  时才设 `Access-Control-Allow-Origin`——设计正确。
- `server.go` `buildOriginChecker`：`devAuth=true` 时会放行
  `http(s)://localhost` 与 `127.0.0.1`；**生产 `devAuth=false`**，
  于是走**精确字符串匹配** `originSet[origin]`。
- `config.go:427`：生产必须显式配 `POCKET_ALLOWED_ORIGINS`，**无缺省值**。
- **`PLAN.md:57` 早就写明**应当含
  `https://pocket.itestu.cn, https://localhost, capacitor://localhost, http://localhost:4175`
  —— 实际生产只配了第一个。

⇒ **这是配置/部署缺项，不是代码缺陷。** Android 壳的 origin 是
`https://localhost`（`api-base.ts` 的 `CAPACITOR_SHELL_ORIGIN`，
Android 默认 `androidScheme`），不在白名单 ⇒ App 对生产**完全不可用**。

### 修法（需要运维/产品授权，我单方面不做）

生产 env 的 `POCKET_ALLOWED_ORIGINS` 补上：

```
https://pocket.itestu.cn,https://localhost,capacitor://localhost,http://localhost
```

补完后用本节的枚举脚本复跑，三个壳 origin 都应「回显自身」。

⚠️ 顺带说明：这也解释了此前那些**看似无关**的生产症状
（flashcards 全 404、chat-agents 500）——在预检就 403 的前提下，
那些状态码根本不可能是后端业务逻辑给出的。**先修 CORS 再谈那些 404/500。**

## §4.85.3 我本轮写坏的两个判据（都已修，记录在案）

1. **`$acao` 保留上一轮旧值**。PowerShell 版枚举脚本里
   `($h | Select-String ...).ToString().Trim()` 没命中时是 `$null`，
   `.ToString()` 抛错，而 `$acao` **保留上一轮的值** ⇒ 打印出
   「`https://localhost` 被回显成 `https://pocket.itestu.cn`」这种假数据，
   看起来像有、其实没有。改用 Node 重写，判据只看**本轮这一行**。
2. **判据方向写反**。`evilOk = echoed(evil) === 'https://evil.example'` ——
   恶意 Origin 正确的样子就是**不回显**，于是「没放行」被判成「竟被放行」。
3. **`jsonOk` 恒真**。`tasks.isJson !== false` 在 `tasks` 带 `err` 时
   `isJson` 是 `undefined`，`undefined !== false` 为真 ⇒
   「三条全失败」也判成 ✅ JSON 正确。已改成显式 `!tasks.err &&`。

三个都是同一族：**判据自己失效，却把失败印成通过。**
恒真的判据比没有判据更糟。

## §4.85.4 本轮新增/改动

- `scripts/verify-https-prod.mjs`：BUG-V10 四处（口令来源 + 失败路径还原 +
  退出码反映判定 + `tcp:0` 端口）
- `scripts/probe-https-device.mjs`（新）：**无需凭据**的 https 设备侧探测，
  只读、覆盖值必还原（含失败路径）
- `scripts/probe-prod-cors-origins.mjs`（新）：从真机 shell 枚举各 Origin
  的 CORS 回显，附判读

## §4.85.5 本轮遗留

- **BUG-V11 的修复需要生产 env 变更授权**，本机不做（共享部署）。
- `verify-https-prod.mjs` 的第 2、3 项（生产登录签发 token、带 token 读）
  **仍未验证**，需要 `POCKET_PROD_PASS`。在 CORS 修好之前它们**必然失败**，
  所以先修 CORS 再补这两项。
- ~40 个 `diag-*` / `verify-*` / `sweep-*` 脚本各自硬编码固定 CDP 端口
  （9402-9476）：`verify-https-prod.mjs` 已改 `tcp:0`，**其余未改**。
- `_login.yaml` 已成孤儿（含 `${POCKET_MASTER}`），要么删要么按新前置重写。
- `/api/marketplace` 带有效 token 仍 401（守卫与 `/api/tasks` 不同）。
- BUG-AX 设备侧负控、闪卡两入口渲染/点击、会议写入设备侧持久化、
  「tap 报 COMPLETED 但没反应」坐标对账：未做。
- `:param` 模板、gateway 六页：未做。
- Keystore 原生插件（§4.83.5 已确认全平台不可用）、同步编排层、改密入口、
  gateway 四页、BUG-AV、i18n ~800 条：待产品定范围。

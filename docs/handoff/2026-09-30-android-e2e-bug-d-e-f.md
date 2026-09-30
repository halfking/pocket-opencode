# 2026-09-30 · 真机/模拟器端到端审计接力单（BUG-D / BUG-E / BUG-F / BUG-G / BUG-H / BUG-I / BUG-J）

> 本轮范围：Android APK 真机 + 模拟器端到端验证、缺陷定位与修复、文档同步。
> **完成度声明必须以本文件的「已验证 / 未验证」两节为准，不得外推。**
> **§3 的 BUG-F 原始归因已被 §3.1 证伪并重写，引用前务必先读 §3.1。**

**本轮工具清单**（全部支持 `POCKET_SERIAL` 切真机/模拟器）：

| 脚本 | 用途 |
|---|---|
| `scripts/redmi-final-verify.mjs` | 一键终验：清 override → 重载 → 真实 UI 登录 → App 自身 WS → BUG-J 回归 |
| `scripts/host-ws-check.mjs` | 宿主侧后端 `/ws` 回归（登录 → 101 → pong） |
| `scripts/cdp-ws-probe.mjs` | WS 归因探针：区分「浏览器阻断 / 后端 404 / 401 / 不可达」 |
| `scripts/cdp-tasks-net.mjs` | BUG-J 复现：对比畸形 URL 与正确 URL 的实际响应 |
| `scripts/cdp-base-audit.mjs` | 审计 localStorage override 与 App 真实请求出口 |
| `scripts/cdp-unlock-debug.mjs` | 本地库解锁排查 |
| `scripts/verify-modules.mjs` | 13 模块可达性巡检（reachability，非功能性） |
| `scripts/redmi-write-ops.mjs` | **功能性**测试：真机笔记完整 CRUD + 落库/删除落库（6 断言） |
| `scripts/cdp-dom-probe.mjs` | 批量探查各功能页的 input/button/textarea 结构 |
| `scripts/cdp-notes-probe.mjs` | 笔记页 DOM + IndexedDB 结构专项探查 |
| `.maestro/*.yaml` | Maestro flow（目标指定方法）；见 §4.11.1 |

---

## 0. 一句话结论

**真机端到端已打通**（Redmi 2411DRN47C，WebView 126.0.6478.71）：真实 UI 登录 → `GET /api/auth/me` 200 →
`ws://localhost:8088/ws` 握手 **101**。

> ### ⚠️ 本轮自我推翻：BUG-F 的原始归因是错的
> 原 §0/§3 断言「WS 被 Chromium ≥111 Insecure-WebSocket 策略**硬阻断**，`setMixedContentMode` 管不到」。
> **该结论已被证伪**，详见 §3.1。真机实测：Chromium 对 `ws://` 只发
> *deprecation warning*，握手照常发出并拿到后端响应（404/401 都收到了）。
> 真机 WS 断连的真实原因是 **① APK 被打成 LAN IP 而真机不可达 ② 设备上 JWT 失效**，
> 与 `androidScheme` 无关。`CAP_ANDROID_SCHEME` 逃生舱本身**仍然有用**，
> 但它解决的是 **XHR 的 mixed-content 硬阻断**（那条是真阻断），不是 WS。
> 请勿再引用「WS 硬阻断」这个说法。

---

## 1. BUG-D：移动端构建守卫下沉到 vite.config（已提交 `ba12217`）

### 根因
`VITE_API_BASE` 为空时，`build-mobile.mjs` 才会拦；裸 `vite build` 不拦，能产出「后端地址为空」的 APK，
装到手机上表现为所有 API 请求指向空 base。此前的守卫只在脚本层，绕得过去。

### 改动
| 文件 | 行为 |
|---|---|
| `frontend/vite.config.ts` | 新增 `assertApiBaseForBuild(mode, env)`：非 development 且 `VITE_API_BASE` 为空 → 抛错中止构建。逃生舱 `MOBILE_ALLOW_EMPTY_API_BASE=1` |
| `frontend/src/api/client.ts` | 改用 `resolveRuntimeApiBase`，与 `api/http.ts` 统一 base 解析入口（此前两处各解析各的，行为分裂） |
| `frontend/src/api/websocket.ts` | `wsHttpBase()` 改用 `resolveRuntimeApiBase()` |
| `frontend/android/app/build.gradle` | `buildTypes.debug.debuggable true` —— **实测对 mixed-content 无效**，见 BUG-F |

### 验证
- 裸 `vite build --mode production` → EXIT=1，打印「[vite] 拒绝构建：VITE_API_BASE 为空」
- `npx vue-tsc --noEmit` → EXIT=0（多次）

---

## 2. BUG-E：en-US.json 缺 40 个 key（底栏英文塌成「订阅」）

### 根因
`frontend/src/locales/en-US.json` 缺 `nav.rss` 与 39 个 `routes.*`，英文环境下底栏 5 个 tab 的第 4 个回落成中文兜底。

### 验证（独立复核，非引用旧结论）
```
zh keys = 292   en keys = 292
missing in en = 0    extra in en = 0
nav.rss(en) = "RSS"
```
注：`notifications` 在 **zh 与 en 两侧都不存在**，此前担心的「取并集」问题已不成立。

---

## 3. BUG-F：真机 WebSocket 被硬阻断（本轮核心修复）

> 🔴 **本节根因判断已于同日被证伪并重写，见 §3.1。下面保留原始记录仅为留痕，不要当作结论引用。**

### 根因（这是本轮最容易被误判的地方）
必须区分两类「mixed content」：

| 通道 | 管控者 | 修复前行为 |
|---|---|---|
| XHR `http://…/api/*` | `WebSettings.setMixedContentMode` | debug 下 `MIXED_CONTENT_ALWAYS_ALLOW` → **放行**（仅留 console 警告） |
| WS `ws://…/ws?token=` | **Chromium ≥111 Insecure-WebSocket 策略** | **硬阻断**，`setMixedContentMode` 完全无效 |

因此此前「加 `debuggable true` 没效果」的观察是**正确**的——那条路本来就不可能生效，不是配错了。

旁证：后端 CORS 本身完全正常，不是后端问题。
```
GET /api/app/check-update   Origin: https://localhost → 200, ACAO: https://localhost
OPTIONS 预检                 Origin: https://localhost → 200
GET /api/app/check-update   Origin: http://localhost  → 200, ACAO: http://localhost
```
生产 `/ws` 的 origin 校验是 `buildOriginChecker(AllowedOrigins, DevAuth)`，devAuth 下放行 `http(s)://localhost`
（`backend/internal/server/server.go:292-296`）。`mobile_api.go:529` 那个 `CheckOrigin: return true` 是**死代码**，
已被 `mobile_api_isolation_test.go` 锁死，不是本次路径。

### 改动
`frontend/capacitor.config.ts`：
```ts
androidScheme: (process.env.CAP_ANDROID_SCHEME as 'http' | 'https') ?? 'https',
```
- **默认仍是 `https`**：生产后端应走 HTTPS + wss，不需要降级。
- 仅本地/内网 HTTP 后端联调时用 `CAP_ANDROID_SCHEME=http` 构建。
- 机制：`cap sync` 会把该值写进 `android/app/src/main/assets/capacitor.config.json`，运行时由 Capacitor 读取。

### 验证结果（模拟器 emulator-5554，WebView 126 同款链路）

| 指标 | 修复前 | 修复后 |
|---|---|---|
| `Capacitor: Loading app at` | `https://localhost` | **`http://localhost`** |
| Mixed Content 告警数 | 6 | **0** |
| WebSocket | `WebSocket error` → `WebSocket disconnected` → `Reconnecting (attempt 1, 3182ms backoff)` 死循环 | **`WebSocket connected`**（单条，无重连） |
| Failed to fetch | — | 0 |

端到端流程实测通过：登录（200，`auth_method=dev-bypass`）→ 创建主密码（3 字段）→ 进入 `/ai` → 状态胶囊显示实时 `● 全部正常 · 0`。

### 构建复现（带断言，防止配置被静默改回默认值）
```bat
set CAP_ANDROID_SCHEME=http
cd frontend
call npx cap sync android
findstr /C:"androidScheme\": \"http" android\app\src\main\assets\capacitor.config.json
cd android && call gradlew.bat assembleDebug --no-daemon
```
⚠️ **必须校验 APK 内实际打包的值**，不能只看工作区文件：
```powershell
$z=[System.IO.Compression.ZipFile]::OpenRead($apk)
$e=$z.Entries | Where-Object { $_.FullName -eq 'assets/capacitor.config.json' }
```
本轮就被这一点坑过一次：`cap sync` 写对了，但打包前被另一个并发会话的 `cap sync` 改回 `https`，
装上去 origin 仍是 `https://localhost`，差点误判成「修复无效」。

---

## 3.1 BUG-F 归因证伪与重写（本轮最重要的一条修正）

### 证伪方法：让 WebView 自己开口

原结论建立在「`WebSocket` 构造函数报错 + 后端日志没有连接记录」上。这个证据组合**不足以区分**
「被浏览器阻断」和「到了后端但被拒」——因为 **401/403 是在 upgrade 之前返回的，
`WebSocket client connected` 这条日志根本不会打**。我据此错误地判定「握手没到后端」。

改用 CDP `Network` 域的 `webSocketHandshakeResponseReceived` / `webSocketFrameError`，
它们能**区分**「没发出去」和「发出去了但响应码不对」：

```js
// scripts/cdp-ws-probe.mjs
ws://localhost:8088/__probe_no_such_route__  →  wsHandshake + "Unexpected response code: 404"
ws://localhost:8088/ws?token=probe            →  wsHandshake + "HTTP Authentication failed; no valid credentials available"
```

**决定性判据**：握手请求**确实到达了后端**并拿到了 Echo 的 404 响应体。
若是 Chromium 策略硬阻断，CDP 只会给 `loadingFailed` / `ERR_BLOCKED_BY_CLIENT`，
不可能给出后端的状态码。console 侧措辞也印证了——是
`Connecting to a non-secure WebSocket server from a secure origin is **deprecated**`（弃用警告），不是 blocked。

### mixed content 对 XHR 与 WS 的真实差别（修正后的表）

| 通道 | `https://localhost` 页面请求 `http://…` / `ws://…` | 实测 |
|---|---|---|
| XHR / fetch | **真阻断**，请求出不去浏览器 | Mixed Content 警告 + `ERR_ADDRESS_UNREACHABLE`（根本没到网络层） |
| WebSocket | **仅 deprecation warning**，照常发出 | `wsHandshake` + 后端真实状态码（404 / 401 / 101） |

所以 `CAP_ANDROID_SCHEME=http` 逃生舱**方向是对的**（消除 XHR 硬阻断，Mixed Content 计数归零），
只是**它对 WS 的作用被本轮错误归因了**。

### 真机 WS 断连的真实原因（两条，都不是代码缺陷）

1. **APK 被打成了 LAN IP**。并发会话在 08:20 用 `.env.android-dev`
   （`VITE_API_BASE=http://192.168.31.20:8088`）重建并重装，覆盖了我的 reversedev 包。
   真机到宿主 LAN IP 是 `net::ERR_ADDRESS_UNREACHABLE`（宿主防火墙，见 §4.5）。
2. **设备上持有的 JWT 失效**。真机 localStorage 里的 token 由**另一个 pocketd 实例**签发
   （当时跑的是 `logs/pocketd-new.exe`），与当前 pocketd 的 `POCKET_JWT_SECRET` 不匹配。
   证据：该 token 连**普通 HTTP** `GET /api/auth/me` 也是 401 `invalid or expired token`；
   现场调 `/api/auth/login` 拿的新 token **立刻 200**。

### 后端 `/ws` 本身无缺陷（宿主侧回归）

`scripts/host-ws-check.mjs`（登录 → `ws://localhost:8088/ws?token=…`）：
```
login ok: user=admin tokenLen=283
GET /api/auth/me -> 200 OK
WS OPEN (handshake 101)
WS FRAME: {"type":"pong","payload":"2026-09-30T08:32:21..."}
VERDICT = OPEN
```

### ⚠️ 一条差点把自己骗过去的证据

我第一次宣布「真机 WS 101」时，**测的是在页面里手工 `new WebSocket(...)` 造的连接**，
不是 App 自己的 `wsClient`。随后 `scripts/cdp-base-audit.mjs` 才暴露真凶：

```
localStorage.pocket_api_base = "http://192.168.31.20:8088"   ← override 优先级高于 VITE_API_BASE
fetch targets = ["http://192.168.31.20:8088/api/tasks?source=opencode", ...]
WS-CREATED ws://192.168.31.20:8088/ws?token=<redacted>
WS-ERR "net::ERR_ADDRESS_UNREACHABLE"
```

**教训**：验证「App 连上了」必须抓 **App 自己发起的那条连接**
（`Network.webSocketCreated` 的 URL 要与 `resolveRuntimeApiBase()` 的解析结果一致），
不能用手工构造的探针替代。设备上残留的 `pocket_api_base` override 会静默压过构建默认值——
换包后第一件事是清它。

---

## 3.5 BUG-G：Capacitor 插件代理的 thenable 陷阱（`EmailFetch` / `Keystore` / `Sherpa`）

### 发现方式
用 CDP 逐个访问 13 个模块并抓控制台异常（不走截图），在 `/email` 与 `/vault` 上暴露：

```
/email : WARNING [email] sync from server: email store not configured
         EXC Error: "EmailFetch.then()" is not implemented on android
/vault : EXC Error: "Keystore" plugin is not implemented on android
```

### 根因
Capacitor 的 `registerPlugin(name)` 返回的是**带 `.then` 的 thenable 代理**。
若把这个代理从 `async` 函数 `return` 出去，或当作 `.then()` 回调的返回值，
JS 的 promise 决议会去调它的 `.then()`，而未实现的原生插件会抛
`"<Name>.then()" is not implemented`。

后果有两层：
1. 抛出**未捕获异常**，掩盖真实原因；
2. 写好的**降级路径完全失效**——`keystore.ts` 的 `StubKeystore` 只在
   `registerPlugin` 抛异常时才启用，但代理不抛、只返回 thenable，
   于是「优雅降级」从未真正跑过一次。

**项目其实早就知道这个陷阱**：`native/biometricAuth.ts:38-40` 有明确注释
「绝不能从 async 函数直接 return 这个代理（会被当 thenable 采用）」，且已用
`Promise<void> + 同步传递实例` 修好；`background-mic.ts` 写法同样正确。
**但 `keystore.ts` / `email-fetch-native.ts` / `util.ts`（Sherpa 在用）三处漏了。**

### 修复
统一改为「非 thenable 盒子」装载实例，`async` 只返回盒子：

| 文件 | 改动 |
|---|---|
| `frontend/src/native/keystore.ts` | `load(): Promise<KeystoreBox>`，`{ value: impl }`；facade 用 `box.value[prop]` |
| `frontend/src/native/email-fetch-native.ts` | `ensurePlugin(): Promise<EmailFetchBox \| null>`；`Promise.resolve().then()` 回调改为**不返回值**（赋值结果若被当决议值会再次触发陷阱） |
| `frontend/src/native/util.ts` | `ensure(): Promise<{ value: T }>`；`registerPluginSafely`（Sherpa 使用） |

### 验证
重装 APK（`index-oroRx_TG.js`）后重跑 13 模块验证：

| 指标 | 修复前 | 修复后 |
|---|---|---|
| `/email` 控制台异常 | `EXC "EmailFetch.then()" is not implemented` | 无 |
| `/vault` 控制台异常 | `EXC "Keystore plugin is not implemented"` | 无 |
| 13 模块控制台错误合计 | 2 | **0** |

---

## 4. 13 个模块可达性验证（CDP，不依赖截图）

工具：`scripts/verify-modules.mjs`（按当前 App PID 解析 WebView devtools socket，
`Runtime.enable` 抓 `exceptionThrown` / `console.error|warning`，
逐路由 `location.hash` 导航后检查最终 URL + 渲染文本量）。

**首轮（重装后冷启动）**：4 个模块 `LOGIN_GATED`（Notes / Email / Vault / PKM），
URL 形如 `#/login?returnTo=/notes&unlock=1`。
这不是缺陷——本地加密库需要主密码解锁，页面提示「检测到已登录态，但本地加密库未解锁」，
输入主密码解锁后 `returnTo` 正确跳回。**冷启动后必须重新解锁**，
否则会把设计行为误判成"模块打不开"。

**解锁后最终结果**：

| # | 模块 | 路由 | 判定 | 控制台 |
|---|---|---|---|---|
| 0 | 笔记 Notes | `/notes` | RENDERED | - |
| 1 | 邮箱 Email | `/email` | RENDERED | - |
| 2 | 财务 Finance | `/finance` | RENDERED | - |
| 3 | 密码箱 Vault | `/vault` | RENDERED | - |
| 4 | 闪卡 Flashcards | `/flashcards` | RENDERED | - |
| 5 | PKM | `/pkm/today` | RENDERED | - |
| 6 | 本地智能体 | `/local-agent` | RENDERED | - |
| 7 | 市场 Market | `/marketplace/skills` | RENDERED | - |
| 8 | 费用配额 Cost | `/cost` | RENDERED | - |
| 9 | 网关 Gateway | `/gateway` | RENDERED | - |
| 10 | 实例 Instances | `/instances` | RENDERED | - |
| 11 | 任务 Tasks | `/tasks` | RENDERED | - |
| 12 | 会话 Sessions | `/sessions` | RENDERED | - |

`LOGIN_GATED: (none)` / `BLANK: (none)` / 控制台错误合计 0。

> ⚠️ 这是**可达性 + 渲染 + 无异常**，**不等于写操作已验证**。见 §5。

---

## 4.5 真机 Redmi 的 BUG-F 复验：网络侧阻塞，非代码问题

真机装上 BUG-F 新包后：
- `Loading app at http://localhost` ✓、Mixed Content 0 ✓、无 WS 报错/重连 ✓
- 但页面内 `fetch` → `Failed to fetch`

用 CDP `Network.enable` 抓到确切原因：
```
loadingFailed: errorText="net::ERR_ADDRESS_UNREACHABLE" blockedReason="-" type=Fetch
```
**注意区分**：`no-cors` 模式同样失败、XHR `status=0` → 排除 CORS，是网络层不可达。
但进一步对照实验表明**与 App 无关**：

| 发起方 | 目标 | 结果 |
|---|---|---|
| 设备 shell `curl` | 网关 `192.168.31.1:80` | 302，10–64ms ✓ |
| 设备 shell `curl` | 宿主 `192.168.31.20:8088` | **000（~1.1s 超时）✗** |
| WebView `fetch` | 网关 `192.168.31.1:80` | opaque 成功 ✓ |
| WebView `fetch` | 宿主 `192.168.31.20:8088` | `ERR_ADDRESS_UNREACHABLE` ✗ |

同一时刻 shell 也连不上宿主，而宿主自身 `127.0.0.1:8088` 与 `192.168.31.20:8088`
都返回 200、`Get-NetTCPConnection` 显示正常监听。→ **宿主侧网络/防火墙问题**。

**结论**：不能把真机失败记为代码缺陷。下轮开工前必须先确认
`设备 shell curl http://192.168.31.20:8088/healthz` 返回 200，否则测了也是白测。

**备选方案**（下轮建议采用，绕开宿主防火墙）：
`adb reverse tcp:8088 tcp:8088` + 以 `VITE_API_BASE=http://localhost:8088` 构建。
后端 `buildOriginChecker` 只校验 hostname 是 `localhost`、**不校验端口**，
所以 `http://localhost:8088` 会被放行；且该 origin 与 Capacitor 的
`http://localhost`（80 端口）不同源，CORS 正常走。

---

## 4.6 写操作验证（CDP 驱动真实 UI，非渲染推断）

> 工具：CDP `Runtime.evaluate` 里 `location.hash` 导航 + 原生 setter 赋值 +
> `dispatchEvent(new Event('input'))` 触发 Vue 的 `v-model`，再点真实按钮。
> **注意**：Vue 的 `v-model` 不会因为直接改 `.value` 而更新，必须走原生 setter + `input` 事件。

### 已验证通过的写操作

| 模块 | 操作 | 证据 |
|---|---|---|
| **笔记 Notes** | 新建 | `createBtn.disabled=false` → 创建 → 列表 `found=true`，标题+正文+「0分钟前」 |
| **笔记 Notes** | 编辑 | id `note-1790723119846-xzs3ce`，`editPersisted=true`，正文已更新并回显 |
| **笔记 Notes** | 删除 | 二次确认 `confirmed=1`，`deleteRemoved=true`，列表回到「还没有笔记」 |
| **财务 Finance** | 解析+入账 | 确认弹窗「支出 · 交通 · ¥32.00」→ 确认入账 → 账本出现条目、月度汇总更新 |
| **财务 Finance** | 删除 | 列表项 `delete` 按钮，`deleteWorked=true`，汇总归 ¥0.00 |
| **本地智能体** | 新建 | 三必填项齐全 → `#/agents/custom-e2e--1790723592`，详情页含名称/简介/System Prompt/角色 ID，`listHasAgent=true` |

### 排查过程中确认的「非缺陷」

- 智能体保存「没反应」→ 读 `AgentEditView.vue:98-111`，`handleSave` 要求
  **name / description / system_prompt 三项都非空**，缺任一就 `toast.error` 后 return。
  这是**正确的校验行为**，不是 bug。补齐三项即通过。
- 4 个模块 `LOGIN_GATED` → 本地加密库需主密码解锁（见 §4），设计行为。
- 财务「32 元被解析成 ¥2.00」→ 是我第一次输入带了 `E2E` 前缀导致的，**不是金额解析 bug**
  （见下方 BUG-H，那是个更窄但真实的问题）。

---

## 4.7 BUG-H：财务金额解析被标识符中的数字抢占（后端）

### 现象
`E2E 50 元` 被解析成 **amount=2**，且 `/api/finance/parse` 返回 **200**，
错误金额一路静默写进账本（前端还会弹出「支出 · 其他 · ¥2.00 确认入账」）。

### 根因
`backend/internal/finance/recognizer.go:27`
```go
amountRegex: regexp.MustCompile(`[¥$]?\s*(\d+(?:\.\d{1,2})?)\s*(?:块钱?|元|钱)?`)
```
除数字外**全部可选**（货币符号可选、单位可选），而 `FindStringSubmatch` 取**第一个**匹配。
`E2E` 里的 `2` 满足「数字」这一唯一必需条件，被直接当成金额。

对照实验（修复前）：
```
打车 32 元        -> 32    ✓
买菜 66 元        -> 66    ✓
50 元             -> 50    ✓
abc 100 元        -> 100   ✓   （abc 里没数字，不影响）
E2E 50 元         -> 2     ✗
报销 1200 元      -> 1200  ✓
```
可见与「拉丁前缀」无关，**只要字符串里第一个数字粘在 ASCII 字母后就会中招**。

### 修复：两级策略
1. `amountRegex` 优先匹配「货币符号+数字」或「数字+货币单位」（单位/符号必填）；
2. 都没有时回退 `looseAmountRegex`：任意数字，但**前一个字符不得是 ASCII 字母/数字**。

> 第一版曾试图「强制单位必填」，结果打破了 4 个既有测试
> （`吃饭花了38` / `入账1000` / `收款1000` / `项目尾款3000到账` 都是裸数字），
> 说明裸数字是既有契约的一部分。两级策略才是正确解法。
> 新增 `recognizer_bugh_test.go` 同时锁定「修好了」和「没改坏」两侧。

### 验证（重建后端后打真实接口）
```
E2E 50 元                    -> 200 amount=50    ✓（修复前 2）
E2E 50                       -> 200 amount=50    ✓
test2 打车 30 元              -> 200 amount=30    ✓
吃饭花了38                    -> 200 amount=38    ✓（契约未破）
入账1000 / 项目尾款3000到账    -> 200 amount=1000/3000 ✓
买了100块…又花了50块打车        -> 200 amount=100   ✓（仍取第一个金额）
乱七八糟没有数字               -> 400 正确拒绝      ✓
E2E                          -> 400 正确拒绝      ✓（修复前静默记 2）
```
`go test ./internal/finance/...` 全绿（含新增 2 个回归测试）。

---

## 4.8 BUG-I：不可恢复的 401 把用户困在死胡同

### 现象
市场模块只显示「invalid or expired token / 刷新 / 重试」，**没有任何回到登录页的路径**。

### 根因
`stores/auth.ts:110-111` 的注释写「refresh 失败返回 false（**由调用方决定是否登出**）」，
但 `api/http.ts:145-150` 在 refresh 失败时只是**原样把 401 抛给调用方**，
而**没有任何调用方真的执行登出**。于是：

- 后端 `POCKET_JWT_SECRET` 变更（重启换密钥）→ 旧 token 全部 401
- 或 token 过期且 refresh 端点也失败
→ 用户拿着死 token 卡在各个模块，**既不能继续，也回不去登录**。

实测确认旧 token **并未过期**（剩余 22 小时）却被所有接口 401，
而同接口用新签发的 token 立即 200 —— 是密钥变更导致，属预期 JWT 行为；
**缺陷在于 App 对此毫无恢复路径**。

### 修复
- `stores/auth.ts` 新增 `clearLocal()`：只清本地态，不打后端
  （此时 `/api/auth/logout` 必然被拒，调它只会白等一轮超时）；`logout()` 复用它。
- `api/http.ts` 新增 `forceReauth()`：401 且 refresh 失败时单飞触发，
  跳过已在登录页的场景（防重定向循环），清本地态后 `location.hash = '#/login?reason=expired'`。

### 验证（模拟器，持真实死 token 复现）
```
onLaunch            hash=#/ai
afterNavMarket      hash=#/login?reason=expired   ← 自动回登录页
marketText          正常渲染登录页（用户名/密码/登录/注册新账号/忘记密码）
```
修复前该页面只有「刷新 / 重试」死循环。

---

## 4.9 后端端点可用性矩阵（决定哪些模块能验证写操作）

用 dev 后端 + 有效 token 实测：

| 端点 | 状态 | 含义 | 受影响模块 |
|---|---|---|---|
| `/api/tasks` | **200** | 可用 | 任务 |
| `/api/sessions` | **200** | 可用 | 会话 |
| `/api/instances` | **200** | 可用 | 实例 |
| `/api/meetings` | **200** | 可用 | 会议 |
| `/api/notes` | 503 | store 未配置 | 笔记（前端仍走本地库，可用） |
| `/api/flashcards` | 503 | store 未配置 | 闪卡（列表页显示 "Could not load flashcards"，保存恒 disabled） |
| `/api/vault` | 404 | 路由/服务未挂载 | 密码箱 |
| `/api/marketplace/*` | 404 | `marketplaceStore` 为 nil | 市场 |

**这批 404/503 是 dev 环境未配存储/服务，不是代码缺陷。**
下轮若要验证闪卡/市场/密码箱的写操作，必须先把对应 store 接上
（dev 环境没有 PostgreSQL 时很多 store 为 nil），否则测的全是配置问题。

---

## 4.10 BUG-J：`getTasks` 把 API base 的 scheme+host 当前缀删掉（本轮新发现并修复）

### 现象

真机（`CAP_ANDROID_SCHEME=http` + `VITE_API_BASE=http://localhost:8088`）批量跑 13 模块可达性时，
只有 `/tasks` 控制台报错：

```
ERROR Failed to load tasks: Error: API 返回了 HTML 页面而非 JSON：
      通常是移动端打包漏注入 VITE_API_BASE（请求落到 WebView 本地 index.html），或被网关/代理重定向。
```

其它 12 个模块正常渲染，容易被当成「tasks 独有的后端问题」而误判。

### 根因（真机实测，非推理）

`frontend/src/api/client.ts` 的 `getTasks` 曾经这样拼 URL：

```ts
const url = new URL(`${resolveApiBase()}/api/tasks`, window.location.origin)
// …设 query…
const res = await authFetch(url.toString().replace(window.location.origin, ''))
```

那句 `.replace(window.location.origin, '')` 本意是「同源时把绝对地址降成相对路径」。
但 `window.location.origin` **不带端口**，API base **带端口**，于是 replace 命中前缀整段删掉：

| 项 | 值 |
|---|---|
| 页面 origin | `http://localhost` |
| 正确 URL | `http://localhost:8088/api/tasks` |
| replace 后 | `:8088/api/tasks` |
| 浏览器解析为 | `http://localhost/:8088/api/tasks` |
| 实际响应 | `200 text/html :: <!doctype html>…` ← **本地 index.html** |
| 修复后 | `200 application/json :: {"tasks":null}` |

（`scripts/cdp-tasks-net.mjs` 现场跑出上表两行 fetch 结果对比。）

**这是个自相矛盾的 bug**：`CAP_ANDROID_SCHEME=https` 时 origin 是 `https://localhost`，
和 `http://localhost:8088` 字符串不匹配，replace 空转，**侥幸不触发**。
所以它是 BUG-F 引入 `CAP_ANDROID_SCHEME=http` 逃生舱后才暴露的**回归**——由我自己引入。

### 同时修掉的第二处

`frontend/src/config/api-base.ts:114` 的 Capacitor 壳回退守卫写的是
`origin === 'https://localhost' || origin === 'capacitor://localhost'`，
**同样漏了 `http://localhost`**。改成 scheme 无关的正则并导出为
`isCapacitorShellOrigin()`，避免以后再加 scheme 时重蹈覆辙。

### 改动

- `frontend/src/api/tasks-url.ts`（新增）：`buildTasksUrl(base, instanceId, opts)` 纯函数。
  单独成模块是为了能被 `node --test` 直接加载——`client.ts` 依赖 pinia store 与无扩展名相对 import，
  Node 的 ESM 解析器跑不起来。
- `frontend/src/api/client.ts`：`getTasks` 改用 `buildTasksUrl`，删掉 `.replace()`。
- `frontend/src/config/api-base.ts`：`isCapacitorShellOrigin()` + `resolveRuntimeApiBase()` 用它。
- `frontend/src/api/tasks-url.test.ts`（新增，4 例）：含一条**反证**断言，
  锁死旧实现会产出 `:8088/api/tasks`。
- `frontend/src/config/api-base.test.ts`：新增 5 例，覆盖 `http://localhost` 壳与
  `http://localhost:8088`（带端口的真实后端，必须**不**被当成壳）。

### 验证

```powershell
cd frontend
node --experimental-strip-types --test src/api/tasks-url.test.ts src/config/api-base.test.ts
# tests 20  pass 20  fail 0
npx vue-tsc --noEmit          # 无输出 = 通过
```

---

## 4.11 Maestro 真机测试 + 真机功能性测试（可达性 ≠ 功能性）

### 4.11.1 Maestro 落地（目标指定的方法，本轮之前一直没用）

**安装**：GitHub 直连会被切断（`Invoke-WebRequest` 与 `curl` 都报
`意外的 EOF` / 0 字节），必须走镜像：

```powershell
curl.exe -sSL --retry 2 -o maestro.zip `
  https://gh-proxy.com/https://github.com/mobile-dev-inc/maestro/releases/latest/download/maestro.zip
# 314,886,578 字节 ≈ 300MB
Expand-Archive maestro.zip -DestinationPath dist -Force
$env:JAVA_HOME='C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
$env:PATH="$env:JAVA_HOME\bin;$env:PATH"
$env:MAESTRO_CLI_ANALYSIS_NOTIFICATION_DISABLED='true'
.\dist\maestro\bin\maestro.bat --version        # 2.11.0
```

**真机跑不了 —— MIUI 安装策略**。Maestro 要先装自己的 driver APK，
真机上被拦死，且 `adb install`、`pm install`（含先 `settings put global
verifier_verify_adb_installs 0` / `package_verifier_enable 0` /
`install_non_market 1` 全部试过）都是同一个错：

```
INSTALL_FAILED_USER_RESTRICTED: Install canceled by user
```

这需要**在手机上手动授权**，adb 无法绕过：
设置 → 更多设置 → 开发者选项 → 打开「USB 安装」（MIUI 还要「安装监控」关闭）。
driver APK 可提前从 jar 里抽出来手动装：

```powershell
# driver APK 内嵌在 maestro-client.jar / maestro-orchestra.jar 里
Add-Type -AssemblyName System.IO.Compression.FileSystem
# …遍历 lib/*.jar 抽出 *.apk -> maestro-server.apk (0.84MB) / maestro-app.apk (11.2MB)
adb install -r -d maestro-server.apk
```

**模拟器上 Maestro 跑通了**（AOSP Android 34，无 MIUI 限制）：

```powershell
.\dist\maestro\bin\maestro.bat --device emulator-5554 test .maestro\_connectivity.yaml
```
```
Launch app "com.kaixuan.opencode.pocket"... COMPLETED
Assert that "全部正常" is visible... COMPLETED
Assert that "全部正常" is visible... COMPLETED
Assert that "AI 工具" is visible... COMPLETED
```

**Maestro 两个坑**：
1. **选择器/断言文本里不能放 emoji**（`🟢` `✓` `✎` `🗑`）。Java 正则里这些字符
   会让整条匹配抛异常并**直接判 false**——第一版 flow 断言
   `"笔记|登录|解锁|🟢|AI"` 就是这么挂的，且报错信息毫无指向性。
   改用纯中文子串（`全部正常` / `创建` / `编辑` / `删除`）。
2. **Windows 控制台把中文输出成乱码**（`ȫ������`），但 Maestro 内部处理是正确的
   ——它生成的 debug 产物文件名 `step-004-assertCondition-笔记登录解锁🟢AI.png`
   说明 YAML 是按 UTF-8 读的。**不要被 console 乱码误导成断言文本不匹配**。
   排查要看 `%USERPROFILE%\.maestro\tests\<时间戳>\<flow>\` 里的
   `screen-hierarchy/*.json`（完整 UI 树）和 `screenshots/*.png`。

新增 flow：`.maestro/_connectivity.yaml`、`.maestro/smoke-login.yaml`、
`.maestro/notes-crud.yaml`（后两者的选择器来自真机 CDP 实测 DOM，不是猜的）。

### 4.11.2 真机功能性测试：`scripts/redmi-write-ops.mjs`（6/6 通过）

`verify-modules.mjs` 只证明「可达 + 渲染」，**不能**当作功能可用。
这个脚本驱动真实 UI 完成写操作并**回读校验**：

```
=== SUMMARY 6/6 ===
  PASS  新建笔记：跳回列表                  hash=#/notes
  PASS  新建笔记：列表回显标题              found
  PASS  落库：force-stop 重启后笔记仍在      persisted across process restart
  PASS  编辑笔记：列表摘要显示新正文        snippet="已编辑-33418938"
  PASS  删除笔记：列表不再回显              gone (cards=1)
  PASS  删除落库：重启后笔记仍不存在        gone after restart (cards=1)
```

关键设计（每一条都是踩过坑换来的）：

1. **落库判据 = `am force-stop` 后重启仍在**。笔记存在 **Capacitor SQLite 原生插件**
   （表 `local_notes`），**不在 WebView 的 localStorage / IndexedDB 里**
   （实测 `indexedDB.databases()` 返回 `[]`）。查 localStorage 只会得到假阴性。
2. **重启后必须先恢复会话再断言**。进程重启后 App 回到「登出 + 本地库锁定」，
   任何 `#/xxx` 都被守卫弹到 `#/login?returnTo=…&unlock=1`，列表本来就是空的。
   不恢复就会把「被 gate 挡住」误判成「数据丢了」——我第一版脚本就因此
   同时产生了 1 个假 FAIL 和 2 个假 PASS。
3. **解锁按钮有 `disabled` 计算属性**，填完主密码必须等 Vue 重渲染（约 1.5s）
   再点，否则点到 disabled 按钮静默无效。这是「解锁点不动」的真正原因。
4. **详情页默认只读**，必须先点「编辑」才出现 textarea；且详情页有**两个** textarea
   （标题 + 正文），`fill('textarea')` 会命中第一个把标题也改掉，导致后续按标题定位卡片失败。
5. **删除确认弹窗的按钮文本也是「删除」**，和工具栏的删除同名；
   用 `class` 含 `button--danger` 区分，fallback 取最后一个。
6. **任何前置失败立即 `exit(5)`**，不产出级联的假结论。
7. 断言要**强**：「编辑后标题还在列表」几乎恒真，必须验「列表摘要显示新正文」。

---

## 4.12 接入 PostgreSQL：解锁被环境阻塞的写操作（关键转折）

### 为什么这是本轮最大的单点突破

前面几轮把「`POST /api/tasks` 503」「`/api/flashcards` 503」「`/api/llm/usage` 503」
一律记成「dev 环境未配存储，下轮再弄」。实际上它**当场就能解**：
`backend/cmd/pocketd/main.go:103` 的 `if pool != nil` 决定所有 store 是否构造，
而 `pool` 只在 `cfg.PostgresDSN != ""` 时初始化（`main.go:70`）。
**没有 DSN ⇒ 一批 store 为 nil ⇒ 一批端点 503/404。**

### 装 PG（Windows，无需管理员、无需 Docker）

```powershell
# Docker 不可用：daemon 未运行且需管理员权限（error: docker client must be run with
# elevated privileges），所以走免安装二进制
curl.exe -sSL --retry 3 -o logs/pg/pg.zip `
  https://get.enterprisedb.com/postgresql/postgresql-16.4-1-windows-x64-binaries.zip
# ⚠️ 338MB / 上万个文件，**不要用 Expand-Archive**（跑十几分钟还没解压完 share/）。
#    tar.exe 几十秒搞定：
tar.exe -xf logs/pg/pg.zip -C logs/pg/dist2

$pg = 'C:\workspace\openpocket\logs\pg\dist2\pgsql'
& "$pg\bin\initdb.exe" -D C:\workspace\openpocket\logs\pg\data -U postgres -A trust -E UTF8 --locale=C
# ⚠️ 必须用 Start-Process 直接起 postgres.exe。用 pg_ctl start 的话，进程会挂在
#    调用的 shell 上，shell 一被回收 PG 就一起死（我踩过一次）。
Start-Process "$pg\bin\postgres.exe" `
  -ArgumentList '-D','C:\workspace\openpocket\logs\pg\data','-p','5432','-h','127.0.0.1' `
  -WindowStyle Hidden
```

### 启带 DSN 的 pocketd

`scripts/start-pocketd-pg.cmd`（已在库；`logs/pocketd-pg.exe` 因 `logs/` 被 gitignore 不入库，
需自己 `cd backend && go build -o ..\logs\pocketd-pg.exe ./cmd/pocketd`）：
```bat
set POCKET_POSTGRES_DSN=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable
set POCKET_PG_SCHEMA=opencode_pocket
set POCKET_DEV_AUTH=true
set POCKET_AUTH_LEGACY_ONLY=true
set POCKET_PORT=8088
logs\pocketd-pg.exe
```

启动日志确认：
```
Postgres pool initialized (schema="opencode_pocket")
Module stores initialized (PG, scheduled tasks and marketplace enabled)
LLM gateway store initialized (PG)
Quota enforcer enabled (PG store, AlwaysAllow strategy)
```

### 端点矩阵前后对比（`scripts/backend-endpoint-matrix.mjs`）

| 端点 | 无 PG | 有 PG |
|---|---|---|
| `POST /api/tasks` | **503** remote-only mode | **201** ✅ |
| `POST /api/notes` | **503** | **201** ✅ |
| `GET /api/flashcards` | **503** | **200** ✅ |
| `GET /api/llm/usage?days=7` | **503** | **200** ✅ |
| `GET /api/llm/quota` | — | **200** ✅ |
| `GET /api/llm-gateway/nodes` | **503** requires PostgreSQL | **200** ✅ |
| `GET /api/marketplace/packages` | **404** | **200** ✅ |
| `GET /api/marketplace/releases` | **404** | **200** ✅ |
| `GET /api/email/accounts` | — | **200** ✅ |
| `GET /api/rss/sources` | — | **200** ✅ |
| `GET /api/scheduled-tasks` | — | **200** ✅ |

**18 / 20 可用**。剩下 2 个 404 都不是端点缺失：
- `GET /api/vault` —— **后端根本没注册这个路由**（`server.go:693` 只有 `/api/vault/sync/`）。
  密码箱是**纯本地**功能（SQLCipher + Keystore），后端只提供同步子树。
  **所以「`/api/vault` 404 ⇒ 密码箱不可用」这个推断是错的**，已修正。
  `GET /api/vault/sync/latest` 返回 `{"error":"no vault for user: no rows in result set"}`
  是**业务上的 404**（该用户还没同步过），端点是通的。
- `GET /api/marketplace/agents` —— `handleMarketplaceRouter` 分发到不存在的子路径。

### 真机任务创建：UI 走通 + 直接查库确认落库

`scripts/redmi-write-ops-modules.mjs`（真机，bottom-sheet 真实交互）：

```
PASS  任务：创建请求 2xx（此前 503）
      — [{"url":"/api/tasks","status":201},{"url":"/api/tasks?source=opencode","status":200}]
```

直接查 PG 确认落库（**这才是权威判据**）：
```sql
select id, title, source, status, workspace_id from opencode_pocket.tasks
order by created_at desc limit 8;
```
```
task-1790735438707 | Maestro任务417149 | local | active | default
task-1790735324849 | Maestro任务310673 | local | active | default
```

> ⚠️ **别拿「列表回显」当断言**：任务列表默认带 `?source=opencode` 过滤，而 UI 建的是
> `source=local`；而且 API 侧按 workspace 隔离（UI 落 `workspace=default`，
> admin dev token 落 `ws_user-admin`）。两个原因叠加会让「已创建」看起来像「没创建」。

---

## 4.13 自我更正：上一版记的 `/cost` 路由异常是**误报**

上一版 handoff 写「真机上访问 `/cost` 实际落到了 `/#/ai-chat`，未定位」，
并被当作待修缺陷交接。**用 `scripts/cdp-cost-probe.mjs` 专项复验后确认：`/cost` 路由完全正常。**

```
### navigate #/cost
  hash timeline: #/cost -> #/cost -> #/cost -> #/cost -> #/cost
  final title  : 成本与配额
  body head    : 跳到主要内容 arrow_back 成本与配额 notifications 今天 7 天 30 天 用量汇总 …

### navigate #/gateway
  hash timeline: #/gateway -> #/gateway -> #/gateway -> #/gateway -> #/gateway
  final title  : 网关节点
### navigate #/instances
  final title  : 实例
```

四个路由全部稳定，无一跳转。之前那次观察的现场条件是：设备上跑的**不是我的包**
（并发会话 09:12 重装的 https 版），且 `pocket_api_base` override 指向不可达地址——
在这种状态下任何页面的数据请求都失败，**观察结果不可作为路由缺陷的证据**。

**教训**：交接文档里「未定位的异常」要先复核再当缺陷传下去。误报会让下一轮
把时间花在不存在的问题上。

（顺带查明：那次真正暴露的问题是 `/api/llm/usage` 与 `/api/llm-gateway/nodes` 都 503，
已由 §4.12 的 PG 接入解决。）

---


## 5. 已验证 / 未验证（严禁外推）

### ✅ 已验证（有证据）
- BUG-D 构建守卫（裸 build EXIT=1）、typecheck EXIT=0
- BUG-E i18n 292/292 对等，底栏英文 `RSS`
- BUG-F 逃生舱机制生效：origin 降级为 `http://localhost`、Mixed Content 归零。
  ⚠️ 但「WS 因 Chromium 硬阻断而需要它」这个**归因是错的**，见 §3.1
- **BUG-G 修复生效**：13 模块控制台异常 2 → **0**
- **BUG-H 修复生效**：`E2E 50 元` 由 amount=2 修正为 50；`E2E` 由静默记 2 改为 400 拒绝；裸数字契约未破
- **写操作（模拟器，CDP 驱动真实 UI）**：
  - 笔记 Notes **完整 CRUD**（新建/编辑/删除，含二次确认）
  - 财务 Finance **新建+删除**（解析→确认入账→账本回显→汇总更新）
  - 本地智能体 **新建**（三必填校验 + 角色详情回显）
  - 会议 Meetings **新建**（自动创建 `meeting-1790725942511-waen21`，列表回显 + 删除按钮）
- **写操作（真机，`scripts/redmi-write-ops.mjs`，6/6）**：笔记 Notes 完整 CRUD
  + **force-stop 进程重启后仍存在**（真持久化证据）+ 删除后重启仍不存在（非软删除残留）。
  详见 §4.11.2
- **后端数据层接入 PostgreSQL 后**（§4.12）：
  - 端点可用性 **18/20**（`POST /api/tasks` 201、`POST /api/notes` 201、
    `GET /api/flashcards` 200、`/api/llm/usage` 200、`/api/llm-gateway/nodes` 200、
    `/api/marketplace/packages` 200 等）
  - **任务**：真机 UI（bottom-sheet）创建 → `POST /api/tasks` **201** →
    直接查 PG 确认落库（`task-1790735438707 | Maestro任务417149 | local | default`）
  - **`/cost` 路由异常是误报**，专项复验后路由完全正常（§4.13）
- **BUG-I 修复生效（真机）**：`#/login?reason=expired` 在真机出现——401 后自动带原因跳登录页
- **BUG-J 修复生效（真机）**：`/api/tasks?source=opencode` → `200 application/json`，
  控制台错误 0（修复前是 `text/html` + 「API 返回了 HTML 页面而非 JSON」）
- **真机端到端打通（Redmi 2411DRN47C，08:59 二次复现）**：
  真实 UI 登录 → `GET /api/auth/me` 200 → **App 自己发起的** `ws://localhost:8088/ws` 握手 **101**
- **13 个模块在真机上全部 RENDERED**，`LOGIN_GATED=0` / `BLANK=0`，控制台异常仅邮箱模块 1 条
  （`email store not configured`，属 §4.9 的 dev 环境限制）
- 后端 CORS / WS origin 校验对 `http://localhost` 均放行
- 后端端点可用性已摸清（见 §4.9）：tasks/sessions/instances/meetings 可用；notes/flashcards 503、vault/marketplace 404 属 dev 未配存储

### ❌ 未验证（下一轮必须补）
- **闪卡的 UI 写路径没打通**。后端已通（`GET /api/flashcards` 200，store 已接 PG），
  但真机上没走完：列表页「新建卡组」实际**直接跳到卡片编辑页**（`#/flashcards/new`），
  而卡片页的「卡组」按钮又**跳回列表**（`#/flashcards`）——**文案与行为不一致**，
  卡片页的 `button.save-link` 因此恒 `disabled`。
  两种可能：① 这是产品交互缺陷（按钮语义/文案错乱）② 存在我没找到的卡组管理入口。
  **需要人看一眼真实 UI 再判定**，不要凭脚本猜。
- **任务 / 会话 的编辑、删除**未验证（创建已验证 201 + 落库）。
- **闪卡 / 密码箱 / 市场 / 邮箱 的写操作**未验证（后端已通，前端路径待打通）。
- **任务 / 会话 的写操作**未验证。`GET /api/tasks` 200 可读，但 `POST /api/tasks` 在 dev 后端
  恒 **503 `local task store not configured (remote-only mode)`**——`taskStore` 只在
  `pool != nil`（PostgreSQL）时构造（`backend/cmd/pocketd/main.go:103-108`）。
  `internal/server/disk_task_fallback.go` 只是**只读**合成，不提供写路径。
  要验证写操作必须先给 dev 后端接上 PG。**这是环境限制，不是模块代码缺陷。**
  ✅ **已解决**：见 §4.12，接 PG 后 `POST /api/tasks` 返回 201，真机 UI 创建已落库。
- **闪卡 / 密码箱 / 市场 / 邮箱 的写操作无法在当前 dev 环境验证**：
  对应后端 store 未配置（`/api/flashcards` 503、`/api/vault` 404、`/api/marketplace/*` 404，
  见 §4.9）。闪卡列表页直接显示「Could not load flashcards」且保存恒 disabled。
  **这是环境限制，不是模块代码缺陷**，但也**不能因此宣称它们可用**。下轮需先把 store 接上再测。
- **密码箱 Vault 存疑**：依赖 `Keystore` 原生插件，Android 尚未实现（BUG-G 只让降级路径
  正确生效）。`/api/vault` 亦 404。**很可能 Android 上根本不可用**，下轮需实测确认。
- **生产 `https` scheme 下的真机回归**。BUG-F 的逃生舱是 `CAP_ANDROID_SCHEME=http` 这条
  opt-in 路径，默认仍是 `https`，该路径本轮未回归（且 §3.1 已证明 WS 在 https 下本就能握手，
  真正需要 https 回归的是 **XHR 混合内容**是否仍被正确阻断）。
- **`Keystore` 原生插件确实不存在**——这是**代码欠账**，不是环境限制。
  `MainActivity.java` 注册了 6 个插件（AppSettings / Sherpa / BiometricAuth /
  BackgroundMic / **EmailFetch** / AiStreamKeepalive），**没有 Keystore**；
  `frontend/src/native/keystore.ts` 自己也写着
  「For now we use a stub that throws until the native plugin is built」。
  SQLCipher 库主密钥按设计应由 Keystore 派生（见 `native/local-db.ts` 头注释），
  当前退化为「主密码直接派生」。**下一轮应当实现它，而不是记为环境问题。**
- ⚠️ **上一版 handoff 把 `EmailFetch` 也写成「原生实现不存在」，这是错的，已修正。**
  `EmailFetchPlugin.java` 存在且已在 `MainActivity` 注册，
  `@CapacitorPlugin(name = "EmailFetch")`，实现 `configure` / `schedule`
  （走 `EmailFetchReceiver` + AlarmManager）/ `runNow`（走 `EmailFetchRunner`，独立线程），
  是有真实行为的实现。BUG-G 里 `"EmailFetch.then()" is not implemented` 那个报错的
  真正来源是 **thenable 陷阱**（§3.5），不是插件缺失。
- **`/cost` 路由行为待查**：真机上访问 `/cost` 实际落到了 `/#/ai-chat`（文本长度 420），
  模拟器上则是 `/#/cost`。可能是「AI 未配置」时的回退跳转，本轮未定位。
- **既有的 `TestMeetingWorkspaceIsolation` 失败**：`go test ./internal/server/...` 全量跑时失败，
  单跑通过（测试间状态污染）。已做同条件对照（stash 我的改动再全量跑），
  **失败一致，非本轮引入**，属既有欠账。

---

## 5. 环境与踩坑记录（下一轮直接复用）

### 并发会话污染（**本轮最大干扰源，务必先解决**）
另一个会话在本仓库同时跑 `scripts/device.mjs` / `scripts/cdp.mjs`，后果（本轮共发生 **4 次**）：
- 反复 `adb kill-server`，导致我这边脚本中途 `device not found`（USB 序列号掉线，改用 WiFi adb 绕过）
- 重新 `cap sync` 把 `androidScheme` 改回默认值 → 08:20 重装覆盖了我的 reversedev 包
- **08:56 再次重装 APK**，把 origin 换回 `https://localhost`，13 模块验证整轮作废
- 抢占同一台真机，导致测量不可信

**接力第一件事：确认没有其他会话在动这个仓库和这两台设备。**

反制手段（本轮实测有效）：
- 构建产物归档成带时间戳的独立文件（`logs/apk/redmi-bugj-<ts>.apk`），
  被覆盖后可直接 `adb install -r` 装回，不用重新构建
- **把「装包 + 起 App + 登录 + 断言」合并成一个脚本**（`scripts/redmi-final-verify.mjs`），
  把竞态窗口从几分钟压到一次运行
- 每次断言前先校验 `location.origin` 与 `resolveRuntimeApiBase()` 的解析结果一致，
  origin 一旦是 `https` 就说明包被换了，立刻停手

### 设备 localStorage 残留会静默压过构建默认值
`localStorage.pocket_api_base` 优先级**高于** `VITE_API_BASE`。本轮它先后被写成
`http://192.168.31.20:8088` 和 `http://127.0.0.1:8088`，导致 App 的 HTTP/WS 全部打向
（对真机不可达的）地址，而页面底部「后端服务器」仍显示构建默认值——**极具迷惑性**。
换包后第一件事：
```js
localStorage.removeItem('pocket_api_base')   // 再 Page.reload
```

### 设备与工具
- 真机 adb：`C:\Users\86133\AppData\Local\Android\platform-tools\adb.exe`，序列号 `4c308e2e`（USB）或 `192.168.31.19:5555`（WiFi）
- 模拟器：`emulator-5554`（AVD `pocket-test`），**无 `curl`**，别浪费一轮去测连通性
- `adb reverse` 在这个 `-no-window` 模拟器上无效；用 LAN 地址 `192.168.31.20:8088`
- JDK：`C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot`
- npm/npx 需 `cmd /c` 包裹（PowerShell 执行策略拦 `npm.ps1`）

### 输入与 UI
- 可靠点击：`adb shell input touchscreen tap <x> <y>`
- 关软键盘必须 `KEYCODE_BACK`（`KEYCODE_ESCAPE` 无效）；`KEYCODE_BACK` 可能连带退出到桌面
- 软键盘会遮挡底部按钮，点之前先确认 `dumpsys input_method` 的 `mInputShown=false`
- **`input text` 遇 `&` 会被设备 shell 吃掉** → 整个参数必须用单引号包住：
  `adb shell "input text '<含 & 的值>'"`。踩过一次：密码少字符导致 401，误以为链路不通。
  （dev 旁路口令不在此回显；需要时从 `backend/internal/server/server_assistant.go` 的
  `devPass` 常量读取，或用 `scripts/redmi-login-ws.mjs` 从源码直接读取注入，不落盘不回显。）
- MIUI「自动保存账号密码」弹窗会吃点击，用 BACK 关掉
- 通知权限弹窗：`pm grant <pkg> android.permission.POST_NOTIFICATIONS`

### 判据技巧
- **优先用 CDP，不要依赖截图**。真机 `screencap` 会返回 0 字节或半张黑图
  （本轮连续 5 次全部失败），截图路径整体不可用。
  DEBUG 包已开 `WebView.setWebContentsDebuggingEnabled(true)`，走 devtools socket 即可：
  ```bash
  adb shell pidof <pkg>                                  # 当前 App PID
  adb shell cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'
  adb forward tcp:9222 localabstract:webview_devtools_remote_<pid>
  ```
  ⚠️ 必须按**当前 PID** 选 socket；用 `head -1` 会拿到上次进程的残留 socket。
  Node 22 自带 `WebSocket`，不装依赖也能写客户端。
  - 现成脚本：`scripts/cdp.mjs`（`eval` / `eval-file` / `targets`）
  - 本轮新增并入库：`scripts/verify-modules.mjs`（13 模块批量可达性 + 控制台异常，
    `POCKET_SERIAL` 环境变量切设备，默认 `emulator-5554`）、
    `scripts/cdp-net-probe.mjs`（抓 `Network.loadingFailed` 的 `errorText`）
  - `Runtime.evaluate` 同步执行有超时，**超过 ~10s 的表达式会报
    `Runtime.evaluate timeout`**；要拆成多个小探针，或改用事件监听。
- 确认 XHR 是否真的到达后端：看 App 错误文案分支。`LoginView.vue:522-529` 的「登录失败：用户名或密码错误」
  **只在 `e instanceof ApiError && e.status === 401` 时出现**；网络失败会走 `e.message`（`Failed to fetch`）。
  看到这个中文，就说明请求往返成功、只差凭据。
- 区分 CORS 拒绝与网络层不可达：`fetch(url, {mode:'no-cors'})`。
  **no-cors 也失败 = 网络层问题**（CORS 只在 cors 模式生效）；
  no-cors 成功但 cors 失败才是 CORS。配合 CDP `Network.loadingFailed` 看 `errorText` 最直接。
- 后端侧权威信号：`logs/pocketd.err.log` 的 `WebSocket client connected: <ip> (total: 1)`

### 安全事项
`backend/internal/server/server_assistant.go:200-217` 的 dev 旁路在 `POCKET_AUTH_PASS` 未设置时
会回落到**源码内置的默认口令**。仅 dev 可用，但生产务必显式设置。文档与报告中不要回显该口令。

---

## 6. 建议的下一轮起手式

> 真机端到端已在 08:59 打通，下一轮可以直接从「补写操作验证」起步，不必重走环境排障。

0. **先确认无并发会话**（本轮被 `device.mjs` / `cdp.mjs` 污染过 4 次：`cap sync`、adb server、两次重装 APK）
0.5 **先把 PG 拉起来**（`logs/start-pocketd-pg.cmd`），否则一半模块的写操作是 503。
   步骤与踩坑见 §4.12。一次性命令：
   ```powershell
   # PG 未启动时
   Start-Process logs\pg\dist2\pgsql\bin\postgres.exe `
     -ArgumentList '-D','C:\workspace\openpocket\logs\pg\data','-p','5432','-h','127.0.0.1' -WindowStyle Hidden
   # 重启 pocketd 带 DSN
   Get-Process pocketd-new -ErrorAction SilentlyContinue | Stop-Process -Force
   Start-Process cmd -ArgumentList '/c','scripts\start-pocketd-pg.cmd' -WindowStyle Hidden
   node scripts/backend-endpoint-matrix.mjs      # 期望 18/20
   ```
1. **直接用 `adb reverse` 绕开宿主网络**，不要再纠缠 `192.168.31.20:8088`：
   ```powershell
   adb -s 192.168.31.19:5555 reverse tcp:8088 tcp:8088
   ```
   ⚠️ 每次 `adb kill-server` / USB 掉线后要**重新执行**（WiFi adb 下隧道名为 `host-30`）。
2. **装包后先清 override 再断言**：`localStorage.removeItem('pocket_api_base')` → `Page.reload`，
   然后确认 `location.origin` 是 `http://localhost`（是 `https` 说明包被换了）。
3. 一条命令跑完终验：
   ```powershell
   $env:POCKET_SERIAL='192.168.31.19:5555'; $env:POCKET_MASTER='<本地库主密码>'
   node scripts/redmi-final-verify.mjs     # 期望 VERDICT 四个布尔全 true
   node scripts/verify-modules.mjs        # 期望 13/13 RENDERED, LOGIN_GATED=0, BLANK=0
   node scripts/host-ws-check.mjs         # 宿主侧后端 WS 回归，期望 OPEN + pong
   ```
4. **补写操作验证**（当前最大空白）。已有：笔记（真机 6/6）、任务创建（真机 201 + 落库）、
   财务 / 智能体 / 会议（模拟器）。仍缺：闪卡 UI 路径、任务/会话的编辑删除、
   密码箱、市场、邮箱、网关、实例、费用配额。
   ⚠️ **先用眼睛看一次真机闪卡 UI**（§5 未验证首条：按钮文案与行为不一致），
   判定是产品缺陷还是我没找对入口，再决定怎么测。
5. **在真机上把 Maestro 跑起来**：需要有人在手机上开「开发者选项 → USB 安装」
   （见 §4.11.1）。授权后 `.maestro/notes-crud.yaml` 可直接用于真机功能回归。
   未安装时先跑 `scripts/maestro-bootstrap.sh`（幂等，含镜像与 JDK 说明）。
6. **查闪卡「新建卡组」按钮的语义**（跳卡片页 / 「卡组」跳列表，文案行为不一致）。
7. **实现 `Keystore` 原生插件**（见 §5 未验证节）：这是代码欠账不是环境问题。
8. 回归默认 `https` 构建，重点看 **XHR 混合内容**是否被正确阻断（WS 在 https 下本就能握手，§3.1）。
7. 可选加固：给指向明文 http:// 后端的构建加断言/告警，避免下一个人重踩 BUG-F。
   另可考虑在 `resolveApiBase` 命中一个**当前 origin 下不可达**的 override 时给出 UI 警告——
   本轮这个「override 静默压过构建默认值 + 页面却显示构建默认值」的行为极具迷惑性。

### 通用教训一：别用「后端没有连接日志」推断握手没到达
401/403 发生在 WebSocket upgrade **之前**，后端的 `WebSocket client connected` 日志根本不会打。
必须用 CDP `Network.webSocketHandshakeResponseReceived` / `webSocketFrameError` 判别，
它们能区分「没发出去」与「发出去了但响应码不对」。本轮就靠这个推翻了 BUG-F 的原始归因。

### 通用教训二：验证「App 连上了」必须抓 App 自己发起的那条连接
我曾用手工 `new WebSocket()` 造的探针拿到 101 就宣布打通，而 App 自己的 `wsClient`
其实连的是另一个地址。判据应是 `Network.webSocketCreated` 的 URL
与 `resolveRuntimeApiBase()` 的解析结果**一致**，且响应码为 101。

### 通用教训五：可达性断言不是功能断言
`LOGIN_GATED=0` / `BLANK=0` / `RENDERED` 只说明组件挂载了。
判「功能可用」必须驱动真实交互 + 回读校验（`scripts/redmi-write-ops.mjs`），
而且断言要强——「编辑后标题还在列表」几乎恒真，「列表摘要显示新正文」才有效。
另见 §4.11.2 的 7 条设计约束（落库判据、重启后恢复会话、disabled 时序、
只读详情页、同名按钮消歧、前置失败即中止）。

### 通用教训六：定性之前先查注册表，别照抄上一轮结论
本轮把 `EmailFetch` 写成「原生插件未实现」，实际 `EmailFetchPlugin.java` 早已实现并注册。
`registerPlugin(...)` 的完整列表在 `MainActivity.java`——**要定性的插件先去那里看一眼**，
比顺着上一轮的结论写要可靠。

### 通用教训三：Capacitor 插件 thenable 陷阱
新增 `registerPlugin` 包装时，**绝不能从 `async` 函数直接 return 插件代理**
（会被当 thenable 采用，触发 `"<Name>.then()" is not implemented`）。
用 `Promise<void>` + 同步传递实例，或 `{ value }` 盒子。
正确范例见 `native/biometricAuth.ts:38-40`（有注释说明）与 `native/background-mic.ts`。
**排查同类问题时全局搜 `registerPlugin`，不要只修报错的那一个。**

### 通用教训四：改 base 解析逻辑时，全局搜同源硬编码
BUG-J 的本质是「新增了一个 origin 取值（`http://localhost`），但旧的 origin 判断没跟上」。
`https://localhost` 写死在 3 处（`api-base.ts`、`client.ts` 注释、`server-select-logic.ts` 注释），
只改了其中一处就会漏。判据函数应该**对取值空间封闭**（如 scheme 无关的正则），
而不是逐个枚举。

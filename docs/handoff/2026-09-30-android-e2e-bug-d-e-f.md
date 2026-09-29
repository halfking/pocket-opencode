# 2026-09-30 · 真机/模拟器端到端审计接力单（BUG-D / BUG-E / BUG-F）

> 本轮范围：Android APK 真机 + 模拟器端到端验证、缺陷定位与修复、文档同步。
> **完成度声明必须以本文件的「已验证 / 未验证」两节为准，不得外推。**

---

## 0. 一句话结论

真机端到端被**一个架构级问题**卡死：**WebView 页面 origin 是 `https://localhost`，而开发后端是 `http://` + `ws://`。
XHR 能通（mixed-content mode 放行），但 WebSocket 被 Chromium ≥111 的 Insecure-WebSocket 策略【硬阻断】，且
`setMixedContentMode` 管不到它**。修复方式是把 Capacitor 本地 scheme 降级为 `http`（`CAP_ANDROID_SCHEME=http` 逃生舱）。
修复后 WebSocket 实测 `connected`。

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

## 5. 已验证 / 未验证（严禁外推）

### ✅ 已验证（有证据）
- BUG-D 构建守卫（裸 build EXIT=1）、typecheck EXIT=0
- BUG-E i18n 292/292 对等，底栏英文 `RSS`
- BUG-F 修复机制在**模拟器**上完整生效：origin 降级、Mixed Content 归零、**WebSocket connected**、登录 200、主密码创建、`/ai` 实时数据
- **BUG-G 修复生效**：13 模块控制台异常 2 → **0**
- **13 个模块全部可达并渲染**，解锁本地库后 `LOGIN_GATED=0` / `BLANK=0`
- **BUG-H 修复生效**：`E2E 50 元` 由 amount=2 修正为 50；`E2E` 由静默记 2 改为 400 拒绝；裸数字契约未破
- **BUG-I 修复生效**：持死 token 时市场模块自动跳 `#/login?reason=expired`，登录页正常渲染（修复前是「刷新/重试」死循环）
- **写操作（部分）**：
  - 笔记 Notes **完整 CRUD**（新建/编辑/删除，含二次确认）
  - 财务 Finance **新建+删除**（解析→确认入账→账本回显→汇总更新）
  - 本地智能体 **新建**（三必填校验 + 角色详情回显）
  - 会议 Meetings **新建**（自动创建 `meeting-1790725942511-waen21`，列表回显 + 删除按钮）
- 后端 CORS / WS origin 校验对 `http://localhost` 均放行
- 后端端点可用性已摸清（见 §4.9）：tasks/sessions/instances/meetings 可用；notes/flashcards 503、vault/marketplace 404 属 dev 未配存储
- 后端 CORS / WS origin 校验对 `http://localhost` 均放行

### ❌ 未验证（下一轮必须补）
- **闪卡 / 密码箱 / 市场 / 邮箱 的写操作无法在当前 dev 环境验证**：
  对应后端 store 未配置（`/api/flashcards` 503、`/api/vault` 404、`/api/marketplace/*` 404，
  见 §4.9）。闪卡列表页直接显示「Could not load flashcards」且保存恒 disabled。
  **这是环境限制，不是模块代码缺陷**，但也**不能因此宣称它们可用**。
  下轮需先把 store 接上再测。
- **任务 / 会话 的写操作**未验证（端点 200 可用，但本轮只做了会议创建）。
- **密码箱 Vault 存疑**：依赖 `Keystore` 原生插件，Android 尚未实现（BUG-G 只让降级路径
  正确生效）。`/api/vault` 亦 404。**很可能 Android 上根本不可用**，下轮需实测确认。
- **真机 Redmi 的端到端**。BUG-F 在真机上 origin 已降级、Mixed Content 归零，
  但宿主侧网络不通（见 §4.5），未能完成登录与 WS 握手。
- **生产 `https` scheme 下的真机回归**。BUG-F 修复是 `CAP_ANDROID_SCHEME=http` 这条
  opt-in 路径，默认仍是 `https`，该路径本轮未回归。
- BUG-G / BUG-H / BUG-I 的**真机**验证（BUG-G/I 目前只在模拟器 WebView 验证）。
- `Keystore` / `EmailFetch` 的**原生实现本身仍不存在**——本轮只是让降级路径
  正确生效（不再抛未捕获异常），并未实现这两个插件。
- **既有的 `TestMeetingWorkspaceIsolation` 失败**：`go test ./internal/server/...` 全量跑时失败，
  单跑通过（测试间状态污染）。已做同条件对照（stash 我的改动再全量跑），
  **失败一致，非本轮引入**，属既有欠账。

---

## 5. 环境与踩坑记录（下一轮直接复用）

### 并发会话污染（**本轮最大干扰源，务必先解决**）
另一个会话在本仓库同时跑 `scripts/device.mjs` / `scripts/cdp.mjs`，后果：
- 反复 `adb kill-server`，导致我这边脚本中途 `device not found`
- 重新 `cap sync` 把 `androidScheme` 改回默认值
- 抢占同一台真机，导致测量不可信

**接力第一件事：确认没有其他会话在动这个仓库和这两台设备。**

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
- **`input text` 遇 `&` 会被设备 shell 吃掉** → 必须写成 `adb shell "input text 'Veritrans&9527'"`（单引号保护）。
  踩过一次：密码少字符导致 401，误以为链路不通
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

0. **先确认无并发会话**（本轮被 `device.mjs` / `cdp.mjs` 污染过 `cap sync` 和 adb server）
1. **先证明宿主可达再开工**：`adb -s 4c308e2e shell curl -s -o /dev/null -w '%{http_code}' http://192.168.31.20:8088/healthz`
   必须返回 200。本轮真机反复卡在宿主不可达（`ERR_ADDRESS_UNREACHABLE`），
   shell 和 WebView 都不通，但同一时刻宿主自身访问却是 200——纯宿主侧网络/防火墙问题。
   **若仍不通，改用 `adb reverse tcp:8088 tcp:8088` + `VITE_API_BASE=http://localhost:8088` 绕开**
   （`buildOriginChecker` 只校验 hostname 不校验端口，`http://localhost:8088` 会被放行，
   且与 Capacitor 的 `http://localhost`(80) 不同源）。
2. `set CAP_ANDROID_SCHEME=http` → `cap sync` → `gradlew assembleDebug`
   → **校验 APK 内 `assets/capacitor.config.json` 的 `androidScheme` 真的是 http**
   （不能只看工作区文件，本轮被并发会话改回过）→ 装机
3. 真机走一遍：登录 → 创建/解锁主密码 → `/ai`，
   确认 logcat 出现 `WebSocket connected` 且无 `Reconnecting WebSocket`、无 `Mixed Content`。
   **验收标准是 WebSocket 连上，不是 origin 变了。**
4. **补 13 个模块的写操作验证**（目前只有可达+渲染）。用 `scripts/verify-modules.mjs`
   做前置巡检，再对每个模块实际新建/编辑/删除一条并回读确认落库。
5. 真机通过后，回归一次默认 `https` 构建，确认生产路径没被降级影响。
6. 可选加固：给指向明文 http:// 后端的构建加断言/告警，避免下一个人重踩 BUG-F。

### 通用教训：Capacitor 插件 thenable 陷阱
新增 `registerPlugin` 包装时，**绝不能从 `async` 函数直接 return 插件代理**
（会被当 thenable 采用，触发 `"<Name>.then()" is not implemented`）。
用 `Promise<void>` + 同步传递实例，或 `{ value }` 盒子。
正确范例见 `native/biometricAuth.ts:38-40`（有注释说明）与 `native/background-mic.ts`。
**排查同类问题时全局搜 `registerPlugin`，不要只修报错的那一个。**

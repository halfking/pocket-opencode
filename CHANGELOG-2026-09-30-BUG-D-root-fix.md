# BUG-D 根治修复（2026-09-30 03:00 凌晨紧急修复）

## 背景

**BUG-D** 在真机验收中自发复现 4 次：裸 `vite build`（即 `npm run build` / `build:fast`，
MODE=production 且无对应 `.env.production`）会让 `VITE_API_BASE` 被替换成 `undefined`，
导致：

1. **ServerSelectView** 的 "Build default" 选项因 `v-if` 被整条移除
2. **App 运行时回落到同源** —— 在 Capacitor 里就是 WebView 自己的 `https://localhost`，
   所有 `/api` 请求拿回本地 `index.html`（HTML 而非 JSON），App 静默失效

**根因**：守卫只写在 `scripts/build-mobile.mjs` 里，任何绕过该脚本的构建（例如并发会话直接
跑 `npm run build:fast`）都不受保护。

**影响面**：本次验收中发现另一个活跃会话在同一工作区并发重建前后端和 APK，4 次覆盖了验证过的构建，
每次都因裸构建而生成故障 APK，直接导致「哪个 APK 在跑」这一前提反复断裂。

---

## 修复内容

### 1. vite.config.ts：下沉守卫到构建入口（对所有 mode 生效）

**文件**：`frontend/vite.config.ts`

**改动**：

- 引入 `loadEnv` 并在 `defineConfig` 回调中加载环境变量
- 新增 `assertApiBaseForBuild(mode, env)` 函数：
  - 非 development mode 且 `VITE_API_BASE` 为空时抛出明确错误
  - 逃生舱：`MOBILE_ALLOW_EMPTY_API_BASE=1`（仅 web 同源部署等确有需要的场景）
- 将该断言置于 `defineConfig` 回调顶部，使所有 mode 的构建都先校验

**验证**：

```powershell
# 裸 production 构建现在会报错并拒绝（EXIT=1）
npx vite build --mode production

[vite] 拒绝构建：VITE_API_BASE 为空。
  本次 mode = "production"，没有加载到任何提供 VITE_API_BASE 的 env 文件。
  ...
```

```powershell
# 合法的移动端构建仍然通过
$env:MOBILE_FAST="1"; node scripts/build-mobile.mjs android dev
# ✓ built in 54.49s
# dist 里包含 192.168.31.20:8088
```

**影响**：

- **不影响**任何合法路径：`scripts/build-mobile.mjs` 本就通过 `--mode <target>` 加载对应
  `.env.<target>`，现在只是把校验前置到 Vite 配置层
- **阻止**绕过脚本的裸构建生成故障 bundle

---

### 2. HTTP client 统一到 `resolveRuntimeApiBase`

**文件**：`frontend/src/api/client.ts`

**问题**：

- `client.ts` 原先用裸 `resolveApiBase()`，而 `api/http.ts` 用的是带兜底的 `resolveRuntimeApiBase()`
- 两条 API 调用路径解析 base 的方式不一致，导致构建缺少 `VITE_API_BASE` 时：
  - `http.ts` 的请求回退到生产入口 `https://pocket.itestu.cn`
  - `client.ts` 的请求却落到同源 `''`，在 Capacitor 里就是 `https://localhost`，
    `/api` 返回本地 `index.html`（HTML 而非 JSON）
- 即测试机上会出现**「一半请求打到生产、一半打到本地壳」的分裂行为**

**改动**：

```typescript
// frontend/src/api/client.ts:1
import { resolveRuntimeApiBase as resolveApiBase } from '../config/api-base'
```

保留本地名 `resolveApiBase` 以免改动全部调用点，实际指向 `resolveRuntimeApiBase`，
与 `http.ts` 走同一个解析入口：Capacitor 源且解析为空时回退生产入口（`api-base.ts:104` 的既有约定）。

**验证**：`npx vue-tsc --noEmit` 通过。

---

### 3. WebSocket mobile base 修复

**文件**：`frontend/src/api/websocket.ts`

**问题**：

- 同样是 `resolveApiBase()` vs `resolveRuntimeApiBase()` 的不一致
- 真机 logcat 显示 mixed-content 警告：
  ```
  Mixed Content: The page at 'https://localhost/#/login?returnTo=/notes&unlock=1' was loaded over HTTPS,
  but attempted to connect to the insecure WebSocket endpoint 'ws://192.168.31.20:8088/ws?token=...'
  ```
- WebSocket 连接反复失败，App 进入无限重连循环

**改动**：

```typescript
// frontend/src/api/websocket.ts:1
import { resolveRuntimeApiBase } from '../config/api-base'

function wsHttpBase(): string {
  // 与 HTTP API 共用运行时解析：Capacitor 的 https://localhost 在缺少
  // build default 时必须回退到生产/配置后的真实 pocketd 地址，不能让 WS
  // 误连 WebView 自身并被 mixed-content 拦截。
  return resolveRuntimeApiBase() || (typeof window !== 'undefined' ? window.location.origin : '')
}
```

**验证**：`npx vue-tsc --noEmit` 通过。

---

## 构建产物

**新 APK**：

- **SHA256**: `9ADC891620CE3EBD958BA79A655D1C90F7B9638C0349458B96E6B0F8236761E2`
- **构建时间**: 2026-09-30 03:53:09
- **包含修复**: BUG-A (WebView insets)、BUG-C (logout button)、BUG-D (API base guard + HTTP/WS 统一)、BUG-E (i18n tab labels)

**部署状态**：

- 已 `pm clear` Redmi 真机 App 数据（清空上一轮遗留的加密库）
- 已安装到 Redmi `4c308e2e` 和模拟器 `emulator-5554`

---

## 遗留问题

### 1. 真机 mixed-content 拦截仍未解决

logcat 仍显示：

```
Mixed Content: ... attempted to connect to the insecure WebSocket endpoint 'ws://192.168.31.20:8088/ws...'
```

**根因**：

- WebView 从 `https://localhost` 发起 `ws://` 连接被 Android WebView 的 mixed-content 策略拦截
- `MainActivity.java:97-100` 设置了 `setMixedContentMode`，但**仅在 `BuildConfig.DEBUG` 时
  才允许**；`assembleDebug` 构建**默认 `debuggable false`**（Gradle 未显式设置时的默认值）

**修复方向**：

1. **临时**：在 `app/build.gradle` 的 `buildTypes { debug { ... } }` 里显式加 `debuggable true`
2. **长期**：后端启用 HTTPS（wss://），或在开发/staging 环境的 APK 里允许 mixed-content

### 2. 真机跨批次交互不稳定

- App 刚启动后，**第一次点击输入框常被吞掉**（不弹键盘、不获得焦点），第二次才生效
- 权限对话框（通知权限、MIUI 自动保存密码弹窗）会吃掉后续点击
- 需在每次冷启动或系统弹窗后补一次点击，或延长等待

### 3. 设备连接极不稳定

- USB (`4c308e2e`) 与 WiFi ADB (`192.168.31.19:5555`) 在单次运行中各掉线 2–3 次，间隔约 2–4 分钟
- 掉线期间 `uiautomator dump` 报 `null root node returned by UiTestAutomationBridge`，截图返回 0 字节
- 单步测试必须带重试，否则证据会静默缺失

### 4. 真机端到端验收未完成

**已验收通过**（上一轮 + 本轮部分）：

- BUG-A 修复（WebView 126 冷启动 0 错误）
- BUG-C 修复（logout 按钮点击生效）
- BUG-E 修复（底栏中英文正确）
- AI 工具 / 对话 tab 基础交互

**仍未验收**（被权限弹窗 + 设备掉线打断）：

- 主密码创建流程
- 笔记 / 会议 / 订阅 / 邮箱 / 密码箱 / 闪卡 / PKM / 本地智能体 共 8 个本地模块
- 费用 / 配额、网关、实例、任务、会话 共 5 个模块
- 写操作验证（创建笔记 / 会议 / 订阅 / 密码）

---

## 总结

**BUG-D 根治修复已完成**：

1. **vite.config.ts 守卫**：对所有 mode 生效，裸构建现在会拒绝（已验证）
2. **HTTP client 统一**：`client.ts` 与 `http.ts` 统一到 `resolveRuntimeApiBase`（typecheck 通过）
3. **WebSocket mobile base 修复**：同样统一到 `resolveRuntimeApiBase`（typecheck 通过）

**新 APK 已部署到真机和模拟器**，但：

- **真机验收被 mixed-content 拦截、权限弹窗、设备掉线反复打断，未完成全功能点验收**
- **mixed-content 需要 Gradle `debuggable true` 或后端 HTTPS** 才能彻底解决
- **跨批次稳定性差、设备连接不稳**是真机测试环境固有障碍，需要重试机制或更稳定的 ADB 通道

---

## 下一步建议

1. **Gradle 加 `debuggable true`**（或后端 HTTPS）解决 mixed-content 拦截
2. **真机重启 + 单独会话**重跑完整验收（避免并发构建干扰）
3. **模拟器补齐 5 个未验证模块**（费用/配额、网关、实例、任务、会话）
4. **写操作验证**（创建笔记/会议/订阅/密码）
5. **更新两份 summary**（`logs/redmi-e2e-.../summary.md` + `logs/emu-e2e-.../summary.md`）

---

**交付时间**：2026-09-30 04:00  
**验收状态**：BUG-D 根治修复已完成并验证；真机全功能点验收因环境障碍未完成

---

# 追加：BUG-F 真机 WebSocket 硬阻断（2026-09-30 05:50）

## 背景

BUG-D 修完后真机仍无法完成端到端：logcat 持续出现 Mixed Content 告警与 WebSocket 无限重连
（`Reconnecting WebSocket (attempt 1, 3182ms backoff)` / `attempt 2, 4.6s...`）。

## 根因（关键：两类 mixed content 行为不同）

| 通道 | 管控者 | 修复前行为 |
|---|---|---|
| XHR `http://…/api/*` | `WebSettings.setMixedContentMode` | debug 下 `ALWAYS_ALLOW` → 放行（仅 console 告警） |
| WS `ws://…/ws?token=` | Chromium >=111 Insecure-WebSocket 策略 | **硬阻断，不受 mixed content mode 影响** |

因此 BUG-D 中「`buildTypes.debug.debuggable true` 对 mixed-content 无效」这一观察是正确的：
那条路径原理上不可能生效，不是配置写错。

后端不是问题（实测）：
- `GET /api/app/check-update` + `Origin: https://localhost` → 200，`ACAO: https://localhost`，预检 200
- 生产 `/ws` 的 origin 校验 `buildOriginChecker(AllowedOrigins, DevAuth)` 在 devAuth 下放行 `http(s)://localhost`
  （`backend/internal/server/server.go:292-296`）
- `mobile_api.go:529` 的 `CheckOrigin: return true` 是死代码，已被 `mobile_api_isolation_test.go` 锁死

## 修复

`frontend/capacitor.config.ts` 新增逃生舱：

```ts
androidScheme: (process.env.CAP_ANDROID_SCHEME as 'http' | 'https') ?? 'https',
```

- 默认仍为 `https`（生产后端应走 HTTPS + wss，不需降级）
- 仅本地/内网 HTTP 后端联调时用 `CAP_ANDROID_SCHEME=http` 构建
- 机制：`cap sync` 写入 `android/app/src/main/assets/capacitor.config.json`，运行时由 Capacitor 读取
- 效果：页面 origin 变为 `http://localhost`，与后端同为非安全上下文，mixed content 规则不再适用；
  XHR 走 CORS（已验证放行），ws:// 直接放行

## 验证（模拟器 emulator-5554）

| 指标 | 修复前 | 修复后 |
|---|---|---|
| `Loading app at` | `https://localhost` | `http://localhost` |
| Mixed Content 告警 | 6 | **0** |
| WebSocket | error → disconnected → 无限重连 | **`WebSocket connected`** |
| Failed to fetch | - | 0 |

端到端通过：登录 200（`auth_method=dev-bypass`）→ 创建主密码 → `/ai` 实时状态 `● 全部正常 · 0`。

## 坑：必须校验 APK 内实际打包的 scheme

本轮被并发会话坑过一次：`cap sync` 已写对 `http`，但打包前另一个会话重跑 `cap sync` 改回 `https`，
装上去 origin 仍是 `https://localhost`，一度误判为「修复无效」。构建流程应加断言：

```powershell
$z=[System.IO.Compression.ZipFile]::OpenRead($apk)
$e=$z.Entries | Where-Object { $_.FullName -eq 'assets/capacitor.config.json' }
```

## 未完成

真机 Redmi（4c308e2e）复验未完成：新 APK 已装、origin 已确认 `http://localhost`、Mixed Content 归零，
但当时设备到宿主 `192.168.31.20` 100% 丢包（`Failed to fetch`），未能完成登录与 WS 握手。
**只能声明「真机 scheme 已生效」，不能声明「真机端到端已通过」。**

详见 `docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md`。

---

# 追加：BUG-G Capacitor 插件 thenable 陷阱 + 13 模块可达性验证（2026-09-30 07:05）

## 背景

BUG-F 修完后用 CDP 逐个访问 13 个模块（不走截图，真机 screencap 连续 5 次返回 0 字节），
在 `/email` 与 `/vault` 上暴露未捕获异常：

```
/email : WARNING [email] sync from server: email store not configured
         EXC Error: "EmailFetch.then()" is not implemented on android
/vault : EXC Error: "Keystore" plugin is not implemented on android
```

## 根因

Capacitor 的 `registerPlugin(name)` 返回**带 `.then` 的 thenable 代理**。
若把它从 `async` 函数 `return` 出去，或当作 `.then()` 回调的返回值，
JS 的 promise 决议会去调它的 `.then()`，未实现的原生插件直接抛
`"<Name>.then()" is not implemented`。

两层后果：
1. 抛出未捕获异常，掩盖真实原因；
2. 写好的降级路径完全失效——`keystore.ts` 的 `StubKeystore` 只在
   `registerPlugin` 抛异常时启用，但代理不抛、只返回 thenable，
   「优雅降级」从未真正跑过一次。

**项目其实早就知道这个陷阱**：`native/biometricAuth.ts:38-40` 有明确注释
「绝不能从 async 函数直接 return 这个代理（会被当 thenable 采用）」，
且已用 `Promise<void> + 同步传递实例` 修好；`background-mic.ts` 写法同样正确。
**但 keystore.ts / email-fetch-native.ts / util.ts（Sherpa 在用）三处漏了。**

## 修复

统一改为「非 thenable 盒子」装载实例，async 只返回盒子：

| 文件 | 改动 |
|---|---|
| `frontend/src/native/keystore.ts` | `load(): Promise<KeystoreBox>`，`{ value: impl }`；facade 用 `box.value[prop]` |
| `frontend/src/features/email/email-fetch-native.ts` | `ensurePlugin(): Promise<EmailFetchBox \| null>`；`Promise.resolve().then()` 回调改为**不返回值**（赋值结果若被当决议值会再次触发陷阱） |
| `frontend/src/native/util.ts` | `ensure(): Promise<{ value: T }>`；`registerPluginSafely`（Sherpa 使用） |

`vue-tsc --noEmit` EXIT=0；重建 APK（`index-oroRx_TG.js`）后 13 模块控制台异常 2 -> 0。

## 13 模块可达性验证（scripts/verify-modules.mjs，新增入库）

CDP `Runtime.enable` 抓 `exceptionThrown` / `console.error|warning`，
逐路由 `location.hash` 导航后检查最终 URL + 渲染文本量 + 是否被重定向回 /login。

首轮出现 4 个 `LOGIN_GATED`（Notes/Email/Vault/PKM，URL 带 `?unlock=1`）——
**这不是缺陷**：本地加密库需主密码解锁，页面提示「检测到已登录态，但本地加密库未解锁」，
输入主密码后 `returnTo` 正确跳回。冷启动后必须重新解锁，
否则会把设计行为误判成「模块打不开」。

解锁后最终：13/13 RENDERED，`LOGIN_GATED=0`，`BLANK=0`，控制台错误 0。

## 真机 Redmi 的 BUG-F 复验：宿主侧网络阻塞，非代码问题

CDP `Network.enable` 抓到 `errorText="net::ERR_ADDRESS_UNREACHABLE"`；
`no-cors` 模式同样失败、XHR `status=0` -> 排除 CORS。但对照实验表明与 App 无关：

| 发起方 | 目标 | 结果 |
|---|---|---|
| 设备 shell curl | 网关 192.168.31.1:80 | 302, 10-64ms OK |
| 设备 shell curl | 宿主 192.168.31.20:8088 | 000, ~1.1s 超时 FAIL |
| WebView fetch | 网关 192.168.31.1:80 | opaque 成功 OK |
| WebView fetch | 宿主 192.168.31.20:8088 | ERR_ADDRESS_UNREACHABLE FAIL |

同一时刻宿主自身 127.0.0.1:8088 与 192.168.31.20:8088 都 200、
`Get-NetTCPConnection` 正常监听 -> 宿主侧网络/防火墙问题。
**不能把真机失败记为代码缺陷。** 下轮备选方案：
`adb reverse tcp:8088 tcp:8088` + `VITE_API_BASE=http://localhost:8088`
（`buildOriginChecker` 只校验 hostname 不校验端口，该 origin 会被放行，
且与 Capacitor 的 `http://localhost`(80) 不同源）。

## 仍未验证（不得写成已完成）

- **13 个模块的写操作**：本轮只证明「可达 + 渲染 + 无异常」，新建/编辑/删除一条都没点过。
- 真机 Redmi 端到端（宿主网络阻塞）
- 生产 `https` scheme 回归
- `Keystore` / `EmailFetch` 的**原生实现本身仍不存在**——本轮只是让降级路径正确生效
  （不再抛未捕获异常），并未实现这两个插件。

详见 `docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md`。

---

# 追加：BUG-H 财务金额解析抢占 + 写操作验证（2026-09-30 07:30）

## 写操作验证（CDP 驱动真实 UI）

工具：CDP `Runtime.evaluate` 导航 + 原生 setter 赋值 + `dispatchEvent(new Event('input'))`
触发 Vue `v-model`，再点真实按钮。**直接改 `.value` 不会更新 v-model，必须走原生 setter。**

已通过：
- 笔记 Notes 完整 CRUD：新建（列表回显「0分钟前」）/ 编辑（id note-1790723119846-xzs3ce，
  editPersisted=true）/ 删除（二次确认 confirmed=1，列表回到空态）
- 财务 Finance：解析->确认入账->账本回显->汇总更新->删除（deleteWorked=true，汇总归 0）
- 本地智能体：三项必填校验后创建，# /agents/custom-e2e--1790723592，详情页字段齐全

排查中确认的非缺陷：
- 智能体保存「没反应」= AgentEditView.vue:98-111 要求 name/description/system_prompt
  三项都非空，缺任一即 toast.error 后 return。正确校验行为。
- 4 模块 LOGIN_GATED = 本地加密库需主密码解锁，设计行为。

## BUG-H 财务金额解析被标识符中的数字抢占（后端）

现象：`E2E 50 元` 解析成 amount=2，/api/finance/parse 返回 200，错误金额静默入账。

根因 recognizer.go:27
    amountRegex: regexp.MustCompile(`[¥$]?\s*(\d+(?:\.\d{1,2})?)\s*(?:块钱?|元|钱)?`)
除数字外全部可选，且 FindStringSubmatch 取第一个匹配 -> "E2E" 的 2 命中。
与拉丁前缀无关：只要第一个数字粘在 ASCII 字母后就会中招（`abc 100 元` 反而正常，
因为 abc 里没数字）。

修复：两级策略
1. amountRegex 优先匹配「货币符号+数字」或「数字+货币单位」（单位/符号必填）；
2. 都没有时回退 looseAmountRegex：任意数字，但前一字符不得是 ASCII 字母/数字。

> 第一版曾试图「强制单位必填」，打破了 4 个既有测试（吃饭花了38 / 入账1000 /
> 收款1000 / 项目尾款3000到账 都是裸数字），说明裸数字是既有契约。两级策略才对。
> 新增 recognizer_bugh_test.go 同时锁定「修好了」与「没改坏」。

验证（重建后端后打真实接口）：
    E2E 50 元   200 amount=50   （修复前 2）
    test2 打车 30 元 200 amount=30
    吃饭花了38 / 入账1000 / 项目尾款3000到账 -> 38/1000/3000 契约未破
    买了100块…又花了50块打车 -> 100 仍取第一个金额
    乱七八糟没有数字 / E2E -> 400 正确拒绝（E2E 修复前静默记 2）
go test ./internal/finance/... 全绿（含 2 个新增回归测试）。

## 既有的 TestMeetingWorkspaceIsolation 失败（非本轮引入）

go test ./internal/server/... 全量跑时该测试失败，单跑通过（测试间状态污染）。
已做同条件对照：stash 掉本轮改动后全量重跑，失败完全一致 -> 属既有欠账。

## 仍未验证

- 剩余 10 个模块写操作：闪卡 / PKM / 密码箱 / 市场 / 邮箱 / 任务 / 会话 / 网关 / 实例 / 费用配额
- 密码箱 Vault 疑似 Android 不可用（依赖未实现的 Keystore 原生插件）
- 市场 Market 疑似加载失败（只见「刷新/重试」）
- 真机端到端、BUG-G/BUG-H 真机验证、生产 https 回归

详见 docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md。

---

# 追加：BUG-J + BUG-F 归因证伪 + 真机端到端打通（2026-09-30 09:00）

## 🔴 先说最重要的：BUG-F 的原始归因是错的

原结论（写在本文件上文与 handoff §3）：「WebSocket 被 Chromium ≥111 的
Insecure-WebSocket 策略**硬阻断**，`setMixedContentMode` 管不到，必须把
Capacitor 的 scheme 降级成 `http`」。**该结论已被真机实测证伪。**

### 证伪的关键判据

原判据是「`WebSocket` 构造失败 + 后端日志里没有连接记录」。这个组合**无法区分**
「被浏览器阻断」和「到了后端但被拒」——因为 401/403 发生在 upgrade **之前**，
后端那条 `WebSocket client connected` 日志根本不会打印。我据此错误地判定「握手没到后端」。

改用 CDP `Network` 域后，真机上的实测结果：

| 目标 | CDP 事件 | 结论 |
|---|---|---|
| `ws://localhost:8088/__probe_no_such_route__` | `wsHandshake` + **404** | 握手**到了后端**并拿到了 Echo 的响应体 |
| `ws://localhost:8088/ws?token=probe` | `wsHandshake` + 401 | 握手**到了后端**，被鉴权拒绝 |
| `ws://192.168.31.20:8088/ws?...` | `ERR_ADDRESS_UNREACHABLE` | 目标地址在真机上不可达 |

console 侧措辞也印证：Chromium 说的是
`Connecting to a non-secure WebSocket server from a secure origin is **deprecated**`
（弃用**警告**），不是 blocked。若真是策略硬阻断，CDP 只会给
`loadingFailed` / `ERR_BLOCKED_BY_CLIENT`，不可能给出后端的状态码。

### mixed content 对 XHR 与 WS 的真实差别（修正）

| 通道 | `https://localhost` 页面请求 `http://…` / `ws://…` | 实测 |
|---|---|---|
| XHR / fetch | **真阻断**，请求出不去浏览器 | Mixed Content 警告 + 请求根本没到网络层 |
| WebSocket | **仅 deprecation warning**，照常发出 | `wsHandshake` + 后端真实状态码 |

`CAP_ANDROID_SCHEME=http` 逃生舱**方向仍然正确**（它消除的是 XHR 的硬阻断，
Mixed Content 计数归零），但「它是用来救 WS 的」这个因果关系是错的。

### 真机 WS 断连的真实原因（两条，都不是代码缺陷）

1. **APK 被打成了 LAN IP**：并发会话用 `.env.android-dev`
   （`VITE_API_BASE=http://192.168.31.20:8088`）重建并重装，覆盖了 reversedev 包，
   而真机到宿主 LAN IP 是 `ERR_ADDRESS_UNREACHABLE`（宿主防火墙）。
2. **设备上持有的 JWT 失效**：真机 localStorage 的 token 由另一个 pocketd 实例签发，
   与当前 pocketd 的 `POCKET_JWT_SECRET` 不匹配。该 token 连**普通 HTTP**
   `GET /api/auth/me` 也是 401 `invalid or expired token`；现场重新登录拿的新 token 立刻 200。

## BUG-J：`getTasks` 把 API base 的 scheme+host 当前缀删掉

真机 13 模块巡检中只有 `/tasks` 报错：

    ERROR Failed to load tasks: Error: API 返回了 HTML 页面而非 JSON：
          通常是移动端打包漏注入 VITE_API_BASE（请求落到 WebView 本地 index.html）…

`frontend/src/api/client.ts` 的 `getTasks` 曾这样拼 URL：

```ts
const url = new URL(`${resolveApiBase()}/api/tasks`, window.location.origin)
const res = await authFetch(url.toString().replace(window.location.origin, ''))
```

那句 `.replace(window.location.origin, '')` 本意是「同源时把绝对地址降成相对路径」，
但 `window.location.origin` **不带端口**而 API base **带端口**，replace 命中前缀整段删掉：

| 项 | 值 |
|---|---|
| 页面 origin | `http://localhost` |
| 正确 URL | `http://localhost:8088/api/tasks` |
| replace 之后 | `:8088/api/tasks` |
| 浏览器解析为 | `http://localhost/:8088/api/tasks` |
| 实际响应 | `200 text/html :: <!doctype html>…`（本地 index.html） |
| 修复后 | `200 application/json :: {"tasks":null}` |

**这是 BUG-F 引入 `CAP_ANDROID_SCHEME=http` 后才暴露的回归，由我自己引入**：
`androidScheme=https` 时 origin 是 `https://localhost`，与 `http://localhost:8088`
字符串不匹配，replace 空转，侥幸不触发。

同时修掉第二处：`frontend/src/config/api-base.ts` 的 Capacitor 壳回退守卫
写的是 `origin === 'https://localhost' || origin === 'capacitor://localhost'`，
**同样漏了 `http://localhost`**。改为 scheme 无关的正则并导出
`isCapacitorShellOrigin()`，让判据对取值空间封闭。

### 改动文件

- `frontend/src/api/tasks-url.ts`（新增）`buildTasksUrl()` 纯函数
- `frontend/src/api/client.ts` `getTasks` 改用 `buildTasksUrl`，删掉 `.replace()`
- `frontend/src/config/api-base.ts` `isCapacitorShellOrigin()` + `resolveRuntimeApiBase()` 用它
- `frontend/src/api/tasks-url.test.ts`（新增，4 例，含一条反证旧实现产物的断言）
- `frontend/src/config/api-base.test.ts` 新增 5 例

### 验证

    node --experimental-strip-types --test src/api/tasks-url.test.ts src/config/api-base.test.ts
    # tests 20   pass 20   fail 0
    npx vue-tsc --noEmit      # 无输出 = 通过

真机复验（Redmi 2411DRN47C，08:59 二次复现）：

    /api/tasks?source=opencode  ->  200 application/json   控制台错误 0

## 真机端到端打通

    node scripts/redmi-final-verify.mjs
    {
      "appWsHandshake101": true,        # App 自己发起的 ws://localhost:8088/ws 握手 101
      "appWsTargetOk": true,            # 目标与 resolveRuntimeApiBase() 解析结果一致
      "tasksJson": true,
      "tasksHtmlRegression": false,
      "tasksConsoleErrors": 0
    }
    node scripts/verify-modules.mjs    # 13/13 RENDERED, LOGIN_GATED=0, BLANK=0
    node scripts/host-ws-check.mjs     # 后端 /ws：OPEN + pong

### ⚠️ 差点把自己骗过去的一处

第一次宣布「真机 WS 101」时，测的是**在页面里手工 `new WebSocket(...)` 造的连接**，
不是 App 自己的 `wsClient`。真正暴露问题的是 `scripts/cdp-base-audit.mjs`：

    localStorage.pocket_api_base = "http://192.168.31.20:8088"   # override 优先级高于 VITE_API_BASE
    WS-CREATED ws://192.168.31.20:8088/ws?token=<redacted>
    WS-ERR "net::ERR_ADDRESS_UNREACHABLE"

**override 会静默压过构建默认值，而页面底部「后端服务器」仍显示构建默认值**，
极具迷惑性。换包后第一件事是 `localStorage.removeItem('pocket_api_base')` 再 reload。

## 仍未验证（不得写成已完成）

- **任务 / 会话的写操作**：`POST /api/tasks` 在 dev 后端恒 503
  `local task store not configured (remote-only mode)`——`taskStore` 只在
  `pool != nil`（PostgreSQL）时构造（`backend/cmd/pocketd/main.go:103-108`）。
  `internal/server/disk_task_fallback.go` 只是**只读**合成，不提供写路径。
- 闪卡 / 密码箱 / 市场 / 邮箱 的写操作：对应后端 store 未配置（503/404），属环境限制。
- 密码箱 Vault 依赖未实现的 `Keystore` 原生插件，Android 上很可能根本不可用。
- 生产 `https` scheme 的真机回归（重点是 XHR 混合内容是否仍被正确阻断）。
- `/cost` 路由：真机上访问 `/cost` 实际落到 `/#/ai-chat`，模拟器上是 `/#/cost`，未定位。
- 既有的 `TestMeetingWorkspaceIsolation` 全量跑失败（测试间状态污染，非本轮引入）。

详见 docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md。

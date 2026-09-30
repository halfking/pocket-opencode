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

---

## BUG-L / M / N / O（2026-09-30 第二轮清扫）

全部由 `scripts/probe-write-methods.mjs`（前端写路径 × 真后端 method 级探测）发现。
这类缺陷静态前缀对账抓不到——路由前缀注册了，handler 却在内部按 method 拒绝。

| 编号 | 现象 | 根因 | 修法 | 回归锁 |
|---|---|---|---|---|
| **BUG-L** | `POST /api/flashcards/notes` 恒 405，闪卡卡片存不进后端 | 创建实现只挂在无尾斜杠的 `/api/flashcards`；`/notes` 子路径的 handler 只允许 GET | 后端补齐 POST，两路径等价；不动前端（契约测试锁的是 `/notes`） | `flashcards_create_note_route_test.go` |
| **BUG-M** | review 空 body 返回 500 `invalid rating 0` | 客户端输入错误走了 store error 分支被归为 500 | 进 store 前挡，返回 400 | `flashcards_review_rating_test.go` |
| **BUG-N** | `PUT /api/notes/:id` 恒 405（API 契约不匹配，**非当前 UI 故障**：编辑走本地 SQLite） | `handleNoteOperations` 只有 GET/DELETE；`notes.Store` 没有任何更新方法 | 新增 `NotePatch` + `UpdateNoteScoped`（所有权进 UPDATE 谓词，Content 变更同步重算 snippet）+ `handleNoteUpdate` | `notes_update_route_test.go` |
| **BUG-O** | 闪卡卡片保存 201、PG 有数据，但卡组页永远看不到卡片 | ① 客户端水位线取 `serverTimeMs` 而非本批最大 `updatedAt`；② 服务端 `updated_at > since` 严格大于；③ 保存后 fire-and-forget 不回读，服务端生成的 card id 客户端拿不到 | ① 水位线改本批最大 `updatedAt`，空结果不推进；② 6 处 `>` 改 `>=`；③ `await flushOutbox()` 后 `await refresh()` 再 `goBack()` | `store_since_test.go` + `flashcards-sync-watermark.test.ts` |

**关于 BUG-O 的服务端 `>=` 改动**：客户端水位线改成"本批最大 updatedAt"后，
严格大于依然会在**同一秒内的多条变更**上丢数据（先收到 A → 水位线 T；服务端同秒写入
B → `T > T` 不成立 → B 永久丢失）。改成 `>=` 会重复返回水位线那一秒的行，
而客户端 merge-by-id 幂等，重复没有副作用，漏数据不可逆。这个不对称是刻意取舍。

### 本轮证伪的疑似缺陷（比新缺陷更值得记）

- `OPTIONS` 任意路径返回 200：`corsMiddleware` 的标准预检短路，设计如此。
- 闪卡列表不显示卡片：列表页按设计只显示卡组，卡片在卡组详情页——验收标准写错了。
- 真机"新建卡组入口没出现"：卡组入口是页内 inline 而非弹窗，测试脚本假设错了。
- 探针里裸 `fetch('/api/...')` 返回 HTML：探针没加 base 前缀，与 BUG-J 同源。

### 与本轮无关的既有失败

`backend/internal/server` 的 `TestMeetingWorkspaceIsolation/list_A` 失败，
已在 `ca4a53e`（不含本轮改动）上用 git worktree 复现且错误信息一致 → 预先存在。
它同时暴露一个未定性的疑点：跨 workspace 的 meeting GET 返回 200、列表返回 0。

---

# 追加：24 小时修正审计轮 BUG-U / V / W（2026-09-30 15:20）

本轮是对 2026-09-30 05:10~13:35 这 20 个提交（BUG-D ~ BUG-T）的审计。
完整分析见 `docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md §4.20`。

## BUG-U：main 的 typecheck 是断的 —— 提交说明写了「exit 0」但实际 exit 2

`npm run gates` 第一步就红：

    src/features/flashcards/FlashcardEditView.vue(208,29): error TS2307:
      Cannot find module '../../composables/useApiError'
    src/features/flashcards/FlashcardListView.vue(81,29): error TS2307: 同上

BUG-O 的提交 `942a379` 引入了一个**从未创建**的模块
`frontend/src/composables/useApiError`，以及两个**从未存在**的 i18n 键
`errors.loadFlashcardsFailed` / `errors.saveFailed`（9 个 locale 都没有
`errors` 顶层命名空间，实际是 `flashcards.error.*`）。

**该提交的说明里写着「vue-tsc --noEmit exit 0；go build ./... OK」，与事实不符。**
一个从未跑过的验证被写成了通过 —— 这就是它能进 main 的直接原因。

修法（`f2f5872`）：补纯函数 `composables/api-error-message.ts` +
组合式函数 `composables/useApiError.ts`；调用点改用既有 `flashcards.error.*`
命名空间；`saveFailed` 补齐 9 语言；8 个单测。**typecheck EXIT=0。**

## BUG-V：WS 目标地址没有判据，配错就进入停不下来的重连循环

`wsHttpBase().replace(/^http/, 'ws') + '/ws'` 在三种输入下产出非法 URL：
空基址 → `/ws`；`capacitor://` → scheme 非法；带尾斜杠 → `wss://h//ws`。
三种都落进 `catch -> scheduleReconnect()`，而重连**没有次数上限**
（`reconnectAttempts` 只参与退避计算，从不终止）。

修法（`22dd321`）：判据收敛为纯函数 `buildWebSocketUrl(apiBase, token)`，
不可用时返回 `null`；`connect()` 拿到 null 直接 return 并 warn，**不排重连**。
同提交修掉 `stores/opencode.ts` 的裸 `fetch('/api/opencode/…')`（BUG-J 同源）
与用 `window.location` 拼 WS 地址两处。

## BUG-W：BUG-D 的构建守卫把 `npm run gates` 自己堵死了

守卫下沉到 `vite.config.ts` 后，`gates` 的 `vite build` 步骤在没有
`.env.production` 的机器上必然抛错，后续 `test:native` / `check:vm-gaps`
永远跑不到 —— 「gates 全绿」对任何人都无法复现。

修法（`8b8e0f1` 前的 `22dd321` 后续提交）：新增 `frontend/scripts/build-gate.mjs`
只给冒烟构建打开逃生舱；`npm run build:fast` 保持受守卫保护。

## 分支裁定与删除

| 分支 | 落后 | 裁定 |
|---|---|---|
| `local/audit-fixes` | 185 | 16 项**全部**已被 main 覆盖（部分还是增强版）→ 删除 |
| `feat/harmonyos-phase-b` | 304 | 仅 `buildWebSocketUrl` 成立 → 合入后删除 |

`git push origin --delete local/audit-fixes feat/harmonyos-phase-b` 已执行。

## 本轮自曝的两个操作错误

1. 用 `git checkout <branch> -- <file>` 合分支，抹掉 main 上的
   `ListRunEvents` / `ToolCall`，`go build ./...` 立刻炸 —— 正确做法是
   `git diff main...branch -- <file> | git apply --3way`；
2. 解冲突时误删 main 上的 `TestDoRaw_FallsBackToRawBearerOn400`。

另外工作区被并发会话的 `git pull --ff-only` 回滚过一次，未提交改动全部丢失，
**共享工作区里必须分批提交**。

## 验证

    cd frontend && npm run gates
    # typecheck 0 错误 / build ✓ / test:native 36/36 / check:vm-gaps 0
    node --test src/api/websocket-url.test.ts src/composables/api-error-message.test.ts
    # tests 15 / pass 15 / fail 0
    cd backend && go build ./...   # EXIT=0
## BUG-P / Q / S / T：可达性与「修好一层露出下一层」（2026-09-30 第三轮）

前一轮把闪卡从零状态修到可用（BUG-K/L/M/N/O），但**用户仍然可能根本用不到它**。
这一轮的四个缺陷全部不在后端，全部是「用户视角的最后一公里」。

| 编号 | 现象 | 根因 | 修法 | 回归锁 |
|---|---|---|---|---|
| **BUG-P** | 闪卡模块修好三轮，用户从 UI 进不去 | `MoreHubView.vue` 的 `mainFeatures` 里根本没有闪卡入口 | 补入口 + 补 `nav.flashcards` 键 | `cdp-more-hub.mjs` 真机复验 |
| **BUG-Q** | 「定时自动化」点进去是空白 | 入口写 `/scheduled-tasks`，路由表里只有 `/settings/scheduled-tasks` | 改成真实路径 | `audit-route-render.mjs` + 入口静态对账 |
| **BUG-S** | 闪卡统计/浏览器整页白屏 | vue-i18n v2 的字面量插值 `{{name}}` 残留在 9 语言里，编译期直接抛 | 全部改回 v3 的 `{name}`，补齐 10 个必崩键 | `audit-i18n-compile.mjs`（带参调用，9/9 语言 0 失败） |
| **BUG-T** | 闪卡统计页显示 `t('flashcards.stats.retentionHint', { again:` —— **用户看到的是 JS 源码** | `StatsView.vue` 模板里那行漏了 `{{ }}` 包裹 | 补上 `{{ }}` | `audit-vue-mustache.mjs`（173 个 .vue，0 命中）+ `verify-audit-detects.mjs` 元验证 |

**BUG-S 的返工值得单记**：修了两轮才修对。第一轮把键名改对了，但**只提交没改工作区**——
`vite build` 读的是工作区，APK 里还是坏版本，真机 `appHTMLLen` 纹丝不动（371）。
第二轮才找准根因不是键名而是**插值语法残留**。

**BUG-T 是 BUG-S 修好后自己露出来的**：白屏消失后，那行漏 `{{ }}` 的模板第一次有了显示机会。
所以「确认不崩了」之后必须**再看一眼页面内容**，不能只看控制台有没有报错。

### 可达性沉淀：两道互补的检查，都不能替代

- **入口静态对账**（MoreHubView 的 `to` vs 路由表）→ 抓「没入口」+「入口指向虚空」（BUG-P / BUG-Q）
- **逐路由渲染验证**（`audit-route-render.mjs`）→ 抓「路由存在但白屏」（BUG-S）

静态对账看不见「页面渲染失败」，动态验证看不见「没有入口」。

---

## 追加定性：上一轮报的 6 条「路由未验证」是被污染的测量，不是缺陷

`audit-route-render.mjs` 上一轮报 31/37，6 条 HASH_MISMATCH
（`/agents`→`#/ai`、`/gateway`→`#/ai-chat`、`/servers`→`#/study` 等）。

**不猜，直接读时间线**：新增 `scripts/probe-route-redirect.mjs`，设置 hash 后每 200ms
连采 25 次（5 秒），记录变化时刻并挂 `frameNavigated` / `exceptionThrown` / `console`。
单看终态无法区分「守卫 redirect」「异步跳转」「渲染进程重载」「外部干扰」这四种成因。

| 轮次 | 环境 | 结果 |
|---|---|---|
| 上一轮 13:33 | 并发会话同时在驱同一台真机 | 31/37 |
| 独立探针 15:10（复跑两次一致） | 隔离 | 嫌疑 6/6 STABLE，对照 2/2 STABLE |
| 隔离重跑审计 15:17 | 隔离 | **37/37** |

6 条全部**首次采样（~300ms）就命中目标，之后 5 秒一次都没漂**，无任何异常事件。
隔离重跑里这 6 条的 `len` 与独立探针的采样值**逐个一致**（26712 / 277 / 159 / 264 /
167 / 421 / 176 / 269 / 82），说明判据本身稳定。

**6 条落点无一例外全是 BottomNav 的 tab 路由，每次都不一样。** 脚本只写
`location.hash`、从不派发点击，纯哈希赋值不可能「点到」底栏 —— 那一刻一定有人在点屏幕。
（**标记为假设，未直接取证**：没抓到并发会话的操作日志。）

**沉淀为硬判据**：疑似缺陷必须能复现两次以上、且在隔离环境下复现，才允许写进缺陷列表。
一次性的观测只能进「待复查」。本轮同一个坑翻了三次（17 条 → 6 条 → 0 条），
三次都是设备被抢占造成的假故障。

---

## BUG-R：会议 ID 撞车导致静默丢数据（挂了两轮的"测试污染"其实是真缺陷）

### 起因

`TestMeetingWorkspaceIsolation/list_A` 被前几轮记为「测试间状态污染 / 预先存在，
未定性」。本轮去查，第一件事是**拿到真实错误信息**：

```
--- FAIL: TestMeetingWorkspaceIsolation/list_A
    workspace_isolation_test.go:116: list total/items=0/0, want 1
workspace_isolation_test.go:129: cross-workspace meeting GET status=200
    body={"id":"mtg_...","workspace_id":"ws-b","title":"workspace B meeting",...}
```

第 129 行是关键：**用 `meetingA.ID` 去请求，却返回了 ws-b 那条会议**。
这不是「过滤太严」，是**返回了错误的对象**。

### 根因

`internal/meeting/store.go:66` —— ID 是纯墙钟纳秒时间戳，直接当 map key：

```go
now := time.Now()
ID: fmt.Sprintf("mtg_%d", now.UnixNano()),
...
s.meetings[m.ID] = m
```

两次创建落在同一时钟刻度 → ID 相同 → 后者覆盖前者，前者被静默抹掉。
「单跑 PASS、全跑 FAIL」与测试顺序**无关**，是**概率**。

### 决定性实验

新增 `internal/meeting/id_collision_diag_test.go`：

```
连续创建 200 条，唯一 ID 6 个，重复 194 次
store 里实际存了 6 条（期望 200）
1000 次 time.Now() 产生 1 个不同值，最小间隔 0ns
```

**本机 `time.Now()` 在 1000 次紧邻调用里只产生 1 个不同值** —— 纳秒时间戳
在这台机器上没有纳秒精度。**194/200 条会议被静默丢弃，且创建全部返回成功。**

### 修法

沿用仓库里 `finance.Store` / `chat_summary.Store` 已有的「时间戳 + 原子序号」：

```go
var meetingIDSeq atomic.Uint64
ID: fmt.Sprintf("mtg_%d_%d", now.UnixNano(), meetingIDSeq.Add(1)),
```

修后：200 次创建 → 200 个唯一 ID、0 碰撞、200 条全部落库。

### 回归

`go test ./internal/server/ -count=1` 从 `FAIL` 转为 `ok 2.906s`，
**挂了两轮的 `list_A` 首次全绿**。`go build ./...` OK、
`go vet` 5 包 OK、`meeting`/`presentation`/`notifycenter`/`server` 四包全绿。

### 同类站点：4 处一起修

全仓扫 `UnixNano()` 找 ID 生成点。**已带唯一性成分的未动**：`finance`、
`chat_summary`、`email/store.go`、`redclaw/audit.go`、
`opencode/session_event_broadcaster.go`、`flashcards/cards.go`、
`scheduledtask`（后两者用 crypto/rand）。

**无唯一性成分、已修**（除 meeting 外均属**预防性**，未观测到实际失败）：

| 站点 | 原 ID | 危害路径 |
|---|---|---|
| `internal/email/invoice_store.go` | `inv_%d` | PG 主键冲突 → 整批 upsert 失败 |
| `internal/notifycenter/service.go` | `%s_%d` | PG 主键冲突 → 丢通知行 |
| `internal/presentation/generator.go` | `pres_%d` | ID 重复，按 ID 查找取错对象 |
| `internal/server/server_assistant.go` | `%s-%d` | 原注释「纳秒级时间戳足够避免冲突」**是错的**，已改写 |

`marketplace` 的 `releaseID`/`installID` 带版本/渠道/工作区前缀，本轮**未处理**。

### 回归锁

- `id_collision_diag_test.go`：200 次创建必须 200 个唯一 ID、store 存满 200 条
- `TestUnixNanoResolution` 改为只记录不判定（测的是平台特性，不是产品行为）
- `workspace_isolation_test.go` 本身即端到端回归锁

### 既有失败的定性（本轮一并澄清）

- `internal/email` 剩 2 个 FAIL：**已用 `git worktree` 在 HEAD 上同条件复现，
  错误信息一致 → 预先存在，非本轮回归**（我改过同包的 `invoice_store.go`，
  所以这一条必须实证）。其中 `TestWriteKeyAtomic_CreatesFileWithCorrectMode`
  是 POSIX 文件权限位测试，Windows 上不成立。
- `internal/agent` 的 FAIL 全部是 Windows 平台问题：
  `fork/exec ...fake-pi.sh: %1 is not a valid Win32 application`、
  无扩展名可执行文件 —— 测试假设 POSIX shell。**预先存在，未修。**

---

## BUG-U：零卡组时「新建卡片」是条死胡同

### 缺陷

`/flashcards` 的空态原本只有一个「新建卡片」按钮，跳 `/flashcards/new`（卡片编辑页）。
但那页的「保存」在**没有卡组时恒 disabled**（`selectedDeckId` 为空 → `isValid` false）。
用户点进去才发现要先建组，而建组入口是**那页顶部的另一个输入框**。

**从零状态看这是一个死胡同**：唯一的 CTA 指向一个必然无法完成任务的页面。

（BUG-K 修的是「卡组页有建组入口」，BUG-U 修的是「从列表页能不能走出来」，盲区不同。）

### 修法

`FlashcardListView.vue` 空态改为**就地内联建组**，走 `store.createDeck()`，
建完空态自动消失。**故意不保留「新建卡片」按钮** —— 摆一个点了必然失败、
又不解释原因的按钮比不放更糟。

同时给视图加了稳定的 `data-testid`（`flashcards-empty` / `deck-create-form` /
`flashcards-deck-item`）。

### 真机验证（scripts/verify-bug-u.mjs，13/13，连跑三轮稳定）

```
PASS  前置：服务端 deck 数为 0            — PG 实际 0
PASS  空态容器出现（可见 pane 内）        — bodyLen=23 deckItems=0
PASS  内联建组表单存在
PASS  建组输入框存在                      — placeholder=卡组名称
PASS  提交按钮存在且初始 disabled          — text=新建卡组
PASS  列表加载完成（loading 态消失后才交互）
PASS  填值回读一致                        — readBack=BUGU-ZEROSTATE-DECK
PASS  填名后提交按钮变为可用              — disabled=false
PASS  空态在提交后消失
PASS  建组表单在提交后消失
PASS  卡组条目节点出现（不是 innerText 碰巧含名字）— items=["BUGU-ZEROSTATE-DECK"]
PASS  PG 落库（不信 UI，不信 localStorage） — PG deck 数=1
PASS  无未捕获 JS 异常
```

落库判据**直接查 PG**。验证前置需清空闪卡表（dev 库，E2E 丢弃数据）。

### 这一节的重点其实是验证脚本翻了三次车

功能第一次跑就通了，**翻车的是判据**：

1. **`.empty` 类名撞车** —— 徽章是 `<span class="badge empty">`，
   `querySelector('.empty')` 拿到的是徽章不是空态容器，
   「空态是否消失」这条判据**构造上就永远失败**。
   → 验收钩子不许建在样式类上，改用 `data-testid`。
2. **`FoldAwareLayout` 故意同时渲染 `#outer`/`#inner` 两个 slot**（靠 CSS 隐藏其一），
   `querySelectorAll` 一次拿到两份。
   → 所有 DOM 查询限定在**可见 pane**（`offsetParent !== null`）。
   这是本仓库折叠屏页面的**通用陷阱**。
3. **在 `store.refresh()` 还在飞行时填值** —— refresh 期间
   `v-if="store.loading"` 把表单整个卸载，填的值和随后重新挂载的按钮是
   **两个不同节点**。表现是「回读成功但按钮永远 disabled」的自相矛盾假 FAIL。
   → **等状态达到期望**（轮询 loading 态消失）再交互，而不是等够时间。

附带两个环境坑：`psql -c` 里的中文兜底串会经 ANSI 码页报
`invalid byte sequence for encoding UTF8`（必须纯 ASCII）；
服务端清空后本地 `flashcards:v1` 缓存仍会把旧卡组灌回来，验证前必须
`localStorage.removeItem` + `Page.reload`。

### 稳定性声明

修好判据后**连跑三轮都是 13/13**；修之前同一脚本是 12/13。
如实记录：这条判据**曾经 flaky**（坑三导致），三轮稳定才敢下结论。

---

## BUG-Y：「安装」对没先点过「查看版本」的包必然失败

### 现象

`/marketplace/skills`：点「安装」→ 确认弹窗正常弹出 → 点「确认安装」→
**`marketplace_installations` 表 0 → 0**，控制台 0 异常。

### 根因

`SkillMarketView.vue` 的 `runInstall()` 直接读 `expanded`：

```ts
const versions = expanded.value[installTarget.value.package_id]
const publishedVersion = versions?.find((v) => v.status === 'published')
if (!publishedVersion) { store.error = '该包尚无已发布版本，无法安装。'; ... }
```

`expanded` 是**纯 UI 展开状态**，只有点过「查看版本」才有值。
没点过 → `undefined` → 报「该包尚无已发布版本」。
**而那个包确实有已发布版本**（versions 接口明确返回 `status: "published"`）——
不只是功能坏，**提示还与事实相反**。

### 最扎眼的一点

`AgentMarketView.vue` 与 `WorkbuddyView.vue` 的 `runInstall` **本来就有**
`ensureVersionsLoaded()` 按需加载版本。**同一段逻辑写了三遍，只有这一遍是错的。**
修法因此不是发明新方案，而是改成和另外两个一样的写法。

顺带修掉同处第二个问题：`await store.install(...)` **忽略返回值**，
而 `store.install` 失败时返回 `null` —— 后端拒绝安装也是完全静默的。

### 真机验证（12/12，连跑两轮稳定）

    API 播种 submit/review/publish      201/200/201
    App 与 API 同一 workspace          ws_user-admin == ws_user-admin
    技能市场渲染包卡片                  articles=7
    安装确认弹窗出现
    UI 点击后 PG 落库                  安装前=0 安装后=1
    落库的是刚播种的包（关联核对）      命中=1
    对照组：重复安装不新增行            唯一索引挡住

## 顺带查清的三件事（都不是产品缺陷，但都曾差点被当成缺陷）

1. **`submit` 忽略客户端传的 `package_id`**，后端自己从 workspace+name+version 推导。
   同名同版本重复提交撞唯一约束 —— ⚠️ **但被返回成 500**，不是 409/400。
   与已修的 BUG-M 同一类，**本轮未修**。
2. **App 与 API 可能在两个不同 workspace**（App 持 `default`，API 给 `ws_user-admin`），
   市场按 workspace 隔离 → 「后端返回了包、UI 却说暂无」。
   差点被当成前端缺陷。因此验证脚本加了**前置判据**：workspace 不一致直接中止。
3. 我自己的四条脚本级错误：`publish` 判据写死 200（实为 201）；
   `clickByText('登录')` 用 indexOf 匹配到「密码登录」那个 tab；
   登录按钮 disabled 是计算属性（需轮询等 enabled）；
   页面内裸 `fetch('/api/...')` 返回 HTML（没加 API base 前缀，与 BUG-J 同源）。

---

## BUG-Z：重复提交同名同版本被归成 500（与 BUG-M 同类）

**现象**：POST /api/marketplace/submit 同 workspace 对同名包重复提交同一版本号 →
**500** + 原始文案 duplicate key ... marketplace_versions_pkey (SQLSTATE 23505)。

**根因**：Submit 里 INSERT INTO marketplace_versions 的错误原样返回，
既不是 ErrMarketplaceNotFound 也不是 ErrMarketplaceConflict，
落到 writeMarketplaceError 的 default 分支 → 500。
该函数本来就有 ErrMarketplaceConflict → 409 的映射，只是没被触发。

**为什么不该是 500**：换个版本号就能继续，是客户端可纠正的输入冲突；
且前端 ApiError.retryable 会把 5xx 当可重试**反复重试**。与已修的 BUG-M 同一类。

**修法**：signing.go 的 RegisterPublisherKey 早有同样的惯用法
（pgconn.PgError code 23505 → ErrMarketplaceConflict）。抽成共用
wrapUniqueViolation，三处裸 INSERT 接上：Submit 的 packages/versions、Publish 的 releases。
（Install 本来就有 ON CONFLICT DO NOTHING + 回查，幂等，不受影响。）

**验证**：

    Go 回归 3/3（含对照组 + 助手透传测试）
    证伪：revert-bugz.mjs 回退修复后测试如期失败，报的正是原始 23505
    端到端 verify-bug-z.mjs 4/4：201 / 409 / 文案不泄漏 23505 / 换版本号仍 201
    go build ./... OK；go vet OK；marketplace ok 15.756s；server ok 14.037s

**顺带更正上一轮的一处错误定性**：「submit 忽略客户端 package_id」**不是缺陷，
是刻意的反伪造设计** —— server_marketplace.go:231-237 的注释写明
「绝不信任 body 中的同名字段，否则 other-ws/some-pkg 会污染本 workspace 命名空间」。

**教训**：看到「后端忽略了客户端传的字段」**先读那段代码的注释**，
注释里往往直接写着为什么。本轮差点把一个安全决策当成 bug 报出去。


---

## BUG-AA：闪卡「建卡组」CTA 实际跳新建卡片页（两个实例，BUG-K 只修对 2/9 语言）

**缺陷一 · BUG-K 的修复只覆盖 2/9 语言**：`FlashcardListView` 主 CTA 走
`goCreate()` → `/flashcards/new`（**新建卡片**页）。而 `flashcards.list.create`
在 zh-CN / en-US 已改对，其余 **7 种语言仍是旧中文文案的直译**：
zh-TW「新增卡組」、ja-JP「デッキを作成」、ko-KR「덱 만들기」、de-DE「Stapel erstellen」、
fr-FR「Créer un paquet」、es-ES「Crear mazo」、pt-BR「Criar baralho」。
BUG-K（`0ac074b`）只动了 zh-CN / en-US。

**缺陷二 · 同一问题的第二个实例，9/9 全错**：`StudyHubView.vue` 的零卡组空态按钮
文案取 `study.decks.create`（「新建牌组 / New deck」），点击 `goCreateDeck()`
→ `router.push('/flashcards/new')`，同样是**新建卡片**页；且从零状态点进去
必然撞 BUG-U 那个死胡同（无卡组时该页「保存」恒 disabled）。BUG-K 没碰过这个组件。

**修法**：StudyHubView 改为与 FlashcardListView（BUG-U / BUG-X）**同构**的内联建组，
复用同一份已真机验证过的 `store.createDeck`（该组件本就已 useFlashcardsStore），
建完 `decks` computed 立刻更新。验收钩子一律 `data-testid` —— 该文件同时存在
`div.empty` 和 `span.deck-badge.empty`，用类名会撞车。
7 种语言的 `flashcards.list.create` 用**定点字符串替换**修正（JSON 重写会重排格式、
产生几百行假 diff），替换前校验旧值、替换后重新 parse、断言
`flashcards.deck.create` 与基线逐字节相同，可重复执行。

**审计脚本 `scripts/audit-deck-cta-i18n.mjs`（判据自证 6/6）**：
第一版判据「两个 CTA 字面必须不同」**被证伪推翻** —— 在 `origin/main` 上只报出 **1/7**，
因为其余 6 种语言字面不同但语义相同。重做为：A2 对照**人工审定的黄金译文表**
（在 origin/main 上报出 **7/7**）+ B 元素粒度判定「指向新建卡片页的可点击元素，
其文案不得等于建卡组文案」。
判据 B 第一版也错了：扫整个文件的所有 `t()`，把 `flashcards.deck.addCard`
（=「添加卡片」，键名带 deck 但语义是**建卡片**、且导航到新卡片页是**正确的**）误报。
**键名不是判据，值比对才语言无关。**

**本轮未修**：`study.decks.*` 整块 7 个键在 7 种语言里与 en-US 逐字节相同（整块英文未翻译），
审计判据 C 只报不拦。42 条译文需逐条审，不宜混进同一次提交。
另外 `study.decks.create` 因本次改动已成为**死键**。

**未验证**：StudyHubView 的内联建组**未在真机/模拟器上跑过**，只有静态修复 + 类型检查 +
i18n 测试（`vue-tsc --noEmit` exit 0；locale 测试 17/17）。不要外推 BUG-X 的真机 13/13。

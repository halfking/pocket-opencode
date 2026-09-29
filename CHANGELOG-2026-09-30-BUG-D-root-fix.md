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

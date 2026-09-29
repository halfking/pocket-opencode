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

## 4. 已验证 / 未验证（严禁外推）

### ✅ 已验证（有证据）
- BUG-D 构建守卫、typecheck 通过
- BUG-E i18n 292/292 对等，底栏英文 `RSS`
- BUG-F 修复机制在**模拟器**上完整生效：origin 降级、Mixed Content 归零、**WebSocket connected**、登录 200、主密码创建、`/ai` 实时数据
- 后端 CORS / WS origin 校验对 `http://localhost` 均放行

### ❌ 未验证（下一轮必须补）
- **真机 Redmi（4c308e2e）上的 BUG-F 复验**。新 APK 已装上、origin 确认为 `http://localhost`、Mixed Content 归零，
  但当时设备到宿主 `192.168.31.20` **100% 丢包**，fetch 报 `Failed to fetch`，未能完成登录与 WS 握手。
  *结论只能写「真机 scheme 已生效」，不能写「真机端到端已通过」。*
- 8 个本地模块（笔记/邮箱/财务/密码箱/闪卡/PKM/本地智能体/市场）的**写操作**
- 5 个抽屉模块（费用配额/网关/实例/任务/会话）
- 生产 scheme（`https`）下的真机行为未回归验证

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
- 确认 XHR 是否真的到达后端：看 App 错误文案分支。`LoginView.vue:522-529` 的「登录失败：用户名或密码错误」
  **只在 `e instanceof ApiError && e.status === 401` 时出现**；网络失败会走 `e.message`（`Failed to fetch`）。
  看到这个中文，就说明请求往返成功、只差凭据。
- 后端侧权威信号：`logs/pocketd.err.log` 的 `WebSocket client connected: <ip> (total: 1)`

### 安全事项
`backend/internal/server/server_assistant.go:200-217` 的 dev 旁路在 `POCKET_AUTH_PASS` 未设置时
会回落到**源码内置的默认口令**。仅 dev 可用，但生产务必显式设置。文档与报告中不要回显该口令。

---

## 6. 建议的下一轮起手式

1. 确认无并发会话；真机与宿主恢复到同一网段（先 `ping 192.168.31.20` 通过再开工）
2. `set CAP_ANDROID_SCHEME=http` → `cap sync` → `gradlew assembleDebug` → **校验 APK 内 scheme** → 装真机
3. 真机走一遍：登录 → 主密码 → `/ai` → 确认 logcat 出现 `WebSocket connected` 且无 `Reconnecting`
4. 补 8 个本地模块 + 5 个抽屉模块的**写操作**验证（目前只有读/渲染）
5. 真机通过后，再回归一次默认 `https` 构建，确认生产路径没被降级影响

# 04 — Hybrid 壳与 Android 构建

> 现行约束。openpocket 的壳与参考仓**形态不同**，本文说明为什么不同，以及构建链怎么走。

## 1. 打包形态：与参考仓的差异（重要）

| | 参考仓（nbjl3） | openpocket |
| --- | --- | --- |
| 壳内打包物 | **只有**一个引导页（~8.7KB，零依赖） | **整个构建产物** `dist` |
| 业务前端来源 | 远端 origin（`location.replace(origin + '/')`） | 本地 assets |
| API 地址 | 用户在引导页填写 | 构建期 `VITE_API_BASE` 注入 |
| appId | `com.kaixuan.nbjl` | `com.kaixuan.opencode.pocket` |
| 原生插件 | 模板（无业务插件） | **8 个自研插件**（见 §3） |

因此参考仓 AGENTS §8.2 的两条红线 ——「壳内不打包业务前端」「壳层零凭据」——
**对 openpocket 不适用**，本专题不继承。理由是 openpocket 已有深度耦合的原生能力
（后台录音前台服务、AI 流前台服务、本地 agent），改远端形态会失去这些能力与免发版
优势。这是 2026-10-04 明确锁定的决策。

## 2. Capacitor 配置要点

`frontend/capacitor.config.ts`：

- **不设 `server.url`**。设了会让 WebView 加载远程站点而非本地打包资源。
- `android.allowMixedContent: true`；`androidScheme` 由 `CAP_ANDROID_SCHEME`
  环境变量控制，默认 `https`。本地/内网 HTTP 后端联调用 `CAP_ANDROID_SCHEME=http`，
  否则 WebSocket 会被 Chromium 的 Insecure WebSocket 策略硬阻断
  （控制台报 insecure endpoint，随后无限重连，真机上表现为流式输出全失效）。
- `SystemBars.insetsHandling: 'disable'` —— Capacitor 8 的默认注入脚本在
  Chromium < 140 上会抛 `Cannot read properties of null`，干扰 Vue mount 导致
  全局点击失效。改由 `MainActivity.injectSafeInsets()` 独家注入
  `--android-safe-top`。
- `SplashScreen.launchShowDuration: 0` + `launchAutoHide: false`，
  由 `main.ts` 在首帧后主动 fade。

## 3. 原生插件与 JS 桥的对应

插件名靠 `registerPlugin('X')` 对齐 —— **改名字要两边一起改**，否则调用方
会静默退回 `getUserMedia` 之类的 Web 方案（例如切后台即断录）。

| 插件 | 职责 | Android 侧要求 |
| --- | --- | --- |
| `BackgroundMic` | 后台录音 | 必须声明 `foregroundServiceType="microphone"` + `FOREGROUND_SERVICE_MICROPHONE`，否则切后台被系统掐断 |
| `AiStreamService` / `AiStreamKeepalive` | AI 流 dataSync 前台服务 + 保活 | 通知常驻 |
| `EmailFetch` / `EmailFetchReceiver` | 邮件周期抓取 | WorkManager |
| `BiometricAuth` | 生物识别解锁 | Keystore |
| `Sherpa` | 本地语音 | 模型包随 APK 或受控下载 |
| `AppSettings` | 原生设置读写 | — |

已注册插件（`cap sync` 实际识别 10 个）：`sqlite` / `text-to-speech` / `app` /
`camera` / `filesystem` / `haptics` / `local-notifications` / `share` /
`splash-screen` / `status-bar`。

## 4. Android 构建链

### 4.1 工具链（本机实测）

| 项 | 值 |
| --- | --- |
| JDK | 21.0.6（`JAVA_HOME` 已设） |
| Gradle | 8.14.3（wrapper） |
| compileSdk | 36（`android-36` 已装） |
| targetSdk / minSdk | 35 / 24 |
| build-tools | 35.0.0、36.1.0 |
| `local.properties` | `sdk.dir=/Users/xutaohuang/Library/Android/sdk` |

### 4.2 唯一认可的构建路径

```bash
cd frontend
node scripts/build-mobile.mjs android dev     # vite build + cap sync android
cd android && ./gradlew assembleDebug
```

**不要直接 `npm run build`。** 该路径在本仓会被
`assert-no-plaintext-backend.mjs` / vite 插件拒绝：移动端 bundle 缺少
`VITE_API_BASE` 时，App 会静默回落到 WebView 同源（`https://localhost`），
所有 `/api` 请求返回本地 `index.html` 而不是 JSON。这是 2026-09-05 的真机事故。

可用环境档案：`.env.android-dev`（当前 `http://192.168.31.37:8090`）、
`.env.ios-dev`、`.env.staging`、`.env.production`。改 LAN IP 时覆盖
`VITE_API_BASE=http://<host>:<port>`。

> ⚠️ `build-mobile.mjs` 会在构建后**核对产物身份**（locale 接线、原生版本读取、
> 回退常量、编译期构建时刻，以及 API base 是否真的进了 chunk）。
> 这一步的存在是因为「构建失败但 `cap sync` 仍把**上一次的** `dist` 拷进
> android assets」是真实可发生的 —— 此时 APK 看起来构建成功，内容却是旧的。

### 4.3 验证命令

```bash
node scripts/build-mobile.mjs android dev   # 含产物身份自检
./gradlew assembleDebug
ls -la app/build/outputs/apk/debug/
```

本轮实测结果见 [09-审计与实施路线](./09-审计与实施路线.md) §5。

## 5. 禁止事项

1. 把 Hybrid 壳做成「离线打包整个 dist 的 App」—— **openpocket 本来就是这样**，
   这条禁令不适用于本仓（它继承自参考仓的远端形态）。改为：不要在壳里放
   第二套业务逻辑。
2. 在原生层存 Provider API Key、做第二套登录 UI。凭据只在既有登录 API 与
   后端 secret store。
3. 用 Web Worker / 后台定时器宣称「任意 iOS/Android 任务永久保活」。
4. 跳过 `build-mobile.mjs` 直接 `npm run build`（见 §4.2）。
5. 在原生层做金额计算或最终财务写入。

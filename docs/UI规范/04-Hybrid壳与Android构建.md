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

可用环境档案：`.env.android-dev`、`.env.ios-dev`、`.env.staging`、
`.env.production`。改 LAN IP 时覆盖 `VITE_API_BASE=http://<host>:<port>`。

> ⚠️ **2026-10-04 实吃：`.env.android-dev` 里的 `192.168.31.37` 已失效。**
> 本机 LAN IP 变成了 `192.168.31.34`，而 `.37` 那台不再监听
> （`curl` 全 000）。但 `build-mobile.mjs` 原有的产物身份自检
> **只验「API base 字符串在 chunk 里」**——一个写死、已失效的 IP
> 照样绿灯打进 APK。它自己的注释写着「fail loudly instead of silently
> shipping a build pointing at the wrong server」，而它能防的只有
> 「值写错」，防不住「值没变、机器没了」。

### 4.1b 后端可达性守卫（2026-10-04 新增）

`build-mobile.mjs` 在 vite build **之前**加了一次 TCP 连通性探针：

```bash
# 死地址 → 判红并退出 1（不再花掉一次完整构建）
VITE_API_BASE=http://192.168.31.37:8090 node scripts/build-mobile.mjs android dev
# [build-mobile] refusing to build android/dev: API base is UNREACHABLE
# [build-mobile]   tcp 192.168.31.37:8090 → ECONNREFUSED

# 确需离线出包 → 显式放行，且在日志留痕
MOBILE_SKIP_REACHABILITY=1 node scripts/build-mobile.mjs android dev
```

dev 与 production 都默认判红（两者后果都是「打出去的包连不上任何后端」），
逃生舱 `MOBILE_SKIP_REACHABILITY=1` 与既有的 `MOBILE_ALLOW_EMPTY_API_BASE`
同一套「要显式、要留痕」的约定。

守卫自带自检（已接进 `gates.json` 的 `check:build-mobile-selftest`，CI 会跑）：

```bash
node scripts/build-mobile.mjs --selftest    # 不构建、不需要设备
#   🟢 活端口必须可达        got=true  want=true
#   🟢 死端口必须不可达      got=false want=false（ECONNREFUSED）
#   ⚠️ 观测（不断言）：不可解析主机名 → connected
```

判据的牙（两点结论相反即证明分支真实）：

| 输入 | 结果 |
| --- | --- |
| `192.168.31.37:8090`（ECONNREFUSED） | 🔴 退出码 **1** |
| `192.168.31.34:8090`（本机在跑） | 🟢 `reachability OK` |
| `MOBILE_SKIP_REACHABILITY=1` | 🟢 出包 + 显式 WARNING |

⚠️ **这条守卫防得住什么、防不住什么（必须说清）**：

- **防得住** —— 最常见的一类：LAN IP 变了、写死的机器下线了、端口没人听。
- **防不住** —— 「连得上，但那不是我们的服务」。**TCP 连通 ≠ 服务可用**。
  2026-10-04 本机实测：`192.0.2.1:9`（RFC 5737 黑洞地址）与
  `no-such-host.invalid:80`（永不可解析）**都返回 `connected`**，
  而 `192.168.31.37:8090`（LAN 上没人听）正确报 `ECONNREFUSED`
  ⇒ 本机存在拦截**非 LAN** 出站 TCP 的透明代理/端口转发。

  所以自检里第三条是**观测而不是断言**：写成断言会得到一条「永远红」的用例，
  那不是判据是噪音。守卫通过时也会打一行提示，让人知道真机联调前应另跑
  `curl -sS <API base>/healthz`。

### 4.1c 真机联调的两个必设变量

| 变量 | 为什么必设 |
| --- | --- |
| `POCKET_ALLOW_PLAINTEXT_API=1` | `assert-no-plaintext-backend.mjs`（BUG-F）拒绝「非本机的明文 http 后端」。真机联调必然是内网 http，守卫的报错信息里就写明了修法 |
| `CAP_ANDROID_SCHEME=http` | 见 `capacitor.config.ts` 的 BUG-F 注释：默认 `https` 时页面 origin = `https://localhost`，**XHR 虽被 mixed-content mode 放行，但 `ws://` 会被 Chromium ≥111 硬阻断**（无限重连）。改成 `http` 后两者同为非安全上下文，规则不再适用 |

> ⚠️ 漏掉 `CAP_ANDROID_SCHEME=http` 的症状是**白屏或登录失败**，很容易被
> 误判成「包坏了」。判别动作：logcat 里 `Handling local request: http://localhost/...`
> ⇒ scheme 已生效；仍是 `https://localhost/` ⇒ 没设。

> ⚠️ `build-mobile.mjs` 会在构建后**核对产物身份**（locale 接线、原生版本读取、
> 回退常量、编译期构建时刻，以及 API base 是否真的进了 chunk）。
> 这一步的存在是因为「构建失败但 `cap sync` 仍把**上一次的** `dist` 拷进
> android assets」是真实可发生的 —— 此时 APK 看起来构建成功，内容却是旧的。

### 4.3 验证命令

```bash
node scripts/build-mobile.mjs android dev   # 含产物身份自检 + 可达性守卫
./gradlew assembleDebug
ls -la app/build/outputs/apk/debug/
```

⚠️ **`assembleDebug` 默认产出的是**正式包** `com.kaixuan.opencode.pocket`。**
真机上直接 `adb install -r` 会覆盖用户已装的正式包并**清掉它的数据**。
旁挂包（与正式包共存、数据互不干扰）：

```bash
./gradlew assembleDebug -PsttDevApp     # → com.kaixuan.opencode.pocket.sttdev
```

### 4.1d 三个 applicationId 变体（2026-10-04 新增，零数据损失）

**走受认可的构建路径**（不要直接调 `gradlew`，会绕过产物身份自检）：

```bash
node scripts/build-mobile.mjs android dev --sttdev                            # …pocket.sttdev
MOBILE_APP_ID_SUFFIX=.matrix node scripts/build-mobile.mjs android dev --sttdev # …pocket.matrix
./gradlew assembleDebug                        # com.kaixuan.opencode.pocket  （正式包，仅 release 用）
```

⚠️ **`--sttdev` 是必须的**：`build-mobile.mjs` 只在 `sttdev` 变体下才跑
gradle 并做 applicationId 自检（实测 `android dev` **不构建 APK**，只做
vite build + cap sync 就返回 OK）。不加它就等于跳过了产物身份验证。

**为什么需要第三个。** 2026-10-04 实测：设备上已有的 `.sttdev` 是**另一台机器**的
debug keystore 签的（证书 `9a06f76f…` vs 本机 `eac58170…`），
`adb install -r` 报 `INSTALL_FAILED_UPDATE_INCOMPATIBLE`。
此时旧解法只有**卸载**——而那会清掉该包的登录态与本地库。
**换 applicationId 就不必卸载任何东西**：三者并存、数据互不干扰。

自证（三条路径各构建一次，读 `output-metadata.json`）：

| 构建 | applicationId |
| --- | --- |
| `assembleDebug`（默认） | `com.kaixuan.opencode.pocket` |
| `--sttdev` | `…pocket.sttdev` |
| `--sttdev` + `MOBILE_APP_ID_SUFFIX=.matrix` | `…pocket.matrix` |

不传 `MOBILE_APP_ID_SUFFIX` 时代码会打一行 `appIdSuffix 未设置` 的
`lifecycle` 日志自证走的是默认分支——**加了新分支之后必须证明旧行为没变**。

断言的牙（变异验证）：把 `build.gradle` 里的
`if (project.hasProperty('appIdSuffix'))` 改成 `if (false)`（模拟 gradle
悄悄丢掉分支）⇒ gradle 退出 0 并静默产出**正式包**
`com.kaixuan.opencode.pocket` ⇒ 自检当场判红、**退出码 1**，并打印
「若产出的是 MAIN package，安装会覆盖用户手机上的正式包」。

⚠️ 这个变异暴露了一件更要紧的事：**断言必须拿「实际请求的后缀」比**，
写死 `.sttdev` 的话，请求 `.matrix` 却产出 `.sttdev`（或反过来）都会
「验证通过」——**断言在说谎**。已改成 `appIdSuffix` 变量驱动。

> 顺带记一个入口陷阱：我第一次测时用了 `android dev`（**不带** `--sttdev`），
> 变异后退出码仍是 0，看着像「断言没牙」。真因是那个入口**根本不跑 gradle**，
> 于是产物根本没变。**判别动作**：变异后先去确认「被验对象真的重新生成了吗」，
> 再判定断言有没有牙。

⚠️ 换成第三个包名**绕不开厂商策略**：`adb install` 与 `pm install`
在真机上都返回 `INSTALL_FAILED_USER_RESTRICTED`（小米「USB 安装」按应用授权，
不在标准 `settings` 里——`install_non_market_apps=1`、
`adb_install_need_confirm=0` 全是放开的，仍然被拦）。
⇒ 这是**需要人在手机上操作**的门槛，不是命令行能清的。

产物指纹核验（**按内容定位 chunk，不要 `head -1` 猜文件名**——包里有 100+ 个
`index-*.js`）：

```bash
CH=$(for f in dist/assets/*.js; do grep -q 'getElementById("app")' "$f" && basename "$f"; done | head -1)
unzip -p app-debug.apk "assets/public/assets/$CH" | shasum -a 256
shasum -a 256 "dist/assets/$CH"
```

⚠️ `npm run gates` 里的 `build:gate` 会跑 `vite build` **覆盖 `dist/`**。
构建完必须**立刻**取证，否则你比的是上一轮构建的产物。
（本轮真踩到：15:21 打的包与 15:24 被 gates 重建的 dist 指纹不一致，
一度以为构建没生效。）

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

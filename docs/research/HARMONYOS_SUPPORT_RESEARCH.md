# OpenCode Pocket 多端覆盖研究（Android / iOS / HarmonyOS NEXT）

> 研究日期：2026-09-01  
> 研究范围：在保留现有 Vue 3 + Capacitor + Go 后端架构的前提下，评估将项目扩展到 HarmonyOS NEXT（纯血鸿蒙）的可行方案  
> 结论摘要：**Capacitor 官方明确不会支持鸿蒙**（issue #7173 / #7818 均已关闭）。  
> 推荐路径：**保留 Android/iOS 不动；鸿蒙采用"自研 ArkTS 壳 + WebView 加载 dist/"混合方案**；所有原生能力均已有 `featureFlag` 门控，鸿蒙壳可直接降级为 Web 实现。

---

## 1. 当前项目技术栈盘点

### 1.1 前端

| 维度 | 现状 | 证据 |
|------|------|------|
| 框架 | Vue 3.5 + TypeScript + Vite 5 | `frontend/package.json` |
| 路由/状态 | vue-router 4 / pinia 2 / vue-i18n 9 | `frontend/package.json` |
| 富文本 | Tiptap 3（@tiptap/core/starter-kit/vue-3） | `frontend/package.json` |
| 原生桥接 | **Capacitor 8**（@capacitor/core/cli/android/ios） | `frontend/package.json`、`frontend/capacitor.config.ts` |
| 打包产物 | `frontend/dist/` 被 Capacitor 拷进 native 壳 | `capacitor.config.ts` `webDir: 'dist'` |
| Android 壳 | 已就绪（`frontend/android/`，Capacitor 8.5，minSdk 24 / compileSdk 36） | `frontend/android/variables.gradle` |
| iOS 壳 | 已就绪（`frontend/ios/`，`App.xcodeproj` + `CapApp-SPM`） | `frontend/ios/App/` |
| 鸿蒙壳 | **不存在** | 全仓搜索无 harmony/arkts/openharmony 关键字 |

### 1.2 后端（与端无关，不影响研究结论）

Go 1.25 后端 + PostgreSQL（可选）+ ACP Agent + WebSocket Hub + AI Gateway。后端是纯 HTTP/WS 服务，三端共用，无需改动。

### 1.3 项目实际用到的 Capacitor 插件清单

通过对 `frontend/src/**/*.ts,*.vue` 的全文检索，确认以下 6 个插件被实际使用：

| 插件包 | 用途 | 使用点 | 鸿蒙对应能力 |
|--------|------|--------|-------------|
| `@capacitor/core` | `Capacitor.isNativePlatform()`、`registerPlugin` | 几乎所有 `composables/*`、`native/*` | — |
| `@capacitor/app` | App 生命周期事件（resume/pause/url-open） | `app/AppLayout.vue`、`features/settings/SettingsPermissionsView.vue` | ArkUI `UIAbility.onCreate/onForeground` 等 |
| `@capacitor/status-bar` | 状态栏样式 | `composables/useStatusBar.ts` | ArkUI `window.setStatusBarColor` |
| `@capacitor/local-notifications` | 本地通知 | `composables/useApprovalAlerts.ts`、`useNotificationPermission.ts` | `@ohos.notificationManager` |
| `@capacitor-community/sqlite` | 离线本地 DB | `native/local-db.ts` | RDB（`@ohos.data.relationalStore`） |
| `@capacitor/camera` | 拍照/相册选择（动态 import） | `composables/useCameraCapture.ts` | `@ohos.file.picker` + camera picker |
| `@capacitor-community/text-to-speech` | **包已装，但代码侧无任何引用**（未使用） | — | — |
| 自研 `BiometricAuth` 插件 | AndroidKeyStore 指纹绑定 | `native/biometricAuth.ts`（Android 端 Java 实现） | `@ohos.userIAM.userAuth` |

### 1.4 已有的"降级基线"——这是研究的关键前提

项目已经为"无原生插件"做了充分准备，可大幅降低鸿蒙接入成本：

1. **`featureFlag` 门控**（`frontend/src/native/capabilities.ts:30-46`）  
   能力（`audioRecording`/`secureStorage`/`push`/`backgroundTask`/`biometricAuth` 等）受 `audio.voice_input_v1`、`security.keystore_v1`、`notifications.push_v1`、`background.task_v1` 等 flag 控制，关闭即退化为 Web 实现。
2. **Capacitor 不可用时降级**（`biometricAuth.ts:33-45`）  
   `Capacitor.isNativePlatform()===false` 时直接返回 `null`，UI 走"不支持"分支。iOS 也是同一套模式，说明团队已习惯"插件缺失→feature flag 关→Web 回退"的范式。
3. **Web 路径已可用**  
   `useCameraCapture` 等已实现 Web 降级（`<input type="file">` + FileReader）。  
   WebSocket、JWT、API 客户端全 Web 可跑。

> **意义**：鸿蒙版即使没有任何 Capacitor 插件能力，只要能跑 WebView 加载 `dist/`，应用主体就能工作；只损失部分原生增强（指纹绑定、推送、系统级通知）。这是评估所有方案的核心出发点。

---

## 2. Capacitor 对 HarmonyOS 的支持现状

### 2.1 官方立场

GitHub 上 Capacitor 仓库的两条相关 issue 都已 **关闭且明确否决**：

- **#7173** "Harmony OS Support"（2024-01-26 closed）  
  官方回复（daltonclaybrook / Contributor）：
  > "We currently have no plans to support HarmonyOS directly as part of Capacitor. Building a custom platform for Capacitor to support HarmonyOS, similar to the community-built `capacitor-community/electron` might be an option for you."

- **#7818** "Capacitor Is there any plan to support extending HarmonyOS NEXT"（2025-01-06 closed）  
  官方回复（markemer / Member）：
  > "Sorry, We have no plans to support anything outside of iOS, Android and PWA. That said, Android based systems that we don't support (Like from Huawei) we will consider PRs for."

**关键含义**：
- 鸿蒙 NEXT 已 **彻底脱钩 Android**（不再兼容 APK），所以"将来 PR 上游"的路径不成立。
- Ionic 官方策略是 **iOS / Android / PWA 三件套**，鸿蒙不在路线图。
- 官方建议的做法是：**仿照 `capacitor-community/electron` 的模式自建一个 Capacitor platform 包**。

### 2.2 社区现状

| 项目 | 状态 | 评估 |
|------|------|------|
| `smarttommyau/capacitor-ohos` | 单人维护，1 star，2026-02 更新 | 极小实验性项目，**生产不可用** |
| `capacitor-community/electron` 模式 | 已被官方背书为可行的参考实现 | 自建鸿蒙 platform 包的工作量约等于写一个完整的 Capacitor runtime（JS 桥 + 原生 Activity + Plugin 注册机制）。考虑到本项目只用了 6 个插件、且全部都有 feature flag，**这个工作量大于直接写一个 100-200 行 ArkTS 壳的成本** |
| 任何官方/华为官方 port | 不存在 | — |

### 2.3 结论

> **Capacitor → 鸿蒙 的"零成本"路径不存在。**  
> 即便强行自建 platform，工作量已经超过"写一个轻量 ArkTS 壳"的成本，没有必要借 Capacitor 这个中间层。

---

## 3. 候选方案横向评估

把"Android + iOS + HarmonyOS 三端"作为一个整体目标，下面是 4 个候选方案。

### 方案 A：维持 Capacitor；鸿蒙自建 ArkTS 壳 + WebView 加载 dist/（推荐）

**思路**：Android/iOS 现状完全不动；新增 `frontend/harmony/` 目录，用 DevEco Studio 生成 ArkTS + ArkUI 的"空壳" UIAbility，内部用 ArkUI `Web` 组件加载 `frontend/dist/index.html`，通过 `runJavaScript()` 与 `onMessage` 暴露桥接。

**优势**
- ✅ **复用现有 `dist/` 产物到 100%**：鸿蒙和 Android/iOS 加载的是同一份 JS bundle，UI 完全一致。
- ✅ **后端零改动**：仍是同一套 Go API + WS。
- ✅ **鸿蒙壳工程量极小**：UIAbility + 一个 `Web` 组件 ≈ 100-200 行 `.ets`。
- ✅ **能力降级路径已现成**：依赖 `featureFlag` 关闭不实现的插件（指纹绑定 / 推送 / SQLite），Web 路径直接接管。
- ✅ **后续可渐进补原生**：当用户量起来后再用 ArkTS 重写特定页面（比如用 `@ohos.data.relationalStore` 替换本地 SQLite）。
- ✅ 不引入新前端框架，团队学习成本 ≈ 0。

**劣势**
- ❌ 鸿蒙壳没有 Capacitor 桥：必须自己写一个最小的"JS ↔ ArkTS"桥（参考 `Web` 组件的 `runJavaScript` + `on('message')`），但只需 6 个插件的子集，工作量可控。
- ❌ 应用市场上架：纯 WebView 应用在鸿蒙应用市场会被打上"非原生"标签，部分品类可能受限。规避办法是把壳做得"重"一点（加 splash、状态栏、自定义导航栏），仍属合规范围。
- ❌ 应用性能比纯 ArkTS 应用略差；但本项目主体是 WebSocket 消息流 + 列表渲染，WebView 完全胜任。

**工作量估算**
- ArkTS 壳骨架（UIAbility + Web 组件 + 基础桥）：3-5 人天
- 桥接 6 个插件到 ArkTS 等价物：5-8 人天（其中 sqlite 和 camera 是大头）
- 应用签名 / 上架材料：1-2 人天
- **总计：约 2-3 人周**，单人投入约 1 个月。

### 方案 B：换到 React Native + react-native-ohos（重写前端）

**思路**：放弃 Vue 3 + Capacitor，把前端用 React Native 重写一遍，并使用 `react-native-oh-library` 组织的 `ohos_react_native`（OpenHarmony SIG 镜像）跑在鸿蒙上。

**优势**
- ✅ React Native 有相对成熟的鸿蒙移植（`AuroraMaster/ohos_react_native` 镜像自 OpenHarmony SIG，更新 2026-04）。
- ✅ iOS / Android / 鸿蒙三端共享 JS 代码。

**劣势**
- ❌ **整个前端重写**：Vue 3 → React Native（团队需要 RN 技能栈 + 大量 UI 组件重写，包括 Tiptap 富文本这一整套）。
- ❌ 项目用了大量 Vue 生态（vue-i18n / pinia / vue-router），RN 生态等价物都要替换。
- ❌ `@capacitor-community/sqlite` 这种 RN 端等价库不成熟，本地离线能力要重做。
- ❌ **后端零改动收益被前端重写的成本完全覆盖**：方案 A 复用前端 95%，方案 B 复用 0%。
- ❌ 鸿蒙侧 `ohos_react_native` 仍处早期，星数极低。

**结论**：**不推荐**。对当前项目是 100% 重写前端，成本与收益严重不对等。

### 方案 C：换到 Flutter + ohos-flutter（重写前端）

**思路**：用 Flutter 重写前端，借助社区/华为的 `ohos-flutter` SDK 跑在鸿蒙上。

**优势**
- ✅ Flutter 渲染引擎统一，三端 UI/UX 高度一致。
- ✅ ohos-flutter 社区相对活跃（`zacksleo/awesome-harmonyos-flutter` 收录）。

**劣势**
- ❌ Flutter **不在 Google 官方路线图**上把鸿蒙列为正式 platform，是社区/华为支持的。
- ❌ 整个前端重写（Dart 语言 + Flutter 组件树），等价于方案 B 的破坏力。
- ❌ Vue 3 + Vite 时代的开发体验完全丧失。

**结论**：**不推荐**。理由同 B，但生态更不成熟。

### 方案 D：仅做"鸿蒙 Web 应用"，不上架应用市场

**思路**：把 `frontend/dist/` 当作普通 H5，借鸿蒙浏览器访问，不做原生壳。

**劣势**
- ❌ 没有离线缓存、没有推送、没有 splash、没有图标，体验差。
- ❌ 不能用应用市场分发，国内移动端基本不可用。
- ❌ 鸿蒙 NEXT 不一定所有用户都习惯从浏览器打开链接。

**结论**：**不推荐**作为正式产品方案，仅适合作为临时验证。

### 横向对比

| 方案 | 前端复用率 | 鸿蒙 UI 体验 | 开发周期 | 长期可维护 | 风险 |
|------|------------|---------------|-----------|------------|------|
| **A：Capacitor 保留 + 鸿蒙 ArkTS WebView 壳（推荐）** | 95%+ | 中 | 2-3 人周 | 高 | 低 |
| B：React Native + ohos RN | 0% | 高 | 6+ 月 | 中 | 高（生态早期） |
| C：Flutter + ohos-flutter | 0% | 高 | 6+ 月 | 中 | 高（生态早期） |
| D：仅 H5 | 100% | 差 | 1 周 | 低（仅 Web 维护） | 中 |

---

## 4. 推荐方案 A 落地细节

### 4.1 目录与工程结构

```
services/opencode-pocket/
├── frontend/                       # 现有（不动）
│   ├── src/                        # Vue 3 业务代码
│   ├── dist/                       # 构建产物（被三端共用）
│   ├── android/                    # 现有 Android 壳
│   ├── ios/                        # 现有 iOS 壳
│   └── capacitor.config.ts
└── frontend-harmony/               # 新增：鸿蒙壳工程（独立）
    ├── AppScope/                   # 包名 com.kaixuan.opencode.pocket
    ├── entry/                      # UIAbility
    │   └── src/main/ets/
    │       ├── entryability/EntryAbility.ets
    │       └── pages/Index.ets     # 主页：Web({ src: $rawfile('index.html'), controller })
    ├── oh_modules/                 # 鸿蒙依赖
    ├── build-profile.json5
    └── package.json
```

**关键点**：`frontend-harmony/` 内的 `rawfile/` 目录通过构建脚本从 `frontend/dist/` 同步，或在 CI 中执行：

```bash
npm run build && cp -r frontend/dist/* frontend-harmony/entry/src/main/resources/rawfile/
```

### 4.2 鸿蒙壳的核心代码（约 150 行）

```ets
// EntryAbility.ets
import UIAbility from '@ohos.app.ability.UIAbility';
import webview from '@ohos.web.webview';

export default class EntryAbility extends UIAbility {
  onCreate(want, launchParam) {
    // 注册自定义 URL scheme / scheme prefix 用于 JS↔ArkTS 桥
    webview.WebviewController.setWebGestureAccess(true);
  }
}
```

```ets
// pages/Index.ets
import webview from '@ohos.web.webview';

@Entry
@Component
struct Index {
  controller: webview.WebviewController = new webview.WebviewController();
  // …构建时从 .env 注入
  private static readonly API_BASE = 'http://192.168.31.45:8088';

  aboutToAppear() {
    this.controller.on('message', (event) => {
      // 收到来自 JS 的桥请求，分发到 ArkTS 端
      const { id, plugin, method, args } = JSON.parse(event.data as string);
      this.handleBridge(id, plugin, method, args);
    });
  }

  build() {
    Column() {
      Web({
        src: $rawfile('index.html'),
        controller: this.controller
      })
      .javaScriptAccess(true)
      .domStorageAccess(true)
      .onPageEnd((event) => {
        // 注入 API_BASE
        this.controller.runJavaScript(
          `window.__HARMONY_API_BASE__ = "${Index.API_BASE}";`
        );
      })
    }
  }
}
```

### 4.3 JS 侧最小改动（`frontend/src/native/harmony-bridge.ts` 新增）

```ts
import { Capacitor } from '@capacitor/core'

declare global { interface Window { __HARMONY_BRIDGE__?: boolean } }

/**
 * 鸿蒙桥：复用 Capacitor 的 registerPlugin 抽象，
 * 但底层走 window.webkit.messageHandlers / postMessage 而非原生 Activity。
 * 行为契约必须与 @capacitor/* 一致，以便上层业务代码不感知。
 */
export function isHarmonyPlatform(): boolean {
  return !!(typeof window !== 'undefined' && (window as any).__HARMONY_BRIDGE__)
}

export async function invokeHarmony<T>(plugin: string, method: string, args?: any): Promise<T> {
  // 通过 location.href 触发自定义 URL，或通过 window.webkit.messageHandlers 通道
  // 由 ArkTS 端 webview controller 的 on('message') 接收并回执
  // 此处省略具体序列化逻辑
  throw new Error('not implemented')
}
```

### 4.4 feature flag 收紧策略

为避免鸿蒙壳里出现"调用插件→未实现→白屏"，建议：

| Flag | Android/iOS | 鸿蒙 | 说明 |
|------|-------------|------|------|
| `audio.voice_input_v1` | on | **off** | 鸿蒙壳先不接 TTS，用 Web Speech API 兜底 |
| `security.keystore_v1` | on | **off** | 鸿蒙先用 WebCrypto 暂存；后续按需补 `@ohos.userIAM.userAuth` |
| `notifications.push_v1` | on | **off** | 鸿蒙 Web 端走浏览器通知，鸿蒙原生通知留到下版本 |
| `background.task_v1` | on | **off** | 鸿蒙 NEXT 暂不实现前台服务 |

可通过 `capacitor.config.ts` 里加 `platforms: { harmony: { ... } }` 或者直接在 `App.vue` 里读 `import.meta.env.VITE_TARGET_PLATFORM` 决定初始化哪个 flag 集合。

### 4.5 CI / 构建脚本

新增 `frontend/package.json` 脚本：

```json
{
  "scripts": {
    "build:harmony": "vite build && node scripts/copy-dist-to-harmony.mjs"
  }
}
```

`scripts/copy-dist-to-harmony.mjs` 负责把 `frontend/dist/` 拷到 `frontend-harmony/entry/src/main/resources/rawfile/`，并把 `VITE_API_BASE` 注入到 `.env.harmony`。

### 4.6 验证与质量门槛

- 启动 `<3s`（已有 Android 基准）
- WebSocket 长连接在 WebView 中能稳定保活（Android/iOS 已验证）
- 应用市场材料：icon / splash / 隐私声明 / 上架资质（华为开发者联盟账号）

---

## 5. 风险与待确认事项

1. **华为开发者联盟资质**：上架鸿蒙应用市场需要企业开发者账号与软著。建议先评估"是否真有商业化鸿蒙版"的需求，再决定要不要做上架。
2. **WebView 兼容性**：鸿蒙 NEXT 的 ArkWeb（基于 Chromium 内核）已自报支持绝大多数现代 Web API，但建议在目标机型（如 Mate 60 / Pura 70）真机跑一遍核心流程。
4. **Capacitor 跨 WebView 桥的语义差异**：iOS WKWebView / Android 系统 WebView / ArkWeb 在 cookie、缓存、跨域策略上略有不同，**核心 API 是 HTTPS+JSON+WSS**，受影响有限；但如果未来引入 OAuth 第三方登录（涉及 cookie 持久化），需要回归测试。
5. **插件替代品的鸿蒙实现深度**：本研究的方案 A 假设"先 Web 降级，后续按需补原生"。如果某个能力（如指纹绑定）必须做鸿蒙原生版，单个能力的补齐成本约 1-2 人周。

---

## 6. 总结

| 问题 | 答案 |
|------|------|
| 当前项目能否同时支持 Android + iOS？ | ✅ 已有 Capacitor 8 双壳，可生产使用 |
| Capacitor 能直接支持鸿蒙吗？ | ❌ Ionic 官方明确拒绝（issue #7173/#7818 均关闭并锁帖）；社区也无成熟方案 |
| 推荐的鸿蒙方案是什么？ | **保留 Android/iOS 不动；新增轻量 ArkTS 壳加载现有 `dist/`，原生能力缺失部分走 feature flag 降级为 Web 实现** |
| 工作量多大？ | 2-3 人周（壳 + 最小桥 + 上架材料），可单人一个月交付 |
| 是否需要重写前端？ | **不需要**。前端 95% 复用，仅在 `frontend/src/native/` 新增一个 `harmony-bridge.ts` |
| 是否可以再演进？ | 可以。先 Web 降级跑起来，再按用户需求逐项把 `keystore` / `notifications` / `sqlite` 用 ArkTS 重写 |

**研究结论**：在不动现有 Vue 3 + Capacitor + Go 后端的前提下，通过 **"鸿蒙 ArkTS 壳 + WebView 加载 dist/"** 这条路径，可以在 **约 2-3 人周** 内实现 Android / iOS / HarmonyOS NEXT 三端覆盖，且对原项目侵入最小、风险最低。
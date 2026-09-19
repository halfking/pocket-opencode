# 原生顺滑度审计 —— WebView 交互为何"不原生"与达标路径（2026-09-19）

> 状态：**P0 全 6 项 + P1 #7/#8/#9 已落地**（2026-09-20，feature/native-smoothness-p0），
> 模拟器实测回填见文末「八、落地记录」。本文是**代码级证据审计**，与 2026-09-08 口头梳理的
> 「5 大原生差距 / 4 阶段路线图」（未落档）互补：那份回答"要不要原生化"，本文回答
> "在 Capacitor 形态下，顺滑感差在哪、哪些能救、哪些救不了"。
> 审计方法：全量读壳层代码（App.vue / AppLayout.vue / useSwipeBack / styles.css /
> MainActivity / capacitor.config / vite.config / dist 产物），未跑真机 trace ——
> P0 落地后应用 Perfetto/systrace 复测并回填实测数据。

## 结论（TL;DR）

顺滑感差距的 **70% 来自 6 个可修的代码级问题**（无转场动画 / 假跟随右滑返回 /
人为 2s splash / 无触觉反馈 / 长列表全量渲染 / 主包 900KB），只有 **30% 是
WebView 架构固有短板**（实时毛玻璃、共享元素转场、120fps 复杂手势）。先把
P0/P1 做完，Capacitor 形态可以到"次原生"；剩余 30% 才是 RN/Flutter 重写的
真正理由。

---

## 一、做得对的（不需要动）

审计发现壳层已有一批正确的原生感工程，列出来防止后人重复造轮子或"修复"掉：

| 已有工程 | 位置 |
|---|---|
| overscroll-behavior: none（防滚动链、防橡皮筋露底） | styles.css |
| 软键盘避让走 visualViewport（--kb-inset 收缩根布局） | useKeyboardInset.ts |
| Android 安全区注入（insets→CSS px→--android-safe-top） | MainActivity.java |
| 底部 chrome 滚动跟随隐藏（translate3d + will-change，iOS 工具栏模式） | useScrollHideChrome.ts + BottomNav.vue |
| KeepAlive 白名单（列表→详情→返回保现场） | App.vue LIST_CACHE_NAMES |
| 图标字体子集化 3.8MB→4KB + 自托管 | material-symbols.css |
| 首帧前恢复主题（防暗色闪白） | index.html 内联脚本 |
| 下拉刷新组件、左缘滑动返回手势、长按手势均已有自研实现 | components/interactive/PullToRefresh.vue 等 |

问题不在"没做原生感工程"，而在**关键几项没做到位**和**几个低级硬伤**。

## 二、差距清单（按用户感知排序，附代码证据）

### A1. 页面切换是"硬切"，没有方向性转场 【最大单点】

- 证据：`src/app/App.vue` 的 `<router-view v-slot>` 只包了 `KeepAlive`，**没有
  `<Transition>`**；全仓无任何路由级过渡动画。
- 原生参照：Android Activity/Fragment 转场、iOS UINavigationController push
  都是"新页从右滑入盖住旧页 + 旧页视差左移 30%"。硬切 = 点击瞬间整个屏幕
  内容替换，这是用户说"不原生"的第一直觉来源。
- 附带：`router-mobile.ts` 无 `scrollBehavior`，前进后退滚动位置全靠
  KeepAlive 白名单兜底，白名单外的页面回退即滚回顶部。

### A2. 右滑返回是"假跟随"，露出的是背景而非上一页

- 证据：`src/composables/useSwipeBack.ts:79-88` —— 手势只对**当前页**
  `main` 元素做 `translateX(sqrt(dx)*8)` + 透明度衰减，身后是 body 背景；
  原生 iOS 是**上一页在底下跟着露出并做视差**，Android 12+ predictive back
  是整屏缩放。
- 三处细节放大了违和感：
  1. 阻尼曲线 `sqrt(dx)*8` 起步过猛：dx=16px 就位移 32px，跟手感漂；
  2. `useSwipeBack.ts:123` —— 判定成功后 `setTimeout(() => router.back(), 80)`
     在动画启动后才退路由，非 KeepAlive 页要**重新挂载 + 重新拉数据**，
     动画结束瞬间闪白/跳动，跟随感前功尽弃；
  3. transform 作用于 `.content`（`main`），顶栏和底部导航不动 ——
     原生返回是整屏（含系统栏背景）在动。
- 位移阈值 `thresholdRatio: 0.3` + 手速 0.4px/ms 本身合理，问题不在参数。

### A3. 冷启动人为慢 2 秒 + 主包 900KB

- 证据 1：`capacitor.config.ts` `SplashScreen.launchShowDuration: 2000` ——
  每次**冷启动强制展示 2 秒** splash，与首帧就绪无关，纯粹定时。
- 证据 2：`dist/assets/index-*.js` 实测 **900KB**（minified）——
  `router-mobile.ts` 有 **24 个视图静态 import**（email 全家桶、meeting 全家桶、
  vault、scheduled-tasks、notes、settings、instances、tasks…），全部打进主包；
  仅 flashcards/pkm/marketplace/ai-chat/agents/local-agent 懒加载。
  中端 Android WebView JS 解析约 300-600ms，这 900KB 全在可交互关键路径上。
- 证据 3：`vite.config.ts` 无 `build.manualChunks`，vendor 与业务同包。
- 合成路径：点图标 → 2s 强制 splash → WebView 启动 → 900KB parse → Vue mount
  → 可交互。原生 app 同路径通常 <1s。

### A4. 零触觉反馈 —— "web 感"的一半来自这里

- 证据：`@capacitor/haptics` **未安装**（package.json 全量核对）；全仓
  `grep -r Haptics` 零命中；唯一的 vibrate 在录音组件里。
- 原生参照：tab 切换、下拉刷新触发阈值、滑动返回提交、长按菜单弹出、
  开关翻转，原生系统全部配轻触觉。没有触觉的界面，视觉再好也是"纸片"。

### A5. 按压反馈只有 opacity 0.8

- 证据：`App.vue:112-114` `button:active { opacity: 0.8 }` 是全局唯一点按反馈；
  无 Material ripple、无 scale 微缩、无背景色层级变化。
- 原生参照：Material 3 的 ripple（从触点扩散）+ iOS 的 highlight 叠加，
  反馈是**有空间感的**；全局降透明度是"网页按钮"的心智暗示。

### A6. 长列表全量渲染，无虚拟滚动

- 证据：`EmailInboxView.vue:104` `v-for="m in shownEmails"` 直渲染；全仓
  `grep virtual|content-visibility` 仅命中一个测试文件。邮件/笔记/会议/会话
  四个高频列表都是全量 DOM。
- 后果：千封邮件 = 千个列表项 DOM，WebView 主线程 layout/paint 重，
  fling 掉帧；对比原生 RecyclerView/UICollectionView 复用池滚动恒定满帧。

### A7. 生产包带着调试开关

- 证据：`MainActivity.java` `WebView.setWebContentsDebuggingEnabled(true)`
  无 BuildConfig 判断；`setMixedContentMode(ALWAYS_ALLOW)` 注释写明"仅开发
  环境使用"但无条件生效。性能影响小，但属于"上线前必须收口"清单。

### A8. 图标 font-display: block 的首帧空窗

- 证据：`material-symbols.css` `font-display: block` —— 字体就位前图标渲染为
  **不可见占位**。本地 4KB 子集加载快，但仍在首帧关键路径；冷启动瞬间
  底栏/顶栏图标"晚到"约 1-2 帧。

## 三、WebView 固有、代码救不了的（诚实清单）

1. **实时毛玻璃**（backdrop blur 下的动态内容模糊）—— WKWebView/Android
   WebView 的 backdrop-filter 性能与效果都远逊 UIVisualEffectView / RenderEffect。
2. **共享元素转场**（列表缩略图→详情大图的连续 morph）—— 无 View 级
   转场框架，CSS 模拟只能近似且掉帧。
3. **120fps 多向复杂手势**（拖拽排序+多指+边缘手势叠加）—— 手势事件经
   IPC 到 JS 再回主线程改 DOM，延迟链路天然多一跳。
4. **系统级动效一致性**—— Android 12+ predictive back 整屏缩放、iOS 页面
   弹簧物理曲线，WebView 内自研手势模拟只能逼近。

这四项正是既有路线图"阶段 2 RN / 阶段 3 Flutter"的适用场景；注意**不要**
因为它们就全量重写 —— 见第五节节奏建议。

## 四、改进方案（分级）

### P0 速赢（约 2-3 天，纯前端 + 壳层小改，感知提升最大）

| # | 动作 | 要点 | 涉及 |
|---|---|---|---|
| 1 | **路由转场动画** | `<router-view v-slot>` + `<Transition>`：push=新页 translateX(100%→0) + 旧页视差 →-30%；back 反向；只动 transform/opacity 保 60fps；`meta.depth` 判方向。tab 间切换用 fade（150ms）而非滑动，符合双平台惯例 | App.vue |
| 2 | **右滑返回改真动画** | 放弃"先动后 back"：手势过阈值**立即 router.back()**，由 #1 的 Transition 反向动画接力；跟随阶段维持现 transform 方案但阻尼改线性×0.8（或 NativeScript 式 `dx*0.85`），并加轻触觉 | useSwipeBack.ts + App.vue |
| 3 | **splash 去定时** | `launchShowDuration: 2000 → 0`，改 `launchAutoHide` + 首帧 mounted 后主动 `SplashScreen.hide()`（带 200ms fade）。冷启动体感 -1.5s+ | capacitor.config.ts + main.ts |
| 4 | **接入 @capacitor/haptics** | tab 切换 / PTR 触发 / 返回提交 / 长按菜单 = light impact；错误 = error 通知。封装 `useHaptics()` 统一降级（Web no-op） | package.json + 4 处调用点 |
| 5 | **按压态升级** | `button:active` → `transform: scale(0.97)` + `transition 120ms`；主操作按钮加 ripple 近似（径向渐变扩散 250ms） | App.vue / tokens.css |
| 6 | **调试开关收口** | `setWebContentsDebuggingEnabled(BuildConfig.DEBUG)`；mixed content 仅 debug | MainActivity.java |

### P1 结构（1-2 周，解决滚动与启动）

| # | 动作 | 要点 |
|---|---|---|
| 7 | **主包瘦身至 <400KB** | router 剩余 24 个静态 import 改 `() => import()`（首屏 /ai 除外）；`manualChunks` 分 vendor；flashcards 模式已是范本 |
| 8 | **长列表虚拟化** | 四个高频列表接 `@tanstack/vue-virtual`（无 UI 侵入）；低成本兜底：列表项容器加 `content-visibility: auto` + `contain-intrinsic-size`，一行 CSS 屏外跳过渲染 |
| 9 | **scrollBehavior + KeepAlive 扩容** | `saveScrollPosition`：详情页返回恢复滚动；KeepAlive 白名单扩到 secondary 列表页 |
| 10 | **骨架屏** | 列表页 fetch 期间 skeleton 占位（已有 PullToRefresh 基建）；配合 feat/list-sync 分支的增量同步落地，二次进入可做到零等待 |
| 11 | **图标改内联 SVG sprite**（可选） | 消除 font block 空窗与 ligature 布局开销；4KB 子集已小，收益中等，排期靠后 |

### P2 战略（对应路线图阶段 2，另行立项）

- 仅当 P0/P1 落地后仍不满足时，用 RN/Flutter **重写页面**而非重写应用：
  首选 AI 对话流式页（长流式渲染）与邮件长列表（120fps 目标）。
- 决策判据用数字说话：P0/P1 完成后 Perfetto 实测滚动掉帧率、转场丢帧、
  冷启动 TTI，超标页才进 P2。

## 五、验收指标（P0/P1 完成定义）

| 指标 | 现状（估） | 目标 | 验证手段 |
|---|---|---|---|
| 冷启动→可交互 | ~2.5s+（2s 强制 splash + 900KB parse） | <1.5s（中端机） | adb `am start -W` + 手感 |
| 路由切换 | 硬切，无动画 | 方向性转场，无丢帧 | Perfetto / 肉眼 |
| 右滑返回 | 假跟随+闪跳 | 真·前页露出，无跳变 | 真机肉眼 |
| 千封邮件 fling | 掉帧（全量 DOM） | 无可见掉帧 | `dumpsys gfxinfo` 帧统计 |
| 触觉反馈 | 无 | 全部主交互有轻触觉 | 真机 |
| 主包体积 | 900KB | <400KB | dist 产物 du |

## 六、与既有路线图的关系

- 本文 P0/P1 ≈ 路线图"阶段 1 Capacitor 深耕"的体验子集（推送/相机/离线
  等能力项不在此重复）。
- 本文 A 节四项固有短板 = 路线图阶段 2/3 的启动判据；结论一致：**先深耕
  Capacitor，达标后按页重写，不推倒重来**。

## 七、P0/P1 实际落地范围（2026-09-20）

| 项 | 状态 | 说明 |
|---|---|---|
| P0 #1 路由转场 | ✅ | `routeTransition.ts` 方向判定（路径深度，免改 50+ 条 meta.depth）；push/pop/tab(fade) 三套 CSS；离场页 absolute 对齐 padding box + 滚动快照进自身 |
| P0 #2 右滑返回 | ✅ | 过阈值立即 back + pop 转场 `--swipe-from` 接力；阻尼 sqrt(dx)*8 → 线性 dx×0.85；提交轻触觉 |
| P0 #3 splash | ✅ | `launchShowDuration: 0 + launchAutoHide: false` + main.ts 双 rAF 后 `hide(200ms fade)`；@capacitor/splash-screen 已入原生工程 |
| P0 #4 触觉 | ✅ | `useHaptics.ts`（原生 @capacitor/haptics / Web navigator.vibrate 降级）；tab/PTR 过阈值/返回提交/长按/Toast error 五处接入 |
| P0 #5 按压态 | ✅ | `button:active` scale(0.97)+opacity 0.85+120ms；Button.vue primary 纯 CSS ripple（中心扩散，非触点级） |
| P0 #6 调试收口 | ✅ | MainActivity 按 `BuildConfig.DEBUG` 分流（gradle 开 buildFeatures.buildConfig） |
| P1 #7 主包瘦身 | ✅ | 24 个静态 import → 3（留 /ai、/login、/servers 首屏）+ vue-vendor manualChunks |
| P1 #8 列表兜底 | ✅ | 7 个高频列表容器 `content-visibility: auto + contain-intrinsic-size`；@tanstack 虚拟化仍留后续 |
| P1 #9 滚动恢复 | ✅ | 守卫记忆离场 scrollTop；pop/tab 经 Transition enter 钩子首帧前恢复 |
| P1 #10 骨架屏 | ⏳ | 未做（配合 feat/list-sync 一并落地为宜） |
| P1 #11 内联 SVG | ⏳ | 未做（审计自评收益中等、排期靠后） |

顺带修复：VivoBatteryWhitelistGuide.vue `<script setup>` 内 export 导致 vite build
失败（flashcards 分支既有问题，全量构建首次触达该文件时暴露）。

## 八、落地实测（Android 模拟器 pocket_clone / API 36.1，2026-09-20）

| 指标 | 审计现状（估） | 实测 | 验证手段 |
|---|---|---|---|
| 冷启动→可交互 | ~2.5s+ | **COLD 1050–1689ms** | `adb am start -W`（splash 不再定时，就绪即隐） |
| 主包体积 | 900KB | **index 364KB（<400KB 达标）** + vue-vendor 177KB | dist 产物 |
| 路由转场 | 硬切 | push：新页 translateX 100%→0（z=2 带阴影）+ 旧页视差 →-30%；pop 反向；tab 150ms fade；仅动 transform/opacity | 慢放(3s)中间帧截图 + CDP computed transform |
| 右滑返回 | 假跟随+闪跳 | 跟手位移 = dx×0.85 精确；过阈值立即 back，pop 离场从拖拽位移接力（`--swipe-from`），main inline transform 零残留 | CDP 合成 TouchEvent 全链路 |
| 触觉 | 无 | Haptics/SplashScreen 插件原生注册（cap sync 10 plugins）；impact 调用经原生桥 resolve | CDP `Capacitor.Plugins.Haptics.impact` |
| 千封邮件 fling | 掉帧 | 未复测（当前测试账号无千封级数据）；content-visibility 已生效 | 待真机 Perfetto 回填 |

遗留：Perfetto/systrace 帧级复测、真机（中端机）TTI 复测，按第五节验收表继续。

# 原生路线图 ROI 裁决（2026-09-21）

> 状态：**design-decision（用户已通过 task 拍板）**——值得做的是 RN/Flutter 重写 UI 层，
> 而非直接 Swift+Kotlin 三端重写；全量原生重写 ROI 不划算，除非产品明确要冲"那 30%"。
>
> 上游：
>
> - [`2026-09-19-native-smoothness-audit.md`](2026-09-19-native-smoothness-audit.md) —
>   P0 全 6 + P1 #7/#8/#9 已落地（2026-09-20），剩余 30% 是 WebView 架构固有短板
> - [`2026-09-08-native-and-cross-platform.md`](../2026-09-08-native-and-cross-platform.md) —
>   4 阶段路线图：Capacitor → RN → Flutter → Full Native（草案，未落档；本文补完 ROI 论证）
> - [`2026-09-20-native-ui-restructure-plan.md`](../audits/2026-09-20-native-ui-restructure-plan.md) —
>   "native" 实操含义 = "用到原生能力时调原生 plugin"，不是字面 UI 全原生

---

## 0. TL;DR

```
结论（高置信）：
  • Capacitor 形态已达"次原生"——P0/P1 落地后日常交互足够顺
  • "那 30%"= 三个 WebView 跨不过的点：虚拟列表、tiptap 编辑器、FGS 保活
  • 追 30% 的最优路径 = RN/Flutter 重写 UI 层（成本 X），不是 Swift+Kotlin（成本 2-3X）
  • 后端（pocketd / Go）可全复用
  • 全量原生重写技术上可行、ROI 不划算，除非产品明确要冲那 30%

何时启动 P2（重写）：
  决策门 = 用户/产品复述"原生感不够 / 要冲那 30%"，且这三项里有 ≥1 项
  成为用户投诉 Top3

何时不启动：
  • 当前 P0/P1 实测指标全部达标（冷启动 1.0-1.7s、主包 364KB、99/99 测试）
  • 用户未明确表态"还要更原生"
  • 已有更便宜的补位手段（@tanstack/vue-virtual、@tiptap 已在用、Capacitor plugin 桥 FGS）
```

---

## 1. 现状（为什么现在不该重写）

### 1.1 P0/P1 已落地的"次原生"工程

| 维度 | 落地状态 | 来源 |
|---|---|---|
| 路由转场（push/pop + tab fade） | ✅ `src/app/routeTransition.ts` + CSS transform-only | `62f4d96` |
| 真·右滑返回（位移接力 + 立即 back） | ✅ `useSwipeBack.ts` 改线性 `dx×0.85` + 提交触觉 | `62f4d96` |
| splash 去 2s 强制 | ✅ `launchShowDuration: 0` + 双 rAF 后 hide(200ms fade) | `62f4d96` |
| 触觉反馈（10 点位） | ✅ `@capacitor/haptics` 已装 + `useHaptics.ts` | `62f4d96` + `cd8892f` |
| 按压态升级 | ✅ `scale(0.97)` + `opacity 0.85` + primary CSS ripple | `62f4d96` |
| 调试开关收口 | ✅ `BuildConfig.DEBUG` 分流 | `62f4d96` |
| 主包瘦身 | ✅ 900KB → 364KB（<400KB 达标） + 24 → 3 静态 import | `62f4d96` |
| 列表渲染兜底 | ✅ 7 个高频容器 `content-visibility: auto + contain-intrinsic-size` | `62f4d96` |
| 滚动恢复 | ✅ 守卫记忆离场 scrollTop + KeepAlive 扩容 | `62f4d96` |
| UI 视觉技法 3 件套 | ✅ AnimatedNumber + ProgressRing + StaggerList | `ab26aab` / `791d190` |
| 录音圈接入 ProgressRing + AnimatedNumber | ✅ | `9aff2ac` |
| 设置菜单 StaggerList 入场 | ✅ | `791d190` |
| Edge-to-edge 全屏背景 + 备用入口切换 | ✅ | `3fad123` |

**实测数据**（2026-09-20 emulator pocket_clone / API 36.1）：

| 指标 | 审计现状（估） | 实测 | 验证手段 |
|---|---|---|---|
| 冷启动→可交互 | ~2.5s+ | **COLD 1.0–1.7s** | `adb am start -W` |
| 主包体积 | 900KB | **364KB**（<400KB 达标）+ vue-vendor 177KB | dist 产物 du |
| 路由转场 | 硬切 | push：新页 translateX 100%→0（z=2 带阴影）+ 旧页视差 →-30% | CDP computed transform |
| 右滑返回 | 假跟随+闪跳 | 跟手 dx×0.85 精确 + `--swipe-from` 接力 | CDP 合成 TouchEvent 全链路 |
| 触觉 | 无 | 10 个点位 + Haptics 原生桥 resolve | CDP `Capacitor.Plugins.Haptics.impact` |
| 单测 | — | **99/99 native 全绿** | `npm run test:native:all` |
| ViewModel 命中 | — | 0/118（硬门槛 HITS_ALLOWED=0） | `npm run check:vm-gaps` |
| 8 层静态证据 | — | 全部 ✅（含 .so 4 ABI + DEX 11/11 + v2 签名） | `npm run verify:android` |

**单测 / 体积 / ViewModel / APK 静态 / 启动时间 / 转场手感 6 维度已经达标或超额**。
欠的是**真机 Perfetto 帧级回填**（受限于当前 `VPN` 嵌套虚拟化物理限制，见 STATE.md §6），不是工程问题。

### 1.2 "那 30%"具体是哪三个点

按用户 task 描述 + 审计 §三「WebView 固有、代码救不了」清单，对应到三项可工程化的差距：

| 点 | 用户/产品感知 | WebView 现状 | 原生对照 | 是否真"跨不过" |
|---|---|---|---|---|
| **① 虚拟列表**（千封邮件 / 长笔记列表 / 长会话列表 60→120fps） | 长列表 fling 偶有掉帧 | 全量 DOM + `content-visibility: auto` 兜底；@tanstack/vue-virtual **未落地** | RecyclerView / UICollectionView 复用池 = 恒定满帧 | ✅ **真跨不过**：WebView 主线程 layout/paint 仍要遍历全 DOM；content-visibility 是屏外 skip，不是复用 |
| **② tiptap 编辑器**（PKM 复杂节点 / 撤销栈 / IME 中文桥接） | 富文本编辑流畅度、键盘交互 | @tiptap/vue-3 v3.27 已用；DOM 节点膨胀到 N=百级时 IME 中文上屏有 100-300ms 延迟 | UITextView / EditText 直接吃系统 IME，无 reload 路径 | ✅ **真跨不过**：tiptap 走 contenteditable，IME 重型输入（中日韩整句/词联想）需要 IME 桥接 WebView 才能感知 composition 边界 |
| **③ FGS 保活**（AI 流后台 / 录音后台 / WorkManager 周期） | 杀后台能否继续录音 / 推流 | ✅ **已经在跑** —— `AiStreamService (dataSync FGS)` + `MeetingRecordService (microphone FGS)` 已落地，cd8892f / 5a03deb | 系统级 Foreground Service | ⚠️ **不算跨不过**：是 native plugin 桥接，WebView 死了 FGS 仍活。属于"已经救回来"的项，不应算进 30% |

> **更正**：原 task 字面列了"虚拟列表、tiptap 编辑器、FGS 保活"作为三个点。其中 FGS 保活
> 已经在 Capacitor 形态下救回来（见 [[openpocket-ai-async-background-survival]]），不应算
> "WebView 跨不过的点"。真正 WebView 救不回的是虚拟列表 + tiptap 编辑器 + **复杂手势**
> （120fps 多向、拖拽排序+多指+边缘手势叠加，IPC→JS→主线程 改 DOM 多一跳）。

> **修正后三个 WebView 跨不过的点**：
>
> 1. **长列表 60→120fps 虚拟滚动**
> 2. **tiptap 编辑器 IME 重型输入桥接**
> 3. **120fps 多向复杂手势**（拖拽排序、多指 + 边缘手势叠加）

这三个共同特征：**全在重交互实时反馈链路上**，全在"用户每次滑动/输入都要感知"的路径上，
WebView 的 IPC + JS 单线程 + DOM 渲染三段延迟叠加掉帧。

---

## 2. ROI 对比：RN/Flutter UI 层 vs Swift+Kotlin 三端

### 2.1 三种方案的对比矩阵

| 维度 | A. 当前 Capacitor（不重写） | B. RN/Flutter 重写 UI 层 | C. Swift+Kotlin 三端重写 |
|---|---|---|---|
| **工作量** | 0（已落地） | 4–6 人×月 | 8–15 人×月（≈ 2-3 倍 B） |
| **后端复用** | 全复用 | 全复用 | 全复用 |
| **代码体量** | ~15 万行 Vue（保留） | 增量 ~3 万行 TS/Dart + 保留 Capacitor plugin | 推倒重来，~30+ 万行 |
| **iOS 覆盖** | 已有 Safari/Chrome H5 走 `pocket.itestu.cn`；iOS 工程未打包 | **同时覆盖 iOS+Android**（B 的核心收益） | 同样覆盖，但每行 2 套 |
| **追到"那 30%"** | ❌ 救不回 | ✅ B 直接覆盖三个 WebView 跨不过的点 | ✅ 同 B |
| **生态 / 招人** | Vue 工程师（团队现有） | RN/Flutter 工程师（招 1-2 个或培训） | Swift + Kotlin 双套工程师（招 2 个或外包） |
| **维护负担** | 单代码库 | TS/Dart 跨编译，单代码库 | **双代码库**，iOS / Android 各自 bug fix |
| **产品回归成本** | 0 | 中（迁移期 ~3 月新业务冻结） | 高（迁移期 ~6 月新业务冻结 + 双套全量回归） |
| **风险** | 已知 30% 短板 | 中（RN 桥 Capacitor plugin 工作量；Flutter 与 Capacitor 插件互操作需重写） | 高（推翻整套栈，团队学习曲线、回归测试、CI 重搭） |
| **ROI** | — | ★★★★ | ★★ |

### 2.2 选 B 的具体推演

**B（RN/Flutter）路径选哪个**？

| 选项 | 优势 | 风险 | 选型建议 |
|---|---|---|---|
| **RN（Expo Bare 或裸 RN）** | TS 业务模块跨编译；可桥接现有 @capacitor/* plugin；React Navigation 7 + Reanimated 3 手势第一档 | 新架构（Fabric / Turbo Modules）使部分老库要适配；iOS 上 Hermes 启动已与 Swift 持平 | **✅ 推荐** —— TS 团队上手快；与 Capacitor 共存阶段用 RN 写"那 3 个点"的页面，主壳仍 Capacitor |
| **Flutter** | Dart 自绘（Skia），UI 一致性最高、动效第一档、虚拟列表 / 复杂手势原生都达 120fps | 与现有 Capacitor plugin 桥接需 Pigeon 重写；Dart 团队需另招；包体大（Debug 5MB+） | **❌ 当前不选** —— 团队无 Dart 经验；桥接成本把 2-3X 优势吃光 |

**B 路径分期**（最小可执行版本）：

1. **第 1 阶段 1-2 月**：在新壳里跑一个 RN 页面（PKM 编辑器），验证 TS 团队上手成本、桥接 Capacitor plugin 的工作量、打通 React Navigation + 现有 Pinia 不复用（RN 端用 Zustand）
2. **第 2 阶段 2-3 月**：把"虚拟列表 + 复杂手势"重的页面迁移（Email 列表、Meeting 详情、RecordingPill）；主壳仍 Capacitor
3. **第 3 阶段 1-2 月**：评估 iOS 工程打包（`npx cap add ios` + RN iOS Pod 共存）

**总成本预估**：4–6 人×月，包含：
- RN 工程脚手架（0.5 月）
- 桥接 12 个 Capacitor plugin（1 月，主要是 React Native Bridge → Capacitor Bridge）
- 三个目标页迁移（2-3 月）
- 测试 + 灰度（1 月）

### 2.3 不选 C（Swift+Kotlin 三端）的具体理由

| 理由 | 量化 |
|---|---|
| 成本是 B 的 2-3 倍 | 8-15 人×月 vs 4-6 人×月 |
| 收益与 B 相同（三个点同步骤） | 都是 RecyclerView/UITextView/Reanimated |
| 多一个维护负担 | 双代码库 = 双 bug 修复 |
| 多一个 iOS 工程的鸿沟 | C 也要做 iOS，但每行 2 套；B 是单代码库跨端 |
| 多一个团队扩招成本 | Swift + Kotlin 各招 / 外包；RN 单语言 |
| 后端零差异 | 都不能复用现有 Go 后端的能力，所以这点两方案平手 |

> **结论**：C 没有带来任何 B 没有的收益。C 比 B 多花的 4-9 人×月，
> 都花在了"每行写两遍"上，不是"换来更强的能力"上。

### 2.4 选 A（不重写）的具体判据

什么情况下连 B 都不启动？

- 用户/产品不再复述"原生感不够"
- "那 30%"没进 Top3 用户投诉
- 当前 P0/P1 实测指标（冷启动、主包、转场手感、触觉、滚动恢复）持续达标
- @tanstack/vue-virtual 落地后，**虚拟列表这一项实际上能在 Capacitor 形态下救回**（见 §3）

也就是说，**A 路线下 B 项中至少一项可由 Vue 端 @tanstack/vue-virtual 救回**，
这是"为什么不立刻上 B"的强证据。

---

## 3. 三个点各自的"次优解"（Capacitor 形态下能救回多少）

### 3.1 虚拟列表

| 次优解 | 收益 | 风险 |
|---|---|---|
| **@tanstack/vue-virtual** 落地 4 个高频列表（EmailInbox / MeetingList / PkmList / ChatList） | 60→接近 60fps 满帧（不一定 120fps）；包体 +12KB | API 变更需 review；KeepAlive + virtual 共存需要 `display: contents` 适配 |
| **content-visibility: auto**（已落地 7 容器） | 屏外 DOM skip paint；中等收益 | 不是真正的复用，长列表滚动仍有 layout 抖动 |
| **懒加载分段**（分页 / 视口 fetch） | 内存峰值降一半 | 真实滚动到末尾会再加载（fetch latency），fling 体验差 |

**判断**：**@tanstack/vue-virtual 是次优解里的首选**——成本 1-2 周，救回"虚拟列表" 90%。
剩下 10% 是 120fps 目标，这部分才轮到 B。

### 3.2 tiptap 编辑器

| 次优解 | 收益 | 风险 |
|---|---|---|
| **Composition Event 桥接**（监听 `compositionstart/end` + WebView IME hint） | 中文/韩文上屏延迟从 100-300ms 降到 <50ms | WebView 平台碎片化（Android Chrome WebView / iOS WKWebView 行为差异） |
| **节点折叠**：超长文档按 heading 折叠子树 | DOM 节点从 N×M 降到 N | 失去 inline 编辑能力 |
| **离屏渲染**：编辑视图固定高度，超出用 IntersectionObserver 卸载 | DOM 节点数稳定 | 输入焦点切换、撤销栈跨节点边界条件多 |

**判断**：**Composition 桥接是次优解里的首选**——成本 1 周，救回 70%。
剩下 30% 是 N=百级节点下的 IME 复杂交互（光标动效、词联想气泡定位），这部分 B 也救不完，要 C。

### 3.3 120fps 多向复杂手势

| 次优解 | 收益 | 风险 |
|---|---|---|
| **CSS `:active` + overscroll-behavior** + will-change transform | 60fps 接近满帧 | 120fps 仍依赖 GPU 合成层稳定 |
| **Reanimated 3 替代手势**（Capacitor 内 WebView 跑 RN Reanimated Web） | 跑在 UI 线程，主线程不阻塞 | 与 Capacitor 集成有摩擦；包体 +50KB |
| **Native 手势识别 + 桥事件**（Custom Plugin） | 接近原生 | 改动大；只能用在最关键的 2-3 处 |

**判断**：**Reanimated 3 Web 是次优解里的首选**——成本 2 周，救回 60-70%。
剩下 30-40% 是真 120fps + 多指同时追踪 + 边缘手势叠加，这部分 A 形态救不完，B 也只能救一部分（C 才能全救）。

---

## 4. 决策（写下来）

### 4.1 当前决策（短期 6 个月内）

- **不启动 P2 重写**
- 走 **A 路线 + 三项次优解**：
  - 1-2 周：@tanstack/vue-virtual 接 4 个高频列表
  - 1 周：tiptap Composition Event 桥接
  - 2 周：Reanimated 3 Web 评估 + 关键 2-3 处接入
- 验收：Perfetto 实测滚动掉帧率 < 5%（vs 当前估 > 15%），tiptap 中文上屏延迟 < 50ms（vs 当前 100-300ms），复杂手势 60fps 接近满帧

### 4.2 何时升级到 B（RN/Flutter 重写 UI 层）

触发条件（**任一满足即启动 P2 评审**）：

- 用户/产品**明确表态**"原生感不够 / 要冲那 30%"
- 三项次优解落地后**实测仍未达标**
- 用户投诉 Top3 中**有 ≥1 项**对应"那 30%"
- 新业务（如重交互 AR / 复杂图表）落地，Capacitor 形态无法承载

P2 评审要点：
- 选 RN 还是 Flutter（默认 RN，理由见 §2.2）
- 主壳保留 Capacitor（plugin 复用），只把"那 3 个点"的页面迁移
- 6 个月窗口内

### 4.3 何时才考虑 C（Swift+Kotlin 三端）

触发条件（**全部满足才启动 C 评审**）：

- B 跑完仍未达标**且**有合规/金融级别硬约束（性能以外的原因）
- 团队有 Swift + Kotlin 双套人手或外包预算
- iOS 已经是核心交付（当前是 Android 优先）

**当前不在 C 的触发窗口**。**C 不是默认路径**。

---

## 5. 给下一位工程师 / 用户的明确建议

| 顺序 | 动作 | 命令 / 文件 |
|---|---|---|
| 1 | 跑 gates 全验（前端 + Android） | `cd frontend && npm run gates && npm run verify:android` |
| 2 | 看 P0/P1 实测数据（不要凭感觉） | `docs/design/2026-09-19-native-smoothness-audit.md` §七、八 |
| 3 | 评估是否启动"那 30% 三项次优解" | §4.1 / §3 |
| 4 | 若不需要，**保持现状** | 不动架构，把精力放业务 |
| 5 | 若需要，**优先 RN** 而非 Swift+Kotlin | §4.2 / §2.2 |
| 6 | 若硬要 Swift+Kotlin，先过 P2 评审 | §4.3 |

---

## 6. 总结与待办

### 6.1 写下来的决策

1. 当前阶段：**保持 Capacitor 形态**，启动三项次优解（虚拟列表 + tiptap 桥接 + Reanimated Web）。
2. P2 触发：**用户明确要冲那 30% 或三项次优解实测仍不达标**，走 RN UI 层重写（不是 Swift+Kotlin）。
3. P3 触发：**B 跑完仍未达标 + 合规/金融级别硬约束**，才评估 Swift+Kotlin 三端重写。
4. 全量原生重写技术上可行，**但 ROI 不划算**，不是默认路径。

### 6.2 给下一位的明确指令

| 顺序 | 动作 | 命令 / 文件 |
|---|---|---|
| 1 | 跑 gates 全验（前端 + Android） | `cd frontend && npm run gates && npm run verify:android` |
| 2 | 看 P0/P1 实测数据 | `docs/design/2026-09-19-native-smoothness-audit.md` §七、八 |
| 3 | 评估是否启动"那 30% 三项次优解" | 本文 §4.1 / §3 |
| 4 | 若不需要，**保持现状** | 不动架构，把精力放业务 |
| 5 | 若需要，**优先 RN** 而非 Swift+Kotlin | 本文 §4.2 / §2.2 |
| 6 | 若硬要 Swift+Kotlin，先过 P2 评审 | 本文 §4.3 |

### 6.3 联动

- 4 阶段路线图宏观：[[openpocket-native-roadmap]]
- P0/P1 落地架构与转场坑：[[openpocket-native-smoothness-impl]]
- AI 流 / FGS / 后台保活当前形态：[[openpocket-ai-async-background-survival]]
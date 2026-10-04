# 08 — Hyper 框架分层与能力协商

> 部分实现。代码落点 `frontend/src/lib/shell/`。

## 1. 两个维度必须分开

| 维度 | 含义 | 谁决定 |
| --- | --- | --- |
| **形态**（`hyperMode`） | 当前是否处于 Hyper 交互形态（compact 宽度 / 在壳内） | 视口 + 平台 |
| **能力**（`capabilities`） | 原生能力**是否真的可用** | 逐项探测 |

**存在 `window.Capacitor`、在壳内、窄屏宽度，都不证明能后台录音、可本地 OCR、
可持续后台执行。** 这是本篇存在的全部理由：`capabilities.ts` 里每个字段都必须
真的问过探针，拿不到证据就报 `false` / `'none'`。

> 「不确定」不等于「可能可以」。

## 2. 分层与 SSOT

| 层 | 责任 | 不能承担 |
| --- | --- | --- |
| 页面 | 数据 / 标题 / 动作 / 区域登记 | 每页自造导航栈、原生录音、Provider 密钥 |
| Hyper Web（`lib/shell`） | 导航上下文、滚动/手势、刷新/连续加载、专注隔离 | 保证 WebView 被挂起后继续 JS、最终财务判断 |
| 能力协商（`capabilities.ts`） | 协议版本、能力、错误分类 | 把任意 JS 名称当授权 |
| 原生宿主（`frontend/android`） | 任务调度、音频采集、系统集成 | 第二套业务逻辑与用户登录 |
| Go 后端 | 长任务真源、鉴权、金额计算与审计 | 信任本地识别结果为最终事实 |

**原生长于原生壳，不拥有金额计算与最终财务写入。**

## 3. 能力协商结果

```ts
detectCapabilities(probes, { continuousScroll })
// →
// {
//   protocolVersion: 2,
//   platform: 'web' | 'ios' | 'android',
//   navigation: true,          // Web 侧运行时，不依赖原生
//   focusWorkspace: true,      // 同上
//   continuousScroll: true,
//   tasks: { durableLocal: false, cloudDetached: true, continuation: 'foregroundOnly' },
//   recording: { available: false, background: false },
//   recognition: { pdfText: true, ocr: 'none', asr: 'none' },
//   agent: { available: false, skillFormat: 'none' },
// }
```

两条易错点（都有单测）：

- `recording.available` 与 `recording.background` 是**两件事**。能录音不等于能后台录音；
  后者需要原生前台服务/background audio 且必须在壳内。
- `tasks.continuation` 只能在**真的探测到**原生任务调度时报 `osScheduled`，
  否则诚实报 `foregroundOnly`。绝不一概写「后台继续」。

### 3.1 两条容易写错、因此在实现里显式成立的规则

1. **必须显式要求在壳内。** `Capacitor.isPluginAvailable('Camera')` 在浏览器里
   也可能返回 true（Capacitor 提供了 web shim）。但那不代表有原生实现。
   所以 `pluginAvailable` 先判 `isNativePlatform()`，再查注册表。
2. **「没有这个插件」和「还没探测」都报 false。** 「不确定」不等于「可能可以」。
   声称有而实际 `start()` 会 reject，比声称没有难排查得多。

> ⚠️ `TaskLedger` 与 `LocalAgent` 是**规划中的**插件名，本仓没有实现。
> 单测 `⚠️ TaskLedger / LocalAgent 在本仓并不存在` 专门守这一点：有人把清单
> 改成假想名字并让探测报 true，界面就会渲染出一个必然 reject 的入口。

## 4. 能力门

渲染原生相关 UI 前先问 `requireCapability(caps, need)`，返回**原因**而不是布尔：

```ts
{ ok: false, reason: 'unsupported', detail: '当前宿主没有录音插件；浏览器窄屏不等于可录音。' }
{ ok: false, reason: 'unavailable', detail: '本地持久任务不可用；当前延续策略为 foregroundOnly。' }
```

`reason` 取值 `unsupported`（宿主没这个能力）/ `denied`（权限不足）/
`cancelled`（用户取消）/ `unavailable`（暂时不可用）。

**`no-op` 不得冒充 `success`**：调用方必须能区分「做了」与「没做」。

## 5. 凭据边界

- Provider API Key **只**由部署注入服务端 secret store。不进仓库、文档、
  `VITE_*`、Web/原生包或客户端模型配置。
- 云端任务由既有登录 API 提交；**不**把 refresh token 搬到原生 Preferences。
- 原生如需后台上传，Go 侧签发短期、任务范围、账号绑定的 upload capability，
  客户端只持有引用；过期转 `waiting_user`，不新增第二套登录。

## 6. 平台后台执行边界

| 场景 | Android 原生 | 浏览器/PWA |
| --- | --- | --- |
| 切路由但 App 前台 | 原生任务继续；JS 非任务真源 | 可继续，**不保证进程保活** |
| CPU/OCR/ASR 后台 | WorkManager 持久工作；长任务按版本限制选前台服务 | 暂停或交云端 |
| 用户主动录音后退后台 | microphone 前台服务 + 常驻通知，**必须在可启动时开启** | 不作可持续录音承诺 |
| 大模型长会话 | 后端独立任务继续，App 只订阅/恢复 | 同左 |
| 被系统杀死 | force-stop 可阻止调度；云任务可继续 | 本地无法保证 |

「后台唤起录音」必须细化为：**用户从前台入口明确点击开始**，完成权限与音频
session 建立后，才允许退后台继续。不得在失焦、定时器、云推送或 Agent 自主
调用时偷偷启动麦克风。

## 7. 状态

| 项 | 状态 |
| --- | --- |
| 能力协商 + 能力门 | **已实现**（focusWorkspace 10 条 + capabilities-detect 7 条） |
| 真实插件探测 | **已实现**：`pluginAvailable` 用 `Capacitor.isPluginAvailable()`，且**显式要求在壳内**（浏览器里的同名 shim 不算原生实现） |
| 能力→插件映射 | 集中在 `CAPABILITY_PLUGINS` 常量里，不散落字符串 |
| **UI 消费者** | ❌ **没有。** 2026-10-04 实测：全仓无任何代码调用 `detectCapabilities`/`requireCapability`，产物内容指纹查 `continuation`/`CAPABILITY_PLUGINS` 均 0 命中 ⇒ **本模块不进 bundle**。实现与测试都到位，但**未上线** |
| 多层后台任务 DAG / 任务账本 | **spec-only**。`TaskLedger` 插件**在本仓并不存在**，所以 `durableLocal` 恒为 `false`、`continuation` 恒为 `foregroundOnly`——这是如实报告，不是缺陷 |

### 3.2 本仓存在**两个**能力模块（2026-10-04 记，勿合并）

| 模块 | 答的问题 | 形状 | 裁剪来源 | 消费者 |
| --- | --- | --- | --- | --- |
| `src/native/capabilities.ts`（**既有**，PR14） | 原生基元有没有：biometric / keystore / push / network | 扁平 boolean | feature flag + `clampHarmonyNativeCapabilities`（HarmonyOS） | ❌ 无 |
| `src/lib/shell/capabilities.ts`（本轮） | Hyper 运行时能力：protocolVersion / continuation / agent / skillFormat | 分层结构 | `Capacitor.isPluginAvailable()` | ❌ 无 |

两者答的都是「这个宿主能不能做 X」，但形状、裁剪来源、消费者都不同，
**故意不合并**：合并会把 feature flag 与 HarmonyOS clamp 渗进 Hyper 协议，
或反过来。两个文件的头部注释都写了这道边界。

> **本轮一处自查更正**：我把「能力探测已接真注册表」写成了「✅ 已接」，
> 随后做产物指纹核验时才发现 `isPluginAvailable` 的命中来自 **@capacitor/core
> 自己的代码**，我的模块其实被 tree-shaking 掉了。**对照组的对照也可能不是对照**——
> 指纹必须用只有本仓才有的字符串（如 `continuation`、`CAPABILITY_PLUGINS`），
> 框架里已有的 API 名一律不能当证据。
| 本地 OCR / ASR 模型包管理 | **spec-only**（`Sherpa` 插件已存在，但未接能力协商） |
| Agent / 技能包 | **spec-only** |

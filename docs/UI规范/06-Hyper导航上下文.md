# 06 — Hyper 导航上下文、标题真源与返回仲裁

> **状态：已实现**。代码落点 `frontend/src/lib/shell/`：
> `types.ts` / `navigationContext.ts` / `titleResolver.ts` / `backDispatcher.ts` / `runtime.ts`。
> 判据 `frontend/src/lib/shell/__tests__/`（导航 16 + 标题 11 + 返回 12 + 接线 9 = 48 条）。
> **未验证**：真机系统返回、浏览器 Back 与覆盖层的原子协调（见 §7）。

## 1. 为什么需要这一层

改造前的 openpocket 里，返回被**四个来源**各自处理：`AppLayout.vue` 的返回钮、
`CapApp.addListener('backButton')` 的 Android 硬件返回、以及各自为政的路由判断。
每个来源都自己决定「要不要先关弹窗」，于是出现过**穿透**（关了弹窗又跳路由）
与**重复**（关了两层）。

标题同理：顶栏从路由 `meta` 翻，页面从 DOM 找 `h1`，两者是两条独立路径，
异步实体名到达时会短暂不一致，旧页面的晚到响应还会覆盖新页面标题。

本篇把这两件事收敛成**各一个真源**。

## 2. 分层与 SSOT

| 层 | 角色 |
| --- | --- |
| Vue Router | **路由真源**。唯一决定「现在该显示哪个页面」 |
| 浏览器 History | 页面时间轴 |
| `NavigationContextStore` | 保存**打开意图**与视图快照，不拥有路由 |
| `TitleResolver` | 标题唯一解析结果；顶栏与页面共用 |
| `BackDispatcher` | **唯一**返回裁决点。四个来源只发意图 |

`runtime.ts` 把它们绑成单例，并把 vue-router 的 `afterEach` / `beforeEach`
接到 store 上。

## 3. 标题解析次序

高优先级在前（`titleResolver.ts` 的 `resolve()`）：

1. 最上层覆盖层**显式登记**的标题
2. 该覆盖层的 `aria-labelledby` → `aria-label`
3. 该覆盖层**自身**的兼容标记（`[data-shell-title]` → `h1|h2`）
4. 覆盖层无标题 → **沿用它打开前的有效标题**（含上一层弹窗的标题）
5. 无覆盖层 → 当前激活页面登记的标题；恢复路径用条目上的标题快照
6. 旧页兼容 → 页面根内的 `[data-shell-title]` / `h1`
7. 页面未渲染 → 路由 `meta.title`；最后 `document.title` / 应用名

两个必须成立的约束：

- **作用域写进选择器本身。** `root.querySelector(...)` 只在**该条目自己的根**里找。
  早期实现把它写成在整个 document 里找第一个 `h1` —— 那会把隐藏的 KeepAlive 页
  或非当前 Tab 的标题认成页面标题。
- **异步结果带 `entryId + renderEpoch`。** 页面每次激活 `beginRender()` 推进 epoch；
  `acceptAsync()` 对旧 epoch 直接拒绝。这是「旧页面晚到的项目名不能覆盖新页面」
  的**唯一**守门点。跨条目隔离由 `registered` 按 `entryId` 分槽保证。

验收样例（已写成单测）：页面「员工」→ 有标题 modal「编辑员工」→ 无标题确认框，
标题依次为 员工 / 编辑员工 / 编辑员工；返回依次关确认、关编辑。

## 4. 导航上下文（v2 schema）

```ts
interface NavigationEntry {
  id: string            // 同一 URL 多次打开也有独立 id
  parentId?: string     // 打开流程，不凭路径层级猜来源
  fullPath: string
  presentation: 'page' | 'modal' | 'sheet' | 'focus'
  openedBy: NavigationKind
  title: string
  titleSource: TitleSource
  inheritedFrom?: string
  scope: { serverId: string; accountId: string; projectId?: string }
  view: { tabId?, filterRef?, listRef?, scroll: Record<string, {x,y,anchorId?}>, focusTarget? }
  createdAt: number
}
```

硬性规则（都有单测）：

| 规则 | 为什么 |
| --- | --- |
| 页面条目封顶 **80**、操作环封顶 **100** | 沿用参考仓 v1 上限 |
| 淘汰**只裁内存**，绝不调用 `history.back/forward` 去配合 | 真实历史是页面时间轴，不是缓存 |
| `push` 截断前进分支；`replace` **保留条目 id** | 同页换月份/筛选不该产生可回退的新条目 |
| 守卫阻止/取消 → 记 `cancelled`，**cursor 不动** | 不能出现「点返回没反应但历史变了」 |
| 持久化只留**白名单 query**（`tab/page/month/project/filter/sort/scope`） | 搜索原文、token、秘密不落 sessionStorage |
| schema 不过 → **整体丢弃**，不做「尽力修复」 | 半恢复的导航比不恢复更危险 |
| 冷启动**不恢复**未提交覆盖层 | 脏表单不该自己弹回来 |
| 换账号/登出 → `reset()` 清空条目 + `titles.clearAll()` + `back.reset()` | 标题里可能有姓名；覆盖层注册表里可能有上一个账号的 `beforeClose` |

`store.reset()` 与 `runtime.setScope()` 是同一个动作的两层：store 管数据，
runtime 额外管标题登记与返回注册表。

## 5. 返回仲裁（`BackDispatcher`）

四个来源（返回钮 / Esc / Android backButton / 内容区手势）都只调
`dispatchBack(runtime)`，由它按同一套优先级裁决：

1. 子菜单/选择器声明 `consumesBack` → 只关它（输入法交给原生，不在 JS 强制提交表单）
2. 关闭最高层覆盖层（专注层上的确认框先关确认框）
3. 覆盖层有 `beforeClose` 且**拒绝** → 返回 `overlay-rejected`，
   **消费这次返回并保持现状，绝不跳背景路由**
4. 无覆盖层 → `router.back()`；被守卫阻止时 `router` 适配器返回 false →
   记 `blocked`，**不冒充成功**
5. 无已知前驱 → 路由登记的 fallback，**用 replace**（避免首页↔详情循环）
6. 首页无层 → `handed-to-system`

另外两条：

- **单飞**：飞行中重复提交复用同一个 Promise（`transitionId`），不靠固定 500ms
  判定成功。重复提交曾导致「关了两层」。
- **返回钮语义随动作变**：页面「返回」/ 弹窗「关闭」/ 专注「退出专注」，
  由 `affordance()` 导出，UI 只渲染不判断。

**不冒充 success**：`dispatchBack()` 永不返回 `undefined`。异常时返回
`{kind:'blocked', reason:'exception'}` 并记诊断，调用方据此决定是否退出应用。

## 6. 降级纪律

`createShellRuntime()` 的任何安装步骤抛错都**不阻断启动**，只写进
`diagnostics` 数组。对应「旧宿主缺能力时用 Web 菜单/手动关闭/页面内刷新」。
这条有单测（`安装失败不抛异常，只记诊断`）。

## 7. 尚未实现 / 未验证（不得当作已完成）

| 项 | 状态 |
| --- | --- |
| 浏览器 Back 与覆盖层的**原子协调**（同 URL 临时 history 标记 + 拒绝后补偿） | **spec-only**。当前只覆盖 Vue 路由，不劫持浏览器 Back |
| Android **预测性返回**（开始/进度/取消/提交四阶段） | **未实现**。`@capacitor/app` 已装，但未接四阶段 |
| iOS 可取消边缘返回 | **未实现**（本轮范围是 Android） |
| 前进按钮的真实启用条件复核（目的地权限） | **未实现**，见 `forward()` 注释 |

## 8. 接入方式

```ts
import { getShellRuntime, dispatchBack } from '@/lib/shell'

const runtime = getShellRuntime(router, { scope: { serverId, accountId } })
runtime.back.registerOverlay({ id, presentation: 'modal', beforeClose, close })
await dispatchBack(runtime)
```

页面登记标题：业务标题绑定 PageHeader 的同一个 computed，然后
`runtime.titles.register(entryId, title)`。

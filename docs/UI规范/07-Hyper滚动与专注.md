# 07 — Hyper 刷新、连续加载、吸顶与专注工作区

> **状态：运行时内核已实现**。代码落点 `frontend/src/lib/shell/`：
> `continuousList.ts` / `hyperPages.ts` / `dockCoordinator.ts` / `focusWorkspace.ts`。
> 判据 40 条（连续加载 20 + 吸顶 10 + 专注/能力 10）。
> **未接线**：这些模块尚未接入任何真实页面，见 §8。

## 1. 为什么不沿用 `useListSentinel`

改造前 `composables/use-list-sentinel.ts` 只有「露出就拉下一页」，缺少代次、去重、
失败态、耗尽态与短页补屏上限。直接后果：

- 筛选变了，上一页的慢响应仍回写
- 同一页重复请求叠加
- 短列表反复触发观察
- 失败后无限重试

参考仓把这几条列成了明确契约，本篇照此实现。

## 2. 连续加载控制器

状态机：`idle / loadingNext / refreshing / failed / exhausted / paused`。

### 2.1 三条正确性约束（都有单测，其中一条做过变异自测）

1. **旧代次响应不得回写。** `queryRevision` 在筛选/账号变化时提升；任何
   `reqRevision !== revision` 的响应**整体丢弃**。
   ⚠️ 代次检查必须发生在**任何**写操作之前。放在 `pages.reset()` 之后就晚了：
   一条过期响应会把当前数据清空却不写入任何内容 —— 用户看到列表突然空了，
   比「晚到覆盖」更糟。
2. **失败保留已加载行。** 请求失败停自动重试、显示显式重试入口，
   绝不清空再报错。
3. **同页重试原位替换，跨页按稳定 rowId 去重。** 去重只能消重、**不能补漏**；
   不假装当前分页接口有快照一致性。

### 2.2 刷新与追加互斥

`refresh()` 提升代次、取消在飞行中的请求、丢弃其单飞记录，然后从第 1 页重载。
真正的 `pages.reset()` 发生在**第 1 页成功那一刻**，不提前。

`failedKind` 记住「刚才失败的是刷新」，于是 `retry()` 知道该重试第 1 页，
而不是在旧数据上继续往下追加。

### 2.3 其它契约

| 情况 | 处理 |
| --- | --- |
| sentinel 距底 ~240px | `IntersectionObserver` 预加载；`root` 必须是 sentinel 的祖先 |
| 没有 `IntersectionObserver` | `needsManualLoad = true`，UI 显示「继续加载」按钮，**不变成不可访问的空页** |
| 首屏不足一屏 | `autoFill()` 顺序补页；**默认 `shouldStopAutoFill = true` 立即停** |
| 同一页重复请求 | 按页单飞，成功后原位替换 |
| 请求失败 | 保留已加载行，状态 `failed`，停自动重试 |
| 没有更多 | 状态 `exhausted`，断开 observer，不再触发 |
| KeepAlive 停用 / 被覆盖 | `pause()` 断开 observer 保留 query 与缓存；`resume()` 重连并**重测 sentinel** |
| 换账号/权限 | `invalidateScope()` 清缓存，不在新账号展示旧行 |
| 内存预算 | 1000 行起 `shouldVirtualize`；>3000 行由调用方裁剪旧页 |

> **关于 `shouldStopAutoFill` 默认 true**：真实「视口是否已填满」只能由布局测量
> （列表高度 vs 容器高度）得出。默认补屏会在没量到的情况下连续 append，触发请求
> 风暴 —— 那比短屏更难排查。所以宁可不补屏，调用方测量后再覆写为 `false`。

### 2.4 手动下拉刷新

`beginPull()` → `updatePull(offset, threshold)` → `endPull()`：

- 未达阈值松手 → 只回弹，**不请求**
- 达到阈值松手 → 恰好请求一次
- 中途回到阈值以下 → 从 `armed` 退回 `pulling`
- `cancelPull()` → 不请求

手势物理复用既有 `composables/pull-gesture.ts`（纯函数，已被
`pull-gesture.test.mjs` 覆盖），本模块只管状态。

#### 2.4.1 谁负责把手势绑到元素上（2026-10-06 补，这一节原本是空的）

⚠️ 上面那 4 条契约写的是**状态机**行为，但它们**不会自己发生** ——
必须有人把组件里的处理器绑到模板元素上。`components/interactive/PullToRefresh.vue`
就是那个人，而**它曾经一个人都没绑**：

- `handleTouchStart` / `handleTouchMove` / `handleTouchEnd` **定义完整、逻辑全对**，
  `onMounted` 只绑了 `scroll` 容器 ⇒ 整套下拉刷新**从未生效过**，
  而编译、类型、单测、`npm run gates` 全绿。
- 2026-10-06 设备实跑 UI-07 才暴露；已补上 4 个绑定
  （`@touchstart` / `@touchmove` / `@touchend` / `@touchcancel="resetGesture"`）。

⇒ **规范必须点明「绑定」这一步存在**，否则读者会以为写完状态机就完事了。
这就是 `components/__tests__/handler-wiring.test.mjs` 的由来
（查「事件处理器定义了却没绑到模板」，剥注释，只收 `handle*` / `on*` 前缀）。

⚠️ 判据的设计代价也要记住：前缀规则**会漏报** ——
前缀不匹配的处理器（如上表的 `resetGesture`）不被这条门禁覆盖，
仍需人工 review。所以本节把四个名字**逐个列出来**，而不是只写「绑了 touch 事件」。

### 2.5 `EmailInboxView` 为什么**不能**直接迁（2026-10-06 结论）

先前的留白写的是「需先解耦 offset 游标与分类副作用」。读完两套代码后，
这个说法**不准确**，照它做会得到一次功能回退。真实差异是**语义级**的：

`features/email/email-inbox-pagination.ts` 已经是一个比 Hyper 核心**更强**的
状态机。它针对本地 SQLite 读页的三个坑，核心目前**都没有**：

| 能力 | `email-inbox-pagination.ts` | Hyper `ContinuousListController` |
| --- | --- | --- |
| 游标 | `nextOffset` 只按「已向数据库索取的**原始行数**」推进，与去重后的列表长度无关 | **页码** `nextPage`，每成功一页 +1 |
| 收敛 | `noProgressStreak`：连续 N 页全无新增即判定到底 | 只信 `fetchPage` 返回的 `hasMore` |
| 刷新 | `applyRefreshPage`：并到顶部、**保留已翻开的页**、同 id 行取刷新页的新值 | `refresh()` 与追加**互斥**，`pages.reset()` 后整体替换 |

逐条对照后的结论比「不能迁」更精确：

1. **游标那条，核心已经更好，不必迁移。** 页码 `nextPage` 与「按已索取行数
   推进」在语义上**等价**（第 N 页 = 已索取 N×pageSize 行），而且由构造保证
   正确——去重丢了几行也不会让游标偏。`email-inbox-pagination.ts` 费力手搓的
   正是这个等价关系。
2. **`noProgressStreak` 也不是核心缺口。** 它存在是因为本地查询没有 total；
   而 `hasMore` 是 `fetchPage` 的**返回值**，适配层自己算即可，不必动核心。
3. **真正的核心缺口只有一条：刷新合并语义。** 收件箱要求「下拉刷新保留已加载
   分页、且同 id 行取新值」（否则新邮件插到顶部会把用户从第 5 页弹回顶部），
   而核心规定刷新 `pages.reset()`。这两者**不可兼得** → 已在 §2.6 补齐。

⇒ 迁移的前置条件不是「解耦分类副作用」，而是给核心加一个
`refreshPolicy: 'replace' | 'merge'`。

### 2.6 `refreshPolicy` 已落地（2026-10-06）

核心新增两个可选依赖项，**默认路径逐字节不变**：

| 依赖 | 默认 | 作用 |
| --- | --- | --- |
| `refreshPolicy` | `'replace'` | `'merge'` 时刷新按**行**并到顶部并保留已加载行 |
| `mergeRows(fresh, kept)` | `(f, k) => [...f, ...k]` | 决定合并后的**业务序** |

实现只改了两处：`loadPage` 的 refresh 分支不再无条件 `pages.reset()`；
`commitPage` 接受 `preserveCursor`，merge 时不把 `nextPage` 拽回 1。

**踩到的两个坑，都写进了源码注释**：

1. **页级 ≠ 行级。** 第一版写成 `pages.replace(1, fresh)`——测试立刻红：
   「从第 1 页滚下去、这次没出现在新第 1 页里」的行（`r1`）会**整段丢掉**。
   收件箱一次到 2 封新邮件就会触发。必须显式算
   `kept = 旧行.filter(r => !freshIds.has(r.id))` 再合成。
2. **首屏不能保留游标。** `preserveCursor` 若无条件成立，首屏（还没有任何
   已加载页）走 merge 时 `nextPage` 会停在 1，之后 `loadMore` 永远重复请求
   第 1 页——症状是「下拉刷新后翻不了页」。故判据是 `pages.count() > 0`。

**核心不决定业务序**：默认只保证「新页在前」，而收件箱要按 `date` 倒序。
排序是领域知识，交给 `mergeRows`——不设就等于强制「新页在前」，
对需要全局重排的列表是错的。

⚠️ `merge` **不删行**：服务端删掉的行会留在列表里，直到换筛选/换账号。
这是合并语义的固有取舍（`applyRefreshPage` 同样声明「不含任何删除语义」），
需要删除语义的列表应当用 `'replace'`。

判据 +6 条（`continuousList.test.mjs` 20 → 27），变异自测 5 项全转红：

| 注入 | 转红 |
| --- | --- |
| `refreshPolicy` 默认改成 `'merge'` | 🔴 默认策略用例 **+ 既有「刷新清游标」用例** |
| `preserveCursor = true` → `false` | 🔴 游标不退回 |
| 删掉 `pages.count() > 0` 首屏保护 | 🔴 首屏仍要进到第 2 页 |
| `kept` 置空（退回页级做法） | 🔴 保留已翻页 + 取新值 + mergeRows 共 3 条 |
| `mergeRows` 默认忽略 `kept` | 🔴 保留已翻页 + 取新值 |

> 变异 1 顺带打红了一条**本轮之前就存在**的用例
> （「刷新与追加互斥：刷新清游标并从首页重载」）——这说明「默认必须是
> replace」不是新写出来的要求，而是既有契约，改默认值会立刻暴露。

⇒ 当时的结论是「迁移无法在当前环境取证」。**该阻塞已于 2026-10-06 解除，见 §2.7。**

### 2.7 登录后 e2e 取证：阻塞已解除（2026-10-06）

§2.5 记的「唯一剩余阻塞：登录后页面无法稳定取证」**已解决**。此前那条留白的
理由是「本仓的 E2E 靠 UI 登录 + 首次主密码创建，进不去无凭据环境」——
这个理由不成立，后端仓里本来就有签发真 JWT 的工具：

| 环节 | 做法 |
| --- | --- |
| 签 token | `backend/cmd/gen-jwt`，用与 `pocketd` **同一个** `POCKET_JWT_SECRET` |
| 起后端 | `POCKET_HTTP_PORT=8088 POCKET_DB_PATH=<临时 sqlite> ./pocketd`（`POCKET_AUTH_LEGACY_ONLY=true`） |
| 代理 | vite 以 `VITE_API_PROXY=http://127.0.0.1:8088` 起（`--strictPort`） |
| 写登录态 | `helpers/tokenAuth.ts` 的 `addInitScript` 写 `pocket_token`/`pocket_user`/`pocket_workspace_id` |
| 过 lobster 门 | 同一模块的**真实** `initLobster(pw)`，用 Vite dev 动态 import 拿模块实例 |

**实测结论**（`/api/auth/me` 200 为前提）：

- `/#/ai`（`requiresAuth` 无 `requiresLobster`）完整渲染：顶栏「AI 工具」、
  底栏 Home/Notes/Messages/More，`/api/tasks` 与 `/api/sessions` 均 200，**无 401**；
- `/#/email` 解锁后完整渲染：顶栏「邮箱」+ 8 个分类 tab + 底栏。

⚠️ 三条**别踩的坑**，都是我自己量具错、不是产品错：

1. **`/#/email` 的一号关卡不是 401，是 `requiresLobster`。** 守卫
   `routeGuards.ts` Case C：`requiresLobster && !isLobsterReady()` → `redirectUnlock`，
   落地 `/#/login?returnTo=/email&unlock=1`。此前记的「假 token 被踢去
   `?reason=expired`」是**另一条**路径，不是 `/email` 的首个失败形态。
2. **底栏只在 compact 档出现。** Playwright 默认视口 1280，在那里断言底栏
   可见是量具错误。`authenticated-shell` 固定 390×844。
3. **底栏的 `aria-label` 走 i18n**（`:aria-label="t('nav.mainNavigation')"`），
   本仓当前语言下渲染成英文，`getByRole('navigation', {name:'主导航'})`
   按中文字面量**必然找不到**。断言用 `.bottom-nav` 类选择器。

`e2e/web/specs/authenticated-shell.spec.ts`（3 条，视口 390×844）守：落地 URL
仍是目标路由；**整程零 401**；顶栏标题非空且邮箱页含「邮箱」；底栏可见；
跨页往返后守卫不二次弹回。

变异自测（2 项全红）：

| 注入 | 结果 |
| --- | --- |
| 签名无效的 token | 🔴 落地 URL 断言先红（token 整体无效时**最先红的是它**） |
| 跳过 `initLobster` | 🔴 `unlock=1` 精确命中 |

> 「整程零 401」与「落地 URL」是**分工不是重复**：前者抓「URL 还对但请求已被拒」
> （token 中途失效 / 代理指错后端 / 某模块的 401 走了不兜底的路径——
> BUG-AX 记录的「`api/client.ts` 整个面绕过兜底」正是这种），后者抓 token 整体无效。

⚠️ `unlockLobster` 依赖 Vite dev 的 `/src/...` 路径，**生产构建下不可用**；
这是刻意的——宁可只在 dev 下可用，也**不给生产代码加测试后门**。

⇒ `EmailInboxView` 的迁移现在**可以取证**了。仍未做的是数据侧：
本地后端未配 email store，收件箱渲染为「暂无邮件」，
所以**合并语义**（新邮件插到顶部而不把用户弹回首页）仍只有单测覆盖（07 §2.6 的 27 条）。

### 2.8 收件箱连续加载：发现并修掉一个真缺陷（2026-10-06）

§2.7 打通登录后 e2e 之后，第一件真事就是这个——**它不是迁移带来的，是 e2e
一跑就撞上的既有缺陷**。

**症状**：库里 75 封邮件，收件箱只渲染 30 封，哨兵显示「已到最早一封」，
滚动到底加载不出任何东西。**还有 45 封用户永远翻不到。**

**根因**：`showLocal(false)`（刷新路径）调了 `advanceInboxPage`——那是
**翻页**的状态机：

```ts
nextOffset       += fetchedCount          // 翻页游标
noProgressStreak += (addedCount === 0)    // 「这页没新行」的收敛信号
hasMore           = fetchedCount >= pageSize && streak < 3
```

而 `load()` 挂载后会在后台最多调 6 次 `showLocal(false)`
（L485 / L496 / L501 / L505，后两次在一个 `safety < 3` 的循环里）。
于是「**这次刷新没带来新邮件**」被当成「**这一页没有新行**」：

| 次序 | nextOffset | streak | hasMore |
| --- | --- | --- | --- |
| `showLocal(true)` 首屏 | 30 | 0 | true |
| 刷新 #1 | 60 | 1 | true |
| 刷新 #2 | 90 | 2 | true |
| 刷新 #3 | 120 | 3 | **false**（`streak < 3` 不成立） |
| 刷新 #4–#6 | 150 / 180 / 210 | 4 / 5 / 6 | false |

实测 `pageState = {nextOffset: 210, noProgressStreak: 6, hasMore: false}`
—— **与推演逐位吻合**（210 = 30×7，6 = 刷新次数），而库里分页是 30/30/15。

**决定性证据**：同一文件的 `onRefresh`（下拉刷新）**早就做对了**——它不碰
`pageState`，注释写着「刷新不改游标」。所以 `showLocal(false)` 不是
「另一种合理语义」，而是**违反了同文件既有约定**。修法没有争议：
把它对齐 `onRefresh`。

**为什么此前没被发现**：`email-inbox-pagination.test.mjs` 测的是**函数**，
而这个缺陷在**调用点**——同一个函数被用在「翻页」和「刷新」两种语义上。
函数没写错，**用错的地方也没有判据**。

⚠️ 新邮件真插到顶部时，保留游标意味着后续 `loadMore` 会取到与已有行重叠的页，
去重后自然消化——代价是几屏内列表不再增长。这**远好过永久卡死**，且与核心
`refreshPolicy: 'merge'` + `preserveCursor` 是同一条规则。

**判据**（`e2e/web/specs/email-continuous-list.spec.ts`，3 条，依赖 §2.7 的登录态）：

| 用例 | 守什么 | 修复前 |
| --- | --- | --- |
| 本地库确实能翻出更多 | **对照**：先证明数据够，避免把「没数据」当「翻不动」 | 🟢 |
| 哨兵不得谎报「已到最早一封」 | 抓**症状** | 🔴 |
| 滚动到底真的补出后续页 | 抓**能力**（不只是文案） | 🔴 |

第一条是关键的负对照：没有它，后两条在「种子没写进去」时也会红，
却会把病因指向错误的方向（与「复现工具出错比被复核对象出错更糟」同族）。

### 2.9 `EmailInboxView` 迁到内核（2026-10-06 完成，连续加载 3/3）

§2.8 修掉缺陷后，迁移本身也做完了——**验收网是 §2.8 那三条 e2e**，
适配层另有 9 条单测（`email-inbox-adapter.test.mjs`），4 项变异全转红。

**新增适配层** `features/email/email-inbox-adapter.ts`：迁移里真正属于
「逻辑」而非「接线」的只有三处，单独成文件是为了能在 node --test 下穷举：

| 产物 | 对应 | 为什么不能由内核代劳 |
| --- | --- | --- |
| `makeInboxFetchPage` | 页码→offset 换算、`hasMore` 推导 | 本地查询没有 total；去重丢行时不能用「列表长度」当游标 |
| `inboxMergeRows` | 合并后整表按 `date` 倒序 | 排序是领域知识，内核刻意不替调用方决定 |
| `pageToOffset` | 与换算互为逆运算 | 便于单独核对边界（页码 <1 夹到 0） |

页面侧只剩三行参数：

```ts
useContinuousList<LocalEmail>({
  fetchPage: makeInboxFetchPage({ readPage, getCategory, getFolder, countAll }, INBOX_PAGE_SIZE),
  pageSize: INBOX_PAGE_SIZE,
  refreshPolicy: 'merge',
  mergeRows: inboxMergeRows,
})
```

`refreshPolicy: 'merge'` 让 §2.8 那个缺陷**在结构上不可能再发生**：内核把
「刷新」与「翻页」分成两条路径，refresh 走 `preserveCursor` 不碰 `nextPage`。
原先的根因是同一个 `advanceInboxPage` 被两种语义共用。

**迁移途中撞出我自己写的 composable 有一个潜在缺陷**（三页同时中招）：

> `connect()` 原本只在 `onMounted` / `onActivated` 调一次。而三个页面的哨兵
> 都写成 `v-if="list.length > 0"` —— **首屏一行都没有时哨兵根本不渲染**，
> `sentinelRef.value` 是 null，于是 observer **一次都没连上过**，滚动到底
> 永远不加载下一页。`MeetingListView` / `NoteListView` 有同一个缺陷，
> 只是当时没有 e2e 测它们。
>
> 单元测试测不到：composable 的测试直接注入元素，不经过「v-if 先 false、
> 拿到数据后才 true」这个时序。是迁移 `EmailInboxView` 时被 §2.8 的
> 「滚动到底真的补出后续页」撞出来的。

修法在 composable 层（一次修好三页）：`watch(sentinelRef, …, { flush: 'post' })`，
哨兵出现即重连、消失即断开。`flush: 'post'` 是必须的——DOM 更新**之后**才能量
`scrollHeight`。变异自测：移除该 watch ⇒ 滚动加载立刻转红（30 → 30）。

⚠️ `showLocal(replace)` 的参数已失去意义（整表重读与合并重读都由
`refreshPolicy` 决定），保留它只是不改后台流程的调用点；`load()` 也已简化。
这两处是**遗留适配**，登记在此以免后来者以为它还有语义。

## 3. 吸顶坐标（`dockCoordinator.ts`）

```
effectiveTop = max(当前滚动视口上沿, 当前可见顶栏下沿)
tabTop       = effectiveTop
tableTop     = effectiveTop + 所属吸顶 Tab 的实测高度 + 区域工具栏高度
```

三条规则：

1. 坐标统一为 **viewport CSS 像素**（`getBoundingClientRect`，不混算
   `offsetTop` 与滚动位置）。
2. **不叠加 safe-area 常量** —— 顶栏下沿是实测值，状态栏 inset 已被吸进顶栏自身
   高度；再加一次就是双重偏移。
3. 亚像素抖动（<0.5px）归零，避免停靠时抖一下。

层级用 `LAYER_BODY(0) < LAYER_TABLE_HEADER(10) < LAYER_TAB_TOOLBAR(20)`，
**不固化成单一 z-index**：吸顶元素不能超出父层 stacking context 压住全局 modal。

防写死验证：把顶栏高度从 48 换成 72，`tableTop` 必须跟着变（单测
`【防写死】改变顶栏高度必须改变输出`）。

### 3.1 本仓现状：`dockCoordinator` **未接线**（附实测理由）

审计结论：**openpocket 没有任何表格**（`<table` / `el-table` 全仓 0 命中），
所以「表头吸顶」这个 openpocket 的原始目标**没有可接对象**。
现存 15 处 `position: sticky` 分散在 12 个文件，各自硬编码 z-index
（`2` / `50` / `100` / `1000` …），这才是真正的问题。

因此本轮**没有**把 `dockCoordinator` 硬接上去。理由与替代动作：

| 判断 | 依据 |
| --- | --- |
| 表格吸顶 | 无表格 ⇒ 无对象。硬接等于造一个没人用的机制 |
| Tab 吸顶 | 有候选（`SessionListView` / `RssListView` / `ThemeTabs`），但需要在真机上看旋转/分屏是否错位，本环境无法目视验证 |
| **层级阶梯** | **本轮实际推进的**：`dockCoordinator` 的「层级按交互层分配、禁止散落字面值」那部分，转成了可执行的 z-index 注册表门禁（见 §3.2） |

### 3.2 层级阶梯门禁（2026-10-04 落地）

`styles/tokens.css` 早年就写着「全局唯一权威。新增浮层必须在此登记，**禁止散落字面值**」，
但那句话**没有强制力**。实测扫描全仓发现三类真实缺陷：

| 类别 | 实测 | 后果 |
| --- | --- | --- |
| ① 引用未定义 token | 2 处 `var(--z-popover, 30)`，而 `--z-popover` 从未定义 | 实际取 30，**低于** `--z-sticky`(50) 与 `--z-bottom-nav`(70) ⇒ tooltip 被吸顶栏与底栏盖住 |
| ② fallback 与 token 冲突 | 3 处：`--z-fab, 40`(token 60)、`--z-bottom-nav, 20`(token 70)、`--z-sheet, 1000`(token 1300) | token 一旦没加载上，元素落到**另一个层**——同一件事两处写法 |
| ③ 裸数字 z-index | 23 处 | 违反自己声明的注册表规则 |

**本轮修掉的**：登记 `--z-popover: 100`（修 ①）；三处冲突 fallback 改为直接用
token（修 ②，视觉不变）；修正阶梯注释里「fab 在 bottomnav 之上」与取值
`60 < 70` 的矛盾（按取值改注释，不擅自改值——改层级只能靠真机/截图验证）。

**2026-10-06 补：7 处全屏遮罩已全部归位，不再需要目视核对。**
上一版把 `SettingsPermissionsView` 40、`WorkbuddyView`/`AgentMarket`/`SkillMarket` 50、
`InvoicePreviewSheet` 80、`RssItemDetail` 1000、`JsonBlock` 999 记为「待目视核对」，
理由是「改错层级的后果无法目视验证」。这个约束后来被**源码本身解除了**——
不需要截图就能定论的有三条独立证据：

1. **作者本意写在注释里**。这 6 处的注释逐字写着「同 BottomSheet.vue」，
   而 `BottomSheet.vue` 用的是 `var(--z-sheet)`(1300)。注释即意图。
2. **祖先链不创建层叠上下文**。静态确认 `#app` / `.app-root` / `.app-layout` /
   `main` / `.content` / `.top-bar` 全都只有 `position: static` +
   无 `transform`/`filter`/`opacity<1`/`will-change` ⇒ 它们与底栏、吸顶栏
   **同处一个根层叠上下文**。既然同层，「更低」就等于「被盖住」，
   不需要知道嵌套关系的细节。
3. **最小复现实测**。Playwright 在授权页顶栏区域打
   `document.elementFromPoint`：z=40 时返回 `header.top-bar`（顶栏压住遮罩），
   z=60 时返回 `div.bind-overlay`（遮罩压住顶栏）。行为差异可直接观测。

归位结果：`SettingsPermissionsView` / `InvoicePreviewSheet` / `JsonBlock` → `--z-sheet`；
`WorkbuddyView` / `AgentMarketView` / `SkillMarketView` / `RssItemDetail` → `--z-dialog`
（前三者是居中确认框、`RssItemDetail` 是居中 `modal-mask`，与 `Dialog.vue` 同形）。
ALLOWLIST 里因此**不再有任何「待目视核对」条目**。

`styles/__tests__/z-index-ladder.test.mjs`（9 条）守：阶梯声明顺序严格递增；
`var(--z-*)` 必须是已定义 token；保留的 fallback 必须等于 token 取值；
裸数字必须在 ALLOWLIST 且带理由；ALLOWLIST 不得有陈旧条目或与 token 重复登记。

变异自测（4 项全转红）：

| 注入 | 结果 |
| --- | --- |
| 恢复 `var(--z-fab, 40)` | 🔴 ② fallback 冲突 |
| 恢复 `var(--z-popover, 30)` | 🔴 ② fallback 冲突（token 已是 100） |
| `BottomNav` 改成 `z-index: 7777` | 🔴 ③ 未登记裸数字 |
| 引用 `--z-totally-undefined` | 🔴 ① + ② |

> 这条门禁自己出过一次事故：初版 `collect()` 对每个条目 `readdirSync(full)`，
> 对**文件**抛 ENOTDIR 被 `catch { continue }` 吞掉 ⇒ `USES` 恒为空 ⇒
> 报「0 处 z-index」，并把 ALLOWLIST 全部误判成陈旧。
> **教训：量具自己报的数字要先自证非空**，空清单会让上表恒为「不合格」。

### 3.3 底部让位门禁 `bottom-chrome-gate.test.mjs`（2026-10-06 落地）

`z-index-ladder` 只管「谁盖谁」。但层级再高也救不了**几何**：一个 `position: fixed`
元素若把 `bottom` 定位在底栏区域内，它会**主动钻到**底栏底下。
这半边此前完全没有门，于是出了三个真缺陷——**都不需要真机即可证明**：

| 元素 | 原 `bottom` | 自身高 | 底栏高 | 被盖比例 |
| --- | --- | --- | --- | --- |
| `MeetingDetailView .speakers-btn` | 14px | 40px | 56px | **100%（整颗按钮点不到）** |
| `MeetingMicDock .mic` | 14px | 56px | 56px | **75%** |
| `VaultEntryView .toast` | 20px | ~40px | 56px | 下沿被压 |

三者的 z-index 都是 `var(--z-fab)`(60) < `var(--z-bottom-nav)`(70)，
`bottom` 又只按 `--app-safe-bottom` 定位、完全没读 `--bottom-chrome-height`
⇒ **几何与层级双重失守**。三个都挂在有底栏的路由下
（`BottomNav` 由 `AppLayout` 的 `showBottomNav` 渲染，仅平板会话工作台详情态关闭）。

另修 3 处「现在对、但靠硬编码撑着」的位置：`PkmEditor` 裸 `80px`、
`RecordingPill` `calc(app-safe-bottom + 72px)`、`DualScreenLayout` `calc(80px + space-4)`。
它们是在**手工复刻** `--bottomnav-height`(56)——底栏一加高就静默失效，
与「fallback 与 token 冲突」是同一类病：同一件事写了两处。
`DualScreenLayout` 的 `z-index: 100` 一并归位到 `--z-fab`：
它 `bottom` 已让开底栏，100 只是**靠更大的数字掩盖了没让开**，底栏加高即穿帮。

门禁（9 条）守：底部固定的浮层必须引用 `--bottom-chrome-height`，
**并且真的把值算出来**——`calc(var(--bottom-chrome-height) - 20px)` 引用了
token 却是 36px < 56px，模式匹配会放行。因此 `resolveBottomPx()` 会把
`calc()` / `max()` / `min()` / `var()` / 裸 px 一路折叠成数字再比较。
豁免项的硬编码数字必须 ≥ `--bottomnav-height`（否则底栏加高时转红）；
ALLOWLIST 不得有陈旧条目、每条必须有理由。

> **解析器踩的坑**：初版扫描用「向上回看 12 行找 `position: fixed`」，
> 于是把 `.actions-bar { position: sticky }` 和 `border-bottom: 1px solid`
> 算成了命中，13 个结果里有一半是假的。**必须按 `}` 切规则块**，
> 且 `bottom` 的正则要有前导边界 `[\s;{]`（否则 `border-bottom` 命中 `bottom`）。
> 门禁首条即「量具自证」：采集结果非空，且必须采到已知正确的参考实现
> （`MeetingListView` 等 5 处）——采不到就说明解析器坏了。
> 豁免表的 key 用**规则块起始行**而非声明行号：改声明值不会让 key 漂移。

变异自测（正反各 1）：

| 注入 | 结果 |
| --- | --- |
| `position: fixed; bottom: 4px` | 🔴 必须让开 chrome |
| `position: fixed; bottom: calc(var(--bottom-chrome-height) + 8px)` | 🟢 负对照：不得误报 |
| `calc(var(--bottom-chrome-height) - 20px)` | 🔴 解析为 36px，**低于**底栏 56px |
| `calc(var(--bottom-chrome-height) + var(--space-4))` | 🟢 70px 放行（证明真算了 spacing token） |

> 解析器自己也踩了三个坑（都曾让 4 条用例全红）：按 `+/-` 切分会连
> `--bottom-chrome-height` **内部的连字符**一起切开；切分后各项带空白；
> `Toast.vue` 的 `max(var(--bottom-chrome-height), var(--composer-inset, 0px))`
> 解析不了——那是**正确写法**，不该被判成缺陷，故改为支持折叠 `max()/min()`。

负对照是这条门禁能用的前提——只会「一律判红」的不是判据，是噪音。

## 4. 专注工作区（`focusWorkspace.ts`）

可退出的**应用内全屏层**。不是浏览器 Fullscreen API，也不靠双击/长按隐藏入口。

状态机：`normal → entering → focused → exiting → normal`。

进入顺序**不能反**：先存快照 → 再锁背景。反过来锁住之后就量不到滚动位置了。

五条容易做错、因此在实现里显式成立的规则：

1. 锁 `body` **不等于**锁住背景。本仓主内容区是独立滚动容器 `<main>`，
   所以必须锁**真实滚动宿主**。
2. 锁要**准确恢复原 inline style**（`overflow`/`position` 的原值），
   粗暴写 `''` 会抹掉页面自己设的值。
3. `inert` 在旧 WebView 不可用时走 fallback（焦点门 + `aria-hidden` + 指针屏蔽 +
   可恢复属性快照）。
4. 专注层之上打开的确认框/菜单**属于专注**，不得被一起屏蔽
   （`pushChild`/`popChild` 引用计数）。
5. 暂停的是**背景**的 observer / 快捷键 / 刷新 / 滑动返回 / 点击；系统返回、
   权限提示、安全事件仍然生效。

`shouldBlockBackgroundEvent()` 是那个 owner 判据：⚠️ 只 `stopPropagation`
**撤销不了已经执行**的外部监听器，所以全局 capture handler 必须检查事件 owner。

任一步清理抛错，状态仍归位（单测：`任一步清理抛错，仍把状态归位`）——
半解锁比不解锁更糟。

### 4.1 `useBodyScrollLock` 已加固（2026-10-06），但**定性是契约加固，不是缺陷修复**

规则 1 此前只有「文档写了、代码没做」。现已落地：除 `body` 外，扫 `#app` 子树
里所有 `scrollHeight > clientHeight + 20 && overflowY ∈ {auto,scroll}` 的容器，
逐项快照 `overflow`/`overflowY` 后置 `hidden`，关闭时**逐项精确恢复**。
只扫 `#app` 是因为三个消费方（`BottomSheet` / `Dialog` / `UnifiedComposer`
全屏态）都用 `<Teleport to="body">`，落在 `#app` 之外——既不漏背景容器，
也不会把弹层自身的长列表锁死。

⚠️ **但它没有修掉任何用户可见缺陷。** 真实滚轮的前后对照（`/email` +
BottomSheet，390×844）：

| 观测量 | 只锁 body（旧） | 锁宿主（现） | 差异 |
| --- | --- | --- | --- |
| 宿主 computed `overflowY` | `auto` | `hidden` | ✅ 契约被兑现 |
| **真实滚轮**能否推动背景 | 否 | 否 | ❌ **无差异** |
| **程序化** `scrollTop` | 可写 | 可写 | ❌ 无差异 |

原因：三个浮层都是 `position: fixed; inset: 0` 的**全屏遮罩**，手势落在遮罩上，
滚动链沿 DOM 祖先走（body/html），**永远到不了 `.content`**——它与遮罩是兄弟
子树，不在同一条滚动链上。旧实现是**碰巧**正确。

所以这次改动的价值是：让 composable **兑现自己的名字**，而不是继续依赖
「遮罩恰好全屏」这个巧合。若将来引入**非全屏**浮层（只占底部的面板、
不 Teleport 的内联层），现在的锁立刻开始起作用。

判据：`e2e/web/specs/modal-scroll-lock.spec.ts`（4 条，变异验证见 §7）。

## 5. 禁止事项

1. 只换分页 UI 而仍用「下一页覆盖上一页数据」。
2. 刷新开始时不清游标 / 不提升代次。
3. 吸顶写死「顶栏 48 + Tab 40」。
4. 拿 `stopPropagation` 当作「已屏蔽背景」。
5. 复制一份表格渲染到专注层（应 Teleport **同一实例** + 原位占位）。
6. 把 `pages.reset()` 放在请求**发出**而不是**成功**之后。
7. **拿程序化 `scrollTop` 当作滚动锁的判据**（2026-10-06 实吃）。
   `overflow: hidden` 按 CSS 规范只禁止*用户*滚动，**不阻止脚本写 scrollTop**，
   所以「弹层打开时 `scrollTop = 700` 仍生效」在改前改后**都是 700**——
   对该修复是**不变量**，推不出任何缺陷。量「锁」必须用：
   - **契约**：`getComputedStyle(宿主).overflowY`；或
   - **真实输入**：`page.mouse.wheel()` / 触摸拖拽（程序化赋值不行）。

## 6. 自检

```bash
cd frontend
node --test src/lib/shell/__tests__/continuousList.test.mjs \
         src/lib/shell/__tests__/dockCoordinator.test.mjs \
         src/lib/shell/__tests__/focusWorkspace.test.mjs
```

## 7. 变异自测记录（2026-10-04）

| 注入变异 | 结果 |
| --- | --- |
| 删掉 `.then` 里的代次守门 | 初次 **🟢 全绿**（判据无牙）→ 补 `过期响应不得让列表瞬时变空` 后转 🔴 |
| 刷新失败也清空 pages（回原缺陷） | 🔴 |

第一条值得记：最初那条判据只断言**最终**行数，而缺陷发生在两条响应**之间**，
所以恒绿。修正方式是把断言改成「订阅每次 emit，一旦非空就不得再变空」，
并且必须让过期响应落在**已经有数据之后**（按代次控制时序）——
顺序写反的话清空发生时列表本来就是空的，判据同样恒绿。

## 8. 变异自测记录（2026-10-06，`modal-scroll-lock.spec.ts`）

基线 4/4 绿。两个变异**分别单独**回退，结果与「哪条守什么」完全对得上：

| 注入变异 | ① 负对照 | ② 契约 | ③ 精确恢复 | ④ 现状刻画 |
| --- | --- | --- | --- | --- |
| 基线 | 🟢 | 🟢 | 🟢 | 🟢 |
| M1：`useBodyScrollLock` 回退成**只锁 body** | 🟢 | 🔴 | 🔴 | 🟢 |
| M2：`release` 改成**写 `''`** | 🟢 | 🟢 | 🔴 | 🟢 |

两条值得记：

1. **④ 对本次修复无牙，且已如实标注。** 它在 M1 下依然绿——因为全屏遮罩
   本来就挡住了真实手势（见 §4.1 的对照表）。它守的是**另一条**不变量：
   「三个浮层必须保持 `position:fixed; inset:0` 全屏」。将来若引入非全屏
   浮层，④ 转红而 ② 才开始起作用。判据名里直接写了「对本次修复无牙」，
   免得下一个接手的人重跑变异后以为它坏了。
2. **③ 初版是恒真的，被 M2 逮到。** `/email` 的 `.refresh-content`
   **本来就没有 inline overflow**（值来自 CSS class），before/after 都是
   `''`，比对自然成立——两边恒等于同一个常量。修法是先写入一个只有本用例
   设过的非空 inline 哨兵值（`'auto'`），并加一条**前提自证**（断言锁确实
   把它覆盖成 `'hidden'`），否则「恢复」无得可失、断言无从谈起。

⚠️ 还有一条被本轮推翻的旧结论，已在 §4.1 留档：曾用「弹层打开时程序化
`scrollTop` 仍可写」当作 `useBodyScrollLock` 的缺陷证据。该量具对修复是
不变量（`overflow:hidden` 不阻止脚本写 scrollTop），**证据不成立**，
据此报出的「用户可见缺陷」实际不存在。

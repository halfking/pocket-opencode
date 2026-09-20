# 邮件域架构裁决 —— 不抽 `useEmailListVM`（2026-09-20 · 周 6 收尾）

> 上游：[`../audits/2026-09-20-native-ui-restructure-plan.md` §2.3 §8.3](../audits/2026-09-20-native-ui-restructure-plan.md) 与 [`./2026-09-20-viewmodel-gap-audit.md`](./2026-09-20-viewmodel-gap-audit.md)
> 结论：**不抽 `useEmailListVM`**——`useEmailInbox` 已经担任 ViewModel 角色；`useEmailListVM` 引入只会冗余。

---

## 0. 决议

| 候选项 | 决议 | 证据 |
|---|---|---|
| 抽 `useEmailListVM` 套在 `useEmailInbox` 与 `EmailInboxView` 之间 | ❌ 不抽 | 见 §1 实证 |
| `useEmailInbox` 升级为"完整 VM"契约 | ✅ 已满足 | 见 §2 |

**周 6 收尾**：本周无需新增代码；本议题关闭。

---

## 1. `EmailInboxView.vue` 实证（main HEAD · 11,539 行级页面）

import 区段：

```ts
import * as emailsStore from './emails-store'                   // Store（Pinia 候选）
import type { LocalEmail } from './emails-store'                // 类型
import { inboxHasMore, pullInboxFromServer, readInboxPage } from './email-inbox-page'  // Runtime（数据出口）
import { runDelegatedEmailFetch } from './email-fetch-run'       // Service 子模块
import { sanitizeFetchHint } from './email-fetch-plan'           // Service 子模块
import { formatEmailRelTime } from './cleanup-filter'            // Service 子模块
import { INBOX_CATEGORY_CHIPS, catLabel } from './email-categories'
import { formatInboxSearchLabel } from './email-inbox-search'
import { useEmailInbox } from './use-email-inbox'                 // ⭐ ViewModel
```

调用区段（节选）：

```ts
const inbox = useEmailInbox()        // ⭐ 顶层 ViewModel
const emails = ref<LocalEmail[]>([])

const shownEmails = computed(() => inbox.visibleEmails(emails.value))

async function onClassify() {
  emails.value = await inbox.runClassify(emails.value)
  await showLocal()
}

async function onPurge() {
  if (!inbox.selectedCount.value) return
  await inbox.confirmPurge()
  await load()
}

async function load() {
  const page = await readInboxPage(activeCategory.value, 0)
  emails.value = page
}
```

➡ 直接 API import：**0 个**（grep `from '../../api'` 在源码中无 import；emails-store 自己持 ref + 内部 fetch）
➡ 直接 store mutation：**无**（全部通过 emailsStore action）

---

## 2. 4 层映射

```
┌──────────────────────────────────────────────────────────────────┐
│ View（EmailInboxView.vue）                                       │
│   - 模板 + 重活 UI（onClassify / onPurge / showLocal）          │
│   - 仅消费 inbox.* 方法 + emailsStore 反引用的 list              │
└──────────────────────────────────────────────────────────────────┘
                  │ 消费
                  ▼
┌──────────────────────────────────────────────────────────────────┐
│ ViewModel（use-email-inbox.ts）                                  │
│   - selectMode/selected/searchOpen/moreOpen  UI-only 状态         │
│   - runClassify / confirmPurge  视图行为（toggle / select）       │
│   - selectedCount 视图派生                                       │
└──────────────────────────────────────────────────────────────────┘
                  │ 委托
                  ▼
┌──────────────────────────────────────────────────────────────────┐
│ Store（emails-store.ts · 17k 行 · Pinia）                        │
│   - list / markRead / setAiClassification / commitLocal          │
│   - 全局缓存与跨页同步                                            │
└──────────────────────────────────────────────────────────────────┘
                  │ 委托
                  ▼
┌──────────────────────────────────────────────────────────────────┐
│ Service 子模块（email-fetch-run / email-inbox-page /             │
│                 email-soft-delete / email-classify-run /          │
│                 email-body-cache / ... 共 25+ 文件）              │
│   - 单职责 + 可组合                                              │
└──────────────────────────────────────────────────────────────────┘
                  │ 依赖
                  ▼
┌──────────────────────────────────────────────────────────────────┐
│ Runtime（native/{mobileSync,outboxStore,local-db,...}）           │
│   - 数据落地、出站、SSE 接收                                    │
└──────────────────────────────────────────────────────────────────┘
```

→ 完整 4 层；不缺任何一层；**完全没有新增 `useEmailListVM` 的语义槽**。

---

## 3. "会不会更纯"——反问拒

有些"洁癖主义者"会建议把 `useEmailInbox` 与 `emailsStore` 间再加一层以避免 Store 直接被 View 引用（"Vue 只调 ViewModel"）。

但：

- `useEmailInbox` 仅暴露视图级状态，**不持有 list cache**；如果抽 `useEmailListVM` 把 list 缓存搬过去，会与 `emailsStore` 重复持有，引入同步复杂度。
- Pinia 设计的本意就是跨组件/跨页共享缓存；View 直接消费 store 是 **正式模式**，并非反模式。
- 周 5-6 工作量已经在 [`viewmodel-gap-audit`](./2026-09-20-viewmodel-gap-audit.md) 压缩为精确 2 文件；为邮件域再多套一层会**反向增加 50-100 行胶水代码**。

→ **不抽**。

---

## 4. 关闭后效

| 项 | 后续 |
|---|---|
| 周 6 阶梯 | 邮件域已完成（无 TODO）；ConfigList 留到下次配置域大改 |
| ViewModel 缺口审查 | 维持现状 1/118（ConfigList） |
| 可能改进点（不属本会话） | `useEmailInbox` 增加 `setSearch(s)` setter 替代 `inbox.search.value = { ... }`，但属微优化、非结构性问题，本期不修 |

---

## 5. 给未来 agent 的判定原则

> 判断"该不该新增 XxxViewModel"时，先用 [`viewmodel-gap-audit.sh`](./2026-09-20-viewmodel-gap-audit.md) 跑一次。
>
> 命中 = 0 → **不加**；
> 命中 ≥ 1 → 才考虑是否值得加；只在 View **直接调用 API / 持有 ref 直连 store.action 时**才加，否则仍保持原结构。

---

**写于**：2026-09-20
**作者**：Mavis / mavis orchestrator
**下次更新**：邮件域下一次大改（如增加"邮件-任务" 关联 / 跨端同步）

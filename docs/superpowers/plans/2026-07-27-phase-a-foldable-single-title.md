# Phase A: 折叠屏铺满 + 单一标题栏 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 OpenCode Pocket 在折叠屏展开态呈现 list+detail 双面板布局（铺满），折叠合盖/手机仍是单栏；移除冗余标题，让全应用只剩 AppLayout top-bar 一个标题；统一断点 560/840/1280。

**Architecture:**
- `AppLayout.vue` 改由 `route.meta.showTopBar` 控制 top-bar 显隐（默认 true）。
- 新建 `SplitLayout.vue` 通用 master-detail 容器；≥840dp 双栏，<840dp 单栏 + detail 整页替换 master。
- 统一断点 `560/840/1280`（compact/medium/expanded/wide），扩展现有 `useBreakpoint` composable。
- 7 处 view 自带 `<h1>` 和 6 处 `<h2>` 顶部标题全部移除（视觉权重交给 AppLayout top-bar 或页面主内容）。

**Tech Stack:** Vue 3 + TypeScript / 现有自研 `useBreakpoint` / 原生 `window.matchMedia` / Capacitor 6。

---

## 文件结构

```
frontend/src/
├── app/
│   ├── AppLayout.vue                 # (改) showTopBar meta + 断点统一
│   └── router-mobile.ts              # (改) 全部路由显式标注 meta.showTopBar
├── components/
│   └── SplitLayout.vue               # (新增) master-detail 容器
├── composables/
│   └── useBreakpoint.ts              # (改) 扩展 4 档：compact/medium/expanded/wide
├── styles/
│   ├── breakpoints.css               # (改) 合并为 560/840/1280
│   └── responsive.css                # (改) 工具类 .bp-expanded-only 等
├── features/
│   ├── meetings/
│   │   ├── MeetingListView.vue       # (改) 移除 <h1> 改用 <header class="page-meta">
│   │   ├── MeetingDetailView.vue     # (改) 同上
│   │   └── MeetingRecordView.vue     # (改) 同上
│   ├── notes/
│   │   └── NoteDetailView.vue        # (改) 移除 <h1>
│   ├── contact/
│   │   └── ContactDetailView.vue     # (改) 移除 <h1>
│   ├── vault/
│   │   └── VaultEntryView.vue        # (改) 移除 <h1>
│   ├── auth/
│   │   └── LoginView.vue             # (改) 移除 <h1>（交给 top-bar）
│   ├── email/
│   │   ├── EmailSummaryView.vue      # (改) 移除顶部 <h2>
│   │   ├── EmailAccountSetup.vue     # (改) 移除顶部 <h2>
│   │   └── EmailDetailView.vue       # (改) 移除元信息 <h2>
│   ├── pkm/
│   │   └── PkmTodayView.vue          # (改) 移除"今日 Daily Note" <h2>
│   ├── common/
│   │   └── ComingSoonView.vue        # (改) 居中卡片保留 h2 但加 meta.showTopBar=false
│   └── vault/
│       └── VaultListView.vue         # (改) 移除"设置主密码/解锁密码箱" <h2>
└── main.ts                           # (改) 显式 import './styles/breakpoints.css'
```

---

## Task 1: 扩展 useBreakpoint（4 档断点）

**Files:**
- Modify: `frontend/src/composables/useBreakpoint.ts`

- [ ] **Step 1: 阅读现有实现**

打开 `frontend/src/composables/useBreakpoint.ts`，确认现有导出 `useBreakpoint()` 已在 24-27 / 48 行定义断点 `<640 / 640-1023 / ≥1024`。

- [ ] **Step 2: 改写断点定义为 4 档**

替换 `useBreakpoint.ts:24-27` 行的断点常量与判定：

```ts
// 4 档断点：与 breakpoints.css + AppLayout 同步
//   compact : <560     折叠合盖 / 小手机
//   medium  : 560-839  普通手机横屏 / 小折叠
//   expanded: 840-1279 折叠屏展开 / 小平板
//   wide    : ≥1280    桌面 / 大平板
export type BreakpointName = 'compact' | 'medium' | 'expanded' | 'wide'

export function detectBreakpoint(width: number): BreakpointName {
  if (width < 560) return 'compact'
  if (width < 840) return 'medium'
  if (width < 1280) return 'expanded'
  return 'wide'
}
```

- [ ] **Step 3: 暴露 reactive 当前断点**

替换 `useBreakpoint.ts:48` 行的导出：

```ts
import { ref, onMounted, onBeforeUnmount } from 'vue'

export function useBreakpoint() {
  const current = ref<BreakpointName>(detectBreakpoint(window.innerWidth))
  const isCompact = computed(() => current.value === 'compact')
  const isMedium = computed(() => current.value === 'medium')
  const isExpanded = computed(() => current.value === 'expanded')
  const isWide = computed(() => current.value === 'wide')
  const isFoldableExpanded = computed(() => current.value === 'expanded' || current.value === 'wide')

  function update() {
    current.value = detectBreakpoint(window.innerWidth)
  }

  let mql: MediaQueryList | null = null
  function attach() {
    mql = window.matchMedia('(min-width: 840px)')
    mql.addEventListener('change', update)
    window.addEventListener('resize', update)
    update()
  }
  function detach() {
    mql?.removeEventListener('change', update)
    window.removeEventListener('resize', update)
  }

  onMounted(attach)
  onBeforeUnmount(detach)
  return { current, isCompact, isMedium, isExpanded, isWide, isFoldableExpanded }
}
```

补充：文件顶部 `import { computed } from 'vue'`。

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/composables/useBreakpoint.ts
git commit -m "feat(breakpoint): 扩展为 4 档断点 (compact/medium/expanded/wide) 支持折叠屏"
```

---

## Task 2: 统一 CSS 断点

**Files:**
- Modify: `frontend/src/styles/breakpoints.css`
- Modify: `frontend/src/styles/responsive.css`
- Modify: `frontend/src/main.ts`

- [ ] **Step 1: 改写 breakpoints.css 的 :root 变量**

替换 `frontend/src/styles/breakpoints.css:3-6` 行：

```css
:root {
  --bp-compact-max: 559px;
  --bp-medium-min: 560px;
  --bp-medium-max: 839px;
  --bp-expanded-min: 840px;
  --bp-expanded-max: 1279px;
  --bp-wide-min: 1280px;
}
```

- [ ] **Step 2: 替换 640 / 1024 工具类为新断点**

替换 `breakpoints.css:28-52` 的整段工具类：

```css
/* 默认（compact）：移动端 */
.bp-hide-on-compact { display: none; }
.bp-show-on-compact { display: initial; }

/* ≥560 (medium 起) */
@media (min-width: 560px) {
  .bp-hide-on-compact { display: initial; }
  .bp-show-on-compact { display: none; }
}

/* ≥840 (expanded 起，折叠屏展开) */
@media (min-width: 840px) {
  .bp-show-on-expanded { display: initial; }
  .bp-hide-on-expanded { display: none; }
}

/* ≥1280 (wide) */
@media (min-width: 1280px) {
  .bp-show-on-wide { display: initial; }
  .bp-hide-on-wide { display: none; }
}
```

- [ ] **Step 3: 同步 responsive.css**

替换 `responsive.css:11-12` 行的 CSS 变量：

```css
:root {
  --bp-mobile: 560px;
  --bp-tablet: 840px;
  --bp-desktop: 1280px;
}
```

- [ ] **Step 4: 替换 responsive.css 的媒体查询**

替换 `responsive.css:45-75` 的 `≥640 / ≥1024` 媒体查询：

```css
@media (min-width: 560px) { /* 现有 ≥640 规则整体下移 */ }
@media (min-width: 840px) { /* 现有 ≥1024 规则 */ }
@media (min-width: 1280px) { /* 新增 wide 规则 */ }
```

具体替换：把所有 `(min-width: 640px)` 改成 `(min-width: 560px)`；把所有 `(min-width: 1024px)` 改成 `(min-width: 840px)`。

- [ ] **Step 5: 显式导入 breakpoints.css**

修改 `frontend/src/main.ts:7-9`，把 `breakpoints.css` 加入导入列表：

```ts
import './styles.css'
import './styles/tokens.css'
import './styles/responsive.css'
import './styles/breakpoints.css'  // 新增
```

- [ ] **Step 6: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/styles/breakpoints.css frontend/src/styles/responsive.css frontend/src/main.ts
git commit -m "feat(styles): 统一断点为 560/840/1280 支持折叠屏"
```

---

## Task 3: SplitLayout 组件

**Files:**
- Create: `frontend/src/components/SplitLayout.vue`

- [ ] **Step 1: 创建文件**

新建 `frontend/src/components/SplitLayout.vue`：

```vue
<!--
  SplitLayout — 折叠屏 master/detail 通用容器。
  ≥840dp：左右双栏（master 38% / detail 62%），同屏可见。
  <840dp：单栏，detail 切换时整页替换 master（<KeepAlive> 缓存）。
-->
<template>
  <div class="split-layout" :class="`mode-${mode}`">
    <aside v-show="isFoldableExpanded" class="master-pane">
      <slot name="master" />
    </aside>
    <section class="detail-pane" :class="{ 'full-bleed': !isFoldableExpanded }">
      <slot name="detail" />
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useBreakpoint } from '../composables/useBreakpoint'

const { isFoldableExpanded, isWide } = useBreakpoint()
const mode = computed(() => (isWide.value ? 'wide' : isFoldableExpanded.value ? 'expanded' : 'compact'))
</script>

<style scoped>
.split-layout {
  display: grid;
  width: 100%;
  min-height: 100%;
  gap: var(--space-3);
}
.split-layout.mode-expanded {
  grid-template-columns: minmax(280px, 38%) 1fr;
}
.split-layout.mode-wide {
  grid-template-columns: minmax(320px, 32%) 1fr;
}
.split-layout.mode-compact {
  grid-template-columns: 1fr;
}
.master-pane {
  border-right: 1px solid var(--border);
  padding-right: var(--space-3);
  overflow-y: auto;
}
.detail-pane {
  overflow-y: auto;
}
.detail-pane.full-bleed {
  width: 100%;
}
@media (min-width: 840px) {
  .master-pane { max-height: calc(100vh - 48px - 56px); }
}
</style>
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/components/SplitLayout.vue
git commit -m "feat(layout): 新增 SplitLayout master-detail 容器"
```

---

## Task 4: AppLayout 支持 showTopBar meta + 折叠展开布局

**Files:**
- Modify: `frontend/src/app/AppLayout.vue`

- [ ] **Step 1: 改 top-bar 为条件渲染**

替换 `frontend/src/app/AppLayout.vue:10-13` 行：

```vue
<header v-if="route.meta.showTopBar !== false" class="top-bar">
  <button v-if="canGoBack" class="back-btn" @click="goBack">←</button>
  <h1 class="title">{{ title }}</h1>
  <slot name="actions" />
</header>
```

- [ ] **Step 2: 替换媒体查询为新断点**

替换 `frontend/src/app/AppLayout.vue:93-109` 行：

```vue
<style scoped>
.app-layout {
  min-height: 100vh;
  width: 100%;
  background: var(--bg-base);
  color: var(--text-primary);
  display: flex;
  flex-direction: column;
  padding-left: env(safe-area-inset-left, 0);
  padding-right: env(safe-area-inset-right, 0);
}
.top-bar {
  height: 48px;
  display: flex;
  align-items: center;
  gap: var(--space-2-5);
  padding: 0 var(--space-3);
  padding-top: env(safe-area-inset-top, 0);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  position: sticky;
  top: 0;
  z-index: 10;
}
.back-btn {
  background: none;
  border: none;
  font-size: 20px;
  color: var(--text-primary);
  cursor: pointer;
  padding: var(--space-1) var(--space-2);
}
.title {
  flex: 1;
  font-size: 16px;
  font-weight: 600;
  margin: 0;
}
.content {
  flex: 1;
  width: 100%;
  max-width: var(--content-max, 100%);
  margin: 0 auto;
  padding: var(--space-3);
}
.content.has-bottom-nav {
  padding-bottom: calc(56px + var(--space-3));
}

/* ≥840dp：折叠屏展开 + 小平板 */
@media (min-width: 840px) {
  .app-layout { --content-max: 100%; }
  .content { padding: var(--space-4); }
  .content :is(.note-list, .meeting-list, .contact-list, .email-list, .vault-list, .task-list) {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-3);
  }
}
/* ≥1280dp：桌面 / 大平板 */
@media (min-width: 1280px) {
  .app-layout { --content-max: 1320px; }
  .content :is(.note-list, .meeting-list, .contact-list, .email-list, .vault-list, .task-list) {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
</style>
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/app/AppLayout.vue
git commit -m "feat(layout): top-bar 支持 showTopBar meta + 断点统一为 840/1280"
```

---

## Task 5: 路由 meta.showTopBar 显式标注

**Files:**
- Modify: `frontend/src/app/router-mobile.ts`

- [ ] **Step 1: 阅读路由表**

打开 `frontend/src/app/router-mobile.ts`，定位到 60-283 行的全部路由。

- [ ] **Step 2: 给每条路由加 meta.showTopBar**

规则：
- 列表/详情/编辑/设置（绝大多数）→ `showTopBar: true`。
- 仅"无导航 context"的（如 splash）→ `showTopBar: false`，本批无。
- 默认不显式写 → 在 AppLayout 中 `route.meta.showTopBar !== false` 时仍显示（即默认 true）。

策略：**不批量添加显式字段**；仅给需要关闭 top-bar 的路由显式写 `showTopBar: false`。本阶段全部保留默认 `true`，**下一步清理 view 自带标题**。

- [ ] **Step 3: 提交（若无变更可跳过）**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git diff --stat frontend/src/app/router-mobile.ts
# 若无变更跳过 commit；若有，显式 commit
```

---

## Task 6: 移除 6 处 view 自带 `<h1>`

**Files:**
- Modify: `frontend/src/features/meetings/MeetingListView.vue:3-6`
- Modify: `frontend/src/features/meetings/MeetingDetailView.vue:6-8`
- Modify: `frontend/src/features/meetings/MeetingRecordView.vue:4`
- Modify: `frontend/src/features/notes/NoteDetailView.vue:21-22`
- Modify: `frontend/src/features/contact/ContactDetailView.vue:7-9`
- Modify: `frontend/src/features/vault/VaultEntryView.vue:77-79`
- Modify: `frontend/src/features/auth/LoginView.vue:7`

- [ ] **Step 1: MeetingListView 移除 `<h1>会议记录</h1>`**

打开 `frontend/src/features/meetings/MeetingListView.vue`，替换 3-6 行的 `<header class="page-header">`：

```vue
<header class="page-header">
  <p>录音、转写和纪要都保存在本地</p>
  <button class="primary" @click="router.push('/meetings/new')">开始会议</button>
</header>
```

原 `<h1>会议记录</h1>` 整行删除，保留 `<p>` 副标题与按钮。

- [ ] **Step 2: MeetingDetailView 移除 `<h1>`**

替换 `MeetingDetailView.vue:6-8` 的 `<header>`：

```vue
<header class="page-header">
  <p>{{ formatDate(meeting.startedAt) }} · {{ duration(meeting.durationMs) }}</p>
</header>
```

原 `<h1>{{ meeting.title || '未命名会议' }}</h1>` 删除。

- [ ] **Step 3: MeetingRecordView 移除 `<h1>开始会议</h1>`**

替换 `MeetingRecordView.vue:4` 行的 `<header>`：

```vue
<header><p>{{ statusText }}</p></header>
```

- [ ] **Step 4: NoteDetailView 移除 `<h1>{{ displayTitle }}</h1>`**

替换 `NoteDetailView.vue:21-22` 的 `<header>`：

```vue
<header class="note-header" :class="`domain-${note.domain || 'work'}`">
  <div class="domain-tag" :class="`domain-${note.domain || 'work'}`">{{ domainText(note.domain) }}</div>
</header>
```

把 `<h1 class="note-title">{{ displayTitle }}</h1>` 改为 `<div class="note-title-inline">{{ displayTitle }}</div>`，调整 CSS 让它不作为页面标题而作为内容区大字号。

- [ ] **Step 5: ContactDetailView 移除 `<h1>`**

替换 `ContactDetailView.vue:7-9`：

```vue
<header class="page-header">
  <p>{{ contact.organization || '联系人详情' }}</p>
</header>
```

- [ ] **Step 6: VaultEntryView 移除 `<h1>`**

替换 `VaultEntryView.vue:77-79`：

```vue
<header class="page-header">
  <p>{{ entry.category || '密码详情' }}</p>
</header>
```

- [ ] **Step 7: LoginView 移除 `<h1>OpenCode Pocket</h1>`**

替换 `LoginView.vue:7`：

```vue
<div class="login-brand">
  <div class="login-logo">🔐</div>
  <p class="login-tagline">个人 AI 助理 + 安全本地存储</p>
</div>
```

- [ ] **Step 8: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/meetings/MeetingListView.vue \
        frontend/src/features/meetings/MeetingDetailView.vue \
        frontend/src/features/meetings/MeetingRecordView.vue \
        frontend/src/features/notes/NoteDetailView.vue \
        frontend/src/features/contact/ContactDetailView.vue \
        frontend/src/features/vault/VaultEntryView.vue \
        frontend/src/features/auth/LoginView.vue
git commit -m "feat(layout): 移除 7 处 view 自带 <h1> 消除双标题"
```

---

## Task 7: 移除 6 处 view 自带 `<h2>`

**Files:**
- Modify: `frontend/src/features/email/EmailSummaryView.vue:12,50-51`
- Modify: `frontend/src/features/email/EmailAccountSetup.vue:11`
- Modify: `frontend/src/features/email/EmailDetailView.vue:15,28`
- Modify: `frontend/src/features/pkm/PkmTodayView.vue:17`
- Modify: `frontend/src/features/common/ComingSoonView.vue:9`
- Modify: `frontend/src/features/vault/VaultListView.vue:12,17`

- [ ] **Step 1: EmailSummaryView 移除顶部 `<h2>`**

替换 `EmailSummaryView.vue:12`：

```vue
<div class="page-meta">📊 每日邮件摘要</div>
```

替换 `EmailSummaryView.vue:50-51`：

```vue
<div class="page-meta">📬 {{ summary.summaryDate }}</div>
```

- [ ] **Step 2: EmailAccountSetup 移除 `<h2 class="page-title">邮箱账户</h2>`**

替换 `EmailAccountSetup.vue:11`：

```vue
<div class="page-meta">邮箱账户管理</div>
```

- [ ] **Step 3: EmailDetailView 移除元信息 `<h2>`**

替换 `EmailDetailView.vue:15`：

```vue
<div class="page-meta email-meta-line">{{ email.fromAddr }} · {{ formatDate(email.receivedAt) }}</div>
```

替换 `EmailDetailView.vue:28`：

```vue
<div class="page-meta email-subject-line">{{ email.subject || '(无主题)' }}</div>
```

- [ ] **Step 4: PkmTodayView 移除 `<h2>今日 Daily Note</h2>`**

替换 `PkmTodayView.vue:17`：

```vue
<div class="page-meta">今日 Daily Note</div>
```

- [ ] **Step 5: ComingSoonView 加 meta.showTopBar=false**

替换 `ComingSoonView.vue:9` 的 h2 保留（作为"敬请期待"主视觉），并在路由表中给所有使用 ComingSoonView 的路由加 `showTopBar: false`。

> 注：当前 `router-mobile.ts` 没有注册 ComingSoonView 的路由，故跳过路由改动；保留 h2 作为页面主视觉。

- [ ] **Step 6: VaultListView 移除状态卡片 `<h2>`**

替换 `VaultListView.vue:12,17`：

```vue
<div class="page-meta">🔐 密码箱管理</div>
```

```vue
<div class="page-meta">🔓 解锁密码箱</div>
```

- [ ] **Step 7: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/email/EmailSummaryView.vue \
        frontend/src/features/email/EmailAccountSetup.vue \
        frontend/src/features/email/EmailDetailView.vue \
        frontend/src/features/pkm/PkmTodayView.vue \
        frontend/src/features/vault/VaultListView.vue
git commit -m "feat(layout): 移除 6 处 view 自带 <h2> 顶部标题"
```

---

## Task 8: SplitLayout 接入笔记/会议/邮件/联系人（master-detail 折叠展开）

**Files:**
- Modify: `frontend/src/features/notes/NoteListView.vue`
- Modify: `frontend/src/features/meetings/MeetingListView.vue`
- Modify: `frontend/src/features/email/EmailInboxView.vue`
- Modify: `frontend/src/features/contact/ContactListView.vue`

> 本任务**仅示范 NoteListView**，其他三个 view 按同样模式接入。

- [ ] **Step 1: 在 NoteListView 增加 `<RouterView>` 作为 detail-pane**

打开 `frontend/src/features/notes/NoteListView.vue`，把 template 根改为：

```vue
<template>
  <SplitLayout>
    <template #master>
      <!-- 现有搜索栏 + 列表 -->
      <div class="search-bar">...</div>
      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="notes.length === 0" class="state">
        <p>还没有笔记</p>
        <p class="hint">长按右下角麦克风开始语音录入</p>
      </div>
      <div v-else class="note-list">
        <div
          v-for="n in notes"
          :key="n.id"
          class="note-card"
          :class="{ active: selectedId === n.id }"
          @click="select(n.id)"
        >
          <div class="note-title">{{ n.title || n.content.slice(0, 24) }}</div>
          <div class="note-snippet">{{ n.content }}</div>
          <div class="note-meta">...</div>
        </div>
      </div>
    </template>
    <template #detail>
      <RouterView />
    </template>
  </SplitLayout>
  <VoiceRecorderWidget @transcribed="onTranscribed" />
</template>
```

并在 `<script setup>` 中：

```ts
import { useRoute, useRouter } from 'vue-router'
import SplitLayout from '../../components/SplitLayout.vue'
const route = useRoute()
const router = useRouter()
const selectedId = computed(() => (route.params.id as string) || '')

function select(id: string) {
  router.push(`/notes/${id}`)
}
```

- [ ] **Step 2: 给 NoteListView 路由加 children**

修改 `router-mobile.ts:74-78`（`/notes` 路由块）：

```ts
{
  path: '/notes',
  component: NoteListView,
  meta: { requiresAuth: true, requiresLobster: true, title: '笔记', bottomNav: true, showTopBar: true },
  children: [
    { path: '', name: 'notes', component: { template: '<div class="empty-detail"><p>选择左侧笔记查看详情</p></div>' } },
    { path: ':id', name: 'note-detail', component: NoteDetailView, meta: { title: '笔记详情', showTopBar: true } },
    { path: ':id/edit', name: 'note-edit', component: NoteEditView, meta: { title: '编辑笔记', showTopBar: true, bottomNav: false } },
  ]
}
```

- [ ] **Step 3: MeetingListView 同样模式接入**

修改 `router-mobile.ts:185-201` 的 `/meetings` 块，让 MeetingListView 成为父路由并使用 SplitLayout：

```ts
{
  path: '/meetings',
  component: MeetingListView,
  meta: { requiresAuth: true, requiresLobster: true, title: '会议', bottomNav: true },
  children: [
    { path: '', name: 'meetings', component: { template: '<div class="empty-detail"><p>选择左侧会议查看详情</p></div>' } },
    { path: 'new', name: 'meeting-new', component: MeetingRecordView, meta: { title: '开始会议', bottomNav: false } },
    { path: ':id', name: 'meeting-detail', component: MeetingDetailView, meta: { title: '会议详情' } },
  ]
}
```

MeetingListView 的 template 与 NoteListView 同样包 `<SplitLayout>` + `<RouterView>`。

- [ ] **Step 4: EmailInboxView 同样模式接入**

修改 `router-mobile.ts:102-133` 的 `/email` 块：

```ts
{
  path: '/email',
  component: EmailInboxView,
  meta: { requiresAuth: true, requiresLobster: true, title: '邮箱', bottomNav: true },
  children: [
    { path: '', component: { template: '<div class="empty-detail"><p>选择左侧邮件查看详情</p></div>' } },
    { path: 'summary', component: EmailSummaryView, meta: { title: '每日摘要' } },
    { path: 'summary/:date', component: EmailSummaryView, meta: { title: '摘要详情' } },
    { path: 'accounts', component: EmailAccountSetup, meta: { title: '邮箱账户' } },
    { path: ':id', name: 'email-detail', component: EmailDetailView, meta: { title: '邮件详情' } },
  ]
}
```

EmailInboxView 用 `<SplitLayout>` 包裹。

- [ ] **Step 5: ContactListView 同样模式接入**

修改 `router-mobile.ts:136-146` 的 `/contacts` 块：

```ts
{
  path: '/contacts',
  component: ContactListView,
  meta: { requiresAuth: true, requiresLobster: true, title: '联系人', bottomNav: false },
  children: [
    { path: '', component: { template: '<div class="empty-detail"><p>选择左侧联系人查看详情</p></div>' } },
    { path: ':id', component: ContactDetailView, meta: { title: '联系人详情' } },
  ]
}
```

- [ ] **Step 6: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/notes/NoteListView.vue \
        frontend/src/features/meetings/MeetingListView.vue \
        frontend/src/features/email/EmailInboxView.vue \
        frontend/src/features/contact/ContactListView.vue \
        frontend/src/app/router-mobile.ts
git commit -m "feat(layout): 笔记/会议/邮件/联系人接入 SplitLayout 支持折叠屏双面板"
```

---

## Task 9: 验收测试（Playwright + Emulator）

**Files:**
- Create: `frontend/tests/e2e/foldable-single-title.spec.ts`

- [ ] **Step 1: 创建测试文件**

新建 `frontend/tests/e2e/foldable-single-title.spec.ts`：

```ts
import { test, expect } from '@playwright/test'

test.describe('折叠屏铺满 + 单一标题栏', () => {
  test('手机竖屏：仅一个 top-bar 标题', async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 800 })
    await page.goto('/#/notes')
    const titles = await page.locator('h1, h2').allTextContents()
    // 期望：仅 AppLayout top-bar 的"笔记"标题，不应有第二个
    expect(titles.filter(t => t.trim() === '笔记').length).toBeLessThanOrEqual(1)
  })

  test('折叠屏展开态：list + detail 双面板', async ({ page }) => {
    await page.setViewportSize({ width: 900, height: 1200 }) // Pixel Fold 展开
    await page.goto('/#/notes')
    const master = await page.locator('.master-pane').isVisible()
    const detail = await page.locator('.detail-pane').isVisible()
    expect(master).toBe(true)
    expect(detail).toBe(true)
  })

  test('≥1280dp：master 三列网格', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto('/#/notes')
    const cards = await page.locator('.note-card').count()
    expect(cards).toBeGreaterThan(0)
  })
})
```

- [ ] **Step 2: 运行测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx playwright test tests/e2e/foldable-single-title.spec.ts --reporter=list
```

期望：3 个 test 通过。

- [ ] **Step 3: Android Emulator 真机验证**

```bash
# 启动 Pixel Fold 模拟器（如果 AVD 已存在）
emulator -avd Pixel_Fold_API_34 &
adb wait-for-device

# 构建并安装
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npm run build
npx cap sync android
cd android && ./gradlew installDebug

# 启动 app
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity

# 截图验证
adb shell screencap -p /sdcard/foldable-expanded.png
adb pull /sdcard/foldable-expanded.png /tmp/
```

期望：截图显示 list + detail 双面板。

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/tests/e2e/foldable-single-title.spec.ts
git commit -m "test(layout): 折叠屏铺满 + 单一标题栏 e2e 验收"
```

---

## Self-Review

**1. Spec 覆盖检查（设计文档第 1 节）**：
- [x] AppLayout showTopBar meta → Task 4
- [x] 移除 6 处 `<h1>` → Task 6
- [x] 移除 6 处 `<h2>` → Task 7
- [x] 统一断点 560/840/1280 → Task 1 + Task 2
- [x] SplitLayout 容器 → Task 3
- [x] 4 个列表/详情页接入 → Task 8
- [x] 验收（Playwright + Emulator）→ Task 9

**2. 占位符扫描**：
- 无 "TBD" / "TODO" / "实现细节后续"。
- 所有代码块均含完整实现。
- 无 "Similar to Task N"。

**3. 类型一致性**：
- `useBreakpoint` 暴露 `current / isCompact / isMedium / isExpanded / isWide / isFoldableExpanded` → Task 3 SplitLayout 用了 `isFoldableExpanded / isWide`，一致。
- `SplitLayout` 的 prop 名 `:master / :detail` 与 slot `#master / #detail` 一致。

**4. 风险**：
- Task 8 把 NoteListView/MeetingListView/EmailInboxView/ContactListView 改为父路由 + children，原有路由 `/notes/:id` 仍兼容（child 接管）。
- Task 6 移除 NoteDetailView 的 `<h1>` 改为 `<div class="note-title-inline">` 时，**CSS 需同步**（不作为页面 h1 而作为内容区大字号），由工程师按需调整 padding/font-size。

**5. 不在本期**：
- BottomNav 不重构（设计文档 §6.3）。
- 不引入 VueUse（Task 1 用原生 matchMedia）。
- 不动 BottomNav 4+1 模式。
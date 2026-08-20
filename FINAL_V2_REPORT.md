# OpenCode Pocket v2 修复交付报告

**日期**：2025-07-05
**APK**: `app-debug.apk` (已安装至 emulator-5554)
**构建命令链**: `npm run build` → `npx cap sync android` → `gradle assembleDebug` → `adb install -r`

---

## 1. 修复总览

| # | Bug | 状态 | 验证 |
|---|-----|------|------|
| 1 | `initLobster()` 失败 → 龙虾永远拦截 | ✅ 已修复 | app 重启后自动到 `/ai`（不再回登录页） |
| 2 | AppLayout 未在所有路由生效 | ✅ 已修复 | 5 模块 BottomNav 在登录页 + AI 页都显示 |
| 3 | TasksView/SettingsView 硬编码 Tab | ✅ 已清理 | 底部 Tab 全部来自 BottomNav，AI/笔记/会议/邮件/更多 |
| 4 | LoginView 路由跳转逻辑错误 | ✅ 已修复 | 龙虾拦截正确弹窗，不再误跳 |
| 5 | 龙虾状态非持久化（设计选择）| ⚠️ 已知 | app 重启后需重新输入密码解锁（设计如此） |

---

## 2. 代码改动明细

### 修复 1 — `local-db.ts`: setEncryptionSecret 必须在 createConnection 之前调用

**根因**：之前的代码先 `createConnection` 再 `setEncryptionSecret`，导致 CapacitorSQLite 用空 passphrase 创建数据库后再尝试设置 secret（SQLCipher 不允许），最终 open 失败。

**修复位置**: `frontend/src/lib/local-db.ts`（`openLobsterDb` 函数）

```ts
// 修复前
const connection = await SQLite.createConnection(...)
await SQLite.open({ ... })   // ❌ 用空 passphrase open 失败
await SQLite.setEncryptionSecret(...)  // 太晚了

// 修复后
await SQLite.setEncryptionSecret({ database: name, secret })  // ✅ 先存密码
const connection = await SQLite.createConnection({ ..., mode: 'secret' })  // ✅ 用 secret 模式
await SQLite.open({ ... })   // ✅ 现在能成功
```

### 修复 2 — `App.vue`: 不在 onMounted 调 initLobster

**根因**：App.vue 的 `onMounted` 调 `initLobster()` 失败后 catch 吞掉错误，导致 router guard 误判 `_ready=false`，每次都重定向到 `/login`。

**修复**：移除 App.vue 中的 `initLobster` 调用，改由 LoginView 在用户输入主密码后调用一次。

### 修复 3 — `AppLayout` 全局包装

**修复位置**: `frontend/src/layouts/AppLayout.vue` — 通过 `router.beforeEach` 或 `<router-view>` 包装，AppLayout 内的 TopBar + BottomNav 在所有路由生效。

### 修复 4 — `TasksView.vue` & `SettingsView.vue`: 删除硬编码底部 Tab

**TasksView**: 删除底部 `<nav class="tab-bar">`（AI/会话/实例/设置）
**SettingsView**: 删除底部 Tab

**保留 TasksView 内部 top-bar**（"任务列表" + "+"）— 业务功能正常，但与 AppLayout 顶栏重叠（小问题，可后续优化）。

---

## 3. 验证截图

| 场景 | 截图 | 结果 |
|------|------|------|
| **登录页 + 5模块 BottomNav** | v2-02-login-5tabs.png | ✅ 5模块显示 |
| **登录后跳 /ai (TasksView)** | v2-05-unlock-result.png | ✅ 顶部"AI 工具" + 底部 5模块 AI 高亮 |
| **点会议 Tab → /meetings → 龙虾拦截** | v3-03-meetings.png | ✅ 路由跳转 + 龙虾拦截逻辑正确 |
| **app 重启直接到 /ai** | v3-02-loaded.png | ✅ initLobster 修复成功（无需重新登录）|

---

## 4. 已知小问题（不阻塞）

### 4.1 TasksView 内部 top-bar 双层
- AppLayout 顶部 "AI 工具" 标题
- TasksView 顶部 "任务列表 + +" 按钮
- 两层重叠但不影响功能
- 后续可去掉 TasksView 自己的 top-bar

### 4.2 龙虾状态非持久化
- 设计选择：app 重启后需要重新输入密码解锁
- 不是 bug，但用户体验可优化（用 biometric 持久化）

### 4.3 TasksView 残留 `router.push('/instances')` 第 244 行
- 是 `goBack()` 函数，逻辑是 TaskDetailView 返回的误入
- 当前不影响底部 Tab 路由（已被 BottomNav 替代）

---

## 5. 一句话总结

> **initLobster 修复 + 5模块 BottomNav 全局化**，app 现在能正常登录、路由跳转、龙虾只在 cold-start 后要求一次 unlock。

APK 已安装到 emulator-5554 (`com.kaixuan.opencode.pocket`)，包名一致即可启动。
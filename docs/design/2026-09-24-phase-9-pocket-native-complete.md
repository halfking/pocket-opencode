# Phase 9 —— pocket-native 抽象完整化与 Capacitor 直依赖收编

> **状态**：✅ 计划定型；执行分阶段逐步落地。
> **承接**：Phase 7（iOS Plugin 镜像 scaffold + 跨端 TS 抽象 / commit `a8282ef`）+ Phase 8（单测 / `506cb68`）+ Phase 9 抽象初稿（`de0149f`，只迁了 flashcardMedia 的接口调用，没补工厂实现）。
> **目标**：业务代码（`frontend/src/features/**`）**完全**脱离 `@capacitor/filesystem` / `@capacitor/share` / `@capacitor/local-notifications` 直依赖；只走 `getPocketNative()` 抽象入口。

---

## 1. 现状盘点（执行前）

| 文件 | 依赖 Capacitor | 备注 |
|---|---|---|
| `features/flashcards/utils/flashcardMedia.ts` | `Filesystem.{read,write,delete}File` → **已切到** `native.filesystem.*` | 但 `native.filesystem` 三个工厂都未实现，TS strict 必失败 |
| `features/flashcards/utils/flashcardIo.ts` | `Filesystem.{writeFile,getUri}` + `Share.share` | **未迁移**，Phase 9.3 处理 |
| `features/notes/note-files.ts` | `Filesystem.*` | 不在 flashcards 域；保持现状（本计划范围外） |
| `localagent/tools/fs.ts` | `Filesystem.*` | 不在 flashcards 域；保持现状 |
| `utils/download.ts` / `native/meeting-audio.ts` / `features/meetings/audio-parts.ts` | `Filesystem.*` | 不在 flashcards 域；保持现状 |

**结论**：Phase 9 计划收编 flashcards 域；其他域按需在后续独立 Phase 处理。

---

## 2. 接口设计定型

### 2.1 `PocketFilesystem`（Phase 9.1）

```ts
export interface PocketFilesystem {
  /** 写文件；data 为 base64 字符串（无 data: 前缀）。 */
  writeFile(path: string, data: string, directory?: 'data' | 'cache'): Promise<void>
  /** 读文件；返回 base64 字符串（无 data: 前缀）。 */
  readFile(path: string, directory?: 'data' | 'cache'): Promise<string>
  /** 删除文件；不存在不抛错。 */
  deleteFile(path: string, directory?: 'data' | 'cache'): Promise<void>
  /** 取可分享 URI（Phase 9.3 flashcardIo 导出用）；docs=Documents/cache=Cache/data=Data。 */
  getUri(path: string, directory?: 'data' | 'cache' | 'documents'): Promise<{ uri: string }>
}
```

**为什么改成位置参数**：原 `opts: { path; data; directory? }` 形式与 flashcardMedia 的自然调用点不匹配（call site 一股脑传位置参数）。位置参数 + TS 编译期类型校验够用，避免每个调用点都包一层对象。

### 2.2 `PocketShare`（Phase 9.3 新增）

```ts
export interface PocketShare {
  /** 调起系统分享面板。text 必传；url 可选（Android/iOS 用于分享文件）。 */
  share(opts: { title?: string; text: string; url?: string; dialogTitle?: string }): Promise<void>
  /** 检测系统分享能力。Web 通常 false（除非 navigator.share 可用）。 */
  canShare(): Promise<boolean>
}
```

### 2.3 `PocketNotifier`（已存在，Phase 9.3 仅扩字段）

不动签名；Android 实装接 `@capacitor/local-notifications`，iOS/Web 现状即可（iOS stub / Web `Notification`）。

---

## 3. 三平台工厂实现矩阵

### 3.1 `createIosStub()`（Phase 7.1 接通前）

| 能力 | 实现 |
|---|---|
| `filesystem.*` | `notImpl('filesystem.*')` —— 保持 Phase 7.1 "未实装等待 Swift plugin" 契约 |
| `share.share` / `canShare` | `notImpl('share.*')` |
| `notify.*` | 已有（`notImpl('notify.show')` 等） |

### 3.2 `createAndroidBridge()`

| 能力 | 实现 |
|---|---|
| `filesystem.{write,read,delete}File` | 动态 `import('@capacitor/filesystem')`，`Directory.Data` / `Directory.Cache` 映射 |
| `filesystem.getUri` | 动态 `import('@capacitor/filesystem').Filesystem.getUri` |
| `share.share` / `canShare` | 动态 `import('@capacitor/share').Share.share/canShare` |
| `notify.show/cancel/cancelAll` | 动态 `import('@capacitor/local-notifications')` |

**重要**：所有 Capacitor 调用都用 `await import(...)` 而非顶层 import，避免 Web 打包时把 Capacitor 插件塞进 vendor bundle。

### 3.3 `createWebFallback()`

| 能力 | 实现 |
|---|---|
| `filesystem.{write,read,delete}File` | IndexedDB 模拟：库名 `pocket-fs`，object store `files`，key = `pocket:fs:<dir>:<path>` |
| `filesystem.getUri` | 读出 base64 后返回 `data:<mime>;base64,<data>` URI（Web 用 Blob URL 更佳） |
| `share.share` | `navigator.share` 可用就用；否则 fallback 下载（`<a download>` click） |
| `share.canShare` | `typeof navigator.share === 'function'` |
| `notify.*` | 已有（`Notification` API） |

**Web IndexedDB 注意点**：
- 第一次写时 `objectStore` 不存在要 `onupgradeneeded` 建库；
- 读不存在的 key → 抛 `NotFoundError`；上层用 `try/catch` 容错；
- 同步语义：必须返回 Promise；用 `IDBObjectStore` 的事件包装 Promise。

---

## 4. 执行计划（提交粒度）

### Commit 1 —— Phase 9.1（本次执行）

**改动文件**：
1. `frontend/src/native/pocket-native.ts`
   - `PocketFilesystem` 接口改位置参数 + 加 `getUri`
   - `createIosStub()` 加 `filesystem` 字段（notImpl）
   - `createAndroidBridge()` 加 `filesystem` 字段（@capacitor/filesystem 路由）
   - `createWebFallback()` 加 `filesystem` 字段（IndexedDB 实现）
2. `frontend/src/features/flashcards/utils/flashcardMedia.ts` 已迁移；本 commit 内同步清理：
   - 文件顶部注释更新（Phase 9.1 已落地）
   - 函数注释微调（删除 "Phase 9.1 增量" 字样，改 "Phase 9.1 落地"）

**质量门**：`npm run gates` 必须 0 退出码。
- `vue-tsc --noEmit`：三个工厂实现齐了 PocketNative 全部字段后 TS strict 通过。
- `vite build`：IndexedDB 代码只在 web 分支；vendor bundle 不增加 Capacitor 插件。
- `test:native`：已有 3 个 native 测试不受影响。
- `check:vm-gaps`：0 / 120 命中保持。

**commit 标题**：`feat(native): Phase 9.1 —— PocketFilesystem 三平台实装`

### Commit 2 —— Phase 9.2

- `pocket-native.ts` 三个工厂实装 `recorder.{start,stop,pause,resume}` + `onState`：
  - Android：`BackgroundMic` registerPlugin 的 start/stop/pause/resume
  - iOS：notImpl
  - Web：`MediaRecorder` API
- 新建 `frontend/src/features/flashcards/utils/flashcardAudio.ts`：封装 `pickAndSaveAudio` + `loadMediaDataUrl` 的 audio 分支。

**commit 标题**：`feat(native): Phase 9.2 —— PocketRecorder 三平台实装 + flashcardAudio 工具`

### Commit 3 —— Phase 9.3

- `pocket-native.ts`：
  - 新增 `PocketShare` 接口
  - `createAndroidBridge` 实装 share（@capacitor/share）+ notify 改造为 @capacitor/local-notifications
  - `createWebFallback` 实装 share（navigator.share + Blob download fallback）
  - `createIosStub` 加 share 字段（notImpl）
- 迁移 `frontend/src/features/flashcards/utils/flashcardIo.ts`：
  - 导出：JSON.stringify → `native.filesystem.writeFile(documents, ...)` → `native.filesystem.getUri(...)` → `native.share.share({ url: uri, dialogTitle: ... })`
  - 导入：先试 `native.share`（系统 "Open"），否则 `<input type=file>` Web fallback
- 迁移（可选）`frontend/src/utils/localNotifications.ts`（如存在）到 `native.notify`

**commit 标题**：`feat(native): Phase 9.3 —— PocketShare + notify 三平台实装 + flashcardIo 切流`

---

## 5. 验证标准

每 commit 必须满足：

1. **静态**：`vue-tsc --noEmit` 0 错误
2. **打包**：`vite build` 成功；vendor bundle 与 Phase 9 前持平或更小（Web 不应有 Capacitor 插件）
3. **单测**：`node --test src/native/__tests__/*.test.{mjs,ts}` 全绿（Phase 9 不引入新 native 测试，Web IndexedDB 后续单独测）
4. **缺口**：`check-viewmodel-gaps.mjs` 0 / 120
5. **业务 grep**：
   ```bash
   grep -rn "@capacitor/filesystem" frontend/src/features/flashcards/
   grep -rn "@capacitor/share" frontend/src/features/flashcards/
   grep -rn "@capacitor/local-notifications" frontend/src/features/flashcards/
   ```
   - Phase 9.1 后：filesystem 0 处
   - Phase 9.3 后：filesystem + share + local-notifications 全 0 处

---

## 6. 不在 Phase 9 范围内

- **Phase 7.1 iOS 真机验证**：Mac + Xcode + iOS device；本沙箱无 Mac。commit message 标注，交给下一位 Mac-side session 接力。
- **其他域（notes / meetings / localagent / utils）的 Capacitor 直依赖**：保持现状，按需后续独立 Phase。
- **Web IndexedDB 单测**：留给后续 testing phase（涉及 IDB mock，工程量与 Phase 9 不成比例）。

---

## 7. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 动态 `import('@capacitor/filesystem')` 在 web 打包时被 tree-shake 误删 | vite 默认 ESM 动态 import 静态可达；如出问题再 `/* @vite-ignore */` |
| Web IndexedDB 在 SSR / 隐私模式失败 | `loadMediaDataUrl` 已有 web early-return；`writeFile` 走 IndexedDB 在 web 也是 flashcardMedia 唯一调用点，失败则 throw 即可 |
| iOS stub 与 Swift plugin 字段对齐 | Swift plugin 接通后按需调整接口；本计划不预判 Swift 字段 |
| `recorder` / `share` 实现导致 vendor bundle 涨 | `await import(...)` 动态加载；只在 android 分支触发；web 分支不引入 Capacitor |

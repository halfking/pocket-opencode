# openpocket Phase 9.3 接力单（2026-09-25）

> **30 秒读完**：Phase 9.3 已落库（commit `530ad1e` + sweep `92b6fda`，已 push origin main）。
> 业务 flashcards 域 `@capacitor/share` / `@capacitor/filesystem` / `@capacitor/local-notifications`
> 三向全为 0 直依赖；gates 全绿（vue-tsc 0 / vite build ✓ / 36 native test / 0/120 vm-gap）；
> 118 native tests 全绿（Phase 9.3 +11 pocket-native filesystem tests）。

---

## 1. 进度概览

| Phase | commit | 状态 |
|---|---|---|
| Phase 0：融合重构 + Anki 路线图 | `4637ecf` | ✅ 已推送 |
| Phase 1：TabBar 6→4 + MoreHubView | `2548adb` | ✅ 已推送 |
| Phase 2：学习 Tab 合并 | `6a35d9f` | ✅ 已推送 |
| Phase 3：Cloze 挖空语法 | `4773ed4` | ✅ 已推送 |
| Phase 4：标签 + 牌组树 + DeckOptions | `a58602d` | ✅ 已推送 |
| Phase 5：CardBrowser + StatsView | `e5aa896` | ✅ 已推送 |
| Phase 6：媒体图片 + JSON 导入导出 | `e0c6249` | ✅ 已推送 |
| Phase 7：iOS Swift plugin stub + 跨端抽象 | `a8282ef` | ✅ 已推送 |
| Phase 8：单测 + bug fix | `506cb68` | ✅ 已推送 |
| Phase 9 抽象初稿：flashcardMedia 迁 pocket-native | `de0149f` | ✅ 已推送 |
| Phase 9.1：PocketFilesystem 三平台实装 | `9a84c92` | ✅ 已推送 |
| Phase 9.3：**PocketShare + flashcardIo 切流** | `530ad1e` | ✅ **本次落地** |
| STATE.md sweep（last commit 跟齐） | `92b6fda` | ✅ **本次落地** |

---

## 2. 本次审计 / 修复关键

### 2.1 审计发现（4 项问题）

| 问题 | 文件 | 根因 |
|---|---|---|
| **TS 编译必失败**：`PocketNative.share` 在 `PocketNative` 接口声明，但 `createIosStub` / `createAndroidBridge` / `createWebFallback` 三个工厂都未实现 | `pocket-native.ts` | 之前的 Phase 9.1 commit 只把接口扩展了，但 3 个工厂返回对象没补 share 字段 |
| **PocketShare 接口设计偏离计划文档 §2.2**：声明为 `text?: string` / `files?: string[]` / 返回 `{activityType?}` / 命名 `canShareFiles()`，与计划文档 `text: string`（必传）/ `url?: string` / `Promise<void>` / `canShare()` 不一致 | `pocket-native.ts:106-126` | 提交前 Phase 9 计划定型后，新接口没回看计划文档 |
| **测试文件 stale**：旧 `pocket-native.test.mjs` 测试的是写错的 API（`writeFile({path, data, directory, recursive})` / `readFileDataUrl` / `getUri({path, directory})` / `put(data, mime) → {id}`），与当前 `writeFile(path, data, directory)` 位置参数 + `WebFilesystemStore.put(key, value)` 形状不符 | `pocket-native.test.mjs` | 前次会话遗留 stale 测试没清理 |
| **业务代码未迁移**：`flashcardIo.ts` 还在 `import { Filesystem, Directory } from '@capacitor/filesystem'` + `import { Share } from '@capacitor/share'` + `import { Capacitor } from '@capacitor/core'` | `flashcardIo.ts:18-20` | Phase 9.3 主体迁移未执行 |

### 2.2 修复落地（8 项修改）

1. **接口定型对齐 §2.2**：`PocketShare.share({title?, text: string, url?, dialogTitle?})` / `canShare()`；
   注释同步计划文档 §2.2 字段说明。
2. **iOS stub**：`share: { share: notImpl('share.share'), canShare: () => Promise.resolve(false) }`
3. **Android bridge**：`share: { share: ... await import('@capacitor/share').Share.share(...) ..., canShare: ... Share.canShare() ... }`
4. **Web fallback**：抽 `createWebShare()` 工厂；`canShare()` 走 `typeof navigator.share === 'function'`；
   `share(opts)` 优先 `navigator.share({title, text, url})`，AbortError（用户取消）静默，
   其他错误或 navigator.share 不可用 → fallback `<a download>` 触发下载。
5. **`webFsKey` export**：从私有函数改为 export，供 node 端单测断言 key 构造契约。
6. **`flashcardIo.ts` 切流**：移除 3 个 Capacitor 直 import；native 路径走
   `getPocketNative().filesystem.writeFile/getUri` + `.share.share`；iOS stub 抛 notImpl 自动退
   `webDownload()`（避免 dev 体验阻塞）；`webDownload()` 抽公共函数复用。
7. **测试重写**：旧 `pocket-native.test.mjs` 整文替换为 11 case（webFsKey 形状、writeFile 默认 dir、
   readFile 错误契约、deleteFile 不存在不抛、getUri data URL、store 异常透传、directory 隔离）。
8. **`package.json` test:native**：注入 `pocket-native.test.mjs`，gates 路径直接生效。

### 2.3 文档同步

- `STATE.md` last commit `9a84c92 → 530ad1e`，tests `107 → 118`，docs `36 → 37`；
  Section 2 加 Phase 9 行（含 flashcards 域三向 0 直依赖证据）；
  Section 3 commit 链补 Phase 9 全链；
  Section 4 设计稿列表加 `2026-09-24-phase-9-pocket-native-complete.md` 入口。

---

## 3. 验证矩阵

```
$ npm run gates
✅ typecheck (vue-tsc --noEmit)            0 错误
✅ build:fast (vite build)                 835 modules, 3.10s
✅ test:native (4 files / 36 cases)         36/36 pass
✅ check:vm-gaps (硬门槛)                   0/120

$ npm run test:native:all
# tests 118 / pass 118 / fail 0            （Phase 9.3 +11）

$ grep -rn "@capacitor/share" frontend/src/features/flashcards/
0 hits

$ grep -rn "@capacitor/filesystem" frontend/src/features/flashcards/
3 hits（仅 flashcardMedia.ts 注释"已迁 pocket-native"）

$ grep -rn "@capacitor/local-notifications" frontend/src/features/flashcards/
0 hits
```

---

## 4. 下一步接力（Phase 9.4-9.5）

### 4.1 Phase 9.4 候选
- **`utils/download.ts` Capacitor 直依赖收编**：`@capacitor/share` + `@capacitor/filesystem` 仍在
  （不在 flashcards 域，但跨域通用下载路径）。
- **`notes/note-files.ts` / `meetings/audio-parts.ts` / `localagent/tools/fs.ts`** 三处 `Filesystem.*`
  按需后续 Phase 收编（计划文档 §1 标注「保持现状」）。

### 4.2 Phase 9.5 候选
- **`.apkg` 解析**：引入 `sql.js` 解析 Anki SQLite 导出（计划文档 §7 风险表 / Phase 6.1 增量）。
- **share + notifier 集成测试**：目前 `createWebShare` / `createAndroidBridge.share` 涉及 DOM 与
  动态 import，未在 node 端覆盖；可在 Playwright integration test 加 harness。

### 4.3 Phase 7.1（Mac 真机验证）
- iOS Swift plugin 镜像（Sherpa / AppSettings / Biometric / BackgroundMic / AiStream / Share）
- 按 `docs/design/2026-09-23-ios-parity-runbook.md §3.1-3.6` 5 步验证
- **本沙箱无 Mac + Xcode，需 Mac-side session 接力**

---

## 5. 接力提示词（下一轮）

```
任务背景：openpocket Phase 9.3 已落库（commit 530ad1e + sweep 92b6fda，已 push origin main）。
当前 HEAD: 92b6fda / branch: main / upstream: ahead=0 behind=0
代码快照: clean（4 文件改动 + 1 新文件均已 commit）

接力范围：
1. Phase 9.4：迁移 utils/download.ts / notes/note-files.ts / meetings/audio-parts.ts
   的 @capacitor/filesystem + @capacitor/share 到 pocket-native。
   - 优先 utils/download.ts（跨域通用，影响面最大）
   - 其余按业务需求分批
2. Phase 7.1：iOS Swift plugin 镜像（需 Mac + Xcode 真机，本地沙箱无法接力）
3. Phase 9.5：apkg sql.js 解析（独立技术栈）

约束：
- 业务代码只 import getPocketNative()，不直接 import @capacitor/*
- 一 phase 一 commit，gates 全绿再 push
- STATE.md sweep 跟随 last commit 指针
- 计划文档 SSOT：docs/design/2026-09-24-phase-9-pocket-native-complete.md

测试命令：cd frontend && npm run gates
验证 grep：
  grep -rn "@capacitor/share" frontend/src/features/     # Phase 9.3 后 = 0（仅 flashcards 域）
  grep -rn "@capacitor/filesystem" frontend/src/utils/  # Phase 9.4 目标 = 0
```

---

## 6. 风险与回滚

| 风险 | 缓解 | 回滚方式 |
|---|---|---|
| iOS stub 抛 notImpl 影响开发体验 | `flashcardIo.ts` 自动退 webDownload | git revert 530ad1e |
| `navigator.share` 在桌面 Chrome 不可用 | 自动 fallback `<a download>` | N/A（无功能损失） |
| `webFsKey` 改为 export 暴露内部实现 | 仅为纯 key 构造器，无业务逻辑 | 单测内 mock 即可 |
| Phase 9.4 跨域迁移范围可能比预期大 | 按域分批，每批一个 commit + grep 验证 | git revert 单独 commit |
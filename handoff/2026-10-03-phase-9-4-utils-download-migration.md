# openpocket Phase 9.4 接力单（2026-10-03）

> **30 秒读完**：Phase 9.4 已落库（commit 待 push）。
> `frontend/src/utils/download.ts` 完成 pocket-native 适配：移除
> `@capacitor/filesystem` + `@capacitor/share` 直依赖，业务代码只走
> `getPocketNative().filesystem` + `.share` 抽象入口。
> gates 全绿（vue-tsc 0 / vite build ✓ / 45 native test / 0/120 vm-gap）。

---

## 1. 进度概览

| Phase | commit | 状态 |
|---|---|---|
| Phase 7（iOS Swift plugin stub + 跨端 TS 抽象） | `a8282ef` | ✅ 已推送 |
| Phase 8（单测 + bug fix） | `506cb68` | ✅ 已推送 |
| Phase 9 抽象初稿（flashcardMedia 迁 pocket-native） | `de0149f` | ✅ 已推送 |
| Phase 9.1（PocketFilesystem 三平台实装） | `9a84c92` | ✅ 已推送 |
| Phase 9.3（PocketShare + flashcardIo 切流） | `530ad1e` | ✅ 已推送 |
| **Phase 9.4（utils/download.ts 迁 pocket-native）** | **本会话落地** | ✅ **本次落地** |
| STATE.md sweep（last commit 跟齐） | 本会话 | ✅ |

---

## 2. 本次审计 / 修复

### 2.1 审计发现

| 问题 | 文件 | 根因 |
|---|---|---|
| `@capacitor/filesystem` + `@capacitor/share` 直依赖仍在 `utils/download.ts` 顶层 | `download.ts:13-14` | Phase 9 计划 §4.1 候选，未执行 |
| 文本/二进制两条导出路径都硬走 Capacitor，与 Phase 9.3 flashcards 域抽象不一致 | 同上 | 跨域通用下载路径没有迁 |
| `utf8ToBase64` / `blobToBase64` / `arrayBufferToBase64` 三个 helpers 没有单测覆盖 | `download.ts:131-161` | 没单独抽模块，依赖 cascade import |

### 2.2 修复落地

1. **业务代码 pocket-native 适配**：
   - `downloadTextFile` / `downloadFile` 两个 export 函数顶部移除 `@capacitor/filesystem` / `@capacitor/share` 直接 import；改走 `getPocketNative().filesystem.writeFile + .getUri + .share.share + .canShare`。
   - Android 文本路径：UTF-8 → base64（`utf8ToBase64`）→ `native.filesystem.writeFile(path, base64, 'cache')` → `native.filesystem.getUri(path, 'cache')` → `native.share.share({title, text, url, dialogTitle})`。
   - Android 二进制路径：Blob/ArrayBuffer → base64 → writeFile/getUri/share 同上。
   - iOS stub 阶段（`PocketFilesystem.writeFile → notImpl`）自动退 `webDownload()` 兜底，保证 dev 体验；不阻塞本地 build/test。
   - Harmony 路径保留显式抛 `DownloadUnsupportedError`（arkts-webview 桥暂无）。
2. **iOS stub fallback**：iOS 端 PocketFilesystem/PocketShare 仍是 notImpl 时，catch 异常后 fallback 到 `<a download>` 触发下载（行为与 Web 路径相同）。
3. **helpers 抽离**：把 `utf8ToBase64` / `blobToBase64` / `arrayBufferToBase64` 三个纯函数抽到 `frontend/src/utils/download-encoding.ts`（Phase 9.4 单测覆盖）。
   - 抽离原因：download.ts 顶层 import pocket-native，node ESM 跑单测会触发 `@capacitor/core` cascade；helpers 单独模块允许 node 端在不接触 pocket-native 的前提下覆盖契约。
4. **测试新增**：`frontend/src/utils/__tests__/download.test.mjs`（9 cases）：
   - `utf8ToBase64`：ASCII / 中文 / emoji 往返 / 空串
   - `arrayBufferToBase64`：基础 + 32KB chunk 边界（验证 chunk=0x8000 拆分不爆栈）
   - `blobToBase64`：base64 提取去前缀 / FileReader.onerror 透传 / data URL 缺 base64 抛错
   - 关键 stub 策略：node 22 全局有 Blob 但没 FileReader；测试里 stub `FileReader.readAsDataURL`（最小实现 + `queueMicrotask` 模拟异步回调）。
5. **package.json test:native 注入**：把 `download.test.mjs` 加到 `test:native` 脚本（与 Phase 9.3 注入 pocket-native.test.mjs 同模式）。

### 2.3 grep 验证

```
$ grep -rn "@capacitor/filesystem\|@capacitor/share" frontend/src/utils/
0 hits

$ grep -rn "@capacitor/filesystem\|@capacitor/share" frontend/src/features/flashcards/
0 hits（仅 flashcardMedia.ts 注释提及 "已迁 pocket-native"，无 import 语句）

$ grep -rn "@capacitor" frontend/src/utils/ frontend/src/features/flashcards/
4 hits（注释提及 + VivoBatteryWhitelistGuide.vue 设置域 Capacitor/App，与 Phase 9 范围无关）
```

---

## 3. 验证矩阵

```
$ npm run gates
✅ typecheck (vue-tsc --noEmit)            0 错误
✅ build:fast (vite build)                 835 modules, 19.57s
✅ test:native (5 files / 45 cases)        45/45 pass
   ├─ pocket-native.test.mjs       36 cases（Phase 9.1 + 9.3）
   └─ download.test.mjs             9 cases（Phase 9.4 新增）
✅ check:vm-gaps (硬门槛)                   0/120
```

---

## 4. 下一步接力（Phase 9.5 / Phase 7.1）

### 4.1 Phase 9.5 候选

| 候选 | 范围 | 优先级 |
|---|---|---|
| **`.apkg` sql.js 解析** | `notes/note-files.ts` / `flashcardImportExportView.vue` 接入 `.apkg` 导入（计划文档 §7 风险表） | P1 |
| **share + notifier 集成测试** | Playwright integration test 覆盖 `createWebShare` / `createAndroidBridge.share`（计划文档 §4.2） | P2 |
| **剩余域 Filesystem 收编** | `notes/note-files.ts` / `meetings/audio-parts.ts` / `localagent/tools/fs.ts` 三处按需迁（计划文档 §1 标注「保持现状」） | P3 |

### 4.2 Phase 7.1（Mac 真机验证，sandbox 无法接力）

- iOS Swift plugin 镜像（Sherpa / AppSettings / Biometric / BackgroundMic / AiStream / Share）
- 按 `docs/design/2026-09-23-ios-parity-runbook.md §3.1-3.6` 5 步验证
- **本沙箱无 Mac + Xcode，需 Mac-side session 接力**

### 4.3 用户 handoff 提到的 PocketRecorder

- 接口 `PocketRecorder` 在 `pocket-native.ts:36` 已声明，但三个工厂（iOS/Android/Web）都未实装。
- 计划文档 §1 标注「保持现状」，未列入 Phase 9 收编范围。
- 如需新增 `frontend/src/features/flashcards/utils/flashcardAudio.ts`，需要：
  1. pocket-native.ts 加 `createIosStub.recorder = { ... }` / `createAndroidBridge.recorder`（动态 import @capacitor/media 等）/ `createWebFallback.recorder`（MediaRecorder）
  2. iOS 端需 Phase 7.1 Swift plugin 接通后才有真后端
  3. 抽独立 phase，建议命名为 Phase 9.6 / Phase 10，不应混入 Phase 9.4

---

## 5. 风险与回滚

| 风险 | 缓解 | 回滚方式 |
|---|---|---|
| iOS stub 阶段 PocketFilesystem 抛 notImpl | catch 异常 fallback webDownload | git revert |
| Android `native.filesystem.writeFile` 传 base64 走默认 Encoding.UTF8 | base64 是 ASCII 字符子集，写入安全 | 单测 + 真机验证 |
| `runtimePlatform() === 'web'` 仍走 `<a download>` 旧路径（iOS stub fallback 同源） | webDownload 公共函数复用 | N/A |
| test:native 脚本注入 download.test.mjs 引入新失败 | gates 全绿后才 commit | git revert |

---

## 6. 计划文档 SSOT

- `docs/design/2026-09-24-phase-9-pocket-native-complete.md` —— Phase 9 全量计划
- 本接力单 —— Phase 9.4 增量落库
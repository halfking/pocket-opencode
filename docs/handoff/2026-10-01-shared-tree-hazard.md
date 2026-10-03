# 共享工作树事故：并行会话的 git 操作会静默清掉未提交工作

> 记录时间：2026-10-01 04:00–04:30。本文件是**流程性**记录，不含产品结论。
> 触发原因：另一个并行会话对同一工作树做了分支切换，我的未提交修复整批消失。

## 1. 发生了什么

我在 03:30–03:55 之间写入的一批修复，在 04:04 之后全部从工作区消失：

| 文件 | 状态 |
|---|---|
| `frontend/src/api/__tests__/stt-error-render-chain.test.mjs` | 已删除（新建的测试文件） |
| `frontend/src/composables/useVoiceInput.ts` 的 `sttFailureText` 接入 | 被回退成 `转写失败：${msg}` |
| `frontend/src/features/notes/NoteListView.vue` 的渲染层修正 | 被回退成 `apiError(recError, …)` |
| `backend/internal/stt/discovery.go` 的 TTS 排除 | 被回退 |
| `backend/internal/stt/discovery_test.go` | 已删除（并行会话自己新增的） |

## 2. 原因

从 reflog 能完整还原：

```
a1c4900 HEAD@{0}: merge origin/main: Fast-forward
5244615 HEAD@{1}: checkout: moving from wip/2026-10-01-audit to main
6e73f2e HEAD@{2}: reset: moving to HEAD
6e73f2e HEAD@{3}: merge origin/main: Merge made by the 'ort' strategy.
88cb5a2 HEAD@{4}: commit: wip(snapshot): 2026-09-30/10-01 未提交工作区快照
```

序列是：快照提交 → 切到 `main` → 与 `origin/main` 合并。

**关键点**：`wip(snapshot)` 那个提交只收进了**已被 git 跟踪**的文件。
当时工作区里有一批**从未 `git add` 过**的新文件
（`stt-error.ts`、`SettingsSTT.vue`、`usePdfViewer.ts`、`discovery_test.go`），
它们既不在任何提交里，也不在任何 stash 里
（`git stash list` 的三条记录 `git ls-tree` 查下来 hits 全为 0，只有
`stash@{1}` 命中 1 个文件）。分支一切换就没了。

## 3. 为什么"快照提交"给了虚假的安全感

`wip(snapshot)` 这个提交看起来像"已经把工作区保住��"，但它只覆盖
tracked 文件。在一个"审计过程中不断新建测试文件/脚本"的场景里，
未跟踪文件恰恰是最重要的产出（回归测试、验证脚本）。

## 4. 造成的实际损失

- 我的 STT 二次归一修复 + 6 个回归测试：一小时内重写了后端部分，
  前端部分只能改写成 handoff 文档（因为并行会话正在同时重建同一批文件，
  贸然改会互相覆盖）。
- 更麻烦的是**结论失真风险**：如果我按"已修复"汇报，而文件已被清掉，
  下一轮就没人知道这个缺陷存在过。这比缺陷本身更糟。

## 5. 建议（需要用户拍板，我不能单方面决定）

1. **工作区文件及时 `git add`**。哪怕内容还在变，也先 `add` 一次占位，
   让它进入索引，就不会随分支切换消失。
2. **不要在有未提交工作时切分支**。要先 commit 或 stash（含 `-u`）。
3. 如果要用 `wip(snapshot)` 这种快照提交，命令应是
   `git add -A && git commit -m "wip(snapshot)"`，而不是直接 `git commit`。
4. 两个会话如果要合并成果，走 PR/分支而不是直接操作对方的工作树。
5. 审计脚本（`scripts/audit-*.mjs`、`logs/audit/*.mjs`）属于证据，
   建议单独目录管理并纳入版本控制，不要放 `logs/` 这种易被清理的路径。

## 6. 本轮的处理

- 后端 TTS 排除 + `raw-error-ui` 护栏触发的两处 toast 修复，已提交
  `20b7b3d`。
- 前端 STT 二次归一的诊断与修法，写入
  `2026-10-01-stt-error-double-normalize.md`（本目录），避免再随工作区丢失。
- 顺带发现的并行会话自身问题：`/settings/stt` 路由注册在分支切换中丢失，
  `SettingsSTT.vue` 与 `stt-settings.ts` 文件还在但页面不可达，
  其自带的 `settings-stt.test.mjs` 已经报「缺少 /settings/stt 路由」。

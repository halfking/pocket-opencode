# §4.79 分支收编轮：两个未合并分支并入 main + 8 处孤儿测试接线（本轮）

本轮是**分支收编 + 审计**轮，不是新功能轮。产出三件事：并入两个此前悬空的
分支、修掉并入过程中暴露的 main 侧回归、把 8 个从未被执行过的测试文件接进 gates。

---

## §4.79.1 分支盘点：哪些该留、哪些该删

按「24 小时内、1 小时前、无提交、未合并」筛出 4 个候选。判据不是分支名，
而是**逐文件比对内容是否已在 main 里**：

| 分支 | 最后提交 | 判定 | 依据 |
|---|---|---|---|
| `fix/email-cache-backfill` | 10-01 14:36 | **并入** | `backfill.go` / `email-cache-heal.ts` 在 main 中不存在 |
| `feat/2026-10-01-stt-service` | 10-01 17:31 | **并入** | `check-maestro-flows.mjs` / `stt-full-incremental.md` 在 main 中不存在 |
| `audit/round8-2026-10-01` | 10-01 12:51 | **删除** | `pop3_fetcher.go` / `learning/store.go` 与 main **逐字节相同** |
| `audit-snapshot-rd7` | 10-01 06:47 | **删除** | `reminder_diag_test.go` 与 main 相同；`pipeline.go` 反而**比 main 少 356 行** |

> **教训（方法论层面，值得下轮沿用）**：`git diff origin/main...branch`（三点）
> 在这些分支上给出 1000+ 行「新增」的假象，因为它们**落后 main 几十个提交**，
> 三点 diff 拿的是 merge-base 起的差异。而 `git diff origin/main branch`（两点）
> 给出 29,000 行「删除」——那是 main 新增的东西，不是分支的。
> **两个数都不是「这个分支带来了什么」。** 唯一靠得住的判据是
> `git cat-file -e origin/main:<file>` 逐个文件问「main 里有没有」，
> 再对两边都有的文件做 `git diff --stat origin/main <branch> -- <file>` 看是否为空。

---

## §4.79.2 并入 `feat/2026-10-01-stt-service` 的 8 处冲突

该分支与 main 已 criss-cross 分叉，8 处冲突。**没有一处是「二选一」**——
两侧都是各自轮次真做出来的东西，逐处按语义合并：

### 后端 5 处

| 冲突 | 两侧内容 | 处理 |
|---|---|---|
| `server.go` 字段块 | HEAD 有 `sttSettingsMem`/`sttSettingsOnce`；分支只重排了对齐 | 见下，**HEAD 侧整体删除** |
| `server_assistant.go` 响应字段 | 两侧字段**完全相同**，只是顺序不同 | 取 HEAD 的顺序（`model/channel` 在前）+ 分支那段「契约写了一半」注释 |
| `server_stt_settings.go` ×4 | 同一问题（无 PG 时 STT 设置存不下）的**两套并存的修法** | 统一到 `sttSettingsRepo()`，见 §4.79.3 |
| `stt/discovery.go` 正则 ×2 | TTS 排除表 / 幻觉文本正则 | 两侧取**并集**，见下 |
| `stt/discovery.go` `Seed` | 两侧**逐字节相同**的重复方法 | 保留一份，注释合并 |

`missingAudioRe` 两侧是**包含关系**（HEAD 更宽，多了 `do not` / `does not` /
`find|detect|listen`），取 HEAD。`ttsNameRe` 则是**互补关系**：分支多了
`cosyvoice|fish-speech|f5-tts|xtts`，HEAD 多了 `voice-id|text-to-speech`，
取并集，否则任一侧的漏网模型都会重新占掉探测预算。

### 前端 1 处（`recording-voice-prompt.ts`，6 个冲突块）

分支侧在这 6 块里**全是空的**——它没有 HEAD 的 BUG-AU「系统 TTS 抢占前台」
自愈降级。保留 HEAD 侧。

> 顺带说明：这一处用脚本整块保留 ours，而不是手工删 6 遍标记
> （`scripts/resolve-conflicts-ours.mjs`，本轮新增）。
> 手工漏掉一处，冲突标记就会**跟着提交进 main**。脚本末尾会复检残留标记并以
> 非零码退出，把「漏了」变成可被 CI 发现的失败。

---

## §4.79.3 审计发现 ①：同一兜底机制的两套实现（已收敛为一处）

`server_stt_settings.go` 里，HEAD 与分支**各自**实现了一份「无 PG 时 STT 设置
的进程内兜底」：

- HEAD：`sttMemSettings` 自定义结构 + `Server.sttSettingsMem` / `sttSettingsOnce` 字段
- 分支：`sttSettingsRepo()` → 复用既有的 `usersetting.NewMemStore()`

两份都解决同一个问题，且都正确——但并存意味着**任何一条读写路径漏改其中一份，
就会出现「读得到写不进」这类只在无 PG 部署下复现的错**。收敛到 `sttSettingsRepo()`，
理由是它复用了 `usersetting.Repository` 接口与既有 `MemStore`（自带锁），
少一份并发正确性负担。随之删除 `sttSettingsMem` / `sttSettingsOnce` 两个字段
与 `sttMemKey` / `newSttMemSettings` / `sttSettingsStore` 三个函数。

**同类问题还有一处**：`sttHTTPClientOr`（HEAD）与 `sttClient`（分支）
是**逐字节相同**的函数，合并后必然二选一。保留 `sttClient`（带 `SetSTTHTTPClient`
注入点，server 测试依赖它），把 HEAD 那段「为什么要回落而不是直接用字段」的
理由并入 `sttClient` 的注释。

> **这正是「单测全绿 ≠ 功能可用」的镜像形态**：本轮不修，将来任何一次
> 只改其中一份的提交，都会静默地把「无 PG 部署下 STT 功能不可用」这个
> 2026-10-01 刚修好的 bug 带回来，而**当时那批单测照样全绿**。

---

## §4.79.4 审计发现 ②：并入把 `apiError` 的 import 弄丢了（真回归）

`vue-tsc` 在并入后报 `TS2304: Cannot find name 'apiError'`（NoteListView.vue:288）。

不是分支带进来的 bug，是**自动合并的产物**：

- `useApiError` 的 import 与 `const apiError = useApiError()` 两行是
  2026-09-30 之后才加的（commit `81c1881`「原始错误上屏」卡口那一轮），
  分支诞生时还没有它们 → 分支侧 import 块**不含**这两行；
- 而 `summarizeError.value = apiError(e, …)` 这行**早于**那个 commit 就存在。

三方合并在 import 区取分支侧、在调用点保留 HEAD 侧，于是调用点失去了定义。
**这正是只有 `vue-tsc` 能抓到的一类错**——`gates` 里的其余检查全是文本扫描，
看不见「未定义标识符」。

补回 import 后，`recordErrorText` 仍**保持直出**、不套 `apiError`：
那是有真机证据支撑的修正（`stt_unavailable:` 前缀已在 runtime 写入时剥掉，
再过 `extractErrorCode()` 会把唯一可行动的信息压成通用兜底）。两者并存才对。

---

## §4.79.5 审计发现 ③：8 个测试文件从未被执行（本轮最大的一笔技术债）

并入的两个分支带来了 8 个 `.test.mjs`、约 1500 行断言。逐个查 `package.json`：

```
ORPHAN  src/api/__tests__/stt-error-render-chain.test.mjs
ORPHAN  src/api/__tests__/stt-presentation.test.mjs
ORPHAN  src/features/notes/__tests__/note-recording-error-visibility.test.mjs
ORPHAN  src/features/settings/__tests__/settings-stt.test.mjs
ORPHAN  src/native/__tests__/background-mic-contract.test.mjs
ORPHAN  src/native/__tests__/recording-voice-prompt.test.mjs
ORPHAN  src/features/email/__tests__/email-cache-heal.test.mjs
ORPHAN  src/features/email/__tests__/email-cache-heal-run.test.mjs
```

`gates` 里只显式列了 4 个 `src/native/__tests__/*.test.mjs`（`test:native`），
其余新文件**不在任何 npm script 里**。也就是说：STT 与邮件回补这两个功能
**在 CI 里零验证**——它们的「已验证」只存在于当初写它们的那个会话里。

手动跑：**127 通过 / 0 失败**。测试本身是好的，**只是没人调用它们**。

本轮接进 `gates`（新增 `test:stt` 91 断言、`test:email-heal` 36 断言）。

> 顺带一提：并发会话在同一时刻独立发现了**同一类**问题并修好了样式那部分
> （`8869943`「把样式护栏接进 gates —— 字体/token 回归测试此前从未执行」，
> commit message 与本节几乎同构）。两份改动在 `package.json` 冲突，
> 已按**并集**合并（`test:styles` 保留，两条新链追加其后）。
> 也就是说「孤儿测试」在本仓库是**系统性问题**，不是单点疏漏。

---

## §4.79.6 负控对照：SSRF 那条修复不是「看起来对」

`02b3e4f` 声称修了一个真实安全缺陷：`validateSTTOutboundURL` 此前调用的是
`validateGatewayURL`，于是任何为「连内网 LLM 网关」而打开
`POCKET_LLM_GATEWAY_ALLOW_PRIVATE` 的部署，**STT 的 SSRF 防护被顺带关掉**。

按本仓库「绿灯不算数」的规矩做了负控——把调用点改回 `gatewayAllowPrivate()`：

| 状态 | `TestSTTConfigRejectsLoopbackUnderGatewaySwitch` |
|---|---|
| 修复后 | **PASS** |
| 改回 `gatewayAllowPrivate()` | **FAIL** ✅ 负控成立 |
| 再次还原 | PASS |

即该测试确实在守这条不变量，不是恰好为绿而绿。已确认工作区无残留
（`git checkout -- ssrf.go` 后复跑为 ok）。

> 分支作者的记录里还有一个值得保留的观察：函数级用例（`TestSTTURL*`）在负控下
> **全部仍然通过**，只有端到端那条转红——因为函数本身没错，错的是**调用点**
> 接错了函数。这类缺陷只能由「走完整链路」的测试抓到，补再多函数级单测都没用。

---

## §4.79.7 本轮未做的事（明确留给下一轮）

1. **未删任何分支**。`audit/round8-2026-10-01` 与 `audit-snapshot-rd7` 已判定
   内容全部被 main 覆盖、可以删，但它们各自挂在一个 worktree 上
   （`openpocket-wt-rd8` 等），且**并发会话可能仍在其中工作**。
   按并发纪律，未在确认对方停手前删除。下一轮确认后可删。
2. **未合并两个仍活跃的分支**：`feat/mail-config-deploy`（18:39）、
   `email-pipeline-snapshot-2026-10-01`（18:33）。提交时间在本轮开始前后，
   属活跃，不在「1 小时前不活跃」的处置范围内。
3. **未动真机验证项**：STT 的 Maestro 流已过静态检查器
   （`scripts/check-maestro-flows.mjs` OK，选择器全部可溯源），
   但**真机执行仍缺**——设备侧 adbd 零回包问题见
   `2026-10-02-real-device-adbd-not-speaking.md`，那是并行的另一条线。
4. **未复核前端 `dist` 产物**：并入带进大量前端改动，
   APK 需重建后与新 commit 对齐（沿用 4.78.8 的 dirty=0 判据）。

---

## §4.79.8 测试命令与结果（本轮实测）

| 命令 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test ./...` | 53 包全 ok，0 FAIL |
| `npm run typecheck` | 通过（修掉 NoteListView 的 TS2304 之后） |
| `npm run gates` | 通过，含新增 `test:stt`(91) / `test:email-heal`(36) |
| `node scripts/check-maestro-flows.mjs` | OK |
| 8 个孤儿测试手动跑 | 127 pass / 0 fail |

负控见 §4.79.6。

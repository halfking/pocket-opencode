# P2 验证证据（2026-09-30）

**范围**：材料管线打通 —— 笔记/邮件/RSS/会议 → 学习条目 / 工作项转化。
只记录**实际执行并看到输出**的验证；未做的一律标「未验证」。

环境：Windows / PowerShell，Node v22.23.2，仓库 `C:\workspace\openpocket`。
PowerShell 执行策略禁止 `npm.ps1`，因此 npm script 一律改用 `npx.cmd <tool>` /
`node scripts/<x>.mjs` 直接调用，判据与 npm script 相同。

---

## 1. 后端（本轮先于前端完成）

### 改动清单

| 文件 | 性质 | 说明 |
|---|---|---|
| `backend/internal/learning/resolver.go` | 新增 | `SourceResolver` 接口 + `ErrSourceNotFound` + `captureTitleFromSource`（title 变可选、服务端解析、rune-safe 截断 200） |
| `backend/internal/learning/sources/resolver.go` | 新增 | notes/email/rss/meeting → title/summary/tags 的**租户隔离**解析器 |
| `backend/internal/learning/store.go` | 改动 | 增 `tags` 列 + `encodeTags/decodeTags` |
| `backend/internal/learning/service.go` | 改动 | `Capture` 顺序改为 validate → resolve → store check |
| `backend/internal/server/task_from_source_handler.go` | 新增 | `POST /api/tasks/from-source`；会议按 action item 展开、按 `(originRef,title)` 幂等、负责人/截止落 description、写 `work_item_events` |
| `backend/internal/server/server.go` | 改动 | `learningSources` 字段、`SetLearningSources`、`MeetingStore()` getter、路由 |
| `backend/cmd/pocketd/main.go` | 改动 | 装配 sources resolver |

### 验证结果

| 命令 | 结果 |
|---|---|
| `go build ./...` | ✅ 0 |
| `go test ./internal/learning/...` | ✅ ok |
| `go test ./internal/server -run "FromSource\|Learning"` | ✅ ok |
| `go test ./...`（全量） | ✅ 48 包通过；`internal/agent`(16) / `internal/email`(2) 失败 —— 已用 `git worktree add .baseline-check HEAD` 逐条对照，**与 HEAD 基线完全一致，属既有问题** |

### 测试抓出并修复的真实缺陷

1. **`Capture` 里 store nil 检查早于来源解析**：store 不可用时会在解析前返回 503，
   把本该是 400 的非法输入掩盖成服务不可用。已调换顺序（先校验、后判 store）。
2. **`from-source` handler 同一类顺序问题**：同样先校验后判 store，已修。

> 顺序原则：输入校验必须先于 store 可用性判断，否则 400 会被 503 掩盖。

---

## 2. 前端

### 改动清单

| 文件 | 性质 | 说明 |
|---|---|---|
| `frontend/src/features/study/AddToLearningButton.vue` | 新增 | 「一键加入学习 / 转为任务」入口；`sourceKind/sourceId/asTask/taskType` |
| `frontend/src/api/client.ts` | 改动 | 新增 `api.createTaskFromSource` |
| `frontend/src/features/notes/NoteDetailView.vue` | 改动 | 笔记标题区接入（学习 / 转为任务，类型 `study`） |
| `frontend/src/features/email/EmailDetailView.vue` | 改动 | 邮件头部接入（学习 / 转为任务，类型 `comms`） |
| `frontend/src/features/rss/RssItemDetail.vue` | 改动 | RSS 条目接入（学习 / 转为任务，类型 `research`） |
| `frontend/src/features/meetings/MeetingDetailView.vue` | 改动 | 会议接入（仅 as-task，类型 `meeting`） |
| `frontend/scripts/add-capture-locale.mjs` | 新增 | 一次性脚本：9 语言 ×5 键（幂等、保留人工译文） |
| `frontend/src/locales/*.json`（9 个） | 改动 | `study.capture.*`，en-US 351 → **356** |
| `frontend/scripts/build-material-symbols-subset.mjs` | 改动 | **根因修复**，见 §3 |
| `frontend/src/assets/fonts/material-symbols-outlined.woff2` | 改动 | 重建，80 → **106** 个图标 |

### 验证结果

| 命令 | 结果 |
|---|---|
| `npx.cmd vue-tsc --noEmit` | ✅ 退出码 0 |
| `node scripts/build-gate.mjs` | ✅ `✓ built in 17.76s`，退出码 0 |
| `node --test src/native/__tests__/*.test.mjs` | ✅ **101 tests / 101 pass / 0 fail** |
| `node scripts/check-viewmodel-gaps.mjs` | ✅ 命中 0 = 阈值 |
| `node scripts/report-locale-gaps.mjs` | ✅ 8 个非 en-US 语言**缺 0 / 多 0**；en-US **356 key** |
| `node scripts/verify-i18n.js` | ✅ 退出码 0 |
| task-type 合法性 | ✅ 手写 4 处（`study`/`comms`/`research`/`meeting`）逐个对照 `worktype.go:43` `typeGroups` 全部合法 |

### 组件三种降级状态

- **Learning Core 不可用**（`fetchDueSummary()` 抛错）→ `available=false`，按钮**不渲染**，
  而不是点了报错。学习是可选能力，不该在没装 PG 的部署里变成一个坏按钮。
- **`asTask` 但来源不在四类内**（`chat`/`manual` 无可回链的来源行）→ 不渲染。
  守卫用 `asTaskSourceKind()` 做**类型窄化**，让 `sourceKind` 从 `LearningSourceKind`
  （含 `chat`/`manual`）收窄到服务端 `sourceOriginKinds` 真正接受的四类，
  编译期即可挡住 400 —— 这条 TS 报错就是靠它修掉的。
- **重复点击** → 服务端按来源唯一索引幂等，所以成功后禁用而非报错；
  **失败**给 toast 但不改按钮状态（可能只是暂时网络不通）。

---

## 3. 顺带修掉的既有缺陷：字体子集漏字（P1 遗留）

P1 证据记录过一个隐患：子集脚本正则
`/material-symbols-outlined"[^>]*>([a-z_]+)</g` **只认字面量图标名**，
写在 JS 插值表达式里的名字扫不到，真机会显示成连字原文。P1 记为「P2 待办」，本轮收口。

**本轮全量扫描结果**：19 处插值式图标，其中 **8 个真实缺字**——
`star_border`(RssItemDetail.vue:111)、`pause` / `play_arrow`(RssListView.vue:148)、
`search_off`(NoteListView.vue:14)、`description` / `hide_image`(InvoiceCard.vue:12)、
`hourglass_top`(UnifiedComposer.vue:104,204)、`fullscreen`(JsonBlock.vue:143)。

**修复**：`build-material-symbols-subset.mjs` 增加第二条规则，
扫 `material-symbols-outlined …>{{ … }}` 同一行内的引号字面量名。

**修这条规则时自己踩的坑（值得记）**：第一版用
`/material-symbols-outlined"[^>]*>\s*\{\{(.*)$/`，看起来能匹配，实际在 Windows 上
**整条规则静默失效**——仓库文件是 CRLF，`.` 不匹配 `\r`，无 `/m` 的 `$` 又只认
字符串末尾，两者叠加必然回溯失败。第一次跑出 97 个图标是**假绿**：
多出的 17 个全来自少数 LF 结尾的文件。改用 `([^}]*)\}\}` 锚定（不依赖行尾）后，
实际收集 92 + 兜底 = **106**。
> 这类「静默失效的正则」比编译错误危险得多 —— 它不报错，只是悄悄少做事。
> 教训：改完扫描类脚本必须用**独立的反向探针**核对「已知缺失项现在是否已命中」，
> 而不是只看脚本自己报的数字变大了。

**字体产物核对**（避免「修图标」反而把包撑大）：

| | 图标数 | 文件大小 |
|---|---|---|
| HEAD 已提交版本 | 80 | 3,963,852 B (3.78 MB) |
| 本轮重建 | 106 | 3,520,800 B (3.36 MB) |

多 26 个图标，**体积反而小 443 KB**。顺带说明：脚本注释里写的
「≈4-12 KB」与实际产物（3.4 MB）严重不符，注释已过时，但本轮未改（不属本轮范围）。
> **后续更正**：该注释已在后续轮次改掉。进一步实测为
> 原始 3.80 MB → 产物 3.52 MB，**只削掉 7.3%**（并非 4-12 KB）。
> 见 [icon-registry-and-font-gate](2026-09-30-icon-registry-and-font-gate.md)。

### 仍未根治的同类问题（仅记录）

约 7 处图标的**名字在运行时才决定**，静态扫描原理上抓不到：
`SettingsView.vue:90`、`MoreHubView.vue:34,49`、`SettingsMenuDrawer.vue:57`、
`FlashcardEditView.vue:25`、`SessionComposer.vue:208`、`StudyHubView.vue:55,113`（`row.icon` / `sourceIcon()`）。
这些图标当前能正常显示，**说明它们的字形此前已通过别的字面量位置进入子集**；
但这是巧合而非保证，根治需要集中式图标映射表（如 `icons.ts` 导出常量），
让「字面量 ↔ 字形」在编译期可枚举。列入 P3 待办。

> **2026-09-30 后续更正：上面这段推断是错的，而且低估了严重性。**
> 实测发现 **20 处声明式图标（`icon: 'x'` 写在数据表里）根本不在子集**，
> 其中 `light_mode` / `dark_mode` / `brightness_auto` 正是子集脚本当初要修的那个 bug 的复发。
> 「当前能显示」是未经验证的推断被当成了观察记录。
> 结论与修法见 [2026-09-30-icon-subset-verification.md](2026-09-30-icon-subset-verification.md)。

---

## 4. 明确未验证的部分

| 项 | 原因 |
|---|---|
| 真机 / 浏览器实测 | 本轮仍只有门禁级验证。四个详情页新按钮的实际观感、点击反馈**未目视确认** |
| 与后端的端到端联调 | 依赖真实 Postgres。`/api/tasks/from-source` 的真实写入、幂等唯一索引、会议 action item 展开、来源删除返回 404，均**未在真 PG 上跑过**；DDL 沿用既有 `CREATE TABLE IF NOT EXISTS` + `ALTER ADD COLUMN IF NOT EXISTS` 幂等范式，**未在真实 PG 验证** |
| 新按钮的 503 降级分支 | 代码路径存在（`fetchDueSummary` 抛错即隐藏），但**未实测**在「Learning Core 不可用」时的真实表现 |
| `psychology` 等新增字形 | 已确认进入子集（106 个），但**未在设备上目视确认**渲染正常 |
| 会议 → 任务的多条展开 | 服务端按 action item 逐条转任务的逻辑有路由测试覆盖，但**未验证真实会议数据下的展开效果与幂等** |
| 新 i18n 文案译文质量 | 机器自译，未经母语校对 |
| e2e 套件 | 未跑；按钮带 `data-testid="add-learning-{sourceKind}"` 供后续补测试 |
| 后端全量回归 | 本轮（前端部分）未改后端代码，结论沿用 §1 |

# P1 验证证据（2026-09-30）

**范围**：前端两个入口成型（统一工作视图 + 学习中心）。只记录**实际执行并看到输出**的验证；
未做的一律标"未验证"。

环境：Windows / PowerShell，Node v22.23.2，仓库 `C:\workspace\openpocket`。
本机 PowerShell 执行策略禁止 `npm.ps1`，因此所有 npm script 改用
`npx.cmd <tool>` / `node scripts/<x>.mjs` 直接调用，判据与 npm script 相同。

---

## 1. 改动清单

| 文件 | 性质 | 说明 |
|---|---|---|
| `frontend/src/types/learning.ts` | 新增 | Learning Core 的 TS 类型（与服务端 JSON tag 一一对应） |
| `frontend/src/services/learning.ts` | 新增 | `/api/learning/*` HTTP 层；不含状态、不发 userId/workspaceId |
| `frontend/src/utils/learning-due.ts` | 新增 | 零依赖纯函数：今天有没有事、标题优先级、下次提醒时间、HH:MM 校验 |
| `frontend/src/services/__tests__/learning.contract.test.ts` | 新增 | HTTP 契约锁定（自包含，理由见文件头注释） |
| `frontend/src/utils/__tests__/learning-due.test.ts` | 新增 | 展示策略的真实单测（33 条中 16 条来自这里） |
| `frontend/src/features/study/StudyHubView.vue` | 改造 | 学习中心：今日回顾 Hero + 四项明细 + 每日提醒 + 学习收件箱 |
| `frontend/src/features/tasks/TasksView.vue` | 改造 | 分类 chip（工作/生活/学习/其他）+ 到期筛选（逾期/今天/本周）+ 分组标题 + 类型/到期/协作标签 + 创建表单加分类与截止日期 |
| `frontend/scripts/add-learning-locale.mjs` | 新增 | 一次性脚本：给 9 个语言包补 18 个键（幂等、保留人工译文） |
| `frontend/src/locales/*.json`（9 个） | 改动 | 新增 `study.due.*` / `study.reminder.*` / `study.inbox.*` / `study.source.*` |

## 2. 执行的验证与结果

| 命令 | 结果 |
|---|---|
| `node --experimental-strip-types --test src/services/__tests__/learning.contract.test.ts src/utils/__tests__/learning-due.test.ts` | ✅ **33 tests / 33 pass / 0 fail** |
| `npx.cmd vue-tsc --noEmit -p tsconfig.json` | ✅ 退出码 0（等价 `npm run typecheck`） |
| `node scripts/build-gate.mjs` | ✅ `✓ built in 25.39s`，退出码 0（等价 `npm run build:gate`） |
| `node --test src/native/__tests__/*.test.mjs`（4 个） | ✅ **38 tests / 38 pass / 0 fail** |
| `node scripts/check-viewmodel-gaps.mjs` | ✅ 命中 0 = 阈值（通过） |
| `node scripts/report-locale-gaps.mjs` | ✅ 8 个非 en-US 语言**缺 0 / 多 0**；en-US 333 → **351 key** |
| `node scripts/verify-i18n.js` | ✅ 退出码 0，9 语言齐备 |
| 图标子集核对（按 `build-material-symbols-subset.mjs` 的同一条正则扫描） | ✅ 20 个用到的图标**全部**在已提交字体子集内 |

### 测试再次抓出并修复的真实缺陷

1. **`dailyRuleTime` 只校验形状**：`"20:75"` 形状合法但不是合法时刻，会被原样渲染到界面，
   而服务端 POST 时会拒绝——界面与服务端对同一个值判断不一致。改为与后端
   `parseHHMM` 同样的范围校验（hh ≤ 23、mm ≤ 59）。

### 顺带确认的一个既有隐患（未修，仅记录）

`frontend/src/features/study/StudyHubView.vue` 原来用
`{{ totalDue > 0 ? 'play_arrow' : 'check_circle' }}` 渲染图标。
`build-material-symbols-subset.mjs` 的正则是
`/material-symbols-outlined"[^>]*>([a-z_]+)</g`，**只认字面量图标名**；
写在 JS 表达式里的 `play_arrow` 扫不到，因此它大概率从未进入子集字体，
在真机上会显示成连字文本 `play_arrow`。本轮把这个位置换成
`auto_awesome`（该图标在 `InvoiceListView.vue:9` 有字面量用法，字体里有）。
同类隐患在其他文件可能仍存在，**未逐个排查**；根治办法是让子集脚本也扫
JS 表达式里的图标名，或改用 `icons.ts` 之类的集中映射表。列入 P2 待办。

## 3. 明确未验证的部分

| 项 | 原因 |
|---|---|
| 真机 / 浏览器实测 | 本轮只有门禁级验证（typecheck / build / 单测 / 静态检查）。**没有**跑过真机或浏览器截图，两块新界面（学习中心、分类筛选）的实际观感与手势未确认 |
| 与后端的端到端联调 | 依赖真实 Postgres：`/api/learning/*` 的真实读写、提醒落库、通知推送均未实测。前端已按降级设计（503 时隐藏收件箱/提醒，退回本地闪卡口径），但该分支也未实测 |
| 新 i18n 文案的译文质量 | 机器自译，未经母语校对 |
| `sourceIcon()` 动态图标 | 运行时返回的名字，静态扫描扫不到；已人工确认 6 个名字都在字体子集内，但**未在设备上目视确认** |
| e2e 套件 | 未跑；新加了 `data-testid`（`study-hero` / `study-due-breakdown` / `study-reminder` / `study-inbox` / `task-filters` / `task-group-*` / `task-due-*` / `create-task-type` / `create-task-due`）供后续补测试 |
| 后端全量回归 | 本轮未改后端代码，结论沿用 P0 证据文档（48 包通过，2 包失败与 HEAD 基线一致） |

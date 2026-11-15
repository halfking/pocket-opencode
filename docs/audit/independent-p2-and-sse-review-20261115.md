# OpenPocket 三项 P2 修复与 SSE 独立复核 — 2026-11-15

## 范围与冻结状态

出版 clone：`/tmp/agent-manager-publish-20261115.D67ER1/openpocket`。
分支 `codex/maestro-codeup-audit-20261115`，HEAD `5805020b0a893efb137bfbeaa875114868a4200e`。
Lagrange 仅修本记录列出的七个 Board/subtitle 路径；不改 source 工作树，不改 Carver 的 SSE 路径，不操作设备，不 commit/push/发布。

空间不足通知后已停止写入和新测试；恢复阶段仅获准新建本独立审计记录与只读审查。下列 Node、浏览器测试、typecheck、fetch-bounds 均是空间冻结前已完成的结果，**此次没有重新执行**。主代理正在把复用 node_modules 换成锁文件独立安装，安装后的统一验证仍由主代理完成。

## 实际修改

| P2 | 修复行为 | 对应文件 |
|---|---|---|
| 旧 authority 缓存回放与旧请求回写 | 复用既有进程级 ACC authority/session；快照只保存在以不透明 authority 对象为键的 WeakMap 中，包含 workspace/session 隔离；owner 变更同步清空列表、模板及草稿，递增请求代次；所有 await 后校验后才回写或跳转；401/403 清缓存且禁止回放 | `frontend/src/features/acc-board/TaskBoardView.vue`、`board-snapshot.ts` |
| 字幕旧语言/旧请求结果覆盖 | 每个请求捕获语言、文本版本、会议、会议代次与现有 auth session；语言、会议、owner 或 store 生命周期改变后撤销旧任务；应答和 JSON 解析完成后再验 fence，旧 finally 也不能清掉新请求的 spinner | `frontend/src/stores/liveSubtitle.ts` |
| 全局 debounce 丢多段翻译 | 改为按片段独立 debounce，队列最多三个 HTTP flight；批量待翻译片段全部保留，同片段新文本替代旧版本 | 同上 |

旧 `acc_board_last_runs` 被安全废弃并尝试删除，永不读取；没有新增持久化缓存键，没有把 raw token 放进新缓存键。当前缓存可跨组件重挂载，但不跨进程/页面重载保存。

共享 `listAccSopRuns` 会吞 HTTP 失败并退回 registry。本修复不改冻结的 accApi，而在该 fallback 路径窄范围核验原列表端点状态，避免 401 被当作离线缓存准入、或暂时故障覆盖好快照；普通成功列表不增加该核验。沿用原 API 的 run normalizer。

字幕裸 fetch 已替换为既有 `fetchWithTimeout`，30 秒正预算并传取消 signal。请求使用捕获的 meeting ID 对应后端路由，携带当前已验证 session 的 Authorization；未增加无限时例外。原 18 个 store 用例的断言保留，仅调整模块加载/隔离 fixture 与清理。

## RED → GREEN 证据

初始 Board Chromium 行为组：6 项失败。workspace/account/ABA/base 变化后旧行仍可见，旧无归属 localStorage 快照未废弃。首个“新凭据 401”场景的 RED 具体失败在旧缓存未删除断言；不把它夸大成 HTTP 401 已直接展示旧行。原 API 吞错误的事实通过源码确认，修复后的真实 HTTP 场景验证不回放。

初始字幕行为组：6 项失败，分别为五条待翻译只发一条、旧语言结果覆盖、结束会议仍派发、同会议 ID 重开接纳旧结果、owner ABA 不清旧字幕、旧文本结果覆盖新文本。修复后六项通过；另补 owner 在 JSON 解析期间变化的 fence 测试通过。

最终修复行为计数：Board 6 + 新字幕 7 + 原字幕 18 = **31 项**。再加现有静默吞错/census 6 与 unmount 6，共 **43 项通过、0 fail/skip/cancel**。

## 已执行命令与历史日志摘录

工作目录：`/tmp/agent-manager-publish-20261115.D67ER1/openpocket/frontend`。
原始 stdout 保存在本会话工具回执中；当时没有另存独立日志文件。以下是该回执的命令与结果摘录，不能冒充依赖重装后的新日志。

```text
$ node --test src/features/acc-board/__tests__/task-board-owner-browser.test.mjs
RED: tests 6 / pass 0 / fail 6 / exit 1

$ node --test src/stores/__tests__/liveSubtitle-races.test.mjs
RED: tests 6 / pass 0 / fail 6 / exit 1

$ node --test src/features/acc-board/__tests__/task-board-owner-browser.test.mjs src/stores/__tests__/liveSubtitle-races.test.mjs src/stores/__tests__/liveSubtitle.test.ts src/native/__tests__/unmount-must-not-abort-streams.test.mjs src/__tests__/silent-catch-must-explain-itself.test.mjs
GREEN: tests 43 / suites 10 / pass 43 / fail 0 / cancelled 0 / skipped 0 / todo 0
duration_ms 4647.634959
exit 0

$ npm run typecheck
> vue-tsc --noEmit
exit 0

$ node scripts/check-fetch-bounds.mjs
自证臂 8/8 通过
686 个文件 / 10 个 fetch 站点全部有界
self 4 / primitive 2 / 登记例外 L1 3 + L2 1
exit 0
```

定向浏览器仅用独立临时 Vite/Chromium、虚构身份及 loopback 应答，不访问生产服务或真实设备。字幕用真实 store/auth session 配合内存 storage 与受控应答，不把它当作真实 LLM/会议端到端验收。未更新任何审计台账、ALLOW 或既有门禁断言来放宽判据。

## 七个归属文件冻结 SHA-256

空间恢复后的只读核实：七个值均与空间不足前的冻结值完全一致。

```text
eadbc04060f2f84ba7253062b91f360c580a00382627062f39ecc34fb910631c  frontend/src/features/acc-board/TaskBoardView.vue
04d6d4df16c7830374e44ed233907eb2951e07483098d0e6a998f64e9bfbebb5  frontend/src/features/acc-board/board-snapshot.ts
7f5c8765255f79fc5cd0e805eb2593f9a3e54d285fa80b3466e90150064bc468  frontend/src/features/acc-board/__tests__/task-board-owner-browser.test.mjs
cc971d320a9a307876845de72b37b9fe1c8a2e65451f383fa6e673651521fe51  frontend/src/stores/liveSubtitle.ts
ee4e3c6355be205a2451e615b002cc687950e1681d2331ebf74ad8c6786ed877  frontend/src/stores/__tests__/liveSubtitle.test.ts
69cc26e7450fc370ea01fffa271d16406788fd57c942fe3ff233bb4e9bd7255e  frontend/src/stores/__tests__/subtitle-fixture.mjs
7d4ba5ad0a4a19aa510b99f11f285bc601b30fd315109868029e1a25aad4a742  frontend/src/stores/__tests__/liveSubtitle-races.test.mjs
```

## SSE 独立复核的证据边界

Carver 的九路径为八个源/测试文件加其 checkpoint 文档。八项机器冻结清单为 `/tmp/openpocket-acc-sse-frozen-20261115.json`，恢复后的只读 SHA 核实全部匹配；checkpoint 为 `docs/audit/codeup-baseline-repair-20261115.md`，本代理未修改。

已读原始证据：`/tmp/openpocket-acc-sse-targeted-final.log` 的 32/32、exit 0；`/tmp/openpocket-acc-sse-typecheck-final.log` 的 exit 0；原 fetch-bounds 的失败确实指向当时字幕裸 fetch。该 fetch 缺口在本轮修改后的上述独立命令已绿。

`/tmp/openpocket-acc-sse-test-all-final.log` 的 435/435 是 Carver 修改完成、三项 P2 修改之前的历史全量，不是本轮合并后的全量。外部 `/tmp/openpocket-sse-sonnet-review.json` 结论为无 blocking，且明确不声称亲自执行测试；本代理仍独立阅读实现与行为断言，最终复核结论另附。

## SSE 只读补审结论

九路径已完成源码/diff、行为断言及历史日志阅读；无新 P1，发现以下两项 P2 需主代理处理。遵守恢复阶段限制，未运行任何新 Node 测试、构建或全量；以下是可由代码调用链确定的场景，**不冒充新运行的复现日志**。未修改 Carver 任一路径。

**S1 / P2：详情视图缺 owner/请求代次 fence。**
`frontend/src/features/acc-board/TaskDetailView.vue:227` 在 await 后直接赋值 `run`；285 行仅监听 runId，未监听 authority 同步清除 `run`、审批/移交弹窗或旧提示。账户/workspace/ACC 配置改变但路径不变时组件保持挂载：旧详情仍在，新旧请求还可乱序覆盖。238–255 行捕获的 owner 只保护 `runtime.cancel`；返回 false 仍写“已请求终止”并调用 load，旧 catch/finally 也能写入新 owner 的界面。

根 App 的 `email-view-boundary.ts` 只对 `/email` 视图做身份 key/渲染门禁，不能替 ACC 详情兜底。应在详情读取、取消及审批/移交完成链中捕获并验证当前 owner/run/请求代次，owner 变化同步清旧视图；不能仅依赖日志 runtime 的 cancel 返回值。原有详情异步隔离在 580 已有缺口，SSE 接线没有覆盖它，不能将其描述为全由 Carver 新增。

现有 browser 测试的 identity 切换只挂 LogStream；挂实际 TaskDetail 的另一用例只测 cancel 404/成功，没有“延迟详情/取消应答 + owner 切换”场景。建议主代理补这些行为负例。真实 App 按路径重挂载详情，因此本发现以**路径不变的 owner 切换**为确定触发，不假定不同路径一定复用同一组件。

**S2 / P2：runtime 内部撤权未传播到 session 的响应式 authority。**
`acc-log-runtime.ts:269–272`、411–415 行在 SSE/poll 401/403 时内部 `setAuthority(null)`；162–169 行的 admitted 失败也可如此。`acc-log-session.ts:34–35` 仅在其 sync 函数内复制 `runtime.captureAuthority()` 至 shallowRef，51 行仅覆盖 auth dispose，没有 runtime→ref 的失效通知。

确定状态链：session 原先暴露 owner A → 同一 auth/config 下服务器返回 401/403 → runtime authority 已 null，而 session.authority.value 仍 A。日志自己的 subscriber 会被清空，但仅监听该 ref 的消费者（包括新 TaskBoard）没有收到同步撤权事件，旧私有列表可继续留在视图中，直到其他 watcher/刷新动作发生。这是 runtime/session 与消费者之间的集成问题；建议主代理给 runtime authority 变化加统一响应式发布，再补“401/403 → ref 为 null → Board/Detail 清空”的组合行为测试。

其余已读安全边界未发现具体新阻断：运行级进程所有权、卸载仅 detach、固定离开宽限、reader/controller 共同清理、SSE frame/重放/轮询体积上限、poll 非续租截止、seq 去重和 gap 回补、终态/显式取消均有实现和已见定向断言。Promise census 102 的变更对应移除的旧 mock handler 和保留的有说明 SW handler，没有扩展 unmount ALLOW。外部 Sonnet 的“无 blocking”保留为另一份审查意见，不替代以上视图/session 集成检查。

## Task Stop Summary

- status: partial
- goal: 已冻结三项授权 P2 修复并记录真实验证；继续只读 SSE 复核，不替代统一发布验证
- baseline: codex/maestro-codeup-audit-20261115 / 5805020b0a893efb137bfbeaa875114868a4200e
- changed_files: 8
- in_scope_files: 8
- out_of_scope_files: 0
- unnecessary_changes: none
- accidental_changes: none
- verification: passed 空间冻结前定向 43 项、typecheck、fetch-bounds；恢复后只读七文件及 SSE 八项 SHA 对比
- unresolved: SSE 只读补审发现 S1/S2 两项 P2，待主代理修复并重新冻结；独立依赖安装后的统一测试由主代理执行；未做真实部署或设备验收
- next_action: 主代理处理 S1/S2、补行为负例、核对冻结 SHA 并统一验证；本代理业务源码保持冻结

## 主线程后续处置 — 2026-11-16

上述 S1/S2 已由 Carver 修复，最终定向 42/42、typecheck exit 0；Lagrange 末次只读复核未发现新增 P1/P2。
详情 API 的 no-store 和登记前 fence 一并核对；弹窗回调捕获打开时 ticket，当前仅有源码/类型及正常 Mock 交互覆盖，不声称已有专门的弹窗 ABA 行为用例。
其后真机发现日志不可达，主线程只改看板/详情的 page CSS，新增两个 Chromium 先红后绿用例；结合 owner/SSE 浏览器组 20/20，最终统一前端 438/438 文件通过。
新增看板 Mock 原生流程已完整通过日志滚动、审批 2/5→3/5 和返回列表。现有 route shell 提供有限高度；外部模型的“未来父容器不定高可能塌陷”假设未在当前实现或设备中证实，没有采用会重新超过可用区域的 100vh 回退。
本节由主线程补充，保留前述独立审查快照；最终源码 SHA、设备范围和发布状态以本轮归档及根仓结构化结果为准。

# 手机端内置本地智能体(Mobile Local Agent)· 设计与实施

> 文档状态:现行方案 v1(2026-09-15,含落地记录)
> 前置文档:[2026-09-08-pi-agent-in-app-architecture.md](../2026-09-08-pi-agent-in-app-architecture.md)(桌面 spawn 方案,本文档在**手机端场景**取代其 M1 路线)
> 调研对象:`~/workspace/ai/pi`(pi-mono earendil-works)、`~/workspace/ai/openhands`(Agent Canvas)

---

## 0. 摘要

在 openpocket 手机 App(WebView)内置一个**本地智能体运行时**:参考 pi 的 Agent 循环 /
SKILL.md 技能标准 / markdown 专家定义,以纯 TypeScript 实现(零 Node 依赖),LLM 流量走
既有 `/api/llm/stream` BFF;交互参考 openhands 的事件时间线 + 工具卡片 + 审批确认 +
任务计划卡。入口:新路由 `/#/local-agent`(≡ 菜单「更多功能」组)。

**MVP 已落地并经 Android 模拟器验证**(见 §9);原生 function-calling 透传为二期(§8)。

## 1. 为什么换路线:桌面方案在手机上不成立

2026-09-08 方案的核心是 App 作为父进程拉起 `pi --mode rpc` 子进程。手机端两条都断:

1. **无子进程**:iOS/Android App 沙箱不允许 spawn Node 进程;pi CLI 也无移动发行版。
2. **无本地 LLM 凭据面**:pi 直连各 provider 需要 API key 落端,违背 openpocket
   「凭据不出后端」红线(llmbff 设计 §6 R6)。

而 pi 官方自己已经给出移动端方向的答案:`packages/agent/docs/mobile-handoff/` 设计
handoff + `scripts/check-browser-smoke.mjs` 维护的**浏览器安全边界清单**——agent 核心循环
(`Agent`/`agent-loop`)、skills 加载(harness 走 `ExecutionEnv` 抽象)、工具协议都是
**纯 TS、无 Node 依赖**,可跑在 WebView。因此正确路线是:**移植 pi 的核心语义,而非移植
pi 进程**。

| 维度 | 2026-09-08(桌面) | 本方案(手机) |
|---|---|---|
| agent 运行位置 | `pi --mode rpc` 子进程 | WebView 内 TS 运行时(`frontend/src/localagent/`) |
| LLM 接入 | pi 直连 provider | 复用 `/api/llm/stream`(凭据留后端 + 120s 看门狗 + 后台保活免费继承) |
| 工具执行 | bash 等 + sandbox 路由 | Capacitor/Web API 白名单工具(文件/计算/HTTP/设备信息) |
| 会话存储 | SQLite | localStorage(JSON,存量小) |

## 2. 从 pi 学什么(语义清单)

pi 源码结论(路径相对 `~/workspace/ai/pi`):

- **Agent 循环**:`packages/agent/src/agent-loop.ts` 双重 while(内层=有工具调用继续,
  外层=followUp 队列);`StreamFn` 抽象把「LLM 调用」与「循环」解耦;`beforeToolCall`
  hook 返回 `{block, reason}` 即审批闸门——我们按此实现。
- **技能**:`packages/coding-agent/src/core/skills.ts` + Agent Skills 标准:
  SKILL.md = YAML frontmatter(`name` ≤64 小写连字符,`description` ≤1024)+ markdown
  正文;渐进披露——system prompt 只放 name/description 清单,模型按需取全文。
  我们同样内置 `load_skill` 工具。
- **专家**:pi 无 expert 概念,最近对应物是 subagent 扩展
  (`packages/coding-agent/examples/extensions/subagent/agents.ts`):markdown 文件
  frontmatter `{name, description, tools?, model?}` + **正文即 system prompt**。
  我们原样采纳该格式(去进程隔离,改同进程新循环实例)。
- **工具定义**:TypeBox schema 在 MVP 里降级为 JSON Schema 对象 + 手写描述
  (TypeBox 不引依赖);`ToolDefinition` 的 `label/description/promptSnippet/risk`
  字段保留,用于 system prompt 与 UI 卡片。

## 3. 从 openhands 学什么(交互清单)

openhands(Agent Canvas)结论(路径相对 `~/workspace/ai/openhands`),本次采纳:

| openhands 模式 | 本方案落地 |
|---|---|
| 事件时间线:action 被 observation 替换、连续工具折叠(`src/utils/handle-event-for-ui.ts`、`group-events.ts`) | 时间线四类条目:user / assistant / tool(工具卡合并调用与结果)/ plan |
| 工具卡片注册表 + 可展开详情(`tool-visualizers/define.ts`、`generic-event-message.tsx`) | `ToolCallCard.vue`:名称+状态+耗时,展开看参数/结果 |
| 尾部确认条 + 风险分级(`conversation-confirmation-buttons.tsx`、`security_risk`) | `ApprovalBar.vue`:medium/high 风险工具暂停循环等确认,Reject 注入拒绝结果让模型改道 |
| ExecutionStatus 状态机(`conversation-state-store.ts`) | run 状态:idle/thinking/tool_running/waiting_approval/error/aborted,驱动输入框禁用与状态徽章 |
| TaskTracker 计划卡(`task-list-section.tsx`,`TaskItem[] {title,notes,status}`) | `task_plan` 工具 + `PlanCard.vue`(todo/in_progress/done) |
| 停止按钮 / pause(interrupt) | composer 发送↔停止双态,abort 走 aiStreamRuntime 用户级取消 |

未采纳(记录理由):乐观消息队列(本通道本地即时回显,无回显竞态)、WS 双通道
(本地循环不需要)、Plan 子会话(二期:规划专家)。

## 4. 架构

```
┌─ LocalAgentView.vue ─────────────────────────────────────────────┐
│  时间线:用户气泡 / 助手分段 / ToolCallCard / PlanCard / ApprovalBar │
│  Composer:专家选择 · 技能附加 · 发送/停止                           │
└──────────────┬───────────────────────────────────────────────────┘
               │ Pinia agentStore(会话列表 + 时间线 + run 状态)
┌──────────────▼───────────────────────────────────────────────────┐
│  localAgentRuntime(frontend/src/localagent/runtime.ts,singleton) │
│  spawnRun / subscribe / abort / approve / localStorage 持久化      │
└──────────────┬───────────────────────────────────────────────────┘
               │
┌──────────────▼───────────────────────────────────────────────────┐
│  agent-loop.ts(移植 pi agent-loop 语义)                           │
│   while steps<max: streamFn(messages) → 收流 → 解析工具调用         │
│   → 审批闸门(beforeToolCall)→ 执行 → <tool_result> 追加 → 再循环    │
│  system-prompt.ts:基础人格/专家覆盖 + 技能清单 + 工具清单 + 协议说明  │
│  skills.ts(SKILL.md 解析+注册表) experts.ts(markdown 专家)          │
│  tools/*:current_time calculate read_file write_file list_files     │
│           device_info http_fetch task_plan load_skill               │
└──────────────┬───────────────────────────────────────────────────┘
               │ llm-stream.ts(流适配,继承看门狗/后台保活/401续期)
┌──────────────▼───────────────────────────────────────────────────┐
│  aiStreamRuntime.spawnChat → POST /api/llm/stream(SSE)→ 网关      │
└───────────────────────────────────────────────────────────────────┘
```

**工具调用传输:提示词驱动 JSON 协议(MVP 决策)。** 现有 `/api/llm/stream` 是纯文本
补全通道(`llmbff.Message` 无 `tool_calls`,网关客户端 `llmgateway` 亦无 tools 字段),
且 auto 回退链会跨模型路由,不保证 function-calling 能力。协议:

- system prompt 声明:需要用工具时,在回复末尾输出 ```json {"tool":"名","args":{…}}```
  围栏块并停止;直接回答时不输出块。
- `tool-protocol.ts` 在回合结束时扫描围栏块(取最后一个合法 `tool` 字段块);
  围栏前的文本作为「思考段」照常展示。
- 工具结果以 `<tool_result tool="名">…</tool_result>` 包裹追加为 user 消息。

该协议对任意模型可用、零后端改动、单测可全覆盖;缺陷(解析可靠性略低、单回合单工具)
由严格提示词 + 容错解析 + maxSteps 兜底,二期切原生 tool_calls(§8)。

## 5. 关键设计决策

1. **纯 TS、无 Vue 依赖、node:test 可测**:`frontend/src/localagent/` 下不 import
   vue/pinia/capacitor 顶层(动态 import + 注入),沿用 `frontend/src/native` 的
   `node --test src/.../__tests__/` 测试约定(Node 22 类型剥离直跑 .ts)。
2. **流适配层复用 aiStreamRuntime**:每次 LLM 回合 = 一个 `agent-<session>-<n>` 流,
   白拿 120s 看门狗、隐藏暂停、401 续期、后台保活(2026-09-09 三件套)。
3. **审批分级**:工具声明 `risk: "low"|"medium"|"high"`;low 自动放行
   (current_time/calculate/device_info/load_skill/task_plan/list_files/read_file),
   medium/high 需确认(write_file/http_fetch)。拒绝不是终止——把「用户拒绝」作为
   工具结果回灌,模型可改道(对齐 pi beforeToolCall `{block}` 语义与 openhands
   UserRejectObservation)。
4. **文件工具沙箱根**:Capacitor 原生用 `Directory.Data` 下 `agent/` 子目录;Web
   降级为 localStorage 虚拟 FS(`pocket:localagent:fs:` 前缀),路径规范化禁 `..`。
5. **专家默认四个**:`general`(通用助理)、`trip-planner`(行程规划,演示技能组合)、
   `notes-writer`(笔记/纪要整理)、`quick-calc`(速算/单位换算)。每个专家可声明
   `allowedTools` 子集与推荐技能;正文即 system prompt(pi 格式)。
6. **技能内置 6 个**:深度阅读、行程规划、会议纪要、周报生成、发票信息提取、
   单位换算。SKILL.md 格式,以 TS 字符串内置(二期接 marketplace 包下载到
   Filesystem 再被发现)。
7. **持久化**:localStorage `pocket:localagent:sessions`,≤20 会话、单条工具结果
   截断 4KB、会话消息 ≤200 条,写失败静默(对齐 usage best-effort 风格)。

## 6. 实施步骤(已执行)

1. 设计文档(本文)。
2. `frontend/src/localagent/`:`types.ts`、`tool-protocol.ts`、`agent-loop.ts`、
   `system-prompt.ts`、`skills.ts`、`experts.ts`、`tools/*.ts`、`llm-stream.ts`、
   `runtime.ts`。
3. `frontend/src/features/local-agent/`:`agentStore.ts`(Pinia)、`LocalAgentView.vue`、
   `ToolCallCard.vue`、`ApprovalBar.vue`、`PlanCard.vue`、`SkillExpertSheet.vue`;
   路由 `/local-agent` + `SettingsMenuDrawer`「更多功能」入口 + i18n(zh-CN/en-US)。
4. 单测:`frontend/src/localagent/__tests__/`(loop 审批/拒绝/终止、协议解析容错、
   工具、技能解析、runtime 事件与持久化),`node --test` 全绿。
5. `vue-tsc --noEmit` 通过;`build-mobile.mjs android dev` 构建 + AVD 安装;
   CDP 全链路验证(§9)。

## 7. 风险与对策

| 风险 | 对策 |
|---|---|
| 模型不守 JSON 协议 | 解析器容错(围栏语言标记可缺、尾逗号修剪);协议重申注入;解析失败按纯文本回答展示 |
| 长任务 token 爆炸 | maxSteps=12/轮;工具结果截断;二期接 pi compaction 思路 |
| WebView CORS 限制 http_fetch | 仅 GET;同源 API 直达;外域失败回灌错误文本由模型解释 |
| 会话存储膨胀 | 截断 + 上限 + 失败静默 |

## 8. 二期路线

1. **原生 function calling 透传**(后端五处):`llmgateway/client.go`(ChatRequest.Tools
   + ChatMessage.ToolCalls/ToolCallID + StreamDelta 解析 `delta.tool_calls`)、
   `llmbff/service.go`(Message/ChatRequest/Delta 同形)、`llmbff_provider_adapters.go`
   映射、`server_llmbff.go` 入参、`aiStreamRuntime` 帧扩展;前端 `streamFn` 按能力探测
   自动降级回 JSON 协议。
2. 技能包接 marketplace(Package.Kind="skill" 已有全生命周期)→ 下载到
   `Directory.Data/agent/skills/` → 启动扫描。
3. 专家接 `chat_agents` 表(`skill_refs` 列已备)→ 云端可配专家。
4. Plan 子会话(openhands Plan 模式):规划专家产出计划卡 → 一键执行。
5. `localagent.go` 后端骨架填真(Go 侧同语义运行时,scheduledtask local_agent
   executor 复用),与前端运行时共享工具协议。

## 9. 验收记录(2026-09-15)

- 单测:`node --test src/localagent/__tests__/` 45/45 全绿(loop 11 + protocol 10 +
  tools 7 + skills/experts 5 + llm-stream 4 + runtime 8);`src/native/__tests__`
  80 用例无回归。
- typecheck:`vue-tsc --noEmit` 0 错;`build-mobile.mjs android dev` 构建通过。
- **提交前审计轮(2026-09-15)**:复核全链路后修复 4 项——
  a) llm-stream 外部 abort 未掐断底层流(zombie fetch 白烧 token)→ onAbort 同步
     `handle.abort()` + 4 个传输层单测;
  b) 中文 IME 组合期 Enter 误发送 → `isComposing || keyCode===229` 守卫;
  c) 流式期间 smooth 滚动风暴 → 改瞬时滚动;
  d) localStorage 半旧数据无防御 → loadSessions 字段级兜底。
  审计后重建 APK,模拟器冒烟(工具循环全链路)PASS。
- Android 模拟器(pocket_clone AVD,API 36.1)CDP 全链路(e2e/android/local-agent-cdp.py,
  配套 mock 网关 e2e/android/mock-llm-gateway.py 提供确定性 LLM 流):
  - P2 工具循环 PASS:calculate 工具卡 completed(23*7+128 = 289)→ 终答含 289,
    usage 累计正确(2 回合 240/80 tokens);
  - P3 审批放行 PASS:write_file(medium 风险)暂停循环出审批条 → 点「允许」→
    completed「已写入 notes/todo.md」→ 终答;
  - P4 审批拒绝 PASS:http_fetch(medium 风险)→ 点「拒绝」→ denied 卡「用户拒绝
    执行该工具」→ 模型改道直接回答 → status idle;
  - P5 计划卡 PASS:task_plan set 三条 → PlanCard 渲染(计算:in_progress /
    保存:todo / 汇报:todo);
  - 持久化 PASS:force-stop 后重启,会话(12 条时间线 + usage + 标题)完整恢复,
    运行中状态恢复时归位为 error。
- 验证过程中发现并修复的缺陷(均已含回归覆盖):
  1. approval_required 事件先于审批注册发出 → 审批条永不渲染;改为 runtime 在
     gate 注册后发事件(loop 侧删除该事件)。
  2. store 浅拷贝时间线数组但共享条目引用 → 工具卡状态卡死「执行中」;
     改为条目级浅克隆强制 Vue 重渲染。
  3. run_error 无可见条目(静默失败)→ 时间线落 system 错误条目。
  4. node 测试环境误触 Capacitor Web FS(IndexedDB)→ fs 后端仅在
     isNativePlatform() 时走 Capacitor,否则 Memory 兜底。
  5. abort 与流 resolve 竞态 → 取消后循环不再把残留文本当答案。
  6. agent-loop 未把 emit 传进 ToolContext → task_plan 成功但 plan 事件被吞、
     计划卡不渲染;补 emit 透传 + 回归测试(单测 41 用例)。
- 环境备注:本机 dev PG(Docker)当日丢失,验证用 SQLite 模式 pocketd +
  `/api/llm-gateway/config` 指向本地 mock 网关(POCKET_LLM_GATEWAY_ALLOW_PRIVATE=true),
  不依赖外网 LLM。

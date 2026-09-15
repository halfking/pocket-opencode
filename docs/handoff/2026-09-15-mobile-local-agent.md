# Handoff:手机端内置本地智能体(2026-09-15)

> 状态:已落地 + 审计修复 + 模拟器验收;同日提交 main。
> 设计方案:[docs/design/2026-09-15-mobile-local-agent.md](../design/2026-09-15-mobile-local-agent.md)(调研 × 交互借鉴 × 架构决策 × 二期路线)

## 结论/根因

1. **结论**:手机端内置本地智能体已可用——`/#/local-agent` 路由,pi 语义移植的
   WebView 内 TS 运行时(skills + experts + tools 三模式),openhands 式交互
   (事件时间线/工具卡/审批条/计划卡/状态机),单测 45/45、typecheck 0 错、
   Android 模拟器 CDP 五场景(工具循环/审批放行/拒绝改道/计划卡/持久化)全 PASS。
2. **关键根因(为什么这么设计)**:
   - 原 2026-09-08 方案在手机上不成立(沙箱禁子进程 + 凭据不出后端红线),pi 官方
     mobile-handoff 的方向是移植语义而非进程 → 纯 TS 运行时 + 复用 `/api/llm/stream`。
   - 网关客户端(`llmgateway`)无 `tools` 字段且 auto 回退跨模型路由 → 工具调用用
     **提示词驱动 JSON 围栏协议**,零后端改动、任意模型可用;原生 function-calling
     留二期(后端五处改动点已写入设计文档 §8)。
3. **审计修复(提交前复核发现 4 项,均已修)**:
   - P1 llm-stream 外部 abort 不掐底层流 → zombie fetch 白烧 token;改为 onAbort
     同步 `handle.abort()`,补 4 个传输层单测。
   - P1 中文 IME 组合期 Enter 误发送 → `isComposing || keyCode===229` 守卫。
   - P3 流式期间 smooth 滚动互相打断卡顿 → 瞬时滚动。
   - P2 localStorage 半旧/损坏数据无防御 → loadSessions 字段级兜底(状态归位、
     timeline/history 过滤、usage 数值化)。
   - 首轮验证还修过 6 项(审批事件时序、store 条目克隆、run_error 静默、Capacitor
     Web FS 误触、abort 竞态、ToolContext.emit 未透传),清单见设计文档 §9。

## 改动文件与关键行为

| 文件/目录 | 关键行为 |
|---|---|
| `frontend/src/localagent/`(新,10 文件) | 纯 TS 运行时:agent-loop(pi 语义)/ tool-protocol(围栏 JSON 协议 + 容错解析)/ system-prompt / skills(SKILL.md,6 技能)/ experts(4 专家)/ tools(calculate/fs 沙箱/http_fetch/task_plan/load_skill 等 9 个,risk 分级)/ llm-stream(适配 aiStreamRuntime,abort 同步掐断)/ runtime(进程 singleton `__openpocket_localAgentRuntime__`,localStorage 持久化,≤20 会话/200 条/4KB 截断) |
| `frontend/src/features/local-agent/`(新,5 文件) | Pinia store(**条目级克隆**避免 Vue props 引用不变跳过重渲染)/ LocalAgentView(时间线 + composer,IME 守卫,瞬时滚动)/ ToolCallCard / ApprovalBar / PlanCard |
| `frontend/src/app/router-mobile.ts` | `/#/local-agent` 路由(懒加载,requiresAuth,canGoBack) |
| `frontend/src/components/base/SettingsMenuDrawer.vue` | 「更多功能」组入口(≡ 菜单) |
| `frontend/src/locales/*.json`(9 个) | `nav.localAgent` 键(fallback en-US,视图内文案硬编码中文,与 ai-chat/rss 一致) |
| `frontend/src/localagent/__tests__/`(新,6 文件 45 用例) | loop/protocol/tools/skills-experts/llm-stream/runtime 全覆盖;node --test 直跑 .ts |
| `e2e/android/local-agent-cdp.py`(新) | CDP 全链路:P0 清残留 → P1 双密码登录注入 → P2 calculate 循环 → P3 审批放行 → P4 拒绝改道;发送带「验证+重试」(点击可能落在 Vue 重渲染前的旧节点) |
| `e2e/android/mock-llm-gateway.py`(新) | 确定性 mock OpenAI SSE 网关(按 prompt 内容脚本化发工具调用/终答),摆脱外网 LLM 不稳定 |
| `docs/design/2026-09-15-mobile-local-agent.md` + `docs/README.md` | 设计方案(含二期路线 §8)+ 索引 |
| 后端 | **零改动**(MVP 全部前端;二期透传改动点已文档化) |

## 测试命令与结果

```bash
cd frontend
node --test "src/localagent/__tests__/*.test.mjs"   # 45/45 pass(41+4 审计新增)
node --test "src/native/__tests__/*.test.mjs"        # 80/80 pass(无回归)
npx vue-tsc --noEmit                                 # 0 错
node scripts/build-mobile.mjs android dev            # OK,sanity(API base 注入)通过
cd android && ./gradlew assembleDebug                # APK 30MB
adb install -r app/build/outputs/apk/debug/app-debug.apk
# 模拟器(pocket_clone,API 36.1)+ mock 网关:
#   python3 e2e/android/mock-llm-gateway.py 18099
#   pocketd: unset POCKET_POSTGRES_DSN + POCKET_AUTH_LEGACY_ONLY=true
#            POCKET_LLM_GATEWAY_ALLOW_PRIVATE=true
#            /api/llm-gateway/config → http://127.0.0.1:18099
#   python3 e2e/android/local-agent-cdp.py <wsUrl> http://<LAN-IP>:8090
# 结果:P2 PASS / P3 PASS / P4 PASS / P5 计划卡 PASS / 重启持久化 PASS
# 审计后重建冒烟(工具循环全链路)PASS
```

## 遗留风险

1. **协议可靠性依赖提示词**:模型不守 JSON 协议时按纯文本回答(可接受降级);
   长尾模型可能多次重试浪费步数。根治 = 二期原生 function-calling 透传。
2. **history 是近似**:跨 send 只保留 user prompt 与 assistant 文本,单次任务内
   工具往返不重放 → 后续提问可能缺上下文细节(时间线留存,模型可要求重读)。
3. **http_fetch 受 CORS 限制**:外域 API 可能拿不到(错误文本会回灌模型解释);
   WebView 内同源/网关代理可达的接口无碍。
4. **审批无超时**:waiting_approval 永久挂起直到用户响应/停止(与 openhands 一致)。
5. **运行中杀进程丢中间工具结果**(send 前/后各持久化一次,中途不刷盘)。
6. **dev 环境**:本机 dev PG(Docker)当日丢失,验证走 SQLite + mock;恢复 PG 后
   需用真网关复跑一轮(上游 502 属 runbook §29 已知状态)。
7. `pocket_test` AVD 的 android-30 镜像缺失,模拟器验证统一用 `pocket_clone`。

## 下一轮提示词(建议)

> 在 openpocket 手机端本地智能体(docs/design/2026-09-15-mobile-local-agent.md)基础上:
> 1) 后端原生 function-calling 透传(llmgateway/client.go + stream.go、llmbff/service.go、
>    llmbff_provider_adapters.go、server_llmbff.go、aiStreamRuntime 帧扩展),前端
>    streamFn 按模型能力自动降级回 JSON 协议;
> 2) 技能包接 marketplace(Package.Kind="skill" 下载到 Directory.Data/agent/skills/,
>    启动扫描注册);
> 3) 专家接 chat_agents 表(skill_refs)云端可配;
> 4) 恢复 dev PG 后用真网关复跑 e2e/android/local-agent-cdp.py 全场景。
> 完成后跑:node --test src/localagent/__tests__/ + vue-tsc + build-mobile android dev
> + 模拟器 CDP 回归。

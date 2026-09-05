# 2026-09-05 budget90-restart：9a74236 实例级验证（最终候选免尝试窗 + 90s 预算）——runbook §27

> 运行实例：pocketd PID 16346（:8088，16:18 由并行会话以 HEAD=8240062 等价代码
> 重启部署，含 9a74236 90s 预算/最终候选放宽 + 8240062 新错误形状回退修复）。
> **本轮未重启服务、未改任何代码**——纯运行时验证轮；网关配置经
> POST /api/llm-gateway/config 三次临时变更（R3'/R4/R1b），结束后已恢复原状
> 并逐字段核验一致（config-before.json）。全程零密钥。

## 背景与上游状态（16:13~16:22 探测）

- `probe-chat-*-1614.txt`：16:14 三通道——glm-5.2 `200 pong` 3s（恢复）；
  minimax/kimi `502 model_not_found`（0s 快速失败、不回退）——该形态即
  8240062 修复的缺口，本档为独立在先的线上佐证（并行会话 16:20 独立发现
  并先行修复提交，归属以其为准）。
- `model-scan-1616.txt`：16:22 串行复探三模型全 `200`（glm 16.3s / minimax
  1.6s / kimi 6.2s→响应 model=glm-5.2，疑上游路由或旧形状 no_candidate
  触发本仓回退，无法从外部分辨）。**注**：此前一次 9 模型并发扫描触发网关
  429 rate_limit_exceeded（§25.1 同类）——上游限流对突发敏感，探测须串行。
- `embed-probe-1614.json`：`/api/embed` 仍 `503 no_provider`——三缺口中唯一
  未恢复项，任务①维持待上游（不伪造）。

## 实例级验证（9a74236 语义，PID 16346）

前置探测确认：kimi-k3 上游无 provider 且 **stream 路径挂死**（非快速失败，
§22.3 形态延续）——用它作「挂死候选」即可确定性构造两个核心场景，不依赖
glm 时延相位（glm 当日 2.8s~16s 反复横跳，prompt 加大也无法稳定超窗）。

| # | 场景（配置临时变更） | 预期（新代码） | 实测 | 存档 |
|---|---|---|---|---|
| P | 显式 kimi-k3，preferred=[glm]（kimi 非最终） | 20s 尝试窗杀挂死候选 → retry 帧 → glm 收尾 | 首帧 `retry:"glm-5.2"` @T+20s → pong → stop；全程 **22.0s** | kimi-hang20s-fallback-glm.sse.txt |
| R4 | preferred=[kimi-k3] 单候选（kimi 即最终候选） | 免 20s 窗，挂死由 **90s 整链预算**兜底 → 规整错误终态 | 错误终态 @**T+89.98s**（旧代码 ~20s 即死）；SSE 存活 90s（写死线豁免有效） | r4-kimi-final-budget.sse.ts.txt |
| R3' | preferred=[no-such-model-r3, glm]，auto | invalid_model 快速失败跳过 → retry 帧 → glm 收尾 | retry 帧 @T+0.37s → glm 15.4s 成功（§16.2 场景在新实例回归 ✅） | r3-invalidmodel-skip.sse.ts.txt |
| R1b | preferred=[glm]，显式 glm + 长 prompt（5652 prompt tokens） | 最终候选直达成功 | T+14.9s 直达成功（glm 快相；慢相下同场景即 R4 的存活版） | r1b-glm-final-longprompt.sse.ts.txt |

服务端日志互证（`server-log-excerpt.txt`，`logs/backend-dev.log` fd 正常、
本轮请求全部落盘）：

```
16:33:52 [llm-auto] kimi-k3 -> fallback glm-5.2 (context deadline exceeded)
16:33:54 [SLOW] POST /api/llm/stream - 200 (22.006s)          ← P
16:36:26 [llm-auto] stop fallback chain: model=kimi-k3 err=context deadline exceeded
          answered=false fallback="" ctx_err=<nil> budget_left=-1ms
16:36:26 [SLOW] POST /api/llm/stream - 200 (1m30.001s)        ← R4：90s 预算准点
16:36:45 [llm-auto] no-such-model-r3 -> fallback glm-5.2 (llm-gateway stream 400: invalid_model ...)
16:37:00 [SLOW] POST /api/llm/stream - 200 (15.402s)          ← R3'
16:37:28 [SLOW] POST /api/llm/stream - 200 (14.930s)          ← R1b
```

**结论**：9a74236 两项语义（最终候选免尝试窗、90s 整链预算）+ 8240062
（model_not_found/no_candidates 形状回退）均在运行实例上得到确定性验证；
retry 帧序（retry→content→done/stop→usage→[DONE]）与 `[llm-auto]` 日志
（fallback 行 + budget_left 终止行）符合设计。

## 会议纪要应用路径说明（§25.6 #3 API 级覆盖口径）

应用侧纪要生成走 `meetings-ai.ts streamChat` → **同一 `/api/llm/stream`**
（SSE retry 灰字即 retry 帧）。本档 R1b/R4/P 已实例级覆盖该端点的
直达/回退/预算兜底三态，纪要级 prompt 形状差异不改变服务端链路语义；
真机 UI 复验（种子方法 §22.4 已备）留待上游慢相窗口或下一轮——当前
glm 快相下真机 gen 预期为直达成功（§24.3 gen#2 同态），retry 路径真机态
依赖 kimi 恢复或 glm 慢相，属上游时序而非代码项。

## 工具

- `tools/req-long.json`：会议转写风格合成长 prompt（9626 字符 ≈ 5652
  tokens，合成数据零真实信息）；`tools/req-short-{kimi,auto}.json`。
- `tools/ts_sse.py`：SSE 逐帧时戳器（**注意**：勿在管道中接 head/tail——
  SIGPIPE 会连锁掐断 curl 存档；先落盘后查看）。

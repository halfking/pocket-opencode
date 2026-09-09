# T3 诊断报告：Web 端 AI 流「120s 看门狗」真因定位

| 字段 | 值 |
|------|----|
| 日期 | 2026-09-10 |
| 任务来源 | handoff 2026-09-10 §2 T3（P1） |
| 结论 | **全链路不存在任何一层 120s 掐断；120s 是纯前端看门狗预算，属设计行为。真正需要修的是 `deploy/本地方案/nginx.conf` 的 SSE 缓冲缺失。** |

---

## 1. 背景与问题

AiStreamRuntime（M1）带 120s 无字节看门狗；M4 已修「后台时间计入预算」的误报。遗留问题：
**120s 是否由 SSE proxy / 反代超时决定？** 即：链路上是否存在某一层在 120s 主动断流，导致前端
看门狗只是「恰好同时」报警？

## 2. 方法

1. **代码审计**：前端 watchdog 常量、后端 `http.Server` 超时、SSE handler 写 deadline、上游
   fallback 预算、仓库内两份 nginx 配置。
2. **实测**：`curl --trace-time --trace-ascii` 直连 dev 8090 `POST /api/llm/stream`，记录 90s
   全过程时间线（/tmp/sse_trace.log，2026-09-10 01:19–01:20）。

## 3. 链路超时全景（审计结果）

| # | 层 | 超时 | 生效条件 | 代码/配置位置 |
|---|----|------|----------|----------------|
| 1 | 前端 AiStreamRuntime | **120s** 活跃无字节（hidden 暂停不计时） | 纯客户端自主判定 | `frontend/src/native/aiStreamRuntime.ts:97` |
| 2 | 后端上游 fallback 单次尝试 | 20s | 每个上游模型 | `backend/internal/server/llmbff_provider_adapters.go:241` |
| 3 | 后端上游 fallback 总预算 | **90s** | 上游全失败时 90s 后下发 error frame 主动收尾 | `llmbff_provider_adapters.go:242` |
| 4 | 后端 SSE 写 deadline | 150s | 每 chunk 刷新；150s 无**可写**数据才断 | `backend/internal/server/server_llmbff.go:171` |
| 5 | http.Server WriteTimeout | 30s | 被 `longLivedPathMiddleware` 对 `/api/llm/stream` **清零**，不生效 | `backend/internal/server/server.go:826-846`、`cmd/pocketd/main.go:1153` |
| 6 | http.Server IdleTimeout | 120s | **只管请求间空闲**，流内不适用 → 与本问题无关 | `cmd/pocketd/main.go:1154` |
| 7 | nginx（252 itestu.cn 切换版） | read/send 3600s + `proxy_buffering off` | SSE 友好 | `docs/2026-09-07-local-cutover/nginx/pocket.itestu.cn.conf:47-49` |
| 8 | nginx（deploy/本地方案） | read/send 180s，**未关 buffering** | 见 §5-R1 | `deploy/本地方案/nginx.conf:12-18` |

**没有任何一层的值是 120s。**

## 4. 实测时间线（直连 8090，无代理）

```
01:19:13.807  TCP 连接建立，POST /api/llm/stream 发出
01:19:13.825  收到首帧（53B，model: claude-fable-5）
01:19:33.830  retry 帧（+20s，claude-opus-4-8）      ← autoFallback 单尝试 20s 超时
01:19:53.833  retry 帧（+20s，claude-sonnet-5）
01:20:13.835  retry 帧（+20s，gpt-5.6）
01:20:33.838  retry 帧（+20s）
01:20:43.828  error frame："context deadline exceeded"  ← 90s 总预算到
01:20:43.828  连接正常关闭（Connection #0 left intact）
```

- 全程 90s，无任何层在 120s 介入；上游失败时后端**主动**通过 error frame 收尾（前端走
  `reason=server-error`，不误报 watchdog）。
- retry 帧每 20s 一帧，客观上也是 SSE 心跳——使服务器 150s 写 deadline 与 nginx 读超时都
  不会命中。

## 5. 发现的真实风险与建议

### R1（建议修）：`deploy/本地方案/nginx.conf` 的 `/api/` 未关 proxy_buffering

`proxy_buffering on`（默认）时 nginx 会攒 SSE 块再下发：前端观感为「长时间无输出 → 突然爆发」，
且可能与前端 120s 看门狗叠加造成误伤（客户端确实 120s 没看到字节——字节在 nginx 缓冲区里）。
**这可能是现场偶发「整段卡住后一次吐完」的直接解释。**

建议（二选一，推荐 a+b 双保险）：
- a. `deploy/本地方案/nginx.conf` 的 `location /api/` 内加 `proxy_buffering off;`（对齐
  itestu.cn 版本）；
- b. 后端 SSE handler 响应头加 `X-Accel-Buffering: no;`（`server_llmbff.go` 流式端点：llm/stream、
  mobile/sessions、gateway live-stream），对任意中间代理生效，不再依赖逐份 nginx 配置。

### R2（可选）：看门狗文案与语义

`reason=watchdog` 当前文案为「网络中断，可重试」。触发时多数场景是「上游 90s 预算内一帧都没有
（连 retry 帧都没有）」或代理缓冲——并非用户网络断。若 R1 修完仍偶发，可把 watchdog 预算做成
可配置（服务端下发），或在文案上区分。

### R3（长期）：直连（非 fallback）模式无 20s 心跳

`autoFallback` 链路自带 20s retry 帧，客观上是 SSE 心跳；但指定模型的直连请求若上游长时间不吐
首 token，只有 150s 服务器写 deadline 兜底，前端 120s 看门狗会先触发。可考虑服务端在流内每 30s
注入 SSE comment（`: ping`）。

### 结论

- **不需要也不应该**去「修 120s」——它是前端预算，且 M4 的 hidden 暂停已消除后台误报。
- 需要落地的是 R1：nginx buffering 缺失是链路中唯一真实的 SSE 传输层缺陷。

## 6. 验收清单核对（handoff T3 要求）

| 要求 | 状态 |
|------|------|
| 抓 SSE 长连接定位 120s 关闭方 | ✅ curl --trace-time 实测 + 时间线（§4） |
| 判断是否反代超时 | ✅ 不是；两份 nginx 均 ≠120s，实测直连与配置审计一致 |
| 修复 PR 或配置变更建议 | ✅ R1a（nginx 一行）+ R1b（后端 header，双保险）；R2/R3 可选 |

— end of report —

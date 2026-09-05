# 2026-09-05 recovery-probes：上游缺口复探 + 空流透传缺口修复（§28）

> 运行实例：轮初为 PID 16346（§27 实例，未含空流修复）；17:19 经
> start-dev.sh 按端口重启至修复版 **PID 40182**（HEAD=bd4156c + 空流修复，
> 90s 预算语义不变）。:8090 姊妹仓进程（PID 42847）全程无恙。零密钥。

## 探测存档（串行小间隔）

| 文件 | 时刻 | 内容 |
|---|---|---|
| embed-probe.json | 16:54 | `/api/embed` 503 `no_provider` 0.34s——任务①维持待上游 |
| probe-chat-kimi.txt | 16:55 | 显式 kimi-k3 → 200 3.8s `model:"glm-5.2"`（kimi 仍无 provider，8240062 回退链在线生效） |
| r1surv-glm-final.sse.ts.txt | 16:53 | 显式 glm 长 prompt（5652 tokens）活时戳存档：TTFT 8.13s / 总 8.42s——快相，存活版 R4 未触成 |
| r1surv-glm-final.sse.raw | 16:52 | 同场景首测（总 9.04s，时戳器事后回放无效故重做） |
| meeting-shape.sse.raw | 17:09 | 会议同形请求（auto）：retry minimax → 流式正文 20s 被尝试窗杀 → T+40s 规整错误终态 |
| probe-chat-glm-1729.txt | 17:31 | chat auto 502 no_candidate 1.1s——上游全灭相（修复版实例冒烟） |

## 空流缺口（§28.3）

- 真机复现：17:06 emulator-5554 纪要生成 ≤3s 终态「模型未返回内容（空流）」
  （截图 04-generating.png，实例为未含修复的 PID 16346）。
- 根因：上游「200+零帧」（[DONE]-only/keepalive/非 SSE 体）在回退链
  `err==nil` 分支被当成功透传。
- 修复 + 双测试 + `go test ./...` 46 包全绿；17:19 重启部署（PID 40182）。

## 真机复验（§28.2/§28.4/§28.5）

- emulator-5554 被并行会话同时段活跃操作（16:55:17 APK 更新、页面状态
  漂移），本轮不争用，改独立 AVD `pocket_test2`（:5556，-no-snapshot
  冷启动）+ HEAD 现场构建 APK（17:27）。
- 03-meeting-detail-before.png：17:07 种子会议详情（4 段转写）；
  05-all-dead-terminal.png：全灭相下生成终态（PID 16346 旧实例，
  空流文案为其 17:06 遗留显示，本轮点击未触达生成——争用期误点记录，
  有效复验以 pocket_test2 结果为准）。

## tools/

- `cdp_eval_dyn.py` / `cdp_click_dyn.py`：CDP 探针/受信任点击（page ID
  动态发现，§22.4 归档版的可复用改版）。
- `seed_meeting_budget90.js`：种子脚本（含 §22.4 三坑的全部规避：
  createConnection 直调+already exists 复用、逐条 run、无 `--` 头）。

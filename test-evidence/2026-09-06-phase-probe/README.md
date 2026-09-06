# 2026-09-06 相位观测轮存档（runbook §30）

本轮时间线（本机时区）：

| 时刻 | 事件 |
|---|---|
| 00:07 | 接手：HEAD f339be4 与远端一致；日志通道第四次外部截断（fd 25745>文件 22118）→ 按端口重启（PID 75266）恢复 |
| 00:08~00:10 | 串行探测：glm `no_candidates` / kimi `invalid_model` / embed `no_provider` / auto→claude-sonnet-4.5 `no_candidate`（§29.3 漂移延续）——全灭 |
| 00:11 | 挂 `probe_watch.sh` 守候（glm+auto 双发/轮 ×8，~40min 窗） |
| 00:2x | ④前置：APK 重建。**纠偏记录**：`npm run build`（mode=production）打进 `.env.production` 占位域名 `https://pocket.example.com` → App 全部 API 报 `Failed to fetch`；正法 `node scripts/build-mobile.mjs android dev`（mode=android-dev → `10.0.2.2:8088`，sanity check 校验 bundle 含正确 base）。重建装入 pocket_clone（emulator-5556，在线无需冷启） |
| 00:34:11 | **守候命中：glm-5.2 恢复** `{"content":"Pong"}` 200（auto 仍灭）——probe-watch.txt try#6 |
| 00:39 / 01:43 | glm 复探 200×2（pong 稳定）；embed/kimi 仍灭 |
| 01:15~02:07 | ④纪要真机三轮（详见下） |
| 01:41 | 日志通道第五次外部截断（fd 12577>文件 1034，mtime 停 00:20）→ 按端口重启（PID 56956）恢复；配置核对一致 |
| 02:02 | 注入 `POCKET_LLM_MODEL=glm-5.2` 重启（PID 70783，不动 preferredModels=[]）——App 纪要仍 90s 超时（见 ④ 结论）；02:11 回滚标准 env（PID 83845） |
| 02:0x | ai-chat 显式 glm 对照改走 API 级 SSE（App localStorage 注入被 store 清理）：`/api/llm/stream model=glm-5.2` 0.19s 快速失败，上游 nginx 502 终态正常收敛 |
| ~02:1x 后（会话长暂停） | **glm 再灭**：10:43 复探 glm#4/#5 均 502（nginx 层） |
| 10:45 | 终态快照：auto/embed 均 502——全灭相持续但形态变化：503 no_candidates（无候选）→ 502 Bad Gateway（有候选被路由、上游入口挂） |

## ④ 纪要真机成功态：本轮判明「结构性不可达」

- App 路径（MeetingDetailView → summarizeMeeting → `llmBffApi.streamChat`
  kind=meeting_summary → POST /api/llm/stream，**无 model 字段**）：
  BFF 端空 model 解析 → preferred 首选；`preferredModels=[]`（并行方终态）
  → auto → 即席最终候选 claude-sonnet-4.5（灭）→ 90s 预算耗尽。
  实测两轮均 `context deadline exceeded` 红字终态、按钮恢复、无悬挂，
  `meetings_with_summary=0` 无幻影写入（cdp-counts-after-summary-attempts.txt，
  CDP 桥法 window.Capacitor.Plugins.CapacitorSQLite 库 lobster）。
- **`POCKET_LLM_MODEL` 只影响后端内部一次性 chat（llmChatOnce，
  server_meeting.go API 级 summarize/refine），不影响 BFF 流式路径**——
  注入实验（02:02）证实。
- 结论：④ 成功态在「preferredModels=[] + auto 灭 + 仅 glm 独活」相位组合下
  结构性不可达，非 App 缺陷。达成条件二选一：auto 链候选恢复；
  或并行方恢复显式 preferredModels 含可用模型（§28.4/§29.6 #3 归属并行方）。

## ① 空流修复留观

- 恢复窗（00:34~01:43+，≥69min）内 glm 全部正常 200，未现空流形态；
  灭相期 SSE/chat 均为快速失败形态（no_candidates / 502，0.2s~0.6s），
  「200+零帧」由头未现——修复未现失效信号，留观窗口继续。
- App 纪要三轮均为 90s 超时收敛（灭相 no_candidate 路径），非空流形态。

## 文件

- `probe-watch.txt` 守候全程（try#6 命中）
- `probes-phase.txt` 各时刻探测快照
- `glm*.json / auto*.json / kimi*.json / embed*.json / nomodel.json / final-*.json` 逐发响应
- `sse-glm-app-path.txt` + `sse-glm-ts.txt` SSE 存档（先落盘后查看，时戳器活接管道）
- `cdp-counts-after-summary-attempts.txt` 纪要尝试后 CDP 基线（全 0，无幻影写入）
- `log-excerpt-final.txt` 收口时日志通道全文（通道健康：fd 偏移=文件大小）
- `tools/cdp_unlock.py` 主密码解锁沉淀（口令经 backend/start-dev.sh 缺省约定
  间接提取注入，不经命令行/落盘明文——§14.1 变量注入规约）
- `tools/cdp_eval_dyn.py / cdp_click_dyn.py` 拷贝自 2026-09-05-recovery-probes/tools

## 环境收口状态

- backend :8088 PID 83845 标准环境（`backend/start-dev.sh`，无注入变量）；
  :8090 姊妹仓无恙；preferredModels=[] 全程未 POST。
- pocket_clone（emulator-5556）装有 **android/dev 正确 base 的最新 HEAD APK**
  （01:21 构建，含 b39714d 前端加固）；App 已解锁、种子会议在列，
  下轮 ④/② 可零预热直接进入。
- 守候脚本 `probe_watch.sh` 复制在本目录（.token 已清理，复用需重建）。

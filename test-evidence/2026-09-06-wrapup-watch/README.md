# 2026-09-06 wrapup-watch：AI 网关收尾轮(runbook §31)

> 接手 HEAD=7444874(与远端一致)。本轮窗口 13:44~14:35,中途 :8088 于
> 14:20:51 被并行会话重启接管(认证变更),BFF 探测通道自此收归并行方,
> 本轮提前收口。零配置 POST,preferredModels=[] 全程未动。

## 时间线

| 时刻 | 事件 |
|---|---|
| 13:44 | 接手核对:git/远端一致;config 逐字段与 §30 一致(preferredModels=[] 维持) |
| 13:45 | **日志通道第六次外部截断**(fd 59973 > 文件 46797,mtime 停 11:06)→ start-dev.sh 按端口重启(PID 95890),通道恢复(偏移==文件) |
| 13:47 | 串行探测全灭:glm 503 **新形态(错误体带 alternatives 目录)** / auto→claude-sonnet-4.5 no_candidate / embed no_provider / kimi-k2-turbo-preview invalid_model(已退役) |
| 13:48 | Catalog 漂移判明:alternatives 目录含 kimi-k3、claude-sonnet-5、gpt-5.6-terra、claude-fable-5 等;**kimi-k3/claude-sonnet-5 在册但 model_not_found(列表≠可路由)**;**gpt-5.6-terra 200 pong——首个新可路由模型** |
| 13:54~13:55 | terra 复探 200×2;App 同链路 SSE 短 prompt 1.96s、长 prompt(7122 tok)2.43s 干净收尾——快相 |
| 13:54~14:17 | 守候 probe_watch(v2: glm+auto)→ v3(glm+auto+terra):全灭 |
| 13:59 | App ai-chat 显式选 terra 发消息(①正向对照):请求 14:00:11 到达 BFF |
| ~14:00 | terra 进入挂相:App 单尝试挂满 90s 预算,14:01:41 准点收敛(server-log-excerpt-140141.txt) |
| 14:02 | App 终态:红字 context deadline exceeded ×2、按钮恢复、无悬挂(截图) |
| 14:03 | terra 直探快速失败 503——**存活窗 ≈7min(13:48~13:55)关闭** |
| 14:04 | CDP 桥基线复确认 local_note_vectors=0/local_notes=0/meetings_with_summary=0;embed 终态 no_provider |
| 14:18~14:21 | dev JWT 临期(13:45 签发,生命周期 ≈32min)→ 守候 401;重新登录 invalid credentials |
| 14:20:51 | **并行方重启 :8088(PID 73100,同一 17:19 二进制)**:admin/Veritrans&9527 → invalid credentials;根 .env 网关变量被改指本地(疑似 RedClaw 认证/本地 mock 上游试验,对应 ⑤ 方向)。按规约未干预 |
| 14:17:24 | 最后一轮有效 BFF 终态快照:glm/auto/terra 全灭 |

## 关键文件

- `probe-watch.txt`:守候全程(try#1~#8 v2 + v3 各轮,含 401 段)
- `glm1.json`:503 alternatives 新形态完整错误体;`kimi-k3.json`/`cs5.json`:model_not_found
- `gpt56.json`/`terra2/3.json`:terra 存活窗 pong;`terra4.json`:14:03 转灭
- `sse-terra-short/long.sse.ts.txt`:ts_sse.py 时戳 SSE 存档(快相画像)
- `app-state-1.png`:模型 chip=gpt-5.6-terra;`app-aichat-terra-deadline.png`:红字终态
- `server-log-excerpt-140141.txt`:90s 兜底关键日志逐字补录(通道 14:20 被并行方覆写)

## 下轮注意

1. :8088 现归并行方(PID 73100 起),认证非 admin/Veritrans&9527——接手先
   `lsof -tiTCP:8088` 核 PID + 登录探测凭据语义,勿假定;交还后按 start-dev.sh 重启。
2. App(aiChatStore)残留 text 模态=gpt-5.6-terra 选择,下轮 ① 留观需显式改选。
3. 守候脚本建议用 v3(glm+auto+terra 三发);dev JWT ≈32min,长守候需轮内刷新 token。

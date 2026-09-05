# 2026-09-05 empty-stream-watch：空流修复实例级留观轮（§29）

> 运行实例：接手时 PID 40182（§28 修复版）；因日志通道再遭外部截断
> （fd 偏移 17170 > 文件 8437 字节，18:11 发生），19:46 经 start-dev.sh
> 按端口重启至 **PID 95713**（同一 17:19 修复版二进制，`backend/pocketd`
> mtime 未变）。:8090 姊妹仓进程全程无恙。零密钥。

## 结论速览

- 上游全灭相自 17:06（§28.1）起持续至本轮收口（23:10 仍全灭），中间
  19:47~20:19 守候探测 8 轮全灭、23:09 终态快照全灭。**任务 ①②③④
  前提（任一模型恢复 / embed 恢复 / kimi 恢复或 glm 慢相）全程未开**，
  均维持待上游；空流修复实例级留观无自然复现窗口（全灭相下无候选
  可产生「200+零帧」形态）。
- 本轮实质产出：**环境整备 + 基线固定 + 两项新观察**（见下）。

## 探测存档（串行小间隔）

| 文件 | 时刻 | 内容 |
|---|---|---|
| embed-probe.txt | 19:02 | `503 no_provider` 0.32s |
| probe-chat-glm.txt / probe-chat-kimi.txt | 19:02 | 均 `502 no_candidates` 0.4~0.6s 快速失败 |
| probe-chat-auto.txt | 19:02 | `502 no_candidate`（abab5.5-chat 即席候选）1.1s |
| probe-chat-auto-1943 / glm-1944 / embed-1945 | 19:43~19:45 | 复探仍全灭 |
| probe-watch.txt | 19:47~20:19 | 守候 8 轮（glm+auto 双发/轮，间隔 4.5min）全灭 |
| embed-probe-2004.txt | 20:03 | `no_provider` |
| probe-final-{glm,kimi,minimax,auto}.txt / embed-probe-final.txt | 23:09~23:10 | 终态快照全灭 |

## 两项新观察

1. **auto 即席候选随上游 catalog 漂移**：19:02~20:19 期间 auto 在
   `preferredModels=[]` 下解析到 catalog 首个 `abab5.5-chat`；23:10 终态
   快照同配置下变为 `claude-sonnet-4.5`。网关配置 POST 终态未变
   （preferredModels=[]，逐字段核对），判定为上游 /models 目录顺序漂移
   所致——「空首选即席最终候选」不稳定的又一实例，后续设计 auto 场景
   时须以当次 GET config + 探测实际解析为准。
2. **日志通道外部截断再发**（§17.5/§22.6/§24.4 同款）：mtime 停 18:11、
   fd 偏移 17170 > 文件 8437。经 start-dev.sh 重启恢复（§28.4 同法）。
   本轮 19:46 后全部请求已正常落盘。

## 环境整备（下轮直接可用）

- **pocket_clone（:5556）**：-no-snapshot 冷启动 20s 上线；App 已解锁
  （dev 主密码与 start-dev.sh 默认同值约定，经输入框注入，未引用）；
  §28 种子会议「E2E Budget90 实例纪要复验」在列（01-clone-meeting-
  detail.png：4 段转写完好、纪要空缺、「生成纪要」按钮就位）。
- **CDP 基线**（cdp-baseline-counts.txt，桥法 §22.4/§28.2，
  `window.Capacitor.Plugins.CapacitorSQLite`，库 `lobster`）：
  `local_note_vectors=0, local_notes=0, meetings_with_summary=0`——
  下轮 embed 恢复后应用内新建/编辑笔记即触发 `embedAndStore`
  （notes-store.ts:99/156）→ vec 应 >0；纪要生成成功后
  meetings_with_summary 应转 1。
- **探测守候脚本**（probe_watch.sh）：glm+auto 双发/轮串行守候，任一
  200 即退出——注意 auto 会随 catalog 漂移（观察 1），判定以 glm 为主。
- **前端代码变更提示**：收口前远端合入 b39714d（frontend API base
  加固，纯前端）；下轮 App 路径验证前应按 §28.4 法以最新 HEAD 现场重
  建 APK（clone 上现装 APK 为 17:27 bd4156c 前端构建）。

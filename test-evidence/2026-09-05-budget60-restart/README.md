# 2026-09-05 budget60-restart：provider 恢复轮 + 60s 预算实例收口（runbook §23）

背景：HEAD=12d7f3a 实例（14:48 重启，含 7ecf9ed 三重修复）。14:47 三通道探测仍全灭；
14:55 起 glm-5.2/minimax-m3 相继恢复（ Issue #14 发出约一天后，网关侧动作发生）。
本目录为 runbook §23 的证据档。

## 探测/回归（curl，显式与 auto）
- chat-glm52-recovered.json / chat-minimax-recovered.json：200 pong（恢复确认，4.1s/10.0s）
- chat-auto-preferred-glm.json：auto → glm-5.2（preferredModels 首选回归 ✅）
- embed-still-no-provider.json：503 no_provider（embedding 仍未恢复 → §23 冒烟结论）
- stream-auto-preferred.sse.txt：auto 3.9s 直达 glm，零 retry 帧
- stream-glm52-longprompt-direct.sse.txt：glm 长 prompt（865 prompt tokens）13.5s 直达成功

## kimi-k3 TTFT 量化（任务② 决策输入）
- kimi-ttft-sample.txt：短 prompt 4.5~15.2s（6 样本，中位 ~12.7s）；长 prompt 27.1/32.9s（2/2 超 20s 尝试窗）

## 60s 预算链（恢复前窗口的唯一实测 + 日志恢复）
- llm-auto-1451-recovered.txt：glm 20s→minimax 20s→kimi 20s 全窗耗尽 budget_left=0s 规整终态；
  连接 60s 存活（http=200）——7ecf9ed Unwrap 修复对照验证（§22.3 旧实例 30s 即被 WriteTimeout 掐断）。
  注：该 60s SSE 原始帧档同被外部删除，帧序自会话转录：T+20.01s retry minimax → T+40.01s retry kimi → T+60.01s finish_reason:error。

## 真机 E2E（emulator-5554 冷启动，15:01~15:13）
- mt-live-1.png：纪要生成中段
- mt-success-final.png：**gen#1 成功态——灰字「已切换到 kimi-k3 重试…」+ 纪要正文同框**（§22.4 留待复现的目标态）
  链路：glm（真实纪要长 prompt 超 20s 尝试窗）→ 回退 → kimi-k3 在 60s 预算内出正文
- mt-success-2nd-glm-direct.png：gen#2 glm 直达成功（无 retry 提示）——恢复后首选路径应用内确认
- chat-direct-8/b-1/final.png：ai-chat auto 直达成功 PREFERRED-DIRECT-OK（无灰字）；
  历史气泡保留恢复前 retry+deadline 对照同框
- （分钟级时序：gen#1 点击 ~15:05 → 完成前 eval 15:09 显示「重新生成纪要」+ 正文）

## 环境观察
- 15:05~15:07 本证据目录曾被外部整体删除（15:04 在、15:07 失踪），档案自会话转录重建并逐份注明；
  logs/backend-dev.log 外部截断后 fd 写入丢失（15:05 起文件停 1034B）——§22.6 同类加重，
  服务侧观测以 curl 存档 + UI 取证为准。
- 纪要正文以纯文本渲染 markdown 源码（#/**/表格线原样可见）：功能可用，渲染升级留产品侧。

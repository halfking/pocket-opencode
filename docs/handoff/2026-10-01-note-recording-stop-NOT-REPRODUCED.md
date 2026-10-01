# 负结果：笔记录音「点击停止无效」在代码层复现不出

> 日期：2026-10-01 17:25，北京时间
> 涉及：`frontend/src/native/recordingRuntime.ts`、`frontend/src/features/notes/note-recording.ts`
> 状态：**未复现**。不宣称已修——真机当时离线，未做设备端验证。

## 用户报告的症状

「在笔记打开录音后点击停止没有办法停止」。

## 排查假设与结果

我先怀疑状态机会卡死。依据是 `nextRecordingState` 的转换规则
（`note-recording.ts:10-18`）：`idle → recording → stopping`，而回 `idle` 只认
`'drafted'` 事件。若 `runStop()` 结束时没有把 phase 归位，`toggle()`
（`recordingRuntime.ts:760-761`）会走到 `return null`——**按钮点了永远没反应**，
且 `start()` 会一直报「上一段录音正在收尾，请稍候」。这与症状高度吻合。

**该假设被证伪**：`runStop()` 的 `finally`（`recordingRuntime.ts:737-738`）
无条件执行 `this.phase.value = nextRecordingState('stopping', 'drafted')`，
即使转写失败也会归位。代码里 720-722 行的注释恰好就是在防这个场景，
并给整段兜底转写加了 20s 超时（「否则后端不响应时 phase 永远停在 'stopping'，
录音按钮彻底锁死」）。

## 已有的加固（并行会话，非本轮）

`recordingRuntime.ts` 里停止路径已被系统性加固：

- `stopInFlight` 重入保护，重复点停止复用同一个 promise
- MediaRecorder 在部分 Android WebView 上不派发 `onstop` → 3 秒强制兜底（`694-700`）
- `sttApi.stopStreaming()` 同样无内建超时 → 5 秒上限（`705`）
- mimeType 必须在 `cleanupMedia()` 之前取，否则猜错导致后端拒收、表现为「有内容但转不出文字」（`681-686`）
- 错误文案走 `sttFailureText` 窄口径，不把 `dial tcp … i/o timeout` 甩给用户（`727-733`）

相关提交：`895d950`（TTS 抢前台）、`dcddd80`（实时字幕触发路径）、`e5e3d85`（模块恢复）。

## 验证到的部分

```
node --test src/features/notes/note-recording.test.ts src/native/__tests__/recordingPolicy.test.ts
  → 12 例通过（含 nextRecordingState 的 stopping→idle 断言，见 note-recording.test.ts:25）
node --test src/native/__tests__/recording-voice-prompt.test.mjs \
                src/native/__tests__/recordingRuntimeMimeFallback.test.mjs
  → 30/30 通过
```

## 两次自我更正，记录在案

1. 先说「`stopping` 无法回 `idle`，是覆盖缺口」——错，`note-recording.test.ts:25`
   早已断言该转换。
2. 随后说「不变量有覆盖」，查证后确认属实。

两次都源于**没读完就下结论**。第一轮我只是 grep 到状态机定义就推断缺口，
没先确认测试里有没有断言。

## 为什么仍然不能结案

症状的另一半（语音转文字、即时总结）依赖 STT 后端，而本网关的 ASR 无可用
provider（需你提供凭据）。而「点击停止」这一半虽然代码层站得住，真机当时
adbd 无响应，**未在设备上复现验证过**。用户在真机上若仍遇到，最有价值的
下一步是带 `scripts/diag-recording-state.mjs` 抓一次现场状态机快照，
而不是继续读代码。

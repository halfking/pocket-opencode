# OpenPocket 录音功能全自动真机测试报告

**日期**: 2026-11-18 02:00–02:25
**设备**: Redmi 2411DRN47C（USB 序列号 `4c308e2e`，MIUI，720x1640）
**App**: AI Work Mess v1.2.0-openpocket (build 3, pid 15802)
**测试方式**: 全自动（uiautomator dump + input tap 驱动 UI，无人工介入）

## 总体结果：5/5 通过 ✅

| # | 测试项 | 结果 | 证据 |
|---|--------|------|------|
| 1 | VAD 初始化 | ✅ | `VAD initialized: sampleRate=16000, minSilence=0.5s, minSpeech=0.5s, threshold=0.50` + `VAD enabled: dynamic endpoint detection active`（每轮录音都有） |
| 2 | VAD 端点检测 | ✅ | 106 次十秒兜底 forced emit（间隔稳定 10.0-10.1s）+ **25 次 speech 端点事件**（第三轮有真实语音时触发） |
| 3 | 本地转写（zipformer） | ✅ | 非空中文输出，如 `"自己验证语音转写功能是否正常工作只是一段自动化测试语音用于检测"`（对 Mac TTS 播放语音的识别，字准≈85%，符合 zipformer 小模型预期） |
| 4 | 应用稳定性 | ✅ | 唯一 FATAL 属 com.google.android.gms（系统进程），被测 App 零崩溃零 ANR |
| 5 | 录音服务 | ✅ | 112 条 MeetingRecordService 日志；AudioRecord 前台服务生命周期完整（start→stop(62) 正常） |

## 端到端闭环验证 ✅

录音 → VAD 切片 → 分片落盘 → 本地转写 → 实时字幕（3 条片段）→ 即时总结（LLM）→ **会议纪要**（要点 3 条）→ **待办提取**（"验证语音转写功能是否正常工作"，含转交 ACC 按钮）→ 知识库关联澄清。停止录音后纪要自动生成完成。

会议持久化：3 个会议目录（三轮录音），第三轮 27 个分片；会议列表正确显示「11月18日 01:54 会议 / 11分56秒」。

## 测试过程中发现并解决的问题（按序）

1. **pocketd 后端未运行** → 设置页显示「● 连接异常 · http://192.168.31.32:8088」。后台 exec 启动 pocketd（8088）解决。
2. **APK 内置后端地址过期**（.32，本机现为 .35）→ 登录页「后端服务器」入口 → 自定义地址输入 `http://192.168.31.35:8088` → 保存。
3. **「会议不存在」根因复现**（owner 未验证：恢复态登录没有 verifiedOwner → INSERT 的 owner 写空串 → ownerScopeSql 查不到）→ 退出登录 → 重新密码登录（admin / <DEV-PASS-PREFIX>…<masked>）→ 重建主密码 12345678 → owner 修复 ✅（会议保存后列表可见）。
4. **转写模型缺失**：APK assets 无 zipformer 模型，设备直连 github.com 超时（15s），云端网关 llm.itestu.cn 无 ASR 且域名解析被拒（stt_unavailable）。→ Mac 经 ghproxy 下载 `sherpa-onnx-streaming-zipformer-small-bilingual-zh-en-2023-02-16-mobile.tar.bz2`（341MB，HTTP/2 断流后断点续传补齐），push 4 个必需文件（encoder int8/decoder/joiner/tokens.txt）到 `/sdcard/Android/data/com.kaixuan.opencode.pocket/files/sherpa-models/` → 转写引擎即通。
5. **MIUI 麦克风后台静音**（关键发现）：`appops` 显示 RECORD_AUDIO=foreground；实测 App 前台分片 peak=48（真底噪），切后台后 peak=2（数字静音，HAL 信号功率 -94dB）。MIUI 麦克风授权只有三档（拒绝/每次询问/仅在使用中），无「总是允许」档，`appops set` 改不动。**结论：App 在前台时拾音正常，切后台（即使有前台服务）麦克风被系统数字静音**。
6. **测试音频注入方案演进**：440Hz 正弦波（VAD 人声模型不响应）→ 设备播放器外放（切回 App 时被 AudioHardening 静音，日志证据 `background playback would be muted for com.miui.mediaviewer`）→ **最终方案：App 保持前台 + Mac `afplay -v 6` 外放 TTS 语音（空气传播）**，一次成功。

## 关键工程事实（供后续测试复用）

- MIUI 吞 tap 是常态：任何按钮点击后必须重新 dump 验证状态变化，失败就重试点击。
- 「停止录音」按钮与「开始录音」共用坐标 (603,1412)；FAB「开始会议录音」在 (636,1412)。
- 页面停止 ≠ 服务停止：MIUI 吞 tap 时可能出现「页面显示已停止但服务还在录」的中间态，以 forced emit 日志为准。
- 本地转写引擎返回格式：`{"text":"...","confidence":0,"rtf":~10,"engine":"zipformer"}`（confidence 恒 0 是已知现象）。
- 精翻/精校依赖云端（llm.itestu.cn），网关无 ASR 时显示「精翻未生效 · 下面显示原文」，本地原文转写不受影响。

## 产物

- 完整日志: `/tmp/openpocket-e2e-full.log`（三轮录音全程 logcat）
- 分片样本: `/tmp/parts/`（含前台/后台拾音对照样本）
- 测试音频: `/tmp/test-voice-16k.wav`（macOS say 生成，12s 中文 TTS）
- 模型缓存: `/tmp/sherpa-extract/`

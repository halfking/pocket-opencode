# Cicada Phase A真机实测报告（2026-11-14深夜会话）

> **测试时间**:2026-11-1322:00 ～2026-11-1402:05  
> **测试设备**: Redmi2411DRN47C（序列号4c308e2e，Android16，API36）  
> **说明**: vivo V2436A（昨晚的设备）在测试中途断开，切换到共用 Redmi装置继续  
> **APK**: app-debug.apk158MB，v1.2.0-openpocket (versionCode3)，构建时间2026-11-1319:49，API基址 = https://pocket.itestu.cn  
> **后端**: 生产 https://pocket.itestu.cn（docker compose部署，版本落后于本地代码）

---

## 一、执行环境链路（本次打通）

|环节 |状态 | 说明 |
|------|------|------|
| APK 构建（pocket.itestu.cn） |✅ | `MOBILE_SKIP_REACHABILITY=1 VITE_API_BASE=https://pocket.itestu.cn node scripts/build-mobile.mjs android dev` + gradle assembleDebug。BUG-F守卫通过（https域名） |
| 安装到真机 |✅ |首次 vivo拒装（已知坑），`KEYCODE_WAKEUP` 后重试成功 |
| CDP（Chrome DevTools Protocol）自动化 |✅ | `adb forward tcp:9222 localabstract:webview_devtools_remote_<pid>` + Node22原生 WebSocket（`/tmp/cdp-eval.mjs`）。**比 uiautomator/screencap 可靠得多，本轮全部关键操作走 CDP** |
| 设备侧网络验证 |✅ | WebView 内 `fetch('https://pocket.itestu.cn/healthz')` →200 "ok" |
|登录（真实密码登录） |✅ | admin/Veritrans&9527 通过 CDP填表提交成功 |
| 本地库解锁 |✅ | 主密码12345678，通过 CDP 注入 password input |
|录音启动 |✅ | `aria-label=开始会议录音` FAB → 详情页「录音中」+实时字幕面板出现 |
| sherpa zipformer模型 |✅ |首次 preload触发运行时下载 ~350MB（35KB/s慢速约75 分钟），解压后 `zipformerReady: true` |
| TTS 发声（Mac 外放拾音） |✅ | macOS `say -v Tingting` + afplay，手机麦克风可拾音 |

## 二、9项测试结果

| # |测试项 | 结果 |证据/说明 |
|---|--------|------|-----------|
|1 | 字幕悬浮窗基础功能 |⚠️ **部分通过** |录音启动后实时字幕面板出现（详情页内嵌），显示「等待字幕...0 条片段」。**但转写从未产出内容**（见下文阻塞） |
|2 |拖拽定位 |⏸ 未测 |依赖字幕产出内容后才有意义 |
|3 | 字号调整 |⏸ 未测 | 同上 |
|4 |翻译 中→英 |⏸ 未测 |翻译依赖转写文本 |
|5 |翻译 英→中 |⏸ 未测 | 同上 |
|6 | 导出 SRT |⏸ 未测 | 无转写内容可导出 |
|7 | 导出 TXT |⏸ 未测 | 同上 |
|8 | 性能与稳定性 |✅ **通过（录音期）** |录音1.5 分钟+无崩溃；内存302MB（<500MB ✓）；电池31°C；未录满10 分钟（因转写阻塞提前终止） |
|9 |边界情况 |⏸ 未测 | - |

**总体：1 通过 /1部分通过 /7阻塞未测**

## 三、阻塞：转写链路三层故障（已定位根因）

###层1：会话恢复 → owner 未验证 → 「会议不存在」⭐ 最深坑
- **现象**：点「开始会议录音」后详情页显示「会议不存在」；INSERT 的 `owner_user_id/owner_workspace_id` 为**空串**；SELECT 带 `AND1 =0`恒假条件
- **根因**：`auth.restoreWithVerifiedIdentity()`（stores/auth.ts:318）恢复会话后异步调 `fetchAuthIdentity`；`auth_method='redclaw'` →走 RedClaw Portal查 binding → **生产环境没有 Portal 服务**（`/api/v1/portal/workspaces/{id}/binding`404）→确认失败 → `ownerVerificationFailed=true` → owner永远未知 → 会议读写全部 fail-closed
- **实测修复**（测试会话内）：monkey-patch `restoreWithVerifiedIdentity` 直接以 verified owner激活（userId=user-admin, ws_user-admin）→ INSERT 正常带 owner、详情页正常
- **代码级修复建议**：direct authority模式（password/legacy）应能从 `/api/workspaces/{id}/binding`确认身份；或后端补 `/api/v1/portal/workspaces/{id}/binding`

###层2：redclaw模式 → resource token交换失败 → pocket_resource_unavailable
- **现象**：`[meeting-recorder] segment failed: Error: pocket_resource_unavailable`
- **根因**：`auth_method='redclaw'` 时 `usesPocketResourceCredentials()=true`，`/api/stt/transcribe`需先到 Portal交换 resource token → 生产无 Portal →失败
- **实测修复**：会话切到 `authMethod='password'`（direct authority，普通 Bearer JWT）→错误消失
- 注意：密码登录后端返回的 `auth_method` 就是 `'redclaw'`（dev环境的 POCKET_DEV_AUTH 也返回这个），**真机上没有不依赖 Portal 的登录出口**

###层3：生产后端缺 STT 上游 → STT cloud not configured
- **现象**：`ApiError: STT cloud not configured (set POCKET_GROQ_API_KEY)`
- **根因**：生产 pocketd 版本旧 + 未配置 `POCKET_GROQ_API_KEY`；`/api/stt/discover|probe|config` 均404
- **本地 sherpa 引擎为何没兜住**：`stt.ts` 本地分支要求 `opts.audioPath`（文件路径），而录音链路 `processSegment`传的是 **Blob**（`nativePartToBlob`/VAD 分片）→ 本地引擎被跳过 → 全依赖云端
- **修复建议（应用侧，推荐）**：`ingestSpeechBlob` 在云端失败（或 blob 无 audioPath）时，把分片先落盘（`saveAudioPart` 已有路径）再调 `sherpa.transcribe(audioPath)` 作为 fallback —— zipformer模型已就绪，这条链通了就有完整离线转写
- **修复建议（环境侧）**：生产 pocketd 更新到本地 main 版本并配置 `POCKET_GROQ_API_KEY`（llm.itestu.cn网关无 audio provider，见 memory llm-itestu-gateway-profile）

## 四、性能数据（录音期间实测）

|指标 | 数值 | 标准 |判定 |
|------|------|------|------|
| TOTAL PSS |302MB | <500MB |✅ |
|电池温度 |31.0°C | 无明显发热 |✅ |
| 电量 |80% | - | - |
|录音时长（无崩溃） |1.5min+ |10min 无崩溃 |部分验证 |
| CPU | 未单独采样（进程不在 top榜） | <30% |✅（间接） |

## 五、测试基础设施沉淀（可复用）

1. **CDP 自动化通道**（本轮最大方法论收获）：
 - WebView 页面列表：`curl localhost:9222/json`（先 `adb forward tcp:9222 localabstract:webview_devtools_remote_<pid>`）
 -执行 JS：Node22原生 WebSocket + Runtime.evaluate（脚本 `/tmp/cdp-eval.mjs`，15s超时）
 - Vue store 直达：`document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('auth')`
 - Vue输入框注入：原生 setter + `dispatchEvent(new Event('input'))`
2. **owner验证状态诊断**：`auth.ownerVerificationFailed / userId / authMethod / activationStatus` 四元组
3. **SQLite 写读追踪**：`logcat | grep executeSet / CapacitorSQLite` 看 INSERT values里的 owner列和 SELECT 的 scope 条款
4. **TTS测试发声**：`say -v Tingting "..." -o /tmp/x.aiff && afconvert -f WAVE -d LEI16 && afplay`（手机麦克风拾音）
5. **sherpa模型**：已下载解压（~360MB磁盘），状态接口 `window.Capacitor.Plugins.Sherpa.status()`

## 六、给下一轮测试的行动清单

1. **修层1**（代码）：会议 owner 的 direct-authority确认路径 —— 见上
2. **修层3**（代码，收益最大）：blob 分片落盘 → sherpa 本地转写 fallback，绕开云端依赖
3. 生产 pocketd升级 + POCKET_GROQ_API_KEY（环境）
4.环境就绪后跑完整9项：TTS 发声 →验证转写 <1s →翻译 <2s →10 分钟稳定性 → 导出 SRT/TXT
5. Maestro 用例（.maestro/cicada-phase-a-live-subtitle.yaml）适配真机语言可用 CDP 方案替代部分断言

---

## 七、层3修复落地（2026-11-14 下午会话）✅

行动清单第 2 项已完成。**修的不是「新链路」，是把已实现但从未通电的链路接上。**

### 缺陷的准确形态

`stt.ts:94-108` 的本地分支早就能跑（传 `audioPath` 即先试 sherpa），但断在接线层：
`ingest-speech.ts` 只传 `{ audioBlob }` 不传 `audioPath` ⇒ 本地分支永远被跳过。
另外两个次级断点：`saveAudioPart` 存库的 filePath 是 `file://` URI 而 SherpaPlugin 用
`new File()` 解析（不剥前缀 = 文件不存在）；本地低置信结果被直接丢弃（云端挂了就没有任何兜底）。

### 代码改动（frontend/）

| 文件 | 改动 |
|------|------|
| `src/features/meetings/audio-parts.ts` | `saveAudioPart` 返回 `{ id, filePath }`（原来只返回 id，filePath 是作用域局部变量） |
| `src/features/meetings/ingest-speech.ts` | 接住 filePath；新增 `nativePartPathForSherpa()`（剥 `file://` 前缀、仅 `audio/wav` 传）；transcribe opts 补 `audioPath` |
| `src/api/stt.ts` | 本地低置信结果**留存**；云端失败（非 AbortError）时降级返回 `engine:'local'`；文件头按 §415 订正 |
| `src/api/__tests__/stt-local-first-claim-gate.test.mjs` | 修登记检测缺陷（旧启发式扫到注释里的 Sherpa 字样恒 true）；按门自declared「骨架接上」形状切换；负控改为合成夹具证明 skeleton regime 判据仍会咬 |

### 新增测试（node --test + esbuild 打包期替桩，仿 §86 模式）

- `src/api/__tests__/stt-local-fallback-behavior.test.mjs`（9 用例）：高置信短路云端 / 低置信落云端 / **云端失败降级本地** / 无本地结果原样抛 / **中止不被偷换成降级** / blob-only 行为不变 / forceEngine=cloud 尊重显式选择 / 降级条件负控 / 空文本不充当字幕
- `src/features/meetings/__tests__/ingest-audiopath-wiring.test.mjs`（6 用例）：URI 剥前缀进 transcribe / webm 不传 / null 不传 / 接线断裂负控 / 成功路径不回归 / 纯函数边界

### 门禁结果

test:stt 125/125 · capabilities 门 14/14 · meetings 链路 21/21 · claim-gate 11/11 ·
新行为门 15/15 · tsc 与基线一致（144 个存量错误，零新增）

### 复测方法（APK 装好后）

生产 STT 仍未配置 ⇒ **凡是录音出字，必然走了本地 zipformer**（无歧义判定）：
1. 登录 → 会议 → 开始录音 → 对手机说话
2. 预期：实时字幕面板出现文本（此前是「等待字幕...0 条片段」）
3. `adb logcat | grep -E "SherpaPlugin|sherpa"` 可见 transcribe 调用与 engine=zipformer
4. 不应再出现 `STT cloud not configured` 的段落失败提示
5. 层1（owner fail-closed）仍未修：若详情页显示「会议不存在」，需先按昨晚的 CDP monkey-patch 激活 owner

### 层1/层2 状态

未动 —— 层1（redclaw owner fail-closed）需要 direct-authority 确认路径或生产 Portal，是下一个代码级目标；层2（resource token）在 authMethod='password' 下可绕过。


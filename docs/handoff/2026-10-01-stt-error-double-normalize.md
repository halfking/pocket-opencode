# 潜伏缺陷：STT 错误码被渲染层二次归一压掉（2026-10-01 真机审计）

> 状态：**当前树里不成立**，但只要 `recordingRuntime` 重新接上 `sttFailureText`
> 就会复现。属于"改完另一半才会显形"的耦合缺陷，所以写成文档而不是测试文件——
> 现在放一个必然失败的测试进共享树只会污染别人的回归结果。

## 1. 现象

真机（Redmi 2411DRN47C / WebView 126）录音 3 秒后停止，界面上应该是后端
返回的可行动原因，实际显示的是一句没有信息量的通用文案。

后端真实响应（`POST /api/stt/transcribe`，502）：

```
stt_unavailable: 网关暂无可用的语音转写模型（mimo-v2.5-asr=网关无上游 provider；
mimo-v2.5-tts-voiceclone=网关无上游 provider；mimo-v2.5-tts-voicedesign=网关无上游 provider）；
另有 3 个候选同样不可用；外部语音转写服务未配置 API Key（设置 → 语音转写）
```

## 2. 两条分支的行为对照（都是真机实测）

| 链路 | 归一发生在哪 | 界面显示 |
|---|---|---|
| 会议录音 `MeetingRecorderRuntime` | 写入时（`sttError = sttFailureText(e, …)`） | 完整可行动原因 ✅ |
| 笔记录音 `NoteRecorderRuntime` | 渲染前（`apiError(recError, 'errors.sttNotConfigured')`） | 「语音转写服务尚未配置」❌ |

同一份后端文案，两条路给用户看到的东西完全不同。**用户报的原话正是
「笔记录音没有转成文字」——最需要解释原因的那条路径，恰恰是信息被压掉的那条。**

## 3. 根因

`recordingRuntime` 在**写入** error 时调了 `sttFailureText()`，而它会把
`stt_unavailable:` 错误码前缀**剥掉**（见 `api/stt-error.ts` 的窄口径设计）。
于是 `this.error` 里已经是**面向用户的成品文案**。

渲染层 `NoteListView` / `NoteRecordingStudio` 又调了一次
`apiError(recError, 'errors.sttNotConfigured')`。`apiError` 内部
（`api/error-message.ts`）靠 `extractErrorCode()` 取**第一个冒号前**的
`[a-z0-9_]+` 当错误码 —— 前缀已经被剥掉了，取不到码 → 落回 fallback
「语音转写服务尚未配置」，可行动原因整个消失。

会议侧不受影响，因为它**直接渲染** `sttError`（`MeetingDetailView.vue:23`、
`SessionLiveRecordPanel.vue:23`），没有第二层归一。

## 4. 为什么不能"两层都留着"

`apiError` 的设计前提是「入参是**原始异常**」。而 `NoteRecorderRuntime` 的
`this.error` 全部写入点（`start()` 的权限失败 / 收尾中 / 开始失败，
`runStop()` 的兜底转写）要么是字面文案，要么经 `sttFailureText` 归一，
**没有一个是原始异常**。所以再套一层归一只会丢信息，不会有任何收益。

归一责任应当只有一处。

## 5. 修法

`NoteListView.vue`：

```diff
-const recordErrorText = computed(() =>
-  recError.value ? apiError(recError.value, 'errors.sttNotConfigured') : '',
-)
+const recordErrorText = computed(() => recError.value || '')
```

`NoteRecordingStudio.vue`：

```diff
-const errorText = computed(() =>
-  props.error ? apiError(props.error, 'errors.sttNotConfigured') : '',
-)
+const errorText = computed(() => props.error || '')
```

顺带清掉随之失效的 `useApiError` 导入与实例化。

## 6. 防回归（建议加在 `api/__tests__/stt-error-render-chain.test.mjs`）

`api/stt-error.ts` 与 `api/error-message.ts` 都是无运行时依赖的纯模块，
`node --test` 可以直接 import，所以**行为级**回归可以直接断言：

```js
const { sttFailureText } = await import('../stt-error.ts')
const { toUserMessage } = await import('../error-message.ts')

const BACKEND = 'stt_unavailable: 网关暂无可用的语音转写模型（…）；外部语音转写服务未配置 API Key（设置 → 语音转写）'
const fakeT = (k) => (k === 'errors.sttNotConfigured' ? '语音转写服务尚未配置' : k)

// 复现：runtime 写入后已是成品文案，再过 apiError 就会丢掉可行动原因
const curated = sttFailureText({ message: BACKEND, body: { error: BACKEND } }, '转写失败')
const afterRender = toUserMessage(curated, fakeT, fakeT('errors.sttNotConfigured'))
assert.ok(!afterRender.includes('设置 → 语音转写'))  // 缺陷即在此
```

再加两条源码断言（锁住"归一只在 runtime 一处"这个不变量）：

- `NoteListView.vue` / `NoteRecordingStudio.vue` 不得出现 `apiError(recError` / `apiError(props.error`
- `NoteRecorderRuntime` 的每一处 `this.error.value =` 不得直接取
  `err.message` / `String(err)`（字面量与字面量三元都合法）

## 7. 同一轮查出的另外两件事

### 7.1 TTS 模型被当成 ASR 候选（**已修并已提交** `20b7b3d`）

`internal/stt/discovery.go` 的 `asrNameRe = (?i)(asr|whisper|transcri|speech|audio|omni|voice)`
里的 `voice` 会把 `mimo-v2.5-tts-voiceclone`、`mimo-v2.5-tts-voicedesign`
两个**语音合成**模型拉进转写候选。三重代价：

1. 探测预算是硬约束（`maxProbeCandidates = 6`，网关限流实测 12 次/分钟），
   两个注定失败的槽位被 TTS 吃掉，真正可能可用的 ASR 模型反而探不到；
2. 设置页把合成模型列在「语音转写」分组下；
3. 失败原因里出现「转写失败 + 两个合成模型名」，比只报通用文案更困惑。

修法：新增 `ttsNameRe` / `strongASRRe`，先用 TTS 规则排除，
名字里另有强 ASR 标记的（`whisper-tts`）由 `strongASRRe` 兜住不误杀。
回归测试 `internal/stt/discovery_tts_filter_test.go`。

### 7.2 160 字符硬截尾砍掉了行动指引（**已在树里**）

`api/stt-error.ts` 原来是 `chars.slice(0, max) + '…'`。真机上砍掉的正好是
`外部语音转写服务未配置 API Key（设…` —— 「设置 → 语音转写」这个**唯一的
行动指引**。用户看完整条仍不知道该去哪，比只显示通用文案好不了多少。

已改成中间省略（`MAX_REASON_LEN` 不变，头尾都留）：

```js
const head = Math.ceil((max - 1) / 2) + 1
const tail = max - 1 - head
return chars.slice(0, head).join('') + '…' + chars.slice(chars.length - tail).join('')
```

## 8. 更根本的问题：共享工作树

本轮我的前端修复被并行会话的分支切换整批清掉过一次：

- 并行会话先把未提交工作快照进 `88cb5a2`（`wip(snapshot)`）
- 随后 `checkout main` + `merge origin/main`（fast-forward 到 `a1c4900`）
- 而 `stt-error.ts` / `SettingsSTT.vue` / `usePdfViewer.ts` / `discovery_test.go`
  这些**从未提交过**的文件，在任何分支上都不存在

未提交的工作区文件在共享树里是**随时会消失**的。审计结论若只存在于
未提交文件里，等于没留痕。参见同目录 `2026-10-01-shared-tree-hazard.md`。

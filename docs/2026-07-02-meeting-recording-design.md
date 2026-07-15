# 🎙️ 会议记录模块设计

**版本**: v1.0.0  
**日期**: 2026-07-15  
**状态**: 设计方案 → 实施中  
**归属**: OpenCode Pocket 个人助理 APP — 会议模块

> 配套：主方案 [`2026-07-02-android-personal-assistant-plan.md`](./2026-07-02-android-personal-assistant-plan.md)  
> STT 评估：[`2026-07-02-android-stt-evaluation.md`](./2026-07-02-android-stt-evaluation.md)  
> kxmemory 契约：[`2026-07-02-kxmemory-api-contract.md`](./2026-07-02-kxmemory-api-contract.md)  
> 实施规划：[`.scratch/meetings/01-implementation-plan.md`](../.scratch/meetings/01-implementation-plan.md)

---

## 📌 概要

会议记录模块提供**一键录音 → 实时转写 → 说话人识别 → 滚动摘要 → 智能推荐 → 本地存档 → 事后精翻**的完整闭环。充分利用 pocketd 网关、kxmemory 记忆/总结能力，以及 OpenCode 智能体会话体系，在移动端实现专业级会议助手体验。

**设计原则**：
- 录音与声纹**本地优先**（隐私 + 离线可用）
- AI 摘要/推荐/精翻**服务端**（算力 + 记忆图谱）
- 实时体验**混合架构**（本地 STT 低延迟 + 服务端增量摘要）
- 与笔记/任务/邮件模块**打通**（纪要自动入库、待办同步 ACC）

---

## 🎯 核心能力

| 能力 | 描述 | 运行位置 |
|------|------|----------|
| 会议列表 | 历史会议卡片，显示标题/时间/时长/摘要预览 | 本地 SQLite |
| 一键录音 | 点击即开始，无需预填表单 | 本地 MediaRecorder |
| 元信息采集 | 标题、地点、参与人、日历关联（可录音后补填） | 本地 + 可选 GPS |
| 声纹波形 | 实时 + 回放波形可视化 | 本地 Web Audio API |
| 实时转写 | VAD 分段 → STT，逐句追加 | 本地 sherpa → 云端 Groq 兜底 |
| 语种识别 | 每句话自动检测语种，混合语言会议支持 | 本地 sherpa / 云端 Whisper |
| 说话人分离 | 声纹 embedding + 增量聚类 | **本地** sherpa-onnx ECAPA |
| 实时摘要 | 右上角半透明面板，50% 屏宽 × 50% 屏高 | **服务端** kxmemory 增量 |
| 智能推荐 | 关联笔记/邮件/历史会议/联系人 | **服务端** kxmemory 记忆检索 |
| 即时提醒 | 检测到行动项/截止时间/人名 → 本地通知 | 本地 + WebSocket |
| 本地存档 | 完整 WAV/WebM + 分段 JSON | 本地 Filesystem |
| 事后精翻 | 全文润色、多语言对照、结构化纪要 | **服务端** kxmemory |
| 摘要全屏 | 点击放大 / 按钮缩小 | 纯前端 |

---

## 🏗️ 总体架构

```
┌─────────────────────────────────────────────────────────────────────┐
│                    APP (Capacitor + Vue3)                            │
│  MeetingListView │ MeetingRecordView │ MeetingDetailView            │
│  ┌──────────────┐  ┌─────────────────────────────────────────────┐  │
│  │ Waveform     │  │ 主区域：TranscriptSegmentList（实时转写）      │  │
│  │ Visualizer   │  │  ├ 说话人标签 + 语种标记 + 时间戳             │  │
│  └──────────────┘  │  └ 自动滚动到最新句                          │  │
│                    │  ┌─ LiveSummaryPanel（右上 50%×50% 半透明）─┐ │  │
│                    │  │ 滚动摘要 + 行动项 + 推荐 chips           │ │  │
│                    │  └ 点击 → 全屏 / 缩小按钮                   │ │  │
│                    └─────────────────────────────────────────────┘  │
│  useMeetingRecorder │ useLiveSummary │ useMeetingRecommendations   │
│  meetings-store (local SQLite) │ Capacitor Filesystem (audio)     │
└──────────┬──────────────────────────────┬───────────────────────────┘
           │ REST + WebSocket              │ 本地 sherpa-onnx
┌──────────▼──────────┐         ┌─────────▼──────────────────────────┐
│  pocketd (Go)        │         │  cap-sherpa 原生插件                │
│  - /api/meetings/*   │         │  - VAD 分段                        │
│  - /api/stt/transcribe│        │  - Paraformer ASR                 │
│  - /api/llm/chat     │         │  - ECAPA 声纹 embedding            │
│  - WS: meeting.*     │         │  - 语种检测 (lang-id)              │
└──────────┬──────────┘         └────────────────────────────────────┘
           │
┌──────────▼──────────────────────────────────────────────────────────┐
│  kxmemory (FastAPI) — 会议智能体集群                                  │
│  ┌─────────────────┐ ┌──────────────────┐ ┌─────────────────────┐  │
│  │ meeting-summary │ │ meeting-recommend │ │ meeting-refine      │  │
│  │ 增量滚动摘要     │ │ 记忆图谱检索推荐   │ │ 事后精翻 + 结构化    │  │
│  └─────────────────┘ └──────────────────┘ └─────────────────────┘  │
│  复用：notes classify / SSOT / todos / Qdrant 向量 / Neo4j 图谱      │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 🤖 智能体规划

### Agent 1: `meeting-stt`（本地，非 LLM）

| 属性 | 值 |
|------|-----|
| 运行位置 | **设备端** cap-sherpa 插件 |
| 触发 | VAD 检测到语音段结束（~1.5s 静音） |
| 输入 | PCM 16kHz mono 音频段 |
| 输出 | `{ text, confidence, lang, speakerId, startMs, endMs, embedding }` |
| 模型 | sherpa-onnx Paraformer + ECAPA-TDNN + lang-id |
| 隐私 | 音频段不上传；仅文本片段按需送服务端摘要 |

### Agent 2: `meeting-summary`（服务端 LLM）

| 属性 | 值 |
|------|-----|
| 运行位置 | **kxmemory** `POST /v1/meetings/summary` |
| 触发 | 每新增 3 句转写 **或** 每 30 秒 **或** 话题切换检测 |
| 输入 | `{ meeting_id, segments[], prev_summary, meeting_meta }` |
| 输出 | `{ summary, key_points[], action_items[], decisions[], open_questions[] }` |
| 模型 | 用户 LLM Gateway 配置的 fast 模型（低延迟） |
| 记忆 | 注入用户近期笔记/邮件/历史会议摘要作为 context |

**pocketd 代理**：`POST /api/meetings/{id}/summary` → kxmemory

### Agent 3: `meeting-recommend`（服务端检索 + LLM）

| 属性 | 值 |
|------|-----|
| 运行位置 | **kxmemory** `POST /v1/meetings/recommend` |
| 触发 | 摘要更新后 **或** 检测到实体（人名/项目/日期） |
| 输入 | `{ user_id, transcript_window, summary, entities[] }` |
| 输出 | `{ items: [{ type, title, snippet, link, score }] }` |
| 数据源 | Qdrant 向量（笔记/邮件）+ PG 历史会议 + Neo4j 关系 |

**推荐类型**：相关笔记、历史决议、待回复邮件、同名联系人、类似议题会议

### Agent 4: `meeting-refine`（服务端 LLM，事后）

| 属性 | 值 |
|------|-----|
| 运行位置 | **kxmemory** `POST /v1/meetings/refine` |
| 触发 | 用户点击「精翻」或会议结束后自动排队 |
| 输入 | `{ meeting_id, segments[], audio_duration, target_langs[] }` |
| 输出 | `{ refined_transcript, translations{}, structured_minutes, todos[] }` |
| 模型 | 用户 LLM Gateway 配置的 quality 模型 |
| 后续 | 自动 `POST /api/notes` 入库 + classify → SSOT + todos |

### Agent 5: `meeting-alert`（本地规则 + 可选 LLM）

| 属性 | 值 |
|------|-----|
| 运行位置 | **前端** composable + `@capacitor/local-notifications` |
| 触发 | 摘要 action_items 新增 / 检测到日期表达式 / 用户自定义关键词 |
| 输出 | 本地通知 + 应用内 toast |
| 示例 | 「张三提到周五截止 → 已添加到待办提醒」 |

### 本地 vs 服务端决策矩阵

| 任务 | 本地 | 服务端 | 理由 |
|------|:----:|:------:|------|
| 音频采集/存储 | ✅ | | 隐私、离线 |
| VAD 分段 | ✅ | | 低延迟 |
| STT 转写 | ✅ | 兜底 | sherpa 离线；Groq 低置信回退 |
| 语种检测 | ✅ | | Whisper lang-id 可云端补 |
| 声纹 embedding | ✅ | | 隐私，不上传 |
| 说话人聚类 | ✅ | | 增量聚类本地完成 |
| 实时摘要 | | ✅ | 需 LLM + 记忆 context |
| 智能推荐 | | ✅ | 需向量/图谱检索 |
| 即时提醒 | ✅ | | 本地通知即时 |
| 事后精翻 | | ✅ | 高质量 LLM |
| 纪要入库 | | ✅ | kxmemory classify + SSOT |

---

## 📱 UI 设计

### 页面结构

```
/meetings              → MeetingListView（列表 + FAB 新会议）
/meetings/record       → MeetingRecordView（录音中）
/meetings/:id          → MeetingDetailView（详情 + 精翻）
```

### MeetingListView

- 顶部：搜索 + 筛选（今天/本周/全部）
- 列表卡片：标题、日期、时长、摘要前 2 行、参与人数
- 左滑：删除
- 下拉刷新
- **FAB**：🎙 新会议 → 直接进入录音（元信息可后补）
- 空态：`EmptyState` + 「点击开始第一次会议录音」

### MeetingRecordView（核心页面）

```
┌────────────────────────────────────────────┐
│ ← 返回    会议录音中 🔴 00:12:34    ⏸ 停止 │  ← 顶栏（hideAppHeader 自定义）
├────────────────────────────────────────────┤
│ ▁▂▃▅▇▅▃▂▁ WaveformVisualizer (全宽)        │  ← 声纹波形
├────────────────────────────────────────────┤
│                                            │
│  [张三 · zh] 10:12                          │
│  我们今天讨论 Q3 预算的问题...                 │  ← 主区域：实时转写
│                                            │
│  [说话人2 · en] 10:15                       │
│  The deadline is next Friday...            │
│                                            │
│  [张三 · zh] 10:16                          │
│  好的，那就定在周五前确认。                     │
│                                            │
│                    ┌──────────────────────┐│
│                    │ 📋 实时摘要          ││  ← 右上 50%×50%
│                    │ · Q3预算讨论         ││     半透明 overlay
│                    │ · 周五前确认截止      ││     可点击放大
│                    │ ─────────────────    ││
│                    │ 💡 相关：Q2预算笔记   ││
│                    │ 💡 邮件：审批待办     ││
│                    └──────────────────────┘│
├────────────────────────────────────────────┤
│  📝 补充信息  │  👤 标注说话人  │  ⏹ 结束   │  ← 底栏操作
└────────────────────────────────────────────┘
```

**LiveSummaryPanel 交互**：
- 默认：右上角，宽 = 50vw，高 = 50vh，`backdrop-filter: blur(8px)`，`opacity: 0.85`
- 点击面板 → 全屏 modal（`position: fixed; inset: 0`）
- 全屏态右上角「缩小」按钮 → 回到 50% 浮层
- 内容分区：摘要 / 行动项 / 推荐（Tab 或滚动）

### MeetingDetailView（事后）

- 波形回放（可点击跳转）
- 完整转写（说话人可编辑标注）
- AI 纪要（summary + decisions + action_items）
- 「精翻」按钮 → 调用 meeting-refine agent
- 多语言对照视图（原文 | 译文）
- 导出：Markdown / 同步到笔记

### MeetingMetaSheet（BottomSheet）

录音中或结束后可弹出：
- 标题（自动从摘要生成，可编辑）
- 地点（文本 + 可选 GPS）
- 开始/结束时间（自动填充）
- 参与人（标签输入）
- 关联日历事件（Phase 2）

---

## 🔄 数据流

### 实时录音流程

```
用户点击 FAB
    ↓
createMeeting() → 写 local_meetings (status=recording)
    ↓
MediaRecorder.start() + WaveformVisualizer
    ↓
[VAD 循环，每 ~3-5s 一段]
    ↓
sherpa.transcribeSegment(audioChunk)
    → { text, lang, confidence, speakerId, embedding }
    ↓
saveSegment() → local_meeting_segments
    ↓
UI 追加 TranscriptSegmentList
    ↓
[每 3 句或 30s]
    ↓
POST /api/meetings/{id}/summary (pocketd → kxmemory)
    → 更新 LiveSummaryPanel
    ↓
POST /api/meetings/{id}/recommend
    → 更新推荐 chips
    ↓
[检测到 action_item / 日期]
    ↓
LocalNotifications.schedule() + toast
    ↓
用户点击停止
    ↓
MediaRecorder.stop() → 保存 audio 到 Filesystem
updateMeeting({ audioPath, durationMs, status=completed })
    ↓
POST /api/meetings/{id}/refine (异步)
    ↓
WS: meeting.completed → 推送最终纪要
    ↓
自动 POST /api/notes (category=meeting) → classifyNoteAsync
```

### 事后精翻流程

```
用户点击「精翻」
    ↓
GET segments + meeting meta
    ↓
POST /api/meetings/{id}/refine
    ↓
kxmemory: LLM 润色 + 结构化 + 多语言翻译
    ↓
updateMeeting({ refinedTranscript, summary, status=refined })
    ↓
自动创建 note + todos
    ↓
UI 展示对照视图
```

---

## 💾 数据模型

### 本地 SQLite 扩展（schema.ts）

```sql
-- local_meetings 扩展字段
ALTER TABLE local_meetings ADD COLUMN location TEXT;
ALTER TABLE local_meetings ADD COLUMN participants TEXT;  -- JSON array
ALTER TABLE local_meetings ADD COLUMN status TEXT DEFAULT 'recording';
  -- recording | completed | processing | refined
ALTER TABLE local_meetings ADD COLUMN live_summary TEXT;  -- 实时摘要 JSON
ALTER TABLE local_meetings ADD COLUMN refined_transcript TEXT;
ALTER TABLE local_meetings ADD COLUMN recommendations TEXT;  -- JSON

-- local_meeting_segments 扩展
ALTER TABLE local_meeting_segments ADD COLUMN lang TEXT;
ALTER TABLE local_meetings ADD COLUMN confidence REAL;
ALTER TABLE local_meeting_segments ADD COLUMN speaker_embedding BLOB;

-- 声纹库（新增）
CREATE TABLE IF NOT EXISTS local_voiceprints (
    id TEXT PRIMARY KEY,
    display_name TEXT,
    embedding BLOB,
    sample_count INTEGER DEFAULT 1,
    created_at INTEGER NOT NULL
);
```

### pocketd PG（Phase 2 云同步）

复用主方案 `meetings` / `meeting_segments` / `voiceprints` 三表。

### kxmemory 新增端点

| 端点 | 方法 | 用途 |
|------|------|------|
| `/v1/meetings/summary` | POST | 增量滚动摘要 |
| `/v1/meetings/recommend` | POST | 记忆检索推荐 |
| `/v1/meetings/refine` | POST | 事后精翻 + 结构化 |
| `/v1/meetings/classify` | POST | 会议类型/重要度分类 |

---

## 🔔 即时信息提醒

| 场景 | 检测方式 | 提醒形式 |
|------|----------|----------|
| 新行动项 | summary.action_items 增量 | 应用内 toast + 可选本地通知 |
| 截止日期 | 正则 + LLM NER | 「检测到截止日期：周五」横幅 |
| 人名首次出现 | NER | 「是否添加到参与人？」快捷按钮 |
| 关键词命中 | 用户自定义 watchlist | 震动 + toast |
| 会议超时 | 录音 > 2h | 「会议较长，是否分段？」 |
| 网络断开 | navigator.onLine | 「离线模式：转写继续，摘要暂停」 |
| 摘要就绪 | WS meeting.summary_updated | LiveSummaryPanel 闪烁更新 |
| 精翻完成 | WS meeting.refined | 推送通知 + 跳转详情 |

---

## 🔌 API 契约（pocketd 新增）

### `POST /api/meetings/{id}/summary`

**请求**
```json
{
  "segments": [
    { "speaker": "张三", "text": "...", "lang": "zh", "start_ms": 12000 }
  ],
  "prev_summary": "...",
  "meta": { "title": "Q3 预算讨论", "participants": ["张三"] }
}
```

**响应**
```json
{
  "summary": "讨论 Q3 预算...",
  "key_points": ["预算需周五前确认"],
  "action_items": [{ "text": "确认 Q3 预算", "assignee": "张三", "due": "2026-07-18" }],
  "decisions": ["采用方案 B"],
  "open_questions": []
}
```

### `POST /api/meetings/{id}/recommend`

**响应**
```json
{
  "items": [
    { "type": "note", "id": "note-xxx", "title": "Q2 预算纪要", "snippet": "...", "score": 0.89 },
    { "type": "email", "id": "em-yyy", "title": "预算审批", "snippet": "...", "score": 0.76 }
  ]
}
```

### `POST /api/meetings/{id}/refine`

**请求**
```json
{
  "segments": [...],
  "target_langs": ["en"]
}
```

**响应**
```json
{
  "refined_transcript": "...",
  "translations": { "en": "..." },
  "structured_minutes": { "agenda": [], "decisions": [], "action_items": [], "next_meeting": null },
  "todos": [{ "text": "...", "due_date": "2026-07-18", "priority": "high" }],
  "note_id": "note-zzz"
}
```

### WebSocket 事件

| 事件 | Payload | 说明 |
|------|---------|------|
| `meeting.transcript_chunk` | `{ meetingId, segment }` | 新转写段（多端同步） |
| `meeting.summary_updated` | `{ meetingId, summary }` | 摘要更新 |
| `meeting.recommend_updated` | `{ meetingId, items[] }` | 推荐更新 |
| `meeting.completed` | `{ meetingId }` | 录音结束 |
| `meeting.refined` | `{ meetingId, noteId }` | 精翻完成 |

---

## 🔒 隐私与安全

- 原始音频**仅存本地** Filesystem（加密目录，随 Lobster 主密码保护）
- 声纹 embedding **仅存本地** SQLite
- 送服务端的仅为**文本片段**（摘要/推荐/精翻所需）
- 用户可在设置中关闭「云端摘要」（降级为本地 LLM 或仅本地存储）
- 会议删除时同步删除本地音频文件

---

## 📦 依赖与插件

| 依赖 | 用途 | 状态 |
|------|------|------|
| `cap-sherpa` | VAD + ASR + 声纹 + lang-id | stub，Phase 4 |
| `@capacitor/filesystem` | 音频文件持久化 | 待接入 |
| `@capacitor/local-notifications` | 即时提醒 | 待接入 |
| `WaveformVisualizer` | 波形 UI | ✅ 已有 |
| `meetings-store.ts` | 本地 CRUD | ✅ 骨架 |
| kxmemory meeting agents | 摘要/推荐/精翻 | 待 kxmemory 团队 |

---

## 🚀 分期交付

| 阶段 | 范围 | 预估 |
|------|------|------|
| **MVP（Sprint 1）** | 列表 + 录音页 + 波形 + 分段 STT + LLM 摘要兜底 + 本地存储 | 1 周 |
| **Sprint 2** | LiveSummaryPanel + 推荐 + 即时提醒 + 详情页 + 精翻 | 1 周 |
| **Sprint 3** | cap-sherpa 流式 STT + 声纹 + 说话人 | 2 周 |
| **Sprint 4** | kxmemory 专用 agents + 云同步 + 日历集成 | 2 周 |

详见 [`.scratch/meetings/01-implementation-plan.md`](../.scratch/meetings/01-implementation-plan.md)。

---

## ✅ 验收标准

- [ ] 点击 FAB 3 秒内开始录音并显示波形
- [ ] 转写延迟 < 5s（云端）/ < 2s（本地 sherpa）
- [ ] 实时摘要每 30s 更新，面板可放大/缩小
- [ ] 说话人至少区分 2 人（Sprint 3）
- [ ] 混合语种句子正确标记 lang
- [ ] 录音结束后本地可回放，文件可找到
- [ ] 精翻生成结构化纪要 + 自动创建笔记
- [ ] 行动项检测触发本地通知
- [ ] 离线模式：录音 + 本地 STT 可用，摘要暂停并恢复

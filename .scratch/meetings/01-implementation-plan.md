# 会议记录模块 — 实施规划

**日期**: 2026-07-15  
**设计文档**: [`docs/2026-07-02-meeting-recording-design.md`](../../docs/2026-07-02-meeting-recording-design.md)

---

## Sprint 1（MVP）— 当前实施

### 目标
可用的会议列表 + 录音 + 实时转写 + 基础摘要 + 本地存档。

### 任务清单

| # | 任务 | 文件 | 状态 |
|---|------|------|------|
| 1.1 | 扩展 schema 字段 | `frontend/src/native/schema.ts` | ✅ |
| 1.2 | 扩展 meetings-store | `frontend/src/features/meetings/meetings-store.ts` | ✅ |
| 1.3 | meetings API 模块 | `frontend/src/api/meetings.ts` | ✅ |
| 1.4 | useMeetingRecorder composable | `frontend/src/composables/useMeetingRecorder.ts` | ✅ |
| 1.5 | useLiveSummary composable | `frontend/src/composables/useLiveSummary.ts` | ✅ |
| 1.6 | LiveSummaryPanel 组件 | `frontend/src/features/meetings/LiveSummaryPanel.vue` | ✅ |
| 1.7 | TranscriptSegmentList 组件 | `frontend/src/features/meetings/TranscriptSegmentList.vue` | ✅ |
| 1.8 | MeetingMetaSheet 组件 | `frontend/src/features/meetings/MeetingMetaSheet.vue` | ✅ |
| 1.9 | MeetingListView | `frontend/src/features/meetings/MeetingListView.vue` | ✅ |
| 1.10 | MeetingRecordView | `frontend/src/features/meetings/MeetingRecordView.vue` | ✅ |
| 1.11 | MeetingDetailView | `frontend/src/features/meetings/MeetingDetailView.vue` | ✅ |
| 1.12 | 路由注册 | `frontend/src/app/router-mobile.ts` | ✅ |
| 1.13 | pocketd meeting handlers | `backend/internal/server/server_meeting.go` | ✅ |
| 2.1 | STT base64 上传 | `server_assistant.go` + `stt.ts` | ✅ |
| 2.2 | IndexedDB 音频持久化 | `native/meeting-audio.ts` | ✅ |
| 2.3 | useMeetingAlerts 即时提醒 | `composables/useMeetingAlerts.ts` | ✅ |
| 2.4 | kxmemory client 会议 API | `backend/internal/kxmemory/client.go` | ✅ |
| 2.5 | MeetingAlertToast 组件 | `MeetingAlertToast.vue` | ✅ |
| 2.6 | kxmemory 服务端 agents 实现 | kxmemory 仓库 | ⬜ 待 kxmemory 团队 |

### MVP 技术决策

1. **分段 STT**：VAD 语音分段（1.5s 静音切句）→ `sttApi.transcribe`（Sprint 4 接 sherpa 原生流式）
2. **摘要**：pocketd `/api/meetings/{id}/summary`（kxmemory 优先，LLM 兜底）
3. **音频存储**：IndexedDB 持久化（Sprint 4 迁移 Capacitor Filesystem）
4. **语种**：文本启发式检测（CJK/Latin 混合）
5. **说话人**：Web 频谱 embedding + 增量聚类；标注写入 local_voiceprints

---

## Sprint 3 — 本地 STT + 声纹 ✅

| # | 任务 | 文件 | 状态 |
|---|------|------|------|
| 3.1 | Web Audio VAD 分段 | `native/vad-segmenter.ts` | ✅ |
| 3.2 | Web 声纹 embedding | `native/speaker-embedding.ts` | ✅ Web 兜底；原生 ECAPA 待 AAR |
| 3.3 | 增量说话人聚类 | `native/speaker-diarization.ts` | ✅ |
| 3.4 | voiceprints-store + 标注 UI | `voiceprints-store.ts` + `SpeakerLabelSheet.vue` | ✅ |
| 3.5 | cap-sherpa Android 骨架 | `plugins/SherpaPlugin.java` | ✅ 占位，待集成 AAR |
| 3.6 | useMeetingRecorder 重构 | VAD + 声纹 + 标注 | ✅ |

---

## Sprint 4 — 云同步 + 深度集成 ✅（日历/kxmemory agents 待后续）

| # | 任务 | 文件 | 状态 |
|---|------|------|------|
| 4.1 | pocketd PG meetings 表 + CRUD 路由 | `backend/internal/meeting/` + `server_meeting_ingest.go` | ✅ |
| 4.2 | 精翻后 PG note + tasks ingest | `finalizeMeetingRefine` | ✅ |
| 4.3 | 前端 meeting-ingest 本地笔记/待办 | `meeting-ingest.ts` | ✅ |
| 4.4 | MeetingDetailView 精翻联动 + 笔记链接 | `MeetingDetailView.vue` | ✅ |
| 4.5 | 录音结束云同步元数据 | `useMeetingRecorder.stop()` | ✅ |
| 4.6 | kxmemory recommend + refine agents | kxmemory 仓库 | ⬜ 待 kxmemory 团队 |
| 4.7 | 日历集成 | — | ⬜ 未开始 |

**验证**: `npm run typecheck` ✅ · `go build ./internal/meeting/...` ✅

---

## 文件结构（Sprint 1 产出）

```
frontend/src/
├── api/meetings.ts
├── composables/
│   ├── useMeetingRecorder.ts
│   ├── useLiveSummary.ts
│   └── useMeetingAlerts.ts
├── features/meetings/
│   ├── meetings-store.ts
│   ├── meeting-ingest.ts          (Sprint 4)
│   ├── voiceprints-store.ts       (Sprint 3)
│   ├── MeetingListView.vue
│   ├── MeetingRecordView.vue
│   ├── MeetingDetailView.vue
│   ├── LiveSummaryPanel.vue
│   ├── TranscriptSegmentList.vue
│   ├── MeetingMetaSheet.vue
│   ├── SpeakerLabelSheet.vue
│   └── MeetingAlertToast.vue
└── native/
    ├── schema.ts
    ├── meeting-audio.ts
    ├── vad-segmenter.ts
    ├── speaker-embedding.ts
    └── speaker-diarization.ts

backend/internal/
├── meeting/                       (Sprint 4 PG store)
└── server/
    ├── server_meeting.go
    └── server_meeting_ingest.go
```

---

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| cap-sherpa 未就绪 | MVP 用 MediaRecorder 分段 + 云端 STT |
| kxmemory meeting API 未实现 | MVP 用 `/api/llm/chat` 直接 prompt |
| 实时摘要延迟高 | 30s 节流 + 本地缓存 prev_summary |
| 音频文件过大 | WebM opus 压缩；>2h 提醒分段 |
| 麦克风权限被拒 | EmptyState 引导 + 设置跳转 |

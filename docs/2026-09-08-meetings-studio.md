# 会议工作台（As-built）

**日期**: 2026-09-08  
**状态**: 现行产品面（UI / IA）  
**证据**: `source-inspected` + 前端纯函数 `contract-tested`  
**分支**: `feat/meeting-nav-detail-actions` @ `4254438`

> 2026-07 设计 [`2026-07-02-meeting-recording-design.md`](./2026-07-02-meeting-recording-design.md) 仍是架构 / 隐私 / API 参考。  
> **当前用户可见信息架构以本文为准。** 实施切片见 [`.scratch/meetings-studio/01-plan.md`](../.scratch/meetings-studio/01-plan.md)。

---

## 1. 产品定位

会议模块是 OpenPocket 的**听见式工作台**：一键开录 → 实时转写 → 会中滚动摘要 → 待办转交 ACC。  
录音与声纹本地优先；摘要 / 推荐走 pocketd → LLM / kxmemory 兜底。

对标讯飞听见的可落地子集，不照搬声纹库、导图、热词、导入精转。

---

## 2. 已落地特性

### 2.1 列表 `/meetings`

| 能力 | 行为 | 代码 |
|---|---|---|
| 顶栏筛选 | 「进行中 / 已归档」在 `HeaderActionsPortal`，不占内容高度 | `MeetingListView.vue` |
| 点行进详情 | 整卡 / `@activate` → `/meetings/:id` | `openMeeting` |
| 左滑 | 进行中归档；已归档恢复 | `SwipeableListItem` |
| 右滑 | 删除会议 + 本地音频 | `deleteMeeting` / `deleteMeetingAudio` |
| 卡片信息 | 标题、状态、主题或摘要预览、时间、时长、地点、参会人 | `meeting-list.ts` |
| FAB 开录 | 先建本地会议，再 `?record=1` 进详情；地点异步回填 | `startNewMeeting` |
| 分页刷新 | 下拉刷新 + 上拉哨兵 | `useListSentinel` |

### 2.2 工作台 `/meetings/:id`

`/meetings/new` 与 `/meetings/:id/record` 只负责建会并跳到详情，**详情即唯一工作台**。

```
顶栏：录音中波形 │ 总结 │ 更多
主区：左 ~70% 转写  │  右 ~30% 即时总结 / 待办 / 相关检索
底栏：麦克风 dock（空闲=品牌色「录音」，录音中=红色「停止」）
```

| 能力 | 行为 |
|---|---|
| 自动开录 | `?record=1` 或 `status=recording` 时挂载即开麦 |
| 上下文抓取 | 进入详情补地点与标题（GPS + `formatCapturedTitle`） |
| 一键总结 | 顶栏「总结」按当前转写 + 所选技能生成纪要；有行动项则写入 `local_todos` |
| 更多菜单 | 归档/恢复、分类（打开设置）、下达 ACC、删除（确认后回列表） |
| 右侧待办 | 单条「转交」（系统分享/剪贴板）或「ACC」（建一次性 RedClaw 任务） |
| 相关检索 | 转写窗口检索笔记 / 知识库 / 网络，与推荐合并 |
| 说话人 | 录音中可打开标注 sheet，写入本地声纹 |

### 2.3 会议设置

`MeetingSettingsSheet`：标题（可抓取）、主题、地点（手动 / 自动）、参与人、标签（可提取）、总结技能。

技能：滚动纪要 / 决议清单 / 待办提取 / 缺席速览（`meeting-skills.ts`）。

### 2.4 采集与转写

- Web：VAD 分段（约 1.5s 静音）→ `ingestSpeechBlob` 云端 STT；Web Speech 作即时字幕。
- Android：优先 `BackgroundMic` 前台服务，失败回退 Web。
- `stop()` 最多等在途分段 10s 再标 `completed`，并同步元数据。
- 组件卸载会停采集（离开详情即停麦；不是跨路由单例）。

### 2.5 ACC 下达

| 粒度 | 入口 | 条件 |
|---|---|---|
| 单条待办 | 右侧「ACC」 | 有行动项文本 |
| 整场会议 | 更多 →「下达任务给 ACC」 | 已有纪要或待办，否则 toast「请先总结或生成待办」 |

均为 `scheduledTasksApi.create`：`kind=redclaw_chat`、`maxRuns=1`、约 1 分钟后触发；成功跳到计划任务详情。

---

## 3. 路由

| path | name | 说明 |
|---|---|---|
| `/meetings` | `meetings` | 列表 |
| `/meetings/new` | `meeting-new` | 建会并 `replace` 到详情 `?record=1` |
| `/meetings/:id` | `meeting-detail` | 工作台 |
| `/meetings/:id/record` | `meeting-record` | 别名，重定向到详情 |

---

## 4. 数据（本地）

`local_meetings` 已含：`archived_at`、`tags`、`topic`、`summary_skill`。  
`local_todos` 已含：`meeting_id`。

状态：`recording | completed | processing | refined`。

---

## 5. 明确未做（仍属 07 月设计 / 听见延后项）

- cap-sherpa 原生流式 STT / ECAPA AAR
- kxmemory 专用 `meeting-summary / recommend / refine` agents（现用 LLM 兜底）
- 日历关联、声纹库管理页、思维导图、热词、导入音视频精转
- 多语言对照精翻视图、行动项本地推送

---

## 6. 验证

| 层 | 结果 | 证据 |
|---|---|---|
| 列表 / 菜单 / ACC payload | 纯函数测试 | `meeting-list.test.ts`、`meeting-page-actions.test.ts` |
| 技能 / 待办 / 元信息 | 纯函数测试 | `meeting-skills.test.ts`、`meeting-todos.test.ts`、`meeting-meta.test.ts` |
| 真机端到端 | 未作为本轮门禁 | 列表/详情交互需 Capacitor 壳，不能用普通网页代替 |

---

## 7. 关键文件

```
frontend/src/features/meetings/
  MeetingListView.vue          列表 + 顶栏筛选
  MeetingDetailView.vue        工作台接线
  MeetingStudioMenu.vue        总结 / 更多
  MeetingInsightPanel.vue      右栏纪要 / 待办 / 相关
  MeetingSettingsSheet.vue     分类设置
  MeetingMicDock.vue           录音 / 停止
  meeting-page-actions.ts      菜单项 + 会议级 ACC
  meeting-todo-persist.ts      待办落库 + 转交
  use-meeting-studio.ts        归档 / 删除 / 下达
frontend/src/composables/useMeetingRecorder.ts
```

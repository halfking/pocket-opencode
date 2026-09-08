# 会议工作台优化（听见式）

**状态**: 2026-09-08 as-built  
**现行文档**: [`docs/2026-09-08-meetings-studio.md`](../../docs/2026-09-08-meetings-studio.md)

---

## 现状（2026-09-07 开工时的代码实测）

1. 列表 `openMeeting` 对录音中会议跳 `meeting-record`，路由表只有 `meeting-new` / `meeting-detail`，录音 FAB 进不去详情。
2. `SwipeableListItem` 用 touch/mouse 拖动手势，Android WebView 常吞掉子元素 click。
3. 左滑只有删除、无归档；地点仅在已写入时显示，参会人从未展示。
4. 录音页与详情页分裂：没有 70/30 工作台，导航栏没有设置 / 一键总结。

## 讯飞听见对标（取可落地子集）

- 采用：点麦克风即进工作台；标题栏波形；左稿右洞察；会中滚动摘要；待办提取；说话人。
- 延后：声纹库管理、思维导图、语篇规整、悬浮字幕、热词库、导入音视频精转。
- 产品差：主题 / tag / 地点自动抓取；总结技能可选；待办转交他人或 ACC。

## 目标信息架构

- 列表：点行进详情；左滑归档、右滑删除；展示时间 / 地点 / 参会人 / 状态；FAB 创建并 `?record=1` 自动开录。
- 详情即工作台：顶栏波形 +「总结」+「更多」；主区左 70% 转写、右 30% 即时总结与检索。
- 设置：主题、tag、地点（手动 / 自动）、标题（抓取 / 编辑）、总结技能。
- 待办：从纪要 / 滚动摘要生成，可分享转交或建一条 ACC 一次性任务。
- 麦克风：空闲=品牌色「录音」，录音中=红色「停止」；busy 时禁用，避免连点。

## Schema（rule 57）

`local_meetings` 增：`archived_at` / `tags` / `topic` / `summary_skill`。  
`local_todos` 增：`meeting_id`。

| 字段 | writer | reader |
|---|---|---|
| archived_at | archiveMeeting | listMeetings / rowToMeeting |
| tags, topic, summary_skill | create/updateMeeting | 设置页 / 总结 |
| meeting_id | createMeetingTodos | 详情待办 |

---

## 切片与落地

| # | 切片 | 状态 |
|---|---|---|
| 1 | 纯函数：列表展示、标题/地点抓取、技能、待办去重 | ✅ |
| 2 | store + migration | ✅ |
| 3 | 列表 + 路由（点行、滑归档/删除、FAB→详情） | ✅ |
| 4 | 工作台 UI + 麦克风 dock | ✅ |
| 5 | 设置 / 总结 / 待办转交 | ✅ |
| 6 | 顶栏筛选 + 详情更多（归档/删除/分类/会议级 ACC） | ✅ `03-nav-detail-actions.md` |

录音采集 / 云同步 schema 不在本工作台范围（见 `feat/sync-schema-conflict-agent`）。

## 不扩

声纹库管理页、导图、日历、kxmemory 专用 agents、cap-sherpa AAR。

# 会议列表导航 + 详情操作

**状态**: ✅ 已落地（2026-09-08）  
**提交**: `4254438` `feat(meetings): 列表筛选举到顶栏，详情可归档删除并下达 ACC`

## 范围（最小）

1. 「进行中 / 已归档」从内容区 `filter-row` 挪到顶栏右侧（HeaderActionsPortal），腾出列表高度。
2. 列表行点击进入详情（保留左滑归档、右滑删除）。
3. 详情顶栏：总结 + 更多。更多里可归档/恢复、删除、分类（打开设置）、按会议下达 ACC 任务。
4. 右侧待办仍可单条转交 ACC。

## 不改

- 录音采集 / schema / 同步分支 `feat/sync-schema-conflict-agent`
- 不扩听见式新能力（声纹库、导图等）

## 文件

- `meeting-list.ts`：筛选项常量
- `meeting-page-actions.ts` + test：菜单项、会议级 ACC payload
- `MeetingListView.vue`：顶栏筛选
- `MeetingStudioMenu.vue`：详情操作菜单
- `MeetingDetailView.vue`：接线
- `use-meeting-studio.ts`：归档 / 恢复 / 删除 / 下达

## 验收（对照代码）

- [x] `MEETING_LIST_FILTERS` = 进行中 / 已归档，由顶栏消费
- [x] 进行中菜单：归档、分类、下达任务给 ACC、删除
- [x] 已归档菜单首项为「恢复」
- [x] 无纪要且无待办时不可下达；有其一即可
- [x] 会议级 ACC payload `source=meeting-dispatch`，含标题 / 主题 / 纪要 / 待办

package server

// §938：会议待办落库的**唯一口径**。
//
// ⚠ 为什么单独一个文件：这件事有**两条落库路径** ——
//
//	- 精校链：handleMeetingRefine → finalizeMeetingRefine → ingestMeetingTasks
//	- from-source 链：POST /api/tasks/from-source → createTasksFromMeeting
//
//	而「同一段逻辑抄两遍 ⇒ 同一缺陷必然出现在两处」（§80 记过的形状）
//	⇒ ⇒ 「什么算同一条待办」「批内怎么去重」「查重入口在哪」「落到哪个租户」
//	这四件事必须**抽成一份**，两条路径共用。
//
//	修复前的真实形态：from-source 链**已经有**按 (origin_kind, origin_ref, title)
//	的幂等，但它键上的溯源列**精校链一条都不写** ⇒ ⇒ 两条链互相看不见
//	⇒ ⇒ 同一场会议的待办在库里可以有**两套**，而用户只以为有一套。
//
// ⭐ 三层去重，缺一不可：
//  1. 批内：同一件事被提示词要求填进两个数组（`todos` 与
//     `structured_minutes.action_items`）⇒ 提取阶段一定会收两遍。
//  2. 跨次：「录一段 → 停 → 再录一段」是正常用法 ⇒ 同一场会议会精校多次。
//  3. 跨路径：手动点一次「转待办」不应把精校已经建好的再建一遍。
//
// ⇒ 三层都落在**同一个键**上：meetingTodoDedupeKey。

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// meetingTodoTarget 是一次「会议待办落库」的落点。
type meetingTodoTarget struct {
	// WorkspaceID 是**调用方所在的真实租户**。
	//
	// ⚠⚠ 修复前这条链**整个不带** workspace ⇒ CreateTask 里的
	//   normalizeWorkspace("") 把它落进遗留单租户桶 "default"
	//   ⇒ ⇒ 用户在自己 workspace 的任务列表里**永远看不到**这些待办，
	//   而所有人的会议待办**堆在同一个桶里**。
	//
	// ⚠ 形态分叉（必须写清）：claims 里带非 default 的 workspace 时 ⇒ 落错桶
	//   （真机实测的 workspace 是 `ws_user-admin`，见
	//   frontend/src/features/meetings/meeting-ingest.ts 的注释）；
	//   单租户部署（claims 的 workspace 恰为 "default"）⇒ 落点恰好是对的
	//   ⇒ ⇒ **dev 测不出生产问题**。
	WorkspaceID string
	// OwnerID 是创建者。
	//
	// ⚠⚠ 修复前**不带** owner ⇒ owner_id 空 + visibility 默认 private
	//   + 没有参与者 ⇒ task.CanReadWorkItem 对**创建者本人**返回 false
	//   ⇒ ⇒ 详情 / 协作 / 层级这些被鉴权的入口**点不开**。
	//   而 server.go 建任务那条路径的注释明写「owner_id 是『谁建的 /
	//   现在归谁』的唯一可查来源，协作鉴权（CanReadWorkItem）依赖它」。
	OwnerID string
	// MeetingID 是会议 id（= origin_ref）。
	MeetingID string
}

// meetingTodoDedupeKey 是「同一条待办」的判定键。
//
// ⚠ 逐字对应前端 `dedupeTodos`（frontend/src/features/meetings/meeting-ingest.ts：
//
//	`const key = i.text.trim()`）⇒ **两侧同形**，改一处必须改另一处。
func meetingTodoDedupeKey(text string) string {
	return strings.TrimSpace(text)
}

// dedupeTodoItems 批内去重，保留**第一条**。
//
// ⚠ 为什么保留第一条：提取顺序是 todos → action_items →
//
//	structured_minutes.action_items，而前端只读 refine.todos
//	⇒ ⇒ **第一条**（来自 todos）才是与前端同源的那一份，
//	它的 due / priority 最完整 ⇒ ⇒ 保留后面那条会丢掉 due。
//
// ⚠ 去重发生在**建之前**（不是解析时）—— 与前端口径同形：
//
//	前端是 normalizeRefine 照收、createLocalTodos 建之前才 dedupe。
func dedupeTodoItems(items []todoItem) []todoItem {
	seen := make(map[string]bool, len(items))
	out := make([]todoItem, 0, len(items))
	for _, it := range items {
		key := meetingTodoDedupeKey(it.Text)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, it)
	}
	return out
}

// meetingTodoIndex 返回「这场会议已经落过库的待办标题」集合。
//
// ★ 这是两条落库路径**共用**的查重入口（§938 的 D6）。
//
// ⚠ 一次查询同时解决「跨次重复」与「跨路径重复」——
//
//	因为两条链写出的 task 都带同一对溯源列 ⇒ ⇒ 查的是同一批行。
func (s *Server) meetingTodoIndex(ctx context.Context, target meetingTodoTarget) (map[string]bool, error) {
	index := map[string]bool{}
	if s.taskStore == nil {
		return index, nil
	}
	rows, err := s.taskStore.ListTasksByOrigin(ctx, target.WorkspaceID, task.OriginMeeting, target.MeetingID)
	if err != nil {
		return index, err
	}
	for _, row := range rows {
		if key := meetingTodoDedupeKey(row.Title); key != "" {
			index[key] = true
		}
	}
	return index, nil
}

// ingestMeetingTasks 把一场会议的行动项落成任务。
//
// ⚠⚠ §938：修复前这条链同时踩了六个坑（逐条对应简报第 162 条）：
//
//	D1 不带 workspace ⇒ 落进遗留单租户桶 "default" ⇒ 用户看不见；
//	D2 Status="pending" **不在任务状态契约里**（前端只认 active/blocked/
//	   completed）⇒ 三个分桶都不收 ⇒ 列表里根本不显示；
//	D3 不带 owner、不种参与者 ⇒ CanReadWorkItem 对创建者本人为 false
//	   ⇒ 点不开；
//	D4 批内不去重（extractTodos 从三处收集同一件事）⇒ 同一件事建两条；
//	D5 没有跨次幂等 ⇒ 「录一段→停→再录一段」会再插一遍；
//	D6 不写溯源列 ⇒ from-source 链的去重看不见它 ⇒ 手动转一次再多一整套。
//
// ⚠ 查重失败**不挡写入**（与前端同款处置）：查重是防重复，不是准入门槛
//
//	（随手记侧同款；见 meeting-ingest.ts 的 catch 分支）。
func (s *Server) ingestMeetingTasks(ctx context.Context, target meetingTodoTarget, todos []todoItem) int {
	if s.taskStore == nil || len(todos) == 0 {
		return 0
	}
	seen, err := s.meetingTodoIndex(ctx, target)
	if err != nil {
		// 查重失败 ⇒ 记一笔，但**继续写**：把「查重挂了」变成「可能重复」，
		// 好过把「用户的行动项全丢了」变成「一条不建」。
		log.Printf("[meeting] load existing meeting todos %s (ws=%s): %v",
			target.MeetingID, target.WorkspaceID, err)
	}
	created := 0
	for _, td := range dedupeTodoItems(todos) {
		key := meetingTodoDedupeKey(td.Text)
		if key == "" {
			continue
		}
		if seen[key] {
			// 跨次 / 跨路径的重复：这件待办已经在这场会议下落过库了。
			continue
		}
		t := &task.Task{
			ID:          randomID("task"),
			WorkspaceID: target.WorkspaceID,
			OwnerID:     target.OwnerID,
			Title:       td.Text,
			Description: fmt.Sprintf("来自会议 %s", target.MeetingID),
			// ⚠ §938（D2）：'active' 才是任务状态契约里的「进行中」——
			//   前端只有三个分桶（active / blocked / completed），仓内其余三条
			//   建任务路径也都写 'active' ⇒ ⇒ 'pending' 落在**三个分桶之外**
			//   ⇒ 任务列表里根本不显示（而"看不见"对用户就是"没有"）。
			Status:     "active",
			Priority:   mapTodoPriority(td.Priority),
			Source:     "meeting",
			Visibility: task.VisibilityPrivate,
			// ★ 溯源列是**跨次去重与两条路径互见**的唯一依据（§938 的 D6）。
			OriginKind: task.OriginMeeting,
			OriginRef:  target.MeetingID,
		}
		if err := s.taskStore.CreateTask(ctx, t); err != nil {
			log.Printf("[meeting] create task failed: %v", err)
			continue
		}
		// 建完就把键加进集合：下一轮精校的同名条目要靠它挡住。
		seen[key] = true
		// 把创建者写进参与者名单，否则「谁在这条工作项上」在 UI 里是空的。
		// 失败不阻断创建：owner_id 已经落库，鉴权仍然成立。
		if err := s.taskStore.SetParticipants(ctx, t.ID, target.WorkspaceID, []task.Participant{
			{UserID: target.OwnerID, Role: task.RoleOwner},
		}); err != nil {
			log.Printf("[meeting] seed owner participant for %s failed: %v", t.ID, err)
		}
		created++
		s.wsHub.Broadcast("task.created", t)
	}
	if created > 0 {
		log.Printf("[meeting] created %d tasks from meeting %s (user=%s ws=%s)",
			created, target.MeetingID, target.OwnerID, target.WorkspaceID)
	}
	return created
}

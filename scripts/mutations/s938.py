#!/usr/bin/env python3
"""§938：服务端「精校 → 待办落库」这条链的变异批次。

⚠ 为什么本批次**必须落盘**（§935 的教训）：§935 发现「当时的变异脚本没落盘」
   ⇒ 「当时绿 = 现在绿」**永久不可回溯** ⇒ ⇒ 本仓从此规定：**变异脚本要落盘**。

⚠ 本批次**需要真 PG**：判据量的全是**落库之后的行**（租户 / 归属 / 溯源 /
   查重结果）⇒ ⇒ 没有 PG 时用例会 skip，而 skip 的退出码是 0 ⇒ ⇒ 本批次在缺 PG
   时**拒绝跑**（不是「跑了个寂寞」）。见 `_harness.require_pg_dsn` 与
   `go_pkg_runner(forbid_skip=True)`。

判据命令：
  go test ./internal/server/ -run 'TestS938' -count=1
"""
import os
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    Batch, HarnessError, go_pkg_runner, require_pg_dsn, run,
)

TARGETS = [
    'backend/internal/server/server_meeting_todo_ingest.go',
    'backend/internal/server/task_from_source_handler.go',
    'backend/internal/task/store.go',
]

# ⛔ 只注入**测试专用** DSN（独立 schema + 自动 DROP）⇒ 绝不碰生产库。
# ⚠ DSN **懒加载**：缺它时本批次「拒绝跑」（退出码 3），而不是让用例被 skip
#   ⇒ skip 的退出码是 0 ⇒ 「跑了个寂寞」与「全过」完全同形（§935.4.2）。
_RUNNERS = {}


def runners():
    if not _RUNNERS:
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        _RUNNERS['server'] = go_pkg_runner(
            './internal/server/', 'TestS938', env=env, forbid_skip=True)
    return _RUNNERS


b = Batch('§938 服务端「精校 → 待办落库」的租户 / 归属 / 状态 / 去重 / 溯源',
          lambda: runners()['server'](), TARGETS)
TODO_GO = b.targets[0]
FROM_SOURCE_GO = b.targets[1]
TASK_STORE = b.targets[2]

# ── 变异的锚点（必须与实现**逐字**一致；不一致 ⇒ 落盘自证会失败并中止）──

# M1：批内去重退化成**不去重**（= §938 修复前的行为）。
#
# ⚠ 这是「同一处逻辑退化」的变异：判据里有纯函数级与落库级两条 ⇒ 两条一起红
#   ⇒ ⇒ 证明落库那条**真的在看库里几条**，而不是在看「调用了哪个函数」。
DEDUPE_OLD = (
    'func dedupeTodoItems(items []todoItem) []todoItem {\n'
    '\tseen := make(map[string]bool, len(items))\n'
    '\tout := make([]todoItem, 0, len(items))\n'
    '\tfor _, it := range items {\n'
    '\t\tkey := meetingTodoDedupeKey(it.Text)\n'
    '\t\tif key == "" || seen[key] {\n'
    '\t\t\tcontinue\n'
    '\t\t}\n'
    '\t\tseen[key] = true\n'
    '\t\tout = append(out, it)\n'
    '\t}\n'
    '\treturn out\n'
    '}'
)
DEDUPE_WHOLE = (
    'func dedupeTodoItems(items []todoItem) []todoItem {\n'
    '\treturn items\n'
    '}'
)

# M2：跨次查重退化成**不查**（查重入口还在调，但结果被丢掉）。
#
# ⚠ 刻意保留 `s.meetingTodoIndex(...)` 的调用 ⇒ ⇒ from-source 那条路径
#   **不受影响** ⇒ ⇒ 本条只红「跨次幂等」那一条判据 ⇒ 单点变异。
CROSS_OLD = (
    '\tseen, err := s.meetingTodoIndex(ctx, target)\n'
    '\tif err != nil {\n'
    '\t\t// 查重失败 ⇒ 记一笔，但**继续写**：把「查重挂了」变成「可能重复」，\n'
    '\t\t// 好过把「用户的行动项全丢了」变成「一条不建」。\n'
    '\t\tlog.Printf("[meeting] load existing meeting todos %s (ws=%s): %v",\n'
    '\t\t\ttarget.MeetingID, target.WorkspaceID, err)\n'
    '\t}\n'
)
CROSS_NONE = (
    '\tseen := map[string]bool{}\n'
    '\tif _, err := s.meetingTodoIndex(ctx, target); err != nil {\n'
    '\t\tlog.Printf("[meeting] load existing meeting todos %s (ws=%s): %v",\n'
    '\t\t\ttarget.MeetingID, target.WorkspaceID, err)\n'
    '\t}\n'
)

# M3：不写溯源列 ⇒ 跨次去重与两条路径互见同时失效。
ORIGIN_OLD = (
    '\t\t\tOriginKind: task.OriginMeeting,\n'
    '\t\t\tOriginRef:  target.MeetingID,\n'
)
ORIGIN_NONE = (
    '\t\t\tOriginKind: "",\n'
    '\t\t\tOriginRef:  "",\n'
)

# M4：查重键**丢掉会议维度**（整租户按标题去重）⇒ 跨会议误去重。
#
# ⚠ 刻意只改这一处 ⇒ 跨次幂等与 D6 那两条判据**仍然绿** ⇒ 单点变异。
INDEX_OLD = (
    '\trows, err := s.taskStore.ListTasksByOrigin(ctx, target.WorkspaceID, task.OriginMeeting, target.MeetingID)\n'
)
INDEX_WHOLE = (
    '\trows, err := s.taskStore.ListTasksScoped(ctx, target.WorkspaceID)\n'
)

# M5：状态退回 "pending"（不在前端任何分桶里 ⇒ 列表不显示）。
STATUS_OLD = '\t\t\tStatus:     "active",\n'
STATUS_PENDING = '\t\t\tStatus:     "pending",\n'

# M6：租户退回遗留单租户桶 "default" ⇒ 用户在自己的租户里看不见。
TENANT_OLD = '\t\t\tWorkspaceID: target.WorkspaceID,\n'
TENANT_DEFAULT = '\t\t\tWorkspaceID: task.DefaultWorkspaceID,\n'

# M7：不写 owner ⇒ 协作鉴权对创建者本人返回 false。
OWNER_OLD = '\t\t\tOwnerID:     target.OwnerID,\n'
OWNER_NONE = '\t\t\tOwnerID:     "",\n'

# M8：不种参与者 ⇒ 「谁在这条工作项上」是空的。
PARTICIPANT_OLD = (
    '\t\t// 把创建者写进参与者名单，否则「谁在这条工作项上」在 UI 里是空的。\n'
    '\t\t// 失败不阻断创建：owner_id 已经落库，鉴权仍然成立。\n'
    '\t\tif err := s.taskStore.SetParticipants(ctx, t.ID, target.WorkspaceID, []task.Participant{\n'
    '\t\t\t{UserID: target.OwnerID, Role: task.RoleOwner},\n'
    '\t\t}); err != nil {\n'
    '\t\t\tlog.Printf("[meeting] seed owner participant for %s failed: %v", t.ID, err)\n'
    '\t\t}\n'
)
PARTICIPANT_NONE = '\t\t// M8：不种参与者。\n'

# M9：from-source 链用的是**另一把**查重钥匙（会议 id 不一致）
# ⇒ ⇒ 两条路径互相看不见 ⇒ 用户手动转一次就再得一整套。
FROM_OLD = (
    '\tseen, err := s.meetingTodoIndex(r.Context(), meetingTodoTarget{\n'
    '\t\tWorkspaceID: wsID,\n'
    '\t\tMeetingID:   meetingID,\n'
    '\t})\n'
)
FROM_OTHER = (
    '\tseen, err := s.meetingTodoIndex(r.Context(), meetingTodoTarget{\n'
    '\t\tWorkspaceID: wsID,\n'
    '\t\tMeetingID:   meetingID + "-other",\n'
    '\t})\n'
)

# ── 负控的锚点（语义等价 ⇒ 必须保持绿）────────────────────────────────

# N1：空判断换成等价的写法（`key != ""` ⇔ `len(key) > 0`）。
INDEX_EMPTY_OLD = '\t\tif key := meetingTodoDedupeKey(row.Title); key != "" {\n'
INDEX_EMPTY_NEW = '\t\tif key := meetingTodoDedupeKey(row.Title); len(key) > 0 {\n'

# N2：批内去重把 `||` 拆成两条 if（语义等价）。
DEDUPE_SPLIT_OLD = (
    '\t\tkey := meetingTodoDedupeKey(it.Text)\n'
    '\t\tif key == "" || seen[key] {\n'
    '\t\t\tcontinue\n'
    '\t\t}\n'
)
DEDUPE_SPLIT_NEW = (
    '\t\tkey := meetingTodoDedupeKey(it.Text)\n'
    '\t\tif key == "" {\n'
    '\t\t\tcontinue\n'
    '\t\t}\n'
    '\t\tif seen[key] {\n'
    '\t\t\tcontinue\n'
    '\t\t}\n'
)

# N3：查重 SQL **只改排版**（`$1` 两侧的空格）。
SQL_OLD = (
    '\t\t`SELECT `+taskColumns+` FROM tasks\n'
    '\t\t WHERE workspace_id = $1 AND origin_kind = $2 AND origin_ref = $3\n'
    '\t\t ORDER BY updated_at DESC`,\n'
)
SQL_FMT = (
    '\t\t`SELECT `+taskColumns+` FROM tasks\n'
    '\t\t WHERE workspace_id=$1 AND origin_kind=$2 AND origin_ref=$3\n'
    '\t\t ORDER BY updated_at DESC`,\n'
)


def body():
    r = runners()
    rc, out = r['server']()
    b.record('基线：未变异时判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))

    # ── M1 必须红：批内去重退化成不去重 ──────────────────────────────
    def m1():
        b.mutate(TODO_GO, DEDUPE_OLD, DEDUPE_WHOLE, 'M1')
    b.case('M1 批内去重退化成不去重 ⇒ 纯函数级 + 落库级判据必须红', True,
           must_contain=['批内去重没修掉'],
           mutate_fn=m1)

    # ── M2 必须红：跨次查重退化成不查 ────────────────────────────────
    def m2():
        b.mutate(TODO_GO, CROSS_OLD, CROSS_NONE, 'M2')
    b.case('M2 跨次查重退化成不查 ⇒ 跨次幂等判据必须红', True,
           must_contain=['跨次幂等没修掉'],
           mutate_fn=m2)

    # ── M3 必须红：不写溯源列 ────────────────────────────────────────
    def m3():
        b.mutate(TODO_GO, ORIGIN_OLD, ORIGIN_NONE, 'M3')
    b.case('M3 不写溯源列 ⇒ 溯源判据 + 两条路径互见判据必须红', True,
           must_contain=['D6：没有溯源', 'D6：它看不见精校链建的'],
           mutate_fn=m3)

    # ── M4 必须红：查重键丢掉会议维度 ────────────────────────────────
    def m4():
        b.mutate(TODO_GO, INDEX_OLD, INDEX_WHOLE, 'M4')
    b.case('M4 查重键丢掉会议维度 ⇒ 跨会议误去重判据必须红', True,
           must_contain=['跨会议被误去重'],
           mutate_fn=m4)

    # ── M5 必须红：状态退回 "pending" ────────────────────────────────
    def m5():
        b.mutate(TODO_GO, STATUS_OLD, STATUS_PENDING, 'M5')
    b.case('M5 状态退回 "pending"（不在任何分桶里）⇒ 状态判据必须红', True,
           must_contain=["D2：'pending' 不在任何分桶里"],
           mutate_fn=m5)

    # ── M6 必须红：租户退回 default ──────────────────────────────────
    def m6():
        b.mutate(TODO_GO, TENANT_OLD, TENANT_DEFAULT, 'M6')
    b.case('M6 租户退回遗留单租户桶 default ⇒ 租户落点判据必须红', True,
           must_contain=['D1：落错桶'],
           mutate_fn=m6)

    # ── M7 必须红：不写 owner ────────────────────────────────────────
    def m7():
        b.mutate(TODO_GO, OWNER_OLD, OWNER_NONE, 'M7')
    b.case('M7 不写 owner ⇒ 归属判据必须红', True,
           must_contain=['D3：没有归属'],
           mutate_fn=m7)

    # ── M8 必须红：不种参与者 ────────────────────────────────────────
    def m8():
        b.mutate(TODO_GO, PARTICIPANT_OLD, PARTICIPANT_NONE, 'M8')
    b.case('M8 不种参与者 ⇒ 参与者判据必须红', True,
           must_contain=['want 含创建者'],
           mutate_fn=m8)

    # ── M9 必须红：两条路径用不同的查重钥匙 ──────────────────────────
    def m9():
        b.mutate(FROM_SOURCE_GO, FROM_OLD, FROM_OTHER, 'M9')
    b.case('M9 from-source 用另一把查重钥匙 ⇒ 两条路径互见判据必须红', True,
           must_contain=['D6：它看不见精校链建的'],
           mutate_fn=m9)

    # ── N1 负控：空判断换等价写法 ⇒ 必须保持绿 ───────────────────────
    def n1():
        b.mutate(TODO_GO, INDEX_EMPTY_OLD, INDEX_EMPTY_NEW, 'N1')
    b.case('N1（负控）空判断 `key != ""` 换 `len(key) > 0` ⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n1)

    # ── N2 负控：批内去重把 `||` 拆开 ⇒ 必须保持绿 ───────────────────
    def n2():
        b.mutate(TODO_GO, DEDUPE_SPLIT_OLD, DEDUPE_SPLIT_NEW, 'N2')
    b.case('N2（负控）批内去重把 `||` 拆成两条 if（语义等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n2)

    # ── N3 负控：查重 SQL 只改排版 ⇒ 必须保持绿 ──────────────────────
    def n3():
        b.mutate(TASK_STORE, SQL_OLD, SQL_FMT, 'N3')
    b.case('N3（负控）查重 SQL 只改排版 ⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n3)


if __name__ == '__main__':
    try:
        run(b, body)
    except HarnessError as e:
        print('❌ %s' % e)
        sys.exit(3)

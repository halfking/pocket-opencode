#!/usr/bin/env python3
"""§936：会议列表口径（D1 total / D2 增量 / D3 形态分叉 / D5 夹取）的变异批次。

⚠ 为什么本批次**必须落盘**（§935 的教训）：§935 发现「当时的变异脚本没落盘」
   ⇒ 「当时绿 = 现在绿」**永久不可回溯** ⇒ ⇒ 本仓从此规定：**变异脚本要落盘**。

⚠ 本批次**需要真 PG**：D2（先 LIMIT 后过滤）只发生在 PG 版 ⇒
   ⇒ 内存版的判据看不见它 ⇒ ⇒ 没有 PG 时这些用例会 **skip**，
   而 skip 的退出码是 0 ⇒ ⇒ 本批次在缺 PG 时**拒绝跑**（不是「跑了个寂寞」）。
   ⇒ 见 `_harness.require_pg_dsn` 与 `go_pkg_runner(forbid_skip=True)`。

判据命令：
  go test ./internal/meeting/ -run 'TestNormalizeListOptions|TestApplyListOptions|TestPGListSQLShape|TestParseList|TestListScoped' -count=1
  go test ./internal/server/  -run 'TestS936' -count=1
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
    'backend/internal/meeting/pg_store.go',
    'backend/internal/meeting/list.go',
    'backend/internal/server/server_meeting_ingest.go',
]

# ⛔ 只注入**测试专用** DSN（独立 schema + 自动 DROP）⇒ 绝不碰生产库。
# ⚠ DSN **懒加载**：缺它时本批次「拒绝跑」（退出码 3），而不是让用例被 skip
#   ⇒ skip 的退出码是 0 ⇒ 「跑了个寂寞」与「全过」完全同形（§935.4.2）。
_RUNNERS = {}


def runners():
    if not _RUNNERS:
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        _RUNNERS['meeting_all'] = go_pkg_runner(
            './internal/meeting/',
            'TestNormalizeListOptions|TestApplyListOptions|TestPGListSQLShape|TestParseList|TestListScoped',
            env=env, forbid_skip=True)
        _RUNNERS['meeting_parity'] = go_pkg_runner(
            './internal/meeting/', 'TestListScoped_StoresAgreeExactly',
            env=env, forbid_skip=True)
        _RUNNERS['meeting_501'] = go_pkg_runner(
            './internal/meeting/', 'TestListScoped_UpdatedOldMeetingSurvivesPaging',
            env=env, forbid_skip=True)
        _RUNNERS['meeting_scope'] = go_pkg_runner(
            './internal/meeting/', 'TestListScoped_TotalIsWorkspaceScoped',
            env=env, forbid_skip=True)
        _RUNNERS['server_all'] = go_pkg_runner('./internal/server/', 'TestS936')
    return _RUNNERS


def combined():
    """两个包的判据都跑 ⇒ 任一红就算红（收尾时两者都必须绿）。"""
    r = runners()
    m_rc, m_out = r['meeting_all']()
    s_rc, s_out = r['server_all']()
    return (m_rc or s_rc), m_out + '\n\n' + s_out


b = Batch('§936 会议列表口径（D1/D2/D3/D5）', combined, TARGETS)
PG_STORE = b.targets[0]
LIST_GO = b.targets[1]
SERVER_GO = b.targets[2]

# ── 变异的锚点（必须与实现**逐字**一致；不一致 ⇒ 落盘自证会失败并中止）──

# M1：把「先过滤后 LIMIT」改回**旧实现的形状**（LIMIT 作用在未过滤的集合上）。
#
# ⚠ 子查询里必须选**原始列名**（不能直接复用 meetingSelectCols）——
#   meetingSelectCols 里的 COALESCE(...) 是**表达式**，子查询的输出列名会变成
#   `coalesce` ⇒ 外层再引用 `key_decisions` 就会报「字段不存在」(SQLSTATE 42703)
#   ⇒ ⇒ 那样红是**SQL 语法错**，不是「语义错」⇒ ⇒ 变异就白做了
#   （「变异转红了」≠「变异验到了你想验的那条」，§935 的教训）。
SQL_OLD = (
    'const listScopedQuery = `SELECT ` + meetingSelectCols + `\n'
    '\tFROM meetings\n'
    '\tWHERE owner_id=$1 AND workspace_id=$2\n'
    '\t  AND ($3::timestamptz IS NULL OR updated_at > $3)\n'
    '\tORDER BY created_at DESC, id DESC\n'
    '\tLIMIT $4 OFFSET $5`'
)
SQL_NEW = (
    'const listScopedQuery = `SELECT ` + meetingSelectCols + `\n'
    '\tFROM (SELECT id, owner_id, workspace_id, title, duration,\n'
    '\t\trecording_url, transcript, summary, key_decisions, action_items, tags,\n'
    '\t\tproject_id, status, location, participants, note_id, created_at, updated_at\n'
    '\t\tFROM meetings\n'
    '\t\tWHERE owner_id=$1 AND workspace_id=$2\n'
    '\t\tORDER BY created_at DESC\n'
    '\t\tLIMIT $4 OFFSET $5) AS page\n'
    '\tWHERE ($3::timestamptz IS NULL OR page.updated_at > $3)\n'
    '\tORDER BY page.created_at DESC, page.id DESC`'
)

# M2：handler 把 store 给的 total 换成 len(list)（D1 的旧行为）。
#
# ⚠ 两处锚点必须**一起**改：只改 `"total": total,` ⇒ `total` 变成「声明未使用」
#   ⇒ **编译失败** ⇒ 那样红是「编不过」，不是「断言红」⇒ ⇒ 同样验不到目标。
#   ⇒ 所以同时把声明改成 `list, _, err :=`（丢掉 store 给的 total）。
HANDLER_OLD_DECL = '\tlist, total, err := s.meetingStore.ListScoped(uid, workspaceID, opts)'
HANDLER_NEW_DECL = '\tlist, _, err := s.meetingStore.ListScoped(uid, workspaceID, opts)'
HANDLER_OLD_FIELD = '\t\t"total":        total,'
HANDLER_NEW_FIELD = '\t\t"total":        len(list),'

# M6：计数 SQL 漏掉 workspace 作用域过滤。
#
# ⚠ `$2::text = $2::text` 是「恒真」：它**引用**了 $2（免得 PG 报
#   「could not determine data type of parameter $2」）**但不筛 workspace**
#   ⇒ ⇒ 才是「漏掉作用域过滤」这个语义，而不是「SQL 写坏了」。
COUNT_OLD = (
    'const listScopedCountQuery = `SELECT COUNT(*)\n'
    '\tFROM meetings\n'
    '\tWHERE owner_id=$1 AND workspace_id=$2\n'
)
COUNT_NEW = (
    'const listScopedCountQuery = `SELECT COUNT(*)\n'
    '\tFROM meetings\n'
    '\tWHERE owner_id=$1 AND $2::text = $2::text\n'
)

# M7（负控）：只改**排版**，不改语义。
FMT_OLD = 'updated_at > $3)'
FMT_NEW = 'updated_at>$3)'


def body():
    r = runners()
    rc, out = combined()
    b.record('基线：未变异时两个包的判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))

    # ── M1 必须红：PG 退回「先 LIMIT 后过滤」（旧实现的形状）──────────
    def m1():
        b.mutate(PG_STORE, SQL_OLD, SQL_NEW, 'M1')
    b.case('M1 PG 退回「先 LIMIT 后过滤」⇒ 501 行 D2 判据必须红 + SQL 形状守卫必须红', True,
           must_contain=['D2 仍在', '排在 LIMIT'],
           mutate_fn=m1)

    # ── M1b 负控：8 行数据集上的跨 store 一致性判据**看不见** D2 ──────
    # ⚠ 这条是**如实登记量具盲区**：一致性判据用 8 行 ⇒ LIMIT 500 不生效
    #   ⇒ 旧实现照样给出同样的 4 行 ⇒ 它**绿** ⇒ ⇒ D2 是靠 501 行那条
    #   与 SQL 形状守卫抓住的 ⇒ ⇒ 「一致性判据覆盖了 D2」这句话是**错的**。
    b.case('M1b（负控/盲区登记）在 M1 变异下 8 行一致性判据仍绿 ⇒ 它看不见 D2', False,
           must_not_contain=['D2 仍在', '排在 LIMIT'],
           mutate_fn=m1, runner=r['meeting_parity'])

    # ── M2 必须红：handler 把 total 换回 len(list)（D1 的旧行为）─────
    def m2():
        b.mutate(SERVER_GO, HANDLER_OLD_DECL, HANDLER_NEW_DECL, 'M2-decl')
        b.mutate(SERVER_GO, HANDLER_OLD_FIELD, HANDLER_NEW_FIELD, 'M2-field')
    b.case('M2 handler 把 total 换回 len(list) ⇒ handler 级 D1 判据必须红', True,
           must_contain=['D1 仍在'], mutate_fn=m2, runner=r['server_all'])

    # ── M3 必须红：total 不计 since 过滤（增量语义错）────────────────
    def m3():
        b.mutate(LIST_GO, '\ttotal := len(matched)', '\ttotal := len(all)', 'M3')
    b.case('M3 total 不计 since 过滤 ⇒ 增量 total 判据必须红', True,
           must_contain=['只有那场刚更新的'], mutate_fn=m3, runner=r['meeting_all'])

    # ── M4 必须红：删掉 limit 的上限夹取（D5）────────────────────────
    def m4():
        b.mutate(LIST_GO,
                 '\tif opts.Limit > ListMaxLimit {\n\t\topts.Limit = ListMaxLimit\n\t}\n',
                 '', 'M4')
    b.case('M4 删掉 limit 的上限夹取 ⇒ 夹取判据必须红', True,
           must_contain=['limit 夹取后'], mutate_fn=m4, runner=r['meeting_all'])

    # ── M5 必须红：排序方向反过来（D3 的形状）───────────────────────
    def m5():
        b.mutate(LIST_GO, 'return a.CreatedAt.After(b.CreatedAt)',
                 'return b.CreatedAt.After(a.CreatedAt)', 'M5')
    b.case('M5 排序方向反过来 ⇒ 顺序判据必须红', True,
           must_contain=['排序不是 created_at DESC'], mutate_fn=m5, runner=r['meeting_all'])

    # ── M6 必须红：计数 SQL 漏掉 workspace 作用域过滤 ───────────────
    def m6():
        b.mutate(PG_STORE, COUNT_OLD, COUNT_NEW, 'M6')
    b.case('M6 计数 SQL 漏掉 workspace 过滤 ⇒ 作用域 total 判据必须红', True,
           must_contain=['缺 workspace 作用域过滤', 'COUNT 漏了作用域过滤'],
           mutate_fn=m6, runner=r['meeting_all'])

    # ── M7 负控：只改排版（语义等价）⇒ 必须保持绿 ───────────────────
    def m7():
        b.mutate(PG_STORE, FMT_OLD, FMT_NEW, 'M7', count=2)
    b.case('M7（负控）SQL 只改排版 ⇒ 必须保持绿', False,
           must_not_contain=['FAIL'], mutate_fn=m7, runner=r['meeting_all'])

    # ── M8 负控：稳定排序换成不稳定排序（语义等价）⇒ 必须保持绿 ─────
    def m8():
        b.mutate(LIST_GO, 'sort.SliceStable(list, func(i, j int) bool {',
                 'sort.Slice(list, func(i, j int) bool {', 'M8')
    b.case('M8（负控）SliceStable 换成 Slice（比较器是全序 ⇒ 语义等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'], mutate_fn=m8, runner=r['meeting_all'])


if __name__ == '__main__':
    try:
        run(b, body)
    except HarnessError as e:
        print('❌ %s' % e)
        sys.exit(3)

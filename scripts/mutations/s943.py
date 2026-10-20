#!/usr/bin/env python3
"""§943：「会议本体的**写入链**」（POST /api/meetings）的变异批次。

⚠ 为什么本批次**必须落盘**（§935 的教训）：§935 发现「当时的变异脚本没落盘」
   ⇒ 「当时绿 = 现在绿」**永久不可回溯** ⇒ ⇒ 本仓从此规定：**变异脚本要落盘**。

⚠ 本批次**需要真 PG**：判据量的全是**落库之后的行**（受害者的行有没有被改、
   墓碑有没有被搬走）⇒ ⇒ 没有 PG 时用例会 skip，而 skip 的退出码是 0
   ⇒ ⇒ 本批次在缺 PG 时**拒绝跑**（退出码 3），而不是「跑了个寂寞」。
   见 `_harness.require_pg_dsn` 与 `go_pkg_runner(forbid_skip=True)`。

⚠⚠ 本批次有**三个判据运行器**，而且**必须一起跑**：
   ① `TestS943`（meeting 包）—— ★★ **两个实现**（内存版 + PG 版）的撞号行为
      必须**逐字一致** + 墓碑守卫；
   ② `TestS943`（server 包）—— 端到端（HTTP 响应）+ `meeting.synced` 广播作用域；
   ③ `TestS943`（repohygiene 包）—— 全仓「裸 id + ON CONFLICT DO UPDATE」候选集棘轮。
   ⇒ ⇒⇒ ★ ③ 是**普查棘轮**：它会在「某处把归属守卫删掉」时红
   （台账里声明为「SQL 谓词守卫」的那几条必须真的有 WHERE）。

⚠⚠ **本节最贵的发现**（也是本批次存在的理由）：
   §943 之前唯一钉「跨作用域撞号」那条语义的承重门
   （server 包 TestClientIDCollisionAcrossWorkspacesNeverOverwrites）
   **只跑在内存版上** ⇒ ⇒⇒ 而生产跑的是 PG 版 ⇒ ⇒⇒⇒ **两个实现分叉时门禁是绿的**。
   ⇒ ⇒⇒ ⇒ 本批次的 ① 就是「**一条判据同时钉两个实现**」。

判据命令：
  go test ./internal/meeting/    -run 'TestS943' -count=1
  go test ./internal/server/    -run 'TestS943' -count=1
  go test ./internal/repohygiene/ -run 'TestS943' -count=1
"""
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    Batch, HarnessError, go_pkg_runner, require_pg_dsn, run,
)

TARGETS = [
    'backend/internal/meeting/pg_store.go',
    'backend/internal/meeting/store.go',
    'backend/internal/server/server_meeting_ingest.go',
    # ⚠ notes/store.go 也在 targets 里 ⇒ 因为 M5 要动它的归属守卫
    #   ⇒ ⇒ ⇒ 还原时它才不会被漏掉。
    'backend/internal/notes/store.go',
]

# ⛔ 只注入**测试专用** DSN（独立 schema + 自动 DROP）⇒ 绝不碰生产库。
# ⚠ DSN **懒加载**：缺它时本批次「拒绝跑」（退出码 3），而不是让用例被 skip
#   ⇒ skip 的退出码是 0 ⇒ 「跑了个寂寞」与「全过」完全同形（§935.4.2）。
_RUNNERS = {}


def runners():
    if not _RUNNERS:
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        _RUNNERS['meeting'] = go_pkg_runner(
            './internal/meeting/', 'TestS943', env=env, forbid_skip=True)
        _RUNNERS['server'] = go_pkg_runner(
            './internal/server/', 'TestS943', env=env, forbid_skip=True)
        _RUNNERS['repohygiene'] = go_pkg_runner(
            './internal/repohygiene/', 'TestS943')
    return _RUNNERS


def combined():
    """三个包的判据都跑 ⇒ 任一红就算红（收尾时三者都必须绿）。"""
    r = runners()
    m_rc, m_out = r['meeting']()
    s_rc, s_out = r['server']()
    g_rc, g_out = r['repohygiene']()
    return ((m_rc or s_rc or g_rc),
            m_out + '\n\n' + s_out + '\n\n' + g_out)


b = Batch('§943 会议本体的写入链：撞 id **不得覆盖**别人的会议 / 墓碑**不得被搬走** / 广播**不得**越权',
          combined, TARGETS)
PG_STORE = b.targets[0]
STORE_GO = b.targets[1]
INGEST_GO = b.targets[2]
NOTES_GO = b.targets[3]

# ── 变异的锚点（必须与实现**逐字**一致；不一致 ⇒ 落盘自证会失败并中止）──
#
# ⚠⚠ **锚点必须唯一**：`WHERE meetings.owner_id = EXCLUDED.owner_id` 这一行
#   在 pg_store.go 里出现 **2 次**（**代码**里 1 次 + 上面的**注释**里 1 次）
#   ⇒ ⇒⇒ ⇒ 修法：**把缩进写进锚点**（代码那行是 2 个 tab，注释那行是 `//\t`）。

# M1（D1 的本体）：`meetings` 的归属守卫被去掉 ⇒ 撞 id 就会**覆盖**别人的行。
#
# ⚠ 变异是**整段删掉 WHERE 子句**（不是把谓词改错）⇒ ⇒ SQL 仍然合法
#   ⇒ ⇒⇒ 判据红在「越权覆盖」上，而不是红在「SQL 语法错」上（★ 变异必须只改语义）。
M1_OLD = (
    '\t\tWHERE meetings.owner_id = EXCLUDED.owner_id\n'
    '\t\t  AND meetings.workspace_id = EXCLUDED.workspace_id\n'
)
M1_NEW = ''

# M2（D2 的本体）：广播退回**全体广播** ⇒ 别人的会议 id 又会泄给所有人。
M2_OLD = '\ts.wsHub.BroadcastToUser(uid, "meeting.synced", map[string]string{"meetingId": m.ID, "userId": uid})\n'
M2_NEW = '\ts.wsHub.Broadcast("meeting.synced", map[string]string{"meetingId": m.ID, "userId": uid})\n'

# M3（D3 的本体，PG 版）：墓碑的归属守卫被去掉 ⇒ 别人的墓碑会被**整个搬走**
#   ⇒ ⇒⇒ 受害者自己的墓碑清单里再也没有那条 id ⇒ ⇒⇒ **已删的会议会复活**。
M3_OLD = (
    '\t\tWHERE meeting_tombstones.owner_id = EXCLUDED.owner_id\n'
    '\t\t  AND meeting_tombstones.workspace_id = EXCLUDED.workspace_id\n'
)
M3_NEW = ''

# M4（D3 的本体，内存版）：内存版当时**也**没有守卫 ⇒ 它也会搬走别人的墓碑
#   ⇒ ⇒⇒ 这条证明「**两个实现**都要修」（★ 只修 PG 版会留下一个分叉的实现）。
M4_OLD = (
    '\tif t, ok := s.tombstones[id]; ok &&\n'
    '\t\t(t.ownerID != ownerID || t.workspaceID != workspaceID) {\n'
    '\t\treturn fmt.Errorf("delete meeting: tombstone %q belongs to another scope: %w",\n'
    '\t\t\tid, ErrMeetingTombstoneScopeConflict)\n'
    '\t}\n'
)
M4_NEW = ''

# M5（**普查棘轮**有牙）：`notes` 的归属守卫被去掉
#   ⇒ ⇒⇒ 普查台账里声明它「带守卫」⇒ ⇒⇒ 棘轮必须红
#   ⇒ ⇒⇒⇒ （★ 这条证明「登记的守卫状态」不是一句注释，而是**被卡住的**。）
M5_OLD = '\t\tWHERE notes.user_id = $2\n'
M5_NEW = ''

# ── 负控的锚点（语义等价 ⇒ 必须保持绿）────────────────────────────────

# N1：守卫的谓词写成**位置参数**（等价：EXCLUDED.owner_id 就是 $2）⇒ 必须保持绿。
N1_OLD = (
    '\t\tWHERE meetings.owner_id = EXCLUDED.owner_id\n'
    '\t\t  AND meetings.workspace_id = EXCLUDED.workspace_id\n'
)
N1_NEW = (
    '\t\tWHERE meetings.owner_id = $2\n'
    '\t\t  AND meetings.workspace_id = $3\n'
)

# N2：广播载荷里的 `userId` 改用**行里的 owner**（等价：那一行就是 uid 写的）⇒ 必须保持绿。
N2_OLD = '\ts.wsHub.BroadcastToUser(uid, "meeting.synced", map[string]string{"meetingId": m.ID, "userId": uid})\n'
N2_NEW = '\ts.wsHub.BroadcastToUser(uid, "meeting.synced", map[string]string{"meetingId": m.ID, "userId": m.OwnerID})\n'

# N3：墓碑守卫的两个条件**换序**（等价）⇒ 必须保持绿。
N3_OLD = (
    '\t\tWHERE meeting_tombstones.owner_id = EXCLUDED.owner_id\n'
    '\t\t  AND meeting_tombstones.workspace_id = EXCLUDED.workspace_id\n'
)
N3_NEW = (
    '\t\tWHERE meeting_tombstones.workspace_id = EXCLUDED.workspace_id\n'
    '\t\t  AND meeting_tombstones.owner_id = EXCLUDED.owner_id\n'
)

# N4：内存版守卫的两个条件**换序**（等价）⇒ 必须保持绿。
N4_OLD = '\t\t(t.ownerID != ownerID || t.workspaceID != workspaceID) {\n'
N4_NEW = '\t\t(t.workspaceID != workspaceID || t.ownerID != ownerID) {\n'


def body():
    rc, out = combined()
    b.record('基线：未变异时判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))

    # ── M1 必须红：归属守卫被去掉 ⇒ 撞 id 覆盖别人的会议 ───────────────
    def m1():
        b.mutate(PG_STORE, M1_OLD, M1_NEW, 'M1')
    b.case('M1 `meetings` 的归属守卫被去掉 ⇒ 撞 id 覆盖别人的会议 ⇒ 「不得覆盖」判据必须红', True,
           must_contain=['越权覆盖'],
           mutate_fn=m1)

    # ── M2 必须红：广播退回全体广播 ⇒ 别人的会议 id 又泄给所有人 ───────
    def m2():
        b.mutate(INGEST_GO, M2_OLD, M2_NEW, 'M2')
    b.case('M2 `meeting.synced` 退回全体广播 ⇒ 别人的会议 id 泄给所有人 ⇒ 「只发给属主」判据必须红', True,
           must_contain=['另一个用户收到了别人的 meeting.synced'],
           mutate_fn=m2)

    # ── M3 必须红：PG 版的墓碑守卫被去掉 ⇒ 别人的墓碑被搬走 ───────────
    def m3():
        b.mutate(PG_STORE, M3_OLD, M3_NEW, 'M3')
    b.case('M3 PG 版的墓碑守卫被去掉 ⇒ 别人的墓碑被搬走（已删会议会复活）⇒ 判据必须红', True,
           must_contain=['把别人的墓碑搬走'],
           mutate_fn=m3)

    # ── M4 必须红：内存版的墓碑守卫被去掉 ⇒ **两个实现分叉** ───────────
    def m4():
        b.mutate(STORE_GO, M4_OLD, M4_NEW, 'M4')
    b.case('M4 内存版的墓碑守卫被去掉 ⇒ 两个实现分叉 ⇒ 判据必须红（证明两个实现都要修）', True,
           must_contain=['把别人的墓碑搬走'],
           mutate_fn=m4)

    # ── M5 必须红：notes 的守卫被去掉 ⇒ 普查棘轮必须红 ────────────────
    def m5():
        b.mutate(NOTES_GO, M5_OLD, M5_NEW, 'M5')
    b.case('M5 `notes` 的归属守卫被去掉 ⇒ 普查台账与实况不符 ⇒ 棘轮必须红', True,
           must_contain=['台账里声明为「SQL 谓词守卫」'],
           mutate_fn=m5, runner=lambda: runners()['repohygiene']())

    # ── N1 负控：守卫写成位置参数（等价）⇒ 必须保持绿 ─────────────────
    def n1():
        b.mutate(PG_STORE, N1_OLD, N1_NEW, 'N1')
    b.case('N1（负控）守卫的谓词写成位置参数（等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n1)

    # ── N2 负控：广播载荷的 userId 换写法（等价）⇒ 必须保持绿 ──────────
    def n2():
        b.mutate(INGEST_GO, N2_OLD, N2_NEW, 'N2')
    b.case('N2（负控）广播载荷的 userId 改用行里的 owner（等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n2)

    # ── N3 负控：墓碑守卫两个条件换序（等价）⇒ 必须保持绿 ─────────────
    def n3():
        b.mutate(PG_STORE, N3_OLD, N3_NEW, 'N3')
    b.case('N3（负控）墓碑守卫的两个条件换序（等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n3)

    # ── N4 负控：内存版守卫两个条件换序（等价）⇒ 必须保持绿 ───────────
    def n4():
        b.mutate(STORE_GO, N4_OLD, N4_NEW, 'N4')
    b.case('N4（负控）内存版守卫的两个条件换序（等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n4)


if __name__ == '__main__':
    try:
        run(b, body, extra_runners=[
            lambda: runners()['server'](),
            lambda: runners()['repohygiene'](),
        ])
    except HarnessError as e:
        print('❌ %s' % e)
        sys.exit(3)

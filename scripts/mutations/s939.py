#!/usr/bin/env python3
"""§939：服务端「会议 → 笔记」这条链的变异批次。

⚠ 为什么本批次**必须落盘**（§935 的教训）：§935 发现「当时的变异脚本没落盘」
   ⇒ 「当时绿 = 现在绿」**永久不可回溯** ⇒ ⇒ 本仓从此规定：**变异脚本要落盘**。

⚠ 本批次**需要真 PG**：判据量的全是**落库之后的行**（租户落点 / 幂等 /
   归属守卫 / 查回来的结果）⇒ ⇒ 没有 PG 时用例会 skip，而 skip 的退出码是 0
   ⇒ ⇒ 本批次在缺 PG 时**拒绝跑**（不是「跑了个寂寞」）。
   见 `_harness.require_pg_dsn` 与 `go_pkg_runner(forbid_skip=True)`。

⚠ 本批次有**两个判据运行器**：
   ① `TestS939*`  —— §939 自己新增的判据（真 PG + 真 WS）。
   ② `TestMeetingNoteSearchSurfaceIsTruncatedToSnippet` —— §333 那道**既有**
      门禁。§939 把 ingestMeetingNote 搬了文件 ⇒ 它的「尺子夹具」指向跟着变
      ⇒ ⇒ ⇒ 本批次必须**同时**验那道门禁仍然有牙（改指向 ≠ 削弱判据）。

判据命令：
  go test ./internal/server/ -run 'TestS939' -count=1
  go test ./internal/server/ -run 'TestMeetingNoteSearchSurfaceIsTruncatedToSnippet' -count=1
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
    'backend/internal/server/server_meeting_note_ingest.go',
    'backend/internal/server/server_meeting_ingest.go',
    'backend/internal/notes/store.go',
    # ⚠⚠ §941：落点 / 广播目标 / 归一这三件事被收进了**共用**那一份
    #   （backend/internal/server/server_note_ingest.go）
    #   ⇒ ⇒ ⇒ 本批次里对应的锚点（M1 / M2 / M4 / M5 / N1 / N3）**全部挪到了这里**
    #   ⇒ ⇒ ⇒ ⇒ ★ 这是「**锚点会随实现重构一起漂**」的第 N 次出现
    #   ⇒ ⇒ ⇒ ⇒ 撞上时的正确动作是「**重新指向**」，**不是**改判据、也不是
    #   ⇒ ⇒ ⇒ ⇒ 把 §939 的语义降级（每条变异要证的**那件事**一个字没变）。
    #   ⇒ ⇒ ⇒ ⇒ ⚠ 它**必须**在 targets 里 ⇒ ⇒ 否则 harness 还原时会漏掉它。
    'backend/internal/server/server_note_ingest.go',
]

# ⛔ 只注入**测试专用** DSN（独立 schema + 自动 DROP）⇒ 绝不碰生产库。
# ⚠ DSN **懒加载**：缺它时本批次「拒绝跑」（退出码 3），而不是让用例被 skip
#   ⇒ skip 的退出码是 0 ⇒ 「跑了个寂寞」与「全过」完全同形（§935.4.2）。
_RUNNERS = {}


def runners():
    if not _RUNNERS:
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        _RUNNERS['server'] = go_pkg_runner(
            './internal/server/', 'TestS939', env=env, forbid_skip=True)
        # §333 那道既有门禁（尺子夹具）：§939 改了它的**指向** ⇒ 必须同批验。
        _RUNNERS['s333'] = go_pkg_runner(
            './internal/server/', 'TestMeetingNoteSearchSurfaceIsTruncatedToSnippet',
            env=env, forbid_skip=True)
    return _RUNNERS


b = Batch('§939 服务端「会议 → 笔记」的租户落点 / 广播作用域 / 幂等 / 归属守卫',
          lambda: runners()['server'](), TARGETS)
NOTE_GO = b.targets[0]
INGEST_GO = b.targets[1]
STORE_GO = b.targets[2]
SHARED_GO = b.targets[3]

# ── 变异的锚点（必须与实现**逐字**一致；不一致 ⇒ 落盘自证会失败并中止）──

# M1（D1）：落点丢租户 ⇒ notes 行的 workspace_id 落空串 ⇒ 谁也查不到。
#
# ⚠ 这就是**修复前**的形态（修复前连 WorkspaceID 这个字段都没传）。
#
# ⚠⚠ §941：这一行**搬到了共用那一份**（ingestNote 统一写落点）
#   ⇒ ⇒ ⇒ 锚点跟着搬（语义不变：还是「落点丢租户」）。
WS_FIELD_OLD = '\tn.WorkspaceID = target.WorkspaceID\n'
WS_FIELD_NONE = '\tn.WorkspaceID = ""\n'

# M2（D2）：广播退回**全体广播** ⇒ 会议笔记被推进每一个在线用户的设备。
#
# ⚠ 只改**这一处** ⇒ 纯函数层（broadcastTarget）仍绿 ⇒ 单点变异。
# ⚠⚠ §941：广播那一步**搬到了共用那一份**（ingestNote）⇒ ⇒ ⇒ 锚点跟着搬
#   ⇒ ⇒ ⇒ 语义不变：还是「调用点退回全体广播」，纯函数层（broadcastTarget）仍绿。
BROADCAST_OLD = '\ts.wsHub.BroadcastTo(target.broadcastTarget(), "note.created", n)\n'
BROADCAST_ALL = '\ts.wsHub.Broadcast("note.created", n)\n'

# M3（D3）：id 退回随机 ⇒ 同一场会议精校两次 ⇒ 两条笔记（旧那条成孤儿）。
# ⚠⚠ §941：结构体字面量里**少了一行**（UserID/WorkspaceID 不再在这里写）
#   ⇒ ⇒ ⇒ gofmt 重新对齐了 ⇒ 锚点的**空格数**变了 ⇒ ⇒ ⇒ 锚点跟着更新
#   ⇒ ⇒⇒ ⇒ 语义不变：还是「id 退回随机」。
ID_FIELD_OLD = '\t\tID: target.noteID(),\n'
ID_FIELD_RAND = '\t\tID: randomID("note"),\n'

# M4：归属归一失效（空 uid 不再回落 "local"）。
#
# ⚠⚠ §941：归一**搬到了共用那一份**（newNoteTarget）⇒ ⇒ ⇒ 锚点跟着搬
#   ⇒ ⇒⇒ ⇒ 语义不变：还是「空 uid 不再回落 local」。
UID_FALLBACK_OLD = '\t\tuid = "local"\n'
UID_FALLBACK_NONE = '\t\tuid = ""\n'

# M5：广播目标**漏填 UserID** ⇒ 零值 target ⇒ hub 退化成全局广播。
#
# ⚠ 与 M2 的区别：M2 换的是**调用方式**，M5 换的是**目标值**
#   ⇒ ⇒ M5 会**额外**让纯函数层那条判据红 ⇒ 两者可区分。
# ⚠⚠ §941：广播目标**搬到了共用那一份**（noteTarget.broadcastTarget）
#   ⇒ ⇒ ⇒ 锚点跟着搬（**字面量一模一样**，只是换了文件）⇒ ⇒⇒ 语义不变。
TARGET_OLD = '\treturn ws.BroadcastTarget{UserID: t.OwnerID}\n'
TARGET_ZERO = '\treturn ws.BroadcastTarget{}\n'

# M6：归属守卫被删（ON CONFLICT 不带 WHERE 谓词）⇒ 撞别人的 id 直接改写对方正文。
WHERE_OLD = '\t\tWHERE notes.user_id = $2\n'
WHERE_NONE = ''

# M7：守卫**在**，但不把冲突报出来 ⇒ 调用方以为写成功了（用户的笔记其实没存上）。
#
# ⚠⚠ 第一版把整块**删掉** ⇒ `tag` 变成**未使用** ⇒ **编译失败** ⇒
#   判据**一条都没跑到**，而退出码仍是 1 ⇒ ⇒ 「变红」是**假的**
#   （§937 记过的「变异必须只改语义」第 **4** 次出现）⇒ ⇒ 改成
#   **`_ = tag`** ⇒ `tag` 仍被使用 ⇒ 编译过 ⇒ 只去掉「报错」这一个维度。
ROWS_OLD = (
    '\tif tag.RowsAffected() == 0 {\n'
    '\t\treturn ErrNoteOwnershipConflict\n'
    '\t}\n'
)
ROWS_NONE = '\t_ = tag\n'

# M8：ON CONFLICT 的 SET 列表里**少了 workspace_id** ⇒ 存量落错租户的行永远改不回来。
SET_WS_OLD = '\t\t\tworkspace_id  = EXCLUDED.workspace_id,\n'
SET_WS_NONE = ''

# M9：调用点把租户**丢掉** ⇒ 落进遗留单租户桶 "default"。
#
# ⚠ 与 M1 的区别：M1 改的是**落库那一行**，M9 改的是**调用点的传参**
#   ⇒ ⇒ M9 只红端到端那条判据，M1 还会额外红落库层那几条 ⇒ 可区分。
CALL_OLD = '\tnoteTarget := newMeetingNoteTarget(uidForStore, workspaceIDForStore, meetingID)\n'
CALL_NONE = '\tnoteTarget := newMeetingNoteTarget(uidForStore, "", meetingID)\n'

# M10：把「尺子」那一行从新文件里删掉 ⇒ §333 那道门禁必须红。
#
# ⚠ 这是本节对**既有门禁**改指向的**配套负控**：尺子夹具读的是**活的生产行**
#   ⇒ 生产行一消失，夹具必须当场 Fatal（而不是静默退回一个写死的 500）。
RULER_OLD = '\t\tSnippet:        truncateStr(content, 500),\n'
RULER_NONE = '\t\tSnippet:        content,\n'

# M11：把「尺子」**抄成两份** ⇒ §333 那道门禁必须红（「恰好一个真源」）。
#
# ⚠ 抄到 server_meeting_ingest.go 里（它也在候选表里）⇒ 两份会各自漂移
#   ⇒ ⇒ ⇒ 门禁必须当场 Fatal，而不是随便挑一份量。
DUP_OLD = CALL_OLD
DUP_NEW = (
    '\tcontent := refined\n'
    '\t_ = struct{ Snippet string }{Snippet: truncateStr(content, 500)}\n'
    '\tnoteTarget := newMeetingNoteTarget(uidForStore, workspaceIDForStore, meetingID)\n'
)

# ── 负控的锚点（语义等价 ⇒ 必须保持绿）────────────────────────────────

# N1：只改**排版**（语义完全相同）。
#
# ⚠⚠ §941：落点那一行**搬到了共用那一份**，而且它是**赋值**（不是结构体字段）
#   ⇒ ⇒ ⇒ 没有「对齐」可改 ⇒ ⇒ ⇒ 改成「`=` 前面多一个空格」
#   ⇒ ⇒⇒ ⇒ 语义不变（Go 不在乎），判据必须仍然绿。
N1_OLD = '\tn.WorkspaceID = target.WorkspaceID\n'
N1_NEW = '\tn.WorkspaceID  = target.WorkspaceID\n'

# N2：SQL **只改排版**（`$2` 两侧的空格）。
N2_OLD = '\t\tWHERE notes.user_id = $2\n'
N2_NEW = '\t\tWHERE notes.user_id=$2\n'

# N3：广播目标**显式写空** WorkspaceID（零值本来就是空 ⇒ 语义等价）。
#
# ⚠⚠ §941：同上 ⇒ 广播目标搬到了共用那一份 ⇒ ⇒ ⇒ 锚点跟着搬。
N3_OLD = '\treturn ws.BroadcastTarget{UserID: t.OwnerID}\n'
N3_NEW = '\treturn ws.BroadcastTarget{UserID: t.OwnerID, WorkspaceID: ""}\n'


def body():
    r = runners()
    rc, out = r['server']()
    b.record('基线：未变异时判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))

    # ── M1 必须红：落点丢租户 ────────────────────────────────────────
    def m1():
        b.mutate(SHARED_GO, WS_FIELD_OLD, WS_FIELD_NONE, 'M1')
    b.case('M1 落点丢租户（workspace_id 落空串）⇒ 租户落点判据必须红', True,
           must_contain=['D1：落错租户'],
           mutate_fn=m1)

    # ── M2 必须红：广播退回全体广播 ──────────────────────────────────
    def m2():
        b.mutate(SHARED_GO, BROADCAST_OLD, BROADCAST_ALL, 'M2')
    b.case('M2 广播退回全体广播 ⇒ 「别人收不到」判据必须红（纯函数层仍绿）', True,
           must_contain=['另一个用户收到了这条会议笔记'],
           mutate_fn=m2)

    # ── M3 必须红：id 退回随机 ───────────────────────────────────────
    def m3():
        b.mutate(NOTE_GO, ID_FIELD_OLD, ID_FIELD_RAND, 'M3')
    b.case('M3 id 退回随机 ⇒ 幂等判据必须红', True,
           must_contain=['D3：没有幂等'],
           mutate_fn=m3)

    # ── M4 必须红：归属归一失效 ──────────────────────────────────────
    def m4():
        b.mutate(SHARED_GO, UID_FALLBACK_OLD, UID_FALLBACK_NONE, 'M4')
    b.case('M4 空归属不再回落 "local" ⇒ 归一判据必须红', True,
           must_contain=['空归属归一为'],
           mutate_fn=m4)

    # ── M5 必须红：广播目标漏填 UserID（零值 ⇒ 退化成全局广播）──────
    def m5():
        b.mutate(SHARED_GO, TARGET_OLD, TARGET_ZERO, 'M5')
    b.case('M5 广播目标漏填 UserID（零值 target）⇒ 目标判据 + 广播判据必须红', True,
           must_contain=['广播目标是零值', '另一个用户收到了这条会议笔记'],
           mutate_fn=m5)

    # ── M6 必须红：归属守卫被删（对方那行也被改写）──────────────────
    def m6():
        b.mutate(STORE_GO, WHERE_OLD, WHERE_NONE, 'M6')
    b.case('M6 归属守卫（ON CONFLICT 的 WHERE 谓词）被删 ⇒ 守卫判据必须红', True,
           must_contain=['撞别人的 id 时 err=<nil> 且受害者的笔记被改了（归属守卫整体失效）',
                         '受害者的笔记被改了'],
           mutate_fn=m6)

    # ── M7 必须红：守卫在但不报冲突 ──────────────────────────────────
    def m7():
        b.mutate(STORE_GO, ROWS_OLD, ROWS_NONE, 'M7')
    b.case('M7 守卫在但不把冲突报出来 ⇒ 只红「没报错」那一条（对方那行不动）', True,
           must_contain=['撞别人的 id 时 err=<nil>（守卫在但没把冲突报出来'],
           mutate_fn=m7)

    # ── M8 必须红：SET 列表里少了 workspace_id ───────────────────────
    def m8():
        b.mutate(STORE_GO, SET_WS_OLD, SET_WS_NONE, 'M8')
    b.case('M8 ON CONFLICT 的 SET 少了 workspace_id ⇒ 存量修不回来判据必须红', True,
           must_contain=['重精校后仍然查不到这条笔记'],
           mutate_fn=m8)

    # ── M9 必须红：调用点把租户丢掉 ──────────────────────────────────
    def m9():
        b.mutate(INGEST_GO, CALL_OLD, CALL_NONE, 'M9')
    b.case('M9 调用点把租户丢掉 ⇒ 端到端落点判据必须红（落库层不受影响）', True,
           must_contain=['在调用方租户里查不到（D1：落点丢了）'],
           mutate_fn=m9)

    # ── M10 必须红：尺子那一行消失 ⇒ §333 门禁必须红 ─────────────────
    def m10():
        b.mutate(NOTE_GO, RULER_OLD, RULER_NONE, 'M10')
    b.case('M10 尺子那一行从生产代码里消失 ⇒ §333 门禁必须红（夹具读的是活的生产行）', True,
           must_contain=['找不到 `Snippet: truncateStr(content, N)`'],
           mutate_fn=m10, runner=lambda: r['s333']())

    # ── M11 必须红：尺子被抄成两份 ⇒ §333 门禁必须红 ─────────────────
    def m11():
        b.mutate(INGEST_GO, DUP_OLD, DUP_NEW, 'M11')
    b.case('M11 尺子被抄成两份 ⇒ §333 门禁必须红（尺子必须恰好一个真源）', True,
           must_contain=['尺子同时出现在'],
           mutate_fn=m11, runner=lambda: r['s333']())

    # ── N1 负控：只改对齐空格 ⇒ 必须保持绿 ───────────────────────────
    def n1():
        b.mutate(SHARED_GO, N1_OLD, N1_NEW, 'N1')
    b.case('N1（负控）只改对齐空格 ⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n1)

    # ── N2 负控：SQL 只改排版 ⇒ 必须保持绿 ───────────────────────────
    def n2():
        b.mutate(STORE_GO, N2_OLD, N2_NEW, 'N2')
    b.case('N2（负控）SQL 只改排版 ⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n2)

    # ── N3 负控：显式写空 WorkspaceID ⇒ 必须保持绿 ───────────────────
    def n3():
        b.mutate(SHARED_GO, N3_OLD, N3_NEW, 'N3')
    b.case('N3（负控）广播目标显式写空 WorkspaceID（语义等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n3)


if __name__ == '__main__':
    try:
        run(b, body, extra_runners=[lambda: runners()['s333']()])
    except HarnessError as e:
        print('❌ %s' % e)
        sys.exit(3)

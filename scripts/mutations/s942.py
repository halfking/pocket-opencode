#!/usr/bin/env python3
"""§942：「笔记的**删除链**」的变异批次。

⚠ 为什么本批次**必须落盘**（§935 的教训）：§935 发现「当时的变异脚本没落盘」
   ⇒ 「当时绿 = 现在绿」**永久不可回溯** ⇒ ⇒ 本仓从此规定：**变异脚本要落盘**。

⚠ 本批次**需要真 PG**：判据量的全是**落库之后的行**（复活后的可见性 / 墓碑 /
   受害者的行还在不在）⇒ ⇒ 没有 PG 时用例会 skip，而 skip 的退出码是 0
   ⇒ ⇒ 本批次在缺 PG 时**拒绝跑**（退出码 3），而不是「跑了个寂寞」。
   见 `_harness.require_pg_dsn` 与 `go_pkg_runner(forbid_skip=True)`。

⚠⚠ 本批次有**两个判据运行器**，而且**必须一起跑**：
   ① `TestS9(39|41|42)`（server 包）—— 复活必须**看得见** + 删除的 404；
   ② `TestS940`（notes 包）—— ★ 因为本节的修法**改了 `notes/store.go`**
      ⇒ ⇒ ⇒ 而 store.go 是 §940 的判据对象 ⇒ ⇒ ⇒ 改它必须连 §940 一起验
      ⇒ ⇒ ⇒⇒ （★ §941 记下的那条：「共用/共享的东西要一起跑」）

⚠ 变异的「前提」：本节的复活修法是**清 `deleted_at`**；而墓碑清单**就是**
   `deleted_at` 那一列 ⇒ ⇒ ⇒ 复活 ⇒ 墓碑自动消失 ⇒ ⇒ ⇒ 两件事一起消。
   ⇒ ⇒ ⇒ 反过来，判据里**必须**钉住「DELETE 之后墓碑里有它」这个前提
   ⇒ ⇒ ⇒ 否则测的是一个恒空的东西（★ 前提不成立 ⇒ 判据没有区分力）。

判据命令：
  go test ./internal/server/ -run 'TestS9(39|41|42)' -count=1
  go test ./internal/notes/  -run 'TestS940' -count=1
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
    'backend/internal/notes/store.go',
    'backend/internal/server/server_assistant.go',
]

# ⛔ 只注入**测试专用** DSN（独立 schema + 自动 DROP）⇒ 绝不碰生产库。
# ⚠ DSN **懒加载**：缺它时本批次「拒绝跑」（退出码 3），而不是让用例被 skip
#   ⇒ skip 的退出码是 0 ⇒ 「跑了个寂寞」与「全过」完全同形（§935.4.2）。
_RUNNERS = {}


def runners():
    if not _RUNNERS:
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        _RUNNERS['server'] = go_pkg_runner(
            './internal/server/', 'TestS9(39|41|42)', env=env, forbid_skip=True)
        _RUNNERS['notes'] = go_pkg_runner(
            './internal/notes/', 'TestS940', env=env, forbid_skip=True)
    return _RUNNERS


def both():
    """两个包**一起**跑 ⇒ 用于「改了 store.go ⇒ 两边都要验」的用例。

    ⚠ 返回 (rc, out)：rc 取两个运行器里**第一个非零** ⇒ 任一侧红 ⇒ 整体红。
    """
    rc1, out1 = runners()['server']()
    rc2, out2 = runners()['notes']()
    return (rc1 or rc2), out1 + '\n' + out2


b = Batch('§942 笔记的删除链：同 id 再写一次必须**复活可见**（墓碑一起消失）/ 删除的「不存在」必须是 404',
          lambda: runners()['server'](), TARGETS)
STORE_GO = b.targets[0]
SERVER_GO = b.targets[1]

# ── 变异的锚点（必须与实现**逐字**一致；不一致 ⇒ 落盘自证会失败并中止）──
#
# ⚠⚠ **锚点必须唯一**（本轮踩到）：`if errors.Is(err, notes.ErrNoteNotFound) {`
#   在 server_assistant.go 里出现 **2 次**（DELETE 分支 3 个 tab、PUT 分支 2 个 tab）
#   ⇒ ⇒ 用它当锚点会被 harness 判成「锚点可能已过期」而中止
#   ⇒ ⇒⇒ ⇒ 修法：**把缩进也写进锚点**（3 个 tab ⇒ 只有 DELETE 分支命中）。

# M1（D1 的本体）：复活被去掉 ⇒ 软删行被同 id 再写一次时，deleted_at 还挂着
#   ⇒ ⇒ ⇒ 正文被更新了，但**所有**读路径都当它不存在、墓碑还说它被删了
#   ⇒ ⇒ ⇒ **哑弹**：用户的最新正文在服务端库里，但服务端自己看不见。
#
# ⚠⚠ 变异**不是**删掉那一行（删掉会让 SET 列表以逗号结尾 ⇒ **SQL 语法错**
#   ⇒ ⇒ 判据红在「DB 报错」上，而不是红在「哑弹」上 ⇒ ⇒ ⇒ 那是**假红**）
#   ⇒ ⇒⇒ 改成「保持原值」：语义等价于「不复活」，且 SQL 合法 ✔
M1_OLD = '\t\t\tdeleted_at    = NULL\n'
M1_NEW = '\t\t\tdeleted_at    = notes.deleted_at\n'

# M2（D2 的本体）：「不存在 / 不是我的」被映射回 500。
M2_OLD = (
    '\t\t\tif errors.Is(err, notes.ErrNoteNotFound) {\n'
    '\t\t\t\twriteError(w, http.StatusNotFound, "note not found")\n'
)
M2_NEW = (
    '\t\t\tif errors.Is(err, notes.ErrNoteNotFound) {\n'
    '\t\t\t\twriteError(w, http.StatusInternalServerError, "note not found")\n'
)

# M3（D2 的**根因**）：store 层退回**裸字符串**错误
#   ⇒ ⇒ ⇒ HTTP 层的 errors.Is 判不出来 ⇒ ⇒ ⇒ 「不存在」又变回 500
#   ⇒ ⇒ ⇒ ⇒ 这条证明「修法必须落在 **store 层**」——只改 HTTP 层是不够的。
M3_OLD = '\t\treturn ErrNoteNotFound\n'
M3_NEW = '\t\treturn fmt.Errorf("note not found or already deleted")\n'

# M4（D2 的第二腿）：`deleted_at IS NULL` 谓词被去掉
#   ⇒ ⇒ ⇒ 重复删除也会「成功」（200）⇒ ⇒ ⇒ 而它本该是 404
#   ⇒ ⇒ ⇒ ⇒ 顺带说明这条谓词还有别的作用：重复删**不动**那一行
#   ⇒ ⇒ ⇒ ⇒ （墓碑的 deleted_at 时间不会被刷新）。
M4_OLD = (
    '\t\tUPDATE notes SET deleted_at = CURRENT_TIMESTAMP\n'
    '\t\tWHERE id = $1 AND user_id = $2 AND workspace_id = $3 AND deleted_at IS NULL\n'
)
M4_NEW = (
    '\t\tUPDATE notes SET deleted_at = CURRENT_TIMESTAMP\n'
    '\t\tWHERE id = $1 AND user_id = $2 AND workspace_id = $3\n'
)

# ── 负控的锚点（语义等价 ⇒ 必须保持绿）────────────────────────────────

# N1：`NULL` 写成 `NULL::timestamptz`（等价）⇒ 必须保持绿。
N1_OLD = '\t\t\tdeleted_at    = NULL\n'
N1_NEW = '\t\t\tdeleted_at    = NULL::timestamptz\n'

# N2：404 的**文案**改写（语义等价）⇒ 必须保持绿。
#
# ⚠ 3 个 tab ⇒ 只有 DELETE 分支命中（PUT 分支是 2 个 tab）⇒ 唯一。
N2_OLD = '\t\t\t\twriteError(w, http.StatusNotFound, "note not found")\n'
N2_NEW = '\t\t\t\twriteError(w, http.StatusNotFound, "note not found (or not yours)")\n'

# N3：typed error 的**文案**改写（语义等价）⇒ 必须保持绿。
N3_OLD = 'var ErrNoteNotFound = errors.New("notes: note not found")'
N3_NEW = 'var ErrNoteNotFound = errors.New("notes: no such note")'


def body():
    r = runners()
    rc, out = r['server']()
    b.record('基线：未变异时判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))
    rc, out = r['notes']()
    b.record('基线：notes 包（§940）判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1200:]))

    # ── M1 必须红：复活被去掉 ⇒ 哑弹 ──────────────────────────────────
    def m1():
        b.mutate(STORE_GO, M1_OLD, M1_NEW, 'M1')
    b.case('M1 复活被去掉（同 id 再写一次仍是哑弹）⇒ 「复活必须可见」判据必须红', True,
           must_contain=['哑弹'],
           mutate_fn=m1, runner=both)

    # ── M2 必须红：「不存在」被映射回 500 ──────────────────────────────
    def m2():
        b.mutate(SERVER_GO, M2_OLD, M2_NEW, 'M2')
    b.case('M2 「不存在 / 不是我的」被映射回 500 ⇒ 「必须是 404」判据必须红', True,
           must_contain=['期望 **404**'],
           mutate_fn=m2)

    # ── M3 必须红：store 层退回裸字符串错误 ────────────────────────────
    def m3():
        b.mutate(STORE_GO, M3_OLD, M3_NEW, 'M3')
    b.case('M3 store 层退回裸字符串错误 ⇒ HTTP 层判不出来 ⇒ 「404」判据必须红（证明修法必须落在 store 层）', True,
           must_contain=['期望 **404**'],
           mutate_fn=m3, runner=both)

    # ── M4 必须红：重复删除也算成功 ────────────────────────────────────
    def m4():
        b.mutate(STORE_GO, M4_OLD, M4_NEW, 'M4')
    b.case('M4 「已经删过」也算删除成功 ⇒ 「重复删必须是 404」判据必须红', True,
           must_contain=['期望 **404**'],
           mutate_fn=m4, runner=both)

    # ── N1 负控：NULL 写成 NULL::timestamptz ⇒ 必须保持绿 ──────────────
    def n1():
        b.mutate(STORE_GO, N1_OLD, N1_NEW, 'N1')
    b.case('N1（负控）NULL 写成 NULL::timestamptz（等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n1, runner=both)

    # ── N2 负控：404 文案改写 ⇒ 必须保持绿 ─────────────────────────────
    def n2():
        b.mutate(SERVER_GO, N2_OLD, N2_NEW, 'N2')
    b.case('N2（负控）404 的文案改写（语义等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n2)

    # ── N3 负控：typed error 的文案改写 ⇒ 必须保持绿 ───────────────────
    def n3():
        b.mutate(STORE_GO, N3_OLD, N3_NEW, 'N3')
    b.case('N3（负控）typed error 的文案改写（语义等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n3, runner=both)


if __name__ == '__main__':
    try:
        run(b, body, extra_runners=[lambda: runners()['notes']()])
    except HarnessError as e:
        print('❌ %s' % e)
        sys.exit(3)

#!/usr/bin/env python3
"""§941：「随手记 → 笔记」这条链的变异批次。

⚠ 为什么本批次**必须落盘**（§935 的教训）：§935 发现「当时的变异脚本没落盘」
   ⇒ 「当时绿 = 现在绿」**永久不可回溯** ⇒ ⇒ 本仓从此规定：**变异脚本要落盘**。

⚠ 本批次**需要真 PG**：判据量的全是**落库之后的行**（正文长度 / 归属 / 租户）
   ⇒ ⇒ 没有 PG 时用例会 skip，而 skip 的退出码是 0
   ⇒ ⇒ 本批次在缺 PG 时**拒绝跑**（退出码 3），而不是「跑了个寂寞」。
   见 `_harness.require_pg_dsn` 与 `go_pkg_runner(forbid_skip=True)`。

⚠ 本批次的判据**同时跑 §939 与 §941**（`-run 'TestS9(39|41)'`）：
   §939 是「会议 → 笔记」那条链的判据，§941 是「随手记 → 笔记」这条链的
   ⇒ ⇒ ⇒ 两条链现在**共用**一份口径（server_note_ingest.go）
   ⇒ ⇒ ⇒ 共用之后，**改一份会影响两份** ⇒ ⇒ ⇒ 判据必须一起跑，
   否则「会议链那边被带跑偏了」根本看不见。

⚠⚠ 本批次量的是**三件不同的事**，别混成一句：

  ① **上限是一份**（不是两份）—— M2 把 PUT 的 1 MiB 分叉**装回去**；
  ② **「太大」与「格式错」可区分** —— M1（退回 400）与 M5（丢了稳定错误码）；
  ③ **请求不会被半处理** —— M3（退回流式解码 ⇒ 前缀完整 + 尾巴超长时
     「返回 201 却只处理了 1% 的 body」）。

判据命令：
  go test ./internal/server/ -run 'TestS9(39|41)' -count=1
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
    'backend/internal/server/server_note_ingest.go',
    'backend/internal/server/server_assistant.go',
    'backend/internal/server/server_meeting_note_ingest.go',
]

# ⛔ 只注入**测试专用** DSN（独立 schema + 自动 DROP）⇒ 绝不碰生产库。
# ⚠ DSN **懒加载**：缺它时本批次「拒绝跑」（退出码 3），而不是让用例被 skip
#   ⇒ skip 的退出码是 0 ⇒ 「跑了个寂寞」与「全过」完全同形（§935.4.2）。
_RUNNERS = {}


def runners():
    if not _RUNNERS:
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        _RUNNERS['server'] = go_pkg_runner(
            './internal/server/', 'TestS9(39|41)', env=env, forbid_skip=True)
    return _RUNNERS


b = Batch('§941 随手记 → 笔记链：正文上限一份 / 「太大」与「格式错」可区分 / 不半处理 / 归属冲突 409',
          lambda: runners()['server'](), TARGETS)
NOTE_GO = b.targets[0]
ASSISTANT_GO = b.targets[1]
MEETING_GO = b.targets[2]

# ── 变异的锚点（必须与实现**逐字**一致；不一致 ⇒ 落盘自证会失败并中止）──
#
# ⚠⚠ 锚点纪律（§940 本轮踩到）：锚点**凭记忆写** ⇒ 0 命中 ⇒ 批次当场中止
#   ⇒ ⇒ 下面的锚点**全部**是从真实文件里 grep 出来的（连行号都对得上）。

# M1（D1 的本体）：「太大」被说成「请求体无效」⇒ 与「JSON 坏了」**同形**。
#
# ⚠ 这条把「413 + 稳定错误码」整个换成「400 invalid body」
#   ⇒ ⇒ 「失败必须可区分」那条纪律被破坏 ⇒ 客户端分不出
#   「重试小一点有用」与「你的 JSON 坏了，重试没用」。
#
# ⚠⚠⚠ **第一版的 M1 是编译红**（把 `writeStructuredError(...)` 换成
#   `writeError(w, http.StatusBadRequest, "invalid body")` ⇒ ⇒ **`fmt` 变成未使用**
#   ⇒ ⇒ **整个包 build failed** ⇒ ⇒ 一条判据都没跑 ⇒ ⇒ 而退出码**仍是 1**
#   ⇒ ⇒ ⇒ 「变红」是**假的**（★ §940 记过的「编译红 ≠ 断言红」）
#   ⇒ ⇒⇒ 是 harness 的 `must_contain` 把它逮住的：期望消息不在场
#   ⇒ ⇒⇒ ⇒ **这条纪律的机制就在这里** —— `must_contain` 不只核对「红不红」，
#   ⇒ ⇒⇒ ⇒ 它核对「红在**哪一条**」⇒ ⇒ ⇒ 编译红**过不了**这一关。
#   ⇒ ⇒⇒ 修法：让替换后的代码**编译干净**（保留 `fmt` 与常量的使用）。
M1_OLD = (
    '\t\t\ts.writeStructuredError(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,\n'
    '\t\t\t\tfmt.Sprintf("request body exceeds the maximum allowed size (%d MiB)", noteBodyLimitBytes>>20))\n'
)
M1_NEW = (
    '\t\t\twriteError(w, http.StatusBadRequest,\n'
    '\t\t\t\tfmt.Sprintf("invalid body (limit %d bytes)", noteBodyLimitBytes))\n'
)

# M2（D1 的**分叉**）：把 PUT 那条 1 MiB 的闸**装回去**
#   ⇒ ⇒ POST 收 2 MiB、PUT 只收 1 MiB ⇒ 同一条笔记写得进去、改不回来。
#
# ⚠ 这是 §941 的**本体缺陷**被原样复原 ⇒ ⇒ 判据必须红。
# ⚠ 变异写在**共用**的 readNoteBody 里（按 Method 分派），
#   而不是改回 handler 里各自一份 ⇒ ⇒ 精确复原「两条路径两份口径」的**行为**，
#   但不把「口径抄两遍」这个**形状**也复原（那是 M7 的活）。
M2_OLD = '\tr.Body = http.MaxBytesReader(w, r.Body, noteBodyLimitBytes)\n'
M2_NEW = (
    '\tlimit := int64(noteBodyLimitBytes)\n'
    '\tif r.Method == http.MethodPut {\n'
    '\t\tlimit = 1 << 20\n'
    '\t}\n'
    '\tr.Body = http.MaxBytesReader(w, r.Body, limit)\n'
)

# M3（D1 的「半处理」）：「先读出来再解」被退回「流式解码」
#   ⇒ ⇒ Decoder 找到**第一个完整 JSON 值**就停
#   ⇒ ⇒ 「前缀完整 + 3 MiB 尾巴」的 body 被**处理一半**并回 201
#   ⇒ ⇒ ⇒ 那道「上限」根本没生效。
#
# ⚠ 这是**读数层面**的缺陷：状态码是 201，看起来完全成功
#   ⇒ ⇒ ⇒ 不测「前缀完整」这一形态就看不见它。
M3_OLD = (
    '\t\traw, ok := s.readNoteBody(w, r)\n'
    '\t\tif !ok {\n'
    '\t\t\treturn\n'
    '\t\t}\n'
    '\t\tvar n notes.Note\n'
    '\t\tif err := decodeNoteJSON(raw, &n); err != nil {\n'
)
M3_NEW = (
    '\t\tvar n notes.Note\n'
    '\t\tif err := json.NewDecoder(r.Body).Decode(&n); err != nil {\n'
)

# M4（D3 的本体）：归属冲突被映射回 500 ⇒ 客户端以为「重试有用」。
M4_OLD = (
    '\tif errors.Is(err, notes.ErrNoteOwnershipConflict) {\n'
    '\t\ts.writeStructuredError(w, r, http.StatusConflict, CodeConflict, err.Error())\n'
)
M4_NEW = (
    '\tif errors.Is(err, notes.ErrNoteOwnershipConflict) {\n'
    '\t\ts.writeStructuredError(w, r, http.StatusInternalServerError, CodeInvalidRequest, err.Error())\n'
)

# M5（D1 的「稳定错误码」那一半）：状态码仍是 413，但**丢了 code 字段**
#   ⇒ ⇒ 客户端拿不到 `payload_too_large` ⇒ 映射不到「太大」的中文文案。
#
# ⚠ 这条说明「413」与「稳定错误码」是**两件**事：只有 413 不够。
M5_OLD = M1_OLD
M5_NEW = (
    '\t\t\twriteError(w, http.StatusRequestEntityTooLarge,\n'
    '\t\t\t\tfmt.Sprintf("request body exceeds the maximum allowed size (%d MiB)", noteBodyLimitBytes>>20))\n'
)

# M6（D2 的**广播目标**）：写成零值 ⇒ hub 退化成**全局广播**
#   ⇒ ⇒ 每个在线用户的设备都收到这条笔记。
#
# ⚠ §939 的注释写着「零值 target 会退化成全局广播」⇒ ⇒ ⇒ 这条注释必须兑现
#   ⇒ ⇒ 判据（随手记链 + 会议链两条）都必须红。
M6_OLD = '\treturn ws.BroadcastTarget{UserID: t.OwnerID}\n'
M6_NEW = '\treturn ws.BroadcastTarget{}\n'

# M7（D2 的**口径一份**）：会议链自己写一份广播目标 ⇒ 与随手记链**分叉**。
#
# ⚠ 这条量的不是「广播对不对」（那由 M6 与 §939 的判据管），
#   而是「两条链**是不是同一份**」⇒ ⇒ 分叉是 §939 的 D1/D2 复发的**形状**。
M7_OLD = (
    'func (t meetingNoteTarget) broadcastTarget() ws.BroadcastTarget {\n'
    '\treturn t.noteTarget.broadcastTarget()\n'
)
M7_NEW = (
    'func (t meetingNoteTarget) broadcastTarget() ws.BroadcastTarget {\n'
    '\treturn ws.BroadcastTarget{UserID: t.OwnerID, WorkspaceID: t.WorkspaceID}\n'
)

# M8（D2 的「落点只认 JWT」）：ingestNote 不再覆写归属
#   ⇒ ⇒ body 里的 userId/workspaceId 生效（或落成空串）
#   ⇒ ⇒ ⇒ §939 D1 的形状（「落进空租户 ⇒ 谁都查不到」）复发。
M8_OLD = '\tn.UserID = target.OwnerID\n\tn.WorkspaceID = target.WorkspaceID\n'
M8_NEW = ''

# ── 负控的锚点（语义等价 ⇒ 必须保持绿）────────────────────────────────

# N1：413 的**文案**改写（语义等价）⇒ 必须保持绿。
#   ⇒ 证明判据认的是**状态码 + 稳定错误码**，不是那句文案。
N1_OLD = '\t\t\t\tfmt.Sprintf("request body exceeds the maximum allowed size (%d MiB)", noteBodyLimitBytes>>20))\n'
N1_NEW = '\t\t\t\tfmt.Sprintf("request body too large (max %d MiB)", noteBodyLimitBytes>>20))\n'

# N2：400 分支的**文案**改写（语义等价）⇒ 必须保持绿。
N2_OLD = '\t\twriteError(w, http.StatusBadRequest, "read body: "+err.Error())\n'
N2_NEW = '\t\twriteError(w, http.StatusBadRequest, "cannot read body: "+err.Error())\n'

# N3：广播目标在调用点**显式构造**（与共用那一份**逐字段等价**）
#   ⇒ ⇒ 必须保持绿。
#   ⇒ ⇒ ⇒ 这条负控量的是：「口径一份」是**设计**，而判据只要求**行为**一致
#   ⇒ ⇒ ⇒ ⇒ 所以将来若实现换写法，判据不会误报。
N3_OLD = '\ts.wsHub.BroadcastTo(target.broadcastTarget(), "note.created", n)\n'
N3_NEW = '\ts.wsHub.BroadcastTo(ws.BroadcastTarget{UserID: target.OwnerID}, "note.created", n)\n'


def body():
    r = runners()
    rc, out = r['server']()
    b.record('基线：未变异时判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))

    # ── M1 必须红：「太大」被说成「请求体无效」 ──────────────────────
    def m1():
        b.mutate(NOTE_GO, M1_OLD, M1_NEW, 'M1')
    b.case('M1 超限被说成「请求体无效」（400 invalid body）⇒ 「太大 vs 格式错」判据必须红', True,
           must_contain=['期望 413'],
           mutate_fn=m1)

    # ── M2 必须红：PUT 的 1 MiB 分叉被装回去 ─────────────────────────
    def m2():
        b.mutate(NOTE_GO, M2_OLD, M2_NEW, 'M2')
    b.case('M2 PUT 的 1 MiB 闸被装回去 ⇒ 「同一条笔记写得进去却改不回来」判据必须红', True,
           must_contain=['写得进去却改不回来'],
           mutate_fn=m2)

    # ── M3 必须红：退回流式解码 ⇒ 请求被半处理 ───────────────────────
    def m3():
        b.mutate(ASSISTANT_GO, M3_OLD, M3_NEW, 'M3')
    b.case('M3 退回流式解码（前缀完整 + 3 MiB 尾巴仍回 201）⇒ 「不得半处理」判据必须红', True,
           must_contain=['上限必须对'],
           mutate_fn=m3)

    # ── M4 必须红：归属冲突被映射回 500 ──────────────────────────────
    def m4():
        b.mutate(NOTE_GO, M4_OLD, M4_NEW, 'M4')
    b.case('M4 归属冲突被映射回 500 ⇒ 「冲突必须是 409」判据必须红', True,
           must_contain=['期望 409'],
           mutate_fn=m4)

    # ── M5 必须红：413 但丢了稳定错误码 ──────────────────────────────
    def m5():
        b.mutate(NOTE_GO, M5_OLD, M5_NEW, 'M5')
    b.case('M5 超限只剩 413、丢了 payload_too_large ⇒ 「稳定错误码」判据必须红', True,
           must_contain=['缺少稳定错误码'],
           mutate_fn=m5)

    # ── M6 必须红：广播目标写成零值 ⇒ 全体广播 ───────────────────────
    def m6():
        b.mutate(NOTE_GO, M6_OLD, M6_NEW, 'M6')
    b.case('M6 广播目标写成零值（退化成全体广播）⇒ 两条链的「只发给属主」判据必须红', True,
           must_contain=['广播没有作用域'],
           mutate_fn=m6)

    # ── M7 必须红：两条链的广播口径分叉 ──────────────────────────────
    def m7():
        b.mutate(MEETING_GO, M7_OLD, M7_NEW, 'M7')
    b.case('M7 会议链自己写一份广播目标 ⇒ 「两条链同一份口径」判据必须红', True,
           must_contain=['两条链的广播 WorkspaceID 不同'],
           mutate_fn=m7)

    # ── M8 必须红：落点不再只认 JWT ──────────────────────────────────
    def m8():
        b.mutate(NOTE_GO, M8_OLD, M8_NEW, 'M8')
    b.case('M8 ingestNote 不再覆写归属 ⇒ 「落点只认 JWT」判据必须红（§939 D1 的形状复发）', True,
           must_contain=['落点丢了租户'],
           mutate_fn=m8)

    # ── N1 负控：413 文案改写 ⇒ 必须保持绿 ───────────────────────────
    def n1():
        b.mutate(NOTE_GO, N1_OLD, N1_NEW, 'N1')
    b.case('N1（负控）413 的文案改写（语义等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n1)

    # ── N2 负控：400 文案改写 ⇒ 必须保持绿 ───────────────────────────
    def n2():
        b.mutate(NOTE_GO, N2_OLD, N2_NEW, 'N2')
    b.case('N2（负控）400 分支的文案改写（语义等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n2)

    # ── N3 负控：调用点显式构造广播目标 ⇒ 必须保持绿 ─────────────────
    def n3():
        b.mutate(NOTE_GO, N3_OLD, N3_NEW, 'N3')
    b.case('N3（负控）调用点显式构造广播目标（与共用那一份逐字段等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n3)


if __name__ == '__main__':
    try:
        run(b, body)
    except HarnessError as e:
        print('❌ %s' % e)
        sys.exit(3)

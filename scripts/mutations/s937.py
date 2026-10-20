#!/usr/bin/env python3
"""§937：服务端会议链「每次操作成本 ∝ 新增量，而不是 ∝ 全文」的变异批次。

⚠ 为什么本批次**必须落盘**（§935 的教训）：§935 发现「当时的变异脚本没落盘」
   ⇒ 「当时绿 = 现在绿」**永久不可回溯** ⇒ ⇒ 本仓从此规定：**变异脚本要落盘**。

⚠ 本批次**需要真 PG**：M2（PG 的尾读 SQL 退化成整读）与 M3（定向更新把
   transcript 列加回 SET）只发生在 PG 版 ⇒ ⇒ 内存版的判据看不见它们
   ⇒ ⇒ 没有 PG 时这些用例会 **skip**，而 skip 的退出码是 0 ⇒ ⇒ 本批次在缺 PG
   时**拒绝跑**（不是「跑了个寂寞」）。见 `_harness.require_pg_dsn` 与
   `go_pkg_runner(forbid_skip=True)`。

判据命令：
  go test ./internal/meeting/ -run 'TestTranscriptTail|TestBuildMetaUpdateSQL|TestUpdateMetaScoped|TestTranscriptTailScopedScope|TestPGTranscriptTail|TestPGUpdateMetaScoped|TestTranscriptTailStoresAgree' -count=1
  go test ./internal/server/ -run 'TestS937' -count=1
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
    'backend/internal/meeting/transcript.go',
    'backend/internal/meeting/pg_store.go',
    'backend/internal/server/server_meeting_analyze.go',
    'backend/internal/server/server_meeting_ingest.go',
]

# ⛔ 只注入**测试专用** DSN（独立 schema + 自动 DROP）⇒ 绝不碰生产库。
# ⚠ DSN **懒加载**：缺它时本批次「拒绝跑」（退出码 3），而不是让用例被 skip
#   ⇒ skip 的退出码是 0 ⇒ 「跑了个寂寞」与「全过」完全同形（§935.4.2）。
_RUNNERS = {}

MEETING_PATTERN = (
    'TestTranscriptTail|TestBuildMetaUpdateSQL|TestUpdateMetaScoped|'
    'TestTranscriptTailScopedScope|TestPGTranscriptTail|TestPGUpdateMetaScoped|'
    'TestTranscriptTailStoresAgree'
)


def runners():
    if not _RUNNERS:
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        _RUNNERS['meeting'] = go_pkg_runner(
            './internal/meeting/', MEETING_PATTERN, env=env, forbid_skip=True)
        _RUNNERS['server'] = go_pkg_runner('./internal/server/', 'TestS937')
    return _RUNNERS


def combined():
    """两个包的判据都跑 ⇒ 任一红就算红（收尾时两者都必须绿）。"""
    r = runners()
    m_rc, m_out = r['meeting']()
    s_rc, s_out = r['server']()
    return (m_rc or s_rc), m_out + '\n\n' + s_out


b = Batch('§937 服务端会议链的「每次操作成本」（D1 读放大 / D2 写放大）', combined, TARGETS)
TAIL_GO = b.targets[0]
PG_STORE = b.targets[1]
ANALYZE_GO = b.targets[2]
INGEST_GO = b.targets[3]

# ── 变异的锚点（必须与实现**逐字**一致；不一致 ⇒ 落盘自证会失败并中止）──

# M1：尾读口径退化成**整读**（= §937 修复前的行为）。
#
# ⚠ 这是「同一处逻辑退化」的变异：口径退化 ⇒ PG 与内存两个 store **同时**
#   退化成整读 ⇒ ⇒ server 侧的读量判据（用内存 store）也会红
#   ⇒ ⇒ 证明那条判据**真的在看搬运量**，而不是在看「调用了哪个方法」。
TAIL_OLD = (
    'func TranscriptTail(transcript string, fromRune int) (tail string, totalRunes int) {\n'
    '\ttotalRunes = utf8.RuneCountInString(transcript)\n'
    '\tif fromRune <= 0 {\n'
    '\t\treturn transcript, totalRunes\n'
    '\t}\n'
    '\tif fromRune >= totalRunes {\n'
    '\t\treturn "", totalRunes\n'
    '\t}\n'
    '\tseen := 0\n'
    '\tfor idx := range transcript {\n'
    '\t\tif seen == fromRune {\n'
    '\t\t\treturn transcript[idx:], totalRunes\n'
    '\t\t}\n'
    '\t\tseen++\n'
    '\t}\n'
    '\treturn "", totalRunes\n'
    '}'
)
TAIL_WHOLE = (
    'func TranscriptTail(transcript string, fromRune int) (tail string, totalRunes int) {\n'
    '\ttotalRunes = utf8.RuneCountInString(transcript)\n'
    '\treturn transcript, totalRunes\n'
    '}'
)

# M2：PG 版的游标**恒为 1** ⇒ 尾读退化成整读。
#
# ⚠⚠ **第一版这里踩了「变异自己的错」**：改成
#   `SELECT transcript, char_length(transcript) … WHERE id=$1 AND owner_id=$2
#    AND workspace_id=$3` ⇒ **$4 这个占位符没了** ⇒ pgx 报
#   `expected 3 arguments, got 4` ⇒ ⇒ 那是**协议错**，断言**一条都没跑到**
#   ⇒ ⇒ 零证据价值（§936.5.2 的教训：变异必须「只改语义」）。
#   ⇒ 现在改成**游标恒为 1**：占位符数量不变、SQL 不变 ⇒ 只有语义变了。
#
# ⚠ 它**只**影响 PG 版 ⇒ ⇒ 内存版的判据看不见它 ⇒ ⇒ 本条同时证明
#   「PG 版有独立判据」（跨 store 一致性 + DB 交出字节数）而不是靠内存版顺带覆盖。
PG_START_OLD = '\tstart := fromRune + 1\n'
PG_START_WHOLE = '\tstart := 1\n'

# M2b：PG 的尾读 SQL 退化成整读（`SELECT transcript`）。
#
# ⚠ 与 M2 同源但落在**另一层**（SQL 文本）：⇒ 必须**保住 $4 这个占位符**
#   （否则又是协议错）⇒ ⇒ 用一个恒真的谓词把它钉在 WHERE 上。
PG_TAIL_OLD = (
    'const transcriptTailQuery = `SELECT substr(transcript, $4), char_length(transcript)\n'
    '\tFROM meetings WHERE id=$1 AND owner_id=$2 AND workspace_id=$3`'
)
PG_TAIL_WHOLE = (
    'const transcriptTailQuery = `SELECT transcript, char_length(transcript)\n'
    '\tFROM meetings WHERE id=$1 AND owner_id=$2 AND workspace_id=$3\n'
    '\t  AND $4::int >= 0`'
)

# M3：定向更新的 SET 里把 transcript 列**加回来**（= 退回整行写）。
#
# ⚠ 一条变异打两处判据：SQL 形状守卫（"SQL 里出现了 transcript"）
#   与 PG 行为判据（"定向更新覆盖了转写"—— 这次它真的把转写抹了）。
META_OLD = (
    '\tif status != nil {\n'
    '\t\targs = append(args, *status)\n'
    '\t\tsets = append(sets, fmt.Sprintf("status=$%d", len(args)))\n'
    '\t}\n'
)
META_WITH_TRANSCRIPT = (
    '\tif status != nil {\n'
    '\t\targs = append(args, *status)\n'
    '\t\tsets = append(sets, fmt.Sprintf("status=$%d", len(args)))\n'
    '\t}\n'
    '\targs = append(args, "")\n'
    '\tsets = append(sets, fmt.Sprintf("transcript=$%d", len(args)))\n'
)

# M6：analyze handler 退回「整读 + Go 里切」（= §937 修复前的**原实现形状**）。
#
# ⚠ **两处一起改**：只改第一处 ⇒ 后面的收缩分支里 `err` 未声明 ⇒ 编译失败
#   ⇒ ⇒ 那样红是「编不过」，**零证据价值**（§935 的教训）。
ANALYZE_READ_OLD = (
    '\ttail, totalRunes, err := s.meetingStore.TranscriptTailScoped(meetingID, uid, workspaceID, fromRune)\n'
    '\tif err != nil {\n'
    '\t\twriteError(w, http.StatusNotFound, "meeting not found")\n'
    '\t\treturn\n'
    '\t}\n'
)
ANALYZE_READ_WHOLE = (
    '\tvar tail string\n'
    '\tvar totalRunes int\n'
    '\t{\n'
    '\t\tfullM, gerr := s.meetingStore.GetScoped(meetingID, uid, workspaceID)\n'
    '\t\tif gerr != nil {\n'
    '\t\t\twriteError(w, http.StatusNotFound, "meeting not found")\n'
    '\t\t\treturn\n'
    '\t\t}\n'
    '\t\ttotalRunes = utf8.RuneCountInString(fullM.Transcript)\n'
    '\t\tif totalRunes > fromRune {\n'
    '\t\t\ttail = string([]rune(fullM.Transcript)[fromRune:])\n'
    '\t\t}\n'
    '\t}\n'
)
ANALYZE_SHRINK_OLD = (
    '\t\ttail, totalRunes, err = s.meetingStore.TranscriptTailScoped(meetingID, uid, workspaceID, 0)\n'
    '\t\tif err != nil {\n'
    '\t\t\twriteError(w, http.StatusNotFound, "meeting not found")\n'
    '\t\t\treturn\n'
    '\t\t}\n'
)
ANALYZE_SHRINK_WHOLE = (
    '\t\tfullM2, gerr2 := s.meetingStore.GetScoped(meetingID, uid, workspaceID)\n'
    '\t\tif gerr2 != nil {\n'
    '\t\t\twriteError(w, http.StatusNotFound, "meeting not found")\n'
    '\t\t\treturn\n'
    '\t\t}\n'
    '\t\ttotalRunes = utf8.RuneCountInString(fullM2.Transcript)\n'
    '\t\ttail = fullM2.Transcript\n'
)

# M4：精校收尾退回「读整行 + 写整行」（= §937 修复前的原实现形状）。
#
# ⚠ 变异里**保留** errors.Is / meeting.ErrMeetingNotFound 的用法 ⇒
#   否则那两个 import 变成未使用 ⇒ 编译失败 ⇒ 同样零证据价值。
REFINE_OLD = (
    '\t\tif _, err := s.meetingStore.UpdateMetaScoped(\n'
    '\t\t\tmeetingID, uidForStore, workspaceIDForStore, titlePtr, &status); err != nil {\n'
    '\t\t\tif errors.Is(err, meeting.ErrMeetingNotFound) {\n'
    '\t\t\t\t// No stub creation: fabricating a row here would land it in the wrong\n'
    '\t\t\t\t// workspace and mask a genuine cross-workspace/missing-meeting error.\n'
    '\t\t\t\tlog.Printf("[meeting] refine target %s not found in workspace %s (owner=%s): %v",\n'
    '\t\t\t\t\tmeetingID, workspaceIDForStore, uidForStore, err)\n'
    '\t\t\t} else {\n'
    '\t\t\t\tlog.Printf("[meeting] update after refine %s: %v", meetingID, err)\n'
    '\t\t\t}\n'
    '\t\t}\n'
)
REFINE_WHOLE = (
    '\t\tif existing, gerr := s.meetingStore.GetScoped(meetingID, uidForStore, workspaceIDForStore); gerr == nil {\n'
    '\t\t\tif titlePtr != nil {\n'
    '\t\t\t\texisting.Title = *titlePtr\n'
    '\t\t\t}\n'
    '\t\t\texisting.Status = status\n'
    '\t\t\tif err := s.meetingStore.UpdateScoped(existing, uidForStore, workspaceIDForStore); err != nil {\n'
    '\t\t\t\tlog.Printf("[meeting] update after refine %s: %v", meetingID, err)\n'
    '\t\t\t}\n'
    '\t\t} else if errors.Is(gerr, meeting.ErrMeetingNotFound) {\n'
    '\t\t\tlog.Printf("[meeting] refine target %s not found in workspace %s (owner=%s): %v",\n'
    '\t\t\t\tmeetingID, workspaceIDForStore, uidForStore, gerr)\n'
    '\t\t}\n'
)

# M5：幂等同步退回整行写。
CREATE_OLD = (
    '\t\t\ttitle := req.Title\n'
    '\t\t\tupdatedAt, err := s.meetingStore.UpdateMetaScoped(id, uid, workspaceID, &title, nil)\n'
    '\t\t\tif err != nil {\n'
    '\t\t\t\twriteError(w, http.StatusInternalServerError, "failed to update meeting: "+err.Error())\n'
    '\t\t\t\treturn\n'
    '\t\t\t}\n'
    '\t\t\texisting.Title = title\n'
    '\t\t\texisting.UpdatedAt = updatedAt\n'
)
CREATE_WHOLE = (
    '\t\t\ttitle := req.Title\n'
    '\t\t\texisting.Title = title\n'
    '\t\t\tif err := s.meetingStore.UpdateScoped(existing, uid, workspaceID); err != nil {\n'
    '\t\t\t\twriteError(w, http.StatusInternalServerError, "failed to update meeting: "+err.Error())\n'
    '\t\t\t\treturn\n'
    '\t\t\t}\n'
)

# ── 负控的锚点（语义等价 ⇒ 必须保持绿）────────────────────────────────

# N1：PG 尾读 SQL **只改排版**。
PG_TAIL_FMT = (
    'const transcriptTailQuery = `SELECT substr(transcript,$4), char_length(transcript) '
    'FROM meetings WHERE id=$1 AND owner_id=$2 AND workspace_id=$3`'
)

# N2：定向更新的 SET **只改排版**（`status=$5` → `status = $5`）。
META_FMT_OLD = '\t\tsets = append(sets, fmt.Sprintf("status=$%d", len(args)))\n'
META_FMT_NEW = '\t\tsets = append(sets, fmt.Sprintf("status = $%d", len(args)))\n'

# N3（盲区登记）：尾读口径换成「整份 []rune 展开再切」。
#
# ⚠⚠ **返回值逐字相同** ⇒ 所有判据都**绿** ⇒ ⇒ 这条是**如实登记量具盲区**：
#   本节的判据量的是「**跨存储边界**搬走了多少字节」，
#   **量不到 store 内部的分配量** ⇒ ⇒ 「内存版尾读仍要 O(全文) 分配」
#   这条**没有判据** ⇒ ⇒ 写下来，免得后人以为「尾读 = 零分配」。
#   ⇒ 生产路径是 PG（分配在 DB 进程内），所以这个盲区是可接受的 ——
#   但**可接受**不等于**不存在**，必须留痕。
TAIL_RUNE_COPY = (
    'func TranscriptTail(transcript string, fromRune int) (tail string, totalRunes int) {\n'
    '\ttotalRunes = utf8.RuneCountInString(transcript)\n'
    '\trunes := []rune(transcript)\n'
    '\tif fromRune <= 0 {\n'
    '\t\treturn transcript, totalRunes\n'
    '\t}\n'
    '\tif fromRune >= totalRunes {\n'
    '\t\treturn "", totalRunes\n'
    '\t}\n'
    '\treturn string(runes[fromRune:]), totalRunes\n'
    '}'
)
#
# ⚠⚠ **第一版这里也踩了「变异自己的错」**：把 totalRunes 改成 len(runes) ⇒
#   `utf8` 变成**未使用 import** ⇒ **编译失败** ⇒ ⇒ 负控「变红」是**假的**
#   （不是判据不敏感，是变异根本没编译过）。
#   ⇒ 现在**保留 utf8 的那一次调用**（纯等价、零成本）⇒ 编译通过 ⇒
#   ⇒ 这条负控才真的在验「判据量不到 store 内部的分配量」。

# N4：空转写守卫的 `fromRune == 0` 换成 `fromRune <= 0`（游标永远 ≥ 0 ⇒ 等价）。
BLANK_OLD = '\tif fromRune == 0 && strings.TrimSpace(tail) == "" {'
BLANK_NEW = '\tif fromRune <= 0 && strings.TrimSpace(tail) == "" {'


def body():
    r = runners()
    rc, out = combined()
    b.record('基线：未变异时两个包的判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))

    # ── M1 必须红：尾读口径退化成整读 ────────────────────────────────
    def m1():
        b.mutate(TAIL_GO, TAIL_OLD, TAIL_WHOLE, 'M1')
    b.case('M1 尾读口径退化成整读 ⇒ 读量判据必须红', True,
           must_contain=['仍在整读（D1 没修掉）'],
           mutate_fn=m1)

    # ── M2 必须红：PG 版的游标恒为 1（尾读退化成整读）────────────────
    def m2():
        b.mutate(PG_STORE, PG_START_OLD, PG_START_WHOLE, 'M2')
    b.case('M2 PG 版游标恒为 1（尾读退化成整读）⇒ PG 字节数判据必须红', True,
           must_contain=['尾读仍搬了'],
           mutate_fn=m2)

    # ── M2b 必须红：PG 的尾读 SQL 退化成整读（保住占位符）────────────
    def m2b():
        b.mutate(PG_STORE, PG_TAIL_OLD, PG_TAIL_WHOLE, 'M2b')
    b.case('M2b PG 尾读 SQL 退化成整读（占位符不变）⇒ PG 字节数判据必须红', True,
           must_contain=['尾读仍搬了'],
           mutate_fn=m2b)

    # ── M3 必须红：定向更新把 transcript 列加回 SET ──────────────────
    def m3():
        b.mutate(TAIL_GO, META_OLD, META_WITH_TRANSCRIPT, 'M3')
    b.case('M3 定向更新把 transcript 列加回 SET ⇒ 形状守卫 + PG 行为判据必须红', True,
           must_contain=['定向更新的 SQL 里出现了 transcript', '定向更新覆盖了转写'],
           mutate_fn=m3)

    # ── M4 必须红：精校收尾退回整行写 ────────────────────────────────
    def m4():
        b.mutate(INGEST_GO, REFINE_OLD, REFINE_WHOLE, 'M4')
    b.case('M4 精校收尾退回「读整行 + 写整行」⇒ 写量判据必须红', True,
           must_contain=['精校收尾写回了'],
           mutate_fn=m4)

    # ── M5 必须红：幂等同步退回整行写 ────────────────────────────────
    def m5():
        b.mutate(INGEST_GO, CREATE_OLD, CREATE_WHOLE, 'M5')
    b.case('M5 幂等同步退回整行写 ⇒ 写量判据必须红', True,
           must_contain=['幂等同步写回了'],
           mutate_fn=m5)

    # ── M6 必须红：analyze handler 退回「整读 + Go 里切」────────────
    def m6():
        b.mutate(ANALYZE_GO, ANALYZE_READ_OLD, ANALYZE_READ_WHOLE, 'M6-read')
        b.mutate(ANALYZE_GO, ANALYZE_SHRINK_OLD, ANALYZE_SHRINK_WHOLE, 'M6-shrink')
    b.case('M6 analyze handler 退回整读（原实现形状）⇒ 读量判据必须红', True,
           must_contain=['仍在整读（D1 没修掉）'],
           mutate_fn=m6)

    # ── N1 负控：PG 尾读 SQL 只改排版 ⇒ 必须保持绿 ───────────────────
    def n1():
        b.mutate(PG_STORE, PG_TAIL_OLD, PG_TAIL_FMT, 'N1')
    b.case('N1（负控）PG 尾读 SQL 只改排版 ⇒ 必须保持绿', False,
           must_not_contain=['尾读仍搬了', 'FAIL'],
           mutate_fn=n1)

    # ── N2 负控：定向更新 SET 只改排版 ⇒ 必须保持绿 ─────────────────
    def n2():
        b.mutate(TAIL_GO, META_FMT_OLD, META_FMT_NEW, 'N2')
    b.case('N2（负控）定向更新的 SET 只改排版 ⇒ 必须保持绿', False,
           must_not_contain=['定向更新的 SQL 里出现了 transcript', 'FAIL'],
           mutate_fn=n2)

    # ── N3 负控/盲区登记：尾读口径换成整份 []rune 展开 ⇒ 必须保持绿 ─
    def n3():
        b.mutate(TAIL_GO, TAIL_OLD, TAIL_RUNE_COPY, 'N3')
    b.case('N3（负控/盲区登记）尾读口径换成整份 []rune 展开 ⇒ 仍绿（说明判据量不到 store 内部分配量）',
           False,
           must_not_contain=['仍在整读（D1 没修掉）', 'FAIL'],
           mutate_fn=n3)

    # ── N4 负控：`fromRune == 0` 换成 `<= 0` ⇒ 必须保持绿 ────────────
    def n4():
        b.mutate(ANALYZE_GO, BLANK_OLD, BLANK_NEW, 'N4')
    b.case('N4（负控）空转写守卫 fromRune==0 换成 <=0（游标恒 ≥0 ⇒ 等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n4)


if __name__ == '__main__':
    try:
        run(b, body)
    except HarnessError as e:
        print('❌ %s' % e)
        sys.exit(3)

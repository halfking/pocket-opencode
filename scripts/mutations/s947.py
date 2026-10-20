#!/usr/bin/env python3
"""§947：裸语句丢弃返回值不许无声（用**类型信息**把这一族量全并定性）。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
   **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠ 本批次是本仓**第三批「把量具自己打坏」的变异**（§945 的 M8、§946 的 M13、
   本批的 M3/M4/M5/M6）—— ⇒ 每次都必须证明：**量具坏了 ⇒ 门会红**。

判据命令：
  go test ./internal/repohygiene/ -run 'TestS947' -count=1

⚠ 本批次的判据是**类型感知**的（go/types + `go list -export`）
   ⇒ 每次跑都会重新做一次类型检查 ⇒ 比纯 AST 的判据慢（实测 ~2s）
   ⇒ ⇒ 但它换来的是**穷尽性**：裸语句里 5043 处，只有 378 处真的丢弃了
   ⇒ ⇒⇒ 而这 378 处里，哪些「丢掉的是 error」**只有类型信息能判**。

⚠ 本批次最该被记住的一条：

   ★ **M3/M4 是「豁免规则被打坏」**：
     豁免规则是本卡口**唯一**的「可以不看」出口 ⇒ 一旦它失效或过宽，
     整个卡口的结论就变了 ⇒ ⇒⇒ 必须有变异**专门去打它**，
     证明「豁免规则一动 ⇒ 门就红」。

★ **M6 是「牙被打坏」**：
   「持久化写的失败必须被兜底」是本卡口的**牙** ⇒ 把「错误分支」的
   判据改坏（认不出 `err != nil`）⇒ 那 6 处 `*os.File.Close` 就不再
   被视为「已兜底」⇒ ⇒⇒ 门必须红。
"""

import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    Batch, go_pkg_runner, run,
)

TARGETS = [
    'backend/internal/repohygiene/bare_discard_test.go',
    'backend/internal/stt/transcribe.go',
    'backend/internal/stt/provider_openai.go',
    'backend/internal/websocket/mobile_hub.go',
    'backend/internal/llmgateway/anthropic.go',
    'backend/internal/server/llmbff_redclaw_provider.go',
    'backend/internal/opencode/approval_projection.go',
    'backend/internal/email/oauth_callback.go',
    'backend/internal/email/mime.go',
    'backend/internal/chatagent/sync.go',
    'backend/internal/adapter/disk/sqlite_ro.go',
    'backend/cmd/pocketd/main.go',
]

b = Batch('§947 裸语句丢弃返回值不许无声（类型感知的普查 + 台账 + 牙）',
          go_pkg_runner('./internal/repohygiene/', 'TestS947'), TARGETS)

HYGIENE = b.targets[0]
TRANSCRIBE = b.targets[1]
PROVIDER = b.targets[2]
MOBILE_HUB = b.targets[3]
ANTHROPIC = b.targets[4]
RECLAW = b.targets[5]
PROJECTION = b.targets[6]
OAUTH = b.targets[7]
MIME = b.targets[8]
SYNC = b.targets[9]
SQLITE_RO = b.targets[10]
POCKETD = b.targets[11]

# ── M1：台账的条数少写一个 ─────────────────────────────────────────────
M1_OLD = '\t\tCallee: "Close", Recv: "*database/sql.Rows", Count: 2,\n'
M1_NEW = '\t\tCallee: "Close", Recv: "*database/sql.Rows", Count: 1,\n'

# ── M2：台账的文件集合里换掉一个文件 ───────────────────────────────────
M2_OLD = '\t\t\t"backend/internal/acchttp/client.go",\n'
M2_NEW = '\t\t\t"backend/internal/agent/transport_http.go",\n'

# ── M3：把「内存缓冲区」豁免打坏（⇒ 180 处缓冲区写入变成未登记）──────
M3_OLD = (
    '\t\tcase "strings.Builder", "*strings.Builder", "bytes.Buffer", "*bytes.Buffer":\n'
    '\t\t\t// 依据：bytes.Buffer / strings.Builder 的写方法**永远返回 nil error**\n'
    '\t\t\t//（它们的写只会因内存不足 panic，不会返回 error）。\n'
    '\t\t\treturn "R1 内存缓冲区（error 恒为 nil）"\n'
)
M3_NEW = (
    '\t\tcase "strings.Builder", "*strings.Builder", "bytes.Buffer", "*bytes.Buffer":\n'
    '\t\t\t// 依据：bytes.Buffer / strings.Builder 的写方法**永远返回 nil error**\n'
    '\t\t\t//（它们的写只会因内存不足 panic，不会返回 error）。\n'
    '\t\t\treturn ""\n'
)

# ── M4：把「copy 是惯用法」豁免打坏（⇒ 16 处 copy 变成未登记）─────────
M4_OLD = (
    '\tcase "copy":\n'
    '\t\t// 依据：copy 的返回值是「复制的字节数」，error 恒为 nil（标准库惯用法）。\n'
    '\t\treturn "R7 copy（惯用法）"\n'
)
M4_NEW = (
    '\tcase "copy":\n'
    '\t\t// 依据：copy 的返回值是「复制的字节数」，error 恒为 nil（标准库惯用法）。\n'
    '\t\treturn ""\n'
)

# ── M5：把豁免规则**放宽**（把 *os.File 也当内存缓冲区）────────────────
M5_OLD = (
    '\t\tswitch s.Recv {\n'
    '\t\tcase "strings.Builder", "*strings.Builder", "bytes.Buffer", "*bytes.Buffer":\n'
)
M5_NEW = (
    '\t\tswitch s.Recv {\n'
    '\t\tcase "strings.Builder", "*strings.Builder", "bytes.Buffer", "*bytes.Buffer", "*os.File":\n'
)

# ── M6：把「错误分支」判据打坏（认不出 err != nil）────────────────────
M6_OLD = '\tif !ok || lhs.Name != "err" {\n'
M6_NEW = '\tif !ok || lhs.Name != "errX" {\n'

# ── M7：删掉回滚的 defer 兜底 ──────────────────────────────────────────
M7_OLD = '\tdefer tx.Rollback(ctx)\n'
M7_NEW = '\t// （变异：故意去掉 defer 兜底）\n'

# ── M8：WS 写失败改回丢弃 ──────────────────────────────────────────────
M8_OLD = (
    '\t\t\tif _, err := w.Write(message); err != nil {\n'
    '\t\t\t\treturn\n'
    '\t\t\t}\n'
)
M8_NEW = '\t\t\tw.Write(message)\n'

# ── M9：multipart 的 Close 改回丢弃 ────────────────────────────────────
M9_OLD = (
    '\tif err := w.Close(); err != nil {\n'
    '\t\treturn 0, "", nil, false, fmt.Errorf("close multipart writer: %w", err)\n'
    '\t}\n'
)
M9_NEW = '\tw.Close()\n'

# ── M10：响应体写（OAuth 回调）改回丢弃 ────────────────────────────────
M10_OLD = (
    '\t\tif _, err := fmt.Fprintf(w, `<html><body><h2>✅ OAuth 授权成功</h2>'
    '<p>账户 %s 已连接，请返回应用。</p></body></html>`, entry.EmailAddress); err != nil {\n'
    '\t\t\tlog.Printf("WARN: oauth callback html write failed: %v", err)\n'
    '\t\t}\n'
)
M10_NEW = (
    '\t\tfmt.Fprintf(w, `<html><body><h2>✅ OAuth 授权成功</h2>'
    '<p>账户 %s 已连接，请返回应用。</p></body></html>`, entry.EmailAddress)\n'
)

# ── M11：emit 的布尔结果改回丢弃 ───────────────────────────────────────
M11_OLD = (
    '\t\t\t\t\tif !emit(StreamDelta{Content: payload.Delta.Text}) {\n'
    '\t\t\t\t\t\tclientGone = true\n'
    '\t\t\t\t\t\treturn false\n'
    '\t\t\t\t\t}\n'
)
M11_NEW = '\t\t\t\t\temit(StreamDelta{Content: payload.Delta.Text})\n'

# ── M12：CAS 的布尔结果改回丢弃 ───────────────────────────────────────
M12_OLD = (
    '\t\tif p.version.CompareAndSwap(cur, now) {\n'
    '\t\t\treturn now\n'
    '\t\t}\n'
)
M12_NEW = (
    '\t\tp.version.CompareAndSwap(cur, now)\n'
    '\t\treturn now\n'
)

# ── M13：fn 的布尔结果改回丢弃 ─────────────────────────────────────────
M13_OLD = (
    '\tif !fn(llmbff.Delta{Done: true, FinishReason: "stop", Usage: &resp.Usage}) {\n'
    '\t\treturn &resp.Usage, nil // 客户端断开\n'
    '\t}\n'
)
M13_NEW = '\tfn(llmbff.Delta{Done: true, FinishReason: "stop", Usage: &resp.Usage})\n'

# ── M14：加一句 `defer` 的持久化写（提交会静默丢失）────────────────────
M14_OLD = (
    '\tout, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)\n'
    '\tif err != nil {\n'
    '\t\treturn err\n'
    '\t}\n'
)
M14_NEW = (
    '\tout, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)\n'
    '\tif err != nil {\n'
    '\t\treturn err\n'
    '\t}\n'
    '\tdefer out.Sync()\n'
)

# ── N1：台账的「理由」文案微调（判据不看理由文本）⇒ 必须保持绿 ───────
N1_OLD = '\t\tReason: "SMTP 探针的连接收尾，无补救动作。",\n'
N1_NEW = '\t\tReason: "SMTP 探针的连接收尾，没有补救动作。",\n'

# ── N2：豁免理由的文案微调 ⇒ 必须保持绿 ───────────────────────────────
N2_OLD = '\t\t\treturn "R1 内存缓冲区（error 恒为 nil）"\n'
N2_NEW = '\t\t\treturn "R1 内存缓冲区（error 恒 nil）"\n'

# ── N3：实现里的注释微调 ⇒ 必须保持绿 ─────────────────────────────────
N3_OLD = '\t\t// §947：Start 会因为「RSS store 不可用 / scope 不完整」而失败\n'
N3_NEW = '\t\t// §947：Start 会因为「RSS store 不可用 / scope 不完整」而失败（见下）\n'

# ── N4：台账里文件清单的**顺序**换一下（集合相等即可）⇒ 必须保持绿 ──
N4_OLD = (
    '\t\t\t"backend/internal/acchttp/client.go",\n'
    '\t\t\t"backend/internal/adapter/opencode_http.go",\n'
)
N4_NEW = (
    '\t\t\t"backend/internal/adapter/opencode_http.go",\n'
    '\t\t\t"backend/internal/acchttp/client.go",\n'
)


def body():
    rc, out = b.runner()
    b.record('基线：未变异时判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))

    # ── M1 必须红：台账条数少写一个 ───────────────────────────────
    def m1():
        b.mutate(HYGIENE, M1_OLD, M1_NEW, 'M1')
    b.case('M1 台账的条数少写一个 ⇒ 「有人偷偷删了台账行」必须红', True,
           must_contain=['台账条数与实际不符'],
           mutate_fn=m1)

    # ── M2 必须红：台账的文件集合换掉一个 ──────────────────────────
    def m2():
        b.mutate(HYGIENE, M2_OLD, M2_NEW, 'M2')
    b.case('M2 台账的文件集合里换掉一个文件 ⇒ 「新增一处在新文件里」必须红', True,
           must_contain=['台账文件集合与实际不符'],
           mutate_fn=m2)

    # ── M3 必须红：把豁免规则打坏（R1 失效）───────────────────────
    def m3():
        b.mutate(HYGIENE, M3_OLD, M3_NEW, 'M3')
    b.case('M3 把「内存缓冲区」豁免打坏 ⇒ 180 处缓冲区写入变成未登记 ⇒ 判据必须红', True,
           must_contain=['未登记的丢弃点'],
           mutate_fn=m3)

    # ── M4 必须红：把豁免规则打坏（R7 失效）───────────────────────
    def m4():
        b.mutate(HYGIENE, M4_OLD, M4_NEW, 'M4')
    b.case('M4 把「copy 是惯用法」豁免打坏 ⇒ 16 处 copy 变成未登记 ⇒ 判据必须红', True,
           must_contain=['未登记的丢弃点'],
           mutate_fn=m4)

    # ── M5 必须红：把豁免规则放宽（*os.File 也豁免）────────────────
    def m5():
        b.mutate(HYGIENE, M5_OLD, M5_NEW, 'M5')
    b.case('M5 把豁免规则放宽到 *os.File ⇒ 真会失败的写也被放过 ⇒ 判据必须红', True,
           must_contain=['豁免规则过宽'],
           mutate_fn=m5)

    # ── M6 必须红：把「错误分支」判据打坏 ──────────────────────────
    def m6():
        b.mutate(HYGIENE, M6_OLD, M6_NEW, 'M6')
    b.case('M6 把「err != nil 分支」判据打坏 ⇒ 持久化写失去兜底却看不出来 ⇒ 判据必须红', True,
           must_contain=['持久化写的失败没有兜底'],
           mutate_fn=m6)

    # ── M7 必须红：删掉回滚的 defer 兜底 ───────────────────────────
    def m7():
        b.mutate(SYNC, M7_OLD, M7_NEW, 'M7')
    b.case('M7 删掉回滚的 defer 兜底 ⇒ 回滚失败会占住连接却没人知道 ⇒ 判据必须红', True,
           must_contain=['回滚没有 defer 兜底'],
           mutate_fn=m7)

    # ── M8 必须红：WS 写失败改回丢弃 ───────────────────────────────
    def m8():
        b.mutate(MOBILE_HUB, M8_OLD, M8_NEW, 'M8')
    b.case('M8 WS 写失败改回丢弃 ⇒ 流已死却继续写 ⇒ 判据必须红', True,
           must_contain=['未登记的丢弃点'],
           mutate_fn=m8)

    # ── M9 必须红：multipart 的 Close 改回丢弃 ─────────────────────
    def m9():
        b.mutate(TRANSCRIBE, M9_OLD, M9_NEW, 'M9')
    b.case('M9 multipart 的 Close 改回丢弃 ⇒ 请求体不完整却无声 ⇒ 判据必须红', True,
           must_contain=['未登记的丢弃点'],
           mutate_fn=m9)

    # ── M10 必须红：响应体写改回丢弃 ───────────────────────────────
    def m10():
        b.mutate(OAUTH, M10_OLD, M10_NEW, 'M10')
    b.case('M10 OAuth 回调的响应体写改回丢弃 ⇒ §946 漏网的那一处复活 ⇒ 判据必须红', True,
           must_contain=['未登记的丢弃点'],
           mutate_fn=m10)

    # ── M11 必须红：emit 的布尔结果改回丢弃 ────────────────────────
    def m11():
        b.mutate(ANTHROPIC, M11_OLD, M11_NEW, 'M11')
    b.case('M11 emit 的布尔结果改回丢弃 ⇒ 客户端断开后仍为一个没人要的流读上游 ⇒ 判据必须红', True,
           must_contain=['未登记的丢弃点'],
           mutate_fn=m11)

    # ── M12 必须红：CAS 的布尔结果改回丢弃 ─────────────────────────
    def m12():
        b.mutate(PROJECTION, M12_OLD, M12_NEW, 'M12')
    b.case('M12 CAS 的布尔结果改回丢弃 ⇒ 版本号可能回退 ⇒ 判据必须红', True,
           must_contain=['未登记的丢弃点'],
           mutate_fn=m12)

    # ── M13 必须红：fn 的布尔结果改回丢弃 ──────────────────────────
    def m13():
        b.mutate(RECLAW, M13_OLD, M13_NEW, 'M13')
    b.case('M13 fn 的布尔结果改回丢弃 ⇒ 客户端断开后仍起备用通道 ⇒ 判据必须红', True,
           must_contain=['未登记的丢弃点'],
           mutate_fn=m13)

    # ── M14 必须红：加一句 defer 的持久化写 ────────────────────────
    def m14():
        b.mutate(SQLITE_RO, M14_OLD, M14_NEW, 'M14')
    b.case('M14 加一句 `defer out.Sync()` ⇒ 落盘失败会静默丢失 ⇒ 判据必须红', True,
           must_contain=['defer 了一个持久化写'],
           mutate_fn=m14)

    # ── 负控：语义等价的改动必须保持绿 ─────────────────────────────
    def n1():
        b.mutate(HYGIENE, N1_OLD, N1_NEW, 'N1')
    b.case('N1 台账的「理由」文案微调（判据不看理由文本）⇒ 必须保持绿', False,
           mutate_fn=n1)

    def n2():
        b.mutate(HYGIENE, N2_OLD, N2_NEW, 'N2')
    b.case('N2 豁免理由的文案微调 ⇒ 必须保持绿', False,
           mutate_fn=n2)

    def n3():
        b.mutate(POCKETD, N3_OLD, N3_NEW, 'N3')
    b.case('N3 实现里的注释微调 ⇒ 必须保持绿', False,
           mutate_fn=n3)

    def n4():
        b.mutate(HYGIENE, N4_OLD, N4_NEW, 'N4')
    b.case('N4 台账里文件清单的顺序换一下（集合相等即可）⇒ 必须保持绿', False,
           mutate_fn=n4)

    b.finish()


run(b, body)

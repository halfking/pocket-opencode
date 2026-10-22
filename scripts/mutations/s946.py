#!/usr/bin/env python3
"""§946：HTTP 响应体写失败不许无声 —— 编码失败不许再是「200 + 空体」。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
   **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠⚠ 本批次有**两个判据运行器**，而且**必须一起跑**：
   ① `TestS946`（repohygiene 包）—— AST 卡口（三族归零 + 形态钉住 + 自证臂）
   ② `TestS946`（server 包）      —— 端到端行为（真跑一次编码失败）
   ⇒ ⇒⇒ ★ ① 是**普查棘轮**：它量的是「响应体写出去这一族」这个**集合本身**
   ⇒ ⇒⇒ ⇒ 任何一处「把错误吃掉」的写法都会让它红。
   ⇒ ⇒⇒ ★ 而 ② 量的是**行为**（失败有没有变得可见、可区分、字节没变）。

⚠⚠ 本批次最该被记住的两条：

   ① **M13 是「把量具自己打坏」的那一条**：
      把扫描器的「哪些形参是 ResponseWriter」判据改坏 ⇒ 扫描器**什么都
      报不出来** ⇒ ⇒⇒⇒ 三族归零那条**照样绿**（扫描器什么都不报 ⇒
      「没有」成立 ⇒ 绿）⇒ ⇒⇒⇒ ⇒ 只有**自证臂**会红
      ⇒ ⇒⇒⇒ ⇒ 这就是「门绿 ≠ 量具有牙」的直接反证。

   ② **M14 暴露了一条判据的盲区**（已补）：
      「SSE 写 helper 把错误吞掉」这件事，三族归零那条**看不到**
      （调用点拿到的是 nil，不是「被丢弃的调用」）
      ⇒ ⇒⇒ ⇒ 所以补了「helper 必须把错误交回去」这条**行为**判据
      ⇒ ⇒⇒ ⇒ 以及 D 族（helper 的结果被丢弃 ⇒ 无法中断死流）。

判据命令：
  go test ./internal/repohygiene/  -run 'TestS946' -count=1
  go test ./internal/server/       -run 'TestS946' -count=1
"""
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    Batch, go_pkg_runner, run,
)

TARGETS = [
    'backend/internal/server/server_assistant.go',
    'backend/internal/server/server_migration.go',
    'backend/internal/server/session_transcript.go',
    'backend/internal/server/sse_write.go',
    'backend/internal/server/server_llmbff.go',
    'backend/internal/server/server_email_invoice_file.go',
    'backend/internal/repohygiene/response_write_test.go',
]

_RUNNERS = {}


def runners():
    if not _RUNNERS:
        _RUNNERS['repohygiene'] = go_pkg_runner('./internal/repohygiene/', 'TestS946')
        _RUNNERS['server'] = go_pkg_runner('./internal/server/', 'TestS946')
    return _RUNNERS


def combined():
    """两个包的判据都跑 ⇒ 任一红就算红（收尾时两者都必须绿）。"""
    r = runners()
    rc_total = 0
    chunks = []
    for key in ('repohygiene', 'server'):
        rc, out = r[key]()
        rc_total = rc_total or rc
        chunks.append('===== %s =====\n%s' % (key, out))
    return rc_total, '\n\n'.join(chunks)


b = Batch('§946 HTTP 响应体写失败不许无声（编码失败从「200 + 空体」改成可区分的 500）',
          combined, TARGETS)
ASSISTANT_GO = b.targets[0]
MIGRATION_GO = b.targets[1]
TRANSCRIPT_GO = b.targets[2]
SSE_GO = b.targets[3]
LLMBFF_GO = b.targets[4]
INVOICE_GO = b.targets[5]
HYGIENE_GO = b.targets[6]

# ── 变异的锚点（必须与实现**逐字**一致；不一致 ⇒ 落盘自证会失败并中止）──

# M1：把「先编码、后写状态」倒回旧顺序（WriteHeader 提到 Marshal 之前）
#   ⇒ ⇒⇒ 编码失败时状态行已经提交 ⇒ 客户端拿到的又是「200 + 空体」
#   ⇒ ⇒⇒ ⇒ 行为判据（状态码不是 500）与形态判据（顺序）**双红**。
M1_OLD = '''func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeJSONEncodeFailure(w, status, err)
		return
	}
	// ⚠ 补回 Encoder 的那个换行 ⇒ 字节与旧实现逐字一致（见上面的「字节兼容」）。
	body = append(body, '\\n')
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
'''
M1_NEW = '''func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, err := json.Marshal(v)
	if err != nil {
		writeJSONEncodeFailure(w, status, err)
		return
	}
	// ⚠ 补回 Encoder 的那个换行 ⇒ 字节与旧实现逐字一致（见上面的「字节兼容」）。
	body = append(body, '\\n')
'''

# M2：兜底体写成**空** ⇒ 客户端拿到的还是空体（只是状态码变了）
M2_OLD = '''	body := `{"error":"response_encode_failed: failed to encode JSON response body",` +
		`"code":"response_encode_failed","retryable":false}` + "\\n"
'''
M2_NEW = '''	body := ``
'''

# M3：兜底体**不带**稳定错误码 ⇒ 前端分不出「服务端自己没写出响应」
M3_OLD = '''		`"code":"response_encode_failed","retryable":false}` + "\\n"
'''
M3_NEW = '''		`"retryable":false}` + "\\n"
'''

# M4：兜底体说「可重试」⇒ 客户端盲目重试可能造成重复副作用
M4_OLD = '''		`"code":"response_encode_failed","retryable":false}` + "\\n"
'''
M4_NEW = '''		`"code":"response_encode_failed","retryable":true}` + "\\n"
'''

# M5：兜底体的 error 文案**不带** `code:` 前缀 ⇒ 前端 extractErrorCode 取不到码
M5_OLD = '''	body := `{"error":"response_encode_failed: failed to encode JSON response body",` +
'''
M5_NEW = '''	body := `{"error":"failed to encode JSON response body",` +
'''

# M6：兜底体**改用编码器** ⇒ 编码失败的原因可能就是值本身不可编码
#   ⇒ ⇒⇒ 兜底体自己也会失败 ⇒ 必须是常量字节。
M6_OLD = '''	body := `{"error":"response_encode_failed: failed to encode JSON response body",` +
		`"code":"response_encode_failed","retryable":false}` + "\\n"
'''
M6_NEW = '''	body, _ := json.Marshal(map[string]any{
		"error":     "response_encode_failed: failed to encode JSON response body",
		"code":      "response_encode_failed",
		"retryable": false,
	})
	body = append(body, '\\n')
'''

# M7：writeMigrationJSON **不再委托**（自己写一遍旧的）
#   ⇒ ⇒⇒ 「先编码后写状态」那条规则**没有覆盖它** ⇒ 委托判据必须红。
M7_OLD = '''func writeMigrationJSON(w http.ResponseWriter, status int, v any) {
	writeJSON(w, status, v)
}
'''
M7_NEW = '''func writeMigrationJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
'''

# M8：writeSessionJSON 委托了却**自己**还提交响应 ⇒ 顺序保证被绕过
M8_OLD = '''func writeSessionJSON(w http.ResponseWriter, v any) {
	writeJSON(w, http.StatusOK, v)
}
'''
M8_NEW = '''func writeSessionJSON(w http.ResponseWriter, v any) {
	w.WriteHeader(http.StatusOK)
	writeJSON(w, http.StatusOK, v)
}
'''

# M9：writeKxmemoryError 同样绕过
M9_OLD = '''	writeJSON(w, status, map[string]any{
		"error":     kxe.Error(),
		"code":      kxe.Code,
		"retryable": kxe.Retryable(),
	})
'''
M9_NEW = '''	w.WriteHeader(status)
	writeJSON(w, status, map[string]any{
		"error":     kxe.Error(),
		"code":      kxe.Code,
		"retryable": kxe.Retryable(),
	})
'''

# M10：writeJSON 少写那个换行 ⇒ 响应体字节变了（§946 实测：Encoder 会加换行）
M10_OLD = """	body = append(body, '\\n')
"""
M10_NEW = ''

# M11：写失败**不再留痕** ⇒ 客户端拿到截断响应而服务端零记录
M11_OLD = '''	if _, err := w.Write(body); err != nil {
		// ⚠ 写失败（客户端断开 / 连接被重置）**没法再改状态行**了 ⇒
		//　 只能留痕。旧实现把这个错误**整个吞掉** ⇒ 「客户端收到截断响应」
		//　 而服务端**没有任何记录**。
		log.Printf("[WARN] writeJSON: 响应体写出失败（status=%d）：%v", status, err)
	}
'''
M11_NEW = '''	if _, err := w.Write(body); err != nil {
		_ = err
	}
'''

# M12：**把扫描器打坏**（裸语句形态认不出来）
#   ⇒ ⇒⇒⇒ 自证臂（合成源码那条）必须红 ⇒ ⇒⇒⇒ 「门绿 ≠ 量具有牙」的直接反证。
M12_OLD = '''	case *ast.ExprStmt:
		call, ok := st.X.(*ast.CallExpr)
		if !ok {
			return nil
		}
		return []*ast.CallExpr{call}
'''
M12_NEW = '''	case *ast.ExprStmt:
		call, ok := st.X.(*ast.CallExpr)
		if !ok {
			return nil
		}
		_ = call
		return nil
'''

# M13：**把扫描器打坏**（认不出哪些形参是 ResponseWriter）
#   ⇒ ⇒⇒⇒ 扫描器**什么都报不出来** ⇒ 三族归零那条**照样绿**
#   ⇒ ⇒⇒⇒ ⇒ 只有自证臂会红 ⇒ ⇒⇒⇒ ⇒ 这条变异专门证明「自证臂不可省」。
M13_OLD = '''		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "http" || sel.Sel.Name != "ResponseWriter" {
			continue
		}
'''
M13_NEW = '''		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "http" || sel.Sel.Name != "ResponseWriterBROKEN" {
			continue
		}
'''

# M14：SSE 写 helper **把错误吞掉**（返回 nil）
#   ⇒ ⇒⇒ 调用方的 `if err != nil { return }` 永远不成立 ⇒ 死流会一直写。
M14_OLD = '''	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		log.Printf("[WARN] SSE 事件写出失败（客户端可能已断开）：%v", err)
		return err
	}
'''
M14_NEW = '''	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		log.Printf("[WARN] SSE 事件写出失败（客户端可能已断开）：%v", err)
		return nil
	}
'''

# M15：某个 SSE 调用点**丢掉** helper 的返回值 ⇒ D 族必须红
M15_OLD = '''		if wErr := s946SSEWritef(w, "data: %s\\n\\n", payload); wErr != nil {
			// ⚠ §946：SSE 写失败 = 客户端已断开 ⇒ 这条流已经死了 ⇒ 中断。
			abort = true
			return false
		}
'''
M15_NEW = '''		s946SSEWritef(w, "data: %s\\n\\n", payload)
'''

# M16：改**稳定错误码**的字面值 ⇒ 前端按它做映射 ⇒ 改了等于改契约
M16_OLD = 'const s946EncodeFailureCode = "response_encode_failed"\n'
M16_NEW = 'const s946EncodeFailureCode = "response_encode_failed_v2"\n'

# M17：某处响应体写出**改回丢弃错误** ⇒ B 族必须红
M17_OLD = '''	if _, err := w.Write(data); err != nil {
		log.Printf("[WARN] 发票文件响应体写出失败：%v", err)
	}
'''
M17_NEW = '''	_, _ = w.Write(data)
'''

# ── 负控的锚点（语义等价 ⇒ 必须保持绿）────────────────────────────────

# N1：日志的格式化从 %v 换成 %s（error 的文本一致）⇒ 等价。
N1_OLD = '\t\tlog.Printf("[WARN] writeJSON: 响应体写出失败（status=%d）：%v", status, err)\n'
N1_NEW = '\t\tlog.Printf("[WARN] writeJSON: 响应体写出失败（status=%d）：%s", status, err)\n'

# N2：换行写成 `byte(10)`（与 `'\\n'` 逐字等价）⇒ 响应体字节不变 ⇒ 必须绿。
N2_OLD = """	body = append(body, '\\n')
"""
N2_NEW = """	body = append(body, byte(10))
"""

# N3：兜底体里两个 JSON 键**换顺序**（语义等价）⇒ 必须保持绿。
N3_OLD = '''		`"code":"response_encode_failed","retryable":false}` + "\\n"
'''
N3_NEW = '''		`"retryable":false,"code":"response_encode_failed"}` + "\\n"
'''

# N4：SSE 日志文案微调（判据只看有没有 "SSE"）⇒ 等价。
N4_OLD = '\t\tlog.Printf("[WARN] SSE 事件写出失败（客户端可能已断开）：%v", err)\n'
N4_NEW = '\t\tlog.Printf("[WARN] SSE 事件写出失败（客户端可能已经断开）：%v", err)\n'


def body():
    rc, out = combined()
    b.record('基线：未变异时判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))

    # ── M1 必须红：顺序倒回「先写状态后编码」─────────────────────────
    def m1():
        b.mutate(ASSISTANT_GO, M1_OLD, M1_NEW, 'M1')
    b.case('M1 writeJSON 把 WriteHeader 提到编码之前 ⇒ 客户端又拿到「200 + 空体」⇒ 判据必须红', True,
           must_contain=['发生在编码'],
           mutate_fn=m1)

    # ── M2 必须红：兜底体写成空 ─────────────────────────────────────
    def m2():
        b.mutate(ASSISTANT_GO, M2_OLD, M2_NEW, 'M2')
    b.case('M2 编码失败的兜底体是**空的** ⇒ 客户端仍拿到空体 ⇒ 判据必须红', True,
           must_contain=['空的'],
           mutate_fn=m2)

    # ── M3 必须红：兜底体不带稳定错误码 ───────────────────────────
    def m3():
        b.mutate(ASSISTANT_GO, M3_OLD, M3_NEW, 'M3')
    b.case('M3 兜底体**不带**稳定错误码 ⇒ 前端分不出「服务端自己没写出响应」⇒ 判据必须红', True,
           must_contain=['兜底响应体的 code'],
           mutate_fn=m3)

    # ── M4 必须红：兜底体说「可重试」──────────────────────────────
    def m4():
        b.mutate(ASSISTANT_GO, M4_OLD, M4_NEW, 'M4')
    b.case('M4 兜底体说「可重试」⇒ 客户端盲目重试可能造成重复副作用 ⇒ 判据必须红', True,
           must_contain=['retryable'],
           mutate_fn=m4)

    # ── M5 必须红：error 文案不带 code: 前缀 ──────────────────────
    def m5():
        b.mutate(ASSISTANT_GO, M5_OLD, M5_NEW, 'M5')
    b.case('M5 兜底体的 error 文案不带 `code:` 前缀 ⇒ 前端取不到码 ⇒ 判据必须红', True,
           must_contain=['期望以'],
           mutate_fn=m5)

    # ── M6 必须红：兜底体改用编码器 ───────────────────────────────
    def m6():
        b.mutate(ASSISTANT_GO, M6_OLD, M6_NEW, 'M6')
    b.case('M6 兜底体**改用编码器** ⇒ 兜底体自己也会失败 ⇒ 判据必须红', True,
           must_contain=['编码器'],
           mutate_fn=m6)

    # ── M7 必须红：writeMigrationJSON 不再委托 ─────────────────────
    def m7():
        b.mutate(MIGRATION_GO, M7_OLD, M7_NEW, 'M7')
    b.case('M7 writeMigrationJSON 不再委托给 writeJSON ⇒ 顺序规则没覆盖它 ⇒ 判据必须红', True,
           must_contain=['委托'],
           mutate_fn=m7)

    # ── M8 必须红：writeSessionJSON 自己提交响应 ──────────────────
    def m8():
        b.mutate(TRANSCRIPT_GO, M8_OLD, M8_NEW, 'M8')
    b.case('M8 writeSessionJSON 委托了却自己还提交响应 ⇒ 顺序保证被绕过 ⇒ 判据必须红', True,
           must_contain=['还提交响应'],
           mutate_fn=m8)

    # ── M9 必须红：writeKxmemoryError 自己提交响应 ────────────────
    def m9():
        b.mutate(ASSISTANT_GO, M9_OLD, M9_NEW, 'M9')
    b.case('M9 writeKxmemoryError 委托了却自己还提交响应 ⇒ 顺序保证被绕过 ⇒ 判据必须红', True,
           must_contain=['还提交响应'],
           mutate_fn=m9)

    # ── M10 必须红：少写那个换行 ─────────────────────────────────
    def m10():
        b.mutate(ASSISTANT_GO, M10_OLD, M10_NEW, 'M10')
    b.case('M10 writeJSON 少写那个换行 ⇒ 响应体字节变了 ⇒ 判据必须红', True,
           must_contain=['字节变了'],
           mutate_fn=m10)

    # ── M11 必须红：写失败不再留痕 ───────────────────────────────
    def m11():
        b.mutate(ASSISTANT_GO, M11_OLD, M11_NEW, 'M11')
    b.case('M11 响应体写失败不再留痕 ⇒ 客户端拿到截断响应而服务端零记录 ⇒ 判据必须红', True,
           must_contain=['没有留痕'],
           mutate_fn=m11)

    # ── M12 必须红：把扫描器打坏（认不出裸语句）──────────────────
    def m12():
        b.mutate(HYGIENE_GO, M12_OLD, M12_NEW, 'M12')
    b.case('M12 把扫描器打坏（裸语句形态认不出来）⇒ 自证臂必须红', True,
           must_contain=['期望 7 处'],
           mutate_fn=m12)

    # ── M13 必须红：把扫描器打坏（认不出 ResponseWriter 形参）──────
    # ⚠ 这一条最该被记住：三族归零那条在 M13 下**照样绿**
    #   （扫描器什么都报不出来 ⇒ 「没有」成立 ⇒ 绿）⇒ ⇒⇒⇒ 只有自证臂会红。
    def m13():
        b.mutate(HYGIENE_GO, M13_OLD, M13_NEW, 'M13')
    b.case('M13 把扫描器打坏（认不出 ResponseWriter 形参）⇒ 三族归零照样绿 ⇒ 只有自证臂会红', True,
           must_contain=['期望 7 处'],
           mutate_fn=m13)

    # ── M14 必须红：SSE helper 把错误吞掉 ─────────────────────────
    def m14():
        b.mutate(SSE_GO, M14_OLD, M14_NEW, 'M14')
    b.case('M14 SSE 写 helper 把错误吞掉（返回 nil）⇒ 调用方无法中断死流 ⇒ 判据必须红', True,
           must_contain=['无法中断'],
           mutate_fn=m14)

    # ── M15 必须红：SSE 调用点丢掉 helper 的返回值 ────────────────
    def m15():
        b.mutate(LLMBFF_GO, M15_OLD, M15_NEW, 'M15')
    b.case('M15 SSE 调用点丢掉 helper 的返回值 ⇒ 无法中断死流 ⇒ D 族必须红', True,
           must_contain=['不许无声'],
           mutate_fn=m15)

    # ── M16 必须红：改稳定错误码的字面值 ─────────────────────────
    def m16():
        b.mutate(ASSISTANT_GO, M16_OLD, M16_NEW, 'M16')
    b.case('M16 改稳定错误码的字面值 ⇒ 前端按它做映射 ⇒ 改了等于改契约 ⇒ 判据必须红', True,
           must_contain=['稳定错误码常量'],
           mutate_fn=m16)

    # ── M17 必须红：响应体写出改回丢弃错误 ───────────────────────
    def m17():
        b.mutate(INVOICE_GO, M17_OLD, M17_NEW, 'M17')
    b.case('M17 某处响应体写出改回丢弃错误 ⇒ B 族必须红', True,
           must_contain=['不许无声'],
           mutate_fn=m17)

    # ── 负控：语义等价的改动必须保持绿 ───────────────────────────
    def n1():
        b.mutate(ASSISTANT_GO, N1_OLD, N1_NEW, 'N1')
    b.case('N1 日志格式化 %v → %s（等价）⇒ 必须保持绿', False,
           mutate_fn=n1)

    def n2():
        b.mutate(ASSISTANT_GO, N2_OLD, N2_NEW, 'N2')
    b.case('N2 换行写成 byte(10)（逐字等价）⇒ 响应体字节不变 ⇒ 必须保持绿', False,
           mutate_fn=n2)

    def n3():
        b.mutate(ASSISTANT_GO, N3_OLD, N3_NEW, 'N3')
    b.case('N3 兜底体两个 JSON 键换顺序（语义等价）⇒ 必须保持绿', False,
           mutate_fn=n3)

    def n4():
        b.mutate(SSE_GO, N4_OLD, N4_NEW, 'N4')
    b.case('N4 SSE 日志文案微调（等价）⇒ 必须保持绿', False,
           mutate_fn=n4)

    b.finish()


# ⚠⚠ §958：**必须有 `__main__` 守卫**（其余 24 批都有，本批原先漏了）。
#   漏了它 ⇒ **任何 import 本模块的动作都会真的跑完整批变异**（改工作树！）
#   ⇒ ⇒ 本轮实测踩到：写「锚点新鲜度检查器」时用 import 方式加载批次
#   ⇒ ⇒ import s946 ⇒ **它自己跑了起来** ⇒ 检查器被 SIGKILL（300 s 超时）
#   ⇒ ⇒ 还原没跑完 ⇒ **工作树留下 M4 的残留**（`retryable:false` 被改成 `true`）
#   ⇒ ⇒ ⇒ 后面的检查器把这份残留读成「s946 的锚点过期」⇒ **假结论**。
#   ⇒ ⇒⇒ ⇒ 纪律：**不要 import 这些脚本**（要读它们的锚点就用 AST 静态解析）；
#   ⇒⇒⇒ ⇒ 且每个批次脚本都必须有这道守卫 ⇒ 否则「读一下」就会**改工作树**。
if __name__ == '__main__':
    run(b, body)

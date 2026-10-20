#!/usr/bin/env python3
"""§935 · §919：重建并重跑「服务端字节闸的调用方登记簿 + 跨语言闸值」那批变异。

⚠⚠ 为什么是**重建**而不是「重跑」：
   §919 当时的变异脚本**没有落盘**（提交信息与主文档只留了汇总）⇒ ⇒ 本脚本是
   **按 §919 的判据描述重建的等价批次** ⇒ ⇒ 它能证明「**判据现在还有牙**」，
   但**证明不了「当时那批脚本真的跑过」** ⇒ ⇒ 这个区别在主文档里如实写明，
   且**不可弥补**（历史读数没有第二份副本）。

判据命令：
  · 前端  node --experimental-strip-types --test --test-name-pattern=§919 \\
            src/api/__tests__/summary-fallback-local-shape.test.ts
  · Go    go test ./internal/server/ -run TestLLMChatLengthGateConstantMatchesFrontend
"""
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import Batch, HarnessError, node_runner, go_runner, run  # noqa: E402

TARGETS = [
    # 前端
    'frontend/src/api/__tests__/summary-fallback-local-shape.test.ts',
    'frontend/src/features/meetings/meetings-ai.ts',
    'frontend/src/features/meetings/live-translate.ts',
    'frontend/src/features/email/translate-email.ts',
    'frontend/src/features/email/compose-mode.ts',
    'frontend/src/features/notes/note-search.ts',
    'frontend/src/features/notes/notes-persist.ts',
    'frontend/src/api/meetings.ts',
    # 后端
    'backend/internal/server/server_llmbff.go',
    'backend/internal/server/server_assistant.go',
    'backend/internal/server/server_llm_chat_test.go',
]

FRONT = node_runner('src/api/__tests__/summary-fallback-local-shape.test.ts', '§919')
GO_XLANG = go_runner('TestLLMChatLengthGateConstantMatchesFrontend')
GO_REGISTRY = go_runner('TestLengthLimitRegistryCoversEveryLengthGate')

b = Batch('§919 登记簿 / 字节闸 / 跨语言门', FRONT, TARGETS)

REG = b.targets[0]        # 判据文件（登记簿在它里面）
MEET_AI = b.targets[1]
LIVE_TR = b.targets[2]
TRANS = b.targets[3]
COMPOSE = b.targets[4]
NSEARCH = b.targets[5]
NPERSIST = b.targets[6]
MEETINGS_API = b.targets[7]
LLMBFF = b.targets[8]
ASSISTANT = b.targets[9]
GO_TEST = b.targets[10]


def body():
    # ── 基线 ────────────────────────────────────────────────────────
    rc, out = FRONT()
    b.record('基线：§919 前端判据必须绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-800:]))
    rc, out = GO_XLANG()
    b.record('基线：§919 Go 跨语言门必须绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-500:]))

    # ── A1 新增一个**未登记**的调用点 ⇒ 「每个调用点都必须登记」红 ──
    def a1():
        b.mutate(NPERSIST,
                 "\nfunction newNoteId(): string {",
                 "\n// ⚠ 变异 A1：新增一个未登记的 LLM 调用点（真实场景：有人加了调用却忘了登记）\n"
                 "export async function s919MutProbeChat(): Promise<void> {\n"
                 "  await http('/api/llm/chat', { method: 'POST', body: '{}' })\n"
                 "}\n\nfunction newNoteId(): string {", 'A1')
    b.case('A1 前端新增一个未登记的调用点 ⇒ 登记簿门当场红并点名', True,
           must_contain=['两者必须**恰好**相等'], mutate_fn=a1)

    # ── A2 已登记的调用点被改名 ⇒ 登记簿那一行过期 ⇒ 红 ────────────
    def a2():
        b.mutate(LIVE_TR, "kind: 'live_translate',", "kind: 'live_translate_v2',", 'A2')
    b.case('A2 已登记的调用点被改名（kind 改了）⇒ 登记簿过期，红', True,
           must_contain=['找不到对应的调用点了'], mutate_fn=a2)

    # ── A3 绕过封装裸写 fetch ⇒ 「真正出口数」红 ────────────────────
    def a3():
        b.mutate(NSEARCH,
                 "export interface NoteSearchBriefing {",
                 "// ⚠ 变异 A3：绕过 llmBffApi 封装裸写 fetch\n"
                 "export async function s919MutProbeRawFetch(): Promise<void> {\n"
                 "  await fetch(`/api/llm/stream`, { method: 'POST' })\n"
                 "}\n\n"
                 "export interface NoteSearchBriefing {", 'A3')
    b.case('A3 绕过封装裸写 fetch ⇒ 真正出口数门红并报出实际数', True,
           must_contain=['真正出口数'], mutate_fn=a3)

    # ── A4 §919 的本体：按**字符**裁而不是按**字节**裁 ⇒ 红 ────────
    def a4():
        b.mutate(MEET_AI,
                 "t = truncateUtf8BytesTail(t, Math.max(0, gate - sysBytes - utf8ByteLength(TRANSCRIPT_TRUNCATED_NOTICE)))",
                 "t = t.slice(-Math.max(0, gate - sysBytes - utf8ByteLength(TRANSCRIPT_TRUNCATED_NOTICE)))",
                 'A4')
    b.case('A4 裁剪改成「按字符取尾」（slice）⇒ 字节闸失守，红', True,
           must_contain=['必然 400'], mutate_fn=a4)

    # ── A5 裁**头**而不是裁**尾** ⇒ 「必须保留尾部」红 ──────────────
    def a5():
        b.mutate(MEET_AI,
                 "t = truncateUtf8BytesTail(t, Math.max(0, gate - sysBytes - utf8ByteLength(TRANSCRIPT_TRUNCATED_NOTICE)))",
                 "t = truncateUtf8BytesHead(t, Math.max(0, gate - sysBytes - utf8ByteLength(TRANSCRIPT_TRUNCATED_NOTICE)))",
                 'A5')
    b.case('A5 裁剪改成取**头部** ⇒ 会议结论/行动项会被丢掉，红', True,
           must_contain=['裁剪没有保留尾部'], mutate_fn=a5)

    # ── A6 裁了但**不说明** ⇒ 模型会把片段当整场 ⇒ 红 ──────────────
    def a6():
        b.mutate(MEET_AI, "的**尾部**片段", "的**片段**", 'A6')
    b.case('A6 裁了转写却不告诉模型「被截断了」⇒ 红', True,
           must_contain=['裁了转写却没告诉模型内容被截断'], mutate_fn=a6)

    # ── A7 邮件翻译的硬切上限被调大 ⇒ 某块提示词必然超闸 ⇒ 红 ──────
    def a7():
        b.mutate(TRANS, r"para.match(/[\s\S]{1,4000}/g)", r"para.match(/[\s\S]{1,20000}/g)", 'A7')
    b.case('A7 邮件翻译硬切上限 4000→20000 码元 ⇒ 某块提示词必然超闸，红', True,
           must_contain=['必然 400'], mutate_fn=a7)

    # ── A8 note-search 的码元上限被调大 ⇒ 最坏字节数离闸没余量 ⇒ 红 ──
    def a8():
        b.mutate(NSEARCH, "content.slice(0, 4000),", "content.slice(0, 60000),", 'A8')
    b.case('A8 note-search 的 4000 码元上限→60000 ⇒ 离闸余量不足，红', True,
           must_contain=['余量不足 25%'], mutate_fn=a8)

    # ── A9 邮件主题不再按字节裁 ⇒ 长主题永远发不出去 ⇒ 红 ──────────
    def a9():
        b.mutate(COMPOSE, "export const EMAIL_SUBJECT_BYTE_LIMIT = 500",
                 "export const EMAIL_SUBJECT_BYTE_LIMIT = 999999", 'A9')
    b.case('A9 邮件主题的字节上限被放大 ⇒ 主题过不了服务端闸，红', True,
           must_contain=['永远发不出去'], mutate_fn=a9)

    # ── A10 登记簿的「retest 声明」与「实现」脱钩 ⇒ 红 ────────────
    def a10():
        b.mutate(REG, "      retest: null,   // §917 的行为判据已钉",
                 "      retest: 'mut-not-implemented',   // §917 的行为判据已钉", 'A10')
    b.case('A10 登记簿声明要重测但没实现（空头支票）⇒ 红', True,
           must_contain=['完全一致'], mutate_fn=a10)

    # ── A11（负控）只改登记簿的 bound 措辞 ⇒ 必须保持绿 ─────────────
    def a11():
        b.mutate(REG,
                 "bound: '§917 的 splitForEmbedding + capChunksByBytes ⇒ 每一块 ≤ 16000 字节',",
                 "bound: '§917 的 splitForEmbedding + capChunksByBytes ⇒ 每一块 ≤ 16000 字节（措辞改动，语义不变）',",
                 'A11')
    b.case('A11（负控）只改登记簿 bound 的措辞（语义不变）⇒ 必须保持绿', False,
           must_not_contain=['登记簿里匹配到 0 行', '找不到对应的调用点了'], mutate_fn=a11)

    # ── A12 服务端那道闸值漂了 ⇒ Go 跨语言门红 ─────────────────────
    def a12():
        b.mutate(LLMBFF, "if len(m.Content) > 32000 {", "if len(m.Content) > 16000 {", 'A12')
    b.case('A12 服务端 /api/llm/stream 的闸值 32000→16000 ⇒ 跨语言门红', True,
           must_contain=['两处服务端闸不一致'], mutate_fn=a12, runner=GO_XLANG)

    # ── A13 同一处再出现第二道同形闸 ⇒ 「恰好 1 处」口径红 ─────────
    def a13():
        before = LLMBFF.read_text(encoding='utf-8')
        LLMBFF.write_text(before + (
            '\n// ⚠ 变异 A13：同形的第二道闸（跨语言门要求「恰好 1 处」）\n'
            'func mutA13Probe(m struct{ Content string }) bool {\n'
            '\treturn len(m.Content) > 32000\n}\n'), encoding='utf-8')
        on_disk = LLMBFF.read_text(encoding='utf-8')
        if on_disk == before or 'mutA13Probe' not in on_disk:
            raise HarnessError('A13 落盘自证失败')
    b.case('A13 同一 handler 出现第二道同形闸 ⇒ 「恰好 1 处」口径红', True,
           must_contain=['匹配到 2 处'], mutate_fn=a13, runner=GO_XLANG)

    # ── A14（负控）四处一起改 ⇒ 跨语言门仍绿（它只管「相等」）───────
    def a14():
        b.mutate(ASSISTANT, "if len(m.Content) > 32000 {", "if len(m.Content) > 16000 {", 'A14-a')
        b.mutate(LLMBFF, "if len(m.Content) > 32000 {", "if len(m.Content) > 16000 {", 'A14-b')
        b.mutate(MEETINGS_API, "const LLM_CHAT_MAX_CONTENT_BYTES = 32000",
                 "const LLM_CHAT_MAX_CONTENT_BYTES = 16000", 'A14-c')
        b.mutate(MEET_AI, "const LLM_STREAM_MAX_CONTENT_BYTES = 32000",
                 "const LLM_STREAM_MAX_CONTENT_BYTES = 16000", 'A14-d')
    b.case('A14（负控）四处闸值**一起**改 ⇒ 跨语言门仍绿（它只管相等）', False,
           must_not_contain=['两处服务端闸不一致', '≠ 服务端'], mutate_fn=a14, runner=GO_XLANG)

    # ── A15 交叉验证：同一处改动下，§921 的清单门必须红（绝对值有牙）─
    b.case('A15 交叉验证：同一处闸值改动 ⇒ §921 的清单门（键含表达式文本）红', True,
           must_contain=['源码里新增了一处长度限制', 'len(m.Content) > 16000'],
           mutate_fn=a14, runner=GO_REGISTRY)

    # ── A16（量具自检·**不是**「有牙」证据）────────────────────────
    #   ⚠ 这条量的是**判据自己的盲区**：Go 侧读前端源码用的是 readFrontend，
    #     读不到时是 `t.Skipf` ⇒ 测试被**跳过** ⇒ `go test` 退出码仍是 0
    #     ⇒ **绿得毫无意义**（空气门）。
    #   ⇒ 实测它确实会这样（把路径改成一个不存在的文件）⇒ 记为**已知局限**。
    #   ⇒ 现状（实测）：CI 的 backend job 是 `actions/checkout@v4` **整仓 checkout**
    #     ⇒ 前端树一定在 ⇒ 这条 skip 今天不会触发。
    def a16():
        b.mutate(GO_TEST, '"../../../frontend/src/api/meetings.ts"',
                 '"../../../frontend/src/api/mut-not-exists.ts"', 'A16-a')
        b.mutate(GO_TEST, '"../../../frontend/src/features/meetings/meetings-ai.ts"',
                 '"../../../frontend/src/features/meetings/mut-not-exists.ts"', 'A16-b')
    b.case('A16（量具自检）前端树缺失时 ⇒ 跨语言门被**跳过**（空气门），退出码仍 0', False,
           must_not_contain=['FAIL'], mutate_fn=a16, runner=GO_XLANG)


if __name__ == '__main__':
    run(b, body, extra_runners=(GO_XLANG,))

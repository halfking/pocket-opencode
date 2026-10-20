#!/usr/bin/env python3
"""§935 · §920：重建并重跑「精校兜底超字节闸时必须出声」那批变异。

⚠⚠ 为什么是**重建**而不是「重跑」：§920 当时的变异脚本**没有落盘**
（提交信息与主文档只留了汇总）⇒ ⇒ 本脚本是**按 §920 的判据描述重建的等价批次**
⇒ ⇒ 它能证明「**判据现在还有牙**」，但**证明不了「当时那批脚本真的跑过」**
⇒ ⇒ 这个区别在主文档里如实写明。

判据命令：
  node --experimental-strip-types --test --test-name-pattern=§920 \\
        src/api/__tests__/summary-fallback-local-shape.test.ts
"""
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import Batch, node_runner, run  # noqa: E402

TARGETS = [
    'frontend/src/api/meetings.ts',
    'frontend/src/features/meetings/refine-prompt.ts',
    'frontend/src/features/meetings/meeting-dedup.ts',
]

FRONT = node_runner('src/api/__tests__/summary-fallback-local-shape.test.ts', '§920')

b = Batch('§920 精校兜底出声', FRONT, TARGETS)
MEETINGS = b.targets[0]


def body():
    rc, out = FRONT()
    b.record('基线：§920 判据必须绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-800:]))

    # ── B1 warn 里的闸值被写错（32000 → 16000）⇒ 红 ────────────────
    def b1():
        b.mutate(MEETINGS, "> 闸 ${LLM_CHAT_MAX_CONTENT_BYTES} 字节", "> 闸 16000 字节", 'B1')
    b.case('B1 warn 里的闸值被写错（32000→16000）⇒ 红', True,
           must_contain=['既不是实测字节数'], mutate_fn=b1)

    # ── B2 同一条 warn 里**并存**一个错值 ⇒ 红 ─────────────────────
    def b2():
        b.mutate(MEETINGS,
                 "` ⇒ 这条请求**必然 400**（转写约 ${(promptBytes / 844).toFixed(1)} 分钟；` +",
                 "` ⇒ 这条请求**必然 400**（转写约 ${(promptBytes / 844).toFixed(1)} 分钟；"
                 "另有一道闸 16384 字节；` +", 'B2')
    b.case('B2 同一条 warn 里并存第二个闸值 ⇒ 红（开发者会读到矛盾说明）', True,
           must_contain=['既不是实测字节数'], mutate_fn=b2)

    # ── B3（负控）同一条 warn 里**多写一遍正确的 32000** ⇒ 必须绿 ──
    def b3():
        b.mutate(MEETINGS,
                 "` ⇒ 这条请求**必然 400**（转写约 ${(promptBytes / 844).toFixed(1)} 分钟；` +",
                 "` ⇒ 这条请求**必然 400**（转写约 ${(promptBytes / 844).toFixed(1)} 分钟；"
                 "闸 32000 字节；` +", 'B3')
    b.case('B3（负控）warn 里多写一遍**正确**的 32000（语义等价）⇒ 必须保持绿', False,
           must_not_contain=['既不是实测字节数', '闸 32000 字节'], mutate_fn=b3)

    # ── B4 warn 的字节数报成了**闸值**而不是实测值 ⇒ 口径自证红 ────
    def b4():
        b.mutate(MEETINGS, "兜底提示词 ${promptBytes} 字节", "兜底提示词 ${LLM_CHAT_MAX_CONTENT_BYTES} 字节", 'B4')
    b.case('B4 warn 报的字节数换成了闸值（不是实测）⇒ 口径自证红', True,
           must_contain=['warn 报的字节数与实际发出去的'], mutate_fn=b4)

    # ── B5 闸内也出声 ⇒ 「闸内不得出声」负控红 ─────────────────────
    def b5():
        b.mutate(MEETINGS, "if (promptBytes > LLM_CHAT_MAX_CONTENT_BYTES) {",
                 "if (promptBytes >= 0) {", 'B5')
    b.case('B5 闸内也出声（warn 变成噪声）⇒ 负控红', True,
           must_contain=['闸内却出声了'], mutate_fn=b5)

    # ── B6 降级行为被改（发出去的 content 多了东西）⇒ 红 ──────────
    def b6():
        b.mutate(MEETINGS, "          content: prompt,", "          content: prompt + ' ',", 'B6')
    b.case('B6 兜底发出去的 content 与提示词不再逐字一致 ⇒ 红', True,
           must_contain=['兜底发出去的 content 与'], mutate_fn=b6)

    # ── B7 失败形态：fromFallback 不置位 ⇒ 红 ──────────────────────
    def b7():
        b.mutate(MEETINGS,
                 "    todos: normalizeActionItems(parsed.todos),\n    fromFallback: true,",
                 "    todos: normalizeActionItems(parsed.todos),\n    fromFallback: false,", 'B7')
    b.case('B7 兜底产物不置 fromFallback ⇒ 界面无从区分「没精校」⇒ 红', True,
           must_contain=['兜底产物没有置 fromFallback'], mutate_fn=b7)

    # ── B8 retentionReason 变了 ⇒ 红 ──────────────────────────────
    def b8():
        b.mutate(MEETINGS,
                 "...(retention.ok ? {} : { retentionReason: retention.reason }),",
                 "...(retention.ok ? {} : { retentionReason: 'mut-other-reason' }),", 'B8')
    b.case('B8 retentionReason 被改 ⇒ 用户侧文案与事实脱节 ⇒ 红', True,
           must_contain=['retentionReason 变了'], mutate_fn=b8)

    # ── B9（负控）只改 warn 的措辞（数字不动）⇒ 必须绿 ──────────────
    def b9():
        b.mutate(MEETINGS, "` ⇒ 这条请求**必然 400**（转写约", "` ⇒ 这条请求会失败（转写约", 'B9')
    b.case('B9（负控）只改 warn 的措辞（数字不动）⇒ 必须保持绿', False,
           must_not_contain=['既不是实测字节数', '闸内却出声了'], mutate_fn=b9)

    # ── B10（负控·**已知局限**）warn 里的斜率被改 ⇒ 仍绿 ──────────
    #   ⚠ 判据**只钉字节层面的性质**（§920 的设计：真实语料在 .artifacts/ 里
    #     ⇒ 已 gitignore ⇒ 判据不得依赖它）⇒ 斜率 844 改 900 判据看不见。
    #   ⇒ 这不是缺陷，是**有意的口径**；这里把它**显式钉出来**，免得后人误以为
    #     判据管着分钟数。
    def b10():
        b.mutate(MEETINGS, "${(promptBytes / 844).toFixed(1)}", "${(promptBytes / 900).toFixed(1)}", 'B10')
    b.case('B10（负控·已知局限）warn 里的斜率 844→900 ⇒ 判据**看不见**（有意不钉分钟数）', False,
           must_not_contain=['既不是实测字节数', '闸内却出声了'], mutate_fn=b10)


if __name__ == '__main__':
    run(b, body)

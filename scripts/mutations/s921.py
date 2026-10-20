#!/usr/bin/env python3
"""§935 · §921：重建并重跑「长度限制清单门」的变异 V1–V9。

⚠⚠ 为什么是**重建**而不是「重跑」：§921 当时的变异脚本**没有落盘**
（提交信息与主文档只留了「V1–V9 全中」的汇总）⇒ ⇒ 本脚本是**按 §921.4/§921.5
的判据描述重建的等价批次** ⇒ ⇒ 它能证明「**判据现在还有牙**」，
但**证明不了「当时那批脚本真的跑过」** ⇒ ⇒ 这个区别在主文档里如实写明。

判据命令：go test ./internal/server/ -run TestLengthLimitRegistryCoversEveryLengthGate
"""
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import Batch, HarnessError, go_runner, run  # noqa: E402

TARGETS = [
    'backend/internal/server/server_llm_chat_test.go',
    'backend/internal/server/server.go',
    'backend/internal/server/server_assistant.go',
]

GATE = go_runner('TestLengthLimitRegistryCoversEveryLengthGate')

b = Batch('§921 长度限制清单门', GATE, TARGETS)
REG = b.targets[0]
SERVER_GO = b.targets[1]
ASSISTANT_GO = b.targets[2]


def body():
    rc, out = GATE()
    b.record('基线：未变异的清单门必须绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-800:]))

    # ── V1 清单删一条（源码里还在 ⇒ 报「新增」）─────────────────────
    def v1():
        b.mutate(REG, '\t{"db/pg.go", "len(s) > 63", "O", ""},\n', '', 'V1')
    b.case('V1 清单删一条 ⇒ 报「源码里新增了一处长度限制」并点名', True,
           must_contain=['源码里新增了一处长度限制', 'db/pg.go', 'len(s) > 63'], mutate_fn=v1)

    # ── V2 清单多一条（源码里没有 ⇒ 报「已失效」）──────────────────
    def v2():
        b.mutate(REG,
                 '\t{"db/pg.go", "len(s) > 63", "O", ""},\n',
                 '\t{"db/pg.go", "len(s) > 63", "O", ""},\n'
                 '\t{"server/zz_mut_v2_nonexistent.go", "len(x) > 999", "O", ""},\n', 'V2')
    b.case('V2 清单多一条 ⇒ 报「已失效」并点名', True,
           must_contain=['在源码里已经找不到了', 'zz_mut_v2_nonexistent.go', 'len(x) > 999'],
           mutate_fn=v2)

    # ── V3 Go 源码真加一道闸（要能编译 ⇒ 追加一个函数）─────────────
    def v3():
        before = SERVER_GO.read_text(encoding='utf-8')
        SERVER_GO.write_text(before + (
            '\n// ⚠ 变异 V3：真加一道闸（必须被清单门点名）\n'
            'func mutV3Probe(s string) bool {\n\treturn len(s) > 12345\n}\n'), encoding='utf-8')
        on_disk = SERVER_GO.read_text(encoding='utf-8')
        if on_disk == before or 'mutV3Probe' not in on_disk:
            raise HarnessError('V3 落盘自证失败')
    b.case('V3 Go 源码真加一道闸 ⇒ 报新增并点名文件+表达式', True,
           must_contain=['源码里新增了一处长度限制', 'server/server.go', 'len(s) > 12345'],
           mutate_fn=v3)

    # ── V4 客户端可提交处真加一道闸 ───────────────────────────────
    def v4():
        before = ASSISTANT_GO.read_text(encoding='utf-8')
        ASSISTANT_GO.write_text(before + (
            '\n// ⚠ 变异 V4：客户端可提交处真加一道闸\n'
            'func mutV4Probe(body struct{ Subject string }) bool {\n'
            '\treturn len(body.Subject) > 321\n}\n'), encoding='utf-8')
        on_disk = ASSISTANT_GO.read_text(encoding='utf-8')
        if on_disk == before or 'mutV4Probe' not in on_disk:
            raise HarnessError('V4 落盘自证失败')
    b.case('V4 客户端可提交处真加一道闸 ⇒ 报新增并点名', True,
           must_contain=['源码里新增了一处长度限制', 'server/server_assistant.go',
                         'len(body.Subject) > 321'],
           mutate_fn=v4)

    # ── V5 语义等价负控（清单两条换位置 ⇒ 必须绿）──────────────────
    def v5():
        a = '\t{"db/pg.go", "len(s) > 63", "O", ""},\n'
        c = '\t{"email/fetcher.go", "len(s) > 64", "O", ""},\n'
        b.mutate(REG, a + c, c + a, 'V5')
    b.case('V5（负控）清单两条换位置 ⇒ 必须保持绿', False,
           must_not_contain=['长度限制出现处共', '在源码里已经找不到了'], mutate_fn=v5)

    # ── V6 A↔O 对调（13 → 13 守恒 ⇒ 只钉条数的旧门看不见）─────────
    def v6():
        b.mutate(REG,
                 '{"server/flashcards_handler.go", "len([]rune(name)) > 80", "A",',
                 '{"server/flashcards_handler.go", "len([]rune(name)) > 80", "O",', 'V6-a')
        b.mutate(REG,
                 '\t{"db/pg.go", "len(s) > 63", "O", ""},\n',
                 '\t{"db/pg.go", "len(s) > 63", "A", '
                 '"客户端提交的标识。**可达性：可达·可区分**（超限即拒绝）"},\n', 'V6-b')
    b.case('V6 A↔O 对调（13 → 13 守恒）⇒ 必须红且**双向点名**，且**条数那条不许响**', True,
           must_contain=['标成了 A 类', '在清单里不是 A 类', 'db/pg.go', 'flashcards_handler.go'],
           must_not_contain=['A 类（客户端可提交字段上的限制）实际'], mutate_fn=v6)

    # ── V7 只从钉住清单删一个键（清单里那条 A 变成「没被钉住」）─────
    def v7():
        b.mutate(REG,
                 '\t{"server/server_email_folders.go", "len(name) > 200"},\n', '', 'V7')
    b.case('V7 只从钉住清单删一个键 ⇒ 报「标成了 A 类但没被钉住」', True,
           must_contain=['标成了 A 类', 'lengthLimitRegistryAKeys 里没有它', 'server_email_folders.go'],
           mutate_fn=v7)

    # ── V8 钉住清单多一个不存在的键 ────────────────────────────────
    def v8():
        b.mutate(REG,
                 '\t{"server/task_collaboration_handler.go", "len(clientEventID) > 80"},\n',
                 '\t{"server/task_collaboration_handler.go", "len(clientEventID) > 80"},\n'
                 '\t{"server/zz_mut_v8_not_exists.go", "len(s) > 777"},\n', 'V8')
    b.case('V8 钉住清单多一个不存在的键 ⇒ 报两份清单不一致', True,
           must_contain=['lengthLimitRegistryAKeys 里的', '在清单里不是 A 类', 'zz_mut_v8_not_exists.go'],
           mutate_fn=v8)

    # ── V9 只把一条 A 改成 O（13 → 12）────────────────────────────
    def v9():
        b.mutate(REG,
                 '{"server/server_email_folders.go", "len(name) > 200", "A",',
                 '{"server/server_email_folders.go", "len(name) > 200", "O",', 'V9')
    b.case('V9 只把一条 A 改成 O（13 → 12）⇒ 报条数不符 + 集合不符', True,
           must_contain=['A 类（客户端可提交字段上的限制）实际 12 条', '在清单里不是 A 类'],
           mutate_fn=v9)


if __name__ == '__main__':
    run(b, body)

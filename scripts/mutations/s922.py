#!/usr/bin/env python3
"""§935 · §922：重建并重跑「客户端单次预算 == 服务端闸值」那批变异。

⚠⚠ 为什么是**重建**而不是「重跑」：§922 当时的变异脚本**没有落盘**
（提交信息与主文档只留了汇总）⇒ ⇒ 本脚本是**按 §922 的判据描述重建的等价批次**
⇒ ⇒ 它能证明「**判据现在还有牙**」，但**证明不了「当时那批脚本真的跑过」**
⇒ ⇒ 这个区别在主文档里如实写明。

判据命令：
  · 前端  node --experimental-strip-types --test --test-name-pattern=§922 \\
            src/features/email/__tests__/email-long-request-budget.test.mjs
  · Go    go test ./internal/server/ -run TestSttTranscribe
"""
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import Batch, node_runner, go_runner, run  # noqa: E402

TARGETS = [
    'frontend/src/api/email.ts',
    'frontend/src/api/stt.ts',
    'frontend/src/utils/chunk.ts',
    'backend/internal/server/server_email_purge.go',
    'backend/internal/server/server.go',
    'backend/internal/server/server_assistant.go',
]

FRONT = node_runner('src/features/email/__tests__/email-long-request-budget.test.mjs', '§922')
GO_STT = go_runner('TestSttTranscribe')

b = Batch('§922 跨语言预算 / 分批语义 / STT 413', FRONT, TARGETS)
EMAIL_API = b.targets[0]
STT_API = b.targets[1]
CHUNK = b.targets[2]
PURGE_GO = b.targets[3]
SERVER_GO = b.targets[4]
ASSISTANT_GO = b.targets[5]

# STT 那条 JSON 分支的**逐字**片段（缩进是 2 个 tab）。
STT_READ_BLOCK = (
    '\t\tif readErr != nil {\n'
    '\t\t\tvar maxErr *http.MaxBytesError\n'
    '\t\t\tif errors.As(readErr, &maxErr) {\n'
    '\t\t\t\twriteError(w, http.StatusRequestEntityTooLarge,\n'
    '\t\t\t\t\t"stt_audio_too_large: audio too large (max 25 MB)")\n'
    '\t\t\t\treturn\n'
    '\t\t\t}\n'
    '\t\t\twriteError(w, http.StatusBadRequest, "read body: "+readErr.Error())\n'
    '\t\t\treturn\n'
    '\t\t}\n'
)
STT_READ_BLOCK_NO_413 = (
    '\t\tif readErr != nil {\n'
    '\t\t\twriteError(w, http.StatusBadRequest, "read body: "+readErr.Error())\n'
    '\t\t\treturn\n'
    '\t\t}\n'
)


def body():
    rc, out = FRONT()
    b.record('基线：§922 前端判据必须绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-800:]))
    rc, out = GO_STT()
    b.record('基线：§922 Go 判据必须绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-500:]))

    # ── C1 服务端 purge 上限改了 ⇒ 前端跨语言契约红 ─────────────────
    def c1():
        b.mutate(PURGE_GO, 'if len(body.IDs) > 100 {', 'if len(body.IDs) > 250 {', 'C1')
    b.case('C1 服务端 purge 的 ID 上限 100→250 ⇒ 前端跨语言契约红', True,
           must_contain=['≠ 服务端 handleEmailPurge 的'], mutate_fn=c1)

    # ── C2 服务端 STT body 上限改了 ⇒ 前端跨语言契约红 ─────────────
    def c2():
        b.mutate(SERVER_GO, 'maxAudioBodyBytes   = 25 << 20', 'maxAudioBodyBytes   = 26 << 20', 'C2')
    b.case('C2 服务端 STT 请求体上限 25→26 MiB ⇒ 前端跨语言契约红', True,
           must_contain=['≠ 服务端 maxAudioBodyBytes 的'], mutate_fn=c2)

    # ── C3 前端 STT 常量落后 ⇒ 跨语言契约红 ───────────────────────
    def c3():
        b.mutate(STT_API, 'export const STT_TRANSCRIBE_MAX_BODY_BYTES = 25 * 1024 * 1024',
                 'export const STT_TRANSCRIBE_MAX_BODY_BYTES = 24 * 1024 * 1024', 'C3')
    b.case('C3 前端 STT 上限落后于服务端 ⇒ 跨语言契约红', True,
           must_contain=['≠ 服务端 maxAudioBodyBytes 的'], mutate_fn=c3)

    # ── C4 分批没接上那个常量 ⇒ 接线判据红 ─────────────────────────
    def c4():
        b.mutate(EMAIL_API, 'for (const chunk of chunkByLimit(ids, PURGE_MAX_IDS_PER_REQUEST)) {',
                 'for (const chunk of chunkByLimit(ids, 999)) {', 'C4')
    b.case('C4 分批用的不是那个常量 ⇒ 上限会与跨语言契约脱钩 ⇒ 红', True,
           must_contain=['分批用的不是那个常量'], mutate_fn=c4)

    # ── C5 STT 的上限检查用错了单位 ⇒ 红 ───────────────────────────
    def c5():
        b.mutate(STT_API, 'const bodyBytes = utf8ByteLength(body)', 'const bodyBytes = body.length', 'C5')
    b.case('C5 STT 用 UTF-16 码元数当字节数 ⇒ 红', True,
           must_contain=['没用 utf8ByteLength'], mutate_fn=c5)

    # ── C6 chunkByLimit 把批次**顺序**反转 ⇒ 「不丢不重」行为判据红 ──
    #   ⚠ 变异要选得准：若直接改成 `slice(i, i + step - 1)`（丢元素），
    #   会**先**撞到 `assert.equal(batches[0].length, 100)` —— 那条**没有消息**
    #   ⇒ 报出来的是 node 断言的默认串 ⇒ 抓不到「不丢不重」那条断言。
    #   ⇒ 这里改成 `.reverse()`（长度不变、顺序变）⇒ 精确命中「不丢不重」那条。
    def c6():
        b.mutate(CHUNK, 'out.push(items.slice(i, i + step))',
                 'out.push(items.slice(i, i + step).reverse())', 'C6')
    b.case('C6 chunkByLimit 把批次顺序反转 ⇒ 「不丢不重（含顺序）」行为判据红', True,
           must_contain=['分批丢了或重了元素'], mutate_fn=c6)

    # ── C6b chunkByLimit 真的**丢元素** ⇒ 长度断言红 ───────────────
    #   ⚠ 这条先撞到的是 `assert.equal(batches[0].length, 100)` —— 那条断言
    #   **没有消息** ⇒ 报出来的是 node 断言的默认串 ⇒ 这里就按它断言。
    def c6b():
        b.mutate(CHUNK, 'out.push(items.slice(i, i + step))',
                 'out.push(items.slice(i, i + step - 1))', 'C6b')
    b.case('C6b chunkByLimit 真的丢元素 ⇒ 批次长度断言红（撞的是无消息那条）', True,
           must_contain=['Expected values to be strictly equal'], mutate_fn=c6b)

    # ── C7 chunkByLimit 把上限多算 1 ⇒ 边界判据红 ──────────────────
    def c7():
        b.mutate(CHUNK, 'const step = Math.max(1, Math.floor(limit))',
                 'const step = Math.max(1, Math.floor(limit) + 1)', 'C7')
    b.case('C7 chunkByLimit 的上限多算 1 ⇒ 「101 个 id / 上限 100 ⇒ 应切 2 批」红', True,
           must_contain=['应切 2 批'], mutate_fn=c7)

    # ── C8（负控）chunkByLimit 换成**语义等价**的写法 ⇒ 必须绿 ──────
    def c8():
        b.mutate(CHUNK,
                 '  const step = Math.max(1, Math.floor(limit))\n'
                 '  const out: T[][] = []\n'
                 '  for (let i = 0; i < items.length; i += step) {\n'
                 '    out.push(items.slice(i, i + step))\n'
                 '  }\n'
                 '  return out\n',
                 '  const step = Math.max(1, Math.floor(limit))\n'
                 '  const out: T[][] = []\n'
                 '  let i = 0\n'
                 '  while (i < items.length) {\n'
                 '    out.push(items.slice(i, i + step))\n'
                 '    i += step\n'
                 '  }\n'
                 '  return out\n', 'C8')
    b.case('C8（负控）chunkByLimit 换成等价的 while 写法 ⇒ 必须保持绿', False,
           must_not_contain=['分批丢了或重了元素', '应切 2 批'], mutate_fn=c8)

    # ── C9 服务端不再区分「读超了」⇒ 413 变 400 ⇒ 红 ─────────────
    def c9():
        b.mutate(ASSISTANT_GO, STT_READ_BLOCK, STT_READ_BLOCK_NO_413, 'C9')
    b.case('C9 服务端不再区分「读超了」⇒ 「音频太大」被说成「请求体无效」⇒ 红', True,
           must_contain=['期望 413'], mutate_fn=c9, runner=GO_STT)

    # ── C10 负控被改坏：坏 JSON 也回 413 ⇒ 负控红 ──────────────────
    def c10():
        b.mutate(ASSISTANT_GO,
                 '\t\tif err := json.NewDecoder(bytes.NewReader(raw)).Decode(&body); err != nil {\n'
                 '\t\t\twriteError(w, http.StatusBadRequest, "invalid body")\n'
                 '\t\t\treturn\n'
                 '\t\t}\n'
                 '\t\tfilename = body.Filename\n',
                 '\t\tif err := json.NewDecoder(bytes.NewReader(raw)).Decode(&body); err != nil {\n'
                 '\t\t\twriteError(w, http.StatusRequestEntityTooLarge,\n'
                 '\t\t\t\t"stt_audio_too_large: audio too large (max 25 MB)")\n'
                 '\t\t\treturn\n'
                 '\t\t}\n'
                 '\t\tfilename = body.Filename\n', 'C10')
    b.case('C10 负控被改坏（坏 JSON 也回 413）⇒ 「仍是 400」那条红', True,
           must_contain=['期望 400（不是 413）'], mutate_fn=c10, runner=GO_STT)

    # ── C11（负控）413 的文案改了但**保留错误码** ⇒ 必须绿 ─────────
    def c11():
        b.mutate(ASSISTANT_GO,
                 '\t\t\t\twriteError(w, http.StatusRequestEntityTooLarge,\n'
                 '\t\t\t\t\t"stt_audio_too_large: audio too large (max 25 MB)")\n'
                 '\t\t\t\treturn\n'
                 '\t\t\t}\n'
                 '\t\t\twriteError(w, http.StatusBadRequest, "read body: "+readErr.Error())\n',
                 '\t\t\t\twriteError(w, http.StatusRequestEntityTooLarge,\n'
                 '\t\t\t\t\t"stt_audio_too_large: 音频太大（上限 25 MB）")\n'
                 '\t\t\t\treturn\n'
                 '\t\t\t}\n'
                 '\t\t\twriteError(w, http.StatusBadRequest, "read body: "+readErr.Error())\n', 'C11')
    b.case('C11（负控）413 的文案改了但保留错误码 ⇒ 必须保持绿（判据只钉错误码）', False,
           must_not_contain=['缺少稳定错误码'], mutate_fn=c11, runner=GO_STT)


if __name__ == '__main__':
    run(b, body, extra_runners=(GO_STT,))

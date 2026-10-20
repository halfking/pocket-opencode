#!/usr/bin/env python3
"""§948：promise 形态的「拒绝被就地消化」不许无声（92 站台账 + 判据自己有牙）。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
   **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠ 本批次是本仓**第四批「把量具自己打坏」的变异**（§945 的 M8、§946 的 M13、
   §947 的 M3–M6、本批的 M4–M8）—— ⇒ 每次都必须证明：**量具坏了 ⇒ 门会红**。

判据命令：
  node --experimental-strip-types --test \\
        --test-name-pattern=§948 \\
        src/__tests__/silent-catch-must-explain-itself.test.mjs

⚠ 本批次最该被记住的一条：

   ★ **M4 是「量具整族漏掉一种形态」**：
     `catch(() => null)` 这一族在 TypeScript AST 里，`null` 是 **NullKeyword**
     而不是 Identifier ⇒ 用 `ts.isIdentifier` 判「表达式体是不是空」就会整族漏掉
     （第一版量具只扫到 81 处，**漏了 8 处**）。
     ⇒ 这就是「量出来的数偏小」的第一嫌疑：**量具漏了一族形态**，
       不是「仓库变干净了」。
     ⇒ 必须有一条变异专门把 NullKeyword 判据打坏，证明门会红。

★ **M1/M2/M3/M9 是「实现侧」的三种典型退化**：
   删理由注释（⇒ 无声）、删留痕（⇒ 无痕）、新增一处没登记的吞错（⇒ 台账失配）、
   删掉一整处吞错（⇒ 条数失配）。

★ **N1–N5 是负控**：语义等价的改动（改措辞、换 console.error、把理由从体内挪到
   紧邻上一行、台账条目换顺序）**必须保持绿** —— 否则这道门会变成「凡改动即报红」。
"""

import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import Batch, node_runner, run  # noqa: E402

TARGETS = [
    'frontend/src/__tests__/silent-catch-must-explain-itself.test.mjs',
    'frontend/src/stores/theme.ts',
    'frontend/src/app/AppLayout.vue',
    'frontend/src/features/agents/AgentLibraryView.vue',
    'frontend/src/features/email/EmailInboxView.vue',
]

FRONT = node_runner('src/__tests__/silent-catch-must-explain-itself.test.mjs', '§948')

b = Batch('§948 promise 形态的拒绝被就地消化不许无声（92 站台账 + 判据自证）',
          FRONT, TARGETS)

JUDGE = b.targets[0]
THEME = b.targets[1]
APP_LAYOUT = b.targets[2]
AGENT_LIB = b.targets[3]
INBOX_VIEW = b.targets[4]

# ── M1：删掉 A 类站点的理由注释 ⇒ 「没解释自己」那条红 ─────────────────
# ⚠ 第一版只删了**第一行**注释 ⇒ 体里还剩第二行（同样是在解释）⇒ 判据仍绿
#   ⇒ 那不是「判据无牙」，是**变异没打坏目标**。⇒ 这里删掉**整段**理由。
M1_OLD = (
    "    import('../native/config-sync/prefs').then((m) => m.persistAppPrefs()).catch((e) => {\n"
    "      // 主题偏好没能落进本地库 ⇒ 本次会话内有效，重启后回到旧值。\n"
    "      // 跨设备同步靠的才是这条持久化路径 ⇒ 失败时别处看不到 ⇒ 留痕。\n"
)
M1_NEW = (
    "    import('../native/config-sync/prefs').then((m) => m.persistAppPrefs()).catch((e) => {\n"
)

# ── M2：删掉 A 类站点的 console.warn ⇒ 台账分类不符 ⇒ 红 ──────────────
M2_OLD = "      console.warn('[app-layout] 返回键监听移除失败：', e)\n"
M2_NEW = ""

# ── M3：新增一处没登记的静默吞错 ⇒ 条数与台账都失配 ⇒ 红 ─────────────
M3_OLD = "function retryLoad() {\n"
M3_NEW = "function retryLoad() {\n  agentStore.loadAgents().catch(() => {})\n"

# ── M4：量具认不出 NullKeyword ⇒ 整族漏掉 ⇒ 自证臂红（**牙在量具自己身上**）──
M4_OLD = "      body.kind === ts.SyntaxKind.NullKeyword ||\n"
M4_NEW = "      false ||\n"

# ── M5：量具的「体内注释」识别坏掉 ⇒ 自证臂红 ─────────────────────────
M5_OLD = (
    "  const inBody = exprForm ? false : commentTokens(code, body.getStart() + 1, body.getEnd()).length > 0\n"
)
M5_NEW = "  const inBody = false\n"

# ── M6：量具的「紧邻上一行注释」识别坏掉 ⇒ 自证臂红 ───────────────────
M6_OLD = "  const above = prevLine.startsWith('//')\n"
M6_NEW = "  const above = false\n"

# ── M7：量具的「留痕识别」坏掉（console.warn 不再算留痕）⇒ 自证臂 + 台账红 ──
M7_OLD = (
    "    (s.expression.expression.name.text === 'warn' || s.expression.expression.name.text === 'error')\n"
)
M7_NEW = "    false\n"

# ── M8：量具把「置状态 / rethrow」也算成吞错点 ⇒ 条数暴涨 ⇒ 红 ────────
M8_OLD = "      if (!stmts.every((s) => isTrace(s) || isNullReturn(s))) return null\n"
M8_NEW = "      if (false) return null\n"

# ── M9：删掉一整处吞错（连 catch 一起没）⇒ 条数失配 ⇒ 红 ──────────────
M9_OLD = (
    "    await pullInboxFromServer().catch(() => {\n"
    "      // pullInboxFromServer 内部已逐段 try/catch 并 console.warn ⇒ 这里接不到东西，\n"
    "      // 只是防止它的实现将来改成往外抛时产生未处理 rejection。\n"
    "    })\n"
)
M9_NEW = "    await pullInboxFromServer()\n"

# ── M10：台账里某个文件的分类序列被改错 ⇒ 台账双向相等那条红 ──────────
M10_OLD = "  'src/stores/theme.ts': 'A',\n"
M10_NEW = "  'src/stores/theme.ts': 'B',\n"

# ── N1：理由注释的措辞微调（判据不看理由文本）⇒ 必须绿 ────────────────
N1_OLD = "      // 主题偏好没能落进本地库 ⇒ 本次会话内有效，重启后回到旧值。\n"
N1_NEW = "      // 主题偏好没写进本地库 ⇒ 本次会话内有效，重启后回到旧值。\n"

# ── N2：台账条目的顺序换一下（按键比对，顺序无关）⇒ 必须绿 ───────────
N2_OLD = (
    "  'src/features/flashcards/CardBrowserView.vue': 'B',\n"
    "  'src/features/flashcards/DeckOptionsView.vue': 'B',\n"
)
N2_NEW = (
    "  'src/features/flashcards/DeckOptionsView.vue': 'B',\n"
    "  'src/features/flashcards/CardBrowserView.vue': 'B',\n"
)

# ── N3：留痕的**方法**换成 console.error（仍是留痕）⇒ 必须绿 ──────────
N3_OLD = "      console.warn('[theme] 主题偏好未能持久化：', e)\n"
N3_NEW = "      console.error('[theme] 主题偏好未能持久化：', e)\n"

# ── N4：把理由从「handler 体内」挪到「紧邻上一行」⇒ 必须绿 ───────────
N4_OLD = (
    "    import('../native/config-sync/prefs').then((m) => m.persistAppPrefs()).catch((e) => {\n"
    "      // 主题偏好没能落进本地库 ⇒ 本次会话内有效，重启后回到旧值。\n"
    "      // 跨设备同步靠的才是这条持久化路径 ⇒ 失败时别处看不到 ⇒ 留痕。\n"
    "      console.warn('[theme] 主题偏好未能持久化：', e)\n"
    "    })\n"
)
N4_NEW = (
    "    // 主题偏好没能落进本地库 ⇒ 本次会话内有效，重启后回到旧值。\n"
    "    // 跨设备同步靠的才是这条持久化路径 ⇒ 失败时别处看不到 ⇒ 留痕。\n"
    "    import('../native/config-sync/prefs').then((m) => m.persistAppPrefs()).catch((e) => {\n"
    "      console.warn('[theme] 主题偏好未能持久化：', e)\n"
    "    })\n"
)


def body():
    rc, out = FRONT()
    b.record('基线：§948 判据必须绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-800:]))

    # ── M1 必须红：删掉理由注释 ⇒ 变成「没解释自己」──────────────────
    def m1():
        b.mutate(THEME, M1_OLD, M1_NEW, 'M1')
    b.case('M1 删掉一处 A 类站点的理由注释 ⇒ 变成无声的吞错 ⇒ 判据必须红', True,
           must_contain=['没解释自己的吞错点'], mutate_fn=m1)

    # ── M2 必须红：删掉留痕 ⇒ A 变 B ⇒ 台账分类不符 ───────────────────
    def m2():
        b.mutate(APP_LAYOUT, M2_OLD, M2_NEW, 'M2')
    b.case('M2 删掉一处 A 类站点的 console.warn ⇒ 失败重新变无声 ⇒ 判据必须红', True,
           must_contain=['分类与台账不一致'], mutate_fn=m2)

    # ── M3 必须红：新增一处没登记的吞错 ⇒ 条数 + 台账 extra ───────────
    def m3():
        b.mutate(AGENT_LIB, M3_OLD, M3_NEW, 'M3')
    b.case('M3 新增一处没登记的静默吞错 ⇒ 条数与台账都失配 ⇒ 判据必须红', True,
           must_contain=['吞错点实测'], mutate_fn=m3)

    # ── M4 必须红：量具认不出 NullKeyword ⇒ 整族漏掉（自证臂当场红）────
    def m4():
        b.mutate(JUDGE, M4_OLD, M4_NEW, 'M4')
    b.case('M4 量具认不出 NullKeyword ⇒ `catch(() => null)` 整族漏掉 ⇒ 自证臂必须红', True,
           must_contain=['没被认出来'], mutate_fn=m4)

    # ── M5 必须红：量具的体内注释识别坏掉 ⇒ 自证臂红 ─────────────────
    def m5():
        b.mutate(JUDGE, M5_OLD, M5_NEW, 'M5')
    b.case('M5 量具的「体内注释」识别坏掉 ⇒ 自证臂必须红', True,
           must_contain=['体内注释必须算'], mutate_fn=m5)

    # ── M6 必须红：量具的「紧邻上一行」识别坏掉 ⇒ 自证臂红 ───────────
    def m6():
        b.mutate(JUDGE, M6_OLD, M6_NEW, 'M6')
    b.case('M6 量具的「紧邻上一行注释」识别坏掉 ⇒ 自证臂必须红', True,
           must_contain=['紧邻上一行的注释必须算'], mutate_fn=m6)

    # ── M7 必须红：量具的留痕识别坏掉 ⇒ 自证臂 + 台账都红 ────────────
    def m7():
        b.mutate(JUDGE, M7_OLD, M7_NEW, 'M7')
    b.case('M7 量具的「console.warn 算留痕」识别坏掉 ⇒ 自证臂必须红', True,
           must_contain=['带 console.warn 的 handler 必须被认成吞错点'], mutate_fn=m7)

    # ── M8 必须红：量具把「置状态 / rethrow」也算进去 ⇒ 条数暴涨 ───────
    def m8():
        b.mutate(JUDGE, M8_OLD, M8_NEW, 'M8')
    b.case('M8 量具把「置状态 / rethrow」也算成吞错点 ⇒ 条数失配 ⇒ 判据必须红', True,
           must_contain=['吞错点实测'], mutate_fn=m8)

    # ── M9 必须红：删掉一整处吞错 ⇒ 条数失配 ─────────────────────────
    def m9():
        b.mutate(INBOX_VIEW, M9_OLD, M9_NEW, 'M9')
    b.case('M9 删掉一整处吞错（连 catch 一起没）⇒ 条数失配 ⇒ 判据必须红', True,
           must_contain=['吞错点实测'], mutate_fn=m9)

    # ── M10 必须红：台账分类序列被改错 ⇒ 台账双向相等那条红 ───────────
    def m10():
        b.mutate(JUDGE, M10_OLD, M10_NEW, 'M10')
    b.case('M10 台账里某文件的分类序列被改错 ⇒ 台账双向相等那条必须红', True,
           must_contain=['分类与台账不一致'], mutate_fn=m10)

    # ── 负控：语义等价的改动必须保持绿 ───────────────────────────────
    def n1():
        b.mutate(THEME, N1_OLD, N1_NEW, 'N1')
    b.case('N1 理由注释的措辞微调（判据不看理由文本）⇒ 必须保持绿', False,
           mutate_fn=n1)

    def n2():
        b.mutate(JUDGE, N2_OLD, N2_NEW, 'N2')
    b.case('N2 台账条目的顺序换一下（按键比对）⇒ 必须保持绿', False,
           mutate_fn=n2)

    def n3():
        b.mutate(THEME, N3_OLD, N3_NEW, 'N3')
    b.case('N3 留痕的**方法**换成 console.error（仍是留痕）⇒ 必须保持绿', False,
           mutate_fn=n3)

    def n4():
        b.mutate(THEME, N4_OLD, N4_NEW, 'N4')
    b.case('N4 把理由从「handler 体内」挪到「紧邻上一行」⇒ 必须保持绿', False,
           mutate_fn=n4)


if __name__ == '__main__':
    run(b, body)

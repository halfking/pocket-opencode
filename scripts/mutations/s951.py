#!/usr/bin/env python3
"""§951：等一个不会自己结束的东西，必须自带 deadline（后端 SMTP + 前端 fetch）。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
  **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠ 本批次是本仓**第七批「把量具自己打坏」的变异**（§945 的 M8、§946 的 M13、
  §947 的 M3–M6、§948 的 M4–M8、§949 的 M9、§950 的 M7/M10/M11/M12、
  本批的 **M10–M13 / F6**）⇒ ⇒ 每次都必须证明：**量具坏了 ⇒ 门会红**。

判据命令：
  go test ./internal/repohygiene/ -run 'TestS951' -count=1     （后端卡口）
  go test ./internal/notify/    -run 'TestSMTPClient' -count=1  （后端行为断言）
  node scripts/check-fetch-bounds.mjs                           （前端卡口）

⚠ 本批次最该被记住的三条：

★ **M1 是「上界算错」而不是「上界没有」**：
  把 deadline 算成**过去时刻** ⇒ 请求「立刻」失败 ⇒ 这恰恰是**双边断言**存在的理由：
  只钉「不许挂住」的话，这条变异会**照样绿**（因为它确实没挂住）。
  ⇒ ⇒⇒ 「太快」那一侧必须一起钉。

★ **M4–M9 是「判据整族漏掉一种形态」**（§949 的 M9、§950 的 M11/M12 同族）：
  禁 `smtp.Dial` / `tls.Dial` / 裸 `net.Dial`、认 `net.Dialer` 的 Timeout 字段、
  认「`smtp.NewClient` 所在函数有没有 SetDeadline」——每一族各一个条件项。
  把其中任何一处打坏 ⇒ 对应的合成用例必须红
  ⇒ ⇒⇒ 「量出来的数比该有的少 ⇒ 第一嫌疑是量具漏了一族形态」。

★ **F6 是「前端量具漏掉一整族写法」**：
  `{ signal }`（简写形态）**不是** `PropertyAssignment` ⇒ 只认后者会漏掉一整族
  ⇒ 而那一族恰好是 `api/llm-bff.ts` 的 SSE 站点用的写法
  ⇒ ⇒⇒ 漏了它 ⇒ 那处站点会从 L2 掉进 L1 ⇒ 台账失配（红），
  但**更糟**的是：它会让「有 signal 但上界在调用方」这件事**不再被要求登记**。
"""

import subprocess
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    BACKEND, FRONTEND, Batch, run,
)

TARGETS = [
    # 后端：被测实现 + 判据 + 被牵连的既有台账
    'backend/internal/notify/smtp.go',
    'backend/internal/repohygiene/smtp_deadline_test.go',
    'backend/internal/repohygiene/swallowed_call_test.go',
    'backend/internal/server/smtp_send.go',
    'backend/internal/server/ssrf.go',
    # 前端：唯一那层 + 改过的 6 处 + 前端判据
    'frontend/src/api/fetch-timeout.ts',
    'frontend/src/api/client.ts',
    'frontend/src/api/email.ts',
    'frontend/src/api/llm-bff.ts',
    'frontend/src/utils/version.ts',
    'frontend/src/config/api-base.ts',
    'frontend/src/stores/connectivity.ts',
    'frontend/scripts/check-fetch-bounds.mjs',
]


def _run(cmd, cwd):
    p = subprocess.run(cmd, cwd=str(cwd), capture_output=True, text=True, timeout=1800)
    return p.returncode, p.stdout + p.stderr


def judge_runner():
    """后端卡口（快）+ 前端卡口（快）—— 批次的**默认**运行器。"""
    def run_it():
        rc1, out1 = _run(
            ['go', 'test', './internal/repohygiene/', '-run', 'TestS951', '-count=1'],
            BACKEND)
        rc2, out2 = _run(['node', 'scripts/check-fetch-bounds.mjs'], FRONTEND)
        return (rc1 or rc2), out1 + '\n' + out2
    return run_it


def notify_runner():
    """后端**行为**断言（notify 包）—— 单独一个运行器。

    ⚠ 为什么要分开：`dialAndUpgrade` 少了 `SetDeadline` ⇒ 那台「一句话不说」的
    服务器会把测试**挂住** ⇒ 放在默认运行器里会让整个批次卡到超时。
    ⇒ ⇒ 「实现被掏空」这类变异走**卡口**（快），行为断言只在收尾时跑一次（还原态）。
    """
    def run_it():
        return _run(
            ['go', 'test', './internal/notify/', '-run', 'TestSMTPClient', '-count=1'],
            BACKEND)
    return run_it


def notify_budget_runner():
    """只跑 notify 包的**预算纯函数**断言（不碰网络 ⇒ 不会挂住）。

    ⚠ 为什么要单独一个：某些变异会让**会话上界消失** ⇒ 那台「一句话不说」的
    服务器会把 `TestSMTPClient_StalledServerIsBounded` **挂住** ⇒ 整个批次
    卡到 Go 的 10 分钟测试超时。
    ⇒ ⇒⇒ ⇒ 而那两条**预算**断言是**纯函数**的（不碰网络）⇒ 它们才是这类
    变异的**正确观察口** ⇒ ⇒⇒ ⇒ ★ **变异要挑「不会挂住」的那条断言当观察口**。
    """
    def run_it():
        return _run(
            ['go', 'test', './internal/notify/', '-run', 'TestSMTPClient_Budget', '-count=1'],
            BACKEND)
    return run_it


def frontend_runner():
    def run_it():
        return _run(['node', 'scripts/check-fetch-bounds.mjs'], FRONTEND)
    return run_it


b = Batch('§951 等一个不会自己结束的东西，必须自带 deadline（后端 SMTP + 前端 fetch）',
          judge_runner(), TARGETS)

SMTP = b.targets[0]
GATE = b.targets[1]
SWALLOWED = b.targets[2]
SMTP_SEND = b.targets[3]
SSRF = b.targets[4]
PRIM = b.targets[5]
CLIENT = b.targets[6]
EMAIL = b.targets[7]
LLMBFF = b.targets[8]
VERSION = b.targets[9]
APIBASE = b.targets[10]
CONNECT = b.targets[11]
FGATE = b.targets[12]


def main():
    # ── M1 必须红：deadline 算成**过去时刻** ⇒ 请求立刻失败（不是挂住）────
    # ⚠ 这条正是「双边断言」存在的理由：只钉「不许挂住」的话它会照样绿。
    M1_OLD = '\tdeadline := time.Now().Add(smtpSessionTimeout)\n'
    M1_NEW = '\tdeadline := time.Now().Add(-time.Hour)\n'

    def m1():
        b.mutate(SMTP, M1_OLD, M1_NEW, 'M1')
    b.case('M1 把 deadline 算成过去时刻 ⇒ 上界算错（立刻失败）⇒ 行为断言必须红', True,
           must_contain=['TestSMTPClient_StalledServerIsBounded'],
           mutate_fn=m1, runner=notify_runner())

    # ── M2 必须红：ctx 的 deadline 不被采纳 ⇒ 调用方给的更早上界被忽略 ────
    M2_OLD = ('\tif d, ok := ctx.Deadline(); ok && d.Before(deadline) {\n'
              '\t\tdeadline = d\n'
              '\t}\n')

    def m2():
        b.mutate(SMTP, M2_OLD, '', 'M2')
    b.case('M2 不采纳 ctx 的 deadline ⇒ 调用方给的更早上界被忽略 ⇒ 行为断言必须红', True,
           must_contain=['TestSMTPClient_BudgetPrefersEarlierCtxDeadline'],
           mutate_fn=m2, runner=notify_budget_runner())

    # ── M3 必须红：没有 ctx deadline 时的回落值不对 ⇒ 上界又不依赖调用方 ──
    M3_OLD = '\treturn smtpBudget{dialTimeout: smtpDialTimeout, deadline: deadline}\n'
    M3_NEW = '\treturn smtpBudget{dialTimeout: smtpDialTimeout}\n'

    def m3():
        b.mutate(SMTP, M3_OLD, M3_NEW, 'M3')
    b.case('M3 没有 ctx deadline 时不给会话上界 ⇒ 「上界不依赖调用方」这条破了 ⇒ 行为断言必须红', True,
           must_contain=['TestSMTPClient_BudgetFallsBackToSessionTimeout'],
           mutate_fn=m3, runner=notify_budget_runner())

    # ── M4 必须红：把 `smtp.Dial(` 放回后端（无拨号超时的 stdlib 口子）────
    M4_OLD = '// dialAndUpgrade 按 TLSMode 连接：starttls / tls / none。\n'
    M4_NEW = ('// s951mutant 是变异用：smtp.Dial 没有拨号超时（门必须红）。\n'
              'func s951mutant() (*smtp.Client, error) {\n'
              '\treturn smtp.Dial("h:1")\n'
              '}\n\n'
              '// dialAndUpgrade 按 TLSMode 连接：starttls / tls / none。\n')

    def m4():
        b.mutate(SMTP, M4_OLD, M4_NEW, 'M4')
    b.case('M4 引入 `smtp.Dial(` ⇒ 无拨号超时 ⇒ 后端卡口必须红', True,
           must_contain=['TestS951NoBannedDialForms'],
           mutate_fn=m4)

    # ── M5 必须红：把 `tls.Dial(` 放回后端 ────────────────────────────────
    M5_OLD = '// dialAndUpgrade 按 TLSMode 连接：starttls / tls / none。\n'
    M5_NEW = ('// s951mutant 是变异用：tls.Dial 没有拨号超时（门必须红）。\n'
              'func s951mutant() (*tls.Conn, error) {\n'
              '\treturn tls.Dial("tcp", "h:1", nil)\n'
              '}\n\n'
              '// dialAndUpgrade 按 TLSMode 连接：starttls / tls / none。\n')

    def m5():
        b.mutate(SMTP, M5_OLD, M5_NEW, 'M5')
    b.case('M5 引入 `tls.Dial(` ⇒ 无拨号超时 ⇒ 后端卡口必须红', True,
           must_contain=['TestS951NoBannedDialForms'],
           mutate_fn=m5)

    # ── M6 必须红：把裸 `net.Dial(` 放回后端 ──────────────────────────────
    M6_OLD = '// dialAndUpgrade 按 TLSMode 连接：starttls / tls / none。\n'
    M6_NEW = ('// s951mutant 是变异用：裸 net.Dial 没有超时（门必须红）。\n'
              'func s951mutant() (net.Conn, error) {\n'
              '\treturn net.Dial("tcp", "h:1")\n'
              '}\n\n'
              '// dialAndUpgrade 按 TLSMode 连接：starttls / tls / none。\n')

    def m6():
        b.mutate(SMTP, M6_OLD, M6_NEW, 'M6')
    b.case('M6 引入裸 `net.Dial(` ⇒ 无拨号超时 ⇒ 后端卡口必须红', True,
           must_contain=['TestS951NoBannedDialForms'],
           mutate_fn=m6)

    # ── M7 必须红：把某处 `net.Dialer` 的 Timeout 删掉 ⇒ 拨号无上界 ──────
    M7_OLD = '\tdialer := &net.Dialer{Timeout: smtpDialTimeout}\n'
    M7_NEW = '\tdialer := &net.Dialer{}\n'

    def m7():
        b.mutate(SMTP_SEND, M7_OLD, M7_NEW, 'M7')
    b.case('M7 删掉某处 `net.Dialer` 的 Timeout ⇒ 拨号无上界 ⇒ 后端卡口必须红', True,
           must_contain=['TestS951EveryDialerHasTimeout'],
           mutate_fn=m7)

    # ── M8 必须红：掏掉 `dialAndUpgrade` 里的 socket deadline ⇒ 会话无界 ──
    # ⚠ 必须**能编译**：第一版写成 `if err := error(nil); err != nil {` ⇒
    #   `conn` 变成未使用 ⇒ **编译失败** ⇒ 门是「编译错」红，不是「判据」红
    #   ⇒ ⇒⇒ ⇒ ★ **变异必须先确认「改完还能编译」**，否则红得没有意义。
    M8_OLD = ('\tif err := conn.SetDeadline(budget.deadline); err != nil {\n'
              '\t\t_ = conn.Close()\n'
              '\t\treturn nil, fmt.Errorf("smtp set deadline: %w", err)\n'
              '\t}\n')
    M8_NEW = ('\t// s951mutant：不设 socket deadline（门必须红）。\n'
              '\t_ = budget.deadline\n')

    def m8():
        b.mutate(SMTP, M8_OLD, M8_NEW, 'M8')
    b.case('M8 掏掉 SMTP 会话的 socket deadline ⇒ 协议阶段无上界 ⇒ 后端卡口必须红', True,
           must_contain=['TestS951EverySMTPClientHasDeadline'],
           mutate_fn=m8)

    # ── M9 必须红：台账登记的那处被修好了 ⇒ 台账条目过期 ⇒ 必须红 ────────
    M9_OLD = '\t\t\t\treturn (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))\n'
    M9_NEW = '\t\t\t\treturn (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))\n'

    def m9():
        b.mutate(SSRF, M9_OLD, M9_NEW, 'M9')
    b.case('M9 台账登记的那处被修好（有了 Timeout）⇒ 台账条目过期 ⇒ 后端卡口必须红', True,
           must_contain=['TestS951DialerExceptionLedger'],
           mutate_fn=m9)

    # ── M10 必须红：量具**漏掉「net.Dialer 没有 Timeout」这一族** ⇒ 自证臂红 ─
    M10_OLD = '\t\t\t\tcase !has:\n'
    M10_NEW = '\t\t\t\tcase !has && false:\n'

    def m10():
        b.mutate(GATE, M10_OLD, M10_NEW, 'M10')
    b.case('M10 量具漏掉「net.Dialer 没有 Timeout」这一族形态 ⇒ 合成用例必须红 ⇒ 后端卡口必须红', True,
           must_contain=['TestS951ScannerHasTeeth'],
           mutate_fn=m10)

    # ── M11 必须红：量具**漏掉「smtp.NewClient 缺 deadline」这一族** ──────
    M11_OLD = '\t\tif !hasSetDeadline {\n'
    M11_NEW = '\t\tif false && !hasSetDeadline {\n'

    def m11():
        b.mutate(GATE, M11_OLD, M11_NEW, 'M11')
    b.case('M11 量具漏掉「smtp.NewClient 所在函数没有 SetDeadline」这一族形态 ⇒ 合成用例必须红 ⇒ 后端卡口必须红', True,
           must_contain=['TestS951ScannerHasTeeth'],
           mutate_fn=m11)

    # ── M12 必须红：量具**把 Timeout: 0 当成合规** ⇒ 零上界漏放 ──────────
    # ⚠ `case false:` 会让 `zero` 变成**未使用** ⇒ 编译失败 ⇒ 必须写成
    #   `case zero && false:`（`zero` 仍被引用 ⇒ 能编译 ⇒ 红得才有意义）。
    M12_OLD = '\t\t\t\tcase zero:\n'
    M12_NEW = '\t\t\t\tcase zero && false:\n'

    def m12():
        b.mutate(GATE, M12_OLD, M12_NEW, 'M12')
    b.case('M12 量具把 `net.Dialer{Timeout: 0}` 当成合规（零超时 = 没有上界）⇒ 合成用例必须红 ⇒ 后端卡口必须红', True,
           must_contain=['TestS951ScannerHasTeeth'],
           mutate_fn=m12)

    # ── M13 必须红：量具**过宽**（把有 Timeout 的也算成没 Timeout）────────
    M13_OLD = '\t\treturn true, false\n'
    M13_NEW = '\t\treturn false, false\n'

    def m13():
        b.mutate(GATE, M13_OLD, M13_NEW, 'M13')
    b.case('M13 量具过宽：把带 Timeout 的 net.Dialer 也算成没 Timeout ⇒ 合成用例必须红 ⇒ 后端卡口必须红', True,
           must_contain=['TestS951ScannerHasTeeth'],
           mutate_fn=m13)

    # ── F1 必须红：把 client.ts 的 authFetch 改回**裸 fetch** ────────────
    F1_OLD = '  const response = await fetchWithTimeout(input, { ...init, headers })\n'
    F1_NEW = '  const response = await fetch(input, { ...init, headers })\n'

    def f1():
        b.mutate(CLIENT, F1_OLD, F1_NEW, 'F1')
    b.case('F1 把 authFetch 改回裸 fetch ⇒ 无上界 ⇒ 前端卡口必须红', True,
           must_contain=['L1 台账少了一条'],
           mutate_fn=f1, runner=frontend_runner())

    # ── F2 必须红：把 email.ts 的发票下载改回裸 fetch ────────────────────
    F2_OLD = '    const res = await fetchWithTimeout(`${resolveRuntimeApiBase()}/api/emails/invoices/${encodeURIComponent(id)}/file`, {\n'
    F2_NEW = '    const res = await fetch(`${resolveRuntimeApiBase()}/api/emails/invoices/${encodeURIComponent(id)}/file`, {\n'

    def f2():
        b.mutate(EMAIL, F2_OLD, F2_NEW, 'F2')
    b.case('F2 把发票 PDF 下载改回裸 fetch ⇒ 无上界 ⇒ 前端卡口必须红', True,
           must_contain=['L1 台账少了一条'],
           mutate_fn=f2, runner=frontend_runner())

    # ── F3 必须红：把 version.ts 的 checkUpdate 改回裸 fetch ─────────────
    F3_OLD = '  const response = await fetchWithTimeout(`${resolveApiBase()}/api/app/check-update`, {\n'
    F3_NEW = '  const response = await fetch(`${resolveApiBase()}/api/app/check-update`, {\n'

    def f3():
        b.mutate(VERSION, F3_OLD, F3_NEW, 'F3')
    b.case('F3 把 checkUpdate 改回裸 fetch ⇒ 冷启动路径无上界 ⇒ 前端卡口必须红', True,
           must_contain=['L1 台账少了一条'],
           mutate_fn=f3, runner=frontend_runner())

    # ── F4 必须红：把 prefetchApk 改回**绑定形态**的裸 fetch ─────────────
    # ⚠ 这一族是 grep 找 `fetch(` **根本找不到**的（它是 `opts.fetchImpl ?? fetch`）
    #   ⇒ 正是「禁住一种写法，不等于禁住那件事」⇒ 由 AST 判据抓住。
    F4_OLD = ('  const doFetch = opts.fetchImpl\n'
              '    ?? ((u: string, i?: RequestInit) => fetchWithTimeout(u, i, { timeoutMs: APK_PREFETCH_TIMEOUT_MS }))\n')
    F4_NEW = '  const doFetch = opts.fetchImpl ?? fetch\n'

    def f4():
        b.mutate(VERSION, F4_OLD, F4_NEW, 'F4')
    b.case('F4 把 APK 预下载改回「绑定形态」的裸 fetch（grep 找不到的那一族）⇒ 前端卡口必须红', True,
           must_contain=['L1 台账少了一条'],
           mutate_fn=f4, runner=frontend_runner())

    # ── F5 必须红：掏空唯一那层的**计时器** ⇒ 兜底被掏空 ─────────────────
    F5_OLD = '    ? setTimeout(() => { timedOut = true; controller.abort() }, timeoutMs)\n'
    F5_NEW = '    ? setTimeout(() => { timedOut = true }, timeoutMs)\n'

    def f5():
        b.mutate(PRIM, F5_OLD, F5_NEW, 'F5')
    b.case('F5 掏掉唯一那层的 abort 计时器 ⇒ 全盘兜底失效 ⇒ 前端卡口必须红', True,
           must_contain=['没有自证有界'],
           mutate_fn=f5, runner=frontend_runner())

    # ── F6 必须红：前端量具**漏掉「{ signal } 简写形态」这一族** ──────────
    F6_OLD = ('    if (ts.isShorthandPropertyAssignment(p)) {\n'
              '      if (p.name.text === \'signal\') return true\n'
              '      continue\n'
              '    }\n')

    def f6():
        b.mutate(FGATE, F6_OLD, '', 'F6')
    b.case('F6 前端量具漏掉「{ signal } 简写形态」这一族 ⇒ 合成用例必须红 ⇒ 前端卡口必须红', True,
           must_contain=['自证臂'],
           mutate_fn=f6, runner=frontend_runner())

    # ── N1 负控：改台账理由的措辞 ⇒ 必须保持绿 ───────────────────────────
    N1_OLD = '出来的**本地内存**对象'
    N1_NEW = '出来的**本地内存**的那个对象'

    def n1():
        b.mutate(FGATE, N1_OLD, N1_NEW, 'N1')
    b.case('N1 前端台账的键写法微调（判据只看键与理由）⇒ 必须保持绿', False,
           must_not_contain=['L1 台账少了一条'],
           mutate_fn=n1, runner=frontend_runner())

    # ── N2 负控：新增一个**合法**的 fetch 站点（走唯一那层）⇒ 必须保持绿 ─
    N2_OLD = 'export interface FetchTimeoutOptions {\n'
    N2_NEW = ('/** s951ok 是合法的新增调用点（负控：必须保持绿）。 */\n'
              'export async function s951ok(url: string): Promise<Response> {\n'
              '  return fetchWithTimeout(url)\n'
              '}\n\n'
              'export interface FetchTimeoutOptions {\n')

    def n2():
        b.mutate(PRIM, N2_OLD, N2_NEW, 'N2')
    b.case('N2 新增一个走唯一那层的 fetch 调用点 ⇒ 这道门不是「任何改动都红」⇒ 必须保持绿', False,
           must_not_contain=['L1 台账少了一条'],
           mutate_fn=n2, runner=frontend_runner())

    # ── N3 负控：改唯一那层的注释措辞 ⇒ 必须保持绿 ──────────────────────
    N3_OLD = ' * ⚠⚠ **`timeoutMs: 0` 是「不设上限」，不是「忘了设」**\n'
    N3_NEW = ' * ⚠⚠ **`timeoutMs: 0` 是「不设上限」，不是「忘了设」（例外口）**\n'

    def n3():
        b.mutate(PRIM, N3_OLD, N3_NEW, 'N3')
    b.case('N3 唯一那层的注释措辞微调 ⇒ 必须保持绿', False,
           must_not_contain=['没有自证有界'],
           mutate_fn=n3, runner=frontend_runner())

    # ── N4 负控：后端加一个**不碰网络**的辅助函数 ⇒ 必须保持绿 ──────────
    N4_OLD = '// smtpCtxErr 在 SMTP 的阶段之间查一次 ctx。\n'
    N4_NEW = ('// s951ok 是不碰网络的辅助函数（负控：必须保持绿）。\n'
              'func s951ok(host string) string {\n'
              '\treturn host\n'
              '}\n\n'
              '// smtpCtxErr 在 SMTP 的阶段之间查一次 ctx。\n')

    def n4():
        b.mutate(SMTP, N4_OLD, N4_NEW, 'N4')
    b.case('N4 后端加一个不碰网络的辅助函数 ⇒ 后端卡口不是「任何改动都红」⇒ 必须保持绿', False,
           must_not_contain=['TestS951NoBannedDialForms'],
           mutate_fn=n4)

    # ── N5 负控：改既有台账（§945 swallowed_call）的措辞 ⇒ 必须保持绿 ────
    N5_OLD = '\t{"backend/internal/notify/smtp.go", "Close", 7},\n'
    N5_NEW = '\t{"backend/internal/notify/smtp.go", "Close", 7}, // §951：放弃出错的连接\n'

    def n5():
        b.mutate(SWALLOWED, N5_OLD, N5_NEW, 'N5')
    b.case('N5 §945 台账的措辞微调（判据只看条数）⇒ 必须保持绿', False,
           must_not_contain=['台账与源码条数不一致'],
           mutate_fn=n5)


if __name__ == '__main__':
    run(b, main, extra_runners=(notify_runner(),))

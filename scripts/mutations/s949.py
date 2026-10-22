#!/usr/bin/env python3
"""§949：后台 goroutine 的 panic 不许打死整个进程（收进 background 包 + 卡口）。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
   **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠ 本批次是本仓**第五批「把量具自己打坏」的变异**（§945 的 M8、§946 的 M13、
   §947 的 M3–M6、§948 的 M4–M8、本批的 **M9**）—— ⇒ 每次都必须证明：
   **量具坏了 ⇒ 门会红**。

判据命令：
  go test ./internal/repohygiene/ -run 'TestS949' -count=1

⚠ 本批次最该被记住的两条：

★ **M1/M2 是「兜底边界」的两侧**：
  M1 把一处后台任务改回**裸** `go`（无任何兜底）⇒ 门必须红。
  M2 把它改回**自带顶层 recover** 的 `go` ⇒ 门**仍然必须红** ——
  因为本卡的口径是**统一走 background.Go**，不是「有 recover 就行」
  ⇒ ⇒⇒ 理由：散落的 recover 各写各的日志格式 ⇒ 事后拼不出「是哪个任务」。

★ **M9 是「量具整族漏掉一种形态」**：
  判据识别 background.Go* 调用点时必须同时认三种形态：
    ① background.Go("x", fn)              —— 普通调用
    ② background.Go2[A, B]("x", fn, a, b) —— 泛型实例化（IndexListExpr）
    ③ background.Go2[A]("x", fn, a)       —— 单个类型实参（IndexExpr）
  把 ② 的识别打坏 ⇒ 仓里那 3 处带显式类型实参的调用点**整族消失**
  ⇒ ⇒⇒ 条数与台账都失配 ⇒ 门必须红。
  ⇒ 这正是 §948 的 M4（NullKeyword 整族漏检）同族：
  **「量出来的数比该有的少 ⇒ 第一嫌疑是量具漏了一族形态」。**

★ **M3/M4 是「兜底被掏空」**：
  recover 没了 ⇒ 兜底等于没有；日志没了 ⇒ panic 被**静默**吞掉
  ⇒ ⇒⇒ 后者正是 §945/§946/§948 这一串在消灭的东西：**失败不许无声**。
"""

import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    Batch, go_pkg_runner, run,
)

TARGETS = [
    'backend/internal/repohygiene/background_goroutine_test.go',
    'backend/internal/background/background.go',
    'backend/internal/agent/transport_stdio.go',
    'backend/internal/notify/smtp.go',
    'backend/internal/rss/scheduler.go',
    'backend/internal/registry/discovery.go',
]

b = Batch('§949 后台 goroutine 的 panic 不许打死整个进程（background 包 + 卡口 + 台账）',
          go_pkg_runner('./internal/repohygiene/', 'TestS949'), TARGETS)

GATE = b.targets[0]
BG = b.targets[1]
STDIO = b.targets[2]
SMTP = b.targets[3]
RSS = b.targets[4]
DISCOVERY = b.targets[5]


def main():
    # ── M1 必须红：把一处后台任务改回**裸** go（无兜底）─────────────────
    M1_OLD = 'background.Go("agent/stdio-wait", func() { done <- t.cmd.Wait() })'
    M1_NEW = 'go func() { done <- t.cmd.Wait() }()'

    def m1():
        b.mutate(STDIO, M1_OLD, M1_NEW, 'M1')
    b.case('M1 把后台任务改回裸 go（无任何兜底）⇒ panic 会打死整个进程 ⇒ 判据必须红', True,
           must_contain=['有一处 `go` 语句没有走 background.Go'],
           mutate_fn=m1)

    # ── M2 必须红：改回**自带顶层 recover** 的 go（口径是统一，不是「有 recover 就行」）──
    M2_NEW = ('go func() { defer func() { _ = recover() }(); done <- t.cmd.Wait() }()')

    def m2():
        b.mutate(STDIO, M1_OLD, M2_NEW, 'M2')
    b.case('M2 把后台任务改回自带 recover 的 go ⇒ 口径是**统一走 background.Go** ⇒ 判据必须红', True,
           must_contain=['有一处 `go` 语句没有走 background.Go'],
           mutate_fn=m2)

    # ── M3 必须红：兜底实现里**没有 recover** ⇒ 兜底等于没有 ──────────
    M3_OLD = (
        '\t\tdefer func() {\n'
        '\t\t\tif r := recover(); r != nil {\n'
        '\t\t\t\tlog.Printf("[PANIC] background goroutine %q panicked: %v\\n%s", name, r, debug.Stack())\n'
        '\t\t\t}\n'
        '\t\t}()\n'
    )
    M3_NEW = (
        '\t\tdefer func() {\n'
        '\t\t\tif r := any(nil); r != nil {\n'
        '\t\t\t\tlog.Printf("[PANIC] background goroutine %q panicked: %v\\n%s", name, r, debug.Stack())\n'
        '\t\t\t}\n'
        '\t\t}()\n'
    )

    def m3():
        b.mutate(BG, M3_OLD, M3_NEW, 'M3')
    b.case('M3 掏掉兜底实现里的 recover() ⇒ 兜底等于没有 ⇒ 判据必须红', True,
           must_contain=['**没有 recover()**'],
           mutate_fn=m3)

    # ── M4 必须红：兜底实现里**没有日志** ⇒ panic 被静默吞掉 ──────────
    M4_NEW = (
        '\t\tdefer func() {\n'
        '\t\t\tif r := recover(); r != nil {\n'
        '\t\t\t\t_ = r\n'
        '\t\t\t\t_ = debug.Stack\n'
        '\t\t\t}\n'
        '\t\t}()\n'
    )

    def m4():
        b.mutate(BG, M3_OLD, M4_NEW, 'M4')
    b.case('M4 掏掉兜底实现里的日志 ⇒ panic 被**静默**吞掉 ⇒ 判据必须红', True,
           must_contain=['recover 之后**没有日志**'],
           mutate_fn=m4)

    # ── M5 必须红：任务名改空 ⇒ panic 日志里定位不到是哪个任务 ─────────
    M5_OLD = 'background.Go("notify/smtp-send", func() {'
    M5_NEW = 'background.Go("", func() {'

    def m5():
        b.mutate(SMTP, M5_OLD, M5_NEW, 'M5')
    b.case('M5 把任务名改空 ⇒ panic 日志里没有任务名 ⇒ 判据必须红', True,
           must_contain=['第一个参数（任务名）是空的'],
           mutate_fn=m5)

    # ── M6 必须红：两个任务同名 ⇒ 同样定位不到 ─────────────────────────
    M6_OLD = 'background.Go("rss/scan-scope", func() {'
    M6_NEW = 'background.Go("rss/scheduler-loop", func() {'

    def m6():
        b.mutate(RSS, M6_OLD, M6_NEW, 'M6')
    b.case('M6 两个后台任务同名 ⇒ panic 日志分不出是哪个 ⇒ 判据必须红', True,
           must_contain=['重复'],
           mutate_fn=m6)

    # ── M7 必须红：任务改名但台账没同步 ⇒ 台账双向失配 ─────────────────
    M7_OLD = 'background.Go1("registry/probe-candidate", func(hp hostPort) {'
    M7_NEW = 'background.Go1("registry/probe-candidate-v2", func(hp hostPort) {'

    def m7():
        b.mutate(DISCOVERY, M7_OLD, M7_NEW, 'M7')
    b.case('M7 改了任务名但台账没同步 ⇒ 台账双向相等失配 ⇒ 判据必须红', True,
           must_contain=['台账里没有登记'],
           mutate_fn=m7)

    # ── M8 必须红：新增一个没登记的后台任务 ⇒ 条数与台账都失配 ─────────
    M8_OLD = '\t\tbackground.Go("rss/scan-scope", func() {\n'
    M8_NEW = ('\t\tbackground.Go("rss/extra-task", func() {})\n'
              '\t\tbackground.Go("rss/scan-scope", func() {\n')

    def m8():
        b.mutate(RSS, M8_OLD, M8_NEW, 'M8')
    b.case('M8 新增一个没登记的后台任务 ⇒ 绝对条数与台账都失配 ⇒ 判据必须红', True,
           must_contain=['background.Go* 调用点总数'],
           mutate_fn=m8)

    # ── M9 必须红：量具**整族漏掉「泛型实例化」形态** ⇒ 3 个调用点消失 ──
    M9_OLD = ('\tcase *ast.IndexListExpr:\n'
              '\t\treturn s949BackgroundCallee(v.X)\n')

    def m9():
        b.mutate(GATE, M9_OLD, '', 'M9')
    b.case('M9 量具漏掉「泛型实例化」形态（background.Go2[A,B]）⇒ 3 个调用点整族消失 ⇒ 判据必须红', True,
           must_contain=['扫描器坏了'],
           mutate_fn=m9)

    # ── M10 必须红：在 background 包里多起一个裸 goroutine ⇒ 绝对条数失配 ──
    M10_OLD = 'func Go(name string, fn func()) {\n'
    M10_NEW = (
        '// GoExtra 是**故意**多起一个裸 goroutine 的辅助函数（变异用）。\n'
        'func GoExtra(name string, fn func()) {\n'
        '\tgo func() {\n'
        '\t\tfn()\n'
        '\t}()\n'
        '}\n'
        '\n'
        'func Go(name string, fn func()) {\n'
    )

    def m10():
        b.mutate(BG, M10_OLD, M10_NEW, 'M10')
    b.case('M10 在 background 包里多起一个裸 goroutine ⇒ 「全后端 go 语句总数」失配 ⇒ 判据必须红', True,
           must_contain=['`go` 语句总数'],
           mutate_fn=m10)

    # ── N1 负控：改注释 ⇒ 必须保持绿 ─────────────────────────────────
    N1_OLD = '//\tname 只用于日志定位（哪个后台任务炸了）⇒ 不许留空。\n'
    N1_NEW = '//\tname 只用于日志定位（哪个后台任务炸了）⇒ 不许留空（否则事后拼不出来）。\n'

    def n1():
        b.mutate(BG, N1_OLD, N1_NEW, 'N1')
    b.case('N1 兜底实现的注释措辞微调 ⇒ 必须保持绿', False,
           must_not_contain=['有一处 `go` 语句没有走 background.Go'],
           mutate_fn=n1)

    # ── N2 负控：改日志措辞 ⇒ 必须保持绿 ─────────────────────────────
    N2_OLD = 'log.Printf("[PANIC] background goroutine %q panicked: %v\\n%s", name, r, debug.Stack())'
    N2_NEW = 'log.Printf("[PANIC] 后台任务 %q panic: %v\\n%s", name, r, debug.Stack())'

    def n2():
        b.mutate(BG, N2_OLD, N2_NEW, 'N2')
    b.case('N2 兜底日志的措辞微调（判据只看「有没有日志调用」）⇒ 必须保持绿', False,
           must_not_contain=['recover 之后**没有日志**'],
           mutate_fn=n2)

    # ── N3 负控：调整调用点的格式（语义等价）⇒ 必须保持绿 ─────────────
    N3_OLD = 'background.Go("notify/smtp-send", func() {\n'
    N3_NEW = 'background.Go(\n\t\t"notify/smtp-send",\n\t\tfunc() {\n'

    def n3():
        b.mutate(SMTP, N3_OLD, N3_NEW, 'N3')
    b.case('N3 调用点的换行格式调整（语义等价）⇒ 必须保持绿', False,
           must_not_contain=['background.Go* 调用点总数'],
           mutate_fn=n3)

    # ── N4 负控：background 包里加一个**不含 go** 的辅助函数 ⇒ 必须绿 ──
    N4_OLD = 'func Go(name string, fn func()) {\n'
    N4_NEW = (
        '// Describe 只是描述一个后台任务（变异用，不起 goroutine）。\n'
        'func Describe(name string) string {\n'
        '\treturn "background task: " + name\n'
        '}\n'
        '\n'
        'func Go(name string, fn func()) {\n'
    )

    def n4():
        b.mutate(BG, N4_OLD, N4_NEW, 'N4')
    b.case('N4 background 包里加一个**不起 goroutine** 的辅助函数 ⇒ 必须保持绿', False,
           must_not_contain=['`go` 语句总数'],
           mutate_fn=n4)


if __name__ == '__main__':
    run(b, main)

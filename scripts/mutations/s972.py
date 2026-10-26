#!/usr/bin/env python3
"""§972：「间歇红」的第二形态 —— **竞态夹具**（准点的红 · 负载/调度依赖）。

⚠ 为什么本批次的方向与「修复前绿 / 修复后红」**相反**：
  §970 那批是「**守门缺口**」形态 ⇒ 同一个注入，**修复前绿、修复后红**
  （注入让门从绿变红 ⇒ 证明门此前漏掉了那个洞）。
  本批是「**竞态夹具**」形态 ⇒ 同一个注入，**修复前红、修复后绿**
  （注入把「本来就偶发」的那条竞态变成**确定性** ⇒ 证明夹具此前在赌运气，
    而修法把它吸收掉了）。
  ⇒ ⇒⇒ 两种形态的判定方向**相反** ⇒ 混用就会得出「修完反而更红」的假结论。

本批要证明两件**互相独立**的事（缺一不可 ⇒ 只做一件等于没验）：

  V1（竞态被吸收）：把「指标自增滞后于投递」变成确定性的 30ms ⇒
      · 修复前的夹具（单次采样指标）⇒ **必须红**（原来靠运气，现在是必红）
      · 修复后的夹具（等指标本身）  ⇒ **必须绿**（等待吸收了滞后）
  V2（判据仍有牙）：把「指标永不自增」注入生产码 ⇒
      · 修复后的夹具 ⇒ **必须红**（等再久也等不到 ⇒ 判据没被改弱）

⚠⚠ **V2 是 V1 的必要配对**：只做 V1 的话，「等指标」有可能把判据改成
   「永远等不到就跳过」这种**恒绿**形态 ⇒ ⇒⇒ V2 证明它等不到时**照样红**。

⚠ **本批次必须在隔离 worktree 里跑**（§935/§964 的教训）：
   harness 是**原地**改源码再还原的 ⇒ ⇒⇒ 在共享工作树上跑 ⇒
   别的会话在同期构建 ⇒ 编译到的可能是**被变异的那份**源码 ⇒ 假绿 + 假红。
   ⇒ ⇒⇒ 本批的运行方式：**先提交修复，再建隔离 worktree，在 worktree 里跑本脚本**
   （脚本放在 worktree 里 ⇒ harness 的 REPO ＝ worktree 根 ⇒ 共享树全程不动）。

判据命令：
  go test ./internal/opencode/ -run 'TestEventStreamManager' -count=1
"""
import subprocess
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    Batch, HarnessError, go_pkg_runner, run,
)

TARGETS = [
    # 被测的生产文件（V1/V2 的注入点都在这里）
    'backend/internal/opencode/event_stream.go',
    # 判据文件（V1-orig 要把它换成**修复前**的那份 ⇒ 必须是 target ⇒ harness 负责还原）
    'backend/internal/opencode/event_stream_test.go',
]

# ⚠ 修复前的提交（CI 当时跑的那份夹具）⇒ V1-orig 的判据必须是它
PRE_FIX_COMMIT = '1f7d1688'

_RUNNERS = {}


def _runner():
    """判据的**运行器**（harness 的 `_go_runner` 闭包，带 `with_fresh_cache`）。"""
    if 'main' not in _RUNNERS:
        _RUNNERS['main'] = go_pkg_runner(
            './internal/opencode/', 'TestEventStreamManager')
    return _RUNNERS['main']


def judge():
    """判据：**跑**一遍 ⇒ 返回 (exit_code, 输出文本)。

    ⚠ harness 要的是**这个二元组**，不是运行器本身 ⇒ ⇒⇒ 所以这里必须
      **调用** `_runner()`，不能把它直接交给 `Batch` 当 runner。
    """
    return _runner()()


def judge_fresh():
    """换一份**全新**缓存再跑一遍（排除「变异没进二进制」）。"""
    return _runner().with_fresh_cache()


# ⚠ harness 的 `Batch.case` 在「该红却绿」时会调 `run.with_fresh_cache`
#   ⇒ ⇒⇒ **包装过一层就必须把这条属性透出来** ⇒ 否则那条兜底会静默失效
#   ⇒ ⇒⇒ 而它失效的后果是「把『门没牙』误判成『门没牙』」—— 方向反了。
judge.with_fresh_cache = judge_fresh


b = Batch('§972「竞态夹具」形态：同一个注入，修复前红 / 修复后绿',
          judge, TARGETS)

PROD = b.targets[0]
JUDGE_FILE = b.targets[1]

# ── V1：把「指标自增滞后于投递」变成确定性的 30ms ─────────────────────
#   ⚠ 这是**确定性代理**：真实成因是调度抢占（µs 级），30ms 让它**可观测**。
#   ⚠ 30ms 不是随便取的：5 个事件 × 30ms = 150ms ≪ SlowSubscriber 那条
#     用例的 2s 等待窗 ⇒ 不会把**别的**用例拖红（否则就分不清是谁红了）。
DELAY_OLD = '\t\t\tm.fanout(domain)\n\t\t\tm.incEvent()\n'
DELAY_NEW = ('\t\t\tm.fanout(domain)\n'
             '\t\t\ttime.Sleep(30 * time.Millisecond)\n'
             '\t\t\tm.incEvent()\n')

# ── V2：指标**永不自增** ⇒ 等再久也等不到 ───────────────────────────────
V2_OLD = '\t\t\tm.fanout(domain)\n\t\t\tm.incEvent()\n'
V2_NEW = '\t\t\tm.fanout(domain)\n'


def restore_pre_fix_judge():
    """把判据文件换回**修复前**的那份（= CI 当时跑的那份）。"""
    p = subprocess.run(
        ['git', 'show',
         '%s:backend/internal/opencode/event_stream_test.go' % PRE_FIX_COMMIT],
        capture_output=True)
    if p.returncode != 0:
        raise HarnessError(
            '取不到修复前的判据文件（提交 %s）⇒ V1-orig 无法运行：\n%s'
            % (PRE_FIX_COMMIT, p.stderr.decode('utf-8', 'replace')))
    JUDGE_FILE.write_bytes(p.stdout)


def main():
    # ── V1-orig 必须红：修复前的夹具 + 确定性滞后 ⇒ 必红 ────────────────
    #   ⚠ 这条的含义：「原来靠运气」被换成「原来**必**红」⇒
    #     ⇒ 证明 (a) 竞态真实存在，(b) 我们量到的就是**那条**竞态。
    def v1_orig():
        b.mutate(PROD, DELAY_OLD, DELAY_NEW, 'V1-delay')
        restore_pre_fix_judge()

    b.case('V1-orig 修复前的夹具 + 指标滞后 30ms ⇒ 判据必须红（原来靠运气，现在必红）',
           True,
           must_contain=['expected TotalEvents > 0, got 0'],
           mutate_fn=v1_orig)

    # ── V1-fixed 必须绿：修法（等指标本身）吸收了这条滞后 ────────────────
    def v1_fixed():
        b.mutate(PROD, DELAY_OLD, DELAY_NEW, 'V1-delay')

    b.case('V1-fixed 修法后的夹具 + 同一个注入 ⇒ 判据必须绿（竞态被吸收）',
           False,
           must_not_contain=['FAIL'],
           mutate_fn=v1_fixed)

    # ── V2 必须红：指标永不自增 ⇒ 修法**等不到** ⇒ 判据必须红 ────────────
    #   ⚠ 这是 V1 的必要配对：证明「等指标」不是「等不到就跳过」的恒绿形态。
    def v2_broken_counter():
        b.mutate(PROD, V2_OLD, V2_NEW, 'V2-no-counter')

    b.case('V2 指标永不自增 ⇒ 修法后的夹具仍必须红（判据没被改弱）',
           True,
           must_contain=['expected TotalEvents > 0, got 0'],
           mutate_fn=v2_broken_counter)

    # ── N1 负控：语义等价改动（只加一行注释）⇒ 门必须保持绿 ───────────────
    N1_OLD = '\t\t\tm.fanout(domain)\n\t\t\tm.incEvent()\n'
    N1_NEW = '\t\t\tm.fanout(domain)\n\t\t\tm.incEvent() // 负控：只加一行注释\n'

    def n1():
        b.mutate(PROD, N1_OLD, N1_NEW, 'N1-comment')

    b.case('N1 负控：只加一行注释 ⇒ 门必须保持绿（不许误报）', False,
           must_not_contain=['FAIL'],
           mutate_fn=n1)

    # ── 收尾自证：还原态下判据必须绿 ──────────────────────────────────
    b.case('收尾自证：还原态下判据必须绿', False,
           must_not_contain=['FAIL'],
           runner=judge)


if __name__ == '__main__':
    run(b, main)

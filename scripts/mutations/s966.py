#!/usr/bin/env python3
"""§966：「**写进 PG 的时刻**」这条纪律的变异批次。

⚠ 为什么本批次**必须落盘**（§935 的教训）：§935 发现「当时的变异脚本没落盘」
   ⇒ 「当时绿 = 现在绿」**永久不可回溯** ⇒ ⇒ 本仓从此规定：**变异脚本要落盘**。
   ⇒ ⇒ ★ 而 §964 那批**没有落盘**（脚本不存在）⇒ 那是本仓的一个缺口，
   ⇒ ⇒ ⇒ 本批次把它补上的一部分。

⚠ 本批次**需要真 PG**：判据量的是「驱动到底怎么编码 timestamptz」
   （§966 的真机读数：参数绑定路径 = 驱动在客户端**向下截断**；
   SQL 字面量路径 = PG 自己 round-half-to-even）⇒ ⇒ 没有 PG 时用例会 skip，
   而 skip 的退出码是 0 ⇒ ⇒ 本批次在缺 PG 时**拒绝跑**（退出码 3），
   而不是「跑了个寂寞」。见 `_harness.require_pg_dsn` 与
   `go_pkg_runner(forbid_skip=True)`。

⚠⚠ **本批次要盯的「前提」不是「实现」**：
   §966 订正了 §964 的一个错前提（原文写「PG 会把亚微秒部分四舍五入」，
   而实测**参数绑定路径是驱动在客户端就向下截断了**）⇒ ⇒ ⇒ 那么
   「pgtime 必须向下截断」这条结论**建立在哪个前提上**？
   ⇒ ⇒ ⇒ 建立在「**驱动向下截断**」上 ⇒ ⇒ ⇒ 而那个前提是**第三方的行为**
   ⇒ ⇒ ⇒ ⇒ 所以 M6 专门把「判据的期望」打坏一次
   （把 floor 改成 round）⇒ 判据必须红 ⇒ ⇒ ⇒ 证明它**真的在量取整方向**，
   不是恒真。

判据命令：
  go test ./internal/pgtime/   -run 'Test(Driver|Truncate|Now)' -count=1
  go test ./internal/meeting/ -run 'TestS966' -count=1
"""
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    Batch, go_pkg_runner, require_pg_dsn, run,
)

TARGETS = [
    # 被测的生产文件
    'backend/internal/pgtime/pgtime.go',
    'backend/internal/meeting/pg_store.go',
    'backend/internal/meeting/store.go',
    # ⚠ 判据本身（允许「把量具自己打坏」的变异 —— M6 要用它证明判据有牙）
    'backend/internal/pgtime/pg_driver_test.go',
]

_RUNNERS = {}


def runners():
    if not _RUNNERS:
        # ⛔ 只注入**测试专用** DSN ⇒ 绝不碰生产库。
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        _RUNNERS['pgtime'] = go_pkg_runner(
            './internal/pgtime/', 'Test(Driver|Truncate|Now)', env=env, forbid_skip=True)
        _RUNNERS['meeting'] = go_pkg_runner(
            './internal/meeting/', 'TestS966', env=env, forbid_skip=True)
    return _RUNNERS


def combined():
    """两个包的判据都跑 ⇒ 任一红就算红（收尾时两者都必须绿）。"""
    r = runners()
    p_rc, p_out = r['pgtime']()
    m_rc, m_out = r['meeting']()
    return ((p_rc or m_rc), p_out + '\n\n' + m_out)


b = Batch('§966「写进 PG 的时刻」：单一事实源 pgtime / 向下截断 / 两个实现同刻度',
          combined, TARGETS)

PGTIME = b.targets[0]
PGSTORE = b.targets[1]
MEMSTORE = b.targets[2]
DRIVER_JUDGE = b.targets[3]


def main():
    # ── M1 必须红：pgtime 改成**四舍五入** ⇒ 落盘值带亚微秒部分 ⇒ 驱动再截一次
    #      ⇒ 「内存值 == 库值」不成立 ⇒ 门必须红。
    #   ⚠ 这是 §964 那批 M1 的对应物（锚点从 pgTruncate 搬到了 pgtime.Truncate）。
    M1_OLD = '\treturn t.Truncate(time.Microsecond)\n'
    M1_NEW = '\treturn t.Round(time.Microsecond)\n'

    def m1():
        b.mutate(PGTIME, M1_OLD, M1_NEW, 'M1')
    b.case('M1 pgtime 改成 Round ⇒ 落盘值带亚微秒部分 ⇒ 门必须红', True,
           must_contain=['必须向下截断到微秒'],
           mutate_fn=m1)

    # ── M2 必须红：pgtime 干脆**不截断** ⇒ 写进去的值带 ns 尾 ⇒
    #      库里读回来的是截断后的值 ⇒ 两者不等 ⇒ 门必须红。
    M2_OLD = '\treturn t.Truncate(time.Microsecond)\n'
    M2_NEW = '\treturn t\n'

    def m2():
        b.mutate(PGTIME, M2_OLD, M2_NEW, 'M2')
    b.case('M2 pgtime 不截断 ⇒ 写进去的值与库里读回来的不等 ⇒ 门必须红', True,
           must_contain=['必须被截断'],
           mutate_fn=m2)

    # ── M3 必须红：PG 版绕过 pgtime（自己也不截断）⇒ 门必须红。
    M3_OLD = '\treturn pgtime.Truncate(s.nowFunc())\n'
    M3_NEW = '\treturn s.nowFunc()\n'

    def m3():
        b.mutate(PGSTORE, M3_OLD, M3_NEW, 'M3')
    b.case('M3 PG 版不截断 ⇒ 返回给调用方的值带 ns 尾 ⇒ 门必须红', True,
           must_contain=['UpdateMetaScoped 返回的时刻必须已被截断'],
           mutate_fn=m3)

    # ── M4 必须红：内存版绕过 pgtime ⇒ 两个实现分叉 ⇒ 门必须红。
    M4_OLD = '\treturn pgtime.Truncate(s.nowFunc())\n'
    M4_NEW = '\treturn s.nowFunc()\n'

    def m4():
        b.mutate(MEMSTORE, M4_OLD, M4_NEW, 'M4')
    b.case('M4 内存版不截断 ⇒ 两个实现分叉 ⇒ 门必须红', True,
           must_contain=['必须逐字相同'],
           mutate_fn=m4)

    # ── M5 必须红：PG 版**不理会注入的时钟**（改回真实时钟）⇒
    #      「写进去的值 == 返回给调用方的值」这条判据就量不到 store 自己的截断
    #      ⇒ 门必须红（否则那条判据只量到了「注入值恰好是什么」）。
    M5_OLD = '\tcreatedAt := s.now()\n'
    M5_NEW = '\tcreatedAt := time.Now()\n'

    def m5():
        b.mutate(PGSTORE, M5_OLD, M5_NEW, 'M5')
    b.case('M5 PG 版不理会注入的时钟 ⇒ 门必须红', True,
           must_contain=['必须被截断'],
           mutate_fn=m5)

    # ── M6 必须红（**量具自证**）：把驱动判据的期望从「向下截断」改成「进位」
    #      ⇒ 判据必须红 ⇒ ⇒ ⇒ 证明它**真的在量取整方向**，不是恒真。
    #   ⚠ 这条的意义：§966 订正的正是「取整方向」这件事
    #   （§964 说 PG 四舍五入，实测是驱动向下截断）
    #   ⇒ ⇒ ⇒ 若这条判据对方向不敏感，那次订正就没有任何判据守着。
    M6_OLD = '\t\t\tif !ts.Equal(floored) {\n'
    M6_NEW = '\t\t\tif !ts.Equal(floored.Add(time.Microsecond)) {\n'

    def m6():
        b.mutate(DRIVER_JUDGE, M6_OLD, M6_NEW, 'M6')
    b.case('M6 量具自证：把驱动判据的期望改成「进位」⇒ 判据必须红（它真在量取整方向）', True,
           must_contain=['必须是 floor'],
           mutate_fn=m6)

    # ── N1 负控：只加一行注释 ⇒ 门必须保持绿（不许误报）。
    N1_OLD = '\treturn t.Truncate(time.Microsecond)\n'
    N1_NEW = '\treturn t.Truncate(time.Microsecond) // 负控：只加一行注释\n'

    def n1():
        b.mutate(PGTIME, N1_OLD, N1_NEW, 'N1')
    b.case('N1 负控：只加一行注释 ⇒ 门必须保持绿（不许误报）', False,
           must_not_contain=['FAIL'],
           mutate_fn=n1)

    # ── 收尾自证：还原态下判据必须绿 ──────────────────────────────────
    b.case('收尾自证：还原态下判据必须绿', False,
           must_not_contain=['FAIL'],
           runner=combined)


if __name__ == '__main__':
    run(b, main)

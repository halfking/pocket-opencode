#!/usr/bin/env python3
"""§964：「写进 PG 的时间戳一律先截断到存储精度」那节的**变异批次重建**。

⚠⚠⚠ **为什么这一批是「重建」而不是「原样」**（§935 的教训）：
   §964 当时**没有落盘**它的变异脚本（§966 才把 s966.py 落盘，并把这个缺口登记在案）
   ⇒ ⇒ 「§964 当时绿 = 现在绿」**永久不可回溯**
   ⇒ ⇒ ⇒ 所以这一批是**按 §964.4.1 记录的那 7 条臂重建**的等价变异
   ⇒ ⇒ ⇒ ★ 它证明的是「**那些判据现在还有牙**」
   ⇒ ⇒ ⇒ ⇒ **它证明不了「§964 当时真的跑过那 7 条」**（历史读数没有第二份副本）
   ⇒ ⇒ ⇒ ⇒ ⇒ 这正是 §935 对 §919–§922 的同一条结论，别把两件事混为一谈。

⚠ **三条臂的落点随 §966 变了**（§966 把那份约定收编成单一事实源 `internal/pgtime`）：
   ① M1 的**目标判据**从 meeting 包的 `TestPGTruncateFloorsToMicrosecond`
      搬到了 pgtime 包的 `TestTruncateFloorsToMicrosecond`（覆盖面逐字保留）；
      变异锚点也随之从 `pgTruncate` 变成 `pgtime.Truncate`。
   ② M2a/M2b 的**变异锚点**从 `pgNow()` 变成 `(*PGStore).now()`（§966 把它改成走 pgtime）。
   ⇒ ⇒ ⇒ 判据本身没变弱，只是搬家了。

⚠⚠ **两条臂是「平台/时刻相关」的，不是恒定有牙的** ⇒ ⇒ 本批把它们**显式区分**开：
   ① **M2a**（把 store 的时钟退回 `time.Now()`）：在**微秒对齐**的平台上（macOS）
      **必然绿** ⇒ ⇒ 它的牙在 **ns 时钟**的平台（Linux CI）上
      ⇒ ⇒ ⇒ 本批**先探测本机时钟精度**，再决定这条臂的期望（不写死）。
   ② **M5b**（把 RSS 夹具的 `day` 退回 `now`）：它只在 **UTC 00:00–01:00** 有牙
      （`rss.DigestDayRange` 取的是 **UTC 日历窗** ⇒ 窗口外两条夹具都在今天 ⇒ 恒绿）
      ⇒ ⇒ ⇒ 本批同样**先探测当前 UTC 小时** ⇒ 窗口外**如实报告「本轮未验证」**
      ⇒ ⇒ ⇒ ⇒ **恒绿 ≠ 通过** —— 这条正是 §964 那个「每天必红的原状红」的**根因证明**。

⚠ 本批次**需要真 PG**（M2a/M2b/M4b/M5 都量的是**落库之后的行**）
   ⇒ ⇒ 缺 PG 时用例会 skip，而 skip 的退出码是 0
   ⇒ ⇒ ⇒ 本批次在缺 PG 时**拒绝跑**（退出码 3），而不是「跑了个寂寞」。
   见 `_harness.require_pg_dsn` 与 `go_pkg_runner(forbid_skip=True)`。

判据命令：
  go test ./internal/pgtime/    -run 'TestTruncateFloorsToMicrosecond' -count=1
  go test ./internal/meeting/   -run 'TestPGTimeEqualTolerance|TestS964TombstoneFixture' -count=1
  go test ./internal/meeting/   -run 'TestPGUpdateMetaScopedLeavesTranscriptUntouched|TestS943TombstoneGuard' -count=1
  go test ./internal/server/    -run 'TestRSSDigestRoutesEndToEnd' -count=1
"""
import sys
import time as _time

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
    # ⚠ 判据/夹具本身（允许「把量具自己打坏」「把夹具改坏」的变异 ——
    #   §964 的 M3/M4a/M4b 打的就是它们）
    'backend/internal/meeting/transcript_test.go',
    'backend/internal/meeting/store_tombstone_scope_test.go',
    'backend/internal/server/rss_starter_routes_pg_test.go',
]

_RUNNERS = {}


def runners():
    if not _RUNNERS:
        # ⛔ 只注入**测试专用** DSN ⇒ 绝不碰生产库。
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        _RUNNERS['pgtime'] = go_pkg_runner(
            './internal/pgtime/', 'TestTruncateFloorsToMicrosecond', env=env, forbid_skip=True)
        _RUNNERS['meeting_pure'] = go_pkg_runner(
            './internal/meeting/', 'TestPGTimeEqualTolerance|TestS964TombstoneFixture',
            env=env, forbid_skip=True)
        _RUNNERS['meeting_pg'] = go_pkg_runner(
            './internal/meeting/',
            'TestPGUpdateMetaScopedLeavesTranscriptUntouched|TestS943TombstoneGuard',
            env=env, forbid_skip=True)
        _RUNNERS['server'] = go_pkg_runner(
            './internal/server/', 'TestRSSDigestRoutesEndToEnd', env=env, forbid_skip=True)
    return _RUNNERS


def platform_clock_has_nanosecond_precision(samples=200):
    """本机时钟有没有纳秒精度（决定 M2a 的期望）。

    ⚠ 为什么要探测而不是写死：M2a 的牙**在 ns 时钟的平台上**
    （Linux CI 恒红、macOS 恒绿）⇒ ⇒ 写死期望就是**把平台差异藏在脚本里**。
    """
    tails = {_time.time_ns() % 1000 for _ in range(samples)}
    return 0 not in tails


def utc_hour_now():
    return _time.gmtime().tm_hour


def combined():
    """四条判据都跑 ⇒ 任一红就算红（收尾时四条都必须绿）。"""
    r = runners()
    rc1, o1 = r['pgtime']()
    rc2, o2 = r['meeting_pure']()
    rc3, o3 = r['meeting_pg']()
    rc4, o4 = r['server']()
    return ((rc1 or rc2 or rc3 or rc4), o1 + '\n' + o2 + '\n' + o3 + '\n' + o4)


b = Batch('§964 变异批次**重建**：「写进 PG 的时刻先截断到存储精度」那条纪律',
          combined, TARGETS)

# ⚠ 模块级赋值（形态照抄 s966.py）：锚点预检器**只认**模块级的 `X = b.targets[N]`
# ⇒ ⇒ 写在函数体里 ⇒ 预检解析不出目标路径 ⇒ 「UNRESOLVED」。
PGTIME = b.targets[0]
PGSTORE = b.targets[1]
TRANSCRIPT = b.targets[2]
TOMBSTONE = b.targets[3]
RSS = b.targets[4]


def main():
    r = runners()

    ns_clock = platform_clock_has_nanosecond_precision()
    hour = utc_hour_now()
    print('  · 本机时钟有纳秒精度：%s（⇒ 决定 M2a 的期望）' % ('是' if ns_clock else '否（微秒对齐）'))
    print('  · 当前 UTC 小时：%02d（⇒ 决定 M5b 的期望；M5b 只在 00–01 点有牙）' % hour)

    # ── M1 必须红：截断改成**四舍五入** ⇒ 落盘值带亚微秒部分 ⇒ 门必须红。
    #   ⚠ §966 把这个函数搬到了 pgtime 包（单一事实源）⇒ 目标判据随之搬到
    #   pgtime 包的 TestTruncateFloorsToMicrosecond（覆盖面逐字保留）。
    M1_OLD = '\treturn t.Truncate(time.Microsecond)\n'
    M1_NEW = '\treturn t.Round(time.Microsecond)\n'

    def m1():
        b.mutate(PGTIME, M1_OLD, M1_NEW, 'M1')
    b.case('M1 截断改成 Round ⇒ 落盘值带亚微秒部分 ⇒ 门必须红', True,
           must_contain=['必须向下截断到微秒'],
           mutate_fn=m1, runner=r['pgtime'])

    # ── M2a：**平台相关**的一条 ⇒ 期望由本机时钟精度决定，不写死。
    #   变异：store 的时钟退回 `time.Now()`（即 §964 当年的「pgNow 退回 time.Now」）。
    M2_OLD = '\treturn pgtime.Truncate(s.nowFunc())\n'
    M2A_NEW = '\treturn time.Now()\n'

    def m2a():
        b.mutate(PGSTORE, M2_OLD, M2A_NEW, 'M2a')
    if ns_clock:
        m2a_expect = 'M2a 退回 time.Now()（ns 时钟平台：牙在这里）⇒ 门必须红'
        m2a_expect_red = True
    else:
        m2a_expect = ('M2a 退回 time.Now()（µs 时钟平台：这条臂**必然绿** ⇒ '
                      '它的牙在 Linux CI 上 ⇒ 本轮只作读数，不作证据）')
        m2a_expect_red = False
    b.case(m2a_expect, m2a_expect_red,
           must_not_contain=['FAIL'] if not m2a_expect_red else (),
           mutate_fn=m2a, runner=r['meeting_pg'])

    # ── M2b 必须红：注入一个**确定的 ns 尾**（模拟 ns 时钟的平台）⇒ 门必须红。
    #   ⚠ 这条是 M2a 的**确定性**版本 ⇒ 它证明「那条严格等值判据真的有牙」。
    M2B_NEW = '\treturn time.Now().Add(123 * time.Nanosecond)\n'

    def m2b():
        b.mutate(PGSTORE, M2_OLD, M2B_NEW, 'M2b')
    b.case('M2b 退回 time.Now()+123ns（模拟 ns 时钟）⇒ 门必须红', True,
           must_contain=['FAIL'],
           mutate_fn=m2b, runner=r['meeting_pg'])

    # ── M3 必须红：容差带收到 0 ⇒ 「容差带非空」那条正控红。
    M3_OLD = '\treturn d <= time.Microsecond && d >= -time.Microsecond\n'
    M3_NEW = '\treturn d == 0\n'

    def m3():
        b.mutate(TRANSCRIPT, M3_OLD, M3_NEW, 'M3')
    b.case('M3 容差带收到 0 ⇒ 「容差带必须真的被用上」那条正控 ⇒ 门必须红', True,
           must_contain=['FAIL'],
           mutate_fn=m3, runner=r['meeting_pure'])

    # ── M4a 必须红：墓碑夹具**去掉 ns 尾** ⇒ 「夹具让容差带非空」这件事没人守了。
    #   ⚠ §964 第一版把这条臂的预期写反了（以为绿）⇒ 实测是红 ⇒ 那个纠正
    #   **暴露了真实缺口** ⇒ 缺口已修（夹具与判据共用 s943PlantedAt）。
    M4A_OLD = '\treturn time.Now().Add(-2 * time.Hour).Truncate(time.Microsecond).Add(123 * time.Nanosecond)\n'
    M4A_NEW = '\treturn time.Now().Add(-2 * time.Hour).Truncate(time.Microsecond)\n'

    def m4a():
        b.mutate(TOMBSTONE, M4A_OLD, M4A_NEW, 'M4a')
    b.case('M4a 墓碑夹具去掉 ns 尾 ⇒ 「夹具的约定没人守」⇒ 门必须红', True,
           must_contain=['123ns'],
           mutate_fn=m4a, runner=r['meeting_pure'])

    # ── M4b 必须红：**保留** ns 尾 ＋ 容差收到 0 ⇒ 墓碑那条的 **PG 臂**红。
    #   ⚠ 与 M3 是**同一条变异**、**不同的目标判据**（M3 量纯函数臂，M4b 量真 PG 臂）。
    def m4b():
        b.mutate(TRANSCRIPT, M3_OLD, M3_NEW, 'M4b')
    b.case('M4b 保留 ns 尾 ＋ 容差收到 0 ⇒ 墓碑那条的 PG 臂 ⇒ 门必须红', True,
           must_contain=['FAIL'],
           mutate_fn=m4b, runner=r['meeting_pg'])

    # ── M5a 必须红（**确定性**）：RSS 夹具的 `day` 锚到 **UTC 午夜**（而不是正午）
    #      ⇒ 第二条夹具（day-1h）落到**昨天** ⇒ itemCount = 1 ⇒ 必红。
    #   ⚠ 这一条是 §964 那条 M5 的**确定性替身**：§964 的原版是「退回 now」，
    #      而它**只在 UTC 00:00–01:00 有牙**（见 M5b）⇒ ⇒ 批次要能随时复跑，
    #      就必须有一条**与挂钟无关**的等价臂。
    M5A_OLD = '\tday := now.Truncate(24 * time.Hour).Add(12 * time.Hour)\n'
    M5A_NEW = '\tday := now.Truncate(24 * time.Hour)\n'

    def m5a():
        b.mutate(RSS, M5A_OLD, M5A_NEW, 'M5a')
    b.case('M5a RSS 夹具的 day 锚到 UTC 午夜（不是正午）⇒ 跨日界 ⇒ 门必须红', True,
           must_contain=['itemCount = 1, want 2'],
           mutate_fn=m5a, runner=r['server'])

    # ── M5b：**忠实重建** §964 的原版（「day 定义退回 now」）⇒ 但它**时刻相关**。
    #   ⚠ 窗口内（UTC 00–01 点）⇒ 必须红；窗口外 ⇒ **必然绿** ⇒
    #   ⇒ ⇒ 窗口外这一轮**不作证据**（绿 ≠ 通过）⇒ ⇒ ⇒ 如实打印出来。
    M5B_NEW = '\tday := now\n'

    def m5b():
        b.mutate(RSS, M5A_OLD, M5B_NEW, 'M5b')
    if 0 <= hour < 1:
        b.case('M5b 忠实重建：「day 退回 now」（当前在 UTC 00–01 窗口内 ⇒ 有牙）⇒ 门必须红', True,
               must_contain=['itemCount = 1, want 2'],
               mutate_fn=m5b, runner=r['server'])
    else:
        b.case('M5b 忠实重建：「day 退回 now」（当前**不在** UTC 00–01 窗口内 ⇒ '
               '这条臂**本轮恒绿** ⇒ **不作证据**，只作读数）', False,
               must_not_contain=['FAIL'],
               mutate_fn=m5b, runner=r['server'])

    # ── 收尾自证：还原态下判据必须绿 ──────────────────────────────────
    b.case('收尾自证：还原态下判据必须绿', False,
           must_not_contain=['FAIL'],
           runner=combined)


if __name__ == '__main__':
    run(b, main)

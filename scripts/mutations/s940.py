#!/usr/bin/env python3
"""§940：notes 的时间戳必须是**绝对时刻**（timestamptz）的变异批次。

⚠ 为什么本批次**必须落盘**（§935 的教训）：§935 发现「当时的变异脚本没落盘」
   ⇒ 「当时绿 = 现在绿」**永久不可回溯** ⇒ ⇒ 本仓从此规定：**变异脚本要落盘**。

⚠ 本批次**需要真 PG**：判据量的全是**落库之后的行**（列类型 / epoch / 表有没有
   被重写）⇒ ⇒ 没有 PG 时用例会 skip，而 skip 的退出码是 0
   ⇒ ⇒ 本批次在缺 PG 时**拒绝跑**（退出码 3），而不是「跑了个寂寞」。
   见 `_harness.require_pg_dsn` 与 `go_pkg_runner(forbid_skip=True)`。

⚠ 本批次有**两个判据运行器**：
   ① `TestS940*`（notes 包）—— 列类型 / 往返精度 / 旧行还原 / 会话时区无关 /
      机制负控 / **启动不得重写 notes 表**。
   ② `TestS940*`（server 包）—— **增量同步必须收敛**（用户可见的那一半）。

⚠⚠ 本批次是 §940 里**唯一一处「我推翻了自己的结论」**的地方，脚本里记着：
   我原本给 advisory lock 写的理由是「类型转换不是幂等的 ⇒ 双实例重复转换 ⇒
   数据被静默平移」⇒ ⇒ **实测否掉了**：重复转换 epoch 一字不变（换会话时区
   也不变）⇒ ⇒ **值上幂等**。
   ⇒ ⇒ 但同一批实测量出另一半：**每次 ALTER COLUMN ... TYPE 都会重写整表**
   （relfilenode 变）⇒ ⇒⇒ **成本上不幂等**。
   ⇒ ⇒⇒ 所以「按列类型决定转不转」这一步的真实价值 = **每次进程启动对 notes 表
   零 DDL** ⇒ ⇒⇒ 而这条价值**只有一条判据看得见**（M2 用的那条）。
   ⇒ ⇒⇒ ⇒ ⇒ **M2 是本批次最重要的一条变异**：它把「值幂等」与「成本不幂等」
   这两件事**同时**证明出来。

判据命令：
  go test ./internal/notes/  -run 'TestS940' -count=1
  go test ./internal/server/ -run 'TestS940' -count=1
"""
import os
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    Batch, HarnessError, go_pkg_runner, require_pg_dsn, run,
)

TARGETS = [
    'backend/internal/notes/store.go',
    # ⚠ M6 要改**测试文件**（往 notes 包塞一个跳过点）⇒ ⇒ 它必须在
    #   targets 里 ⇒ ⇒ 否则 harness 还原时会把这次变异**留在工作树上**。
    'backend/internal/notes/store_timestamp_test.go',
]

# ⛔ 只注入**测试专用** DSN（独立 schema + 自动 DROP）⇒ 绝不碰生产库。
# ⚠ DSN **懒加载**：缺它时本批次「拒绝跑」（退出码 3），而不是让用例被 skip
#   ⇒ skip 的退出码是 0 ⇒ 「跑了个寂寞」与「全过」完全同形（§935.4.2）。
_RUNNERS = {}


def runners():
    if not _RUNNERS:
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        _RUNNERS['notes'] = go_pkg_runner(
            './internal/notes/', 'TestS940', env=env, forbid_skip=True)
        _RUNNERS['server'] = go_pkg_runner(
            './internal/server/', 'TestS940', env=env, forbid_skip=True)
        # ⚠ 跳点普查那道门（§467 同族）：§940 把 notes 包接上真 PG ⇒⇒
        #   它的 6 个跳过点消失 ⇒⇒ 台账那条**过期** ⇒⇒ 按门自己的规则删掉
        #   ⇒⇒ ⇒ 删完之后，notes 包的棘轮**更紧**（再出现跳过点 = 新增 ⇒ 必红）
        #   ⇒⇒ ⇒ M6 用这道门当尺子，钉住「更紧」这件事。
        _RUNNERS['census'] = go_pkg_runner(
            './internal/testaudit/', 'TestSkipCensus', env=env, forbid_skip=True)
    return _RUNNERS


def both():
    """两个包**一起**跑 ⇒ 用于「同一个变异在两侧都该红」的用例。

    ⚠ 返回 (rc, out)：rc 取两个运行器里**第一个非零** ⇒ 任一侧红 ⇒ 整体红。
    """
    rc1, out1 = runners()['notes']()
    rc2, out2 = runners()['server']()
    return (rc1 or rc2), out1 + '\n' + out2


b = Batch('§940 notes 的时间戳必须是绝对时刻（timestamptz）：列类型 / 往返精度 / 旧行还原 / 启动不得重写表',
          lambda: runners()['notes'](), TARGETS)
STORE_GO = b.targets[0]
TEST_GO = b.targets[1]

# ── 变异的锚点（必须与实现**逐字**一致；不一致 ⇒ 落盘自证会失败并中止）──
#
# ⚠⚠ 锚点纪律（§940 本轮踩到）：`current_setting('TimeZone')` 这个串在
#   store.go 里出现 **3 次**（其中 2 次在**注释**里）⇒ ⇒ 用它当锚点会被
#   harness 判成「锚点可能已过期」而中止 ⇒ ⇒⇒ 必须用**带引号的整条 SQL 字面量**
#   （带引号 ⇒ 只可能出现在代码里 ⇒ 恰好 1 次）。

# M1（D1 的守卫）：类型守卫失效 ⇒ PG 的 format_type 从不输出 "timestamptz"
#   ⇒ 守卫永不命中 ⇒ 撞上「意外类型必须报错」分支。
#
# ⚠ 这条钉住两件事：① 守卫是**活的**（不是死代码）；② 判据认的是 PG 的
#   format_type **长形态**（有人「简化」成 timestamptz ⇒ 全线报错而不是静默）。
#
# ⚠⚠ 锚点教训（本轮踩过）：第一版把 M2 的锚点**凭记忆**写成「守卫 + 错误分支
#   相邻」⇒ ⇒ 真实文件里两者**中间隔着一行注释** ⇒ ⇒ 锚点 0 命中 ⇒ ⇒
#   ⇒ 批次在 M2 处**当场中止**（harness 的自证把它逮住了）⇒ ⇒⇒ 修法不是
#   ⇒ ⇒⇒ 把注释抄进锚点，而是**把判定抽成一个纯函数**（notesColumnNeedsConversion）
#   ⇒ ⇒⇒ 于是锚点变成**单行** ⇒ ⇒⇒ 再加一条**不依赖 PG** 的纯函数判据。
M1_OLD = '\tcase "timestamp with time zone":\n'
M1_NEW = '\tcase "timestamptz":\n'

# M2（D1 的**成本**那一半）：「按列类型决定转不转」被绕过去
#   ⇒ 每次进程启动都把三列重写一遍。
#
# ⚠⚠ 本批次**最重要**的一条：它把两件事**同时**证明出来 ——
#   ① **值上幂等**：重复转换不会让 epoch 漂 ⇒ ⇒ 精度判据**仍然绿**
#      ⇒ ⇒ 所以「精度绿了」**不能**推出「重复转换无害」；
#   ② **成本上不幂等**：每次 ALTER 都重写整表（relfilenode 变）⇒ ⇒
#      「不得重写 notes 表」判据**必须红** ⇒ ⇒ 这才是类型判定与 advisory
#      lock 的真实价值。
M2_OLD = (
    '\t\tif !needs {\n'
    '\t\t\tcontinue // ✅ 已经是绝对时刻\n'
    '\t\t}\n'
)
# ⚠ `_ = needs` 而不是整块删掉：删掉会让 needs **未使用** ⇒ **编译失败**
#   ⇒ 判据一条都没跑到而退出码仍是 1 ⇒ ⇒ 「变红」是假的（§937 记过的
#   「变异必须只改语义」第 5 次出现）⇒ ⇒ 保留 needs 的使用，只去掉判定。
M2_NEW = '\t\t_ = needs // M2: 绕开「按列类型决定转不转」\n'

# M3（D1 的存量还原）：还原时的会话时区被写死成 'UTC'
#   ⇒ ⇒ 旧行按 UTC 解释墙钟 ⇒ ⇒ 偏一个时区差 ⇒ ⇒ 旧行还原判据必须红。
#
# ⚠ 这是**存量**那一半：只保证「以后写的不再歪」是不够的。
M3_OLD = ('"ALTER TABLE notes ALTER COLUMN %s TYPE timestamptz '
          "USING %s AT TIME ZONE current_setting('TimeZone')\"")
M3_NEW = ('"ALTER TABLE notes ALTER COLUMN %s TYPE timestamptz '
          "USING %s AT TIME ZONE 'UTC'\"")

# M4（D1 的覆盖面）：只转第一列 ⇒ updated_at / deleted_at 仍是裸墙钟
#   ⇒ ⇒ 列类型判据 + 往返判据 + 旧行还原判据 + 收敛判据必须红。
#
# ⚠ 锚点用 `[:1]` 而不是把 continue 改成 break：后者只在「列类型混合」时才有
#   效果（旧表三列全是裸墙钟 ⇒ 守卫那两行根本走不到）⇒ ⇒ 那种变异在旧表
#   场景下**是空操作** ⇒ ⇒ 必须换锚点。
M4_OLD = '\tfor _, col := range notesTimestampColumns {\n'
M4_NEW = '\tfor _, col := range notesTimestampColumns[:1] {\n'

# M5（D1 的本体）：ALTER 的目标类型被改回裸墙钟 ⇒ 转换根本不生效
#   ⇒ ⇒ 列类型 + 往返 + 旧行还原 + 收敛必须红。
M5_OLD = M3_OLD
M5_NEW = ('"ALTER TABLE notes ALTER COLUMN %s TYPE timestamp '
          "USING %s AT TIME ZONE current_setting('TimeZone')\"")

# ── 负控的锚点（语义等价 ⇒ 必须保持绿）────────────────────────────────

# N1：新库快路径退回裸墙钟（DDL 写 TIMESTAMP WITHOUT TIME ZONE）
#   ⇒ ⇒ ALTER 仍会把列转成 timestamptz ⇒ ⇒ 必须仍绿
#   ⇒ ⇒ ⇒ 这条负控证明的是「**ALTER 才是真保证，DDL 只是新库快路径**」。
N1_OLD = '\t\tcreated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,\n'
N1_NEW = '\t\tcreated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT CURRENT_TIMESTAMP,\n'

# N2：`current_setting` 的参数写成 `::text`（等价）。
N2_OLD = M3_OLD
N2_NEW = ('"ALTER TABLE notes ALTER COLUMN %s TYPE timestamptz '
          "USING %s AT TIME ZONE current_setting('TimeZone'::text)\"")

# N3：`AT TIME ZONE` 的参数加一对括号（等价）。
N3_OLD = M3_OLD
N3_NEW = ('"ALTER TABLE notes ALTER COLUMN %s TYPE timestamptz '
          "USING %s AT TIME ZONE (current_setting('TimeZone'))\"")

# N4：advisory lock 被摘掉 ⇒ 必须仍绿。
#
# ⚠⚠ 这条负控量的是**局限**：单实例判据看不见这个锁的价值
#   （它防的是**并发启动时各自重写一遍全表**）⇒ ⇒ ⇒ 诚实的结论是：
#   「锁有用」这句话**没有并发判据支撑** ⇒ ⇒ 只在文档里登记为**设计取舍**。
#   ⇒ ⇒ 并且 N4 顺带证明：精度与还原判据**不依赖**这把锁 ⇒ ⇒ 证明
#   「值幂等」那个实测结论（重复转换无害）**与锁无关**。
N4_OLD = ('\tif _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", '
          'int64(notesTimestampAdvisoryLock)); err != nil {\n')
N4_NEW = '\tif _, err := tx.Exec(ctx, "SELECT 1"); err != nil {\n'

# M6（**既有门禁**那侧）：往 notes 包的一个测试函数里塞一个 `t.Skip(`。
#
# ⚠⚠ 这条是 §940 撞上**既有门禁**（`internal/testaudit` 的跳点普查棘轮）
#   之后的**配套负控**：
#   §940 把 notes 包接上真 PG ⇒⇒ 它的 6 个跳过点消失 ⇒⇒ 台账里那条**过期**
#   ⇒⇒ 按门自己的规则（「基线里有、现在整个包都没了 ⇒ 必须删」）**删掉了它**
#   ⇒⇒⇒ ⚠ **「删台账条目」这件事必须证明它不是「给这个包开了后门」** ⇒⇒⇒
#   ⇒⇒⇒ 所以：往 notes 包塞一个跳过点 ⇒⇒ **这道门必须红** ⇒⇒⇒
#   ⇒⇒⇒ ⇒ 期望消息：`该包不在基线里，属新增`（基线里没有 notes ⇒ 更紧，不是更松）。
#
# ⚠ 锚点必须在**测试函数体内**（census 只数 `func TestXxx` 里的 `t.Skip(`）
#   ⇒ ⇒ 插在 `TestS940ColumnTypeDecision` 的开头。
# ⚠⚠ 本轮这条锚点**改过一次**：第一版指的是 `TestS940TimestampColumnsAreAbsolute`
#   ⇒ ⇒ 后来那条判据被改成「两种夹具各跑一遍」（§940.7 的 M4/M5 逼出来的）⇒ ⇒
#   ⇒ ⇒ 函数体变了 ⇒ ⇒ 锚点 0 命中 ⇒ ⇒ ⇒ 又一次被 harness 的自证当场逮住。
#   ⇒ ⇒ ⇒ ⇒ ★ **锚点会随判据重构一起漂** ⇒ ⇒⇒⇒ 每次改判据**之后**都要
#   ⇒ ⇒ ⇒ ⇒ 重新核一遍锚点（`scripts/mutations/s940.py` 的锚点表）。
M6_OLD = 'func TestS940ColumnTypeDecision(t *testing.T) {\n'
M6_NEW = ('func TestS940ColumnTypeDecision(t *testing.T) {\n'
          '\tt.Skip("M6: 往 notes 包塞一个跳过点")\n')


def body():
    r = runners()
    rc, out = r['notes']()
    b.record('基线：未变异时判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))

    # ── M1 必须红：类型守卫失效 ──────────────────────────────────────
    def m1():
        b.mutate(STORE_GO, M1_OLD, M1_NEW, 'M1')
    b.case('M1 类型守卫失效（拿 "timestamptz" 去比 PG 的 format_type）⇒ 「意外类型必须报错」判据必须红', True,
           must_contain=['unexpected column type'],
           mutate_fn=m1)

    # ── M2 必须红：类型判定被拆掉 ⇒ 每次启动都重写整表 ──────────────
    def m2():
        b.mutate(STORE_GO, M2_OLD, M2_NEW, 'M2')
    out2 = b.case('M2 「按列类型决定转不转」被拆掉 ⇒ 「第 2 次建 store 不得重写 notes 表」判据必须红', True,
                  must_contain=['又重写了 notes 表'],
                  mutate_fn=m2)
    # ⚠⚠ M2 的**额外断言**（这才是本批次最重要的一条结论）：
    #   精度判据**仍然绿** ⇒ ⇒ 「重复转换无害」（值幂等）⇒ ⇒⇒
    #   而成本判据红 ⇒ ⇒⇒ 「重复转换很贵」（每次全表重写）⇒ ⇒⇒
    #   ⇒ 两者**必须分开记**，不能合成一句「重复转换没问题」。
    b.record('M2 的额外断言：精度判据必须**仍然绿**（值上幂等 ⇒ 重复转换不漂）',
             ('读回 epoch 偏' not in out2 and '旧行没有被还原成真正的 instant' not in out2
              and '的类型是' not in out2))

    # ── M3 必须红：还原时区写死 'UTC' ────────────────────────────────
    def m3():
        b.mutate(STORE_GO, M3_OLD, M3_NEW, 'M3')
    b.case("M3 还原时的会话时区被写死成 'UTC' ⇒ 旧行还原判据必须红", True,
           must_contain=['旧行没有被还原成真正的 instant'],
           mutate_fn=m3)

    # ── M4 必须红：只转第一列 ───────────────────────────────────────
    def m4():
        b.mutate(STORE_GO, M4_OLD, M4_NEW, 'M4')
    b.case('M4 只转第一列（updated_at / deleted_at 仍是裸墙钟）⇒ 列类型 + 往返 + 旧行还原 + 收敛判据必须红', True,
           must_contain=['的类型是', '读回 epoch 偏', '旧行没有被还原成真正的 instant',
                         '增量没有收敛'],
           mutate_fn=m4, runner=both)

    # ── M5 必须红：ALTER 的目标类型被改回裸墙钟 ──────────────────────
    def m5():
        b.mutate(STORE_GO, M5_OLD, M5_NEW, 'M5')
    b.case('M5 ALTER 的目标类型被改回 timestamp（转换不生效）⇒ 列类型 + 往返 + 旧行还原 + 收敛判据必须红', True,
           must_contain=['的类型是', '读回 epoch 偏', '旧行没有被还原成真正的 instant',
                         '增量没有收敛'],
           mutate_fn=m5, runner=both)

    # ── N1 负控：DDL 快路径退回裸墙钟 ⇒ 必须保持绿 ───────────────────
    def n1():
        b.mutate(STORE_GO, N1_OLD, N1_NEW, 'N1')
    b.case('N1（负控）DDL 的新库快路径退回裸墙钟 ⇒ 必须保持绿（ALTER 才是真保证）', False,
           must_not_contain=['FAIL'],
           mutate_fn=n1)

    # ── N2 负控：current_setting 参数加 ::text ⇒ 必须保持绿 ──────────
    def n2():
        b.mutate(STORE_GO, N2_OLD, N2_NEW, 'N2')
    b.case('N2（负控）current_setting 的参数写成 ::text（等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n2)

    # ── N3 负控：AT TIME ZONE 的参数加括号 ⇒ 必须保持绿 ─────────────
    def n3():
        b.mutate(STORE_GO, N3_OLD, N3_NEW, 'N3')
    b.case('N3（负控）AT TIME ZONE 的参数加一对括号（等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n3)

    # ── N4 负控：摘掉 advisory lock ⇒ 必须保持绿（并登记其局限）──────
    def n4():
        b.mutate(STORE_GO, N4_OLD, N4_NEW, 'N4')
    b.case('N4（负控）摘掉 advisory lock ⇒ 必须保持绿（单实例判据看不见它 ⇒ 局限）', False,
           must_not_contain=['FAIL'],
           mutate_fn=n4)

    # ── M6 必须红：往 notes 包塞一个跳过点 ⇒ 跳点普查棘轮必须红 ──────
    def m6():
        b.mutate(TEST_GO, M6_OLD, M6_NEW, 'M6')
    b.case('M6 往 notes 包塞一个 t.Skip( ⇒ 跳点普查那道门必须红（台账条目删掉后棘轮更紧，不是更松）', True,
           must_contain=['该包不在基线里，属新增'],
           mutate_fn=m6, runner=lambda: runners()['census']())


if __name__ == '__main__':
    try:
        run(b, body, extra_runners=[lambda: runners()['server'](),
                                    lambda: runners()['census']()])
    except HarnessError as e:
        print('❌ %s' % e)
        sys.exit(3)

#!/usr/bin/env python3
"""§944：把 §943 留下的**普查台账**收口 —— 撞号 ⇒ 不得覆盖别人的行。

⚠ 为什么本批次**必须落盘**（§935 的教训）：§935 发现「当时的变异脚本没落盘」
   ⇒ 「当时绿 = 现在绿」**永久不可回溯** ⇒ ⇒ 本仓从此规定：**变异脚本要落盘**。

⚠ 本批次**需要真 PG**：判据量的全是**落库之后的行**（受害者的行有没有被改）
   ⇒ ⇒ 没有 PG 时用例会 skip，而 skip 的退出码是 0 ⇒ ⇒ 本批次在缺 PG 时
   **拒绝跑**（退出码 3），而不是「跑了个寂寞」。见 `_harness.require_pg_dsn`
   与 `go_pkg_runner(forbid_skip=True)`。

⚠⚠ 本批次有**六个判据运行器**，而且**必须一起跑**：
   ① `TestS944`（auth 包）        —— 生物认证凭证（PG 版 + 内存版两条实现）
   ② `TestS944`（lobster 包）     —— 加密资产镜像
   ③ `TestS944`（identity 包）    —— 设备行
   ④ `TestS944`（notifycenter 包）—— 通知规则
   ⑤ `TestS944`（server 包）      —— 端到端（HTTP 状态码必须**可区分** = 409）
   ⑥ `TestS943`（repohygiene 包） —— 全仓「裸 id + ON CONFLICT DO UPDATE」候选集棘轮
   ⇒ ⇒⇒ ★ ⑥ 是**普查棘轮**：本节把 §943 留下的 9 处「待定性」收口成两维
   （Reach = 撞号可达性 / Fix = 处置），并因此多出一条**机器可判**的不变量：
   「**id 由客户端自选 ⇒ 必须有归属守卫**」。

⚠⚠ **本批次最该被记住的一条**（M5）：
   「守卫存在」**不等于**「守卫能挡住」⇒ ⇒⇒ 守卫不成立时 PG **不报错**，
   只是**不返回行**⇒ ⇒⇒ 用 `Exec` 的话这道守卫就是**哑弹**（存在、但无声
   ⇒ 等于没有）⇒ ⇒⇒ 所以判据必须断言「**拒绝读得出来**」，而不只是
   「受害者的行没被改」⇒ ⇒⇒⇒ M5 把 `QueryRow` 改回 `Exec` ⇒ 受害者的行
   **确实没被改**（守卫还在！）⇒ ⇒⇒ 但调用方拿到 **nil** ⇒ ⇒⇒ 以为注册
   成功了 ⇒ ⇒⇒⇒ 而受害者的凭证已经**报废**（他的设备再也登不进来）。

判据命令：
  go test ./internal/auth/         -run 'TestS944' -count=1
  go test ./internal/lobster/      -run 'TestS944' -count=1
  go test ./internal/identity/     -run 'TestS944' -count=1
  go test ./internal/notifycenter/ -run 'TestS944' -count=1
  go test ./internal/server/       -run 'TestS944' -count=1
  go test ./internal/repohygiene/  -run 'TestS943' -count=1
"""
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    Batch, HarnessError, go_pkg_runner, require_pg_dsn, run,
)

TARGETS = [
    'backend/internal/auth/biometric.go',
    'backend/internal/lobster/store.go',
    'backend/internal/identity/store.go',
    'backend/internal/notifycenter/service.go',
    'backend/internal/server/server_lobster.go',
    # ⚠ 台账文件也在 targets 里 ⇒ M6/M7 要动它 ⇒ ⇒ ⇒ 还原时它才不会被漏掉。
    'backend/internal/repohygiene/on_conflict_ownership_test.go',
]

# ⛔ 只注入**测试专用** DSN（独立 schema + 自动 DROP）⇒ 绝不碰生产库。
# ⚠ DSN **懒加载**：缺它时本批次「拒绝跑」（退出码 3），而不是让用例被 skip
#   ⇒ skip 的退出码是 0 ⇒ 「跑了个寂寞」与「全过」完全同形（§935.4.2）。
_RUNNERS = {}


def runners():
    if not _RUNNERS:
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        for key, pkg, pat in (
            ('auth', './internal/auth/', 'TestS944'),
            ('lobster', './internal/lobster/', 'TestS944'),
            ('identity', './internal/identity/', 'TestS944'),
            ('notifycenter', './internal/notifycenter/', 'TestS944'),
            ('server', './internal/server/', 'TestS944'),
        ):
            _RUNNERS[key] = go_pkg_runner(pkg, pat, env=env, forbid_skip=True)
        _RUNNERS['repohygiene'] = go_pkg_runner(
            './internal/repohygiene/', 'TestS943')
    return _RUNNERS


def combined():
    """六个包的判据都跑 ⇒ 任一红就算红（收尾时六者都必须绿）。"""
    r = runners()
    rc_total = 0
    chunks = []
    for key in ('auth', 'lobster', 'identity', 'notifycenter', 'server', 'repohygiene'):
        rc, out = r[key]()
        rc_total = rc_total or rc
        chunks.append('===== %s =====\n%s' % (key, out))
    return rc_total, '\n\n'.join(chunks)


b = Batch('§944 普查台账收口：撞 id ⇒ 不得覆盖别人的行（生物凭证 / 资产镜像 / 设备 / 通知规则）',
          combined, TARGETS)
AUTH_GO = b.targets[0]
LOBSTER_GO = b.targets[1]
IDENTITY_GO = b.targets[2]
NOTIFY_GO = b.targets[3]
SERVER_GO = b.targets[4]
LEDGER_GO = b.targets[5]

# ── 变异的锚点（必须与实现**逐字**一致；不一致 ⇒ 落盘自证会失败并中止）──
#
# ⚠⚠ **锚点必须唯一**：每条锚点在本文件里都只出现一次（写脚本前用 count 核过）
#   ⇒ ⇒⇒ 否则 `b.mutate` 会因为「锚点出现 N 次」直接中止（这是好事）。

# M1（生物认证的本体）：`biometric_credentials` 的归属守卫被去掉
#   ⇒ ⇒⇒ 撞 id 会**覆盖**别人的 public_key ⇒ ⇒⇒ 登录链按 id 取行 ⇒
#   ⇒⇒⇒ 攻击者用自己的私钥就能**顶替原主人登录**。
M1_OLD = '\t\tWHERE biometric_credentials.user_id = EXCLUDED.user_id\n'
M1_NEW = ''

# M2（资产镜像的本体）：`asset_mirrors` 的归属守卫被去掉
#   ⇒ ⇒⇒ 撞 id 会把**别人的密文**搬进自己的租户并覆盖。
M2_OLD = '\tWHERE asset_mirrors.workspace_id = EXCLUDED.workspace_id\n'
M2_NEW = ''

# M3（设备的本体）：`devices` 的归属守卫被去掉
#   ⇒ ⇒⇒ 撞 id 会把**别人的设备行**搬走 + push_token 被换。
M3_OLD = 'WHERE devices.user_id = EXCLUDED.user_id\n'
M3_NEW = ''

# M4（通知规则的本体）：`notification_rules` 的归属守卫被去掉
#   ⇒ ⇒⇒ 撞 id 会**改写别人的通知规则**（留在别人的租户里）。
M4_OLD = 'WHERE notification_rules.workspace_id = EXCLUDED.workspace_id\n'
M4_NEW = ''

# M5（**哑弹**）：`QueryRow` 改回 `Exec` ⇒ 守卫**还在**（行没被改），
#   但拒绝**读不出来**（调用方拿到 nil ⇒ 以为注册成功了）。
#
# ⚠⚠ 这是本批次最该被记住的一条：
#   「守卫存在」**不等于**「守卫能挡住」⇒ ⇒⇒ 守卫不成立时 PG **不报错**，
#   只是**不返回行**⇒ ⇒⇒ `Exec` 只看命令标签 ⇒ 拿到的是一个**成功的**标签
#   ⇒ ⇒⇒⇒ 于是这道守卫是**哑弹**（存在、但无声 ⇒ 等于没有）。
#
# ⚠ 变异必须**编译干净**（★ §943 的教训）：`var storedID string` 与
#   `.Scan(&storedID)` 要一起去掉，否则留下「声明未使用」⇒ 编译红
#   ⇒ ⇒⇒ 那样测的就不是「语义变了」，而是「编不过了」（★ 必须区分
#   「编译红」与「断言红」）。
M5A_OLD = '\tvar storedID string\n\terr := s.pool.QueryRow(ctx, `\n'
M5A_NEW = '\t_, err := s.pool.Exec(ctx, `\n'
M5B_OLD = ').Scan(&storedID)\n'
M5B_NEW = ')\n'

# M6（**台账棘轮有牙**）：把台账里 `biometric_credentials` 的守卫形态改成
#   「没有」⇒ ⇒⇒ §944 新增的那条不变量（「id 由客户端自选 ⇒ 必须有守卫」）
#   ⇒ ⇒⇒⇒ 必须红 ⇒ ⇒⇒⇒ （★ 这证明那两维**不是注释**，而是被卡住的。）
M6_OLD = '\t\tTable: "biometric_credentials", Guard: s943GuardSQLWhere,\n'
M6_NEW = '\t\tTable: "biometric_credentials", Guard: s943GuardNone,\n'

# M7（**台账棘轮非空转**）：把台账里 `emails` 的 Reach 从「服务端派生」改成
#   「客户端自选」⇒ ⇒⇒ 那条不变量必须红。
#
# ⚠⚠ 为什么必须有这一条：M6 证明的是「不变量会咬住**台账与实况不符**」，
#   但如果台账里所有 client-chosen 条目都**恰好**有守卫 ⇒ ⇒⇒⇒ 那么这条
#   不变量可能是**空转**的（它从来没被真正触发过）⇒ ⇒⇒ M7 故意把一条
#   「无守卫 + 可达」的组合**造出来** ⇒ ⇒⇒⇒ 不变量必须当场咬住它。
M7_OLD = ('\t\tTable: "emails", Guard: s943GuardNone,\n'
          '\t\tReach: s943ReachServerDerived, Fix: s943FixQualified,\n')
M7_NEW = ('\t\tTable: "emails", Guard: s943GuardNone,\n'
          '\t\tReach: s943ReachClientChosen, Fix: s943FixQualified,\n')

# ── 负控的锚点（语义等价 ⇒ 必须保持绿）────────────────────────────────
#
# ⚠ 四条负控都是**同一个动作**：把守卫的谓词从 `EXCLUDED.x` 换成等价的
#   位置参数（$2 就是同一列的值）⇒ ⇒⇒ 它们证明「判据咬的是**语义**，
#   不是某种写法」⇒ ⇒⇒⇒ （★ 否则「判据 = 文本匹配」这类量具缺陷会藏很久。）

# N1：生物认证的守卫写成位置参数（$2 == c.UserID）⇒ 等价 ⇒ 必须保持绿。
N1_OLD = '\t\tWHERE biometric_credentials.user_id = EXCLUDED.user_id\n'
N1_NEW = '\t\tWHERE biometric_credentials.user_id = $2\n'

# N2：资产镜像的守卫写成位置参数（$2 == m.WorkspaceID）⇒ 等价 ⇒ 必须保持绿。
N2_OLD = '\tWHERE asset_mirrors.workspace_id = EXCLUDED.workspace_id\n'
N2_NEW = '\tWHERE asset_mirrors.workspace_id = $2\n'

# N3：设备的守卫写成位置参数（$2 == d.UserID）⇒ 等价 ⇒ 必须保持绿。
N3_OLD = 'WHERE devices.user_id = EXCLUDED.user_id\n'
N3_NEW = 'WHERE devices.user_id = $2\n'

# N4：通知规则的守卫写成位置参数（$2 == r.WorkspaceID）⇒ 等价 ⇒ 必须保持绿。
N4_OLD = 'WHERE notification_rules.workspace_id = EXCLUDED.workspace_id\n'
N4_NEW = 'WHERE notification_rules.workspace_id = $2\n'

# N5：内存路径的归属判定换个写法（`a != b` ⇒ `b != a`，等价）⇒ 必须保持绿。
N5_OLD = 'if existing, ok := s.memory[c.ID]; ok && existing.UserID != c.UserID {\n'
N5_NEW = 'if existing, ok := s.memory[c.ID]; ok && c.UserID != existing.UserID {\n'


def body():
    rc, out = combined()
    b.record('基线：未变异时判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))

    # ── M1 必须红：生物认证的守卫被去掉 ⇒ 覆盖别人的认证公钥 ───────────
    #
    # ⚠ 期望消息是**实测**出来的：这里钉的是**「越权覆盖」那个症状**
    #   （受害者的公钥被换掉）⇒ ⇒⇒ 而**不是**「报错类型不对」那个症状
    #   ⇒ ⇒⇒⇒ （★ 「变异转红了」≠「变异验到了你想验的那条」）
    def m1():
        b.mutate(AUTH_GO, M1_OLD, M1_NEW, 'M1')
    b.case('M1 `biometric_credentials` 的归属守卫被去掉 ⇒ 撞 id 覆盖别人的认证公钥 ⇒ 判据必须红', True,
           must_contain=['越权覆盖'],
           mutate_fn=m1)

    # ── M2 必须红：资产镜像的守卫被去掉 ⇒ 密文被搬走 + 覆盖 ──────────
    def m2():
        b.mutate(LOBSTER_GO, M2_OLD, M2_NEW, 'M2')
    b.case('M2 `asset_mirrors` 的归属守卫被去掉 ⇒ 撞 id 把别人的密文搬走 ⇒ 判据必须红', True,
           must_contain=['越权覆盖'],
           mutate_fn=m2)

    # ── M3 必须红：设备的守卫被去掉 ⇒ 设备行被搬走 ────────────────────
    def m3():
        b.mutate(IDENTITY_GO, M3_OLD, M3_NEW, 'M3')
    b.case('M3 `devices` 的归属守卫被去掉 ⇒ 撞 id 把别人的设备行搬走 ⇒ 判据必须红', True,
           must_contain=['那一行被搬走了'],
           mutate_fn=m3)

    # ── M4 必须红：通知规则的守卫被去掉 ⇒ 别人的规则被改写 ────────────
    def m4():
        b.mutate(NOTIFY_GO, M4_OLD, M4_NEW, 'M4')
    b.case('M4 `notification_rules` 的归属守卫被去掉 ⇒ 撞 id 改写别人的通知规则 ⇒ 判据必须红', True,
           must_contain=['越权覆盖'],
           mutate_fn=m4)

    # ── M5 必须红：**哑弹**（守卫在，但拒绝读不出来）────────────────
    #
    # ⚠ 期望消息钉的是**哑弹那个症状**（「读不出来」）⇒ ⇒⇒ 而 M1 钉的是
    #   「越权覆盖」⇒ ⇒⇒⇒ 两条变异的报错文本**不同** ⇒ ⇒⇒ 各自都能被
    #   期望消息钉住 ⇒ ⇒⇒⇒ （★ 这就是「必须把期望消息也钉住」的做法）
    def m5():
        b.mutate(AUTH_GO, M5A_OLD, M5A_NEW, 'M5a')
        b.mutate(AUTH_GO, M5B_OLD, M5B_NEW, 'M5b')
    b.case('M5 守卫还在但拒绝**读不出来**（QueryRow 改回 Exec ⇒ 哑弹）⇒ 判据必须红', True,
           must_contain=['读不出来'],
           mutate_fn=m5)

    # ── M6 必须红：台账与实况不符 ⇒ 普查棘轮必须红 ──────────────────
    def m6():
        b.mutate(LEDGER_GO, M6_OLD, M6_NEW, 'M6')
    b.case('M6 台账里把生物认证的守卫形态改成「没有」⇒ 普查棘轮必须红', True,
           must_contain=['却没有归属守卫'],
           mutate_fn=m6, runner=lambda: runners()['repohygiene']())

    # ── M7 必须红：台账里造出「可达 + 无守卫」的组合 ⇒ 棘轮必须红 ────
    #
    # ⚠ 这一条证明 §944 那条新不变量**不是空转**的（★ 「恒绿」的第一嫌疑
    #   永远是量具坏了 ⇒ ⇒⇒ 必须有一条专门去**触发**它）。
    def m7():
        b.mutate(LEDGER_GO, M7_OLD, M7_NEW, 'M7')
    b.case('M7 台账里把邮件的可达性改成「客户端自选」⇒ 普查棘轮必须红（证明新不变量会咬）', True,
           must_contain=['却没有归属守卫'],
           mutate_fn=m7, runner=lambda: runners()['repohygiene']())

    # ── N1–N4 负控：守卫写成等价的位置参数 ⇒ 必须保持绿 ──────────────
    for label, path, old, new, desc in (
        ('N1', AUTH_GO, N1_OLD, N1_NEW, '生物认证的守卫写成位置参数（等价）'),
        ('N2', LOBSTER_GO, N2_OLD, N2_NEW, '资产镜像的守卫写成位置参数（等价）'),
        ('N3', IDENTITY_GO, N3_OLD, N3_NEW, '设备的守卫写成位置参数（等价）'),
        ('N4', NOTIFY_GO, N4_OLD, N4_NEW, '通知规则的守卫写成位置参数（等价）'),
    ):
        def mk(path=path, old=old, new=new):
            def fn():
                b.mutate(path, old, new, label)
            return fn
        b.case('%s（负控）%s ⇒ 必须保持绿' % (label, desc), False,
               must_not_contain=['FAIL'],
               mutate_fn=mk())

    # ── N5 负控：内存路径的归属判定换个写法（等价）⇒ 必须保持绿 ───────
    def n5():
        b.mutate(AUTH_GO, N5_OLD, N5_NEW, 'N5')
    b.case('N5（负控）内存路径的归属判定换个写法（等价）⇒ 必须保持绿', False,
           must_not_contain=['FAIL'],
           mutate_fn=n5)


if __name__ == '__main__':
    try:
        run(b, body, extra_runners=[
            lambda: runners()['auth'](),
            lambda: runners()['lobster'](),
            lambda: runners()['identity'](),
            lambda: runners()['notifycenter'](),
            lambda: runners()['server'](),
            lambda: runners()['repohygiene'](),
        ])
    except HarnessError as e:
        print('❌ %s' % e)
        sys.exit(3)

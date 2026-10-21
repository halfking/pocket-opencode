#!/usr/bin/env python3
"""§945：后端「被丢弃的调用结果」——持久化写失败不许无声，守卫标志不许丢。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
   **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠⚠ 本批次**需要真 PG**：判据里的故障注入要**真的落库**（发票的 exported_at
   要能读回来）⇒ ⇒ 没有 PG 时用例会 skip，而 skip 的退出码是 0 ⇒ ⇒ 本批次
   在缺 PG 时**拒绝跑**（退出码 3），而不是「跑了个寂寞」。
   见 `_harness.require_pg_dsn` 与 `go_pkg_runner(forbid_skip=True)`。

⚠⚠ 本批次有**两个判据运行器**，而且**必须一起跑**：
   ① `TestS945`（repohygiene 包）—— AST 扫描器 + 台账 + 参考集（**不需 PG**）
   ② `TestS945`（server 包）      —— 端到端（会议状态写 / 发票导出标记写）
   ⇒ ⇒⇒ ★ ① 是**普查棘轮**：它量的是「后端非测试代码里被丢弃的调用」这个
   ⇒ ⇒⇒ **集合本身** ⇒ ⇒⇒⇒ 任何一处「把错误吃掉」的写法都会让它红。
   ⇒ ⇒⇒ ★ 而 ② 量的是**行为**（失败有没有变得可见、可区分）。

⚠⚠ 本批次最该被记住的一条（M8）：
   一条只断言「源码里没有 X」的卡口，在**扫描器坏了**时同样会绿
   ⇒ ⇒⇒⇒ （扫描器什么都不报 ⇒ 「没有」成立 ⇒ 绿）⇒ ⇒⇒ 所以必须有一条
   ⇒ ⇒⇒⇒ 变异**专门去把扫描器打坏** ⇒ ⇒⇒⇒ 证明那道自证臂**会红**
   ⇒ ⇒⇒⇒ ⇒ 「门绿」≠「量具有牙」。

⚠⚠ 本批次还暴露了本节**判据自己的一条盲区**（M2 相关）：
   「进度写失败**不阻断**」这种「断言某件事**没有**发生」的判据，
   **任何合理变异都不会改变它的结论** ⇒ ⇒⇒⇒ 它是**负控**而不是行为判据
   ⇒ ⇒⇒⇒ ⇒ 它的牙只能靠**另一半**（「那件事确实发生了」）来撑
   ⇒ ⇒⇒⇒ ⇒ 所以 M2 变异的是「**根本没去写**」而不是「写失败后的处理」。

判据命令：
  go test ./internal/repohygiene/  -run 'TestS945' -count=1
  go test ./internal/server/       -run 'TestS945' -count=1
"""
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    Batch, HarnessError, go_pkg_runner, require_pg_dsn, run,
)

TARGETS = [
    'backend/internal/server/server_meeting.go',
    'backend/internal/server/server_email_pipeline.go',
    'backend/internal/server/mobile_approval_handler.go',
    'backend/internal/agentbridge/bridge.go',
    'backend/internal/repohygiene/swallowed_call_test.go',
]

# ⛔ 只注入**测试专用** DSN（独立 schema + 自动 DROP）⇒ 绝不碰生产库。
# ⚠ DSN **懒加载**：缺它时本批次「拒绝跑」（退出码 3），而不是让用例被 skip
#   ⇒ skip 的退出码是 0 ⇒ 「跑了个寂寞」与「全过」完全同形（§935.4.2）。
_RUNNERS = {}


def runners():
    if not _RUNNERS:
        env = {'POCKET_TEST_POSTGRES_DSN': require_pg_dsn()}
        _RUNNERS['repohygiene'] = go_pkg_runner('./internal/repohygiene/', 'TestS945')
        _RUNNERS['server'] = go_pkg_runner(
            './internal/server/', 'TestS945', env=env, forbid_skip=True)
    return _RUNNERS


def combined():
    """两个包的判据都跑 ⇒ 任一红就算红（收尾时两者都必须绿）。"""
    r = runners()
    rc_total = 0
    chunks = []
    for key in ('repohygiene', 'server'):
        rc, out = r[key]()
        rc_total = rc_total or rc
        chunks.append('===== %s =====\n%s' % (key, out))
    return rc_total, '\n\n'.join(chunks)


b = Batch('§945 后端「被丢弃的调用结果」：持久化写失败不许无声，守卫标志不许丢',
          combined, TARGETS)
MEETING_GO = b.targets[0]
PIPELINE_GO = b.targets[1]
MOBILE_GO = b.targets[2]
AGENTBRIDGE_GO = b.targets[3]
HYGIENE_GO = b.targets[4]

# ── 变异的锚点（必须与实现**逐字**一致；不一致 ⇒ 落盘自证会失败并中止）──
#
# ⚠⚠ **锚点必须唯一**：每条锚点在本文件里都只出现一次
#   ⇒ ⇒⇒ 否则 `b.mutate` 会因为「锚点出现 N 次」直接中止（这是好事）。

# M1：终态写失败**不再让用户看见**（setMeetingStatus 把错误吃掉）
#   ⇒ ⇒⇒ 调用方拿到 nil ⇒ 于是回 400「empty audio data」⇒ ⇒⇒⇒
#   ⇒ ⇒⇒ 会议永远停在 "transcribing"，而 HTTP 说的是「音频为空」⇒ 矛盾。
M1_OLD = ('\t\tlog.Printf("ERROR: meeting %s: 状态 %q 写入失败: %v", m.ID, status, err)\n'
          '\t\treturn err\n')
M1_NEW = ('\t\tlog.Printf("ERROR: meeting %s: 状态 %q 写入失败: %v", m.ID, status, err)\n'
          '\t\treturn nil\n')

# M2：**根本没去写**进度状态（把 markMeetingProgress 调用删掉）
#   ⇒ ⇒⇒ 「进度写失败不阻断」这条负控**依然绿**（它断言的是「没变」）
#   ⇒ ⇒⇒ ⇒ 但「确实尝试写过」那一条必须红 ⇒ ⇒⇒ ⇒ 这是本批次最该被
#   ⇒ ⇒⇒ ⇒ 记住的一条：**只断言「没发生」的判据对变异免疫**。
M2_OLD = '\ts.markMeetingProgress(m, "transcribing", uid, workspaceID)\n'
M2_NEW = ''

# M3：发票导出标记写失败 ⇒ **不点名**那张票（unmarked 收不到票 id）
#   ⇒ ⇒⇒ 下一轮会重导同一张票，而调用方**不知道是哪一张**。
M3_OLD = '\t\t\tunmarked = append(unmarked, exportedIDs[i])\n'
M3_NEW = ''

# M4：`unmarked` **总是**出现（把「有内容才加这个键」改回去）
#   ⇒ ⇒⇒ 正常路径的响应体里也会出现 `"unmarked": null`
#   ⇒ ⇒⇒ ⇒ 而 TS 的 `unmarked?: string[]` 在 strictNullChecks 下**不接受 null**
#   ⇒ ⇒⇒ ⇒ ⇒ 响应形状变了 ⇒ 负控必须红。
M4_OLD = '\tif len(unmarked) > 0 {\n'
M4_NEW = '\tif true {\n'

# M5：守卫的 fail-closed 标志又被丢掉了（改回 `workspaceID, _ :=`）
#   ⇒ ⇒⇒ ⇒ 潜伏的 fail-open 回来了（今天因 validateMobileApproval 先校验
#   ⇒ ⇒⇒ ⇒ 而不可利用，但契约已经破了）。
# ⚠ 这三处守卫调用**文本完全相同**⇒ ⇒⇒ 锚点必须带上**唯一的上下文**
#   （这里取第一处：它前面的 validateMobileApproval 调用带 `decision`，
#   而另两处带 `auth.DecisionAnswer` / `auth.DecisionReject`）⇒ ⇒⇒
#   ⇒ 否则 `b.mutate` 会因为「锚点出现 3 次」直接中止。
M5_OLD = '\tif err := s.validateMobileApproval(r, body.InstanceID, body.SessionID, requestID, decision, body.Message, nil); err != nil {\n\t\ts.writeApprovalValidationError(w, r, err)\n\t\treturn\n\t}\n\n\t// ⚠⚠ §945：原来这里是 `workspaceID, _ := s.requireMobileWorkspace(w, r)`\n\t//\t⇒ ⇒⇒ **把守卫的 fail-closed 标志丢掉了**。\n\t//\n\t//\t⚠ `requireMobileWorkspace` 的契约白纸黑字写着「**fails closed**」\n\t//\t  （mobile_endpoint_scope.go:233）⇒ ⇒⇒⇒ 守卫失败时它**已经写了**\n\t//\t  ⇒ ⇒⇒⇒ 401/400 响应 ⇒ ⇒⇒⇒ 而调用方丢掉 ok ⇒ ⇒⇒⇒ ⇒ **handler\n\t//\t  ⇒ ⇒⇒⇒ ⇒ 继续往下跑**（用一个空 workspace 去执行副作用）。\n\t//\n\t//\t⚠ **今天还不可利用**（这一点必须说清，不能夸大）：\n\t//\t  上面 :103 的 `validateMobileApproval` **已经**检查过同样的两件事\n\t//\t  （claims 非空 + workspace_id 非空 + scope 校验）⇒ ⇒⇒⇒ ⇒ 所以\n\t//\t  ⇒ ⇒⇒⇒ ⇒ 走到这里时守卫**不可能失败** ⇒ ⇒⇒⇒ ⇒ 那三个 `_` 今天\n\t//\t  ⇒ ⇒⇒⇒ ⇒ 是**死代码**，不是活的越权。\n\t//\t  ⇒ ⇒⇒⇒ 但它是**潜伏的 fail-open**：哪天有人调整了\n\t//\t  ⇒ ⇒⇒⇒ ⇒ `validateMobileApproval` 的前置顺序，这三行就会**静默**\n\t//\t  ⇒ ⇒⇒⇒ ⇒ 变成真正的越权 ⇒ ⇒⇒⇒ ⇒ 所以按守卫契约补上 `if !ok`。\n\tworkspaceID, ok := s.requireMobileWorkspace(w, r)\n\tif !ok {\n\t\treturn\n\t}\n'
M5_NEW = '\tif err := s.validateMobileApproval(r, body.InstanceID, body.SessionID, requestID, decision, body.Message, nil); err != nil {\n\t\ts.writeApprovalValidationError(w, r, err)\n\t\treturn\n\t}\n\n\t// ⚠⚠ §945：原来这里是 `workspaceID, _ := s.requireMobileWorkspace(w, r)`\n\t//\t⇒ ⇒⇒ **把守卫的 fail-closed 标志丢掉了**。\n\t//\n\t//\t⚠ `requireMobileWorkspace` 的契约白纸黑字写着「**fails closed**」\n\t//\t  （mobile_endpoint_scope.go:233）⇒ ⇒⇒⇒ 守卫失败时它**已经写了**\n\t//\t  ⇒ ⇒⇒⇒ 401/400 响应 ⇒ ⇒⇒⇒ 而调用方丢掉 ok ⇒ ⇒⇒⇒ ⇒ **handler\n\t//\t  ⇒ ⇒⇒⇒ ⇒ 继续往下跑**（用一个空 workspace 去执行副作用）。\n\t//\n\t//\t⚠ **今天还不可利用**（这一点必须说清，不能夸大）：\n\t//\t  上面 :103 的 `validateMobileApproval` **已经**检查过同样的两件事\n\t//\t  （claims 非空 + workspace_id 非空 + scope 校验）⇒ ⇒⇒⇒ ⇒ 所以\n\t//\t  ⇒ ⇒⇒⇒ ⇒ 走到这里时守卫**不可能失败** ⇒ ⇒⇒⇒ ⇒ 那三个 `_` 今天\n\t//\t  ⇒ ⇒⇒⇒ ⇒ 是**死代码**，不是活的越权。\n\t//\t  ⇒ ⇒⇒⇒ 但它是**潜伏的 fail-open**：哪天有人调整了\n\t//\t  ⇒ ⇒⇒⇒ ⇒ `validateMobileApproval` 的前置顺序，这三行就会**静默**\n\t//\t  ⇒ ⇒⇒⇒ ⇒ 变成真正的越权 ⇒ ⇒⇒⇒ ⇒ 所以按守卫契约补上 `if !ok`。\n\tworkspaceID, _ := s.requireMobileWorkspace(w, r)\n'

# M6：台账里登记一个**持久化写** ⇒ 「台账里不许有持久化写」那条必须红
#   ⇒ ⇒⇒ ⇒ 这是本卡口的**牙**：光有「双向相等」不够，否则「登记一下就
#   ⇒ ⇒⇒ ⇒ 放过」这条路是通的。
# ⚠§951 连带修正：§951 把 internal/notify/smtp.go 的 SMTP 拨号改成了
#   「自己拨号 + 每一个错误分支都显式关连接」⇒ 那一条台账的条数由 **2** 变成 **7**
#   ⇒ ⇒⇒ ⇒ 锚点必须跟着走（否则本批会「锚点出现 0 次 ⇒ 锚点可能已过期」而**拒绝跑**）。
M6_OLD = '\t{"backend/internal/notify/smtp.go", "Close", 7},\n'
M6_NEW = '\t{"backend/internal/notify/smtp.go", "UpdateStatus", 7},\n'

# M7：把 §945 参考集里的一条删掉 ⇒ 参考集的**绝对条数**必须红
#   ⇒ ⇒⇒ ⇒ 这条防的是「把参考集改小来消红」。
# ⚠ 锚点必须与 gofmt 之后的**逐字**形态一致（gofmt 把 `,` 与 `}` 分成两行）。
M7_OLD = ('\t{File: "backend/internal/server/mobile_approval_handler.go", Method: "requireMobileWorkspace",\n'
          '\t\tWhy: "守卫的 **fail-closed 标志**被丢掉（3 处）：守卫已写了 401/400 响应而 handler 继续跑 ⇒ 今天因 validateMobileApproval 已先校验而**不可利用**，但是**潜伏的 fail-open** ⇒ 按契约补 `if !ok`。",\n'
          '\t},\n')
M7_NEW = ''

# M8：**把扫描器打坏**（判定函数恒返回 false）
#   ⇒ ⇒⇒ ⇒ 自证臂（合成源码那一条）必须红 ⇒ ⇒⇒⇒ ⇒ 这是「门绿 ≠ 量具有牙」
#   ⇒ ⇒⇒ ⇒ 的直接反证。
M8_OLD = ('\tif len(as.Lhs) == 0 {\n'
          '\t\treturn false\n'
          '\t}\n')
M8_NEW = ('\tif len(as.Lhs) >= 0 {\n'
          '\t\treturn false\n'
          '\t}\n')

# M9：agent 实例状态写**又被吃掉**（改回 `_ =`）
#   ⇒ ⇒⇒ ⇒ 仓库级参考集（agentbridge :: UpdateStatus）必须红。
M9_OLD = '\tb.updateStatus(ctx, agentID, StatusOnline)\n'
M9_NEW = '\t_ = b.store.UpdateStatus(ctx, agentID, StatusOnline)\n'

# ── 负控的锚点（语义等价 ⇒ 必须保持绿）────────────────────────────────

# N1：日志的格式化从 %v 换成 %s（error 的 Error() 文本一致）⇒ 等价。
N1_OLD = '\t\tlog.Printf("ERROR: meeting %s: 状态 %q 写入失败: %v", m.ID, status, err)\n'
N1_NEW = '\t\tlog.Printf("ERROR: meeting %s: 状态 %q 写入失败: %s", m.ID, status, err)\n'

# N2：`len(unmarked) > 0` 换成 `len(unmarked) != 0`（等价）⇒ 必须保持绿。
N2_OLD = '\tif len(unmarked) > 0 {\n'
N2_NEW = '\tif len(unmarked) != 0 {\n'

# N3：台账里「Close」那条的**理由文字**改写（判据不看理由内容）⇒ 等价。
N3_OLD = '"Close": "关闭句柄/连接/文件。失败之后**没有可执行的后续动作**（句柄已经不可用），" +\n'
N3_NEW = '"Close": "关闭句柄/连接/文件。失败之后**没有可执行的后续动作**（句柄已经不可用了），" +\n'


def body():
    rc, out = combined()
    b.record('基线：未变异时判据必须全绿（exit 0）', rc == 0, 'exit=%d\n%s' % (rc, out[-1500:]))

    # ── M1 必须红：终态写失败不再让用户看见 ───────────────────────────
    #
    # ⚠ 期望消息是**实测**出来的：钉的是「状态码不是 500」那个症状。
    def m1():
        b.mutate(MEETING_GO, M1_OLD, M1_NEW, 'M1')
    b.case('M1 终态写失败被吃掉（setMeetingStatus 返回 nil）⇒ 用户看不见 ⇒ 判据必须红', True,
           must_contain=['期望 500'],
           mutate_fn=m1)

    # ── M2 必须红：进度状态**根本没去写** ─────────────────────────────
    #
    # ⚠ 这一条同时证明「只断言『没变』的判据对变异免疫」：
    #   若没有「确实尝试写过」那一条断言，M2 **不会红** ⇒ ⇒⇒⇒
    #   ⇒ 判据得只有一半 ⇒ ⇒⇒⇒ ⇒ 「负控」要写成「负控 + 它的另一半」。
    def m2():
        b.mutate(MEETING_GO, M2_OLD, M2_NEW, 'M2')
    b.case('M2 进度状态**根本没去写**（markMeetingProgress 被删）⇒ 判据必须红', True,
           must_contain=['进度状态没有被尝试写入'],
           mutate_fn=m2)

    # ── M3 必须红：导出标记写失败不点名那张票 ─────────────────────────
    def m3():
        b.mutate(PIPELINE_GO, M3_OLD, M3_NEW, 'M3')
    b.case('M3 发票导出标记写失败**不点名**那张票（unmarked 收不到票 id）⇒ 判据必须红', True,
           must_contain=['unmarked ='],
           mutate_fn=m3)

    # ── M4 必须红：unmarked 总是出现（响应形状变了）──────────────────
    def m4():
        b.mutate(PIPELINE_GO, M4_OLD, M4_NEW, 'M4')
    b.case('M4 `unmarked` 总是出现（正常路径也多一个 null 键）⇒ 响应形状变了 ⇒ 判据必须红', True,
           must_contain=['unmarked 键'],
           mutate_fn=m4)

    # ── M5 必须红：守卫的 fail-closed 标志又被丢掉 ───────────────────
    def m5():
        b.mutate(MOBILE_GO, M5_OLD, M5_NEW, 'M5')
    b.case('M5 守卫的 fail-closed 标志又被丢掉（改回 `workspaceID, _ :=`）⇒ 判据必须红', True,
           must_contain=['被丢弃'],
           mutate_fn=m5)

    # ── M6 必须红：台账里登记一个持久化写 ───────────────────────────
    def m6():
        b.mutate(HYGIENE_GO, M6_OLD, M6_NEW, 'M6')
    b.case('M6 台账里登记一个**持久化写** ⇒ 「台账里不许有持久化写」必须红', True,
           must_contain=['持久化写'],
           mutate_fn=m6, runner=lambda: runners()['repohygiene']())

    # ── M7 必须红：参考集被改小 ─────────────────────────────────────
    def m7():
        b.mutate(HYGIENE_GO, M7_OLD, M7_NEW, 'M7')
    b.case('M7 §945 参考集被删掉一条 ⇒ 参考集的绝对条数必须红', True,
           must_contain=['参考集条数'],
           mutate_fn=m7, runner=lambda: runners()['repohygiene']())

    # ── M8 必须红：**把扫描器打坏** ⇒ 自证臂必须红 ──────────────────
    #
    # ⚠⚠ 这是本批次最该被记住的一条：一条只断言「源码里没有 X」的卡口，
    #   在**扫描器坏了**时同样会绿 ⇒ ⇒⇒⇒ 所以必须有一条变异**专门**
    #   ⇒ ⇒⇒⇒ 去打坏扫描器 ⇒ ⇒⇒⇒ 证明那道自证臂**会红**。
    def m8():
        b.mutate(HYGIENE_GO, M8_OLD, M8_NEW, 'M8')
    b.case('M8 把扫描器打坏（判定函数恒返回 false）⇒ 自证臂必须红（证明量具有牙）', True,
           must_contain=['扫描器可能坏了'],
           mutate_fn=m8, runner=lambda: runners()['repohygiene']())

    # ── M9 必须红：agent 实例状态写又被吃掉 ─────────────────────────
    def m9():
        b.mutate(AGENTBRIDGE_GO, M9_OLD, M9_NEW, 'M9')
    b.case('M9 agent 实例状态写又被吃掉（改回 `_ =`）⇒ 仓库级参考集必须红', True,
           must_contain=['被丢弃'],
           mutate_fn=m9, runner=lambda: runners()['repohygiene']())

    # ── N1–N3 负控：语义等价的改写 ⇒ 必须保持绿 ─────────────────────
    for label, path, old, new, desc in (
        ('N1', MEETING_GO, N1_OLD, N1_NEW, '日志格式化 %v → %s（等价）'),
        ('N2', PIPELINE_GO, N2_OLD, N2_NEW, '`len(x) > 0` → `len(x) != 0`（等价）'),
        ('N3', HYGIENE_GO, N3_OLD, N3_NEW, '台账里「Close」那条的理由文字改写（等价）'),
    ):
        def mk(path=path, old=old, new=new):
            def fn():
                b.mutate(path, old, new, label)
            return fn
        b.case('%s（负控）%s ⇒ 必须保持绿' % (label, desc), False,
               must_not_contain=['FAIL'],
               mutate_fn=mk())


if __name__ == '__main__':
    try:
        run(b, body, extra_runners=[
            lambda: runners()['repohygiene'](),
            lambda: runners()['server'](),
        ])
    except HarnessError as e:
        print('❌ %s' % e)
        sys.exit(3)

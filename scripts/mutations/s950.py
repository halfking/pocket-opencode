#!/usr/bin/env python3
"""§950：出站 HTTP 必须有上界（收进 outbound 包 + 卡口）。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
   **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠ 本批次是本仓**第六批「把量具自己打坏」的变异**（§945 的 M8、§946 的 M13、
   §947 的 M3–M6、§948 的 M4–M8、§949 的 M9、本批的 **M7/M10/M11/M12**）
   ⇒ ⇒ 每次都必须证明：**量具坏了 ⇒ 门会红**。

判据命令：
  go test ./internal/repohygiene/ ./internal/outbound/ -run 'TestS950|TestNewClient|...' -count=1

⚠ 本批次最该被记住的两条：

★ **M11/M12 是「量具整族漏掉一种形态」**（§949 的 M9 同族）：
  判据识别「值类型用法」与「new(http.Client)」时，两处各一个条件项。
  把其中任何一处打坏 ⇒ 对应的合成用例必须红
  ⇒ ⇒⇒ 「量出来的数比该有的少 ⇒ 第一嫌疑是量具漏了一族形态」。

★ **M10 是「量具过宽」**（§949 的 M2 同族）：
  把 `http.NewRequestWithContext` 也当成「不带 ctx 的请求」
  ⇒ ⇒⇒ 负控必须红 —— 否则这道门会把**正常代码**判红（误报比漏报更招人烦）。

★ **M8/M9 是「兜底被掏空」的两种漏法**：
  M8 把「timeout <= 0 ⇒ 回落默认值」整段删掉；
  M9 把它改成「回落成 0」——**两处都必须红**，否则「配置漏填 ⇒ 无上界」这条路是通的。
"""

import os
import subprocess
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    BACKEND, Batch, run,
)

TARGETS = [
    'backend/internal/repohygiene/outbound_http_bound_test.go',
    'backend/internal/outbound/outbound.go',
    'backend/internal/registry/registry.go',
    'backend/internal/feishu/client.go',
    'backend/internal/redclaw/client.go',
    'backend/internal/facade/events.go',
    'backend/internal/registry/discovery.go',
]

# ⚠ 判据要同时跑**卡口**与**outbound 的单元测试** ——
#   后者才是「timeout <= 0 ⇒ 回落默认值」这条兜底的牙。
RUN_PATTERN = (
    'TestS950|TestNewClientAlwaysHasBound|TestNewStreamingClientHasNoTotalTimeout|'
    'TestOptionsAreApplied|TestNilOptionIsIgnored|TestEachCallReturnsFreshClient|'
    'TestRawClientHasNoBound'
)


def s950_runner():
    cmd = ['go', 'test', './internal/repohygiene/', './internal/outbound/',
           '-run', RUN_PATTERN, '-count=1']

    def run_it():
        p = subprocess.run(cmd, cwd=str(BACKEND), capture_output=True,
                           text=True, timeout=1800)
        return p.returncode, p.stdout + p.stderr
    return run_it


b = Batch('§950 出站 HTTP 必须有上界（outbound 包 + 卡口 + 两本台账）',
          s950_runner(), TARGETS)

GATE = b.targets[0]
HELPER = b.targets[1]
REGISTRY = b.targets[2]
FEISHU = b.targets[3]
REDCLAW = b.targets[4]
EVENTS = b.targets[5]
DISCOVERY = b.targets[6]


def main():
    # ── M1 必须红：把一个客户端改回**裸字面量**（没有字面量 ⇒ 就没有兜底）──
    M1_OLD = '\tclient := outbound.NewClient(3 * time.Second)\n'
    M1_NEW = ('\tclient := outbound.NewClient(3 * time.Second)\n'
              '\t_ = &http.Client{}\n')

    def m1():
        b.mutate(REGISTRY, M1_OLD, M1_NEW, 'M1')
    b.case('M1 加一处裸 `&http.Client{}` 字面量 ⇒ 那个客户端 Timeout=0 ⇒ 没有上界 ⇒ 判据必须红', True,
           must_contain=['有一处 `http.Client{…}` 字面量'],
           mutate_fn=m1)

    # ── M2 必须红：用 `http.DefaultClient`（它没有 Timeout）────────────────
    M2_OLD = '// Available 报告出站能力是否配置齐全。\n'
    M2_NEW = ('// s950mutant 是变异用：DefaultClient 没有 Timeout（门必须红）。\n'
              'func s950mutant() *http.Client {\n'
              '\treturn http.DefaultClient\n'
              '}\n\n'
              '// Available 报告出站能力是否配置齐全。\n')

    def m2():
        b.mutate(FEISHU, M2_OLD, M2_NEW, 'M2')
    b.case('M2 引入 `http.DefaultClient` ⇒ 没有客户端级上界 ⇒ 判据必须红', True,
           must_contain=['用了 `http.DefaultClient`'],
           mutate_fn=m2)

    # ── M3 必须红：用包级 `http.Get`（内部就是 DefaultClient）─────────────
    M3_OLD = '// checkInstanceHealth 检查单个实例健康状态，并尝试从 health 响应中\n'
    M3_NEW = ('// s950mutant 是变异用：包级 http.Get 走的是 DefaultClient（门必须红）。\n'
              'func s950mutant() {\n'
              '\tresp, err := http.Get("http://example.invalid")\n'
              '\tif err == nil {\n'
              '\t\tresp.Body.Close()\n'
              '\t}\n'
              '}\n\n'
              '// checkInstanceHealth 检查单个实例健康状态，并尝试从 health 响应中\n')

    def m3():
        b.mutate(REGISTRY, M3_OLD, M3_NEW, 'M3')
    b.case('M3 引入包级 `http.Get` ⇒ 内部是 DefaultClient ⇒ 没有上界 ⇒ 判据必须红', True,
           must_contain=['用了包级 http 便利函数'],
           mutate_fn=m3)

    # ── M4 必须红：新增一个**没登记**的不带 ctx 的请求 ────────────────────
    M4_OLD = '\treturn c.httpDo.Do(req)\n}\n'
    M4_NEW = ('\treturn c.httpDo.Do(req)\n}\n\n'
              '// s950mutant 是变异用：没登记的不带 ctx 的请求（门必须红）。\n'
              'func s950mutant(rawurl string) error {\n'
              '\t_, err := http.NewRequest(http.MethodGet, rawurl, nil)\n'
              '\treturn err\n'
              '}\n')

    def m4():
        b.mutate(REDCLAW, M4_OLD, M4_NEW, 'M4')
    b.case('M4 新增一个没登记的「不带 ctx 的 http.NewRequest」⇒ 台账失配 ⇒ 判据必须红', True,
           must_contain=['台账里没有登记'],
           mutate_fn=m4)

    # ── M5 必须红：把已登记的流式客户端改成带总超时的 ⇒ 台账项消失 ────────
    M5_OLD = '\thttpClient := outbound.NewStreamingClient()\n'
    M5_NEW = '\thttpClient := outbound.NewClient(30 * time.Second)\n'

    def m5():
        b.mutate(EVENTS, M5_OLD, M5_NEW, 'M5')
    b.case('M5 把已登记的流式客户端改成带总超时的 ⇒ 台账项在源码里消失 ⇒ 判据必须红', True,
           must_contain=['在源码里**不存在了**'],
           mutate_fn=m5)

    # ── M6 必须红：新增一个**没登记**的流式客户端 ────────────────────────
    M6_OLD = '\tresp, err := httpClient.Do(req)\n'
    M6_NEW = ('\tresp, err := httpClient.Do(req)\n')

    def m6():
        s = EVENTS.read_text(encoding='utf-8')
        s += ('\n// s950mutant 是变异用：没登记的流式客户端（门必须红）。\n'
              'func s950mutant() *http.Client {\n'
              '\treturn outbound.NewStreamingClient()\n'
              '}\n')
        EVENTS.write_text(s, encoding='utf-8')
    b.case('M6 新增一个没登记的 `outbound.NewStreamingClient` ⇒ 台账失配 ⇒ 判据必须红', True,
           must_contain=['台账里没有登记'],
           mutate_fn=m6)

    # ── M7 必须红：量具**漏掉「别名导入」这一族形态** ⇒ 自证臂必须红 ──────
    M7_OLD = ('\t\tif imp.Name != nil {\n'
              '\t\t\tif imp.Name.Name == "." {\n'
              '\t\t\t\treturn "", true\n'
              '\t\t\t}\n'
              '\t\t\treturn imp.Name.Name, false\n'
              '\t\t}\n')
    M7_NEW = ('\t\tif imp.Name != nil {\n'
              '\t\t\tif imp.Name.Name == "." {\n'
              '\t\t\t\treturn "", true\n'
              '\t\t\t}\n'
              '\t\t\treturn "http", false\n'
              '\t\t}\n')

    def m7():
        b.mutate(GATE, M7_OLD, M7_NEW, 'M7')
    b.case('M7 量具漏掉「net/http 别名导入」这一族形态 ⇒ 合成用例必须红 ⇒ 判据必须红', True,
           must_contain=['扫描器的判断不对'],
           mutate_fn=m7)

    # ── M8 必须红：掏掉 outbound 的「timeout <= 0 ⇒ 回落默认值」兜底 ──────
    M8_OLD = ('\tif timeout <= 0 {\n'
              '\t\ttimeout = DefaultTimeout\n'
              '\t}\n')

    def m8():
        b.mutate(HELPER, M8_OLD, '', 'M8')
    b.case('M8 掏掉「timeout <= 0 ⇒ 回落默认值」⇒ 配置漏填就变成无上界 ⇒ 判据必须红', True,
           must_contain=['TestNewClientAlwaysHasBound/零值回落默认'],
           mutate_fn=m8)

    # ── M9 必须红：兜底改成「回落成 0」（另一种漏法）─────────────────────
    M9_OLD = '\t\ttimeout = DefaultTimeout\n'
    M9_NEW = '\t\ttimeout = 0\n'

    def m9():
        b.mutate(HELPER, M9_OLD, M9_NEW, 'M9')
    b.case('M9 把兜底改成「回落成 0」⇒ 仍然是漏的 ⇒ 判据必须红', True,
           must_contain=['TestNewClientAlwaysHasBound/零值回落默认'],
           mutate_fn=m9)

    # ── M10 必须红：量具**过宽**（把 WithContext 也当成 ctx-less）⇒ 负控红 ─
    M10_OLD = 'if s950IsNetHTTPSelector(sel, httpLocal, httpDot, "NewRequest") {'
    M10_NEW = 'if s950IsNetHTTPSelector(sel, httpLocal, httpDot, "NewRequest", "NewRequestWithContext") {'

    def m10():
        b.mutate(GATE, M10_OLD, M10_NEW, 'M10')
    b.case('M10 量具过宽：把 `http.NewRequestWithContext` 也当成 ctx-less ⇒ 负控必须红', True,
           must_contain=['扫描器的判断不对'],
           mutate_fn=m10)

    # ── M11 必须红：量具**漏掉「值类型用法」这一族形态** ⇒ 自证臂必须红 ──
    M11_OLD = 'if sel, ok := s950HTTPClientSelector(v, httpLocal, httpDot); ok && !allowed[sel] {'
    M11_NEW = 'if sel, ok := s950HTTPClientSelector(v, httpLocal, httpDot); ok && !allowed[sel] && false {'

    def m11():
        b.mutate(GATE, M11_OLD, M11_NEW, 'M11')
    b.case('M11 量具漏掉「http.Client 值类型用法」这一族形态 ⇒ 合成用例必须红 ⇒ 判据必须红', True,
           must_contain=['扫描器的判断不对'],
           mutate_fn=m11)

    # ── M12 必须红：量具**漏掉「new(http.Client)」这一族形态** ⇒ 自证臂红 ──
    M12_OLD = 'if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "new" && len(v.Args) == 1 {'
    M12_NEW = 'if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "never" && len(v.Args) == 1 {'

    def m12():
        b.mutate(GATE, M12_OLD, M12_NEW, 'M12')
    b.case('M12 量具漏掉「new(http.Client)」这一族形态（零值客户端）⇒ 合成用例必须红 ⇒ 判据必须红', True,
           must_contain=['扫描器的判断不对'],
           mutate_fn=m12)

    # ── N1 负控：改 outbound 的注释措辞 ⇒ 必须保持绿 ─────────────────────
    N1_OLD = '// DefaultTimeout 是 NewClient 在 timeout <= 0 时回落的默认总超时。\n'
    N1_NEW = '// DefaultTimeout 是 NewClient 在 timeout <= 0 时回落的默认总超时（兜底用）。\n'

    def n1():
        b.mutate(HELPER, N1_OLD, N1_NEW, 'N1')
    b.case('N1 outbound 的注释措辞微调 ⇒ 必须保持绿', False,
           must_not_contain=['TestNewClientAlwaysHasBound/零值回落默认'],
           mutate_fn=n1)

    # ── N2 负控：新增一个**合法**的出站客户端 ⇒ 必须保持绿 ───────────────
    N2_OLD = '// probeCandidates concurrently probes all candidates and returns configs for\n'
    N2_NEW = ('// s950ok 是合法的新增出站客户端（负控：必须保持绿）。\n'
              'func s950ok() *http.Client {\n'
              '\treturn outbound.NewClient(7 * time.Second)\n'
              '}\n\n'
              '// probeCandidates concurrently probes all candidates and returns configs for\n')

    def n2():
        b.mutate(DISCOVERY, N2_OLD, N2_NEW, 'N2')
    b.case('N2 新增一个**合法**的 outbound.NewClient 调用点 ⇒ 这道门不是「任何改动都红」⇒ 必须保持绿', False,
           must_not_contain=['有一处 `http.Client{…}` 字面量'],
           mutate_fn=n2)

    # ── N3 负控：改台账里某一条的**理由文字** ⇒ 必须保持绿 ───────────────
    N3_OLD = 'Why: "客户端 Timeout 兜底（outbound.NewClient 保证 > 0）；调用链是 redclaw 的控制面短请求",'
    N3_NEW = 'Why: "客户端 Timeout 兜底（outbound.NewClient 保证 > 0）；调用链是 redclaw 控制面的短请求",'

    def n3():
        b.mutate(GATE, N3_OLD, N3_NEW, 'N3')
    b.case('N3 台账理由的措辞微调（判据只看理由非空）⇒ 必须保持绿', False,
           must_not_contain=['台账里没有登记'],
           mutate_fn=n3)

    # ── N4 负控：在 outbound 包里加一个**不造客户端**的辅助函数 ⇒ 必须绿 ──
    N4_OLD = 'func NewClient(timeout time.Duration, opts ...Option) *http.Client {\n'
    N4_NEW = ('// Describe 只是描述一个出站客户端（变异用，不造客户端）。\n'
              'func Describe(timeout time.Duration) string {\n'
              '\treturn "outbound client timeout=" + timeout.String()\n'
              '}\n\n'
              'func NewClient(timeout time.Duration, opts ...Option) *http.Client {\n')

    def n4():
        b.mutate(HELPER, N4_OLD, N4_NEW, 'N4')
    b.case('N4 outbound 包里加一个**不造客户端**的辅助函数 ⇒ 必须保持绿', False,
           must_not_contain=['字面量数'],
           mutate_fn=n4)


if __name__ == '__main__':
    run(b, main)

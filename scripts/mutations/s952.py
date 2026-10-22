#!/usr/bin/env python3
"""§952：读一个不知道多大的东西，必须自带上界（后端读取大小上界）。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
  **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠ 本批次是本仓**第八批「把量具自己打坏」的变异**（§945 的 M8、§946 的 M13、
  §947 的 M3–M6、§948 的 M4–M8、§949 的 M9、§950 的 M7/M10/M11/M12、
  §951 的 M10–M13/F6、本批的 **M15–M21**）⇒ ⇒ 每次都必须证明：
  **量具坏了 ⇒ 门会红**。

判据命令：
  go test ./internal/repohygiene/ -run 'TestS952' -count=1   （后端卡口）
  go test ./internal/sizedread/  -count=1                   （收口自身的自证）
  go test ./internal/rss/ -run 'TestReadLimited|TestSetMaxBodyBytes' -count=1

⚠ 本批次最该被记住的三条：

★ **M1–M4 是「收口自己坏了」**：
  `sizedread.All` 不再限流 / `Limit` 超限时伪装成 EOF / `Limit` 读满就报错
  / `Snippet` 不再截断 ⇒⇒⇒ ⇒ **四种坏法各不相同**，观察口也各不相同
  ⇒ ⇒⇒ ★ **同一个包里的四个函数，要用四条不同的观察口**。

★ **M2 与 M3 是一对「双边」**（§951 的教训）：
  M2（超限伪装成 EOF）⇒ 「太大」被伪装成「半个 JSON」⇒ 观察口是
     `TestLimitReportsTooLargeNotEOF`；
  M3（读满就报错）⇒ 「**恰好等于**上限」被误判成超限 ⇒ 观察口是
     `TestAllAcceptsExactlyAtLimit`。
  ⇒ ⇒⇒ ⇒ 只钉一侧的话，另一侧会照样绿。

★ **M15–M21 是「量具自己瞎掉」**：
  判据漏认一族形态（`sizedread.Limit` / `http.MaxBytesReader` 绑定 /
  `io.Discard` 目标 / 入站 body 的读取点限流）、上限检查恒真、
  中间件检查恒真、「有界」恒真 ⇒⇒⇒ ⇒ 每一种都必须让门**红**
  ⇒ ⇒⇒ ⇒ ★ **「量具的读数比该有的少 ⇒ 先查整族形态没被认出来」**
  本节的量具在施工时**自己**漏认了三族（M15–M17）⇒ 都是自证臂抓的。
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
    # 收口本体 + 判据
    'backend/internal/sizedread/sizedread.go',
    'backend/internal/repohygiene/bounded_read_test.go',
    # 被测的调用点（每族挑一处代表）
    'backend/internal/adapter/opencode_http.go',
    'backend/internal/agent/helpers.go',
    'backend/internal/agent/transport_http.go',
    'backend/internal/email/mime.go',
    'backend/internal/rss/url.go',
    'backend/internal/updates/updates.go',
    'backend/internal/server/server_assistant.go',
]


def _run(cmd, cwd):
    p = subprocess.run(cmd, cwd=str(cwd), capture_output=True, text=True, timeout=1800)
    return p.returncode, p.stdout + p.stderr


def judge_runner():
    """后端卡口（§952 的判据）—— 批次的**默认**运行器。"""
    def run_it():
        return _run(
            ['go', 'test', './internal/repohygiene/', '-run', 'TestS952', '-count=1'],
            BACKEND)
    return run_it


def sizedread_runner():
    """收口自身的自证（`internal/sizedread` 的单元测试）。"""
    def run_it():
        return _run(['go', 'test', './internal/sizedread/', '-count=1'], BACKEND)
    return run_it


def rss_runner():
    """台账里「上限由调用方给」那条的**行为证明**。"""
    def run_it():
        return _run(
            ['go', 'test', './internal/rss/', '-run', 'TestReadLimited|TestSetMaxBodyBytes',
             '-count=1'],
            BACKEND)
    return run_it


b = Batch('§952 读一个不知道多大的东西，必须自带上界（后端读取大小上界）',
          judge_runner(), TARGETS)

SIZED = b.targets[0]
GATE = b.targets[1]
OPENCODE = b.targets[2]
AGENT_HELPERS = b.targets[3]
AGENT_HTTP = b.targets[4]
MIME = b.targets[5]
RSS_URL = b.targets[6]
UPDATES = b.targets[7]
ASSISTANT = b.targets[8]


def main():
    # ── M1 必须红：`sizedread.All` 不再限流 ⇒ 收口本身失效 ──────────────
    M1_OLD = '\tb, err := io.ReadAll(Limit(r, max))\n'
    M1_NEW = '\tb, err := io.ReadAll(r)\n'

    def m1():
        b.mutate(SIZED, M1_OLD, M1_NEW, 'M1')
    b.case('M1 `sizedread.All` 不再限流 ⇒ 收口失效 ⇒ 收口自证必须红', True,
           must_contain=['TestAllRejectsOverLimit'],
           mutate_fn=m1, runner=sizedread_runner())

    # ── M2 必须红：`Limit` 超限时伪装成 EOF ⇒「太大」退化成「半个 JSON」──
    M2_OLD = ('\t\tif n > 0 || err == nil {\n'
              '\t\t\treturn 0, fmt.Errorf("%w: limit %d bytes", ErrTooLarge, l.max)\n'
              '\t\t}\n')
    # ⚠ 不能写成 `return 0, io.EOF`（那会让 fmt 变成**未使用** ⇒ 编译失败
    #   ⇒ 门是「编译错」红，不是「判据」红 ⇒ 必须保留 fmt 的使用）。
    M2_NEW = ('\t\tif n > 0 || err == nil {\n'
              '\t\t\treturn 0, fmt.Errorf("%w: limit %d bytes", io.EOF, l.max)\n'
              '\t\t}\n')

    def m2():
        b.mutate(SIZED, M2_OLD, M2_NEW, 'M2')
    b.case('M2 `Limit` 超限时伪装成 EOF ⇒「太大」与「读完了」同形 ⇒ 收口自证必须红', True,
           must_contain=['TestLimitReportsTooLargeNotEOF'],
           mutate_fn=m2, runner=sizedread_runner())

    # ── M3 必须红：`Limit` 读满就报错 ⇒「**恰好等于**上限」被误判成超限 ──
    # ⚠ 这是 M2 的**另一半**（§951 的「双边」纪律）：只钉上限那一侧的话，
    #   这条变异会照样绿。
    M3_OLD = ('\tif l.n <= 0 {\n'
              '\t\t// 上限已用尽：**探一个字节**，确认源里是不是真的还有内容。\n')
    M3_NEW = ('\tif l.n <= 0 {\n'
              '\t\t// s952mutant：不探测，直接报超限（门必须红）。\n'
              '\t\treturn 0, fmt.Errorf("%w: limit %d bytes", ErrTooLarge, l.max)\n'
              '\t\t// 上限已用尽：**探一个字节**，确认源里是不是真的还有内容。\n')

    def m3():
        b.mutate(SIZED, M3_OLD, M3_NEW, 'M3')
    b.case('M3 `Limit` 读满就报超限 ⇒「恰好等于上限」被误拒 ⇒ 收口自证必须红', True,
           must_contain=['TestAllAcceptsExactlyAtLimit'],
           mutate_fn=m3, runner=sizedread_runner())

    # ── M4 必须红：`Snippet` 不再截断 ⇒ 错误文案会把整个响应体读进来 ────
    M4_OLD = '\tb, err := io.ReadAll(io.LimitReader(r, max))\n'
    M4_NEW = '\tb, err := io.ReadAll(r)\n'

    def m4():
        b.mutate(SIZED, M4_OLD, M4_NEW, 'M4')
    b.case('M4 `Snippet` 不再截断 ⇒ 错误文案把整个响应体读进内存 ⇒ 收口自证必须红', True,
           must_contain=['TestSnippetTruncatesSilently'],
           mutate_fn=m4, runner=sizedread_runner())

    # ── M5 必须红：某处错误消息体改回「整个读进来」──────────────────────
    M5_OLD = ('\t\tbody := sizedread.Snippet(resp.Body, sizedread.MaxErrorBodyBytes)\n'
              '\t\treturn nil, fmt.Errorf("opencode get permission requests returned %d: %s", resp.StatusCode, body)\n')
    M5_NEW = ('\t\tbody, _ := io.ReadAll(resp.Body)\n'
              '\t\treturn nil, fmt.Errorf("opencode get permission requests returned %d: %s", resp.StatusCode, body)\n')

    def m5():
        b.mutate(OPENCODE, M5_OLD, M5_NEW, 'M5')
    b.case('M5 错误消息体改回 `io.ReadAll(resp.Body)` ⇒ 出站响应体无界 ⇒ 卡口必须红', True,
           must_contain=['TestS952NoUnboundedReadsExceptLedger'],
           mutate_fn=m5)

    # ── M6 必须红：某处成功载荷改回「整个读进来」────────────────────────
    M6_OLD = ('\tbody, err := sizedread.All(resp.Body, sizedread.MaxJSONBodyBytes)\n'
              '\tif err != nil {\n'
              '\t\treturn nil, fmt.Errorf("read messages body failed: %w", err)\n'
              '\t}\n')
    M6_NEW = ('\tbody, err := io.ReadAll(resp.Body)\n'
              '\tif err != nil {\n'
              '\t\treturn nil, fmt.Errorf("read messages body failed: %w", err)\n'
              '\t}\n')

    def m6():
        b.mutate(OPENCODE, M6_OLD, M6_NEW, 'M6')
    b.case('M6 成功载荷改回 `io.ReadAll(resp.Body)` ⇒ 出站响应体无界 ⇒ 卡口必须红', True,
           must_contain=['TestS952NoUnboundedReadsExceptLedger'],
           mutate_fn=m6)

    # ── M7 必须红：某处解码器改回「直接读 body」────────────────────────
    M7_OLD = '\tif err := json.NewDecoder(sizedread.Limit(resp.Body, sizedread.MaxJSONBodyBytes)).Decode(&result); err != nil {\n'
    M7_NEW = '\tif err := json.NewDecoder(resp.Body).Decode(&result); err != nil {\n'

    def m7():
        b.mutate(OPENCODE, M7_OLD, M7_NEW, 'M7', count=2)
    b.case('M7 解码器改回直接读 body ⇒ 出站响应体无界 ⇒ 卡口必须红', True,
           must_contain=['TestS952NoUnboundedReadsExceptLedger'],
           mutate_fn=m7)

    # ── M8 必须红：删掉 updates handler 的入站上限 ⇒ 入站请求体无界 ─────
    M8_OLD = '\tr.Body = http.MaxBytesReader(w, r.Body, maxCheckUpdateBodyBytes)\n'

    def m8():
        b.mutate(UPDATES, M8_OLD, '', 'M8')
    b.case('M8 删掉 updates handler 的入站上限 ⇒ 入站请求体无界 ⇒ 卡口必须红', True,
           must_contain=['TestS952NoUnboundedReadsExceptLedger'],
           mutate_fn=m8)

    # ── M9 必须红：MIME 部件正文改回「整个读进来」──────────────────────
    M9_OLD = '\t\traw, err := sizedread.All(r, maxMIMEPartBytes)\n'
    M9_NEW = '\t\traw, err := io.ReadAll(r)\n'

    def m9():
        b.mutate(MIME, M9_OLD, M9_NEW, 'M9')
    b.case('M9 MIME 部件正文改回 `io.ReadAll(r)` ⇒ 邮件部件无界 ⇒ 卡口必须红', True,
           must_contain=['TestS952NoUnboundedReadsExceptLedger'],
           mutate_fn=m9)

    # ── M10 必须红：agent 协议帧改回「整个读进来」──────────────────────
    M10_OLD = '\treturn sizedread.All(r, maxAgentFrameBytes)\n'
    M10_NEW = '\treturn io.ReadAll(r)\n'

    def m10():
        b.mutate(AGENT_HELPERS, M10_OLD, M10_NEW, 'M10')
    b.case('M10 agent 协议帧改回 `io.ReadAll(r)` ⇒ 响应帧无界 ⇒ 卡口必须红', True,
           must_contain=['TestS952NoUnboundedReadsExceptLedger'],
           mutate_fn=m10)

    # ── M11 必须红：删掉 stt handler 里的入站上限 ─────────────────────
    M11_OLD = '\tr.Body = http.MaxBytesReader(w, r.Body, maxAudioBodyBytes)\n'

    # ⚠ **这条是负控**，不是「必须红」。
    #   为什么：`internal/server` 包的入站请求体由**全局限流中间件**兜住
    #   （`/api/stt/transcribe` 前缀 ⇒ limit = maxAudioBodyBytes，
    #   与 handler 里写的那个**同一个数字**）⇒⇒⇒ ⇒ 删掉 handler 里这一层，
    #   对判据是**冗余的** ⇒ 门**必须保持绿**。
    #   ⇒⇒⇒ ⇒ 但代码里仍然保留它（note_ingest.go 的纪律：「让这条链的上限
    #   写在 handler 里，不依赖中间件是否挂载」）⇒⇒⇒ ⇒ 这层是**防御性**的，
    #   不是判据要求的 ⇒ 如实登记。
    def m11():
        b.mutate(ASSISTANT, M11_OLD, '', 'M11')
    b.case('N3 负控：删掉 stt handler 里的**冗余**入站上限（全局中间件已兜住）⇒ 门必须保持绿',
           False,
           must_not_contain=['TestS952NoUnboundedReadsExceptLedger'],
           mutate_fn=m11)

    # ── M12 必须红：台账条目过期（那处被修好了）────────────────────────
    # ⚠ §951 的教训：「改一本台账 ⇒ 别的批次的变异锚点会过期」——
    #   这一条是**反向**的：代码改了、**台账没改** ⇒ 门必须红。
    M12_OLD = '\tb, err := io.ReadAll(io.LimitReader(r, int64(maxLen)))\n'
    M12_NEW = '\tb, err := io.ReadAll(io.LimitReader(r, 200))\n'

    def m12():
        b.mutate(AGENT_HELPERS, M12_OLD, M12_NEW, 'M12')
    b.case('M12 台账里那处被修好了 ⇒ 台账条目过期 ⇒ 卡口必须红', True,
           must_contain=['TestS952LedgerIsNotStale'],
           mutate_fn=m12)

    # ── M13 必须红：台账里的调用点给了**非**正常量 ─────────────────────
    M13_OLD = '\t\tbody := readBody(resp.Body, 200)\n'
    M13_NEW = ('\t\ts952mutantCap := 200\n'
               '\t\tbody := readBody(resp.Body, s952mutantCap)\n')

    def m13():
        b.mutate(AGENT_HTTP, M13_OLD, M13_NEW, 'M13', count=3)
    b.case('M13 台账里的调用点给了非正常量 ⇒「上限由调用方给」不再成立 ⇒ 卡口必须红', True,
           must_contain=['TestS952LedgerCallersPassConstants'],
           mutate_fn=m13)

    # ── M14 必须红：rss 的 readLimited 改回「整个读进来」───────────────
    M14_OLD = '\tb, err := io.ReadAll(io.LimitReader(r, max+1))\n'
    M14_NEW = '\tb, err := io.ReadAll(r)\n'

    def m14():
        b.mutate(RSS_URL, M14_OLD, M14_NEW, 'M14')
    b.case('M14 rss 的 readLimited 改回 `io.ReadAll(r)` ⇒ 它的上限没了 ⇒ 台账的**上限形态**必须红', True,
           must_contain=['TestS952LedgerCapFormIsPinned'],
           mutate_fn=m14)

    # ── M15 必须红：**量具**不认 `sizedread.Limit` 形态 ⇒ 自证臂红 ─────
    M15_OLD = 'if id, ok := sel.X.(*ast.Ident); ok && id.Name == "sizedread" && sel.Sel.Name == "Limit" && len(e.Args) == 2 {'
    M15_NEW = 'if id, ok := sel.X.(*ast.Ident); ok && id.Name == "sizedreadXXX" && sel.Sel.Name == "Limit" && len(e.Args) == 2 {'

    def m15():
        b.mutate(GATE, M15_OLD, M15_NEW, 'M15')
    b.case('M15 量具不认 `sizedread.Limit` 形态 ⇒ 整族漏判 ⇒ 自证臂必须红', True,
           must_contain=['TestS952SelfVerification'],
           mutate_fn=m15)

    # ── M16 必须红：**量具**不认 `http.MaxBytesReader` 绑定 ─────────────
    # ⚠ 必须打的是**绑定解析**那处（s952ResolveBindings），不是表达式解析那处
    #   —— 第一版打错了地方 ⇒ 门**照样绿**（那一族仍被绑定解析认出来）
    #   ⇒ ⇒⇒ ⇒ ★ **变异必须打在「真的决定判定结果」的那一处**。
    M16_OLD = 'case s952IsPkgSel(sel, "http", "MaxBytesReader") && len(call.Args) == 3:'
    M16_NEW = 'case s952IsPkgSel(sel, "http", "MaxBytesReaderXXX") && len(call.Args) == 3:'

    def m16():
        b.mutate(GATE, M16_OLD, M16_NEW, 'M16')
    b.case('M16 量具不认 `http.MaxBytesReader` 绑定 ⇒ 整族漏判 ⇒ 自证臂必须红', True,
           must_contain=['TestS952SelfVerification'],
           mutate_fn=m16)

    # ── M17 必须红：**量具**把 `io.Discard` 当成内存 sink ───────────────
    # ⚠ 必须**同时**打两处：文本判定（`io.Discard`）与类型清单
    #   —— 只打一处时，另一处仍然会说「不是内存目标」⇒ 门照样绿。
    M17_OLD = '\tif s947ExprText(call.Args[0]) == "io.Discard" {\n\t\treturn false\n\t}\n'
    M17_NEW = '\tif false {\n\t\treturn false\n\t}\n'
    M17B_OLD = 'case "*bytes.Buffer", "*bytes.Reader", "*strings.Builder":'
    M17B_NEW = 'case "*bytes.Buffer", "*bytes.Reader", "*strings.Builder", "io.Writer":'

    def m17():
        b.mutate(GATE, M17_OLD, M17_NEW, 'M17a')
        b.mutate(GATE, M17B_OLD, M17B_NEW, 'M17b')
    b.case('M17 量具把 `io.Discard` 当成内存 sink ⇒ 误判 ⇒ 自证臂必须红', True,
           must_contain=['TestS952SelfVerification'],
           mutate_fn=m17)

    # ── M18 必须红：**量具**不认「入站 body 在读取点被限流」─────────────
    # ⚠ 第一版打的是「不认读取点的限流」⇒ 那是**变严** ⇒ 坏③ 臂照样绿
    #   ⇒ ⇒⇒ ⇒ 真正该打的是「入站 body **一律**有界」（**变松**）
    #   ⇒ ⇒⇒ ⇒ ★ **变异要么让判据变松（漏判），要么让判据变严（误判）**；
    #   两种都要能被抓到 ⇒ ⇒⇒ ⇒ 观察口要选对。
    M18_OLD = '\treturn strings.HasPrefix(c.rel, "backend/internal/server/")\n'
    M18_NEW = '\treturn true\n'

    def m18():
        b.mutate(GATE, M18_OLD, M18_NEW, 'M18')
    b.case('M18 量具不认「入站 body 在读取点被限流」⇒ 整族漏判 ⇒ 自证臂必须红', True,
           must_contain=['TestS952SelfVerification'],
           mutate_fn=m18)

    # ── M19 必须红：**量具**的「上限是正常量」检查恒真 ─────────────────
    # ⚠ 不能写成 `return true`（那会让 `i` 变成**未使用** ⇒ 编译失败
    #   ⇒ 门是「编译错」红，不是「判据」红 ⇒ §951 的教训）。
    #   ⇒ 改成 `>= 0`：上限 0 也算数 ⇒ 坏④ 臂（上限写成 0）必须红。
    M19_OLD = '\treturn i > 0\n'
    M19_NEW = '\treturn i >= 0\n'

    def m19():
        b.mutate(GATE, M19_OLD, M19_NEW, 'M19')
    b.case('M19 量具的「上限是正常量」检查恒真 ⇒ 上限写成 0 也会绿 ⇒ 自证臂必须红', True,
           must_contain=['TestS952SelfVerification'],
           mutate_fn=m19)

    # ── M20 必须红：**量具**的中间件检查瞎掉 ───────────────────────────
    M20_OLD = 'if ok && s952IsPkgSel(cs, "http", "MaxBytesReader") {'
    M20_NEW = 'if ok && s952IsPkgSel(cs, "http", "MaxBytesReaderXXX") {'

    def m20():
        b.mutate(GATE, M20_OLD, M20_NEW, 'M20')
    b.case('M20 量具的中间件检查瞎掉 ⇒ 全局兜底没了也不报 ⇒ 自证臂必须红', True,
           must_contain=['TestS952MiddlewareSelfVerification'],
           mutate_fn=m20)

    # ── M21 必须红：**量具**的「有界」判定恒真 ─────────────────────────
    M21_OLD = ('\t\tdefault:\n'
               '\t\t\tsite.Bounded = capOK && cap != ""\n'
               '\t\t}\n')
    M21_NEW = ('\t\tdefault:\n'
               '\t\t\tsite.Bounded = true\n'
               '\t\t}\n')

    def m21():
        b.mutate(GATE, M21_OLD, M21_NEW, 'M21')
    b.case('M21 量具的「有界」判定恒真 ⇒ 量具整体失明 ⇒ 绝对条数断言必须红', True,
           must_contain=['TestS952CensusCountsArePinned'],
           mutate_fn=m21)

    # ── N1 负控：换个**仍然是正常量**的上限 ⇒ 门必须保持绿 ─────────────
    N1_OLD = ('\tbody, err := sizedread.All(resp.Body, sizedread.MaxJSONBodyBytes)\n'
              '\tif err != nil {\n'
              '\t\treturn nil, fmt.Errorf("read session body failed: %w", err)\n'
              '\t}\n')
    N1_NEW = ('\tbody, err := sizedread.All(resp.Body, 16<<20)\n'
              '\tif err != nil {\n'
              '\t\treturn nil, fmt.Errorf("read session body failed: %w", err)\n'
              '\t}\n')

    def n1():
        b.mutate(OPENCODE, N1_OLD, N1_NEW, 'N1')
    b.case('N1 负控：把上限换成另一个正常量 ⇒ 门必须保持绿（不许误报）', False,
           must_not_contain=['TestS952NoUnboundedReadsExceptLedger'],
           mutate_fn=n1)

    # ── N2 负控：加一行注释 ⇒ 门必须保持绿 ─────────────────────────────
    N2_OLD = '\tbody, err := sizedread.All(resp.Body, sizedread.MaxJSONBodyBytes)\n'
    N2_NEW = ('\t// s952mutant 负控：只加注释，语义不变。\n'
              '\tbody, err := sizedread.All(resp.Body, sizedread.MaxJSONBodyBytes)\n')

    def n2():
        b.mutate(OPENCODE, N2_OLD, N2_NEW, 'N2', count=2)
    b.case('N2 负控：只加一行注释 ⇒ 门必须保持绿（不许误报）', False,
           must_not_contain=['TestS952NoUnboundedReadsExceptLedger'],
           mutate_fn=n2)

    # ── 收尾自证：还原态下三条判据都必须绿 ─────────────────────────────
    def final_judge():
        rc1, out1 = _run(
            ['go', 'test', './internal/repohygiene/', '-run', 'TestS952', '-count=1'],
            BACKEND)
        rc2, out2 = _run(['go', 'test', './internal/sizedread/', '-count=1'], BACKEND)
        rc3, out3 = _run(
            ['go', 'test', './internal/rss/', '-run', 'TestReadLimited|TestSetMaxBodyBytes',
             '-count=1'],
            BACKEND)
        return (rc1 or rc2 or rc3), out1 + '\n' + out2 + '\n' + out3
    b.case('收尾自证：还原态下卡口 + 收口自证 + 台账行为证明 全绿', False,
           must_not_contain=['FAIL'],
           runner=final_judge)


if __name__ == '__main__':
    run(b, main)

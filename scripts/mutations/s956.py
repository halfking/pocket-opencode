#!/usr/bin/env python3
"""§956：原生切设备 ⇒ 分片时间轴**回卷** ⇒ 会话时间轴必须由 JS 侧重基。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
  **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠ 本批次是本仓**第十五批「把量具自己打坏」的变异** ⇒ ⇒ 每次都必须证明：
  **量具坏了 ⇒ 门会红**。

判据命令（两个文件都要跑）：
  node --experimental-strip-types --test \\
    src/features/meetings/__tests__/recording-duration-consistency.test.ts （§956 组：纯函数 + 接线扫描）
    src/native/__tests__/background-mic-contract.test.mjs                  （§956 组：Java/桥契约）

⚠⚠ 本批次最该被记住的三条：

★ **M1 是「钳位」这条不变式本身**：去掉 `Math.max(rawStart, prev.lastEndMs)`
  ⇒ 回卷不再被拦住 ⇒ 相邻段重新变成**重叠**
  ⇒ ⇒ 去重的**精确裁剪档**（§634/§683 认定生产不可达、能吃掉正文的那一档）
  重新变得可达 ⇒ ⇒ ★ 通用式：**护栏要比/要算的数必须来自同一个坐标系**，
  而本模块存在的全部理由就是「输出恒不倒退」这一条不变式。

★ **M2 是「重基」与「透传」的区别**：把 epoch 的偏移去掉 ⇒ 换轮次后又从 0 开始
  ⇒ ⇒ 看起来「也单调」但**错了**（第二半场被排到第一半场之前）
  ⇒ ⇒ ★ 通用式：**「不倒退」不等于「对」**；判据必须同时量「单调」与「位置正确」。

★ **M4/M7 是「生产走的那一段」**：
  · M4 把 runtime 改回「直接用 part.startMs」⇒ 纯函数的判据**全绿**
    （它们量的是纯函数那一层）⇒ 只有**接线扫描**抓得到
  · M7 让 Java 传的 epoch 换成另一个钟 ⇒ 桥侧与纯函数**全绿** ⇒ 只有**契约**抓得到
  ⇒ ⇒ ★ 通用式：**判据量到的层 ≠ 生产跑的层 ⇒ 必须各有一条**。
"""

import subprocess
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    FRONTEND, Batch, run,
)

TARGETS = [
    # 被测的生产文件
    'frontend/src/features/meetings/native-part-timeline.ts',
    'frontend/src/native/recordingRuntime.ts',
    'frontend/src/native/background-mic.ts',
    'frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/plugins/MeetingRecordService.java',
    'frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/plugins/BackgroundMicPlugin.java',
    # 判据本身（允许「把量具自己打坏」的变异）
    'frontend/src/features/meetings/__tests__/recording-duration-consistency.test.ts',
    'frontend/src/native/__tests__/background-mic-contract.test.mjs',
]

JUDGES = [
    'src/features/meetings/__tests__/recording-duration-consistency.test.ts',
    'src/native/__tests__/background-mic-contract.test.mjs',
]


def judge_runner():
    """前端判据（§956 的纯函数组 + §956 的 Java/桥契约组）。"""
    def run_judges():
        for j in JUDGES:
            p = subprocess.run(
                ['node', '--experimental-strip-types', '--test', j],
                cwd=str(FRONTEND), capture_output=True, text=True, timeout=900)
            if p.returncode != 0:
                return p.returncode, p.stdout + p.stderr
        return 0, ''
    return run_judges


def typecheck_runner():
    """类型层判据（`vue-tsc`）—— 只给 M10 用。"""
    def run_typecheck():
        p = subprocess.run(
            ['npx', 'vue-tsc', '--noEmit'],
            cwd=str(FRONTEND), capture_output=True, text=True, timeout=1800)
        return p.returncode, p.stdout + p.stderr
    return run_typecheck


b = Batch('§956 原生切设备 ⇒ 分片时间轴回卷 ⇒ 会话时间轴重基',
          judge_runner(), TARGETS)

TIMELINE = b.targets[0]
RUNTIME = b.targets[1]
BRIDGE = b.targets[2]
SERVICE = b.targets[3]
PLUGIN = b.targets[4]
TESTFILE_DUR = b.targets[5]
TESTFILE_CONTRACT = b.targets[6]


def main():
    # ── M1 必须红：去掉钳位（时间轴可以倒退）────────────────────────────
    M1_OLD = '  const startMs = Math.max(rawStart, prev.lastEndMs)\n'
    M1_NEW = '  const startMs = rawStart\n'

    def m1():
        b.mutate(TIMELINE, M1_OLD, M1_NEW, 'M1')
    b.case('M1 去掉钳位 ⇒ 回卷不再被拦住 ⇒ 相邻段重新重叠 ⇒ 门必须红', True,
           must_contain=['倒退必须被钳位'],
           mutate_fn=m1)

    # ── M2 必须红：不按 epoch 重基（换轮次后又从 0 开始）────────────────
    M2_OLD = '      rawStart += offset\n      rawEnd += offset\n'
    M2_NEW = '      void offset\n'

    def m2():
        b.mutate(TIMELINE, M2_OLD, M2_NEW, 'M2')
    b.case('M2 不按 epoch 重基 ⇒ 换轮次后时间轴又从 0 开始 ⇒ 门必须红', True,
           must_contain=['换轮次必须精确重基'],
           mutate_fn=m2)

    # ── M3 必须红：退化不留痕（近似时间轴被当成精确的）──────────────────
    M3_OLD = '    degraded: prev.degraded || clamped,\n'
    M3_NEW = '    degraded: false,\n'

    def m3():
        b.mutate(TIMELINE, M3_OLD, M3_NEW, 'M3')
    b.case('M3 退化不留痕 ⇒ 近似时间轴被当成精确的 ⇒ 门必须红', True,
           must_contain=['近似时间轴必须留痕'],
           mutate_fn=m3)

    # ── M4 必须红：runtime 改回直接用原生的轮内相对量 ──────────────────
    #   ⚠⚠ 这条**纯函数判据全绿**（它们量的是纯函数那一层）⇒ 只有接线扫描抓得到
    M4_OLD = '            void this.processSegment(nativePartToBlob(part), t.startMs, t.endMs)\n'
    M4_NEW = '            void this.processSegment(nativePartToBlob(part), part.startMs, part.endMs)\n'

    def m4():
        b.mutate(RUNTIME, M4_OLD, M4_NEW, 'M4')
    b.case('M4 runtime 改回直接用 part.startMs ⇒ 回卷照旧 ⇒ 门必须红', True,
           must_contain=['必须用重基结果的时间'],
           mutate_fn=m4)

    # ── M5 必须红：切设备（resume）也重置时间轴 ────────────────────────
    M5_OLD = '    if (this.segmentAbort) this.segmentAbort.abort()\n'
    M5_NEW = ('    this.nativeTimeline = createNativePartTimelineState()\n'
              '    if (this.segmentAbort) this.segmentAbort.abort()\n')

    def m5():
        b.mutate(RUNTIME, M5_OLD, M5_NEW, 'M5')
    b.case('M5 切设备也重置时间轴 ⇒ 第二轮又从 0 开始 ⇒ 门必须红', True,
           must_contain=['时间轴只许在 freshSession 里重置一次'],
           mutate_fn=m5)

    # ── M6 必须红：Java 载荷丢掉 epoch ──────────────────────────────────
    M6_OLD = '    data.put("epochMs", epochMs);\n'
    M6_NEW = ''

    def m6():
        b.mutate(PLUGIN, M6_OLD, M6_NEW, 'M6')
    b.case('M6 Java 载荷丢掉 epoch ⇒ 消费侧拿不到原点 ⇒ 门必须红', True,
           must_contain=['emitPart 的载荷里没有 epochMs'],
           mutate_fn=m6)

    # ── M7 必须红：Java 传的 epoch 换成另一个钟 ─────────────────────────
    M7_OLD = '          BackgroundMicPlugin.emitPart(seq, wav, partStartRel, nowRel, captureStart);\n'
    M7_NEW = '          BackgroundMicPlugin.emitPart(seq, wav, partStartRel, nowRel, System.currentTimeMillis());\n'

    def m7():
        b.mutate(SERVICE, M7_OLD, M7_NEW, 'M7')
    b.case('M7 Java 传的 epoch 换成另一个钟 ⇒ 原点与相对量不同源 ⇒ 门必须红', True,
           must_contain=['emitPart 传的 epoch 必须就是算相对时间用的那个原点'],
           mutate_fn=m7)

    # ── M8 必须红：桥侧把缺失的 epoch 折成 0 ────────────────────────────
    M8_OLD = '    if (epochMs !== undefined) part.epochMs = epochMs\n'
    M8_NEW = '    part.epochMs = epochMs ?? 0\n'

    def m8():
        b.mutate(BRIDGE, M8_OLD, M8_NEW, 'M8')
    b.case('M8 桥侧把缺失的 epoch 折成 0 ⇒ 偏移是天文数字 ⇒ 门必须红', True,
           must_contain=['缺失的 epoch 不得被折成 0'],
           mutate_fn=m8)

    # ── M9 必须红：解析接受 0（0 = 1970 年）─────────────────────────────
    M9_OLD = '    return Number.isFinite(raw) && raw > 0 ? raw : undefined\n'
    M9_NEW = '    return Number.isFinite(raw) ? raw : undefined\n'

    def m9():
        b.mutate(TIMELINE, M9_OLD, M9_NEW, 'M9')
    b.case('M9 解析接受 epoch=0 ⇒ 下一片的偏移爆炸 ⇒ 门必须红', True,
           must_contain=['偏移爆炸了'],
           mutate_fn=m9)

    # ── M10 必须红（**类型层**）：载荷字段被删 ⇒ 编译器必须红 ───────────
    #   ⚠ 这条 node 测不到（node 不做类型检查）⇒ 必须由 `vue-tsc` 抓
    #   ⇒ 证明「载荷形状由类型层守着」这道防线**真的存在**。
    M10_OLD = '  epochMs?: number\n}\n'
    M10_NEW = '}\n'

    def m10():
        b.mutate(BRIDGE, M10_OLD, M10_NEW, 'M10')
    b.case('M10 删掉载荷里的 epochMs 字段 ⇒ **编译器**必须红（类型层真的有牙）', True,
           must_contain=['error TS'],
           mutate_fn=m10, runner=typecheck_runner())

    # ── N1 负控：只加一行注释 ⇒ 门必须保持绿（不许误报）────────────────
    N1_OLD = '  const startMs = Math.max(rawStart, prev.lastEndMs)\n'
    N1_NEW = '  const startMs = Math.max(rawStart, prev.lastEndMs)   // 负控：只加一行注释\n'

    def n1():
        b.mutate(TIMELINE, N1_OLD, N1_NEW, 'N1')
    b.case('N1 负控：只加一行注释 ⇒ 门必须保持绿（不许误报）', False,
           must_not_contain=['not ok'],
           mutate_fn=n1)

    # ── 收尾自证：还原态下判据必须绿 ───────────────────────────────────
    def final_judge():
        p = subprocess.run(
            ['node', '--experimental-strip-types', '--test', *JUDGES],
            cwd=str(FRONTEND), capture_output=True, text=True, timeout=900)
        return p.returncode, p.stdout + p.stderr
    b.case('收尾自证：还原态下判据必须绿', False,
           must_not_contain=['not ok'],
           runner=final_judge)

    # ── 收尾自证 2：还原态下类型层也必须绿 ──────────────────────────────
    b.case('收尾自证 2：还原态下 vue-tsc 必须通过（exit 0）', False,
           must_not_contain=['error TS'],
           runner=typecheck_runner())


if __name__ == '__main__':
    run(b, main)

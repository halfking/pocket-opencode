#!/usr/bin/env python3
"""§957：原生链切设备后，§805 时长护栏**恢复能判**，并把「切换空隙」与「真丢音频」分开。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
  **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠ 本批次是本仓**第十六批「把量具自己打坏」的变异** ⇒ ⇒ 每次都必须证明：
  **量具坏了 ⇒ 门会红**。

判据命令（两个文件都要跑）：
  node --experimental-strip-types --test \\
    src/features/meetings/__tests__/recording-duration-consistency.test.ts （§957 组：纯函数 + 接线扫描）
    src/native/__tests__/background-mic-contract.test.mjs                  （§957 组：Java 契约）

⚠⚠ 本批次最该被记住的三条：

★ **M1/M2 是「空隙」这件事的两个方向**：
  · M1 去掉「轮次边界」的判定 ⇒ 空隙恒 0 ⇒ 护栏把切换空隙当成丢音频（**误报**）
  · M2 把**所有**跳跃都算成空隙 ⇒ 同一轮里**丢了一片**也被当成空隙（**漏报**）
  ⇒ ⇒ ★ 通用式：**「空隙」与「丢片」必须分得开**；分错了任一方向，
    护栏要么天天误报、要么把真丢音频放过去 ⇒ **两种错法都致命**。

★ **M4 是「理由变了 ⇒ 规则也变了」**：§955 的规则（切设备 ⇒ 窗口不可信 ⇒ 判「量不到」）
  建立在「时间轴回卷」那个**已被 §956 消灭**的理由上 ⇒ 照抄旧规则 ⇒
  护栏在原生链跨设备时**永远瞎掉** ⇒ ⇒ ★ 通用式：**规则与它的理由必须一起复核**，
  理由被推翻后还照抄旧规则 ⇒ 那个规则就成了「没人再论证过的历史遗留」。

★ **M5/M9 是「生产走的那一段」**：
  · M5 把窗口改回用轮内相对量（`part.endMs`）⇒ 跨设备时窗口**回卷**
  · M9 删掉逐片的信任更新 ⇒ 时间轴不精确时也照样拿去比
  ⇒ ⇒ ★ 通用式：**纯函数判据只覆盖纯函数那一层**；接线（喂哪个值、
    什么时候更新）必须**各有一条**。
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
    'frontend/src/features/meetings/recording-duration-consistency.ts',
    'frontend/src/native/recordingRuntime.ts',
    'frontend/src/features/meetings/meeting-recording-finalize.ts',
    'frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/plugins/MeetingRecordService.java',
    # 判据本身（允许「把量具自己打坏」的变异）
    'frontend/src/features/meetings/__tests__/recording-duration-consistency.test.ts',
    'frontend/src/native/__tests__/background-mic-contract.test.mjs',
]

JUDGES = [
    'src/features/meetings/__tests__/recording-duration-consistency.test.ts',
    'src/native/__tests__/background-mic-contract.test.mjs',
]


def judge_runner():
    """前端判据（§957 的纯函数组 + 接线扫描 + §957 的 Java 契约组）。"""
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


b = Batch('§957 原生链切设备后护栏恢复能判（切换空隙 ≠ 丢音频）',
          judge_runner(), TARGETS)

TIMELINE = b.targets[0]
DURATION = b.targets[1]
RUNTIME = b.targets[2]
FINALIZE = b.targets[3]
SERVICE = b.targets[4]
TESTFILE_DUR = b.targets[5]
TESTFILE_CONTRACT = b.targets[6]


def main():
    # ── M1 必须红：不认轮次边界 ⇒ 空隙恒 0 ⇒ 切换空隙被当成丢音频 ────────
    M1_OLD = '  const roundChanged = hasEpoch && prev.lastEpochMs !== null && epoch !== prev.lastEpochMs\n'
    M1_NEW = '  const roundChanged = false\n'

    def m1():
        b.mutate(TIMELINE, M1_OLD, M1_NEW, 'M1')
    b.case('M1 不认轮次边界 ⇒ 空隙恒 0 ⇒ 切换空隙被误报成丢音频 ⇒ 门必须红', True,
           must_contain=['切换空隙必须是 2_500ms'],
           mutate_fn=m1)

    # ── M2 必须红：所有跳跃都算空隙 ⇒ 丢了一片也被放过去 ─────────────────
    M2_OLD = '  const gapMs = roundChanged ? jumpMs : 0\n'
    M2_NEW = '  const gapMs = jumpMs\n'

    def m2():
        b.mutate(TIMELINE, M2_OLD, M2_NEW, 'M2')
    b.case('M2 所有跳跃都算空隙 ⇒ 丢了一片被当成切换空隙放过去 ⇒ 门必须红', True,
           must_contain=['不许算成空隙'],
           mutate_fn=m2)

    # ── M3 必须红：护栏不扣空隙 ⇒ 跨设备必误报 ──────────────────────────
    M3_OLD = '    const wallClockMs = Math.max(0, native.windowMs - gapMs)\n'
    M3_NEW = '    const wallClockMs = native.windowMs\n'

    def m3():
        b.mutate(DURATION, M3_OLD, M3_NEW, 'M3')
    b.case('M3 护栏不扣切换空隙 ⇒ 跨设备录音必误报「音频比墙钟短」⇒ 门必须红', True,
           must_contain=['必须扣掉切换空隙'],
           mutate_fn=m3)

    # ── M4 必须红：信任规则退回 §955 的旧规则（切设备 ⇒ 永远判「量不到」）──
    M4_OLD = ('  if (!Number.isFinite(opts.runs) || opts.runs <= 1) return true\n'
              '  return opts.timelineExact === true\n')
    M4_NEW = '  return opts.runs === 1\n'

    def m4():
        b.mutate(DURATION, M4_OLD, M4_NEW, 'M4')
    b.case('M4 信任规则退回旧规则 ⇒ 跨设备时护栏永远瞎掉 ⇒ 门必须红', True,
           must_contain=['多轮 + 时间轴精确'],
           mutate_fn=m4)

    # ── M5 必须红：runtime 把窗口改回用轮内相对量（跨设备 ⇒ 窗口回卷）─────
    #   ⚠⚠ 这条**纯函数判据全绿**（它们量的是纯函数那一层）⇒ 只有接线扫描抓得到
    M5_OLD = '              this.nativeCaptureWindowMs, t.endMs,\n'
    M5_NEW = '              this.nativeCaptureWindowMs, part.endMs,\n'

    def m5():
        b.mutate(RUNTIME, M5_OLD, M5_NEW, 'M5')
    b.case('M5 runtime 把窗口改回用 part.endMs ⇒ 跨设备时窗口回卷 ⇒ 门必须红', True,
           must_contain=['不能用轮内相对量'],
           mutate_fn=m5)

    # ── M6 必须红：新一轮开始时把窗口清零（会话坐标被截断）──────────────
    M6_OLD = ('          })\n'
              '          this.selectedInput.value = this.inputs.value.find((i) => i.deviceId === opts?.deviceId)\n')
    M6_NEW = ('          })\n'
              '          this.nativeCaptureWindowMs = 0\n'
              '          this.selectedInput.value = this.inputs.value.find((i) => i.deviceId === opts?.deviceId)\n')

    def m6():
        b.mutate(RUNTIME, M6_OLD, M6_NEW, 'M6')
    b.case('M6 新一轮开始时清零窗口 ⇒ 窗口只剩最新一轮的跨度 ⇒ 门必须红', True,
           must_contain=['窗口只许在 freshSession 里清零一次'],
           mutate_fn=m6)

    # ── M7 必须红：快照不交出空隙 ⇒ 护栏扣不到、也看不见 ─────────────────
    M7_OLD = 'gapMs: this.nativeTimeline.gapMs'
    M7_NEW = 'gapMs: 0'

    def m7():
        b.mutate(RUNTIME, M7_OLD, M7_NEW, 'M7')
    b.case('M7 快照不交出空隙 ⇒ 护栏扣不到切换空隙 ⇒ 门必须红', True,
           must_contain=['快照没有把空隙交出去'],
           mutate_fn=m7)

    # ── M8 必须红：「每片都带 epoch」不再粘性 ⇒ 分不清空隙与丢片 ────────
    M8_OLD = '  const allEpochsKnown = prev.allEpochsKnown && hasEpoch\n'
    M8_NEW = '  const allEpochsKnown = hasEpoch\n'

    def m8():
        b.mutate(TIMELINE, M8_OLD, M8_NEW, 'M8')
    b.case('M8 「每片都带 epoch」不再粘性 ⇒ 时间轴被误判成精确 ⇒ 门必须红', True,
           must_contain=['有一片没带 epoch'],
           mutate_fn=m8)

    # ── M9 必须红：不逐片更新信任（时间轴不精确时也照样拿去比）──────────
    M9_OLD = ('            this.nativeCaptureTrusted = nativeCaptureWindowTrustable({\n'
              '              runs: this.nativeCaptureRuns,\n'
              '              timelineExact: isNativeTimelineExact(this.nativeTimeline),\n'
              '            })\n'
              '            void this.processSegment(nativePartToBlob(part), t.startMs, t.endMs)\n')
    M9_NEW = '            void this.processSegment(nativePartToBlob(part), t.startMs, t.endMs)\n'

    def m9():
        b.mutate(RUNTIME, M9_OLD, M9_NEW, 'M9')
    b.case('M9 不逐片更新信任 ⇒ 时间轴不精确时也拿去比 ⇒ 门必须红', True,
           must_contain=['又成了没有行为门的决策'],
           mutate_fn=m9)

    # ── M10 必须红（**类型层**）：快照的 gapMs 字段被删 ⇒ 编译器必须红 ──
    #   ⚠ 这条 node 测不到（node 不做类型检查）⇒ 必须由 `vue-tsc` 抓
    #   ⇒ 证明「快照形状由类型层守着」这道防线**真的存在**。
    M10_OLD = '  gapMs: number | null\n}\n'
    M10_NEW = '}\n'

    def m10():
        b.mutate(DURATION, M10_OLD, M10_NEW, 'M10')
    b.case('M10 删掉快照里的 gapMs 字段 ⇒ **编译器**必须红（类型层真的有牙）', True,
           must_contain=['error TS'],
           mutate_fn=m10, runner=typecheck_runner())

    # ── M11 必须红：空隙的文案不说「不算音频丢失」（说错话也是错）────────
    M11_OLD = "+ '（这段时间本来就没在采音，不算音频丢失；已从时长一致性比对里扣除）'\n"
    M11_NEW = "+ '（这段时间本来就没在采音）'\n"

    def m11():
        b.mutate(DURATION, M11_OLD, M11_NEW, 'M11')
    b.case('M11 空隙文案不说「不算音频丢失」⇒ 用户被误导 ⇒ 门必须红', True,
           must_contain=['不算音频丢失'],
           mutate_fn=m11)

    # ── M12 必须红：收尾链不把空隙说出来（用户少的那一截被静默吞掉）─────
    M12_OLD = ('  const gapNotice = describeCoverageGap(guardInput.coverageGapMs)\n'
               '  if (gapNotice) console.warn(`[meeting-finalize] ${gapNotice}`)\n')
    M12_NEW = ''

    def m12():
        b.mutate(FINALIZE, M12_OLD, M12_NEW, 'M12')
    b.case('M12 收尾链不把切换空隙说出来 ⇒ 用户少的那一截被静默吞掉 ⇒ 门必须红', True,
           must_contain=['收尾链没有把切换空隙说出来'],
           mutate_fn=m12)

    # ── M13 必须红（**Java 契约**）：原生侧不再把 partStartRel 推进到 nowRel
    #   ⇒ 同一轮里的片不再连续 ⇒ 「丢片」与「空隙」再也分不开。
    M13_OLD = '          partStartRel = nowRel;\n'
    M13_NEW = '          partStartRel = nowRel + 100;\n'

    def m13():
        b.mutate(SERVICE, M13_OLD, M13_NEW, 'M13')
    b.case('M13 Java 不再把 partStartRel 推进到 nowRel ⇒ 片间留缝 ⇒ 门必须红', True,
           must_contain=['发片之后没有把 partStartRel 推进到 nowRel'],
           mutate_fn=m13)

    # ── N1 负控：只加一行注释 ⇒ 门必须保持绿（不许误报）────────────────
    N1_OLD = '  const gapMs = roundChanged ? jumpMs : 0\n'
    N1_NEW = '  const gapMs = roundChanged ? jumpMs : 0   // 负控：只加一行注释\n'

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

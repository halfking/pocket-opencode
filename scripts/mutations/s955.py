#!/usr/bin/env python3
"""§955：原生链的 §805 时长护栏**真的**接上了（且用**同源**的墙钟）。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
  **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠ 本批次是本仓**第十四批「把量具自己打坏」的变异** ⇒ ⇒ 每次都必须证明：
  **量具坏了 ⇒ 门会红**。

判据命令（两个文件都要跑）：
  node --experimental-strip-types --test \\
    src/features/meetings/meeting-final-transcript.test.ts            （§955 的 K 组）
    src/features/meetings/__tests__/recording-duration-consistency.test.ts （§955 组）

⚠⚠ 本批次最该被记住的三条：

★ **M1 就是 §954 留下的那个假账本身**：生产者不产 `durationSec`
  ⇒ 消费侧读到 `undefined` ⇒ `measuredAudioSec` 恒不产出
  ⇒ 护栏**仍然**恒判 `no-ground-truth`
  ⇒ ⇒⇒ ⇒ ★ 通用式：**判据量到的必须是真件**。
    注入式判据（假件）只覆盖**消费侧**；**生产侧的接线必须单独量**，
    否则「假件比真件完整」这类缺陷**永远查不出来**
    （§954 的 J-12 用的就是假件 ⇒ 假件带 `durationSec`、真件不带 ⇒ 全绿）。

★ **M9 是「类型层也有牙」**：把真件的 yield 里的 `durationSec` **删掉**
  ⇒ 运行时判据会红（M1 那一路），而**编译器**也必须红
  ⇒ 本批次特意为它跑一次 `vue-tsc` ⇒ 证明「生产者的类型比消费者更严格」
  这道防线**真的存在**，而不只是一句注释。
  ⇒ ⚠ 这类变异**node 测不到**（node 不做类型检查）⇒ 只能由 typecheck 抓
  ⇒ ⇒ ★ 通用式：**「类型收紧」这条防线要单独验一次**，
    否则它可能只是一段让人安心的注释。

★ **M4 是「宁可瞎也不误报」那条**：窗口不可信时退回 JS 墙钟
  ⇒ 差的是**系统偏移**而不是「音频丢了多少」⇒ 短录音会假警报
  ⇒ ⇒ 通用式：**护栏要比的两个数必须同源**；不同源时宁可不判。
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
    'frontend/src/features/meetings/native-audio-parts.ts',
    'frontend/src/features/meetings/audio-parts.ts',
    'frontend/src/features/meetings/meeting-final-transcript.ts',
    'frontend/src/features/meetings/meeting-recording-finalize.ts',
    'frontend/src/features/meetings/recording-duration-consistency.ts',
    'frontend/src/features/sessions/useSessionLiveRecord.ts',
    'frontend/src/native/recordingRuntime.ts',
    # 判据本身（允许「把量具自己打坏」的变异）
    'frontend/src/features/meetings/meeting-final-transcript.test.ts',
    'frontend/src/features/meetings/__tests__/recording-duration-consistency.test.ts',
]

JUDGES = [
    'src/features/meetings/meeting-final-transcript.test.ts',
    'src/features/meetings/__tests__/recording-duration-consistency.test.ts',
]


def judge_runner():
    """前端判据（§955 的 K 组 + §955 组）—— 批次的**默认**运行器。"""
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
    """类型层判据（`vue-tsc`）—— 只给 M9 用。"""
    def run_typecheck():
        p = subprocess.run(
            ['npx', 'vue-tsc', '--noEmit'],
            cwd=str(FRONTEND), capture_output=True, text=True, timeout=1800)
        return p.returncode, p.stdout + p.stderr
    return run_typecheck


b = Batch('§955 原生链的 §805 时长护栏真的接上了（且用同源的墙钟）',
          judge_runner(), TARGETS)

PLANNER = b.targets[0]
PARTS = b.targets[1]
FINAL_TXT = b.targets[2]
FINALIZE = b.targets[3]
DURATION = b.targets[4]
SESSION = b.targets[5]
RUNTIME = b.targets[6]
TESTFILE = b.targets[7]
TESTFILE_DUR = b.targets[8]


def main():
    # ── M1 必须红：真件不产 durationSec（= §954 那个 bug 本身）──────────
    M1_OLD = '      durationSec: chunk.durationSec,\n'
    M1_NEW = '      durationSec: 0 as unknown as number,\n'

    def m1():
        b.mutate(PLANNER, M1_OLD, M1_NEW, 'M1')
    b.case('M1 真件不产 durationSec ⇒ 护栏在原生链上仍然是死的 ⇒ 门必须红', True,
           must_contain=['真件产出的每一块都必须带 durationSec'],
           mutate_fn=m1)

    # ── M2 必须红：消费侧不累计时长（量到了也不报）──────────────────────
    M2_OLD = '    if (Number.isFinite(chunk.durationSec) && chunk.durationSec > 0) {\n'
    M2_NEW = '    if (false && Number.isFinite(chunk.durationSec) && chunk.durationSec > 0) {\n'

    def m2():
        b.mutate(FINAL_TXT, M2_OLD, M2_NEW, 'M2')
    b.case('M2 消费侧不累计时长 ⇒ 整场真实时长量不到 ⇒ 门必须红', True,
           must_contain=['真件接进整场重转'],
           mutate_fn=m2)

    # ── M3 必须红：原生链仍拿 JS 墙钟比（不同源 ⇒ 系统偏移）────────────
    M3_OLD = "  if (typeof native.windowMs === 'number' && Number.isFinite(native.windowMs) && native.windowMs > 0) {\n"
    M3_NEW = "  if (typeof native.windowMs === 'number' && Number.isFinite(native.windowMs) && native.windowMs > 0 && false) {\n"

    def m3():
        b.mutate(DURATION, M3_OLD, M3_NEW, 'M3')
    b.case('M3 原生链仍拿 JS 墙钟比 ⇒ 比的是系统偏移 ⇒ 门必须红', True,
           must_contain=['墙钟必须换成**原生采集窗口**'],
           mutate_fn=m3)

    # ── M4 必须红：窗口不可信时退回 JS 墙钟（假警报路径）────────────────
    M4_OLD = "  return { wallClockMs: 0, audioSec, source: 'native-untrusted' }\n"
    M4_NEW = "  return { wallClockMs: opts.elapsedMs, audioSec, source: 'native-untrusted' }\n"

    def m4():
        b.mutate(DURATION, M4_OLD, M4_NEW, 'M4')
    b.case('M4 窗口不可信时退回 JS 墙钟 ⇒ 短录音会假警报 ⇒ 门必须红', True,
           must_contain=['不许**拿 JS 墙钟顶替'],
           mutate_fn=m4)

    # ── M5 必须红：收尾不把原生窗口接进护栏（通道掐断）─────────────────
    M5_OLD = '    nativeCapture: readNativeCaptureSnapshot(deps.nativeCaptureSnapshot),\n'
    M5_NEW = '    nativeCapture: null,\n'

    def m5():
        b.mutate(FINALIZE, M5_OLD, M5_NEW, 'M5')
    b.case('M5 收尾不读那条通道 ⇒ 护栏拿不到同源墙钟 ⇒ 门必须红', True,
           must_contain=['收尾声明了通道却没读它'],
           mutate_fn=m5)

    # ── M6 必须红：窗口累积规则被破坏（回卷时变小）──────────────────────
    M6_OLD = '  return partEndMs > currentMs ? partEndMs : currentMs\n'
    M6_NEW = '  return partEndMs\n'

    def m6():
        b.mutate(DURATION, M6_OLD, M6_NEW, 'M6')
    b.case('M6 窗口在回卷时变小 ⇒ 墙钟倒退 ⇒ 门必须红', True,
           must_contain=['乱序/回卷的读数不得让窗口变小'],
           mutate_fn=m6)

    # ── M7 必须红：两条路径之一不传（把通道掐断的写法）──────────────────
    M7_OLD = '        nativeCaptureSnapshot: () => recorder.nativeCaptureSnapshot(),\n'
    M7_NEW = '        nativeCaptureSnapshot: undefined as never,\n'

    def m7():
        b.mutate(SESSION, M7_OLD, M7_NEW, 'M7')
    b.case('M7 随手记路径把窗口通道掐断 ⇒ 那个入口的护栏是死的 ⇒ 门必须红', True,
           must_contain=['没把原生采集窗口接上去'],
           mutate_fn=m7)

    # ── M8 必须红（**类型层**）：适配层又手写了一份形状（回到 §954 的病根）──
    #   ⚠⚠ 这条第一版是**绿的** —— 而它绿得有道理：
    #   行为判据量的是**纯函数**那一层（生产入口住在 import 了 Filesystem/local-db
    #   的模块里 ⇒ node 跑不到）⇒ 适配层被手写了形状，判据**看不见**。
    #   ⇒ ⇒ 这条被**类型层**抓住（`NativeAudioChunkSource.durationSec` 必填）
    #   ⇒ ⇒ 所以它的运行器必须是 `vue-tsc`，不是 node 测试。
    M8_OLD = """  yield* streamNativeAudioChunkSources(
    files,
    (seq, mimeType) => readAudioPartBytes(meetingId, seq, mimeType),
    { ...opts, label: `meeting=${meetingId}` },
  )
"""
    M8_NEW = """  yield* (async function* () {
    for await (const c of streamNativeAudioChunkSources(
      files,
      (seq, mimeType) => readAudioPartBytes(meetingId, seq, mimeType),
      { ...opts, label: `meeting=${meetingId}` },
    )) {
      yield { blob: c.blob, offsetSec: c.offsetSec }
    }
  })()
"""

    def m8():
        b.mutate(PARTS, M8_OLD, M8_NEW, 'M8')
    b.case('M8 适配层又手写形状（丢掉 durationSec）⇒ **编译器**必须红（类型层兜底）', True,
           must_contain=['durationSec'],
           mutate_fn=m8, runner=typecheck_runner())

    # ── M10 必须红（**只有扫描能抓**）：适配层手写一个**类型合法**的形状 ──
    #   ⚠⚠ 这一条是 M8 的**同族**、但打的是**另一层**：
    #   M8 手写的形状**类型不合法** ⇒ 编译器能抓；
    #   M10 手写的形状**类型合法**（`durationSec: 0`）⇒ **编译器抓不到**
    #   ⇒ ⇒ 只剩「形状只写一处」那条**结构不变式**能抓
    #   ⇒ ⇒ ★ 通用式：**类型层能兜住的缺陷，扫描层就别重复守**；
    #   ⇒   **类型层兜不住的**（值错了、语义错了）⇒ 必须另有一条**行为/结构**判据。
    M10_OLD = """  yield* streamNativeAudioChunkSources(
    files,
    (seq, mimeType) => readAudioPartBytes(meetingId, seq, mimeType),
    { ...opts, label: `meeting=${meetingId}` },
  )
"""
    M10_NEW = """  yield* (async function* () {
    for await (const c of streamNativeAudioChunkSources(
      files,
      (seq, mimeType) => readAudioPartBytes(meetingId, seq, mimeType),
      { ...opts, label: `meeting=${meetingId}` },
    )) {
      yield { blob: c.blob, offsetSec: c.offsetSec, durationSec: 0 }
    }
  })()
"""

    def m10():
        b.mutate(PARTS, M10_OLD, M10_NEW, 'M10')
    b.case('M10 适配层手写**类型合法**的形状 ⇒ 编译器抓不到 ⇒ 只有结构不变式能抓 ⇒ 门必须红', True,
           must_contain=['适配层必须**直接转发**'],
           mutate_fn=m10)

    # ── M9 必须红（**类型层**）：删掉真件 yield 里的 durationSec ─────────
    #   ⚠ 这条 node 测不到（node 不做类型检查）⇒ 必须由 `vue-tsc` 抓
    #   ⇒ 证明「生产者的类型比消费者更严格」这道防线**真的存在**。
    M9_OLD = '      durationSec: chunk.durationSec,\n'
    M9_NEW = ''

    def m9():
        b.mutate(PLANNER, M9_OLD, M9_NEW, 'M9')
    b.case('M9 删掉真件 yield 里的 durationSec ⇒ **编译器**必须红（类型收紧真的有牙）', True,
           must_contain=['durationSec'],
           mutate_fn=m9, runner=typecheck_runner())

    # ── N1 负控：只加一行注释 ⇒ 门必须保持绿（不许误报）────────────────
    N1_OLD = '  for await (const chunk of streamNativeWavChunks(parts(), chunkOpts)) {\n'
    N1_NEW = '  for await (const chunk of streamNativeWavChunks(parts(), chunkOpts)) {   // 负控：只加一行注释\n'

    def n1():
        b.mutate(PLANNER, N1_OLD, N1_NEW, 'N1')
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

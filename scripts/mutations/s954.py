#!/usr/bin/env python3
"""§954：原生链（Android）的「整场重转」从**盘上已有的分片**流式供给。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
  **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠ 本批次是本仓**第十批「把量具自己打坏」的变异** ⇒ ⇒ 每次都必须证明：
  **量具坏了 ⇒ 门会红**。

判据命令（两个文件都要跑）：
  node --experimental-strip-types --test \\
    src/features/meetings/meeting-final-transcript.test.ts   （§954 的 J 组）
    src/native/__tests__/meeting-audio-native-mode.test.ts    （§826 的文案门）

⚠ 本批次最该被记住的两条：

★ **M1 就是施工时真踩到的那个 bug**：切块阈值只按「新收的秒数」算
  ⇒ carry（上一轮切点之后剩下的那一截）被**逐轮累加**
  ⇒ 切出来的块越来越长 ⇒ 收尾那块涨到 **764 s**
  ⇒ base64 后 **32.6 MB** ⇒ **超服务端 25 MiB 的请求体闸** ⇒ 那一块必然被拒。
  ⇒ ⇒⇒ ⇒ 这条变异证明「块长与会议时长无关」那条判据**真的有牙**。
  ⇒ ⇒⇒ ⇒ ★ 通用式：**带 carry 的流式切块，阈值必须按「组的长度」算，
    而不是按「这一轮新收的量」算** —— 后者会让 carry 单调增长。

★ **M2 是「收尾那一截不许整块吐出去」**：不切 ⇒ 一块 764 s ⇒ 与 M1 同一个闸。
  ⇒ 两条变异打的是**同一个后果的两个成因** ⇒ 说明那条后果有**两处**依赖。

★ **M3/M4 打的是「信号变死代码」**：§818 的教训 —— 判据钉在源码形状上时，
  「把整条链掐断」是变异最常见的形态。M3 掐**接线**（调用点不传），
  M4 掐**分支**（分块源存在但分支不走）⇒ 两处都要有门。
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
    # 被测的四个生产文件
    'frontend/src/features/meetings/native-audio-parts.ts',
    'frontend/src/features/meetings/audio-parts.ts',
    'frontend/src/features/meetings/meeting-final-transcript.ts',
    'frontend/src/features/meetings/meeting-recording-finalize.ts',
    # 判据本身（允许「把量具自己打坏」的变异）
    'frontend/src/features/meetings/meeting-final-transcript.test.ts',
    'frontend/src/native/__tests__/meeting-audio-native-mode.test.ts',
]

JUDGES = [
    'src/features/meetings/meeting-final-transcript.test.ts',
    'src/native/__tests__/meeting-audio-native-mode.test.ts',
]


def judge_runner():
    """前端判据（§954 的 J 组 + §826 的文案门）—— 批次的**默认**运行器。"""
    def run_judges():
        for j in JUDGES:
            p = subprocess.run(
                ['node', '--experimental-strip-types', '--test', j],
                cwd=str(FRONTEND), capture_output=True, text=True, timeout=900)
            if p.returncode != 0:
                return p.returncode, p.stdout + p.stderr
        return 0, ''
    return run_judges


b = Batch('§954 原生链：整场重转从盘上的分片流式供给（峰值 O(块)）',
          judge_runner(), TARGETS)

PLANNER = b.targets[0]
PARTS = b.targets[1]
FINAL_TXT = b.targets[2]
FINALIZE = b.targets[3]
TESTFILE = b.targets[4]
TESTFILE826 = b.targets[5]


def main():
    # ── M1 必须红：阈值只按「新收的秒数」算 ⇒ carry 逐轮累加 ────────────
    M1_OLD = '    if (this.groupTotalSeconds() < this.groupTargetSeconds) return []\n'
    M1_NEW = '    if (this.groupSeconds < this.groupTargetSeconds) return []\n'

    def m1():
        b.mutate(PLANNER, M1_OLD, M1_NEW, 'M1')
    b.case('M1 切块阈值只按「新收的秒数」算 ⇒ carry 逐轮累加 ⇒ 块长随时长变 ⇒ 门必须红', True,
           must_contain=['块长必须与会议时长无关'],
           mutate_fn=m1)

    # ── M2 必须红：收尾那一截不切（整块吐出去 ⇒ 超 25 MiB 请求体闸）────
    M2_OLD = '    const out = this.cutGroup(true)\n'
    M2_NEW = '    const out = this.cutGroup(false)\n'

    def m2():
        b.mutate(PLANNER, M2_OLD, M2_NEW, 'M2')
    b.case('M2 收尾那一截不切就整块吐出去 ⇒ 末块 764 s ⇒ base64 32.6 MB ⇒ 门必须红', True,
           must_contain=['一秒都不许丢'],
           mutate_fn=m2)

    # ── M3 必须红：调用点不传分块源 ⇒ 整条链是死代码 ───────────────────
    M3_OLD = '    { toWav, loadAudioChunks: deps.loadAudioChunks ?? defaultLoadAudioChunks },\n'
    M3_NEW = '    { toWav, loadAudioChunks: undefined },\n'

    def m3():
        b.mutate(FINALIZE, M3_OLD, M3_NEW, 'M3')
    b.case('M3 收尾不把分块源传进去 ⇒ §954 整条链变成死代码 ⇒ 门必须红', True,
           must_contain=['收尾必须把分块源传进 refetchFullTranscript'],
           mutate_fn=m3)

    # ── M4 必须红：分块分支存在但走不到（被 loadAudio 抢先）─────────────
    M4_OLD = '  const chunkFactory = opts?.loadAudioChunks\n  if (chunkFactory) {\n'
    M4_NEW = '  const chunkFactory = opts?.loadAudioChunks\n  if (chunkFactory && opts?.loadAudio) {\n'

    def m4():
        b.mutate(FINAL_TXT, M4_OLD, M4_NEW, 'M4')
    b.case('M4 分块分支被 loadAudio 抢先 ⇒ 原生链上永远走不到 ⇒ 门必须红', True,
           must_contain=['给了分块源就**不许再去取整场音频**'],
           mutate_fn=m4)

    # ── M5 必须红：读不出/不是 WAV 的一片被静默跳过 ⇒ 时间轴错位 ────────
    M5_OLD = "      throw new Error('原生音频分片不是可解析的 16-bit PCM WAV')\n"
    M5_NEW = '      return []\n'

    def m5():
        b.mutate(PLANNER, M5_OLD, M5_NEW, 'M5')
    b.case('M5 读不出来的一片被静默跳过 ⇒ 那一段变成洞 ⇒ 时间轴错位 ⇒ 门必须红', True,
           must_contain=['读不到 / 不是 WAV 的一片必须**整条中止**'],
           mutate_fn=m5)

    # ── M6 必须红：布局一致性不再校验 ⇒ 拼出来的 WAV 是坏的 ────────────
    M6_OLD = '    if (this.layout) {\n'
    M6_NEW = '    if (false && this.layout) {\n'

    def m6():
        b.mutate(PLANNER, M6_OLD, M6_NEW, 'M6')
    b.case('M6 分片布局不一致不再中止 ⇒ 拼出来的 WAV 采样率错乱 ⇒ 门必须红', True,
           must_contain=['布局不一致的一片必须中止'],
           mutate_fn=m6)

    # ── M7 必须红：清单又变成 SELECT * ⇒ 整场 base64 一次性进内存 ──────
    M7_OLD = "    'SELECT seq, mime_type, file_path FROM local_meeting_audio_parts WHERE meeting_id = ? ORDER BY seq',\n"
    M7_NEW = "    'SELECT * FROM local_meeting_audio_parts WHERE meeting_id = ? ORDER BY seq',\n"

    def m7():
        b.mutate(PARTS, M7_OLD, M7_NEW, 'M7')
    b.case('M7 分片清单又整列读音频 ⇒ 峰值回到 O(时长) ⇒ 门必须红', True,
           must_contain=['分片清单**不许整列读音频**'],
           mutate_fn=m7)

    # ── M8 必须红：落盘扩展名又写死 .webm（内容是 WAV）──────────────────
    M8_OLD = '      const path = audioPartRelativePath(meetingId, seq, audioPartExtForMime(mimeType))\n'
    M8_NEW = "      const path = audioPartRelativePath(meetingId, seq, 'webm')\n"

    def m8():
        b.mutate(PARTS, M8_OLD, M8_NEW, 'M8')
    b.case('M8 落盘扩展名又写死 .webm ⇒ 文件名与内容不符 ⇒ 门必须红', True,
           must_contain=['落盘路径不许再写死 .webm'],
           mutate_fn=m8)

    # ── N1 负控：只加一行注释 ⇒ 门必须保持绿（不许误报）────────────────
    N1_OLD = '  private readonly opts: ResolvedOptions\n'
    N1_NEW = '  private readonly opts: ResolvedOptions   // 负控：只加一行注释\n'

    def n1():
        b.mutate(PLANNER, N1_OLD, N1_NEW, 'N1', count=1)
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


if __name__ == '__main__':
    run(b, main)

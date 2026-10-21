#!/usr/bin/env python3
"""§953：webm 的解码从「整场 / 累计」换成「**逐簇**」。

⚠ 本批次**必须落盘**（§935 的教训）：变异脚本不落盘 ⇒ 「当时绿 = 现在绿」
  **永久不可回溯** ⇒ ⇒ 本仓规定：**变异脚本要落盘**。

⚠ 本批次是本仓**第九批「把量具自己打坏」的变异** ⇒ ⇒ 每次都必须证明：
  **量具坏了 ⇒ 门会红**。

判据命令：
  node --experimental-strip-types --test \\
    src/native/__tests__/recording-audio-transcode.test.mjs     （前端判据）

⚠ 本批次最该被记住的三条：

★ **M1 是「回到旧的 workaround」**：把切分器改成「每次返回整块缓冲」
  ⇒ 解码器每次都解整场 ⇒ 喂给解码器的字节从 O(时长) 回到 O(时长²)
  ⇒ ⇒⇒ ⇒ 这条**必须**让「喂给解码器的字节是 O(整场)」那条红。
  ⭐ 而那条判据**只有在「一片一片推」时才有牙** —— 一次性推完再取一次，
  「解一次整场」和「逐簇解」喂进去的字节是同一量级 ⇒ ⇒ 判据的形状本身
  决定了它能不能抓住这条变异（§953 施工时真的踩到了这个坑）。

★ **M7 是「次序」**：解码失败时**先出队再解码** ⇒ 那一簇永久丢失
  ⇒ ⇒⇒ ⇒ 「先解成功再出队」这个次序是**行为**，源码上长得几乎一样
  ⇒ ⇒⇒ ⇒ 只能靠行为判据抓。

★ **M8 是「行为回归」**：把尾巴又变回「凑不够就丢掉」
  ⇒ §953 特意改掉的那条行为（少丢内容）被改回去 ⇒ 门必须红。
  ⭐ 这条提醒：**「顺手修好的东西」也要钉住**，否则下一次重构会悄悄改回去。
"""

import subprocess
import sys

# ⚠ 禁止写 .pyc ⇒ 仓里不留 __pycache__（否则工作树会多出未跟踪目录）
sys.dont_write_bytecode = True
sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
from _harness import (  # noqa: E402
    FRONTEND, Batch, node_runner, run,
)

TARGETS = [
    # 被测的三个生产文件
    'frontend/src/utils/webm-incremental.ts',
    'frontend/src/native/recording-audio-transcode.ts',
    'frontend/src/utils/wav-encode.ts',
    # 判据本身（允许「把量具自己打坏」的变异）
    'frontend/src/native/__tests__/recording-audio-transcode.test.mjs',
]

JUDGE = 'src/native/__tests__/recording-audio-transcode.test.mjs'


def judge_runner():
    """前端判据（§953 的判据）—— 批次的**默认**运行器。"""
    return node_runner(JUDGE, cwd=FRONTEND)


b = Batch('§953 webm 增量解码：逐簇解，替掉「整场 / 累计」',
          judge_runner(), TARGETS)

MODULE = b.targets[0]
DECODER = b.targets[1]
WAV_ENCODE = b.targets[2]
TESTFILE = b.targets[3]


def main():
    # ── M1 必须红：切分器退回「每次返回整块缓冲」⇒ 解码退回 O(时长²) ─────
    M1_OLD = '    if (offsets.length < 2) return []\n'
    M1_NEW = '    if (offsets.length < 2) return [{ bytes: this.buffer, timecodeMs: 0 }]\n'

    def m1():
        b.mutate(MODULE, M1_OLD, M1_NEW, 'M1')
    b.case('M1 增量解码退回「每次解整场」⇒ 喂给解码器的字节回到 O(时长²) ⇒ 门必须红', True,
           must_contain=['喂给解码器的字节是 O(整场)'],
           mutate_fn=m1)

    # ── M2 必须红：假 Cluster ID 不再被 Timecode 校验挡住 ────────────────
    M2_OLD = ('    const timecode = readWebmClusterTimecode(bytes, i)\n'
              '    if (timecode === null) continue\n')
    M2_NEW = '    const timecode = readWebmClusterTimecode(bytes, i) ?? 0\n'

    def m2():
        b.mutate(MODULE, M2_OLD, M2_NEW, 'M2')
    b.case('M2 簇边界不再校验 Timecode ⇒ 载荷里的假 Cluster ID 会被当真 ⇒ 门必须红', True,
           must_contain=['载荷里的假 Cluster ID'],
           mutate_fn=m2)

    # ── M3 必须红：解码时不再带初始化前缀 ⇒ 单簇解不开 ──────────────────
    M3_OLD = '    const payload = composeWebmWithInit(init, cluster.bytes)\n'
    M3_NEW = '    const payload = cluster.bytes\n'

    def m3():
        b.mutate(DECODER, M3_OLD, M3_NEW, 'M3')
    b.case('M3 逐簇解码不再拼「初始化前缀 + 簇」⇒ 单簇解不开 ⇒ 门必须红', True,
           must_contain=['每次解码都必须带上初始化前缀'],
           mutate_fn=m3)

    # ── M4 必须红：会议收尾退回整场解码（峰值 659 MB/小时回来）──────────
    M4_OLD = '  if (!clusters.length) return null\n'
    M4_NEW = '  if (clusters.length) return null\n'

    def m4():
        b.mutate(WAV_ENCODE, M4_OLD, M4_NEW, 'M4')
    b.case('M4 会议收尾退回整场解码 ⇒ 解码次数从 N 回到 1 ⇒ 门必须红', True,
           must_contain=['会议收尾的整场转码改为'],
           mutate_fn=m4)

    # ── M5 必须红：收尾只取第一段簇 ⇒ 中间的音频被丢掉 ──────────────────
    M5_OLD = ('    for (let i = 0; i + 1 < offsets.length; i++) {\n'
              '      const timecode = readWebmClusterTimecode(this.buffer, offsets[i])\n')
    M5_NEW = ('    for (let i = 0; i < 1; i++) {\n'
              '      const timecode = readWebmClusterTimecode(this.buffer, offsets[i])\n')

    def m5():
        b.mutate(MODULE, M5_OLD, M5_NEW, 'M5')
    b.case('M5 收尾只取第一段簇 ⇒ 中间的簇被丢掉 ⇒ 门必须红', True,
           must_contain=['WebmClusterSplitter：逐片推进'],
           mutate_fn=m5)

    # ── M6 必须红：裁剪点取错 ⇒ 缓冲不再被裁（内存回到 O(整场)）──────────
    M6_OLD = '      this.buffer = this.buffer.slice(offsets[offsets.length - 1])\n'
    M6_NEW = '      this.buffer = this.buffer.slice(0)\n'

    def m6():
        b.mutate(MODULE, M6_OLD, M6_NEW, 'M6')
    b.case('M6 切完簇不裁缓冲 ⇒ 内存回到 O(整场) ⇒ 门必须红', True,
           must_contain=['缓冲不随录音时长增长'],
           mutate_fn=m6)

    # ── M7 必须红：解码失败时先出队 ⇒ 那一簇永久丢失 ────────────────────
    M7_OLD = ('      const decoded = await this.decodeCluster(cluster)\n'
              '      this.queued.shift()\n')
    M7_NEW = ('      this.queued.shift()\n'
              '      const decoded = await this.decodeCluster(cluster)\n')

    def m7():
        b.mutate(DECODER, M7_OLD, M7_NEW, 'M7')
    b.case('M7 解码失败时先出队 ⇒ 那一段音频永久丢失（§571 的承诺被破）⇒ 门必须红', True,
           must_contain=['§571'],
           mutate_fn=m7)

    # ── M8 必须红：尾巴又被丢掉（§953 特意改掉的那条行为）───────────────
    M8_OLD = ('      //   ⛔ 代价：pending 会短暂多留一点音频（实测 ≤ 一簇，≈ 4 秒）。\n'
              '      return null\n')
    M8_NEW = ('      //   ⛔ 代价：pending 会短暂多留一点音频（实测 ≤ 一簇，≈ 4 秒）。\n'
              '      this.sentUpToSec = to\n'
              '      this.pending = []\n'
              '      return null\n')

    def m8():
        b.mutate(DECODER, M8_OLD, M8_NEW, 'M8')
    b.case('M8 凑不够 minWindowSec 就把尾巴丢掉（旧行为回归）⇒ 门必须红', True,
           must_contain=['尾巴'],
           mutate_fn=m8)

    # ── N1 负控：只加一行注释 ⇒ 门必须保持绿（不许误报）────────────────
    N1_OLD = '    const out: WebmCluster[] = []\n'
    N1_NEW = '    const out: WebmCluster[] = []   // 负控：只加一行注释\n'

    def n1():
        b.mutate(MODULE, N1_OLD, N1_NEW, 'N1', count=1)
    b.case('N1 负控：只加一行注释 ⇒ 门必须保持绿（不许误报）', False,
           must_not_contain=['not ok'],
           mutate_fn=n1)

    # ── 收尾自证：还原态下判据必须绿 ───────────────────────────────────
    def final_judge():
        p = subprocess.run(
            ['node', '--experimental-strip-types', '--test', JUDGE],
            cwd=str(FRONTEND), capture_output=True, text=True, timeout=900)
        return p.returncode, p.stdout + p.stderr
    b.case('收尾自证：还原态下判据必须绿', False,
           must_not_contain=['not ok'],
           runner=final_judge)


if __name__ == '__main__':
    run(b, main)

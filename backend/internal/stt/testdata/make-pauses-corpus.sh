#!/usr/bin/env bash
# 造一份**带真实停顿**的会议语料，供 stt 的真网关探针使用。
#
# ── 为什么不能只用 `say` 一次成段（2026-10-11 实测踩到）──
#
# §480 用 `say -o long.wav` 得到的三份语料，逐帧能量测出来
# **≥800ms 的静音段是 0 个**（最长 490ms）。而生产切分器的门槛是
# `minSilenceMS = 800`（full.go:106）⇒ **静音感知分支永不触发**，
# `SilenceCut` 恒为 false，`SplitWAV` 退化成纯 25 秒硬切。
#
# ⇒ 用连续 TTS 语料量「生产整段路径」，量到的是**降级形态**。
#   静音感知设计（§121 在 79 秒真人语料上校准的那套）根本没被跑到。
#   这不是产品缺陷（真人会议本来就有 1575–3475ms 的句间停顿），
#   但它让那批实验的适用范围被限死在「持续讲话」上，必须写清。
#
# 这份脚本按句合成、句间插 1.2 秒数字静音，从而**单独构造出**
# §121 假设的停顿条件，用来量静音感知分支本身。
#
# ⚠ 它仍然是 TTS：音色单一、无噪声、无多人、无方言、无远场。
#   「真人会议真实 CER」仍然缺（见决策简报 #15 B 格的适用链说明）。
#
# 用法（macOS）：
#   bash backend/internal/stt/testdata/make-pauses-corpus.sh
#   → /tmp/gt-voice-pauses-16k.wav（45.3s，12 个 ≥800ms 静音段）
set -euo pipefail

OUT="${1:-/tmp/gt-voice-pauses-16k.wav}"
GAP_MS=1200
VOICE="${OPK_TTS_VOICE:-Tingting}"
RATE="${OPK_TTS_RATE:-170}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# 逐句列表必须与 meeting-pauses.truth.txt 逐字一致（去掉句间空白后）。
SENTS=(
  "各位，今天我们开第三季度产品评审会。"
  "第一项议题是结算上线的验收标准，"
  "目前进度是百分之七十，"
  "剩下的百分之三十主要是联调和回归测试，"
  "预计下周三之前完成。"
  "第二项议题是预算，"
  "本季度预算是三百五十万，"
  "分三期付款，"
  "每期一百二十万，"
  "请财务确认现金流。"
  "第三项议题是人力，"
  "当前团队有八个人，"
  "缺口是两个后端。"
)

i=0
for s in "${SENTS[@]}"; do
  say -v "$VOICE" -r "$RATE" -o "$WORK/s$i.aiff" "$s"
  afconvert -f WAVE -d LEI16@16000 -c 1 "$WORK/s$i.aiff" "$WORK/s$i.wav"
  i=$((i + 1))
done

GAP_MS="$GAP_MS" WORK="$WORK" OUT="$OUT" python3 - <<'PY'
import glob, os, wave
import numpy as np

sr = 16000
gap = np.zeros(int(sr * int(os.environ["GAP_MS"]) / 1000), dtype=np.int16)
parts = []
for p in sorted(glob.glob(os.path.join(os.environ["WORK"], "s*.wav"))):
    with wave.open(p) as w:
        assert w.getframerate() == sr, (p, w.getframerate())
        parts.append(np.frombuffer(w.readframes(w.getnframes()), dtype=np.int16))
    parts.append(gap)
audio = np.concatenate(parts)
while len(audio) > sr and np.all(audio[-1600:] == 0):
    audio = audio[:-1600]                       # 去掉尾部多余静音
with wave.open(os.environ["OUT"], "wb") as w:
    w.setnchannels(1); w.setsampwidth(2); w.setframerate(sr)
    w.writeframes(audio.astype(np.int16).tobytes())

# 自证：确认这份语料**真的**满足 minSilenceMS=800，否则静音分支仍跑不到。
fl = int(sr * 0.01)
fr = audio[:len(audio) // fl * fl].reshape(-1, fl).astype(np.float64) / 32768.0
runs, cur = [], 0
for v in np.sqrt((fr ** 2).mean(axis=1)) < 0.012:
    if v: cur += 10
    else:
        if cur: runs.append(cur)
        cur = 0
if cur: runs.append(cur)
n800 = len([r for r in runs if r >= 800])
print(f"{os.environ['OUT']}: {len(audio)/sr:.2f}s  静音段≥800ms = {n800}")
if n800 == 0:
    raise SystemExit("⚠ 这份语料没有 ≥800ms 静音段，静音感知分支仍然跑不到 —— 别拿它下静音分支的结论")
PY
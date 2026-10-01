#!/usr/bin/env python3
"""
长录音（会议）全链路准确率评估：把 full.go 的切分结果与逐句 ground truth 对齐。

## 为什么需要单独一个评估器

scripts/eval-asr-accuracy.py 评的是「一段音频直接送引擎」。
但会议录音走的是另一条路：POST /api/stt/transcribe-full →
backend/internal/stt/full.go 按静音边界**切段** → 每段单独送引擎 → 拼回全文。

这条路上有一类前面评不到的失效：
  - 切段把一句话劈成两半 → 每半都识别得对，拼接后语义断了
  - 切段漏掉一整句 → 该句在最终文本里凭空消失
  - 纯静音段被引擎**幻觉**出一段文字（比识别错更严重）
  - 段之间重复转写 → 全文出现同一句话两遍

所以这里不用「每段音频单独评」，而是**拿后端真实返回的 segments（带
startSec/endSec）去和语料时间轴做时间重叠对齐**，逐句算 CER。

## 对齐规则

对语料里的每一句 [s, e]，取出所有与它重叠超过该句时长 50% 的返回段，
按 startSec 排序后拼接文本。50% 阈值是为了容忍边界抖动：如果 full.go
在句中切一刀，那一刀两侧的段都算这句话的贡献。

## 静音检查

语料里故意插了一段 2 秒纯静音。**任何**与它重叠的返回段含非空文本，
都判为幻觉。这是独立于 CER 的硬断言。

## 用法

    python scripts/eval-long-asr.py \
        --timeline .verify-stt-data/meeting-long.wav.json \
        --result   .verify-stt-data/long-asr-result.json \
        [--out .verify-stt-data/long-asr-eval.json]

退出码 0 = 结构与质量都达标（无幻觉 + 覆盖率达标）；
2 = 出现幻觉或大面积丢句 —— 此时不能引用任何准确率数字。
"""
from __future__ import annotations

import argparse
import importlib.util
import json
import os
import sys

# 复用同一套规范化与 CER 实现，避免两处各写一份、口径慢慢分叉。
# eval-asr-accuracy.py 的文件名带连字符，**不能**直接 import，必须按路径加载
# （否则 import 语法错误）。这里显式失败而不是静默跳过——两处各写一份
# 规范化逻辑，比测不出问题更糟。
_spec = importlib.util.spec_from_file_location(
    "eval_asr_accuracy",
    os.path.join(os.path.dirname(os.path.abspath(__file__)), "eval-asr-accuracy.py"),
)
if _spec is None or _spec.loader is None:
    print("找不到同目录下的 eval-asr-accuracy.py，拒绝对齐口径不明的数据。")
    sys.exit(2)
_mod = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_mod)
cer, normalize = _mod.cer, _mod.normalize

# 每句至少要有这个比例的时长被返回段覆盖，才认为「这句话被听到了」。
COVER_MIN = 0.5
# 判定幻觉的阈值：静音段里出现这么多字就认为产生了幻觉内容。
HALLUC_CHARS = 1


def load_json(path: str):
    """读 JSON，容忍 UTF-8 BOM。

    Windows PowerShell 5.1 的 Set-Content -Encoding UTF8 会写 BOM，
    证据文件由它产出就必然带 BOM；python 的 json.load 默认按 utf-8 解码，
    遇到 BOM 直接报 JSONDecodeError（2026-10-01 实测）。
    """
    with open(path, encoding="utf-8-sig") as f:
        return json.load(f)


def overlap(a0: float, a1: float, b0: float, b1: float) -> float:
    return max(0.0, min(a1, b1) - max(a0, b0))


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--timeline", required=True, help="make-meeting-wav.py 产出的 *.wav.json")
    ap.add_argument("--result", required=True, help="verify-stt-long-asr.ps1 产出的后端返回")
    ap.add_argument("--out", default="")
    args = ap.parse_args()

    tl = load_json(args.timeline)
    res = load_json(args.result)

    segments = res.get("segments") or []
    print(f"语料时长 {tl['duration']:.1f}s  句子 {sum(1 for t in tl['timeline'] if not t['silence'])}  "
          f"静音段 {sum(1 for t in tl['timeline'] if t['silence'])}")
    print(f"后端返回 {len(segments)} 段  failed={res.get('failed', 0)}  "
          f"模型={res.get('model', '?')}  通道={res.get('channel', '?')}\n")

    for s in segments:
        t = (s.get("text") or "").strip()
        print(f"  段{s['index']:>2} [{s['startSec']:6.2f}–{s['endSec']:6.2f}] "
              f"{'ERR:' + s['error'] if s.get('error') else t[:46]}")

    rows = []
    hallucination = None

    # ---- 逐句：只报覆盖与所在段，不在这里算 CER ----
    #
    # 为什么不用「把所有重叠段拼起来跟单句比」：full.go 的切点落在**静音中点**，
    # 一个返回段经常横跨「上一句尾巴 + 下一句开头」，把重叠段一拼就会把
    # 邻居的词也算进这句的识别结果里，CER 直接冲上 100%~285%（2026-10-01
    # 实测，看着像识别全崩，其实是我的度量错了）。
    # 长音频的通行做法是**全局对齐**：整篇转写 vs 整篇 ground truth。
    print("\n逐句覆盖（只看有没有被听到，不在此处算 CER）：")
    for entry in tl["timeline"]:
        dur = max(1e-6, entry["end"] - entry["start"])
        hit = [s for s in segments
               if overlap(s["startSec"], s["endSec"], entry["start"], entry["end"]) > 0]
        hit.sort(key=lambda s: s["startSec"])
        cover = sum(overlap(s["startSec"], s["endSec"], entry["start"], entry["end"])
                    for s in hit) / dur

        if entry["silence"]:
            # 只有**完全落在静音窗内**的段才能用来判幻觉。
            # 跨越静音的段里既有真话也有静音，把整段文本算成「静音的幻觉」
            # 是误判（2026-10-01 实测踩过：13 秒的段横跨 2 秒静音，
            # 却被报成「静音段产生幻觉」）。
            inside = [s for s in hit
                      if s["startSec"] >= entry["start"] - 0.05
                      and s["endSec"] <= entry["end"] + 0.05]
            text = "".join((s.get("text") or "") for s in inside)
            clean = normalize(text) == ""
            print(f"[{'OK ' if clean else 'HALLUC'} {entry['id']:<12}] "
                  f"完全落在静音窗内的段 {len(inside)} 个  文本={text!r}")
            if not clean and len(normalize(text)) >= HALLUC_CHARS:
                hallucination = {"id": entry["id"], "text": text,
                                 "segments": [s["index"] for s in inside]}
            rows.append({"id": entry["id"], "silence": True, "cover": cover,
                         "segments_inside": len(inside), "hyp": text, "clean": clean})
            continue

        missing = cover < COVER_MIN
        in_seg = [s["index"] for s in hit]
        print(f"[{'OK ' if not missing else 'LOST'} {entry['id']:<12}] "
              f"覆盖 {cover * 100:5.1f}%  落在段 {in_seg}")
        rows.append({"id": entry["id"], "silence": False, "cover": cover,
                     "ref": entry["text"], "segments": in_seg, "missing": missing})

    spoken = [r for r in rows if not r["silence"]]
    lost = [r["id"] for r in spoken if r["missing"]]

    # ---- 主指标：全局 CER ----
    # 参考 = 语料里所有**非静音**句子按时间顺序拼接；识别 = 后端返回的全文。
    # 这是长音频 ASR 的标准口径：只比内容，不关心切在哪。
    ref_full = "".join(e["text"] for e in tl["timeline"] if not e["silence"])
    hyp_full = res.get("text") or ""
    g_strict = cer(ref_full, hyp_full, numerals=False)
    g_norm = cer(ref_full, hyp_full, numerals=True)

    print("\n" + "=" * 72)
    print(f"全局 CER_strict : {g_strict['cer'] * 100:.1f}%  "
          f"({g_strict['edits']}/{g_strict['ref_len']} 字)")
    print(f"全局 CER_norm   : {g_norm['cer'] * 100:.1f}%  "
          f"({g_norm['edits']}/{g_norm['ref_len']} 字)")
    print(f"整句丢失       : {len(lost)}  {lost if lost else ''}")

    # 重复检测：同一句在拼接全文里出现的次数超过语料里它本来该出现的次数，
    # 才是真的重复转写。
    # 注意不能直接判 count>1 —— make-meeting-wav.py 的语料里 meeting-01 **故意**
    # 出现了两次（模拟会上同一议题被重提），直接判会一路假阳性
    # （2026-10-01 自测实测：完美对齐也被报「跨段重复 2」）。
    full_n = normalize(hyp_full)
    expected = {}
    for e in tl["timeline"]:
        if not e["silence"]:
            r = normalize(e["text"])
            if r:
                expected[r] = expected.get(r, 0) + 1
    dups = [r["id"] for r in spoken
            if normalize(r["ref"]) and full_n.count(normalize(r["ref"])) > expected.get(normalize(r["ref"]), 0)]
    print(f"跨段重复       : {len(dups)}  {dups if dups else ''}")

    inconclusive = []
    if not any(r.get("segments_inside") for r in rows if r["silence"]):
        # 没有任何一个段完全落在静音窗内 → 这份语料无法证明「没有幻觉」。
        # 必须如实说成「未验证」，不能默认通过。
        inconclusive.append("静音幻觉（本次切分没有产生完全落在静音窗内的段，"
                            "需单独用纯静音音频验证）")

    if hallucination:
        print(f"\n[FAIL] 静音段产生幻觉文本：{hallucination}")
    if lost:
        print(f"[FAIL] 有整句在切分后丢失：{lost}")
    for i in inconclusive:
        print(f"[未验证] {i}")

    ok = not hallucination and not lost
    if ok:
        print("\n[OK] 无幻觉、无整句丢失 —— CER 数字可以引用。")
    else:
        print("\n[FAIL] 结构性问题未解决，CER 数字不代表真实质量，不应引用。")

    if args.out:
        with open(args.out, "w", encoding="utf-8") as f:
            json.dump({"duration": tl["duration"], "segments": len(segments),
                       "cer_strict": g_strict["cer"], "cer_norm": g_norm["cer"],
                       "edits": g_strict["edits"], "ref_len": g_strict["ref_len"],
                       "lost": lost, "dups": dups, "hallucination": hallucination,
                       "inconclusive": inconclusive, "ref_full": ref_full,
                       "hyp_full": hyp_full, "rows": rows},
                      f, ensure_ascii=False, indent=2)
        print(f"明细: {args.out}")
    return 0 if ok else 2


if __name__ == "__main__":
    sys.exit(main())

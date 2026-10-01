#!/usr/bin/env python3
"""
真实 ASR 准确率评测：把「识别得准不准」变成可计算的问题。

## 为什么需要这个

此前所有 ASR 验证都是**结构性**的：假上游写死文本 / 纯音调音频，只能证明
「音频到了上游、请求形状对、返回被正确解析」。**没有一个能回答
「中文识别得准吗」** —— 那个问题一直挂在「未验证项」里，因为它需要
(a) 真引擎 (b) ground truth。

两者现在都有了：
  - 真引擎：scripts/local-asr-server.py（faster-whisper，本地 CPU，零成本）
  - ground truth：scripts/make-asr-groundtruth.ps1 用系统中文 TTS 合成，
    文本自己写，所以每段的正确转写是**已知**的

## 指标

中文 ASR 用 **CER（字符错误率）** 而不是 WER（词错误率）——中文没有空格
分词，CER 是通行标准：

    CER = (替换 S + 删除 D + 插入 I) / 参考文本字符数

配套两个辅助量：
  - 完全正确率：整段一字不差的比例（用户体感最直接的指标）
  - 分类明细：meeting / dictation / numbers / mixed 四类的错误率差异，
    用来判断「哪类场景是真雷区」

## 比较规范化

中英混排、数字读法、标点在不同引擎间差异很大，直接比会误判。比较前统一：
  - 去标点与空白
  - 全角转半角
  - 英文字母转小写
  - 数字不做等价展开（「3」与「三」算不同，但会在明细里单独列出来，
    避免把「数字规范化」的差异算成识别错误）

## 用法

    python scripts/eval-asr-accuracy.py \
        --corpus .verify-stt-data/asr-corpus \
        --endpoint http://127.0.0.1:18900 \
        [--out .verify-stt-data/asr-eval-base.json]

退出码 0 = 全部段落都成功转写（不设质量阈值；阈值判断交给看报告的人，
因为「CER 多低算可用」取决于场景，见输出里的解读段）。
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
import urllib.request
import uuid

# 比较前要剔除的标点与空白。
#
# 不用正则字符类：中文引号/书名号/破折号混在 r"..." 里会把引号截断
# （2026-10-01 实测踩过，字符串被切成两段，后半段不再是 raw string，
# 于是 \[ 之类全报 invalid escape sequence）。改用 unicodedata 分类，
# 既没有转义问题，也天然覆盖各类 Unicode 标点。
import unicodedata


def _is_punct_or_space(ch: str) -> bool:
    if ch.isspace():
        return True
    cat = unicodedata.category(ch)
    # P* 各类标点（Pc 连接符、 Pd 破折号、 Ps/Pe 引号、 Pi/Pf 引号、 Po 其它）
    if cat.startswith("P"):
        return True
    # 一些引擎会输出零宽字符与软连字符
    if cat in ("Cf", "Cc"):
        return True
    return False


def strip_punct(s: str) -> str:
    return "".join(ch for ch in s if not _is_punct_or_space(ch))


def normalize(s: str) -> str:
    """比较用的规范化：只做无争议的变换，不做数字等价展开。"""
    s = strip_punct(s.strip())
    # 全角英数 → 半角
    out = []
    for ch in s:
        o = ord(ch)
        if 0xFF01 <= o <= 0xFF5E:
            out.append(chr(o - 0xFEE0))
        else:
            out.append(ch)
    return "".join(out).lower()


def cer(ref: str, hyp: str) -> dict:
    """标准 Levenshtein，返回 (S, D, I) 与 CER。"""
    ref, hyp = normalize(ref), normalize(hyp)
    n, m = len(ref), len(hyp)
    if n == 0:
        return {"S": 0, "D": 0, "I": m, "cer": 1.0 if m else 0.0, "exact": m == 0}
    prev = list(range(m + 1))
    back = []
    for i in range(1, n + 1):
        cur = [i] + [0] * m
        for j in range(1, m + 1):
            if ref[i - 1] == hyp[j - 1]:
                cur[j] = prev[j - 1]
            else:
                cur[j] = 1 + min(prev[j - 1], prev[j], cur[j - 1])
        back.append(cur)
        prev = cur
    edits = prev[m]
    return {"edits": edits, "cer": edits / n, "exact": edits == 0, "ref_len": n}


def post_audio(endpoint: str, path: str, language: str = "zh") -> dict:
    """multipart 上传，与后端 Go 侧用的形状一致。"""
    boundary = "----asr" + uuid.uuid4().hex
    with open(path, "rb") as f:
        data = f.read()
    parts = []
    parts.append(f"--{boundary}\r\n".encode())
    parts.append(
        f'Content-Disposition: form-data; name="file"; filename="{os.path.basename(path)}"\r\n'.encode()
    )
    parts.append(b"Content-Type: audio/wav\r\n\r\n")
    parts.append(data)
    parts.append(f"\r\n--{boundary}\r\n".encode())
    parts.append(f'Content-Disposition: form-data; name="language"\r\n\r\n{language}\r\n'.encode())
    parts.append(f"--{boundary}--\r\n".encode())
    body = b"".join(parts)

    req = urllib.request.Request(
        endpoint.rstrip("/") + "/v1/audio/transcriptions",
        data=body,
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=1800) as r:
        return json.loads(r.read().decode("utf-8"))


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--corpus", required=True, help="ground truth 目录（含 groundtruth.json 与 wav）")
    ap.add_argument("--endpoint", default="http://127.0.0.1:18900")
    ap.add_argument("--out", default="")
    args = ap.parse_args()

    gt_path = os.path.join(args.corpus, "groundtruth.json")
    with open(gt_path, encoding="utf-8") as f:
        items = json.load(f)

    print(f"语料 {len(items)} 段  endpoint={args.endpoint}\n")
    rows = []
    for it in items:
        wav = os.path.join(args.corpus, it["file"])
        t0 = time.time()
        try:
            r = post_audio(args.endpoint, wav)
            hyp = r.get("text", "")
            dur = r.get("duration", 0)
            err = None
        except Exception as e:
            hyp, dur, err = "", 0, f"{type(e).__name__}: {e}"
        el = time.time() - t0

        if err:
            print(f"[{it['id']:<12}] 请求失败：{err}")
            rows.append({**it, "hyp": "", "error": err, "seconds": el})
            continue

        m = cer(it["text"], hyp)
        flag = "OK " if m["exact"] else ("~  " if m["cer"] <= 0.2 else "ERR")
        rtf = (el / dur) if dur else 0
        print(f"[{flag} {it['id']:<12}] CER={m['cer'] * 100:5.1f}%  {dur:5.1f}s  {el:5.1f}s (rtf={rtf:.2f})  [{it['category']}]")
        print(f"      参考: {it['text']}")
        print(f"      识别: {hyp}")
        rows.append({
            "id": it["id"], "category": it["category"], "ref": it["text"],
            "hyp": hyp, "duration": dur, "seconds": el, "rtf": rtf,
            "cer": m["cer"], "exact": m["exact"], "error": None,
        })

    ok = [r for r in rows if r["error"] is None]
    if not ok:
        print("\n没有任何一段成功转写 —— 引擎或链路有问题，不能谈准确率。")
        return 2

    total_ref = sum(cer(r["ref"], r["hyp"])["ref_len"] for r in ok)
    total_edits = sum(cer(r["ref"], r["hyp"])["edits"] for r in ok)
    overall = total_edits / total_ref if total_ref else 0
    exact_n = sum(1 for r in ok if r["exact"])

    print("\n" + "=" * 64)
    print(f"总体 CER        : {overall * 100:.1f}%   （{total_edits} 处错误 / {total_ref} 字）")
    print(f"完全正确        : {exact_n}/{len(ok)} 段 ({exact_n / len(ok) * 100:.0f}%)")
    print(f"平均实时率 RTF  : {sum(r['rtf'] for r in ok) / len(ok):.2f}  （<1 表示比实时快）")

    by_cat: dict[str, list] = {}
    for r in ok:
        by_cat.setdefault(r["category"], []).append(cer(r["ref"], r["hyp"]))
    print("\n分类别：")
    for cat, ms in sorted(by_cat.items()):
        n = sum(m["ref_len"] for m in ms)
        e = sum(m["edits"] for m in ms)
        ex = sum(1 for m in ms if m["exact"])
        print(f"  {cat:<11} CER={e / n * 100:5.1f}%   完全正确 {ex}/{len(ms)}")

    print(
        "\n解读：\n"
        "  - CER < 5%  日常可用；< 2% 接近「听不出来」\n"
        "  - 纯音调/静音输入若也返回文本，是**幻觉**，比识别错更严重，\n"
        "    上层已有 usage.total_characters==0 与文本正则双信号守卫，\n"
        "    本脚本不覆盖那一路（见 verify-stt.ps1 的 probe 用例）\n"
        "  - numbers 类 CER 通常明显高于其它类，因为数字读法歧义大；\n"
        "    若业务依赖精确数字，应在展示层做二次确认而不是指望 ASR"
    )

    if args.out:
        with open(args.out, "w", encoding="utf-8") as f:
            json.dump(
                {"endpoint": args.endpoint, "overall_cer": overall,
                 "exact": exact_n, "n": len(ok), "rows": rows},
                f, ensure_ascii=False, indent=2)
        print(f"\n明细: {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

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


# ---------------------------------------------------------------------------
# 数字归一化
# ---------------------------------------------------------------------------
#
# 为什么需要第二个指标：实测 2026-10-01，whisper-base 把「二零二六年十月十五号
# 上午十点」输出成「2026年10月15号上午10点」。**内容完全正确**，只是把中文
# 数字写成了阿拉伯数字 —— 对笔记场景这其实是**更好的**输出（可搜索、可计算）。
#
# 但严格 CER 会把它整段算成错误，于是 base（长句 5.7%）与 tiny（长句 12.9%）
# 的真实差距被数字格式的噪声盖住，总体分几乎一样（都 22.1%）。
#
# 所以报两个数：
#   CER_strict    不做数字归一 —— 反映「用户逐字看到的差异」
#   CER_norm      中文数字↔阿拉伯数字视为等价 —— 反映「信息有没有听错」
# 两个数差得越多，说明问题出在格式而不是听错。
#
# 注意：CER_norm 只能免除**格式**差异，不能免除**数值**差异。
# 「十二万三千四百五十」→「12,450」 数值本身就错了，两个指标都会算错。

_CN_DIGITS = {"零": 0, "〇": 0, "一": 1, "二": 2, "两": 2, "三": 3, "四": 4,
              "五": 5, "六": 6, "七": 7, "八": 8, "九": 9}
_CN_UNITS = {"十": 10, "百": 100, "千": 1000, "万": 10000, "亿": 100000000}
# 常见口语量词，不影响数值本身
_CN_MEASURE = {"个", "块", "元", "毛", "角"}


def _cn_num_to_int(s: str):
    """把一段纯中文数字转成 int；无法解析返回 None。

    三种读法必须分开处理，混在一起就会算错（2026-10-01 自测实测）：

    1. **位值读法**：十二万三千四百五十 = 123450
       遇到十/百/千/万/亿 走位值累加。
    2. **逐位读法**：二零二六 = 2026
       年份、编号、日期都这么读。**整段没有位值单位时按位拼接**，
       否则「二零二六」会被当成 2+0+2+6 算成 10（自测实测曾错成 6）。
    3. **百分之**：百分之十八 = 百分之18
       「百」在「百分之」里是词不是位值，必须整词识别。

    遇到「点」（小数）直接放弃解析，交给严格指标去报 ——
    宁可少归一，也不要归一错。
    """
    if not s:
        return None
    # 3. 百分之 / 千分之 整词先行
    for pct, mult in (("百分之", None), ("千分之", None)):
        if s.startswith(pct):
            rest = s[len(pct):]
            v = _cn_num_to_int(rest) if rest else None
            return None if v is None else v  # 「百分之」本身不是数值，交给外层拼接
    if any(c not in _CN_DIGITS and c not in _CN_UNITS and c not in _CN_MEASURE
           for c in s):
        return None

    has_unit = any(c in _CN_UNITS for c in s)
    # 2. 无位值单位 → 逐位拼接
    if not has_unit:
        digits = "".join(str(_CN_DIGITS[c]) for c in s if c in _CN_DIGITS)
        if digits and len(digits) == len([c for c in s if c in _CN_DIGITS]):
            return int(digits)
        return None

    # 1. 位值读法
    total, section, digit = 0, 0, 0
    for ch in s:
        if ch in _CN_DIGITS:
            digit = _CN_DIGITS[ch]
        elif ch in _CN_UNITS:
            u = _CN_UNITS[ch]
            if u >= 10000:
                section = (section + digit) * u
                total += section
                section, digit = 0, 0
            else:
                # 「十五」= 10 + 5，前面没有数字时十本身就是 10
                section += (digit or 1) * u
                digit = 0
        elif ch in _CN_MEASURE:
            pass  # 量词不影响数值，**不能**把待定数字清零（自测踩过）
    total += section + digit
    return total if total else None


def normalize_numerals(s: str) -> str:
    """把中文数字串替换成阿拉伯数字，让两种写法可比。"""
    out = []
    i = 0
    keys = sorted(set(_CN_DIGITS) | set(_CN_UNITS) | set(_CN_MEASURE),
                  key=len, reverse=True)
    while i < len(s):
        # 「百分之」整体识别：只换后面的数字，「百」本身是词不是位值
        for pct in ("百分之", "千分之"):
            if s.startswith(pct, i):
                out.append(pct)
                i += len(pct)
                break
        else:
            # 数字串在**量词处断开**：量词不属于数值本身，必须原样保留。
            # 否则「三个议题」会变成「3议题」（自测实测踩过）。
            if s[i] in set(_CN_DIGITS) | set(_CN_UNITS):
                j = i
                while j < len(s) and (s[j] in _CN_DIGITS or s[j] in _CN_UNITS):
                    j += 1
                run = s[i:j].split("点")[0]
                v = _cn_num_to_int(run)
                if v is not None:
                    out.append(str(v))
                    i = j
                    continue
            out.append(s[i])
            i += 1
    return "".join(out)


def normalize(s: str, numerals: bool = False) -> str:
    """比较用的规范化：只做无争议的变换，不做标点/大小写之外的改写。

    numerals=True 时额外做中文数字→阿拉伯数字的等价替换（见上方说明）。
    """
    s = strip_punct(s.strip())
    # 全角英数 → 半角
    out = []
    for ch in s:
        o = ord(ch)
        if 0xFF01 <= o <= 0xFF5E:
            out.append(chr(o - 0xFEE0))
        else:
            out.append(ch)
    s = "".join(out).lower()
    if numerals:
        s = normalize_numerals(s)
    return s


def _levenshtein(ref: str, hyp: str) -> int:
    n, m = len(ref), len(hyp)
    if n == 0:
        return m
    prev = list(range(m + 1))
    for i in range(1, n + 1):
        cur = [i] + [0] * m
        for j in range(1, m + 1):
            if ref[i - 1] == hyp[j - 1]:
                cur[j] = prev[j - 1]
            else:
                cur[j] = 1 + min(prev[j - 1], prev[j], cur[j - 1])
        prev = cur
    return prev[m]


def cer(ref: str, hyp: str, numerals: bool = False) -> dict:
    """CER。两个指标：
       numerals=False → CER_strict（用户逐字看到的差异）
       numerals=True  → CER_norm（信息有没有听错，免除数字格式差异）
    """
    r, h = normalize(ref, numerals), normalize(hyp, numerals)
    edits = _levenshtein(r, h)
    return {"edits": edits, "cer": edits / len(r) if r else (1.0 if h else 0.0),
            "exact": edits == 0, "ref_len": len(r)}


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

        ms_strict = cer(it["text"], hyp, numerals=False)
        ms_norm = cer(it["text"], hyp, numerals=True)
        flag = "OK " if ms_norm["exact"] else ("~  " if ms_norm["cer"] <= 0.2 else "ERR")
        rtf = (el / dur) if dur else 0
        print(f"[{flag} {it['id']:<12}] CER strict={ms_strict['cer'] * 100:5.1f}%  "
              f"norm={ms_norm['cer'] * 100:5.1f}%  {dur:5.1f}s  {el:5.1f}s (rtf={rtf:.2f})  [{it['category']}]")
        print(f"      参考: {it['text']}")
        print(f"      识别: {hyp}")
        rows.append({
            "id": it["id"], "category": it["category"], "ref": it["text"],
            "hyp": hyp, "duration": dur, "seconds": el, "rtf": rtf,
            "cer_strict": ms_strict["cer"], "cer_norm": ms_norm["cer"],
            "exact": ms_norm["exact"], "error": None,
        })

    ok = [r for r in rows if r["error"] is None]
    if not ok:
        print("\n没有任何一段成功转写 —— 引擎或链路有问题，不能谈准确率。")
        return 2

    n_ref = sum(cer(r["ref"], r["hyp"])["ref_len"] for r in ok)
    e_strict = sum(cer(r["ref"], r["hyp"])["edits"] for r in ok)
    e_norm = sum(cer(r["ref"], r["hyp"], numerals=True)["edits"] for r in ok)
    ov_s = e_strict / n_ref if n_ref else 0
    ov_n = e_norm / n_ref if n_ref else 0
    exact_n = sum(1 for r in ok if r["exact"])

    print("\n" + "=" * 72)
    print(f"CER_strict（逐字差异）: {ov_s * 100:.1f}%   （{e_strict} 处 / {n_ref} 字）")
    print(f"CER_norm  （信息听错）: {ov_n * 100:.1f}%   （{e_norm} 处 / {n_ref} 字）")
    if e_strict - e_norm > n_ref * 0.02:
        print(f"  → 两者差 {(e_strict - e_norm) / n_ref * 100:.1f} 个百分点："
              "这部分是**格式差异**（如「二零二六年」写成「2026年」），不是听错。")
        print("    对笔记场景这通常是**好事**（可搜索），不必算作缺陷。")
    print(f"完全正确        : {exact_n}/{len(ok)} 段 ({exact_n / len(ok) * 100:.0f}%)")
    print(f"平均实时率 RTF  : {sum(r['rtf'] for r in ok) / len(ok):.2f}  （<1 表示比实时快）")

    by_cat: dict[str, list] = {}
    for r in ok:
        by_cat.setdefault(r["category"], []).append(r)
    print("\n分类别：")
    for cat, rs in sorted(by_cat.items()):
        n = sum(cer(r["ref"], r["hyp"])["ref_len"] for r in rs)
        es = sum(cer(r["ref"], r["hyp"])["edits"] for r in rs)
        en = sum(cer(r["ref"], r["hyp"], numerals=True)["edits"] for r in rs)
        ex = sum(1 for r in rs if r["exact"])
        print(f"  {cat:<11} strict={es / n * 100:5.1f}%  norm={en / n * 100:5.1f}%  完全正确 {ex}/{len(rs)}")

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
                {"endpoint": args.endpoint,
                 "cer_strict": ov_s, "cer_norm": ov_n,
                 "exact": exact_n, "n": len(ok), "rows": rows},
                f, ensure_ascii=False, indent=2)
        print(f"\n明细: {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
